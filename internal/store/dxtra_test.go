package store

import (
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"iidx-tracker/internal/eamuse"
)

func binNode(name string, data []byte) *eamuse.Node {
	return &eamuse.Node{Name: name, Attrs: []eamuse.Attr{{Name: "__type", Value: "bin"}}, Text: hex.EncodeToString(data)}
}

// customPlayNode builds the <custom_play> tracker_link.dll adds to pc.save for a play on a 2dxtra
// chart, with its music_play_log block laid out as the game fills it.
func customPlayNode(hash string, ended int64) *eamuse.Node {
	log := make([]byte, logSize)
	copy(log, "12345678")
	put32 := func(off int, v int32) { binary.LittleEndian.PutUint32(log[off:], uint32(v)) }
	for off, v := range map[int]int32{0x10: 1001, 0x14: 0, 0x18: 3, 0x20: 1500, 0x24: 700, 0x28: 100, 0x2c: 4,
		0x30: 10, 0x34: 2, 0x38: 0, 0x3c: 21, 0x40: 1, 0x44: 0, 0x58: 20, logEndTime: int32(ended), logProgress: 10000} {
		put32(off, v)
	}
	binary.LittleEndian.PutUint64(log[0x48:], 0x100080)
	binary.LittleEndian.PutUint64(log[logRanArrange:], uint64(0xffffffffffffffff)) // -1: no lane ticket
	for i := 0; i < 14*8; i++ {
		put32(logChatter+i*4, int32(i%3))
	}
	p := &eamuse.Node{Name: "custom_play"}
	for k, v := range map[string]string{"chart_id": hash, "notes": "900", "side": "0", "mid": "1001", "clid": "3",
		"style": "0", "ex": "1500", "lamp": "4", "dj": "2", "miss": "10", "kinds": "700 100 50 5 5",
		"combo": "3", "fast": "20", "slow": "30"} {
		p.Set(k, v)
	}
	ghost := make([]byte, 64)
	for i := range ghost {
		ghost[i] = 20
	}
	p.Children = []*eamuse.Node{binNode("log", log), binNode("ghost", ghost), binNode("gauge", make([]byte, 768)),
		binNode("judge", make([]byte, 88)), binNode("lane", make([]byte, 384))}
	return p
}

func pcSaveCall(plays ...*eamuse.Node) *Call {
	req := &eamuse.Node{Name: "IIDX33pc"}
	req.Set("method", "save")
	req.Set("iidxid", "12345678")
	link := &eamuse.Node{Name: "tracker_link", Children: plays}
	link.Set("ver", "1.1.0")
	return &Call{Model: "LDJ:J:D:S:2026081900", Module: "IIDX33pc", Method: "save", Req: req, Link: link,
		Upstream: "http://10.0.0.5:8083", TS: 1790000999}
}

func TestCustomPlay(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.Ingest(pcSaveCall(customPlayNode("abc", 1790000100))); err != nil {
		t.Fatal(err)
	}
	// the same pc.save again (a retry) adds nothing
	if err := st.Ingest(pcSaveCall(customPlayNode("abc", 1790000100))); err != nil {
		t.Fatal(err)
	}
	rows, err := st.Rows("SELECT * FROM plays")
	if err != nil || len(rows) != 1 {
		t.Fatal(len(rows), err)
	}
	p := rows[0]
	want := map[string]any{"chart_set": unknownSet, "chart_hash": "abc", "chart_notes": int64(900),
		"iidx_id": int64(12345678), "music_id": int64(1001), "chart": int64(3), "played_at": int64(1790000100),
		"clear": int64(4), "ex_score": int64(1500), "pgreat": int64(700), "great": int64(100), "good": int64(50),
		"bad": int64(5), "poor": int64(5), "combo_break": int64(3), "fast": int64(20), "slow": int64(30),
		"miss_count": int64(10), "dj_level": int64(2), "gauge_type": int64(0), "option1": int64(0x100080),
		"ran_arrange": int64(-1), "play_style": int64(0), "play_side": int64(0), "progress": int64(10000),
		"is_death": int64(0)}
	for k, v := range want {
		if p[k] != v {
			t.Errorf("%s = %v (%T), want %v", k, p[k], p[k], v)
		}
	}
	if c := strings.Fields(str(p["chatter"])); len(c) != 112 || c[1] != "1" || c[2] != "2" {
		t.Error("chatter", p["chatter"])
	}
	if g, _ := p["ghost"].([]byte); len(g) != 64 || g[0] != 20 {
		t.Error("ghost", g)
	}
	if lanes := strings.Fields(str(p["judge_lanes"])); len(lanes) != 96 {
		t.Error("lanes", p["judge_lanes"])
	}
	// the 2dxtra chart's notes say nothing about the game's chart
	if n, _ := st.Row("SELECT COUNT(*) AS n FROM chart_notes_db"); n["n"] != int64(0) {
		t.Error("observed notes", n)
	}

	// kept apart from the game's charts, listed under its set with its own note count
	key := "iidx:12345678"
	if b, err := st.bests(key, nil, ""); err != nil || len(b) != 0 {
		t.Fatal("the game's charts got a 2dxtra play:", b, err)
	}
	list, err := st.ChartRows(key, "", 0, "", "", nil, unknownSet)
	if err != nil || len(list) != 1 || list[0]["best_ex"] != int64(1500) || list[0]["notes"] != int64(900) {
		t.Fatal(list, err)
	}
	chart, err := st.Chart(key, nil, 1001, 3, unknownSet)
	if err != nil || chart["notes"] != int64(900) || len(chart["plays"].([]Row)) != 1 {
		t.Fatal(chart, err)
	}
	if plain, _ := st.Chart(key, nil, 1001, 3, ""); len(plain["plays"].([]Row)) != 0 {
		t.Fatal("the game's chart page shows a 2dxtra play")
	}
	graphs, err := st.PlayGraphs(p["id"].(int64))
	if err != nil || graphs.(map[string]any)["graph_type"] != int64(20) {
		t.Fatal(graphs, err)
	}
}

// make2dxtraDB writes a 2dxtra.sqlite with its tables (2dxtra database.cc) and a few charts.
func make2dxtraDB(t *testing.T, charts map[string][4]int64) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "2dxtra.sqlite")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE chart_set (id INTEGER PRIMARY KEY, name TEXT NOT NULL UNIQUE);
		CREATE TABLE original_charts (music_id INTEGER NOT NULL, difficulty INTEGER NOT NULL, hash TEXT NOT NULL,
		  PRIMARY KEY (music_id, difficulty));
		CREATE TABLE charts (chart_set INTEGER NOT NULL, music_id INTEGER NOT NULL, difficulty INTEGER NOT NULL,
		  hash TEXT NOT NULL, notes INTEGER NOT NULL, radar_notes INTEGER, radar_peak INTEGER, radar_scratch INTEGER,
		  radar_soflan INTEGER, radar_charge INTEGER, radar_chord INTEGER, data BLOB NOT NULL,
		  PRIMARY KEY (chart_set, music_id, difficulty));
		INSERT INTO chart_set (id, name) VALUES (0, 'Kiraku'), (1, 'Kichiku'), (2, 'All-Scratch');`); err != nil {
		t.Fatal(err)
	}
	for hash, c := range charts { // set, music, difficulty, notes
		if _, err := db.Exec("INSERT INTO charts VALUES (?, ?, ?, ?, ?, 1, 2, 3, 4, 5, 6, x'00')",
			c[0], c[1], c[2], hash, c[3]); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func TestImportChartSets(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	// a play recorded before the import is filed under "2dxtra" until then
	if err := st.Ingest(pcSaveCall(customPlayNode("abc", 1790000100))); err != nil {
		t.Fatal(err)
	}
	path := make2dxtraDB(t, map[string][4]int64{"abc": {1, 1001, 3, 900}, "def": {2, 1001, 3, 1200}, "ghi": {0, 1002, 8, 700}})
	res, err := st.ImportChartSets(`"` + path + `"`) // pasted with quotes
	if err != nil || res["charts"] != 3 || res["plays"] != int64(1) {
		t.Fatal(res, err)
	}
	if set, _ := st.Row("SELECT chart_set FROM plays"); set["chart_set"] != "Kichiku" {
		t.Fatal(set)
	}
	sets, err := st.ChartSets()
	if err != nil || len(sets) != 3 {
		t.Fatal(sets, err)
	}
	var names []string
	for _, s := range sets {
		names = append(names, str(s["name"])+":"+strconv.FormatInt(orZero(s["charts"]), 10)+":"+strconv.FormatInt(orZero(s["plays"]), 10))
	}
	if strings.Join(names, " ") != "Kiraku:1:0 Kichiku:1:1 All-Scratch:1:0" {
		t.Fatal(names)
	}
	// a set's list shows its charts, played or not, with their own note counts
	list, err := st.ChartRows("", "DP", 0, "", "", nil, "Kiraku")
	if err != nil || len(list) != 1 || list[0]["music_id"] != int64(1002) || list[0]["notes"] != int64(700) {
		t.Fatal(list, err)
	}
	if r, _ := st.Row("SELECT radar FROM custom_charts WHERE hash = 'ghi'"); r["radar"] != "1 2 3 4 5 6" {
		t.Fatal(r)
	}
	if _, err := st.ImportChartSets(filepath.Join(t.TempDir(), "missing.sqlite")); err == nil {
		t.Fatal("a missing file was accepted")
	}
}

// A 2dxtra set's notes radar, computed the way the game does from the set's bests.
func TestSetRadar(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	chart := func(set string, music, chart, notes int64, radar string) {
		if _, err := st.Exec("INSERT INTO custom_charts VALUES (?, 1, ?, ?, ?, ?, ?, 0)",
			set, music, chart, fmt.Sprintf("%s-%d-%d", set, music, chart), notes, radar); err != nil {
			t.Fatal(err)
		}
	}
	play := func(set string, music, chart, ex int64) {
		if _, err := st.Exec("INSERT INTO plays (card_id, music_id, chart, ex_score, chart_set, played_at) VALUES ('C1', ?, ?, ?, ?, 1)",
			music, chart, ex, set); err != nil {
			t.Fatal(err)
		}
	}
	// the credit of 2026-09-27: one Kichiku chart, all PGREAT - the game saved 2000 1642 706 0 0 2000
	chart("Kichiku", 33051, 3, 2562, "20000 16420 7060 0 0 20000")
	play("Kichiku", 33051, 3, 5124)
	// the ten best charts, one per song; over 20000 counts as nothing; a half score halves the value
	for i := int64(1); i <= 11; i++ {
		chart("Kiraku", 1000+i, 3, 100, fmt.Sprintf("%d 0 0 0 0 0", 1000*i))
		play("Kiraku", 1000+i, 3, 200)
	}
	chart("Kiraku", 1011, 2, 100, "15000 0 0 0 0 0") // the song's other chart counts instead
	play("Kiraku", 1011, 2, 200)
	chart("Kiraku", 1012, 3, 100, "30000 0 0 0 0 0")
	play("Kiraku", 1012, 3, 200)
	chart("Kiraku", 1013, 8, 100, "8000 0 0 0 0 4000") // DP, half the EX
	play("Kiraku", 1013, 8, 100)
	for set, want := range map[string]map[string][6]int64{
		"Kichiku": {"SP": {2000, 1642, 706, 0, 0, 2000}},
		"Kiraku":  {"SP": {(15000 + 10000 + 9000 + 8000 + 7000 + 6000 + 5000 + 4000 + 3000 + 2000) / 10}, "DP": {400, 0, 0, 0, 0, 200}},
	} {
		got, err := st.SetRadar("C1", nil, set)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Errorf("%s: %v %v, want %v", set, got, err, want)
		}
	}
}

// The notes radar a credit saves belongs to the chart set of its last play.
func TestRadarSource(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.Exec("INSERT INTO sessions (upstream, iidx_id, started_at) VALUES ('http://10.0.0.5:8083', 12345678, ?)", Now()); err != nil {
		t.Fatal(err)
	}
	call := pcSaveCall(customPlayNode("abc", 1790000100))
	radar := &eamuse.Node{Name: "notes_radar", Children: []*eamuse.Node{{Name: "radar_score", Text: "2000 1642 706 0 0 2000"}}}
	radar.Set("style", "0")
	call.Req.Children = append(call.Req.Children, radar)
	if err := st.Ingest(call); err != nil {
		t.Fatal(err)
	}
	p, err := st.Player("iidx:12345678", "")
	if err != nil {
		t.Fatal(err)
	}
	s := p["sessions"].([]Row)[0]
	if s["radar_sp"] != "2000 1642 706 0 0 2000" || s["radar_sp_set"] != unknownSet || s["radar_dp_set"] != nil {
		t.Fatal(s)
	}
}
