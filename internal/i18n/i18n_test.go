package i18n

import (
	"errors"
	"fmt"
	"testing"
)

func TestText(t *testing.T) {
	m := Errorf("%d 曲を取り込みました", "imported %d songs", 3)
	if Text(m, "ja") != "3 曲を取り込みました" || Text(m, "en") != "imported 3 songs" || m.Error() != "3 曲を取り込みました" {
		t.Fatal(m)
	}
	// wrapped: still found; plain errors pass through
	if Text(fmt.Errorf("context: %w", m), "en") != "imported 3 songs" || Text(errors.New("plain"), "en") != "plain" {
		t.Fatal("wrapping")
	}
	if Pick("en") != "en" || Pick("ja") != "ja" {
		t.Fatal("Pick")
	}
	t.Setenv("LC_ALL", "ja_JP.UTF-8")
	if Pick("") != "ja" {
		t.Fatal("locale")
	}
	t.Setenv("LC_ALL", "C")
	if Pick("auto") != "en" {
		t.Fatal("locale C")
	}
}
