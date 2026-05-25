package core

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadSlots(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "slots.json")
	_ = os.WriteFile(p, []byte(`{"slots":[
      {"id":"bridge-main","repo":"bridge","worktree":"","agent":"claude","created":"2026-05-01T00:00:00Z"},
      {"id":"ingest-bug","repo":"ingest","worktree":"bug-142","agent":"copilot","created":"2026-05-02T00:00:00Z"}
    ]}`), 0o644)
	slots, err := LoadSlots(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(slots) != 2 {
		t.Fatalf("got %d", len(slots))
	}
	if slots[0].ID != "bridge-main" || slots[1].Worktree != "bug-142" {
		t.Errorf("%+v", slots)
	}
}

func TestLoadSlotsMissing(t *testing.T) {
	slots, err := LoadSlots(filepath.Join(t.TempDir(), "missing"))
	if err != nil {
		t.Fatal(err)
	}
	if len(slots) != 0 {
		t.Errorf("want empty, got %v", slots)
	}
}
