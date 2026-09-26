package store

import (
	"database/sql"
	"fmt"
	"strings"

	"iidx-tracker/internal/i18n"

	"iidx-tracker/internal/sound"
)

// ImportScan stores the note counts of a sound folder scan for one music database. Songs the
// database does not have are skipped (the folder and the database belong to different games or
// omnimix builds), so the choice of database decides which charts are kept.
func (s *Store) ImportScan(musicdbID int64, root string, mods []string, songs []sound.Song) (map[string]any, error) {
	found, err := s.Row("SELECT id FROM musicdbs WHERE id = ?", musicdbID)
	if err != nil {
		return nil, err
	}
	if found == nil {
		return nil, i18n.New("指定された曲DBが存在しません", "No such music database")
	}
	inDB, err := songSet(s.db, musicdbID)
	if err != nil {
		return nil, err
	}
	now := Now()
	stored, charts, skipped := 0, 0, 0
	errs := []map[string]any{}
	err = s.tx(func(tx *sql.Tx) error {
		for _, sg := range songs {
			if sg.Err != "" {
				if len(errs) < 100 {
					errs = append(errs, map[string]any{"music_id": sg.ID, "source": sg.Source, "error": sg.Err, "error_en": sg.ErrEn})
				}
				continue
			}
			if !inDB[sg.ID] {
				skipped++
				continue
			}
			stored++
			for chart, n := range sg.Counts {
				if n <= 0 {
					continue
				}
				if _, err := tx.Exec("INSERT OR REPLACE INTO chart_notes_db VALUES (?, ?, ?, ?, ?, ?)",
					musicdbID, sg.ID, chart, n, "analysis", now); err != nil {
					return err
				}
				charts++
			}
		}
		_, err := insertRow(tx, "sound_scans",
			[]string{"musicdb_id", "root", "mods", "songs", "charts", "skipped", "errors", "scanned_at"},
			[]any{musicdbID, root, strings.Join(mods, ","), stored, charts, skipped, countErrors(songs), now})
		return err
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"songs": stored, "charts": charts, "skipped": skipped,
		"errors": countErrors(songs), "error_list": errs}, nil
}

func countErrors(songs []sound.Song) int {
	n := 0
	for _, sg := range songs {
		if sg.Err != "" {
			n++
		}
	}
	return n
}

// MachineUnreported notes a login without a report from tracker_link.dll (the game runs without it
// now): what it reported before no longer applies until it reports again.
func (s *Store) MachineUnreported(pcbid string) error {
	_, err := s.Exec("UPDATE machines SET unreported_at = ? WHERE pcbid = ?", Now(), pcbid)
	return err
}

// MachineBoot records what tracker_link.dll reported at a cabinet's login: the music data file the
// game loaded and whether 2dxtra is loaded. It returns the music database holding that file - the
// one it was imported into, or the one the player put it in - or nil when the file is new (the UI
// then asks which one it is, see Unassigned).
func (s *Store) MachineBoot(pcbid, sha, filename string, size int64, dxtra any, dll, game, remote string) (any, error) {
	if pcbid == "" || len(sha) != 64 {
		return nil, i18n.New("pcbid と sha256 が必要です", "pcbid and sha256 are required")
	}
	r, err := s.Row("SELECT COALESCE((SELECT musicdb_id FROM musicdb_imports WHERE sha256 = ? ORDER BY id DESC LIMIT 1), "+
		"(SELECT musicdb_id FROM music_files WHERE sha256 = ?)) AS id", sha, sha)
	if err != nil {
		return nil, err
	}
	db := r["id"]
	err = s.tx(func(tx *sql.Tx) error {
		if _, err := tx.Exec("INSERT INTO music_files (sha256, filename, size, musicdb_id) VALUES (?, ?, ?, ?) "+
			"ON CONFLICT (sha256) DO UPDATE SET filename = excluded.filename, size = excluded.size, "+
			"musicdb_id = excluded.musicdb_id", sha, filename, size, db); err != nil {
			return err
		}
		_, err := tx.Exec("INSERT OR REPLACE INTO machines (pcbid, musicdb_id, sha256, filename, size, altfix, game, remote, "+
			"booted_at, dxtra) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)", pcbid, db, sha, filename, size, dll, game, remote, Now(), dxtra)
		return err
	})
	return db, err
}

// Cabinet is what a cabinet's calls are recorded under.
type Cabinet struct {
	DB    any   // the music database, nil when not known
	Omni  int64 // 1 = omnimix
	Hash  any   // the music data file tracker_link.dll reported, nil without a report
	Dxtra any   // 1 when 2dxtra is loaded, nil when not reported
}

// CabinetFor decides where a cabinet's call belongs. With a report from tracker_link.dll (every login
// carries one), the music data file the game loaded decides: the database holding it (and
// with it omnimix or not), or none until the player says which one it is (the file name then tells
// omnimix apart, for the relay). Without a report the model's revision does ('S' = omnimix, set by
// altfix), and the newest database of that kind and game version.
func (s *Store) CabinetFor(pcbid, model string, version any) Cabinet {
	cab := Cabinet{}
	if parts := strings.Split(model, ":"); len(parts) > 3 && parts[3] == "S" {
		cab.Omni = 1
	}
	r, err := s.Row("SELECT m.sha256, m.filename, m.dxtra, d.id, d.kind FROM machines m LEFT JOIN musicdbs d ON d.id = m.musicdb_id "+
		"WHERE m.pcbid = ? AND m.sha256 IS NOT NULL AND m.booted_at >= COALESCE(m.unreported_at, 0)", pcbid)
	if err != nil || r == nil {
		cab.DB = s.DBFor(cab.Omni, version)
		return cab
	}
	cab.Hash, cab.Dxtra = r["sha256"], r["dxtra"]
	if r["id"] != nil {
		cab.DB = r["id"]
		cab.Omni = 0
		if r["kind"] == "omni" {
			cab.Omni = 1
		}
	} else if strings.Contains(strings.ToLower(str(r["filename"])), "omni") {
		cab.Omni = 1
	}
	return cab
}

// AssignMusicFile puts a music data file in a music database: the cabinets that reported it, and
// the logins and plays recorded on it, belong to that database from now on (omnimix or not as the
// database is).
func (s *Store) AssignMusicFile(sha string, db any) error {
	r, err := s.Row("SELECT kind FROM musicdbs WHERE id = ?", db)
	if err != nil {
		return err
	}
	if r == nil {
		return i18n.New("指定された曲DBが存在しません", "No such music database")
	}
	omni := 0
	if r["kind"] == "omni" {
		omni = 1
	}
	err = s.tx(func(tx *sql.Tx) error {
		for _, q := range []struct {
			sql  string
			args []any
		}{
			{"INSERT INTO music_files (sha256, musicdb_id) VALUES (?, ?) ON CONFLICT (sha256) DO UPDATE SET musicdb_id = excluded.musicdb_id", []any{sha, db}},
			{"UPDATE machines SET musicdb_id = ? WHERE sha256 = ?", []any{db, sha}},
			{"UPDATE plays SET musicdb_id = ?, omni = ? WHERE music_hash = ?", []any{db, omni, sha}},
			{"UPDATE sessions SET musicdb_id = ?, omni = ? WHERE music_hash = ?", []any{db, omni, sha}},
		} {
			if _, err := tx.Exec(q.sql, q.args...); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	return assignUnknownDBs(s.db) // server bests take the database of the latest login
}

// Unassigned lists the music data files tracker_link.dll reported that no music database holds yet,
// with their latest plays and, per database, how many of the songs played on the file it has and
// what it calls them - so that the player can tell which database the file is.
func (s *Store) Unassigned() ([]Row, error) {
	files, err := s.Rows(`SELECT p.music_hash AS sha256, f.filename, f.size, MAX(p.game_version) AS game_version,
		COUNT(*) AS plays, COUNT(DISTINCT p.music_id) AS songs, MIN(p.played_at) AS first_played, MAX(p.played_at) AS last_played
		FROM plays p LEFT JOIN music_files f ON f.sha256 = p.music_hash
		WHERE p.musicdb_id IS NULL AND p.music_hash IS NOT NULL GROUP BY p.music_hash ORDER BY last_played DESC`)
	if err != nil || len(files) == 0 {
		return files, err
	}
	dbs, err := s.Rows("SELECT id, name, kind, game_version FROM musicdbs ORDER BY id")
	if err != nil {
		return nil, err
	}
	for _, f := range files {
		samples, err := s.Rows("SELECT id, played_at, iidx_id, music_id, chart, level, clear, ex_score, miss_count, chart_set, dxtra "+
			"FROM plays WHERE music_hash = ? AND musicdb_id IS NULL ORDER BY played_at DESC LIMIT 20", f["sha256"])
		if err != nil {
			return nil, err
		}
		fits := []Row{}
		for _, d := range dbs {
			songs, err := s.Rows("SELECT music_id, title FROM songs WHERE musicdb_id = ? AND removed = 0 AND music_id IN "+
				"(SELECT DISTINCT music_id FROM plays WHERE music_hash = ? AND musicdb_id IS NULL)", d["id"], f["sha256"])
			if err != nil {
				return nil, err
			}
			titles := map[any]any{}
			for _, sg := range songs {
				titles[sg["music_id"]] = sg["title"]
			}
			for _, p := range samples {
				if p["titles"] == nil {
					p["titles"] = map[string]any{}
				}
				p["titles"].(map[string]any)[fmt.Sprint(d["id"])] = titles[p["music_id"]]
			}
			fits = append(fits, Row{"id": d["id"], "name": d["name"], "kind": d["kind"],
				"game_version": d["game_version"], "has": len(songs)})
		}
		f["dbs"], f["samples"] = fits, samples
	}
	return files, nil
}

// DBFor returns the music database that fits a play: omnimix plays use the omni lineage of the
// game version, others the regular one (the newest if several). nil when there is none.
func (s *Store) DBFor(omni, version any) any {
	kind := "vanilla"
	if o, _ := asInt(omni); o == 1 {
		kind = "omni"
	}
	r, err := s.Row("SELECT id FROM musicdbs WHERE kind = ? AND game_version = ? ORDER BY id DESC LIMIT 1", kind, version)
	if err != nil || r == nil {
		return nil
	}
	return r["id"]
}

// DefaultDB is the music database to show a player by default: the one of their latest login, else
// of their latest play; without a player, the newest database. nil when there is none.
func (s *Store) DefaultDB(key string) any {
	if key != "" {
		where, args := playerFilter(key)
		for _, q := range []string{
			"SELECT musicdb_id FROM sessions WHERE " + where + " AND musicdb_id IS NOT NULL ORDER BY started_at DESC LIMIT 1",
			"SELECT musicdb_id FROM plays WHERE " + where + " AND musicdb_id IS NOT NULL ORDER BY played_at DESC LIMIT 1",
		} {
			if r, err := s.Row(q, args...); err == nil && r != nil {
				return r["musicdb_id"]
			}
		}
	}
	if r, err := s.Row("SELECT id FROM musicdbs ORDER BY id DESC LIMIT 1"); err == nil && r != nil {
		return r["id"]
	}
	return nil
}
