package store

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

// SongMeta is what the UI shows about a song.
type SongMeta struct {
	Title   string  `json:"title"`
	Artist  string  `json:"artist"`
	Genre   string  `json:"genre"`
	Version int64   `json:"version"`
	Levels  []int64 `json:"levels"`
}

type chartKey struct{ music, chart int64 }

type noteCount struct {
	notes  int64
	source string
}

// SongMetas maps music_id to metadata within one music database (music IDs mean different songs in
// different databases), preferring songs still present, then the newest import. A nil database
// merges all of them (only when there is no database to go by).
func (s *Store) SongMetas(db any) (map[int64]SongMeta, error) {
	key := fmt.Sprint(db)
	s.mu.Lock()
	cached := s.meta[key]
	s.mu.Unlock()
	if cached != nil {
		return cached, nil
	}
	query, args := "SELECT * FROM songs ORDER BY removed DESC, last_import ASC", []any{}
	if db != nil {
		query, args = "SELECT * FROM songs WHERE musicdb_id = ? ORDER BY removed DESC, last_import ASC", []any{db}
	}
	rows, err := s.Rows(query, args...)
	if err != nil {
		return nil, err
	}
	meta := map[int64]SongMeta{}
	for _, r := range rows {
		var levels []int64
		json.Unmarshal([]byte(str(r["levels"])), &levels)
		meta[r["music_id"].(int64)] = SongMeta{str(r["title"]), str(r["artist"]), str(r["genre"]), orZero(r["version"]), levels}
	}
	s.mu.Lock()
	s.meta[key] = meta
	s.mu.Unlock()
	return meta, nil
}

// ForgetSongs drops the cached metadata after a music database changed.
func (s *Store) ForgetSongs() {
	s.mu.Lock()
	s.meta = map[string]map[int64]SongMeta{}
	s.mu.Unlock()
}

func str(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case []byte:
		return string(x)
	case nil:
		return ""
	}
	return fmt.Sprint(v)
}

// noteCounts returns the note count to use per chart of music database db. The database's own
// count comes first (read from the chart "analysis" > imported JSON "import" > learned from a
// finished play "observed", one row per chart). Without one, a count another database has for
// the same song - same music ID and same title, since IDs can mean different songs - and last
// the old counts that belong to no database.
func (s *Store) noteCounts(db any) (map[chartKey]noteCount, error) {
	out := map[chartKey]noteCount{}
	add := func(query string, args ...any) error {
		rows, err := s.Rows(query, args...)
		for _, r := range rows {
			out[chartKey{orZero(r["music_id"]), orZero(r["chart"])}] = noteCount{orZero(r["notes"]), str(r["source"])}
		}
		return err
	}
	rank := "CASE n.source WHEN 'analysis' THEN 3 WHEN 'import' THEN 2 ELSE 1 END, n.updated_at"
	steps := []func() error{
		func() error { return add("SELECT music_id, chart, notes, source FROM chart_notes") },
		func() error {
			if db == nil {
				return add("SELECT music_id, chart, notes, source FROM chart_notes_db n ORDER BY " + rank)
			}
			return add("SELECT n.music_id, n.chart, n.notes, n.source FROM chart_notes_db n "+
				"JOIN songs o ON o.musicdb_id = n.musicdb_id AND o.music_id = n.music_id "+
				"JOIN songs c ON c.musicdb_id = ? AND c.music_id = n.music_id AND c.title = o.title "+
				"WHERE n.musicdb_id != ? ORDER BY "+rank, db, db)
		},
		func() error {
			if db == nil {
				return nil
			}
			return add("SELECT music_id, chart, notes, source FROM chart_notes_db WHERE musicdb_id = ?", db)
		},
	}
	for _, step := range steps {
		if err := step(); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// Notes returns the note count of a chart and where it came from ("analysis", "observed" or
// "import"), in the context of music database db (nil = none).
func (s *Store) Notes(music, chart int64, db any) (int64, string, bool) {
	all, _ := s.noteCounts(db)
	n, ok := all[chartKey{music, chart}]
	return n.notes, n.source, ok
}

// playerFilter: player keys are card IDs, or "iidx:<id>" for plays whose card is unknown.
func playerFilter(key string) (string, []any) {
	if strings.HasPrefix(key, "iidx:") {
		return "card_id IS NULL AND iidx_id = ?", []any{num(key[5:], true)}
	}
	return "card_id = ?", []any{key}
}

// ---- categories --------------------------------------------------------------

// CategorySongs returns the song IDs of an automatic (v:, db:, diff:) or manual (m:) category;
// versions are looked up in music database db. nil for an unknown kind.
func (s *Store) CategorySongs(key string, db any) (map[int64]bool, error) {
	kind, arg, _ := strings.Cut(key, ":")
	switch kind {
	case "v":
		meta, err := s.SongMetas(db)
		if err != nil {
			return nil, err
		}
		v := num(arg, true)
		out := map[int64]bool{}
		for mid, m := range meta {
			if v != nil && m.Version == v.(int64) {
				out[mid] = true
			}
		}
		return out, nil
	case "m":
		rows, err := s.Rows("SELECT music_id FROM category_songs WHERE category_id = ?", num(arg, true))
		if err != nil {
			return nil, err
		}
		out := map[int64]bool{}
		for _, r := range rows {
			out[r["music_id"].(int64)] = true
		}
		return out, nil
	case "db":
		return songSet(s.db, num(arg, true))
	case "diff":
		a, b, _ := strings.Cut(arg, ":")
		left, err := songSet(s.db, num(a, true))
		if err != nil {
			return nil, err
		}
		right, err := songSet(s.db, num(b, true))
		if err != nil {
			return nil, err
		}
		for id := range right {
			delete(left, id)
		}
		return left, nil
	}
	return nil, nil
}

// Categories lists the categories the UI offers within music database db: its versions, the
// songs it has and another database of the same game version lacks, and the manual categories.
func (s *Store) Categories(db any) (map[string]any, error) {
	meta, err := s.SongMetas(db)
	if err != nil {
		return nil, err
	}
	seen := map[int64]bool{}
	for _, m := range meta {
		seen[m.Version] = true
	}
	var versions []int64
	for v := range seen {
		versions = append(versions, v)
	}
	sort.Slice(versions, func(i, j int) bool { return versions[i] < versions[j] })
	dbs, err := s.Rows("SELECT * FROM musicdbs ORDER BY id")
	if err != nil {
		return nil, err
	}
	auto := []map[string]any{}
	for _, cur := range dbs {
		if fmt.Sprint(cur["id"]) != fmt.Sprint(db) {
			continue
		}
		for _, other := range dbs {
			if other["id"] != cur["id"] && fmt.Sprint(other["game_version"]) == fmt.Sprint(cur["game_version"]) {
				auto = append(auto, map[string]any{"key": fmt.Sprintf("diff:%v:%v", cur["id"], other["id"]),
					"name": fmt.Sprintf("%s に無い曲", str(other["name"])), "other": str(other["name"])})
			}
		}
	}
	manual, err := s.Rows("SELECT c.*, COUNT(s.music_id) AS songs FROM categories c " +
		"LEFT JOIN category_songs s ON s.category_id = c.id GROUP BY c.id ORDER BY c.id")
	if err != nil {
		return nil, err
	}
	vs := []map[string]any{}
	for _, v := range versions {
		name, ok := VersionNames[v]
		if !ok {
			name = fmt.Sprintf("ver %d", v)
		}
		vs = append(vs, map[string]any{"key": fmt.Sprintf("v:%d", v), "name": name, "version": v})
	}
	return map[string]any{"versions": vs, "auto": auto, "manual": manual}, nil
}

// ---- queries for the web UI ------------------------------------------------------

const playColumns = "id, session_id, card_id, upstream, iidx_id, played_at, model, game_version, omni, " +
	"cabinet, music_id, chart, level, clear, ex_score, pgreat, great, good, bad, poor, " +
	"combo_break, fast, slow, miss_count, dj_level, gauge_type, mode_type, option1, option2, " +
	"ran_arrange, play_style, play_side, progress, is_death, prev_best_score, " +
	"prev_best_clear, prev_best_miss, musicdb_id, pcbid, " +
	"judge_timing IS NOT NULL AS analyzed, chart_set, dxtra"

// Players lists the cards seen, then the card-less players (by IIDX ID).
func (s *Store) Players() ([]Row, error) {
	out, err := s.Rows("SELECT c.card_id AS key, c.card_id, c.first_seen, c.last_seen, " +
		"COUNT(p.id) AS plays, SUM(p.cabinet = 'TDJ') AS tdj, SUM(p.cabinet = 'LDJ') AS ldj, " +
		"MAX(p.played_at) AS last_played " +
		"FROM cards c LEFT JOIN plays p ON p.card_id = c.card_id " +
		"GROUP BY c.card_id ORDER BY c.last_seen DESC")
	if err != nil {
		return nil, err
	}
	for _, c := range out {
		if c["profile"], err = s.Row("SELECT * FROM profiles WHERE card_id = ? ORDER BY last_seen DESC LIMIT 1", c["card_id"]); err != nil {
			return nil, err
		}
	}
	anon, err := s.Rows("SELECT iidx_id, COUNT(*) AS plays, SUM(cabinet = 'TDJ') AS tdj, " +
		"SUM(cabinet = 'LDJ') AS ldj, MAX(played_at) AS last_played " +
		"FROM plays WHERE card_id IS NULL GROUP BY iidx_id")
	if err != nil {
		return nil, err
	}
	for _, o := range anon {
		o["key"], o["card_id"], o["profile"] = fmt.Sprintf("iidx:%v", o["iidx_id"]), nil, nil
		out = append(out, o)
	}
	return out, nil
}

type best struct {
	music, chart                           int64
	ex, clear, miss, lastPlayed, level, dj *int64
	plays                                  int64
}

// inDB selects the rows of table (plays or server_bests) that count for music database db: those
// recorded under it, and those recorded under another database of the same game version on a chart
// db has too (same music ID, same title, the chart exists) - a song of the regular game counts the
// same whether it was played on omnimix or not. Songs and charts only one database has stay with it.
func inDB(table string, db any) (string, []any) {
	if db == nil {
		return table + ".musicdb_id IS NULL", nil
	}
	return fmt.Sprintf(`(%[1]s.musicdb_id = ? OR EXISTS (SELECT 1 FROM songs o
		JOIN songs d ON d.musicdb_id = ? AND d.music_id = o.music_id AND d.title = o.title AND d.removed = 0
		JOIN musicdbs om ON om.id = o.musicdb_id JOIN musicdbs dm ON dm.id = d.musicdb_id AND dm.game_version = om.game_version
		WHERE o.musicdb_id = %[1]s.musicdb_id AND o.music_id = %[1]s.music_id
			AND json_extract(d.levels, '$[' || %[1]s.chart || ']') > 0))`, table), []any{db, db}
}

// bests: best per chart of music database db from recorded plays, merged with the server's bests
// (getrank). set: a 2dxtra chart set, whose plays are kept apart ("" = the game's charts).
func (s *Store) bests(key string, db any, set string) (map[chartKey]*best, error) {
	who, whoArgs := playerFilter(key)
	pdb, pdbArgs := inDB("plays", db)
	sw, sa := setWhere(set)
	pw, pa := who+" AND "+pdb+" AND "+sw, append(append(append([]any{}, whoArgs...), pdbArgs...), sa...)
	rows, err := s.Rows("SELECT music_id, chart, MAX(ex_score) AS best_ex, MAX(clear) AS best_clear, "+
		"MIN(CASE WHEN miss_count >= 0 THEN miss_count END) AS best_miss, "+
		"COUNT(*) AS plays, MAX(played_at) AS last_played, MAX(level) AS level "+
		"FROM plays WHERE "+pw+" GROUP BY music_id, chart", pa...)
	if err != nil {
		return nil, err
	}
	out := map[chartKey]*best{}
	for _, r := range rows {
		b := &best{music: orZero(r["music_id"]), chart: orZero(r["chart"]), ex: ptr(r["best_ex"]),
			clear: ptr(r["best_clear"]), miss: ptr(r["best_miss"]), plays: orZero(r["plays"]),
			lastPlayed: ptr(r["last_played"]), level: ptr(r["level"])}
		out[chartKey{b.music, b.chart}] = b
	}
	var server []Row
	if set == "" {
		sdb, sdbArgs := inDB("server_bests", db)
		if server, err = s.Rows("SELECT music_id, chart, MAX(ex_score) AS ex, MAX(clear) AS clear, "+
			"MIN(miss_count) AS miss FROM server_bests WHERE "+who+" AND "+sdb+" GROUP BY music_id, chart",
			append(append([]any{}, whoArgs...), sdbArgs...)...); err != nil {
			return nil, err
		}
	}
	maxOf := func(vals ...*int64) *int64 {
		m := int64(0) // the Python max() includes 0
		for _, v := range vals {
			if v != nil && *v > m {
				m = *v
			}
		}
		return &m
	}
	for _, r := range server {
		k := chartKey{orZero(r["music_id"]), orZero(r["chart"])}
		b := out[k]
		if b == nil {
			b = &best{music: k.music, chart: k.chart}
			out[k] = b
		}
		b.ex = maxOf(b.ex, ptr(r["ex"]))
		b.clear = maxOf(b.clear, ptr(r["clear"]))
		if m := ptr(r["miss"]); m != nil && (b.miss == nil || *m < *b.miss) {
			b.miss = m
		}
	}
	// the DJ LEVEL the game reported for the best EX, for charts whose note count is unknown
	lv, err := s.Rows("SELECT music_id, chart, ex_score, dj_level FROM plays WHERE "+pw+" AND dj_level IS NOT NULL", pa...)
	if err != nil {
		return nil, err
	}
	for _, r := range lv {
		b := out[chartKey{orZero(r["music_id"]), orZero(r["chart"])}]
		if ex := ptr(r["ex_score"]); b != nil && ex != nil && b.ex != nil && *ex == *b.ex {
			b.dj = ptr(r["dj_level"])
		}
	}
	return out, nil
}

// bestDjPoint: DJ POINT of a chart's best (DjPoint_CalcForRecord: best EX, best lamp and the
// DJ LEVEL of the best EX from the note count). Without a note count, the level the game sent.
func bestDjPoint(b *best, n *noteCount) *int64 {
	if b == nil || b.ex == nil {
		return nil
	}
	level := b.dj
	if n != nil {
		l := DjLevel(*b.ex, n.notes)
		level = &l
	}
	return DjPoint(b.ex, b.clear, level)
}

func val(p *int64) any {
	if p == nil {
		return nil
	}
	return *p
}

// ChartRows lists the charts of music database db with the player's bests; every filter is optional.
// With a 2dxtra chart set, the set's charts (those imported or played) and the bests on them.
func (s *Store) ChartRows(key, style string, level int64, category, query string, db any, set string) ([]map[string]any, error) {
	meta, err := s.SongMetas(db)
	if err != nil {
		return nil, err
	}
	notes, err := s.noteCounts(db)
	if err != nil {
		return nil, err
	}
	bests := map[chartKey]*best{}
	if key != "" {
		if bests, err = s.bests(key, db, set); err != nil {
			return nil, err
		}
	}
	var custom map[chartKey]int64
	if set != "" {
		if custom, err = s.customNotes(set); err != nil {
			return nil, err
		}
	}
	lo, hi := int64(0), int64(10)
	switch style {
	case "SP":
		hi = 5
	case "DP":
		lo = 5
	}
	var allowed map[int64]bool
	if category != "" {
		if allowed, err = s.CategorySongs(category, db); err != nil {
			return nil, err
		}
	}
	query = strings.ToLower(strings.TrimSpace(query))
	ids := map[int64]bool{}
	if custom == nil {
		for mid := range meta {
			ids[mid] = true
		}
	}
	for k := range custom {
		ids[k.music] = true
	}
	for k := range bests {
		ids[k.music] = true
	}
	sorted := make([]int64, 0, len(ids))
	for mid := range ids {
		sorted = append(sorted, mid)
	}
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	out := []map[string]any{}
	for _, mid := range sorted {
		if category != "" && allowed != nil && !allowed[mid] {
			continue
		}
		song, hasSong := meta[mid]
		if query != "" {
			hay := strconv.FormatInt(mid, 10)
			if hasSong {
				hay = fmt.Sprintf("%s %s %d", song.Title, song.Artist, mid)
			}
			if !strings.Contains(strings.ToLower(hay), query) {
				continue
			}
		}
		for chart := lo; chart < hi; chart++ {
			b := bests[chartKey{mid, chart}]
			customNotes, inSet := custom[chartKey{mid, chart}]
			if custom != nil && !inSet && b == nil {
				continue
			}
			lv := int64(0)
			if hasSong && int(chart) < len(song.Levels) {
				lv = song.Levels[chart]
			}
			if lv == 0 && b != nil && b.level != nil {
				lv = *b.level
			}
			if lv == 0 && b == nil && !inSet {
				continue
			}
			if level != 0 && lv != level {
				continue
			}
			row := map[string]any{"music_id": mid, "chart": chart, "level": lv,
				"title": nil, "artist": nil, "version": nil, "notes": nil, "notes_source": nil}
			if hasSong {
				row["title"], row["artist"], row["version"] = song.Title, song.Artist, song.Version
			}
			var nc *noteCount
			if n, ok := notes[chartKey{mid, chart}]; ok && custom == nil {
				nc = &n
				row["notes"], row["notes_source"] = n.notes, n.source
			} else if customNotes > 0 {
				nc = &noteCount{customNotes, unknownSet}
				row["notes"], row["notes_source"] = customNotes, unknownSet
			}
			row["djpoint"] = val(bestDjPoint(b, nc))
			if t := s.Tiers(mid, chart); t != nil && custom == nil { // the arcade charts only
				row["tier"] = t
			}
			if b != nil {
				row["best_ex"], row["best_clear"], row["best_miss"] = val(b.ex), val(b.clear), val(b.miss)
				row["plays"], row["last_played"] = b.plays, val(b.lastPlayed)
			}
			out = append(out, row)
		}
	}
	return out, nil
}

// DjPointTotal: DjPoint_CalcTotal (0x18082d0d0) takes per song the best DJ POINT among its charts
// of the style, sums them and divides by 10000. rows: ChartRows of one style.
func DjPointTotal(rows []map[string]any) map[string]any {
	perSong := map[int64]int64{}
	unknown := 0
	for _, r := range rows {
		if r["best_ex"] == nil {
			continue
		}
		dp, ok := asInt(r["djpoint"])
		if !ok {
			unknown++
			continue
		}
		mid := r["music_id"].(int64)
		if dp > perSong[mid] {
			perSong[mid] = dp
		} else if _, seen := perSong[mid]; !seen {
			perSong[mid] = dp
		}
	}
	total := int64(0)
	for _, v := range perSong {
		total += v
	}
	return map[string]any{"total": total / 10000, "songs": len(perSong), "unknown": unknown}
}

// PlaysFor lists plays with the derived fields the UI shows; titles come from each play's own
// music database.
func (s *Store) PlaysFor(where string, args []any, tail string) ([]Row, error) {
	out, err := s.Rows("SELECT "+playColumns+" FROM plays WHERE "+where+" "+tail, args...)
	if err != nil {
		return nil, err
	}
	for _, p := range out {
		p["title"] = nil
		meta, err := s.SongMetas(p["musicdb_id"])
		if err != nil {
			return nil, err
		}
		if song, ok := meta[orZero(p["music_id"])]; ok {
			p["title"] = song.Title
		}
		p["options"] = DescribeOptions(ptr(p["option1"]), ptr(p["option2"]), ptr(p["play_style"]))
		p["gauge"] = GaugeName(ptr(p["gauge_type"]), ptr(p["mode_type"]))
		p["hispeed"] = HiSpeed(ptr(p["option1"]))
		p["mode"] = p["mode_type"]
		if m, ok := asInt(p["mode_type"]); ok {
			if name, known := Modes[m]; known {
				p["mode"] = name
			}
		}
		p["djpoint"] = val(DjPoint(ptr(p["ex_score"]), ptr(p["clear"]), ptr(p["dj_level"])))
	}
	return out, nil
}

// Player gathers a player's page; set picks a 2dxtra chart set for the plays ("" = the game's charts).
// Each login's radar_sp_set / radar_dp_set name the chart set its notes radar belongs to (see
// radarSource; nil = the game's charts).
func (s *Store) Player(key, set string) (map[string]any, error) {
	where, args := playerFilter(key)
	out := map[string]any{"key": key, "profiles": []Row{}}
	var err error
	if !strings.HasPrefix(key, "iidx:") {
		if out["profiles"], err = s.Rows("SELECT * FROM profiles WHERE card_id = ? ORDER BY first_seen", key); err != nil {
			return nil, err
		}
	}
	if out["sessions"], err = s.Rows("SELECT id, upstream, iidx_id, model, game_version, omni, dxtra, cabinet, sp_plays, "+
		"dp_plays, arena_sp, arena_dp, radar_sp, radar_dp, started_at, saved_at, djpoint_sp, djpoint_dp, pcbid, musicdb_id, "+
		"(SELECT chart_set FROM plays p WHERE p.id = radar_sp_play) AS radar_sp_set, "+
		"(SELECT chart_set FROM plays p WHERE p.id = radar_dp_play) AS radar_dp_set "+
		"FROM sessions WHERE "+where+" ORDER BY started_at", args...); err != nil {
		return nil, err
	}
	sw, sa := setWhere(set)
	where, args = where+" AND "+sw, append(args, sa...)
	since := Now() - 180*86400
	if out["per_day"], err = s.Rows("SELECT date(played_at, 'unixepoch', 'localtime') AS day, COUNT(*) AS plays "+
		"FROM plays WHERE "+where+" AND played_at > ? GROUP BY day ORDER BY day", append(args, since)...); err != nil {
		return nil, err
	}
	if out["recent"], err = s.PlaysFor(where, args, "ORDER BY played_at DESC LIMIT 50"); err != nil {
		return nil, err
	}
	return out, nil
}

// Chart gathers one chart's page for a player, within music database db; set picks a 2dxtra chart
// set ("" = the game's chart).
func (s *Store) Chart(key string, db any, music, chart int64, set string) (map[string]any, error) {
	where, args := playerFilter(key)
	sw, sa := setWhere(set)
	dw, da := inDB("plays", db)
	where, args = where+" AND "+dw+" AND "+sw, append(append(args, da...), sa...)
	meta, err := s.SongMetas(db)
	if err != nil {
		return nil, err
	}
	out := map[string]any{"music_id": music, "chart": chart, "song": nil, "notes": nil, "notes_source": nil, "set": nil, "tier": nil}
	if m, ok := meta[music]; ok {
		out["song"] = m
	}
	if t := s.Tiers(music, chart); t != nil && set == "" {
		level := int64(12)
		if m, ok := meta[music]; ok && int(chart) < len(m.Levels) {
			level = m.Levels[chart]
		}
		out["tier"], out["tier_sources"] = t, TierSources(level)
	}
	var nc *noteCount
	if set != "" {
		out["set"] = set
		custom, err := s.customNotes(set)
		if err != nil {
			return nil, err
		}
		if n := custom[chartKey{music, chart}]; n > 0 {
			nc = &noteCount{n, unknownSet}
			out["notes"], out["notes_source"] = n, unknownSet
		}
	} else if n, src, ok := s.Notes(music, chart, db); ok {
		nc = &noteCount{n, src}
		out["notes"], out["notes_source"] = n, src
	}
	bests, err := s.bests(key, db, set)
	if err != nil {
		return nil, err
	}
	out["djpoint"] = val(bestDjPoint(bests[chartKey{music, chart}], nc))
	if out["plays"], err = s.PlaysFor(where+" AND music_id = ? AND chart = ?", append(args, music, chart), "ORDER BY played_at"); err != nil {
		return nil, err
	}
	sections, err := s.sections(where, args, music, chart, 20)
	if err != nil {
		return nil, err
	}
	out["sections"] = sections
	return out, nil
}

// sections: EX lost per ghost bucket, averaged over the recent finished plays. Plays are grouped
// by their judged note count (assist options drop notes) and the most common count is used.
func (s *Store) sections(where string, args []any, music, chart int64, recent int) (any, error) {
	rows, err := s.Rows("SELECT ghost, pgreat, great, good, miss_count FROM plays WHERE "+where+
		" AND music_id = ? AND chart = ? AND progress = 10000 AND COALESCE(is_death, 0) = 0 "+
		"AND good IS NOT NULL AND miss_count >= 0 AND length(ghost) = 64 ORDER BY played_at DESC",
		append(args, music, chart)...)
	if err != nil {
		return nil, err
	}
	byNotes := map[int64][][]byte{}
	var order []int64 // first appearance, so ties go the way Python's max() goes
	for _, r := range rows {
		n := orZero(r["pgreat"]) + orZero(r["great"]) + orZero(r["good"]) + orZero(r["miss_count"])
		if _, ok := byNotes[n]; !ok {
			order = append(order, n)
		}
		g, _ := r["ghost"].([]byte)
		byNotes[n] = append(byNotes[n], g)
	}
	if len(order) == 0 {
		return nil, nil
	}
	notes := order[0]
	for _, n := range order[1:] {
		if len(byNotes[n]) > len(byNotes[notes]) {
			notes = n
		}
	}
	ghosts := byNotes[notes]
	if len(ghosts) > recent {
		ghosts = ghosts[:recent]
	}
	sizes := GhostBucketSizes(int(notes))
	lost := make([]float64, 64)
	for b, size := range sizes {
		sum := 0
		for _, g := range ghosts {
			sum += 2*size - int(g[b])
		}
		lost[b] = float64(sum) / float64(len(ghosts))
	}
	return map[string]any{"plays": len(ghosts), "notes": notes, "sizes": sizes, "lost": lost}, nil
}

// PlayGraphs returns one play's graphs and the music.reg values the history table does not show.
func (s *Store) PlayGraphs(id int64) (any, error) {
	r, err := s.Row("SELECT music_id, chart, ghost, ghost_gauge, is_death, progress, pgreat, great, "+
		"good, miss_count, chatter, raw, musicdb_id, judge_timing, judge_lanes, judge_measures, chart_notes FROM plays WHERE id = ?", id)
	if err != nil || r == nil {
		return nil, err
	}
	var gauge []float64
	if g, _ := r["ghost_gauge"].([]byte); len(g) > 0 {
		var raw []int
		for i := 0; i+1 < len(g); i += 2 {
			raw = append(raw, int(g[i])|int(g[i+1])<<8)
		}
		for len(raw) > 0 && raw[len(raw)-1] == 0 {
			raw = raw[:len(raw)-1]
		}
		if d, _ := asInt(r["is_death"]); d != 0 {
			raw = append(raw, 0)
		}
		for _, v := range raw {
			gauge = append(gauge, float64(v)/50)
		}
	}
	if gauge == nil {
		gauge = []float64{}
	}
	// ghost: EX score gained in each 1/64 of the judged notes; gauge: 1/50 %
	ghostBytes, _ := r["ghost"].([]byte)
	ghost := make([]int, len(ghostBytes))
	for i, b := range ghostBytes {
		ghost[i] = int(b)
	}
	var notes *int64
	var source any
	progress, _ := asInt(r["progress"])
	death, _ := asInt(r["is_death"])
	miss, hasMiss := asInt(r["miss_count"])
	if progress == 10000 && death == 0 && r["good"] != nil && hasMiss && miss >= 0 {
		n := orZero(r["pgreat"]) + orZero(r["great"]) + orZero(r["good"]) + miss
		notes, source = &n, "play"
	} else if n, ok := asInt(r["chart_notes"]); ok && n > 0 {
		notes, source = &n, unknownSet
	} else if n, src, ok := s.Notes(orZero(r["music_id"]), orZero(r["chart"]), r["musicdb_id"]); ok {
		notes, source = &n, src
	}
	var sizes []int
	if notes != nil && *notes > 0 && len(ghost) == 64 {
		sizes = GhostBucketSizes(int(*notes))
		for i, g := range ghost {
			if g > 2*sizes[i] {
				sizes = nil // the note count does not fit this play
				break
			}
		}
	}
	out := map[string]any{"ghost": ghost, "gauge": gauge, "bucket_notes": nil, "notes": nil,
		"notes_source": source, "graph_type": nil, "target_score": nil, "folder_type": nil, "chatter": nil}
	if sizes != nil {
		out["bucket_notes"], out["notes"] = sizes, *notes
	}
	if raw, _ := r["raw"].([]byte); len(raw) > 0 {
		if m, err := decompressRaw(raw); err == nil {
			log, best := m.Find("music_play_log"), m.Find("best_result")
			out["graph_type"], out["folder_type"], out["target_score"] = attr(log, "graph_type"), attr(log, "folder_type"), attr(best, "target_score")
		}
	}
	if c := str(r["chatter"]); c != "" {
		out["chatter"] = chatterRows(strings.Fields(c))
	}
	out["judges"] = judgeDetail(r["judge_timing"], r["judge_lanes"], r["judge_measures"])
	return out, nil
}

// judgeDetail unpacks what tracker_link.dll added to the play's music.reg (see linkInts):
// timing[lane side][11 display codes], lanes[lane side][8 lanes][6 kinds] and measures (score rate
// per measure in %, nil for a measure without notes; plays before it have none). nil without it.
func judgeDetail(timing, lanes, measures any) any {
	t, l := strings.Fields(str(timing)), strings.Fields(str(lanes))
	if len(t) != 22 || len(l) != 96 {
		return nil
	}
	n := func(s string) int64 { v, _ := strconv.ParseInt(s, 10, 64); return v }
	out := map[string]any{}
	tm := make([][]int64, 2)
	ln := make([][][]int64, 2)
	for side := 0; side < 2; side++ {
		tm[side] = make([]int64, 11)
		for c := range tm[side] {
			tm[side][c] = n(t[side*11+c])
		}
		ln[side] = make([][]int64, 8)
		for lane := range ln[side] {
			ln[side][lane] = make([]int64, 6)
			for k := range ln[side][lane] {
				ln[side][lane][k] = n(l[side*48+lane*6+k])
			}
		}
	}
	var ms []any
	for _, f := range strings.Fields(str(measures)) {
		v, err := strconv.ParseFloat(f, 64)
		if err != nil || v < 0 {
			ms = append(ms, nil)
		} else {
			ms = append(ms, math.Round(v*1000)/10)
		}
	}
	out["timing"], out["lanes"], out["measures"] = tm, ln, ms
	return out
}

func chatterRows(fields []string) [][]int64 {
	rows := make([][]int64, 14)
	for i := range rows {
		rows[i] = make([]int64, 8)
		for k := 0; k < 8 && i*8+k < len(fields); k++ {
			rows[i][k], _ = strconv.ParseInt(fields[i*8+k], 10, 64)
		}
	}
	return rows
}

// ChatterSummary adds up chattering per key over the recent plays not known to be on a standard
// cabinet (the game only counts it on the Lightning Model).
func (s *Store) ChatterSummary(key string, recent int) (any, error) {
	where, args := playerFilter(key)
	rows, err := s.Rows("SELECT chatter FROM plays WHERE "+where+" AND chatter IS NOT NULL "+
		"AND cabinet IS NOT 'LDJ' ORDER BY played_at DESC LIMIT ?", append(args, recent)...)
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	total := make([]string, 112)
	sums := make([]int64, 112)
	for _, r := range rows {
		for i, v := range strings.Fields(str(r["chatter"])) {
			if i < 112 {
				n, _ := strconv.ParseInt(v, 10, 64)
				sums[i] += n
			}
		}
	}
	for i, v := range sums {
		total[i] = strconv.FormatInt(v, 10)
	}
	return map[string]any{"plays": len(rows), "counts": chatterRows(total)}, nil
}
