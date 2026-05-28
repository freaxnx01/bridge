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

func TestLoadSlotsLegacyShapeBacksUpAndStartsFresh(t *testing.T) {
	// The bash bridge wrote slots.json as a top-level object map keyed by
	// slot id. Unmarshaling that into our slotFile{Slots []Slot} fails
	// with "cannot unmarshal object into Go struct field slotFile.slots
	// of type []core.Slot". LoadSlots should recover by renaming the
	// legacy file to a .bak and returning an empty registry, so the
	// caller (UpsertSlot) doesn't trip on every invocation.
	dir := t.TempDir()
	p := filepath.Join(dir, "slots.json")
	legacy := `{"slots": {"bridge-main": {"repo": "bridge", "agent": "claude"}}}`
	if err := os.WriteFile(p, []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	slots, err := LoadSlots(p)
	if err != nil {
		t.Fatalf("expected nil error on legacy shape, got %v", err)
	}
	if len(slots) != 0 {
		t.Errorf("expected empty slot list after legacy migration, got %+v", slots)
	}
	// Original file should be gone.
	if _, statErr := os.Stat(p); statErr == nil {
		t.Errorf("expected slots.json to be renamed; still present")
	}
	// A .bak with the legacy content should exist somewhere in dir.
	entries, _ := os.ReadDir(dir)
	foundBak := false
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".bak" {
			body, _ := os.ReadFile(filepath.Join(dir, e.Name()))
			if string(body) == legacy {
				foundBak = true
			}
		}
	}
	if !foundBak {
		t.Errorf("expected a .bak with the legacy content; dir entries: %v", entries)
	}
}

func TestLoadSlotsCorruptIsBackedUp(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "slots.json")
	if err := os.WriteFile(p, []byte("not json at all"), 0o644); err != nil {
		t.Fatal(err)
	}
	slots, err := LoadSlots(p)
	if err != nil {
		t.Fatalf("expected nil error on corrupt file (recoverable), got %v", err)
	}
	if len(slots) != 0 {
		t.Errorf("expected empty slot list after corrupt-file recovery, got %+v", slots)
	}
	if _, statErr := os.Stat(p); statErr == nil {
		t.Errorf("expected corrupt slots.json to be renamed; still present")
	}
}

func TestUpsertSlotAfterLegacyMigration(t *testing.T) {
	// End-to-end: legacy file present, UpsertSlot should silently migrate
	// and succeed, leaving a Go-shape slots.json with one entry.
	dir := t.TempDir()
	p := filepath.Join(dir, "slots.json")
	legacy := `{"slots": {"bridge-main": {"repo": "bridge", "agent": "claude"}}}`
	if err := os.WriteFile(p, []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := UpsertSlot(p, Slot{ID: "new", Repo: "x", Agent: "claude", Created: time.Now().UTC()}); err != nil {
		t.Fatalf("UpsertSlot after legacy: %v", err)
	}
	got, err := LoadSlots(p)
	if err != nil {
		t.Fatalf("LoadSlots after upsert: %v", err)
	}
	if len(got) != 1 || got[0].ID != "new" {
		t.Errorf("expected one fresh slot, got %+v", got)
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
