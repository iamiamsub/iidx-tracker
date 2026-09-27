package store

import (
	_ "embed"
	"encoding/json"
	"sync"
)

// Difficulty tables, taken once (2026-09-27) and built in: the SP☆12 normal / hard clear reference
// tables ("☆12参考表", rank F..S+ as 1..10) and the DP unofficial difficulty table (5.9..12.7),
// matched to music ids by the difficulty-tables tool. The ranks are the work of those tables'
// authors and voters (credited in the README); they apply to the arcade charts, not chart sets.
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

var tierTables = map[string]string{"sp12_normal": "normal", "sp12_hard": "hard", "dp_normal": "dp"}

var tiers struct {
	once    sync.Once
	charts  map[chartKey]map[string]Tier
	sources map[string]TierSource
}

func loadTiers() {
	tiers.charts, tiers.sources = map[chartKey]map[string]Tier{}, map[string]TierSource{}
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
		kind, ok := tierTables[table]
		if !ok {
			continue
		}
		tiers.sources[kind] = t.TierSource
		for _, e := range t.Entries {
			mid, _ := e[0].(float64)
			chart, _ := e[1].(float64)
			value, _ := e[2].(float64)
			label, _ := e[3].(string)
			k := chartKey{int64(mid), int64(chart)}
			if tiers.charts[k] == nil {
				tiers.charts[k] = map[string]Tier{}
			}
			tiers.charts[k][kind] = Tier{label, value}
		}
	}
}

// Tiers gives a chart's ranks: "normal" and "hard" (SP☆12), "dp" (DP); nil when no table has it.
func Tiers(music, chart int64) map[string]Tier {
	tiers.once.Do(loadTiers)
	return tiers.charts[chartKey{music, chart}]
}

// TierSources says where each kind of rank comes from.
func TierSources() map[string]TierSource {
	tiers.once.Do(loadTiers)
	return tiers.sources
}
