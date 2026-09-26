package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"io"
	"io/fs"
	"math"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"iidx-tracker/internal/eamuse"
	"iidx-tracker/internal/store"
)

// Runs the real proxy between a real client and a fake e-amusement server, using traffic
// captured from IIDX 33 (card and IDs replaced), and checks what got recorded.

const (
	model = "LDJ:J:D:S:2026081900"
	card  = "E004000000000001"
)

func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func lz77Literals(data []byte) []byte {
	var out []byte
	for i := 0; i < len(data); i += 8 {
		chunk := data[i:min(i+8, len(data))]
		out = append(out, byte(1<<len(chunk)-1))
		out = append(out, chunk...)
	}
	return append(out, 0, 0, 0)
}

func wrap(t *testing.T, xml, info string, lz77 bool) ([]byte, http.Header) {
	t.Helper()
	root, err := eamuse.ParseXML([]byte(xml))
	if err != nil {
		t.Fatal(err)
	}
	body, err := eamuse.EncodeBinary(root)
	if err != nil {
		t.Fatal(err)
	}
	h := http.Header{}
	h.Set("X-Eamuse-Info", info)
	h.Set("X-Compress", "none")
	if lz77 {
		body = lz77Literals(body)
		h.Set("X-Compress", "lz77")
	}
	body, _ = eamuse.Crypt(info, body)
	return body, h
}

// upstreamSaw keeps the requests the fake server received (as XML), to check what was relayed.
var upstreamSaw struct {
	sync.Mutex
	xml []string
}

// fakeServer plays the e-amusement server, answering from the captured responses.
func fakeServer(t *testing.T) *httptest.Server {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		doc, _, err := eamuse.Decode(body, r.Header.Get("X-Eamuse-Info"), r.Header.Get("X-Compress"))
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		upstreamSaw.Lock()
		upstreamSaw.xml = append(upstreamSaw.xml, string(doc.XML()))
		upstreamSaw.Unlock()
		req := doc.Children[0]
		key := req.Name + "." + req.Get("method")
		var xml string
		switch key {
		case "services.get":
			xml = `<response><services expire="10800" method="get" mode="operation" status="0">` +
				`<item name="ntp" url="ntp://pool.ntp.org/"/>` +
				`<item name="keepalive" url="http://127.0.0.1/core/keepalive?pa=127.0.0.1"/>` +
				fmt.Sprintf(`<item name="cardmng" url="%s"/><item name="local" url="%s/core"/>`, srv.URL, srv.URL) +
				`</services></response>`
		case "IIDX33pc.get":
			xml = fixture(t, "pcget_resp.xml")
		case "IIDX33music.getrank":
			xml = fixture(t, "getrank_resp.xml")
		default:
			xml = fmt.Sprintf(`<response><%s status="0"/></response>`, req.Name)
		}
		out, h := wrap(t, xml, "1-12345678-abcd", key == "IIDX33pc.get")
		for k, v := range h {
			w.Header()[k] = v
		}
		w.Write(out)
	}))
	return srv
}

func post(t *testing.T, base, path, xml string) (int, http.Header, []byte) {
	t.Helper()
	body, h := wrap(t, xml, "1-11111111-2222", false)
	req, _ := http.NewRequest("POST", base+path, bytes.NewReader(body))
	req.Header = h
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, resp.Header, data
}

func serviceURLs(t *testing.T, h http.Header, body []byte) map[string]string {
	t.Helper()
	doc, _, err := eamuse.Decode(body, h.Get("X-Eamuse-Info"), h.Get("X-Compress"))
	if err != nil {
		t.Fatal(err)
	}
	urls := map[string]string{}
	for _, it := range doc.Iter("item") {
		urls[it.Get("name")] = it.Get("url")
	}
	return urls
}

func TestPrivateHosts(t *testing.T) {
	for _, h := range []string{"localhost", "127.0.0.1", "10.0.0.5", "172.16.0.1", "192.168.1.5", "::1", "fd00::1"} {
		if !isPrivateHost(h) {
			t.Error("should be private:", h)
		}
	}
	for _, h := range []string{"8.8.8.8", "172.32.0.1", "example.com", "10.evil.com", ""} {
		if isPrivateHost(h) {
			t.Error("should not be private:", h)
		}
	}
}

func TestServiceHost(t *testing.T) {
	cases := [][4]any{
		{"127.0.0.1", 8084, "", "127.0.0.1:8084"},
		{"192.168.1.5", 8084, "", "192.168.1.5:8084"},        // game on another PC
		{"::ffff:192.168.1.5", 8084, "", "192.168.1.5:8084"}, // dual-stack socket
		{"::1", 8084, "", "[::1]:8084"},
		{"127.0.0.1", 8084, "tracker.lan", "tracker.lan:8084"}, // advertise_host
		{"127.0.0.1", 8084, "203.0.113.5:9000", "203.0.113.5:9000"},
	}
	for _, c := range cases {
		if got := serviceHost(c[0].(string), c[1].(int), c[2].(string)); got != c[3] {
			t.Errorf("serviceHost(%v) = %s", c, got)
		}
	}
}

func TestEndToEnd(t *testing.T) {
	up := fakeServer(t)
	defer up.Close()
	tmp := t.TempDir()
	config := defaultConfig()
	config["upstream"], config["db_path"] = up.URL, filepath.Join(tmp, "t.db")
	static, _ := fs.Sub(embedded, "static")
	app, err := newApp(filepath.Join(tmp, "config.json"), config, tmp, static)
	if err != nil {
		t.Fatal(err)
	}
	defer app.store.Close() // before TempDir cleanup: Windows cannot delete an open database
	proxy := httptest.NewServer(app)
	defer proxy.Close()
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(proxy.URL, "http://"))

	// services.get: every http service must now point back at the tracker
	status, h, body := post(t, proxy.URL, "/core?model="+model+"&f=services.get",
		`<call model="`+model+`"><services method="get"/></call>`)
	if status != 200 {
		t.Fatal(status, string(body))
	}
	urls := serviceURLs(t, h, body)
	if urls["ntp"] != "ntp://pool.ntp.org/" || !strings.HasPrefix(urls["keepalive"], "http://127.0.0.1/core/keepalive") ||
		urls["cardmng"] != "http://127.0.0.1:"+port+"/fwd/1" || urls["local"] != "http://127.0.0.1:"+port+"/fwd/1/core" {
		t.Fatal(urls)
	}
	for _, name := range []string{"pcget_req.xml", "getrank_req.xml", "musicreg_req.xml", "pcsave_req.xml"} {
		if status, _, body := post(t, proxy.URL, "/fwd/1/core?model="+model, fixture(t, name)); status != 200 {
			t.Fatal(name, status, string(body))
		}
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		app.mu.Lock()
		n := app.stats["recorded"].(int)
		app.mu.Unlock()
		if n == 4 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if stat(app, "recorded") != 4 {
		t.Fatal(app.stats)
	}

	s := app.store
	prof, _ := s.Row("SELECT * FROM profiles")
	if prof["card_id"] != card || prof["name"] != "DJTEST" || prof["pid"] != int64(13) || prof["sgid"] != int64(16) ||
		prof["iidx_id"] != int64(12345678) {
		t.Fatal(prof)
	}
	sess, _ := s.Row("SELECT * FROM sessions")
	if sess["cabinet"] != "TDJ" || sess["omni"] != int64(1) || sess["game_version"] != int64(33) ||
		strings.Fields(sess["radar_sp"].(string))[0] != "18824" || sess["arena_sp"] != int64(19) {
		t.Fatal(sess)
	}
	// DJ POINT: pc.get had sach 4598 / dach 192, pc.save sent the recomputed s_achi 4697 / d_achi 192
	if sess["djpoint_sp"] != int64(4697) || sess["djpoint_dp"] != int64(192) {
		t.Fatal(sess["djpoint_sp"], sess["djpoint_dp"])
	}
	play, _ := s.Row("SELECT * FROM plays")
	expect := map[string]any{"card_id": card, "music_id": 18032, "chart": 3, "level": 12, "clear": 7,
		"ex_score": 3438, "pgreat": 1719, "great": 0, "good": 0, "bad": 0, "poor": 0,
		"combo_break": 0, "fast": 0, "slow": 0, "miss_count": 0, "progress": 10000,
		"option1": 1048704, "gauge_type": 4, "ran_arrange": -1, "cabinet": "TDJ",
		"prev_best_score": 0, "prev_best_miss": -1, "omni": 1, "mode_type": 6}
	for k, v := range expect {
		if want, isInt := v.(int); isInt {
			v = int64(want)
		}
		if play[k] != v {
			t.Errorf("plays.%s = %v, want %v", k, play[k], v)
		}
	}
	shown, _ := s.PlaysFor("1", nil, "")
	if shown[0]["options"] != "OFF" || shown[0]["gauge"] != "NORMAL" || shown[0]["mode"] != "PREMIUM FREE" {
		t.Fatal(shown[0])
	}
	if n, src, _ := s.Notes(18032, 3, nil); n != 1719 || src != "observed" {
		t.Fatal(n, src)
	}

	// getrank: the player's own server bests (rival rows ignored), merged with recorded plays
	sb, _ := s.Rows("SELECT * FROM server_bests ORDER BY music_id")
	if len(sb) != 2 || sb[0]["music_id"] != int64(1001) || sb[0]["chart"] != int64(1) || sb[0]["card_id"] != card ||
		sb[1]["clear"] != int64(5) || sb[1]["ex_score"] != int64(3000) || sb[1]["miss_count"] != int64(4) {
		t.Fatal(sb)
	}
	rows, _ := s.ChartRows(card, "", 0, "", "", nil)
	byChart := map[[2]int64]map[string]any{}
	for _, r := range rows {
		byChart[[2]int64{r["music_id"].(int64), r["chart"].(int64)}] = r
	}
	if r := byChart[[2]int64{18032, 3}]; r["best_ex"] != int64(3438) || r["best_clear"] != int64(7) || r["best_miss"] != int64(0) || r["plays"] != int64(1) {
		t.Fatal(r)
	}
	if r := byChart[[2]int64{1001, 1}]; r["best_ex"] != int64(1200) || r["best_clear"] != int64(3) || r["best_miss"] != int64(10) || r["plays"] != int64(0) {
		t.Fatal(r)
	}

	g, _ := s.PlayGraphs(play["id"].(int64))
	graphs := g.(map[string]any)
	ghost := graphs["ghost"].([]int)
	sum := 0
	for _, v := range ghost {
		sum += v
	}
	gauge := graphs["gauge"].([]float64)
	maxGauge := 0.0
	for _, v := range gauge {
		maxGauge = max(maxGauge, v)
	}
	if sum != 3438 || len(ghost) != 64 || gauge[0] != 22 || maxGauge != 100 {
		t.Fatal(sum, len(ghost), gauge[:3], maxGauge)
	}
	if graphs["notes"] != int64(1719) || graphs["notes_source"] != "play" || graphs["graph_type"] != int64(27) ||
		graphs["target_score"] != int64(3247) || graphs["folder_type"] != int64(94) {
		t.Fatal(graphs["notes"], graphs["notes_source"], graphs["graph_type"], graphs["target_score"], graphs["folder_type"])
	}
	zeros := make([][]int64, 14)
	for i := range zeros {
		zeros[i] = make([]int64, 8)
	}
	if sizes := graphs["bucket_notes"].([]int); sizes[0] != 26 || sizes[1] != 27 || !reflect.DeepEqual(graphs["chatter"], zeros) {
		t.Fatal(sizes[:2], graphs["chatter"])
	}
	if len(strings.Fields(play["chatter"].(string))) != 112 {
		t.Fatal(play["chatter"])
	}

	// DJ POINT: FULL COMBO + AAA = 150 per EX; the server-only chart has no note count, so no DJ LEVEL
	if shown[0]["djpoint"] != int64(3438*150) {
		t.Fatal(shown[0]["djpoint"])
	}
	sp, _ := s.ChartRows(card, "SP", 0, "", "", nil)
	if total := store.DjPointTotal(sp); !reflect.DeepEqual(total, map[string]any{"total": int64(3438 * 150 / 10000), "songs": 1, "unknown": 1}) {
		t.Fatal(total)
	}
	chart, _ := s.Chart(card, nil, 18032, 3)
	sec := chart["sections"].(map[string]any)
	maxLost := 0.0
	for _, v := range sec["lost"].([]float64) {
		maxLost = max(maxLost, v)
	}
	if sec["plays"] != 1 || sec["notes"] != int64(1719) || maxLost != 0 {
		t.Fatal(sec)
	}
	if cs, _ := s.ChatterSummary(card, 100); !reflect.DeepEqual(cs, map[string]any{"plays": 1, "counts": zeros}) {
		t.Fatal(cs)
	}

	// the web API answers with the same data
	resp, err := http.Get(proxy.URL + "/api/player?key=" + card)
	if err != nil || resp.StatusCode != 200 {
		t.Fatal(err, resp.StatusCode)
	}
	resp.Body.Close()
	if resp, _ := http.Get(proxy.URL + "/ui/"); resp.StatusCode != 200 || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/html") {
		t.Fatal(resp.StatusCode, resp.Header)
	}

	// omnimix must not be relayed to a public server (refused before connecting)
	setUpstream(app, "http://203.0.113.9:8083")
	if status, _, _ := post(t, proxy.URL, "/?model="+model, fixture(t, "pcget_req.xml")); status != 403 || stat(app, "refused") != 1 {
		t.Fatal(status, app.stats)
	}
	setUpstream(app, up.URL)

	// the rewritten services follow the address the game used (a second loopback address stands
	// in for a LAN address), not a fixed 127.0.0.1
	ln, err := net.Listen("tcp4", "127.0.0.2:0")
	if err != nil {
		t.Log("127.0.0.2 not available, skipped the second-address check")
		return
	}
	other := httptest.NewUnstartedServer(app)
	other.Listener.Close()
	other.Listener = ln
	other.Start()
	defer other.Close()
	_, port2, _ := net.SplitHostPort(ln.Addr().String())
	status, h, body = post(t, other.URL, "/core?model="+model+"&f=services.get", `<call model="`+model+`"><services method="get"/></call>`)
	if urls := serviceURLs(t, h, body); status != 200 || urls["local"] != "http://127.0.0.2:"+port2+"/fwd/1/core" {
		t.Fatal(status, urls)
	}
	// an upstream that is the tracker itself is refused instead of looping
	setUpstream(app, "http://localhost:"+port2)
	if status, _, _ := post(t, other.URL, "/?model="+model, fixture(t, "pcget_req.xml")); status != 508 {
		t.Fatal(status)
	}
}

func setUpstream(a *App, u string) {
	a.mu.Lock()
	a.config["upstream"] = u
	a.mu.Unlock()
}

func stat(a *App, key string) any {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.stats[key]
}

// mdb33 builds a small version-33 music_omni.bin with the given song IDs.
func mdb33(ids ...int) []byte {
	head := []byte("IIDX")
	head = binary.LittleEndian.AppendUint32(head, 33)
	head = binary.LittleEndian.AppendUint16(head, uint16(len(ids)))
	head = binary.LittleEndian.AppendUint16(head, 0)
	head = binary.LittleEndian.AppendUint32(head, 0)
	for _, id := range ids {
		e := make([]byte, 0x7F8)
		copy(e, []byte{'S', 0, 'o', 0, 'n', 0, 'g', 0})
		binary.LittleEndian.PutUint16(e[0x3DC:], 33)
		e[0x3EC+3] = 12 // SPA level 12
		binary.LittleEndian.PutUint32(e[0x67C:], uint32(id))
		head = append(head, e...)
	}
	return head
}

// altfix reports the music data file the game loaded; the cabinet's plays then belong to it.
func TestTrackerLink(t *testing.T) {
	up := fakeServer(t)
	defer up.Close()
	tmp := t.TempDir()
	config := defaultConfig()
	config["upstream"], config["db_path"] = up.URL, filepath.Join(tmp, "t.db")
	static, _ := fs.Sub(embedded, "static")
	app, err := newApp(filepath.Join(tmp, "config.json"), config, tmp, static)
	if err != nil {
		t.Fatal(err)
	}
	defer app.store.Close()
	proxy := httptest.NewServer(app)
	defer proxy.Close()
	upstreamSaw.Lock()
	upstreamSaw.xml = nil
	upstreamSaw.Unlock()

	// services.get carries the music data file the game loads (the PCBID is the call's srcid)
	file := mdb33(18032, 33999)
	services := fmt.Sprintf(`<call model="%s" srcid="00010203040506070809"><services method="get">`+
		`<tracker_link ver="t" name="music_omni.bin" size="%d" sha256="%x"/></services></call>`,
		model, len(file), sha256.Sum256(file))
	if status, _, _ := post(t, proxy.URL, "/", services); status != 200 {
		t.Fatal(status)
	}
	machine, _ := app.store.Row("SELECT musicdb_id, filename, altfix FROM machines WHERE pcbid = '00010203040506070809'")
	if machine == nil || machine["musicdb_id"] != nil || machine["filename"] != "music_omni.bin" || machine["altfix"] != "tracker_link t" {
		t.Fatal(machine) // known cabinet, file not imported yet
	}
	res, err := app.store.ImportMusicDB("music_omni.bin", file, "auto", "")
	if err != nil {
		t.Fatal(err)
	}

	// music.reg carries the play's judgments by timing and by key
	judge, lane, measure := make([]byte, 88), make([]byte, 384), make([]byte, 8)
	binary.LittleEndian.PutUint32(judge[4*4:], 1719)                      // 1P side, PGREAT
	binary.LittleEndian.PutUint32(lane, 245)                              // 1P side, key 1, PGREAT
	binary.LittleEndian.PutUint32(measure, math.Float32bits(-1))          // no notes
	binary.LittleEndian.PutUint32(measure[4:], math.Float32bits(0.96875)) // 96.875%
	reg := strings.Replace(fixture(t, "musicreg_req.xml"), "</IIDX33music>", fmt.Sprintf(`<tracker_link ver="t">`+
		`<judge __type="bin" __size="88">%x</judge><lane __type="bin" __size="384">%x</lane>`+
		`<measure __type="bin" __size="8">%x</measure></tracker_link></IIDX33music>`, judge, lane, measure), 1)
	for _, xml := range []string{fixture(t, "pcget_req.xml"), reg} {
		if status, _, _ := post(t, proxy.URL, "/?model="+model, xml); status != 200 {
			t.Fatal(status)
		}
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && stat(app, "recorded") != 2 {
		time.Sleep(50 * time.Millisecond)
	}
	play, _ := app.store.Row("SELECT musicdb_id, pcbid, judge_timing, judge_lanes, judge_measures FROM plays")
	if play == nil || play["musicdb_id"] != res["musicdb_id"] || play["pcbid"] != "00010203040506070809" ||
		!strings.HasPrefix(fmt.Sprint(play["judge_timing"]), "0 0 0 0 1719 0") ||
		!strings.HasPrefix(fmt.Sprint(play["judge_lanes"]), "245 0 0") || play["judge_measures"] != "-1.0000 0.9688" {
		t.Fatal(play, res)
	}

	// the server upstream got both requests, without the element
	upstreamSaw.Lock()
	defer upstreamSaw.Unlock()
	relayed := strings.Join(upstreamSaw.xml, "\n")
	if strings.Contains(relayed, "tracker_link") || !strings.Contains(relayed, `method="reg"`) ||
		!strings.Contains(relayed, `method="get"`) {
		t.Fatal(relayed)
	}
}

// Every Japanese text the UI shows through t() (and the fixed text of index.html, and the Japanese
// names the server sends) needs an English entry in static/i18n.js.
func TestUIEnglish(t *testing.T) {
	read := func(name string) string {
		b, err := embedded.ReadFile("static/" + name)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	app, page, dict := read("app.js"), read("index.html"), read("i18n.js")
	en := map[string]bool{}
	for _, m := range regexp.MustCompile(`(?m)^\s*"((?:[^"\\]|\\.)*)":`).FindAllStringSubmatch(dict[strings.Index(dict, "const EN = {"):], -1) {
		en[m[1]] = true
	}
	var keys []string
	for _, m := range regexp.MustCompile(`\bt\("((?:[^"\\]|\\.)*)"`).FindAllStringSubmatch(app, -1) {
		keys = append(keys, m[1])
	}
	for _, m := range regexp.MustCompile(`data-i18n>([^<]*)<`).FindAllStringSubmatch(page, -1) {
		keys = append(keys, strings.TrimSpace(m[1]))
	}
	for _, m := range regexp.MustCompile(`title="([^"]*)" data-i18n-title`).FindAllStringSubmatch(page, -1) {
		keys = append(keys, m[1])
	}
	for _, name := range store.Modes {
		if strings.IndexFunc(name, func(r rune) bool { return r > 0x7f }) >= 0 {
			keys = append(keys, name)
		}
	}
	dan := int64(3)
	for g := int64(0); g < 2; g++ {
		keys = append(keys, store.GaugeName(&g, &dan).(string))
	}
	if len(keys) < 200 {
		t.Fatalf("only %d texts found: did the markup change?", len(keys))
	}
	for _, k := range keys {
		if !en[k] {
			t.Errorf("no English for %q", k)
		}
	}
}
