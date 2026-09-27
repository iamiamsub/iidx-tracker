package store

import (
	"path/filepath"
	"testing"
)

func TestTiers(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	mei := st.Tiers(12004, 3) // 冥 SPA
	if mei["normal"].Label != "地力A" || mei["hard"].Label != "地力S+" || mei["hard"].Value != 10 {
		t.Fatalf("冥 SPA: %+v", mei)
	}
	if q := st.Tiers(9028, 8); q["dp"].Value != 12.6 || q["dp"].Label != "12.6" { // quasar DPA
		t.Fatalf("quasar DPA: %+v", q)
	}
	if st.Tiers(12004, 0) != nil {
		t.Fatal("a chart no table rates has no tier")
	}
	if r := st.Tiers(1017, 4); r["normal"].Label != "地力A" || r["hard"] != (Tier{"地力S", 9}) { // a ☆11 SPL (the ☆11 tables)
		t.Fatalf("☆11: %+v", r)
	}
	if s := TierSources(12); s["normal"].Source == "" || s["dp"].Fetched != "2026-09-27" {
		t.Fatalf("sources: %+v", s)
	}
	if s := TierSources(11); s["hard"].Source != "https://w.atwiki.jp/bemani2sp11/" || s["dp"].Source == "" {
		t.Fatalf("☆11 sources: %+v", s)
	}

	// changes over the snapshot: a rank, a number, no rank, back to the snapshot
	label := func(s string) *string { return &s }
	if cur, err := st.SetTier("hard", 12004, 3, label("個人差A+"), false); err != nil || cur != "個人差A+" {
		t.Fatalf("set: %v %v", cur, err)
	}
	if got := st.Tiers(12004, 3); got["hard"] != (Tier{"個人差A+", 8}) || got["normal"].Label != "地力A" {
		t.Fatalf("after set: %+v", got)
	}
	if cur, _ := st.SetTier("dp", 9028, 8, label("12.64"), false); cur != "12.6" {
		t.Fatalf("dp: %v", cur)
	}
	if cur, _ := st.SetTier("normal", 12004, 3, nil, false); cur != nil || st.Tiers(12004, 3)["normal"].Label != "" {
		t.Fatalf("no rank: %v", cur)
	}
	if cur, _ := st.SetTier("hard", 12004, 3, nil, true); cur != "地力S+" {
		t.Fatalf("reset: %v", cur)
	}
	if cur, _ := st.SetTier("hard", 99999, 3, label("地力B"), false); cur != "地力B" { // a chart the snapshot has not got
		t.Fatalf("new: %v", cur)
	}
	for _, bad := range []struct{ kind, label string }{{"hard", "地力Z"}, {"dp", "99"}, {"nope", "地力A"}} {
		if _, err := st.SetTier(bad.kind, 12004, 3, label(bad.label), false); err == nil {
			t.Fatalf("accepted %+v", bad)
		}
	}
}
