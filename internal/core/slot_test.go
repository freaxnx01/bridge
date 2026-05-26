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

func TestLoadSlotsBashFormat(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "slots.json")
	// Real shape emitted by the legacy bash bridge: object map keyed by index,
	// nullable values for empty slots, no `agent` field.
	_ = os.WriteFile(p, []byte(`{"slots":{
      "1":{"repo":"bridge","worktree":null,"pid":14597,"started_at":1779732205,"session":"bridge"},
      "2":null,
      "3":{"repo":"ai-instructions","worktree":"feature-x","pid":90893,"started_at":1779734834,"session":"ai-instructions"}
    }}`), 0o644)
	slots, err := LoadSlots(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(slots) != 2 {
		t.Fatalf("got %d (want 2 non-null entries)", len(slots))
	}
	// Map iteration order is non-deterministic; sort by ID for assertions.
	byID := map[string]Slot{}
	for _, s := range slots {
		byID[s.ID] = s
	}
	if got, ok := byID["bridge"]; !ok || got.Repo != "bridge" || got.Worktree != "" {
		t.Errorf("bridge slot: %+v", got)
	}
	if got, ok := byID["ai-instructions"]; !ok || got.Worktree != "feature-x" {
		t.Errorf("ai-instructions slot: %+v", got)
	}
}

func TestLoadSlotsNullSlotsKey(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "slots.json")
	_ = os.WriteFile(p, []byte(`{"slots":null}`), 0o644)
	slots, err := LoadSlots(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(slots) != 0 {
		t.Errorf("want empty, got %v", slots)
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
