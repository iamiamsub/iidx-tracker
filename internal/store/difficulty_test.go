package store

import "testing"

func TestTiers(t *testing.T) {
	mei := Tiers(12004, 3) // 冥 SPA
	if mei["normal"].Label != "地力A" || mei["hard"].Label != "地力S+" || mei["hard"].Value != 10 {
		t.Fatalf("冥 SPA: %+v", mei)
	}
	if q := Tiers(9028, 8); q["dp"].Value != 12.6 || q["dp"].Label != "12.6" { // quasar DPA
		t.Fatalf("quasar DPA: %+v", q)
	}
	if Tiers(12004, 0) != nil {
		t.Fatal("a chart no table rates has no tier")
	}
	if s := TierSources(); s["normal"].Source == "" || s["dp"].Fetched != "2026-09-27" {
		t.Fatalf("sources: %+v", s)
	}
}
