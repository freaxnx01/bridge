package core

import (
	"reflect"
	"testing"
	"time"
)

func TestPruneSlots(t *testing.T) {
	now := time.Unix(2_000_000, 0).UTC()
	slots := []Slot{
		{ID: "alive", Repo: "x", Created: now},
		{ID: "dead", Repo: "y", Created: now},
		{ID: "also-alive", Repo: "z", Created: now},
	}
	sessions := []Session{
		{SlotID: "alive"},
		{SlotID: "also-alive"},
		{SlotID: "untagged-extra"}, // not in slots; ignored
	}
	kept, dropped := PruneSlots(slots, sessions)
	wantKept := []Slot{{ID: "alive", Repo: "x", Created: now}, {ID: "also-alive", Repo: "z", Created: now}}
	wantDropped := []Slot{{ID: "dead", Repo: "y", Created: now}}
	if !reflect.DeepEqual(kept, wantKept) {
		t.Errorf("kept: got %+v want %+v", kept, wantKept)
	}
	if !reflect.DeepEqual(dropped, wantDropped) {
		t.Errorf("dropped: got %+v want %+v", dropped, wantDropped)
	}
}

func TestPruneSlotsAllDead(t *testing.T) {
	slots := []Slot{{ID: "a"}, {ID: "b"}}
	kept, dropped := PruneSlots(slots, nil)
	if len(kept) != 0 || len(dropped) != 2 {
		t.Errorf("got kept=%v dropped=%v", kept, dropped)
	}
}

func TestPruneSlotsAllAlive(t *testing.T) {
	slots := []Slot{{ID: "a"}, {ID: "b"}}
	sessions := []Session{{SlotID: "a"}, {SlotID: "b"}}
	kept, dropped := PruneSlots(slots, sessions)
	if len(kept) != 2 || len(dropped) != 0 {
		t.Errorf("got kept=%v dropped=%v", kept, dropped)
	}
}
