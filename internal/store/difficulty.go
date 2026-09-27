package store

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"sync"

	"iidx-tracker/internal/i18n"
)

// Difficulty tables, taken once (2026-09-27) and built in: SP tables sp<level>_<normal|hard> (the
// SP☆12 reference tables "☆12参考表", the SP☆11 tables of 2026-09-16, the SP☆10 / ☆9 normal-clear-or-
// under table of 2026-09-05, the SP☆10 hard table of 2025-03-06; ranks F- 0, F 1 .. S+ 10, not every
// table has every rank) and the DP unofficial difficulty table (5.9..12.7), matched to music ids by
// the difficulty-tables tool. The ranks are the work of those tables' authors and voters (credited in
// the README); they apply to the arcade charts, not chart sets.
// The tiers page changes ranks over the snapshot (tier_overrides).
//
//go:embed difficulty.json
var difficultyJSON []byte

// Tier is a chart's place in one table.
type Tier struct {
	Label string  `json:"label"`
	Value float64 `json:"value"`
}

// TierSource says where a table comes from.
type TierSource struct {
	Name    string `json:"name"`
	Source  string `json:"source"`
	Fetched string `json:"fetched"`
}

// tierKind is the kind of rank a table gives, by the name the API uses: a chart is in the SP table of
// its level, so "normal" / "hard" are each several tables.
var tierKind = regexp.MustCompile(`^(?:sp(?:9|1[012])_(normal|hard)|dp_normal)$`)

// tierTable is the table a kind of rank comes from for charts of a level.
func tierTable(kind string, level int64) string {
	if kind == "dp" {
		return "dp_normal"
	}
	return fmt.Sprintf("sp%d_%s", level, kind)
}

var snapshot struct {
	once    sync.Once
	charts  map[chartKey]map[string]Tier
	sources map[string]TierSource // by table
}

func loadSnapshot() {
	snapshot.charts, snapshot.sources = map[chartKey]map[string]Tier{}, map[string]TierSource{}
	var data struct {
		Tables map[string]struct {
			TierSource
			Entries [][4]any `json:"entries"`
		} `json:"tables"`
	}
	if json.Unmarshal(difficultyJSON, &data) != nil {
		return
	}
	for table, t := range data.Tables {
		m := tierKind.FindStringSubmatch(table)
		if m == nil {
			continue
		}
		kind := m[1]
		if kind == "" {
			kind = "dp"
		}
		snapshot.sources[table] = t.TierSource
		for _, e := range t.Entries {
			mid, _ := e[0].(float64)
			chart, _ := e[1].(float64)
			value, _ := e[2].(float64)
			label, _ := e[3].(string)
			k := chartKey{int64(mid), int64(chart)}
			if snapshot.charts[k] == nil {
				snapshot.charts[k] = map[string]Tier{}
			}
			snapshot.charts[k][kind] = Tier{label, value}
		}
	}
}

// TierSources says where each kind of rank comes from for charts of a level.
func TierSources(level int64) map[string]TierSource {
	snapshot.once.Do(loadSnapshot)
	out := map[string]TierSource{}
	for _, kind := range []string{"normal", "hard", "dp"} {
		if src, ok := snapshot.sources[tierTable(kind, level)]; ok {
			out[kind] = src
		}
	}
	return out
}

// Tiers gives a chart's ranks in use: "normal" and "hard" (SP☆11 / ☆12), "dp" (DP); nil when it has none.
func (s *Store) Tiers(music, chart int64) map[string]Tier {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.tiers == nil {
		s.tiers = s.mergeTiers()
	}
	return s.tiers[chartKey{music, chart}]
}

// mergeTiers: the snapshot with the changes (s.mu held).
func (s *Store) mergeTiers() map[chartKey]map[string]Tier {
	snapshot.once.Do(loadSnapshot)
	out := make(map[chartKey]map[string]Tier, len(snapshot.charts))
	for k, v := range snapshot.charts {
		m := make(map[string]Tier, len(v))
		for kind, t := range v {
			m[kind] = t
		}
		out[k] = m
	}
	rows, err := s.db.Query("SELECT kind, music_id, chart, label, value FROM tier_overrides")
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var kind string
		var k chartKey
		var label *string
		var value *float64
		if rows.Scan(&kind, &k.music, &k.chart, &label, &value) != nil {
			continue
		}
		if label == nil || value == nil {
			delete(out[k], kind)
			continue
		}
		if out[k] == nil {
			out[k] = map[string]Tier{}
		}
		out[k][kind] = Tier{*label, *value}
	}
	return out
}

// TierRows lists a table's charts of a music database with the snapshot's rank and the one in use:
// the SP level 9 .. 12 (else 12) HYPER..LEGGENDARIA charts for "normal" / "hard", the DP ones of the
// level for "dp".
func (s *Store) TierRows(kind string, level int64, db any) (map[string]any, error) {
	charts := []int64{2, 3, 4}
	switch kind {
	case "normal", "hard":
		if level < 9 || level > 12 {
			level = 12
		}
	case "dp":
		charts = []int64{7, 8, 9}
	default:
		return nil, i18n.New("不明な難易度表です", "unknown difficulty table")
	}
	meta, err := s.SongMetas(db)
	if err != nil {
		return nil, err
	}
	snapshot.once.Do(loadSnapshot)
	rows := []map[string]any{}
	for mid, song := range meta {
		for _, chart := range charts {
			if int(chart) >= len(song.Levels) || song.Levels[chart] != level {
				continue
			}
			row := map[string]any{"music_id": mid, "chart": chart, "title": song.Title, "level": level, "snapshot": nil, "current": nil}
			if t, ok := snapshot.charts[chartKey{mid, chart}][kind]; ok {
				row["snapshot"] = t.Label
			}
			if t, ok := s.Tiers(mid, chart)[kind]; ok {
				row["current"] = t.Label
			}
			rows = append(rows, row)
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		a, b := rows[i]["music_id"].(int64), rows[j]["music_id"].(int64)
		return a < b || a == b && rows[i]["chart"].(int64) < rows[j]["chart"].(int64)
	})
	return map[string]any{"kind": kind, "level": level, "source": TierSources(level)[kind], "rows": rows}, nil
}

var spRank = regexp.MustCompile(`^(地力|個人差)(F-?|E|D|C|B\+?|A\+?|S\+?)$`)
var spRanks = []string{"F-", "F", "E", "D", "C", "B", "B+", "A", "A+", "S", "S+"} // value = index

// SetTier changes a chart's rank in one table: a label (SP: 地力/個人差 and F-..S+; DP: the
// number), nil for no rank, or reset to take the change back. It returns the rank now in use.
func (s *Store) SetTier(kind string, music, chart int64, label *string, reset bool) (any, error) {
	if _, ok := map[string]bool{"normal": true, "hard": true, "dp": true}[kind]; !ok || chart < 0 || chart > 9 {
		return nil, i18n.New("譜面の指定が正しくありません", "bad chart")
	}
	var err error
	if reset {
		_, err = s.db.Exec("DELETE FROM tier_overrides WHERE kind = ? AND music_id = ? AND chart = ?", kind, music, chart)
	} else {
		var text, value any
		if label != nil && *label != "" {
			if kind == "dp" {
				v, perr := strconv.ParseFloat(*label, 64)
				if perr != nil || v < 1 || v > 13 {
					return nil, i18n.New("難易度は 1〜13 の数で入れてください", "enter the difficulty as a number from 1 to 13")
				}
				v = math.Round(v*10) / 10
				text, value = strconv.FormatFloat(v, 'f', 1, 64), v
			} else {
				m := spRank.FindStringSubmatch(*label)
				if m == nil {
					return nil, i18n.New("ランクは 地力/個人差 と F-〜S+ で入れてください", "enter the rank as 地力/個人差 and F- to S+")
				}
				for i, r := range spRanks {
					if r == m[2] {
						value = float64(i)
					}
				}
				text = m[0]
			}
		}
		_, err = s.db.Exec(`INSERT INTO tier_overrides (kind, music_id, chart, label, value, changed_at) VALUES (?, ?, ?, ?, ?, ?)
			ON CONFLICT (kind, music_id, chart) DO UPDATE SET label = excluded.label, value = excluded.value, changed_at = excluded.changed_at`,
			kind, music, chart, text, value, Now())
	}
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.tiers = nil // read again with the change
	s.mu.Unlock()
	if t, ok := s.Tiers(music, chart)[kind]; ok {
		return t.Label, nil
	}
	return nil, nil
}
