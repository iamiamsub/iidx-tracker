// Package store keeps what the tracker records in SQLite: plays (music.reg), profiles and
// sessions (pc.get), the cabinet type (pc.save), the server's bests (music.getrank), the music
// database and the queries behind the web UI. The schema is the Python tracker's, so an
// existing tracker.db is used as is.
package store

import (
	"bytes"
	"compress/zlib"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"

	"iidx-tracker/internal/eamuse"
	"iidx-tracker/internal/i18n"
)

const schema = `
CREATE TABLE IF NOT EXISTS origins (
    id INTEGER PRIMARY KEY, origin TEXT UNIQUE NOT NULL);

CREATE TABLE IF NOT EXISTS cards (
    card_id TEXT PRIMARY KEY, first_seen INTEGER, last_seen INTEGER);

-- One row per distinct identity (name, area, dan...) a card was seen with.
CREATE TABLE IF NOT EXISTS profiles (
    id INTEGER PRIMARY KEY, card_id TEXT, upstream TEXT, refid TEXT, iidx_id INTEGER,
    name TEXT, pid INTEGER, sgid INTEGER, dgid INTEGER,
    first_seen INTEGER, last_seen INTEGER);

-- One row per login (pc.get), carrying the values that move every session.
CREATE TABLE IF NOT EXISTS sessions (
    id INTEGER PRIMARY KEY, card_id TEXT, upstream TEXT, refid TEXT, iidx_id INTEGER,
    profile_id INTEGER, model TEXT, game_version INTEGER, omni INTEGER, cabinet TEXT,
    sp_plays INTEGER, dp_plays INTEGER, arena_sp INTEGER, arena_dp INTEGER,
    radar_sp TEXT, radar_dp TEXT, started_at INTEGER, saved_at INTEGER,
    djpoint_sp INTEGER, djpoint_dp INTEGER, pcbid TEXT, musicdb_id INTEGER,
    music_hash TEXT, dxtra INTEGER, radar_sp_play INTEGER, radar_dp_play INTEGER);

CREATE TABLE IF NOT EXISTS plays (
    id INTEGER PRIMARY KEY, session_id INTEGER, card_id TEXT, upstream TEXT, iidx_id INTEGER,
    played_at INTEGER, model TEXT, game_version INTEGER, omni INTEGER, cabinet TEXT,
    music_id INTEGER NOT NULL, chart INTEGER NOT NULL, level INTEGER,
    clear INTEGER, ex_score INTEGER, pgreat INTEGER, great INTEGER, good INTEGER,
    bad INTEGER, poor INTEGER, combo_break INTEGER, fast INTEGER, slow INTEGER,
    miss_count INTEGER, dj_level INTEGER, gauge_type INTEGER, mode_type INTEGER,
    option1 INTEGER, option2 INTEGER, ran_arrange INTEGER,
    play_style INTEGER, play_side INTEGER, progress INTEGER, is_death INTEGER,
    prev_best_score INTEGER, prev_best_clear INTEGER, prev_best_miss INTEGER,
    ghost BLOB, ghost_gauge BLOB, raw BLOB, chatter TEXT, pcbid TEXT, musicdb_id INTEGER,
    judge_timing TEXT, judge_lanes TEXT, judge_measures TEXT,
    chart_set TEXT, chart_hash TEXT, chart_notes INTEGER, music_hash TEXT, dxtra INTEGER);
CREATE INDEX IF NOT EXISTS plays_chart ON plays (card_id, music_id, chart);
CREATE INDEX IF NOT EXISTS plays_time ON plays (played_at);

-- A music database lineage ("IIDX33 omni") and the files imported into it.
CREATE TABLE IF NOT EXISTS musicdbs (
    id INTEGER PRIMARY KEY, name TEXT NOT NULL, kind TEXT, game_version INTEGER,
    created_at INTEGER);
CREATE TABLE IF NOT EXISTS musicdb_imports (
    id INTEGER PRIMARY KEY, musicdb_id INTEGER NOT NULL, filename TEXT, sha256 TEXT,
    song_count INTEGER, added INTEGER, removed INTEGER, imported_at INTEGER);
CREATE TABLE IF NOT EXISTS songs (
    musicdb_id INTEGER NOT NULL, music_id INTEGER NOT NULL,
    title TEXT, title_ascii TEXT, genre TEXT, artist TEXT, subtitle TEXT,
    version INTEGER, levels TEXT, last_import INTEGER, removed INTEGER DEFAULT 0,
    PRIMARY KEY (musicdb_id, music_id));

CREATE TABLE IF NOT EXISTS chart_notes (
    music_id INTEGER, chart INTEGER, notes INTEGER, source TEXT, updated_at INTEGER,
    PRIMARY KEY (music_id, chart));

-- Note counts read from the game's charts (.1), tied to the music database they belong to;
-- one row per scan in sound_scans.
CREATE TABLE IF NOT EXISTS chart_notes_db (
    musicdb_id INTEGER NOT NULL, music_id INTEGER NOT NULL, chart INTEGER NOT NULL,
    notes INTEGER, source TEXT, updated_at INTEGER,
    PRIMARY KEY (musicdb_id, music_id, chart));
CREATE TABLE IF NOT EXISTS sound_scans (
    id INTEGER PRIMARY KEY, musicdb_id INTEGER, root TEXT, mods TEXT, songs INTEGER,
    charts INTEGER, skipped INTEGER, errors INTEGER, scanned_at INTEGER);

-- Best per chart as the server knows it (IIDX33music.getrank at login). Covers charts
-- played before the tracker existed; plays keep the detail, this only the best values.
CREATE TABLE IF NOT EXISTS server_bests (
    card_id TEXT, upstream TEXT, iidx_id INTEGER, music_id INTEGER, chart INTEGER,
    clear INTEGER, ex_score INTEGER, miss_count INTEGER, updated_at INTEGER, musicdb_id INTEGER,
    PRIMARY KEY (upstream, iidx_id, music_id, chart));

-- Cabinets (by PCBID, the srcid of every call) and the music data file tracker_link.dll reported
-- at their last login, which decides the music database of their plays until a login comes without
-- one (unreported_at). "altfix" holds the reporting DLL's version (the name is
-- older than tracker_link), dxtra whether 2dxtra was loaded.
CREATE TABLE IF NOT EXISTS machines (
    pcbid TEXT PRIMARY KEY, musicdb_id INTEGER, sha256 TEXT, filename TEXT, size INTEGER,
    altfix TEXT, game TEXT, remote TEXT, booted_at INTEGER, dxtra INTEGER, unreported_at INTEGER);

CREATE TABLE IF NOT EXISTS categories (
    id INTEGER PRIMARY KEY, name TEXT NOT NULL, color TEXT, created_at INTEGER);
CREATE TABLE IF NOT EXISTS category_songs (
    category_id INTEGER, music_id INTEGER, PRIMARY KEY (category_id, music_id));
` + customChartsSchema + musicFilesSchema + importSongsSchema + tierOverridesSchema

// importSongsSchema: the song list of every imported music data file, so that one import can be
// taken back (songs is rebuilt from the imports that remain, see DeleteImport).
const importSongsSchema = `
CREATE TABLE IF NOT EXISTS import_songs (
    import_id INTEGER NOT NULL, music_id INTEGER NOT NULL,
    title TEXT, title_ascii TEXT, genre TEXT, artist TEXT, subtitle TEXT, version INTEGER, levels TEXT,
    PRIMARY KEY (import_id, music_id));
`

// tierOverridesSchema: difficulty-table ranks changed on the tiers page, over the built-in snapshot
// (difficulty.go); a NULL label takes the snapshot's rank away.
const tierOverridesSchema = `
CREATE TABLE IF NOT EXISTS tier_overrides (
    kind TEXT NOT NULL, music_id INTEGER NOT NULL, chart INTEGER NOT NULL, label TEXT, value REAL,
    changed_at INTEGER, PRIMARY KEY (kind, music_id, chart));
`

// musicFilesSchema: every music data file tracker_link.dll reported, and the music database the
// player put it in when it was not one they had imported. Plays keep the file in music_hash; until
// the file has a database their musicdb_id stays empty and the UI asks which one it is.
const musicFilesSchema = `
CREATE TABLE IF NOT EXISTS music_files (
    sha256 TEXT PRIMARY KEY, filename TEXT, size INTEGER, musicdb_id INTEGER);
`

// customChartsSchema: the charts of an imported 2dxtra.sqlite (2dxtra generates them from the
// game's charts: Kiraku, Kichiku, All-Scratch). Plays on them carry chart_set, chart_hash (2dxtra's
// chart id, the SHA-256 of the chart) and chart_notes in plays.
const customChartsSchema = `
CREATE TABLE IF NOT EXISTS custom_charts (
    chart_set TEXT NOT NULL, set_order INTEGER, music_id INTEGER NOT NULL, chart INTEGER NOT NULL,
    hash TEXT NOT NULL, notes INTEGER, radar TEXT, imported_at INTEGER,
    PRIMARY KEY (chart_set, music_id, chart));
CREATE INDEX IF NOT EXISTS custom_charts_hash ON custom_charts (hash);
`

// A play that arrives this long after the login is not tied to it.
const sessionWindow = 12 * 3600

// Now is the clock the store uses (a variable so tests can move it).
var Now = func() int64 { return time.Now().Unix() }

// UserError is a mistake in the request (bad input) or a message meant for the user; the API
// answers 400 with its text in the page's language.
type UserError = i18n.Msg

type sessionKey struct {
	upstream string
	iidx     any // int64 or nil
}

// Store is the database. Safe for concurrent use.
type Store struct {
	db     *sql.DB
	mu     sync.Mutex // guards active and meta
	active map[sessionKey]int64
	meta   map[string]map[int64]SongMeta // per music database (see SongMetas)
	tiers  map[chartKey]map[string]Tier  // the ranks in use (see Tiers), nil until read
}

// Call is one recorded request: model, cabinet (PCBID = the call's srcid), module and method,
// the request and response module elements, the upstream origin and the time. Link is the
// <tracker_link> element tracker_link.dll added to the request (taken out before relaying).
type Call struct {
	Model, PCBID, Module, Method, Upstream string
	Req, Resp, Link                        *eamuse.Node
	TS                                     int64
}

// Open opens (and if needed creates) the database.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // one writer; also keeps transactions and reads in order
	for _, q := range []string{"PRAGMA journal_mode=WAL", "PRAGMA busy_timeout=5000"} {
		if _, err := db.Exec(q); err != nil {
			db.Close()
			return nil, err
		}
	}
	if err := upgrade(db, path); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db, active: map[sessionKey]int64{}, meta: map[string]map[int64]SongMeta{}}, nil
}

// The database's version is kept in SQLite's user_version:
//
//	0  from before versions: the Python tracker, or this one before 2026-09-26
//	1  2026-09-26: the tables in schema
//	2… one per step in migrations
//
// To change the tables later, append a step to migrations (never change one that has been
// released) and change schema to match, so that a new database starts out at the latest version:
//
//	// 1 -> 2: why the change
//	func(tx *sql.Tx) error {
//		_, err := tx.Exec("ALTER TABLE plays ADD COLUMN example TEXT")
//		return err
//	},
//
// Opening an older database first copies it next to itself (<file>.v<version>-<time>.bak), then
// runs the missing steps in order, each in one transaction that also records its version.
var migrations = []func(tx *sql.Tx) error{
	// 1 -> 2 (2026-09-26): plays on 2dxtra's charts (tracker_link.dll reports them in pc.save) and
	// the charts of an imported 2dxtra.sqlite
	func(tx *sql.Tx) error {
		if err := addColumns(tx, "plays", "chart_set TEXT", "chart_hash TEXT", "chart_notes INTEGER"); err != nil {
			return err
		}
		_, err := tx.Exec(customChartsSchema)
		return err
	},
	// 2 -> 3 (2026-09-26): the music data file (and 2dxtra) tracker_link.dll reports at login, kept
	// per play, per login and per file
	func(tx *sql.Tx) error {
		for table, cols := range map[string][]string{
			"plays":    {"music_hash TEXT", "dxtra INTEGER"},
			"sessions": {"music_hash TEXT", "dxtra INTEGER"},
			"machines": {"dxtra INTEGER", "unreported_at INTEGER"},
		} {
			if err := addColumns(tx, table, cols...); err != nil {
				return err
			}
		}
		_, err := tx.Exec(musicFilesSchema)
		return err
	},
	// 3 -> 4 (2026-09-26): the song list of every import, so that one import can be taken back
	func(tx *sql.Tx) error {
		if _, err := tx.Exec(importSongsSchema); err != nil {
			return err
		}
		// ponytail: an earlier import keeps only the songs it was the last to carry (the latest import
		// of a database keeps all of its songs), so taking back a later one leaves it incomplete - import
		// that file again then
		_, err := tx.Exec("INSERT OR IGNORE INTO import_songs SELECT last_import, music_id, title, title_ascii, genre, " +
			"artist, subtitle, version, levels FROM songs WHERE last_import IS NOT NULL")
		return err
	},
	// 4 -> 5 (2026-09-27): which play's chart set a recorded notes radar belongs to (see radarSource)
	func(tx *sql.Tx) error {
		if err := addColumns(tx, "sessions", "radar_sp_play INTEGER", "radar_dp_play INTEGER"); err != nil {
			return err
		}
		// earlier logins: a credit saved the radar of its last play's set; a login shows what the
		// credit before saved (ponytail: both styles alike, the save only carries those that changed)
		rows, err := rowsOf(tx, `SELECT s.id, s.upstream, s.iidx_id, s.saved_at,
			(SELECT p.id FROM plays p WHERE p.session_id = s.id ORDER BY p.played_at DESC, p.id DESC LIMIT 1) AS last
			FROM sessions s ORDER BY s.upstream, s.iidx_id, s.started_at, s.id`)
		if err != nil {
			return err
		}
		carry := map[string]any{}
		for _, r := range rows {
			k := fmt.Sprint(r["upstream"], "|", r["iidx_id"])
			if r["saved_at"] != nil {
				carry[k] = r["last"]
			}
			if _, err := tx.Exec("UPDATE sessions SET radar_sp_play = ?, radar_dp_play = ? WHERE id = ?", carry[k], carry[k], r["id"]); err != nil {
				return err
			}
		}
		return nil
	},	// 5 -> 6 (2026-09-27): difficulty-table ranks changed in the tracker
	func(tx *sql.Tx) error {
		_, err := tx.Exec(tierOverridesSchema)
		return err
	},
}

// addColumns adds the columns a table does not have yet (a database made from the latest schema
// already has them: tests make "old" databases that way).
func addColumns(tx *sql.Tx, table string, cols ...string) error {
	have, err := columns(tx, table)
	if err != nil {
		return err
	}
	for _, c := range cols {
		if name, _, _ := strings.Cut(c, " "); !have[name] {
			if _, err := tx.Exec("ALTER TABLE " + table + " ADD COLUMN " + c); err != nil {
				return err
			}
		}
	}
	return nil
}

// LatestVersion is the database version this build writes.
func LatestVersion() int { return 1 + len(migrations) }

// upgrade creates a new database at the latest version, or brings an older one up to it.
func upgrade(db *sql.DB, path string) error {
	var version, tables int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type = 'table'").Scan(&tables); err != nil {
		return err
	}
	latest := LatestVersion()
	switch {
	case version > latest:
		return i18n.Errorf("データベースは新しい版のトラッカーで作られています (形式 %d、この版は %d まで)。新しい版のトラッカーを使ってください",
			"the database was written by a newer tracker (format %d; this one knows up to %d); use a newer tracker", version, latest)
	case tables == 0: // new
		if _, err := db.Exec(schema); err != nil {
			return err
		}
		_, err := db.Exec(fmt.Sprintf("PRAGMA user_version = %d", latest))
		return err
	case version == latest:
		return nil
	}

	backup := fmt.Sprintf("%s.v%d-%s.bak", path, version, time.Now().Format("20060102-150405"))
	if _, err := db.Exec("VACUUM INTO ?", backup); err != nil {
		return i18n.Errorf("更新前のバックアップを作れません (%s): %v", "cannot back up the database before the upgrade (%s): %v", backup, err)
	}
	log.Printf(i18n.L("データベースを形式 %d から %d に更新します (更新前の写し: %s)",
		"upgrading the database from format %d to %d (copy before the upgrade: %s)"), version, latest, backup)

	if version == 0 {
		if err := upgradeLegacy(db); err != nil {
			return err
		}
		if _, err := db.Exec("PRAGMA user_version = 1"); err != nil {
			return err
		}
		version = 1
	}
	for ; version < latest; version++ {
		err := func() error {
			tx, err := db.Begin()
			if err != nil {
				return err
			}
			defer tx.Rollback()
			if err := migrations[version-1](tx); err != nil {
				return err
			}
			if _, err := tx.Exec(fmt.Sprintf("PRAGMA user_version = %d", version+1)); err != nil {
				return err
			}
			return tx.Commit()
		}()
		if err != nil {
			return i18n.Errorf("データベースを形式 %d から %d に更新できません: %v", "cannot upgrade the database from format %d to %d: %v", version, version+1, err)
		}
	}
	return nil
}

// upgradeLegacy brings a database from before versions (version 0) to version 1: it creates the
// tables and adds the columns introduced after it was made. Old rows keep NULL: the values come
// from traffic that was not stored. Every step is safe to repeat.
func upgradeLegacy(db *sql.DB) error {
	if _, err := db.Exec(schema); err != nil {
		return err
	}
	cols, err := columns(db, "plays")
	if err != nil {
		return err
	}
	for _, c := range []string{"mode_type", "chatter"} {
		if !cols[c] { // added by the Python tracker in 2026-09; its own migration fills old rows
			return i18n.Errorf("データベースが古い形式です (plays.%s がありません)。Python 版で一度開いて更新してください",
				"the database is too old (plays.%s is missing); open it once with the Python tracker to update it", c)
		}
	}
	added := []struct{ table, column, kind string }{
		{"sessions", "djpoint_sp", "INTEGER"}, // 2026-09-26: DJ POINT the game computed (pc.get sach/dach, pc.save s_achi/d_achi)
		{"sessions", "djpoint_dp", "INTEGER"},
		{"sessions", "pcbid", "TEXT"}, // 2026-09-26: which cabinet and which music database
		{"sessions", "musicdb_id", "INTEGER"},
		{"plays", "pcbid", "TEXT"},
		{"plays", "musicdb_id", "INTEGER"},
		{"server_bests", "musicdb_id", "INTEGER"},
		{"plays", "judge_timing", "TEXT"}, // 2026-09-26: judgments by timing and by key (tracker_link.dll)
		{"plays", "judge_lanes", "TEXT"},
		{"plays", "judge_measures", "TEXT"}, // 2026-09-26: score rate per measure (tracker_link.dll)
	}
	newDBColumn := false
	for _, a := range added {
		cols, err := columns(db, a.table)
		if err != nil {
			return err
		}
		if !cols[a.column] {
			if _, err := db.Exec(fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", a.table, a.column, a.kind)); err != nil {
				return err
			}
			newDBColumn = newDBColumn || (a.table == "plays" && a.column == "musicdb_id")
		}
	}
	if err := assignUnknownDBs(db); err != nil {
		return err
	}
	// scans before 2026-09-26 stored the chart's file as the source; the source is the kind now
	if _, err := db.Exec("UPDATE chart_notes_db SET source = 'analysis' WHERE source NOT IN ('analysis', 'import', 'observed')"); err != nil {
		return err
	}
	if newDBColumn {
		// counts learned from plays belonged to no database; learn them again per database
		if _, err := db.Exec(`INSERT OR IGNORE INTO chart_notes_db
			SELECT musicdb_id, music_id, chart, MAX(pgreat + great + good + miss_count), 'observed', MAX(played_at)
			FROM plays WHERE musicdb_id IS NOT NULL AND progress = 10000 AND COALESCE(is_death, 0) = 0
			  AND good IS NOT NULL AND miss_count >= 0
			GROUP BY musicdb_id, music_id, chart`); err != nil {
			return err
		}
	}
	return nil
}

// assignUnknownDBs gives rows recorded without a known music database the one that fits them:
// the newest lineage of the play's kind (omnimix or not) and game version; server bests take the
// database of the player's latest login. Run when a database from before versions is upgraded
// and after a music database is imported.
func assignUnknownDBs(db interface {
	Exec(string, ...any) (sql.Result, error)
}) error {
	fit := `(SELECT id FROM musicdbs m WHERE m.kind = CASE %[1]s.omni WHEN 1 THEN 'omni' ELSE 'vanilla' END
		AND m.game_version = %[1]s.game_version ORDER BY id DESC LIMIT 1)`
	for _, q := range []string{
		fmt.Sprintf("UPDATE plays SET musicdb_id = "+fit+" WHERE musicdb_id IS NULL AND music_hash IS NULL", "plays"),
		fmt.Sprintf("UPDATE sessions SET musicdb_id = "+fit+" WHERE musicdb_id IS NULL AND music_hash IS NULL", "sessions"),
		`UPDATE server_bests SET musicdb_id = (SELECT musicdb_id FROM sessions s WHERE s.upstream = server_bests.upstream
			AND s.iidx_id = server_bests.iidx_id AND s.musicdb_id IS NOT NULL ORDER BY started_at DESC LIMIT 1)
			WHERE musicdb_id IS NULL`,
	} {
		if _, err := db.Exec(q); err != nil {
			return err
		}
	}
	return nil
}

func columns(db queryer, table string) (map[string]bool, error) {
	rows, err := db.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var cid int
		var name, kind string
		var notnull, pk int
		var dflt any
		if err := rows.Scan(&cid, &name, &kind, &notnull, &dflt, &pk); err != nil {
			return nil, err
		}
		out[name] = true
	}
	return out, rows.Err()
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

// ---- helpers -------------------------------------------------------------------

type queryer interface {
	Query(string, ...any) (*sql.Rows, error)
}

// Row is one result row keyed by column name (INTEGER -> int64, TEXT -> string, NULL -> nil).
type Row = map[string]any

func rowsOf(q queryer, query string, args ...any) ([]Row, error) {
	rs, err := q.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	cols, _ := rs.Columns()
	var out []Row
	for rs.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rs.Scan(ptrs...); err != nil {
			return nil, err
		}
		r := make(Row, len(cols))
		for i, c := range cols {
			r[c] = vals[i]
		}
		out = append(out, r)
	}
	if out == nil {
		out = []Row{}
	}
	return out, rs.Err()
}

// Rows runs a query and returns every row.
func (s *Store) Rows(query string, args ...any) ([]Row, error) { return rowsOf(s.db, query, args...) }

// Row runs a query and returns the first row, or nil.
func (s *Store) Row(query string, args ...any) (Row, error) {
	r, err := s.Rows(query, args...)
	if err != nil || len(r) == 0 {
		return nil, err
	}
	return r[0], nil
}

// Exec runs a statement and returns the last inserted row id.
func (s *Store) Exec(query string, args ...any) (int64, error) {
	res, err := s.db.Exec(query, args...)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// num parses an integer like Python's int(); nil when it is not one.
func num(s string, ok bool) any {
	if !ok {
		return nil
	}
	v, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil {
		return nil
	}
	return v
}

func attr(n *eamuse.Node, name string) any {
	if n == nil {
		return nil
	}
	return num(n.Attr(name))
}

func asInt(v any) (int64, bool) {
	switch x := v.(type) {
	case int64:
		return x, true
	case float64:
		return int64(x), true
	}
	return 0, false
}

func ptr(v any) *int64 {
	if x, ok := asInt(v); ok {
		return &x
	}
	return nil
}

func orZero(v any) int64 {
	x, _ := asInt(v)
	return x
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func binText(n *eamuse.Node) any {
	if n == nil || n.Text == "" {
		return nil
	}
	b, err := hex.DecodeString(strings.TrimSpace(n.Text))
	if err != nil {
		return nil
	}
	return b
}

// linkInts reads a bin element tracker_link.dll added to music.reg as n little-endian int32,
// stored as space-separated numbers:
//
//	judge  [2 lane sides][11] judgments by display code: FAST POOR, FAST BAD, FAST GOOD,
//	       FAST GREAT, PGREAT, SLOW GREAT, SLOW GOOD, SLOW BAD, SLOW POOR, 2 unused
//	       (g_DeadState+0x94, analysis_0819 playanalyze4.md §2.2)
//	lane   [2 lane sides][8 lanes: keys 1-7, scratch][PG, GR, GD, BD, POOR, empty POOR]
//	       (g_ScoreBlock lane table, playanalyze4.md §4.4)
//
// A lane side is the 1P or 2P half of the cabinet: an SP play uses its own, a DP play both.
func linkInts(link *eamuse.Node, name string, n int) any {
	if link == nil {
		return nil
	}
	b, _ := binText(link.Find(name)).([]byte)
	if len(b) != n*4 {
		return nil
	}
	out := make([]string, n)
	for i := range out {
		out[i] = strconv.Itoa(int(int32(binary.LittleEndian.Uint32(b[4*i:]))))
	}
	return strings.Join(out, " ")
}

// linkMeasures reads the <measure> element tracker_link.dll adds to music.reg: the score rate of
// every measure (float32, 0..1, -1 = no notes; StageResultDrawGraph::CalcMeasureValue, the values
// the result screen's graph shows), stored as space-separated numbers.
func linkMeasures(link *eamuse.Node) any {
	if link == nil {
		return nil
	}
	b, _ := binText(link.Find("measure")).([]byte)
	if len(b) == 0 || len(b)%4 != 0 {
		return nil
	}
	out := make([]string, len(b)/4)
	for i := range out {
		v := math.Float32frombits(binary.LittleEndian.Uint32(b[4*i:]))
		out[i] = strconv.FormatFloat(float64(v), 'f', 4, 32)
	}
	return strings.Join(out, " ")
}

// chatter reads music_play_log/chattering_log: chattering_0..13 = 1P keys 1-7, 2P keys 1-7, each
// s32[8] = presses that came 1..8 frames after the previous press of the same key
// (Judge_CountChatter, counted on the Lightning Model only). Stored as 112 space-separated numbers.
func chatter(log *eamuse.Node) any {
	c := log.Find("chattering_log")
	if c == nil {
		return nil
	}
	var all []string
	for i := 0; i < 14; i++ {
		t, _ := c.FindText(fmt.Sprintf("chattering_%d", i))
		row := strings.Fields(t)
		if len(row) != 8 {
			return nil
		}
		all = append(all, row...)
	}
	return strings.Join(all, " ")
}

// OriginID returns the id of an upstream origin, registering it first.
func (s *Store) OriginID(origin string) (int64, error) {
	if _, err := s.db.Exec("INSERT OR IGNORE INTO origins (origin) VALUES (?)", origin); err != nil {
		return 0, err
	}
	var id int64
	err := s.db.QueryRow("SELECT id FROM origins WHERE origin = ?", origin).Scan(&id)
	return id, err
}

// Origin returns the origin with that id, or "".
func (s *Store) Origin(id int64) string {
	var o string
	s.db.QueryRow("SELECT origin FROM origins WHERE id = ?", id).Scan(&o)
	return o
}

// ---- recording -------------------------------------------------------------------

// Ingest records a call the tracker relayed.
func (s *Store) Ingest(c *Call) error {
	model := strings.Split(c.Model, ":")
	if model[0] != "LDJ" || !strings.HasPrefix(c.Module, "IIDX") || len(c.Module) < 7 {
		return nil
	}
	version := num(c.Module[4:6], true)
	cab := s.CabinetFor(c.PCBID, c.Model, version)
	switch c.Module[6:] + "." + c.Method {
	case "pc.get":
		return s.pcGet(c, version, cab)
	case "music.reg":
		return s.musicReg(c, version, cab, nil)
	case "pc.save":
		if err := s.pcSave(c); err != nil {
			return err
		}
		if err := s.customPlays(c, version, cab); err != nil {
			return err
		}
		return s.radarSource(c)
	case "music.getrank":
		return s.musicGetrank(c, cab.DB)
	}
	return nil
}

func (s *Store) sessionFor(q queryer, upstream string, iidx any) (Row, error) {
	s.mu.Lock()
	sid, ok := s.active[sessionKey{upstream, iidx}]
	s.mu.Unlock()
	if !ok {
		r, err := rowsOf(q, "SELECT id FROM sessions WHERE upstream = ? AND iidx_id = ? AND started_at > ? "+
			"ORDER BY id DESC LIMIT 1", upstream, iidx, Now()-sessionWindow)
		if err != nil || len(r) == 0 {
			return nil, err
		}
		sid = r[0]["id"].(int64)
	}
	r, err := rowsOf(q, "SELECT * FROM sessions WHERE id = ?", sid)
	if err != nil || len(r) == 0 {
		return nil, err
	}
	return r[0], nil
}

func (s *Store) tx(fn func(*sql.Tx) error) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

func insertRow(tx *sql.Tx, table string, cols []string, vals []any) (int64, error) {
	q := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)", table, strings.Join(cols, ", "),
		strings.TrimSuffix(strings.Repeat("?, ", len(cols)), ", "))
	res, err := tx.Exec(q, vals...)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) pcGet(c *Call, version any, cab Cabinet) error {
	pc := c.Resp.Find("pcdata")
	if pc == nil {
		return nil // unregistered card or an error response
	}
	card := nullStr(c.Req.Get("cid"))
	iidx := attr(pc, "id")
	grade := c.Resp.Find("grade")
	radar := map[string]any{}
	for _, r := range c.Resp.FindAll("notes_radar") {
		if t, ok := r.FindText("radar_score"); ok {
			radar[r.Get("style")] = t
		}
	}
	arena := map[string]any{}
	for _, a := range c.Resp.FindAll("arena_data/achieve_data") {
		arena[a.Get("play_style")] = attr(a, "arena_class")
	}
	refid := nullStr(c.Req.Get("rid"))
	identCols := []string{"card_id", "upstream", "refid", "iidx_id", "name", "pid", "sgid", "dgid"}
	name, hasName := pc.Attr("name")
	var nameVal any
	if hasName {
		nameVal = name
	}
	ident := []any{card, c.Upstream, refid, iidx, nameVal, attr(pc, "pid"), attr(grade, "sgid"), attr(grade, "dgid")}

	var sid int64
	err := s.tx(func(tx *sql.Tx) error {
		if card != nil {
			if _, err := tx.Exec("INSERT OR IGNORE INTO cards VALUES (?, ?, ?)", card, c.TS, c.TS); err != nil {
				return err
			}
			if _, err := tx.Exec("UPDATE cards SET last_seen = ? WHERE card_id = ?", c.TS, card); err != nil {
				return err
			}
		}
		last, err := rowsOf(tx, "SELECT * FROM profiles WHERE card_id IS ? AND upstream = ? AND iidx_id IS ? "+
			"ORDER BY id DESC LIMIT 1", card, c.Upstream, iidx)
		if err != nil {
			return err
		}
		var profileID int64
		same := len(last) > 0
		for i, col := range identCols {
			if same && fmt.Sprint(last[0][col]) != fmt.Sprint(ident[i]) {
				same = false
			}
		}
		if same {
			profileID = last[0]["id"].(int64)
			if _, err := tx.Exec("UPDATE profiles SET last_seen = ? WHERE id = ?", c.TS, profileID); err != nil {
				return err
			}
		} else if profileID, err = insertRow(tx, "profiles", append(identCols, "first_seen", "last_seen"),
			append(ident, c.TS, c.TS)); err != nil {
			return err
		}
		// the radar the server returns is the one the credit before saved (the server does not compute it)
		before, err := rowsOf(tx, "SELECT radar_sp_play, radar_dp_play FROM sessions WHERE upstream = ? AND iidx_id = ? "+
			"ORDER BY started_at DESC, id DESC LIMIT 1", c.Upstream, iidx)
		if err != nil {
			return err
		}
		radarPlay := Row{}
		if len(before) > 0 {
			radarPlay = before[0]
		}
		sid, err = insertRow(tx, "sessions",
			[]string{"card_id", "upstream", "refid", "iidx_id", "profile_id", "model", "game_version", "omni",
				"sp_plays", "dp_plays", "arena_sp", "arena_dp", "radar_sp", "radar_dp", "started_at",
				"djpoint_sp", "djpoint_dp", "pcbid", "musicdb_id", "music_hash", "dxtra", "radar_sp_play", "radar_dp_play"},
			[]any{card, c.Upstream, refid, iidx, profileID, c.Model, version, cab.Omni,
				attr(pc, "spnum"), attr(pc, "dpnum"), arena["0"], arena["1"], radar["0"], radar["1"], c.TS,
				attr(pc, "sach"), attr(pc, "dach"), nullStr(c.PCBID), cab.DB, cab.Hash, cab.Dxtra,
				radarPlay["radar_sp_play"], radarPlay["radar_dp_play"]})
		if err != nil {
			return err
		}
		// Plays recorded before the tracker saw this login belong to this card.
		if card != nil {
			_, err = tx.Exec("UPDATE plays SET card_id = ?, session_id = COALESCE(session_id, ?) "+
				"WHERE card_id IS NULL AND upstream = ? AND iidx_id = ?", card, sid, c.Upstream, iidx)
		}
		return err
	})
	if err == nil {
		s.mu.Lock()
		s.active[sessionKey{c.Upstream, iidx}] = sid
		s.mu.Unlock()
	}
	return err
}

// musicReg records a play from its music.reg request; custom names the 2dxtra chart it was on
// (the request is then one customMusicReg made).
func (s *Store) musicReg(c *Call, version any, cab Cabinet, custom *CustomChart) error {
	m := c.Req
	musicID, chart := attr(m, "mid"), attr(m, "clid")
	if musicID == nil || chart == nil {
		return nil
	}
	iidx := attr(m, "iidxid")
	log, best := m.Find("music_play_log"), m.Find("best_result")
	pgreat, great := orZero(attr(m, "pgnum")), orZero(attr(m, "gnum"))
	session, err := s.sessionFor(s.db, c.Upstream, iidx)
	if err != nil {
		return err
	}
	get := func(k string) any {
		if session == nil {
			return nil
		}
		return session[k]
	}
	pick := func(fromLog string, otherwise any) any {
		if log != nil {
			return attr(log, fromLog)
		}
		return otherwise
	}
	styleDefault := int64(0)
	if chart.(int64) >= 5 {
		styleDefault = 1
	}
	var raw bytes.Buffer
	zw := zlib.NewWriter(&raw)
	zw.Write(m.XML())
	zw.Close()
	var chatterLog *eamuse.Node
	if log != nil {
		chatterLog = log.Find("chattering_log")
	}
	var chatterText any
	if log != nil {
		chatterText = chatter(log)
	}
	cols := []string{"session_id", "card_id", "upstream", "iidx_id", "played_at", "model", "game_version", "omni",
		"cabinet", "music_id", "chart", "level", "clear", "ex_score", "pgreat", "great", "good", "bad", "poor",
		"combo_break", "fast", "slow", "miss_count", "dj_level", "gauge_type", "mode_type", "option1", "option2",
		"ran_arrange", "play_style", "play_side", "progress", "is_death", "prev_best_score", "prev_best_clear",
		"prev_best_miss", "ghost", "ghost_gauge", "raw", "chatter", "pcbid", "musicdb_id",
		"music_hash", "dxtra", "judge_timing", "judge_lanes", "judge_measures", "chart_set", "chart_hash", "chart_notes"}
	vals := []any{get("id"), get("card_id"), c.Upstream, iidx, c.TS, c.Model, version, cab.Omni,
		get("cabinet"), musicID, chart, attr(m, "mlevel"), attr(m, "cflg"),
		pick("ex_score", pgreat*2+great), pgreat, great,
		attr(best, "now_good"), attr(best, "now_bad"), attr(best, "now_poor"), attr(best, "now_combo"),
		attr(best, "now_fast"), attr(best, "now_slow"), attr(m, "mnum"), attr(m, "dj_level"),
		attr(log, "gauge_type"), attr(log, "mode_type"),
		pick("option1", attr(m, "opt")), pick("option2", attr(m, "opt2")),
		attr(log, "ran_arrange"), pick("play_style", styleDefault), attr(m, "pside"),
		attr(chatterLog, "progress"), attr(m, "is_death"),
		attr(best, "best_score"), attr(best, "best_clear"), attr(best, "best_misscount"),
		binText(m.Find("ghost")), binText(m.Find("ghost_gauge")), raw.Bytes(), chatterText, nullStr(c.PCBID), cab.DB,
		cab.Hash, cab.Dxtra, linkInts(c.Link, "judge", 22), linkInts(c.Link, "lane", 96), linkMeasures(c.Link), nil, nil, nil}
	if custom != nil {
		n := len(vals)
		vals[n-3], vals[n-2], vals[n-1] = custom.Set, custom.Hash, custom.Notes
	}
	play := map[string]any{}
	for i, col := range cols {
		play[col] = vals[i]
	}
	return s.tx(func(tx *sql.Tx) error {
		if _, err := insertRow(tx, "plays", cols, vals); err != nil {
			return err
		}
		if custom != nil {
			return nil // a 2dxtra chart's notes say nothing about the game's chart
		}
		if cab.DB == nil && cab.Hash != nil {
			return nil // ponytail: nothing is learned while the file's database is unknown; relearn on assignment if it matters
		}
		return observeNotes(tx, cab.DB, musicID, chart, play)
	})
}

// observeNotes learns a chart's note count from a finished play, which judged every note exactly
// once: PG+GR+GD+BAD+missed POOR. MISS COUNT is BAD+missed POOR (empty POORs excluded). It is kept
// for the play's music database and never replaces a count read from the chart or imported.
func observeNotes(tx *sql.Tx, db, musicID, chart any, p map[string]any) error {
	progress, _ := asInt(p["progress"])
	death, _ := asInt(p["is_death"])
	miss, hasMiss := asInt(p["miss_count"])
	good, hasGood := asInt(p["good"])
	if progress != 10000 || death != 0 || !hasGood || !hasMiss || miss < 0 {
		return nil
	}
	notes := orZero(p["pgreat"]) + orZero(p["great"]) + good + miss
	table, key, args := "chart_notes", "music_id = ? AND chart = ?", []any{musicID, chart}
	if db != nil {
		table, key, args = "chart_notes_db", "musicdb_id = ? AND music_id = ? AND chart = ?", []any{db, musicID, chart}
	}
	known, err := rowsOf(tx, "SELECT notes, source FROM "+table+" WHERE "+key, args...)
	if err != nil {
		return err
	}
	// Assist options remove notes, so the largest observation is the real count.
	if len(known) == 0 || (known[0]["source"] == "observed" && notes > orZero(known[0]["notes"])) {
		if db != nil {
			_, err = tx.Exec("INSERT OR REPLACE INTO chart_notes_db VALUES (?, ?, ?, ?, 'observed', ?)",
				db, musicID, chart, notes, p["played_at"])
		} else {
			_, err = tx.Exec("INSERT OR REPLACE INTO chart_notes VALUES (?, ?, ?, 'observed', ?)",
				musicID, chart, notes, p["played_at"])
		}
	}
	return err
}

func (s *Store) pcSave(c *Call) error {
	iidx := attr(c.Req, "iidxid")
	// The Lightning Model sends its own play data block; the standard cabinet does not.
	cabinet := "LDJ"
	if c.Req.Find("lightning_play_data") != nil {
		cabinet = "TDJ"
	}
	session, err := s.sessionFor(s.db, c.Upstream, iidx)
	if err != nil || session == nil {
		return err
	}
	// The DJ POINT and notes radar the game computed again at the end of the credit
	// (DjPoint_CalcTotal; the radar only for the styles that changed).
	radar := map[string]any{}
	for _, r := range c.Req.FindAll("notes_radar") {
		if t, ok := r.FindText("radar_score"); ok {
			radar[r.Get("style")] = t
		}
	}
	return s.tx(func(tx *sql.Tx) error {
		if _, err := tx.Exec("UPDATE sessions SET cabinet = ?, saved_at = ?, "+
			"djpoint_sp = COALESCE(?, djpoint_sp), djpoint_dp = COALESCE(?, djpoint_dp), "+
			"radar_sp = COALESCE(?, radar_sp), radar_dp = COALESCE(?, radar_dp) WHERE id = ?",
			cabinet, c.TS, attr(c.Req, "s_achi"), attr(c.Req, "d_achi"), radar["0"], radar["1"], session["id"]); err != nil {
			return err
		}
		_, err := tx.Exec("UPDATE plays SET cabinet = ? WHERE session_id = ?", cabinet, session["id"])
		return err
	})
}

// radarSource notes which play decided the notes radar a pc.save carries. The game computes the
// radar again at every result (CPlayerNotesRadarGameData::Recalculate) from the radar values and
// scores of the charts in use - 2dxtra swaps both for its set's - so what a credit saves belongs to
// the chart set of its last play; with no play, to the game's charts (computed on leaving the mode
// select, where no set is active).
func (s *Store) radarSource(c *Call) error {
	cols := map[string]bool{} // the styles whose radar the save carries
	for _, r := range c.Req.FindAll("notes_radar") {
		if col, ok := map[string]string{"0": "radar_sp_play", "1": "radar_dp_play"}[r.Get("style")]; ok {
			cols[col] = true
		}
	}
	if len(cols) == 0 {
		return nil
	}
	session, err := s.sessionFor(s.db, c.Upstream, attr(c.Req, "iidxid"))
	if err != nil || session == nil {
		return err
	}
	last, err := rowsOf(s.db, "SELECT id FROM plays WHERE session_id = ? ORDER BY played_at DESC, id DESC LIMIT 1", session["id"])
	if err != nil {
		return err
	}
	var play any
	if len(last) > 0 {
		play = last[0]["id"]
	}
	for col := range cols {
		if _, err := s.Exec("UPDATE sessions SET "+col+" = ? WHERE id = ?", play, session["id"]); err != nil {
			return err
		}
	}
	return nil
}

// musicGetrank stores the player's own bests. Response: <style type="0|1"/> and one <m> s32[17]
// per song: rival (-1 = the player), music id, clear[5], ex score[5], miss count[5]; the five
// charts are B/N/H/A/L of the requested style (Xrpc_MusicGetrank_Parse).
func (s *Store) musicGetrank(c *Call, db any) error {
	if c.Resp == nil {
		return nil
	}
	iidx := attr(c.Req, "iidxid")
	style := attr(c.Resp.Find("style"), "type")
	if style == nil {
		style = attr(c.Req, "cltype")
	}
	st, ok := asInt(style)
	if iidx == nil || !ok || (st != 0 && st != 1) {
		return nil
	}
	session, err := s.sessionFor(s.db, c.Upstream, iidx)
	if err != nil {
		return err
	}
	var card any
	if session != nil {
		card = session["card_id"]
	}
	return s.tx(func(tx *sql.Tx) error {
		for _, m := range c.Resp.FindAll("m") {
			fields := strings.Fields(m.Text)
			v := make([]any, len(fields))
			for i, f := range fields {
				v[i] = num(f, true)
			}
			if len(v) < 17 || v[0] != int64(-1) {
				continue // rivals are not stored
			}
			for i := 0; i < 5; i++ {
				clear, ex, miss := v[2+i], v[7+i], v[12+i]
				if orZero(clear) == 0 && orZero(ex) == 0 {
					continue // never played
				}
				if x, ok := asInt(miss); !ok || x < 0 {
					miss = nil
				}
				if _, err := tx.Exec("INSERT OR REPLACE INTO server_bests VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
					card, c.Upstream, iidx, v[1], st*5+int64(i), clear, ex, miss, c.TS, db); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

// ---- music database ----------------------------------------------------------------

// ImportMusicDB imports a music_data.bin into a lineage: target "auto" picks the lineage of
// the same version and kind with the most songs in common, "new" starts one, a number names one.
func (s *Store) ImportMusicDB(filename string, data []byte, target, name string) (map[string]any, error) {
	md, err := ParseMusicData(data)
	if err != nil {
		return nil, err // an i18n message: the API answers 400
	}
	kind := "vanilla"
	if md.Sealed || strings.Contains(strings.ToLower(filename), "omni") {
		kind = "omni"
	}
	sum := sha256.Sum256(data)
	digest := hex.EncodeToString(sum[:])
	now := Now()
	var result map[string]any
	err = s.tx(func(tx *sql.Tx) error {
		var dbid any
		if target != "new" {
			if dbid = num(target, true); dbid == nil {
				return i18n.New("取り込み先の曲DB を選んでください", "Pick the music DB to import into")
			}
			found, err := rowsOf(tx, "SELECT 1 FROM musicdbs WHERE id = ?", dbid)
			if err != nil {
				return err
			}
			if len(found) == 0 {
				return i18n.New("指定された曲DBが存在しません", "No such music database")
			}
		} else {
			n := strings.TrimSpace(name)
			if n == "" { // the same in every language: "IIDX33", "IIDX33 omni"
				n = fmt.Sprintf("IIDX%d", md.Version)
				if kind == "omni" {
					n += " omni"
				}
			}
			id, err := insertRow(tx, "musicdbs", []string{"name", "kind", "game_version", "created_at"},
				[]any{n, kind, md.Version, now})
			if err != nil {
				return err
			}
			dbid = id
		}
		latest, err := rowsOf(tx, "SELECT sha256 FROM musicdb_imports WHERE musicdb_id = ? ORDER BY id DESC LIMIT 1", dbid)
		if err != nil {
			return err
		}
		if len(latest) > 0 && latest[0]["sha256"] == digest {
			result = map[string]any{"musicdb_id": dbid, "duplicate": true,
				"songs": len(md.Songs), "added": 0, "removed": 0}
			return errDuplicate
		}
		imp, err := insertRow(tx, "musicdb_imports",
			[]string{"musicdb_id", "filename", "sha256", "song_count", "imported_at"},
			[]any{dbid, filename, digest, len(md.Songs), now})
		if err != nil {
			return err
		}
		for _, sg := range md.Songs {
			levels := make([]string, len(sg.Levels))
			for i, l := range sg.Levels {
				levels[i] = strconv.FormatInt(l, 10)
			}
			if _, err := tx.Exec("INSERT OR REPLACE INTO import_songs VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
				imp, sg.ID, sg.Text["title"], sg.Text["title_ascii"], sg.Text["genre"], sg.Text["artist"],
				sg.Text["subtitle"], sg.Version, "["+strings.Join(levels, ", ")+"]"); err != nil {
				return err
			}
		}
		added, removed, err := applyImport(tx, dbid, imp)
		if err != nil {
			return err
		}
		result = map[string]any{"musicdb_id": dbid, "duplicate": false,
			"songs": len(md.Songs), "added": added, "removed": removed}
		return nil
	})
	if errors.Is(err, errDuplicate) {
		err = nil
	}
	if err != nil {
		return nil, err
	}
	s.ForgetSongs()
	if err := assignUnknownDBs(s.db); err != nil {
		return nil, err
	}
	// cabinets that reported this file before it was imported, and their plays, now have their music database
	if err := s.AssignMusicFile(digest, result["musicdb_id"]); err != nil {
		return nil, err
	}
	return result, nil
}

var errDuplicate = errors.New("duplicate import")

// applyImport lays an import's song list over a music database's songs: its songs take its values,
// the database's songs it lacks are marked removed. It records and returns how many songs it added
// and removed.
func applyImport(tx *sql.Tx, db, imp any) (added, removed int, err error) {
	if err = tx.QueryRow(`SELECT
		(SELECT COUNT(*) FROM import_songs i WHERE i.import_id = ?1 AND NOT EXISTS
			(SELECT 1 FROM songs s WHERE s.musicdb_id = ?2 AND s.music_id = i.music_id AND s.removed = 0)),
		(SELECT COUNT(*) FROM songs s WHERE s.musicdb_id = ?2 AND s.removed = 0 AND s.music_id NOT IN
			(SELECT music_id FROM import_songs WHERE import_id = ?1))`, imp, db).Scan(&added, &removed); err != nil {
		return
	}
	for _, q := range []string{
		"UPDATE songs SET removed = 1 WHERE musicdb_id = ?2 AND music_id NOT IN (SELECT music_id FROM import_songs WHERE import_id = ?1)",
		"INSERT OR REPLACE INTO songs SELECT ?2, music_id, title, title_ascii, genre, artist, subtitle, version, levels, ?1, 0 " +
			"FROM import_songs WHERE import_id = ?1",
	} {
		if _, err = tx.Exec(q, imp, db); err != nil {
			return
		}
	}
	_, err = tx.Exec("UPDATE musicdb_imports SET added = ?, removed = ? WHERE id = ?", added, removed, imp)
	return
}

// DeleteImport takes back one import of a music database: its songs are rebuilt from the imports
// that remain. When no import of the database carries the file any more, the file is no longer the
// database's: cabinets reporting it and the plays recorded on it wait to be assigned again.
func (s *Store) DeleteImport(id any) error {
	r, err := s.Row("SELECT musicdb_id, sha256 FROM musicdb_imports WHERE id = ?", id)
	if err != nil {
		return err
	}
	if r == nil {
		return i18n.New("指定された取り込みがありません", "No such import")
	}
	db, sha := r["musicdb_id"], r["sha256"]
	err = s.tx(func(tx *sql.Tx) error {
		for _, q := range []string{"DELETE FROM import_songs WHERE import_id = ?", "DELETE FROM musicdb_imports WHERE id = ?"} {
			if _, err := tx.Exec(q, id); err != nil {
				return err
			}
		}
		if _, err := tx.Exec("DELETE FROM songs WHERE musicdb_id = ?", db); err != nil {
			return err
		}
		imports, err := rowsOf(tx, "SELECT id FROM musicdb_imports WHERE musicdb_id = ? ORDER BY id", db)
		if err != nil {
			return err
		}
		for _, imp := range imports {
			if _, _, err := applyImport(tx, db, imp["id"]); err != nil {
				return err
			}
		}
		if still, err := rowsOf(tx, "SELECT 1 FROM musicdb_imports WHERE musicdb_id = ? AND sha256 = ?", db, sha); err != nil || len(still) > 0 {
			return err
		}
		for _, q := range []string{
			"UPDATE music_files SET musicdb_id = NULL WHERE sha256 = ? AND musicdb_id = ?",
			"UPDATE machines SET musicdb_id = NULL WHERE sha256 = ? AND musicdb_id = ?",
			"UPDATE plays SET musicdb_id = NULL WHERE music_hash = ? AND musicdb_id = ?",
			"UPDATE sessions SET musicdb_id = NULL WHERE music_hash = ? AND musicdb_id = ?",
		} {
			if _, err := tx.Exec(q, sha, db); err != nil {
				return err
			}
		}
		return nil
	})
	s.ForgetSongs()
	return err
}

func songSet(q queryer, dbid any) (map[int64]bool, error) {
	rows, err := rowsOf(q, "SELECT music_id FROM songs WHERE musicdb_id = ? AND removed = 0", dbid)
	if err != nil {
		return nil, err
	}
	out := map[int64]bool{}
	for _, r := range rows {
		out[r["music_id"].(int64)] = true
	}
	return out, nil
}

// ImportNotes takes iidx-datatools parse_chart_notecounts.py output: {mid: {"SPA": n, ...}} for one
// music database. A count read from the chart (sound folder scan) is not replaced.
func (s *Store) ImportNotes(db int64, counts map[string]any) (int, error) {
	if r, err := s.Row("SELECT id FROM musicdbs WHERE id = ?", db); err != nil || r == nil {
		if err != nil {
			return 0, err
		}
		return 0, i18n.New("指定された曲DBが存在しません", "No such music database")
	}
	index := map[string]int{}
	for i, n := range ChartNames {
		index[n] = i
	}
	now := Now()
	n := 0
	mids := make([]string, 0, len(counts))
	for mid := range counts {
		mids = append(mids, mid)
	}
	sort.Strings(mids)
	err := s.tx(func(tx *sql.Tx) error {
		for _, mid := range mids {
			id := num(mid, true)
			charts, ok := counts[mid].(map[string]any)
			if id == nil || !ok {
				continue
			}
			for label, v := range charts {
				chart, known := index[label]
				notes := jsonInt(v)
				if !known || notes == nil || *notes == 0 {
					continue
				}
				res, err := tx.Exec("INSERT INTO chart_notes_db VALUES (?, ?, ?, ?, 'import', ?) "+
					"ON CONFLICT (musicdb_id, music_id, chart) DO UPDATE SET notes = excluded.notes, source = 'import', "+
					"updated_at = excluded.updated_at WHERE source != 'analysis'", db, id, chart, *notes, now)
				if err != nil {
					return err
				}
				if changed, _ := res.RowsAffected(); changed > 0 {
					n++
				}
			}
		}
		return nil
	})
	return n, err
}

// jsonInt converts a decoded JSON value to an integer like Python's int() would accept it.
func jsonInt(v any) *int64 {
	switch x := v.(type) {
	case float64:
		i := int64(x)
		return &i
	case string:
		return ptr(num(x, true))
	case bool:
		i := int64(0)
		if x {
			i = 1
		}
		return &i
	}
	return nil
}

// decompressRaw returns the stored request XML of a play.
func decompressRaw(raw []byte) (*eamuse.Node, error) {
	zr, err := zlib.NewReader(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	b, err := io.ReadAll(zr)
	if err != nil {
		return nil, err
	}
	return eamuse.ParseXML(b)
}
