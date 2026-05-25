package core

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadPresence(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "presence.json")
	_ = os.WriteFile(p, []byte(`{"mode":"away","overrides":{"a":"on"},"updated_at":"2026-05-01T00:00:00Z"}`), 0o644)
	pr, err := LoadPresence(p)
	if err != nil {
		t.Fatal(err)
	}
	if pr.Mode != "away" || pr.Overrides["a"] != "on" {
		t.Errorf("%+v", pr)
	}
}

func TestLoadPresenceMissingDefaults(t *testing.T) {
	pr, err := LoadPresence(filepath.Join(t.TempDir(), "missing"))
	if err != nil {
		t.Fatal(err)
	}
	if pr.Mode != "auto" {
		t.Errorf("default mode: %s", pr.Mode)
	}
}
