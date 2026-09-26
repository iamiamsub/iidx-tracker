package store

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"unicode/utf16"

	"iidx-tracker/internal/eamuse"
	"iidx-tracker/internal/sound"
)

func i64(v int64) *int64 { return &v }

func bits(n ...int) *int64 {
	var v int64
	for _, b := range n {
		v |= 1 << b
	}
	return &v
}

func TestOptions(t *testing.T) {
	// the captured SP play: floating hi-speed step 7 (shown as 8), normal gauge, nothing else
	o := unpackOptions(i64(0x100080))
	if o.hs != 7 || o.classicHS != 0 || o.gauge != "NORMAL" || o.random != "" {
		t.Fatalf("%+v", o)
	}
	cases := []struct {
		o1, o2, style *int64
		want          string
	}{
		{i64(0x100080), i64(0), i64(0), "OFF"},
		{bits(5, 20, 39, 37, 34, 43), i64(0), i64(0), "RANDOM, HARD, SUD+, A-SCR"},
		{bits(40, 42, 50), i64(0), i64(0), "R-RANDOM, MIRROR, LEGACY"},
		{bits(41, 38, 49, 34), i64(0), i64(0), "S-RANDOM, EX-HARD, LIFT & SUD+"},
		{bits(35, 32, 34), i64(0), i64(0), "A-EASY, SUD+ & HID+"},
		// DP: left/right per value; SYNC/SYMM = BATTLE + RANDOM both sides + MIRROR on one/both
		{i64(0x100001), i64(0x100001), i64(1), "OFF"},
		{bits(39, 45), bits(42, 45), i64(1), "RAN/MIR, FLIP"},
		{bits(46, 39, 42), bits(46, 39), i64(1), "BATTLE, SYNC-RAN"},
		{bits(46, 39, 42), bits(46, 39, 42), i64(1), "BATTLE, SYMM-RAN"},
	}
	for _, c := range cases {
		if got := DescribeOptions(c.o1, c.o2, c.style); got != c.want {
			t.Errorf("DescribeOptions(%x, %x) = %q, want %q", *c.o1, *c.o2, got, c.want)
		}
	}
	if HiSpeed(i64(0x100080)) != "HS 8 / CLASSIC ×1.00" {
		t.Error(HiSpeed(i64(0x100080)))
	}
	if GaugeName(i64(4), i64(6)) != "NORMAL" || GaugeName(i64(6), i64(0)) != "EX-HARD" || GaugeName(i64(1), i64(3)) != "段位 (EX-HARD)" {
		t.Error("gauge names")
	}
}

func TestGameFormulas(t *testing.T) {
	// ghost buckets: the all-PGREAT fixture play (1719 notes) earned exactly 2 EX per note in every bucket
	raw, _ := os.ReadFile(filepath.Join("..", "..", "testdata", "musicreg_req.xml"))
	doc, err := eamuse.ParseXML(raw)
	if err != nil {
		t.Fatal(err)
	}
	ghostHex, _ := doc.FindText("IIDX33music/ghost")
	ghost, _ := hex.DecodeString(strings.TrimSpace(ghostHex))
	sizes := GhostBucketSizes(1719)
	nine := 0
	for i, s := range sizes {
		if int(ghost[i]) != 2*s {
			t.Fatalf("bucket %d: ghost %d, size %d", i, ghost[i], s)
		}
		if s == 26 {
			nine++
		}
	}
	if sizes[0] != 26 || sizes[1] != 27 || nine != 9 {
		t.Fatal(sizes)
	}
	// single precision + 0.0001: when k*64/n is a whole number the note already goes to the next bucket
	sizes = GhostBucketSizes(64)
	if sizes[0] != 0 || sizes[63] != 2 {
		t.Fatal(sizes)
	}
	// DJ LEVEL borders and DJ POINT (x10000)
	if DjLevel(3056, 1719) != 0 || DjLevel(3055, 1719) != 1 || DjLevel(0, 1719) != 7 {
		t.Fatal("dj level")
	}
	if *DjPoint(i64(3438), i64(7), i64(0)) != 3438*150 || *DjPoint(i64(1000), i64(4), i64(3)) != 1000*110 ||
		*DjPoint(i64(0), i64(1), i64(7)) != 0 || DjPoint(i64(100), nil, i64(0)) != nil {
		t.Fatal("dj point")
	}
	// chattering_log: 14 rows (1P keys 1-7, 2P keys 1-7) of 8 counts
	var xml strings.Builder
	xml.WriteString("<music_play_log><chattering_log>")
	for i := 0; i < 14; i++ {
		v := "0 0 0 0 0 0 0 0"
		if i == 3 {
			v = "0 2 0 0 0 0 0 1"
		}
		n := "chattering_" + strconv.Itoa(i)
		xml.WriteString("<" + n + ` __type="s32" __count="8">` + v + "</" + n + ">")
	}
	xml.WriteString("</chattering_log></music_play_log>")
	log, _ := eamuse.ParseXML([]byte(xml.String()))
	values := strings.Fields(chatter(log).(string))
	if len(values) != 112 || values[3*8+1] != "2" || values[3*8+7] != "1" {
		t.Fatal(values)
	}
	empty, _ := eamuse.ParseXML([]byte("<music_play_log/>"))
	if chatter(empty) != nil {
		t.Fatal("empty log")
	}
}

// makeMDB builds a version 33 music_data.bin: header, index (entry number at each song ID, -1
// elsewhere) and entries.
func makeMDB(songs [][4]any) []byte {
	slots := 0
	for _, s := range songs {
		slots = max(slots, s[0].(int)+1)
	}
	index := make([]int32, slots)
	for i := range index {
		index[i] = -1
	}
	for n, s := range songs {
		index[s[0].(int)] = int32(n)
	}
	head := []byte("IIDX")
	head = binary.LittleEndian.AppendUint32(head, 33)
	head = binary.LittleEndian.AppendUint16(head, uint16(len(songs)))
	head = binary.LittleEndian.AppendUint16(head, 0)
	head = binary.LittleEndian.AppendUint32(head, uint32(slots))
	for _, v := range index {
		head = binary.LittleEndian.AppendUint32(head, uint32(v))
	}
	data := head
	for _, s := range songs {
		e := make([]byte, 0x7F8)
		for i, u := range utf16.Encode([]rune(s[2].(string))) {
			binary.LittleEndian.PutUint16(e[2*i:], u)
		}
		binary.LittleEndian.PutUint16(e[0x3DC:], uint16(s[1].(int)))
		for i, l := range s[3].([]int) {
			e[0x3EC+i] = byte(l)
		}
		binary.LittleEndian.PutUint32(e[0x67C:], uint32(s[0].(int)))
		data = append(data, e...)
	}
	return data
}

// altfix's music_omni.bin cannot be read from its header; the song table is found without it.
func TestSealedMusicData(t *testing.T) {
	songs := [][4]any{
		{18032, 33, "Stay my side", []int{0, 3, 7, 12, 0, 0, 3, 7, 12, 0}},
		{1000, 0, "5.1.1.", []int{0, 2, 6, 10, 0, 0, 1, 7, 10, 0}},
		{80001, 80, "inf song", []int{0, 4, 8, 11, 0, 0, 4, 8, 11, 0}},
	}
	plain := makeMDB(songs)
	sealed := append([]byte(nil), plain...)
	for i := 0; i < 16; i++ { // any change that makes the header unreadable
		sealed[i] ^= byte(0xA5 + i)
	}
	md, err := ParseMusicData(sealed)
	if err != nil || !md.Sealed || md.Version != 33 || len(md.Songs) != 3 ||
		md.Songs[0].Text["title"] != "Stay my side" || md.Songs[2].ID != 80001 || md.Songs[1].Levels[3] != 10 {
		t.Fatalf("%+v %v", md, err)
	}
	if md, _ := ParseMusicData(plain); md.Sealed {
		t.Fatal("a readable file taken as sealed")
	}
	if _, err := ParseMusicData(bytes.Repeat([]byte{7}, 50000)); err == nil {
		t.Fatal("junk read as music data")
	}

	// a sealed file is omni whatever its name
	st, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	res, err := st.ImportMusicDB("renamed.bin", sealed, "new", "")
	if err != nil {
		t.Fatal(err)
	}
	if r, _ := st.Row("SELECT kind, game_version FROM musicdbs WHERE id = ?", res["musicdb_id"]); r == nil ||
		r["kind"] != "omni" || r["game_version"] != int64(33) {
		t.Fatal(r)
	}
}

func userVersion(t *testing.T, st *Store) int {
	t.Helper()
	var v int
	if err := st.db.QueryRow("PRAGMA user_version").Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestMigrateAddsColumns(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "old.db")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if v := userVersion(t, st); v != LatestVersion() {
		t.Fatal("new database at version", v)
	}
	// a database from before versions and before 2026-09-26: no version, no DJ POINT columns
	for _, q := range []string{"ALTER TABLE sessions DROP COLUMN djpoint_sp",
		"ALTER TABLE sessions DROP COLUMN djpoint_dp", "PRAGMA user_version = 0"} {
		if _, err := st.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	st.Close()
	if st, err = Open(path); err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	cols, _ := columns(st.db, "sessions")
	if !cols["djpoint_sp"] || !cols["djpoint_dp"] || userVersion(t, st) != LatestVersion() {
		t.Fatal(cols, userVersion(t, st))
	}
	if backups, _ := filepath.Glob(filepath.Join(dir, "old.db.v0-*.bak")); len(backups) != 1 {
		t.Fatal("no backup before the upgrade", backups)
	}
}

func TestMigrationSteps(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "t.db")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.Exec("INSERT INTO categories (name) VALUES ('keep')"); err != nil {
		t.Fatal(err)
	}
	st.Close()

	// a later build with two more steps; the second fails once and is retried on the next open
	saved := migrations
	defer func() { migrations = saved }()
	fail := true
	migrations = append(append([]func(*sql.Tx) error{}, saved...),
		func(tx *sql.Tx) error { _, err := tx.Exec("ALTER TABLE categories ADD COLUMN step2 TEXT"); return err },
		func(tx *sql.Tx) error {
			if _, err := tx.Exec("UPDATE categories SET step2 = 'done'"); err != nil || fail {
				return errors.New("step 3 failed")
			}
			return nil
		})
	if _, err := Open(path); err == nil {
		t.Fatal("a failed step was ignored")
	}
	fail = false
	if st, err = Open(path); err != nil {
		t.Fatal(err)
	}
	var step2 string
	if err := st.db.QueryRow("SELECT step2 FROM categories WHERE name = 'keep'").Scan(&step2); err != nil || step2 != "done" {
		t.Fatal(step2, err)
	}
	if v := userVersion(t, st); v != len(saved)+3 {
		t.Fatal("version", v)
	}
	st.Close()

	// an older build refuses a database a newer one wrote
	migrations = saved
	if _, err := Open(path); err == nil || !strings.Contains(err.Error(), "新しい版") {
		t.Fatal("opened a newer database:", err)
	}
}

func TestMusicDB(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	stay := [4]any{18032, 18, "Stay my side", []int{0, 5, 9, 12, 0, 0, 5, 9, 11, 0}}
	news := [4]any{33001, 33, "new song", []int{0, 3, 7, 10, 0, 0, 3, 7, 10, 0}}
	old := [4]any{1000, 0, "5.1.1.", []int{0, 2, 6, 10, 0, 0, 1, 7, 10, 0}}
	inf := [4]any{80001, 80, "inf song", []int{0, 4, 8, 11, 0, 0, 4, 8, 11, 0}}
	vanilla := makeMDB([][4]any{stay, news})
	omni := makeMDB([][4]any{stay, news, old})
	omni2 := makeMDB([][4]any{stay, news, old, inf})

	if md, _ := ParseMusicData(vanilla); md.Sealed || md.Songs[0].Text["title"] != "Stay my side" || md.Songs[0].Levels[3] != 12 {
		t.Fatalf("%+v", md)
	}
	a, _ := st.ImportMusicDB("music_data.bin", vanilla, "new", "")
	b, _ := st.ImportMusicDB("music_omni.bin", omni, "new", "")
	if a["musicdb_id"] == b["musicdb_id"] {
		t.Fatal("omni and vanilla share a lineage")
	}
	if r, _ := st.Row("SELECT kind FROM musicdbs WHERE id = ?", b["musicdb_id"]); r["kind"] != "omni" {
		t.Fatal(r) // a new database is omni when the file name says so
	}
	// a newer omni build added to its lineage
	c, _ := st.ImportMusicDB("music_omni_v13.bin", omni2, str(b["musicdb_id"]), "")
	if c["musicdb_id"] != b["musicdb_id"] || c["added"] != 1 || c["duplicate"] != false {
		t.Fatal(c)
	}
	if again, _ := st.ImportMusicDB("music_omni_v13.bin", omni2, str(b["musicdb_id"]), ""); again["duplicate"] != true {
		t.Fatal(again)
	}
	// the target is never guessed
	for _, target := range []string{"", "auto", "999"} {
		if _, err := st.ImportMusicDB("music_omni_v14.bin", omni2, target, ""); err == nil {
			t.Fatalf("imported with target %q", target)
		}
	}
	d, _ := st.ImportMusicDB("renamed.bin", vanilla, "new", "手動")
	if d["musicdb_id"] == a["musicdb_id"] || d["musicdb_id"] == b["musicdb_id"] {
		t.Fatal(d)
	}
	onlyOmni, _ := st.CategorySongs("diff:"+str(b["musicdb_id"])+":"+str(a["musicdb_id"]), nil)
	if !reflect.DeepEqual(onlyOmni, map[int64]bool{1000: true, 80001: true}) {
		t.Fatal(onlyOmni)
	}
	if v, _ := st.CategorySongs("v:18", nil); !reflect.DeepEqual(v, map[int64]bool{18032: true}) {
		t.Fatal(v)
	}
	if _, err := st.ImportNotes(999, map[string]any{}); err == nil {
		t.Fatal("unknown database accepted")
	}
	n, _ := st.ImportNotes(a["musicdb_id"].(int64), map[string]any{"18032": map[string]any{"SPA": 1719.0, "SPH": 1000.0}, "bad": 1.0})
	if n != 2 {
		t.Fatal(n)
	}
	if notes, src, _ := st.Notes(18032, 3, nil); notes != 1719 || src != "import" {
		t.Fatal(notes, src)
	}

	// sound folder scans are stored per music database; songs the database lacks are skipped
	omniDB, vanillaDB := b["musicdb_id"], a["musicdb_id"]
	res, err := st.ImportScan(omniDB.(int64), "root", []string{"mod"}, []sound.Song{
		{ID: 18032, Counts: map[int]int64{3: 1800, 1: 900}}, {ID: 99999, Counts: map[int]int64{3: 5}},
		{ID: 1000, Err: "broken"}})
	if err != nil || res["songs"] != 1 || res["charts"] != 2 || res["skipped"] != 1 || res["errors"] != 1 {
		t.Fatal(res, err)
	}
	if _, err := st.ImportScan(12345, "root", nil, nil); err == nil {
		t.Fatal("unknown database accepted")
	}
	// the viewed database's scan wins, then plays/JSON, then another database's scan
	for _, c := range []struct {
		chart int64
		db    any
		want  int64
		src   string
	}{{3, omniDB, 1800, "analysis"}, {3, vanillaDB, 1719, "import"}, {3, nil, 1800, "analysis"}, {1, vanillaDB, 900, "analysis"}} {
		if n, src, _ := st.Notes(18032, c.chart, c.db); n != c.want || src != c.src {
			t.Errorf("chart %d db %v: %d %s, want %d %s", c.chart, c.db, n, src, c.want, c.src)
		}
	}

	// the same music ID can be a different song in another database: titles and note counts
	// stay with their database
	other, _ := st.ImportMusicDB("custom.bin", makeMDB([][4]any{{18032, 33, "Different song",
		[]int{0, 1, 2, 3, 0, 0, 1, 2, 3, 0}}}), "new", "custom")
	if _, _, ok := st.Notes(18032, 1, other["musicdb_id"]); ok {
		t.Fatal("borrowed the note count of a different song")
	}
	m1, _ := st.SongMetas(other["musicdb_id"])
	m2, _ := st.SongMetas(omniDB)
	if m1[18032].Title != "Different song" || m2[18032].Title != "Stay my side" {
		t.Fatal(m1[18032], m2[18032])
	}

	// tracker_link reports the file the game loaded at login; the cabinet's traffic then belongs to
	// its database, omnimix or not as the database is (with 2dxtra the revision is 'E', not 'S')
	const dxModel = "LDJ:J:D:E:2026081900"
	shaOf := func(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }
	if db, err := st.MachineBoot("PCB1", shaOf(omni2), "music_omni.bin", int64(len(omni2)), 1, "v", "g", "127.0.0.1"); err != nil || db != omniDB {
		t.Fatal(db, err)
	}
	if cab := st.CabinetFor("PCB1", dxModel, int64(33)); cab.DB != omniDB || cab.Omni != 1 || cab.Hash != shaOf(omni2) || cab.Dxtra != int64(1) {
		t.Fatal(cab)
	}
	// a later login without a report: the game runs without tracker_link.dll now
	now := Now
	defer func() { Now = now }()
	Now = func() int64 { return now() + 60 }
	if err := st.MachineUnreported("PCB1"); err != nil {
		t.Fatal(err)
	}
	if cab := st.CabinetFor("PCB1", dxModel, int64(33)); cab.DB == omniDB || cab.Omni != 0 || cab.Hash != nil {
		t.Fatal("report used after a login without one", cab)
	}
	custom := makeMDB([][4]any{stay, {33999, 80, "custom omni song", []int{0, 1, 2, 3, 0, 0, 1, 2, 3, 0}}})
	if db, err := st.MachineBoot("PCB2", shaOf(custom), "music_omni.bin", int64(len(custom)), nil, "v", "g", "127.0.0.1"); err != nil || db != nil {
		t.Fatal(db, err) // not imported yet
	}
	if cab := st.CabinetFor("PCB2", dxModel, int64(33)); cab.DB != nil || cab.Omni != 1 || cab.Hash != shaOf(custom) {
		t.Fatal(cab) // no database until the player says which one (the file name tells omnimix)
	}
	// importing the reported file ties the cabinet to it
	res, err = st.ImportMusicDB("music_omni.bin", custom, "new", "custom omni")
	if err != nil || st.CabinetFor("PCB2", dxModel, int64(33)).DB != res["musicdb_id"] {
		t.Fatal(res, err)
	}
}

// A song of the regular game counts for both the omnimix and the regular database, whichever it was
// played on; a song or chart only omnimix has stays with omnimix.
func TestSharedSongs(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	stay := [4]any{18032, 18, "Stay my side", []int{0, 5, 9, 12, 0, 0, 5, 9, 11, 0}}
	news := [4]any{33001, 33, "new song", []int{0, 3, 7, 10, 0, 0, 3, 7, 10, 0}}
	old := [4]any{1000, 0, "5.1.1.", []int{0, 2, 6, 10, 0, 0, 1, 7, 10, 0}}
	stayL := [4]any{18032, 18, "Stay my side", []int{0, 5, 9, 12, 12, 0, 5, 9, 11, 0}} // omnimix adds a LEGGENDARIA
	va, _ := st.ImportMusicDB("music_data.bin", makeMDB([][4]any{stay, news}), "new", "")
	om, _ := st.ImportMusicDB("music_omni.bin", makeMDB([][4]any{stayL, news, old}), "new", "")
	for _, p := range []struct {
		db           any
		music, chart int64
		ex           int
	}{
		{om["musicdb_id"], 18032, 3, 1500}, {va["musicdb_id"], 18032, 3, 1400}, {va["musicdb_id"], 33001, 3, 1000},
		{om["musicdb_id"], 1000, 3, 900}, {om["musicdb_id"], 18032, 4, 1600},
	} {
		if _, err := st.Exec("INSERT INTO plays (card_id, music_id, chart, ex_score, musicdb_id, game_version, played_at) "+
			"VALUES ('C1', ?, ?, ?, ?, 33, 1)", p.music, p.chart, p.ex, p.db); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range []struct {
		db   any
		want map[chartKey][2]int64 // best EX, plays
	}{
		{va["musicdb_id"], map[chartKey][2]int64{{18032, 3}: {1500, 2}, {33001, 3}: {1000, 1}}},
		{om["musicdb_id"], map[chartKey][2]int64{{18032, 3}: {1500, 2}, {33001, 3}: {1000, 1}, {1000, 3}: {900, 1}, {18032, 4}: {1600, 1}}},
	} {
		bests, err := st.bests("C1", c.db, "")
		if err != nil {
			t.Fatal(err)
		}
		got := map[chartKey][2]int64{}
		for k, b := range bests {
			got[k] = [2]int64{*b.ex, b.plays}
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("db %v: got %v, want %v", c.db, got, c.want)
		}
	}
}

func TestLinkInts(t *testing.T) {
	b := make([]byte, 22*4)
	binary.LittleEndian.PutUint32(b[4*4:], 1643)                    // side 0 PGREAT
	binary.LittleEndian.PutUint32(b[(11+5)*4:], uint32(0xFFFFFFFF)) // negative stays negative
	link := &eamuse.Node{Name: "tracker_link", Children: []*eamuse.Node{
		{Name: "judge", Text: hex.EncodeToString(b)},
		{Name: "lane", Text: "0011"}, // wrong size: dropped
	}}
	got := linkInts(link, "judge", 22)
	if s, _ := got.(string); !strings.HasPrefix(s, "0 0 0 0 1643 0") || strings.Fields(s)[16] != "-1" {
		t.Fatal(got)
	}
	if linkInts(link, "lane", 96) != nil || linkInts(nil, "judge", 22) != nil {
		t.Fatal("accepted a bad element")
	}
	m := make([]byte, 8)
	binary.LittleEndian.PutUint32(m, math.Float32bits(0.5))
	binary.LittleEndian.PutUint32(m[4:], math.Float32bits(-1))
	measures := linkMeasures(&eamuse.Node{Name: "tracker_link", Children: []*eamuse.Node{{Name: "measure", Text: hex.EncodeToString(m)}}})
	if measures != "0.5000 -1.0000" {
		t.Fatal(measures)
	}
	if d := judgeDetail(got, strings.Repeat("7 ", 96), measures); d == nil {
		t.Fatal("judgeDetail")
	} else if tm := d.(map[string]any)["timing"].([][]int64); tm[0][4] != 1643 || tm[1][5] != -1 {
		t.Fatal(tm)
	} else if ms := d.(map[string]any)["measures"].([]any); len(ms) != 2 || ms[0] != 50.0 || ms[1] != nil {
		t.Fatal(ms)
	}
}
