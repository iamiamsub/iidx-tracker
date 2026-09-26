package sound

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

// Scans a real game folder when asked, for comparing with other tools:
//
//	IIDX_SOUND_ROOT=<folder with data> IIDX_SOUND_OUT=out.json [IIDX_SOUND_MODS=a,b | *] go test -run Real ./internal/sound
func TestRealFolder(t *testing.T) {
	root := os.Getenv("IIDX_SOUND_ROOT")
	if root == "" {
		t.Skip("IIDX_SOUND_ROOT not set")
	}
	var mods []string // nil = every mod
	if m := os.Getenv("IIDX_SOUND_MODS"); m != "*" {
		mods = []string{}
		if m != "" {
			mods = strings.Split(m, ",")
		}
	}
	layers, err := Layers(root, mods)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	songs := Scan(layers)
	out := map[int64]any{}
	errs := 0
	for _, s := range songs {
		out[s.ID] = map[string]any{"source": s.Source, "counts": s.Counts, "error": s.Err}
		if s.Err != "" {
			errs++
		}
	}
	t.Logf("%d songs, %d errors, %v", len(songs), errs, time.Since(start))
	b, _ := json.MarshalIndent(out, "", " ")
	if err := os.WriteFile(os.Getenv("IIDX_SOUND_OUT"), b, 0o644); err != nil {
		t.Fatal(err)
	}
}
