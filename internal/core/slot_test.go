package core

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
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

func TestWriteSlotsRoundTrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "slots.json")
	want := []Slot{
		{ID: "a", Repo: "x", Agent: "claude", Created: time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)},
		{ID: "b", Repo: "y", Worktree: "wt-1", Agent: "code", Created: time.Date(2026, 5, 2, 0, 0, 0, 0, time.UTC)},
	}
	if err := WriteSlots(p, want); err != nil {
		t.Fatal(err)
	}
	got, err := LoadSlots(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[1].Worktree != "wt-1" {
		t.Errorf("got %+v", got)
	}
}

func TestUpsertSlotAddsNew(t *testing.T) {
	p := filepath.Join(t.TempDir(), "slots.json")
	if err := UpsertSlot(p, Slot{ID: "a", Repo: "x", Agent: "claude", Created: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	got, _ := LoadSlots(p)
	if len(got) != 1 || got[0].ID != "a" {
		t.Errorf("got %+v", got)
	}
}

func TestUpsertSlotReplacesExisting(t *testing.T) {
	p := filepath.Join(t.TempDir(), "slots.json")
	_ = UpsertSlot(p, Slot{ID: "a", Repo: "old", Agent: "claude", Created: time.Now().UTC()})
	_ = UpsertSlot(p, Slot{ID: "a", Repo: "new", Agent: "code", Created: time.Now().UTC()})
	got, _ := LoadSlots(p)
	if len(got) != 1 || got[0].Repo != "new" || got[0].Agent != "code" {
		t.Errorf("expected replaced entry, got %+v", got)
	}
}

func TestUpsertSlotConcurrent(t *testing.T) {
	p := filepath.Join(t.TempDir(), "slots.json")
	var wg sync.WaitGroup
	const N = 20
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_ = UpsertSlot(p, Slot{
				ID:      fmt.Sprintf("s%d", i),
				Repo:    fmt.Sprintf("r%d", i),
				Created: time.Now().UTC(),
			})
		}(i)
	}
	wg.Wait()
	got, _ := LoadSlots(p)
	if len(got) != N {
		t.Errorf("expected %d unique slots, got %d", N, len(got))
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
