package store

import (
	"database/sql"
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

// MachineBoot records what tracker_link.dll reported in a cabinet's services.get: the music data
// file the game loads. It returns the music database holding that file, or nil when the file has
// not been imported yet (importing it later ties the cabinet to it, see ImportMusicDB).
func (s *Store) MachineBoot(pcbid, sha, filename string, size int64, altfix, game, remote string) (any, error) {
	if pcbid == "" || len(sha) != 64 {
		return nil, i18n.New("pcbid と sha256 が必要です", "pcbid and sha256 are required")
	}
	var db any
	if r, err := s.Row("SELECT musicdb_id FROM musicdb_imports WHERE sha256 = ? ORDER BY id DESC LIMIT 1", sha); err != nil {
		return nil, err
	} else if r != nil {
		db = r["musicdb_id"]
	}
	_, err := s.Exec("INSERT OR REPLACE INTO machines (pcbid, musicdb_id, sha256, filename, size, altfix, game, remote, booted_at) "+
		"VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)", pcbid, db, sha, filename, size, altfix, game, remote, Now())
	return db, err
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
