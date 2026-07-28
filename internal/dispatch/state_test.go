package dispatch

import (
	"path/filepath"
	"testing"
	"time"
)

func TestReadStateMissingFile(t *testing.T) {
	s, err := ReadState(filepath.Join(t.TempDir(), "nope.json"))
	if err != nil {
		t.Fatalf("missing file must not error: %v", err)
	}
	if s.Paused || s.DispatchedTonight != 0 {
		t.Errorf("zero state expected: %+v", s)
	}
}

func TestWriteThenReadState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dispatch.json")
	want := State{Paused: true, DispatchedTonight: 2, NightStartedAt: time.Date(2026, 7, 27, 22, 0, 0, 0, time.UTC)}
	if err := WriteState(path, want); err != nil {
		t.Fatal(err)
	}
	got, err := ReadState(path)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Paused || got.DispatchedTonight != 2 || !got.NightStartedAt.Equal(want.NightStartedAt) {
		t.Errorf("got %+v want %+v", got, want)
	}
}

func TestNightBudgetResetsOnANewNight(t *testing.T) {
	s := State{DispatchedTonight: 4, NightStartedAt: time.Date(2026, 7, 27, 22, 0, 0, 0, time.UTC)}

	// A 02:00 retry tick is the SAME night as the 22:00 dispatch before it.
	sameNight := time.Date(2026, 7, 28, 2, 0, 0, 0, time.UTC)
	if got := s.NightBudgetUsed(sameNight); got != 4 {
		t.Errorf("same night should keep the count, got %d", got)
	}

	// The next evening is a new night.
	nextNight := time.Date(2026, 7, 28, 22, 0, 0, 0, time.UTC)
	if got := s.NightBudgetUsed(nextNight); got != 0 {
		t.Errorf("new night should reset, got %d", got)
	}
}
