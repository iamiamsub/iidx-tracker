package store

// 2dxtra (a hook DLL for the game) generates charts from the game's own - Kiraku, Kichiku,
// All-Scratch - and keeps them in 2dxtra.sqlite next to itself. It holds back music.reg for plays
// on them, so tracker_link.dll collects those plays and sends them with the player's pc.save
// (<tracker_link><custom_play .../></tracker_link>, see tracker_link.cc). They are recorded like
// music.reg plays, but with the chart they were on (plays.chart_set / chart_hash / chart_notes)
// and kept apart from the game's charts.

import (
	"bytes"
	"database/sql"
	"encoding/binary"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"iidx-tracker/internal/eamuse"
	"iidx-tracker/internal/i18n"
)

// CustomChart is the 2dxtra chart a play was on.
type CustomChart struct {
	Set, Hash string
	Notes     any
}

// unknownSet files plays on a chart no imported 2dxtra.sqlite lists (yet).
const unknownSet = "2dxtra"

// The music_play_log block tracker_link.dll copies (bm2dx 2026081900, filled by MusicPlayLog_Fill;
// analysis_0819 score4.md §5.1): little-endian, iidx_id a NUL-terminated string at 0.
const (
	logSize       = 0x658
	logDeath      = 0x39c // u8: the play ended early (music.reg is_death@)
	logEndTime    = 0x3c4 // UNIX seconds
	logChatter    = 0x3c8 // s32[14][8]
	logProgress   = 0x588 // 10000 = to the end
	logRanArrange = 0x3a0 // s64
)

var logInts = []struct {
	name string
	off  int
}{{"music_id", 0x10}, {"play_style", 0x14}, {"note_id", 0x18}, {"ex_score", 0x20}, {"pgreat_num", 0x24},
	{"great_num", 0x28}, {"clear_flg", 0x2c}, {"miss_num", 0x30}, {"dj_level", 0x34}, {"mode_type", 0x38},
	{"folder_type", 0x3c}, {"stage_num", 0x40}, {"gauge_type", 0x44}, {"graph_type", 0x58}}

// customMusicReg turns a <custom_play> into the music.reg request the game would have sent,
// so it is recorded the same way. Also returns when the play ended (0 = unknown).
func customMusicReg(p *eamuse.Node) (*eamuse.Node, int64, bool) {
	log, _ := binText(p.Find("log")).([]byte)
	kinds := strings.Fields(p.Get("kinds"))
	if len(log) < logSize || len(kinds) != 5 {
		return nil, 0, false
	}
	i32 := func(off int) int64 { return int64(int32(binary.LittleEndian.Uint32(log[off:]))) }
	i64 := func(off int) int64 { return int64(binary.LittleEndian.Uint64(log[off:])) }
	iidx := string(log[:16])
	if end := strings.IndexByte(iidx, 0); end >= 0 {
		iidx = iidx[:end]
	}
	if iidx == "" {
		return nil, 0, false
	}
	node := func(name string, attrs ...any) *eamuse.Node {
		n := &eamuse.Node{Name: name}
		for i := 0; i+1 < len(attrs); i += 2 {
			n.Set(attrs[i].(string), fmt.Sprint(attrs[i+1]))
		}
		return n
	}
	death := int(log[logDeath])
	m := node("custom_play", "iidxid", iidx, "mid", p.Get("mid"), "clid", p.Get("clid"), "pgnum", kinds[0],
		"gnum", kinds[1], "mnum", p.Get("miss"), "cflg", p.Get("lamp"), "dj_level", p.Get("dj"),
		"pside", p.Get("side"), "is_death", death, "chart_id", p.Get("chart_id"), "notes", p.Get("notes"))
	play := node("music_play_log", "iidx_id", iidx, "option1", i64(0x48), "option2", i64(0x50),
		"is_sudden_death", death, "ran_arrange", i64(logRanArrange))
	for _, f := range logInts {
		play.Set(f.name, strconv.FormatInt(i32(f.off), 10))
	}
	chatter := node("chattering_log", "play_end_time", i32(logEndTime), "progress", i32(logProgress))
	for i := 0; i < 14; i++ {
		row := make([]string, 8)
		for k := range row {
			row[k] = strconv.FormatInt(i32(logChatter+(i*8+k)*4), 10)
		}
		chatter.Children = append(chatter.Children, &eamuse.Node{Name: fmt.Sprintf("chattering_%d", i), Text: strings.Join(row, " ")})
	}
	play.Children = []*eamuse.Node{chatter}
	best := node("best_result", "now_pgreat", kinds[0], "now_great", kinds[1], "now_good", kinds[2],
		"now_bad", kinds[3], "now_poor", kinds[4], "now_combo", p.Get("combo"), "now_fast", p.Get("fast"),
		"now_slow", p.Get("slow"))
	m.Children = []*eamuse.Node{play, best}
	for _, bin := range [][2]string{{"ghost", "ghost"}, {"gauge", "ghost_gauge"}} {
		if g := p.Find(bin[0]); g != nil {
			m.Children = append(m.Children, &eamuse.Node{Name: bin[1], Attrs: g.Attrs, Text: g.Text})
		}
	}
	return m, i32(logEndTime), true
}

// customPlays records the plays on 2dxtra's charts a pc.save carries.
func (s *Store) customPlays(c *Call, version any, cab Cabinet) error {
	if c.Link == nil {
		return nil
	}
	for _, p := range c.Link.FindAll("custom_play") {
		m, ended, ok := customMusicReg(p)
		if !ok {
			continue
		}
		call := *c
		call.Req, call.Link = m, p
		if ended > 0 {
			call.TS = ended
		}
		hash := p.Get("chart_id")
		// a pc.save sent twice (after a network error) carries the same plays
		var seen int
		if err := s.db.QueryRow("SELECT COUNT(*) FROM plays WHERE iidx_id = ? AND played_at = ? AND chart_hash = ?",
			attr(m, "iidxid"), call.TS, hash).Scan(&seen); err != nil {
			return err
		}
		if seen > 0 {
			continue
		}
		chart := &CustomChart{Set: s.chartSetOf(hash), Hash: hash, Notes: num(p.Get("notes"), true)}
		if err := s.musicReg(&call, version, cab, chart); err != nil {
			return err
		}
	}
	return nil
}

// SetRadar computes a player's notes radar on a 2dxtra chart set the way the game does
// (CPlayerNotesRadarGameData::Recalculate, analysis score4.md 4.2): per chart, from its best EX,
// v = radar x EX / (notes x 2) (radar values over 20000 count as 0); per attribute, the 10 highest v,
// one chart per song, summed and divided by 10. The game's own value only knows the set's plays since
// the game started (2dxtra keeps their scores in memory); this one knows all of them. SP and DP (the
// charts 0..4 and 5..9), attributes in the game's order (NOTES, PEAK, SCRATCH, SOF-LAN, CHARGE, CHORD).
func (s *Store) SetRadar(key string, db any, set string) (map[string][6]int64, error) {
	bests, err := s.bests(key, db, set)
	if err != nil {
		return nil, err
	}
	charts, err := s.Rows("SELECT music_id, chart, notes, radar FROM custom_charts WHERE chart_set = ?", set)
	if err != nil {
		return nil, err
	}
	type value struct {
		music int64
		v     [6]int64
	}
	byStyle := map[string][]value{}
	for _, c := range charts {
		music, chart, notes := orZero(c["music_id"]), orZero(c["chart"]), orZero(c["notes"])
		b := bests[chartKey{music, chart}]
		if b == nil || b.ex == nil || *b.ex <= 0 || notes <= 0 || *b.ex > notes*2 {
			continue
		}
		rate := float32(*b.ex) / float32(notes*2)
		var v [6]int64
		for a, f := range strings.Fields(str(c["radar"])) {
			if r, err := strconv.ParseInt(f, 10, 64); err == nil && a < 6 && r <= 20000 {
				v[a] = int64(float32(r) * rate)
			}
		}
		style := "SP"
		if chart >= 5 {
			style = "DP"
		}
		byStyle[style] = append(byStyle[style], value{music, v})
	}
	out := map[string][6]int64{}
	for style, values := range byStyle {
		var radar [6]int64
		for a := range radar {
			sort.SliceStable(values, func(i, j int) bool { return values[i].v[a] > values[j].v[a] })
			seen, sum := map[int64]bool{}, int64(0)
			for _, x := range values {
				if len(seen) == 10 || x.v[a] <= 0 {
					break
				}
				if !seen[x.music] {
					seen[x.music] = true
					sum += x.v[a]
				}
			}
			radar[a] = sum / 10
		}
		out[style] = radar
	}
	return out, nil
}

// chartSetOf names the set of a 2dxtra chart id from the imported 2dxtra.sqlite.
func (s *Store) chartSetOf(hash string) string {
	var set string
	if s.db.QueryRow("SELECT chart_set FROM custom_charts WHERE hash = ? ORDER BY set_order LIMIT 1", hash).Scan(&set) != nil || set == "" {
		return unknownSet
	}
	return set
}

// setWhere limits plays to a 2dxtra chart set, or to the game's own charts for "".
func setWhere(set string) (string, []any) {
	if set == "" {
		return "chart_set IS NULL", nil
	}
	return "chart_set = ?", []any{set}
}

// customNotes: the note count of every chart of a set - from the imported 2dxtra.sqlite, else as
// tracker_link.dll reported it with a play.
func (s *Store) customNotes(set string) (map[chartKey]int64, error) {
	out := map[chartKey]int64{}
	rows, err := s.Rows("SELECT music_id, chart, MAX(chart_notes) AS notes FROM plays WHERE chart_set = ? "+
		"AND chart_notes > 0 GROUP BY music_id, chart", set)
	if err != nil {
		return nil, err
	}
	catalog, err := s.Rows("SELECT music_id, chart, notes FROM custom_charts WHERE chart_set = ?", set)
	if err != nil {
		return nil, err
	}
	for _, r := range append(rows, catalog...) {
		out[chartKey{orZero(r["music_id"]), orZero(r["chart"])}] = orZero(r["notes"])
	}
	return out, nil
}

// ChartSets lists the 2dxtra chart sets imported or played, in 2dxtra's order.
func (s *Store) ChartSets() ([]Row, error) {
	sets, err := s.Rows("SELECT chart_set AS name, MIN(set_order) AS set_order, COUNT(*) AS charts " +
		"FROM custom_charts GROUP BY chart_set")
	if err != nil {
		return nil, err
	}
	played, err := s.Rows("SELECT chart_set AS name, COUNT(*) AS plays FROM plays WHERE chart_set IS NOT NULL GROUP BY chart_set")
	if err != nil {
		return nil, err
	}
	byName := map[string]Row{}
	for _, r := range sets {
		r["plays"] = int64(0)
		byName[str(r["name"])] = r
	}
	for _, r := range played {
		if known, ok := byName[str(r["name"])]; ok {
			known["plays"] = r["plays"]
		} else {
			r["set_order"], r["charts"] = nil, int64(0)
			sets = append(sets, r)
		}
	}
	sort.SliceStable(sets, func(i, j int) bool {
		a, aok := asInt(sets[i]["set_order"])
		b, bok := asInt(sets[j]["set_order"])
		if aok != bok {
			return aok
		}
		return a < b
	})
	return sets, nil
}

// SetChart is one chart of 2dxtra.sqlite: its set (name and 2dxtra's order), song, chart (2dxtra's
// difficulty is the game's chart index, SPB..SPL, DPB..DPL, as clid@ is), id, note count and radar.
type SetChart struct {
	Set                string
	Order, Music, Diff int64
	Hash               string
	Notes              int64
	Radar              [6]int64
}

// ImportChartSets reads the list of charts from 2dxtra's 2dxtra.sqlite on this PC (read only; the
// charts themselves stay there) and files the plays recorded so far under their set.
func (s *Store) ImportChartSets(path string) (map[string]any, error) {
	path = strings.Trim(strings.TrimSpace(path), `"`)
	if st, err := os.Stat(path); err != nil || st.IsDir() {
		return nil, i18n.Errorf("ファイルがありません: %s", "file not found: %s", path)
	}
	var charts []SetChart
	read := func(options string) error {
		uri := strings.NewReplacer("%", "%25", "?", "%3f", "#", "%23").Replace(strings.ReplaceAll(path, `\`, "/"))
		src, err := sql.Open("sqlite", "file:"+uri+"?"+options)
		if err != nil {
			return err
		}
		defer src.Close()
		rows, err := src.Query("SELECT s.name, s.id, c.music_id, c.difficulty, c.hash, c.notes, c.radar_notes, c.radar_peak, " +
			"c.radar_scratch, c.radar_soflan, c.radar_charge, c.radar_chord FROM charts c JOIN chart_set s ON s.id = c.chart_set")
		if err != nil {
			return err
		}
		defer rows.Close()
		charts = charts[:0]
		for rows.Next() {
			var r SetChart
			var radar [6]sql.NullInt64 // missing = 0
			if err := rows.Scan(&r.Set, &r.Order, &r.Music, &r.Diff, &r.Hash, &r.Notes, &radar[0], &radar[1],
				&radar[2], &radar[3], &radar[4], &radar[5]); err != nil {
				return err
			}
			for i, v := range radar {
				r.Radar[i] = v.Int64
			}
			charts = append(charts, r)
		}
		return rows.Err()
	}
	// While the game runs, 2dxtra keeps the file in WAL mode; immutable reads it without its log.
	if err := read("mode=ro"); err != nil {
		if err2 := read("mode=ro&immutable=1"); err2 != nil {
			return nil, i18n.Errorf("2dxtra のデータベースとして読めません: %v", "cannot read it as 2dxtra's database: %v", err)
		}
	}
	return s.ImportSetCharts(charts)
}

// ImportSetCharts replaces the charts of the 2dxtra sets with those of a 2dxtra.sqlite - read here
// (ImportChartSets) or by a browser on its own PC - and files the plays recorded so far under their set.
func (s *Store) ImportSetCharts(charts []SetChart) (map[string]any, error) {
	if len(charts) == 0 {
		return nil, i18n.New("2dxtra の譜面がありません", "No 2dxtra charts in it")
	}
	now := time.Now().Unix()
	counts, order := map[string]int{}, map[string]int64{}
	var filed int64
	err := s.tx(func(tx *sql.Tx) error {
		if _, err := tx.Exec("DELETE FROM custom_charts"); err != nil {
			return err
		}
		for _, r := range charts {
			var radar bytes.Buffer
			for i, v := range r.Radar {
				if i > 0 {
					radar.WriteByte(' ')
				}
				radar.WriteString(strconv.FormatInt(v, 10))
			}
			if _, err := tx.Exec("INSERT OR REPLACE INTO custom_charts VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
				r.Set, r.Order, r.Music, r.Diff, r.Hash, r.Notes, radar.String(), now); err != nil {
				return err
			}
			counts[r.Set]++
			order[r.Set] = r.Order
		}
		res, err := tx.Exec("UPDATE plays SET chart_set = (SELECT c.chart_set FROM custom_charts c WHERE c.hash = plays.chart_hash " +
			"ORDER BY c.set_order LIMIT 1) WHERE chart_hash IS NOT NULL AND EXISTS " +
			"(SELECT 1 FROM custom_charts c WHERE c.hash = plays.chart_hash)")
		if err != nil {
			return err
		}
		filed, err = res.RowsAffected()
		return err
	})
	if err != nil {
		return nil, err
	}
	sets := []map[string]any{}
	for name, n := range counts {
		sets = append(sets, map[string]any{"name": name, "charts": n})
	}
	sort.Slice(sets, func(i, j int) bool { return order[str(sets[i]["name"])] < order[str(sets[j]["name"])] })
	return map[string]any{"sets": sets, "charts": len(charts), "plays": filed}, nil
}
