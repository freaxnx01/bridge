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

func TestDispatchesSinceScopesTheCountToOneWindowOccurrence(t *testing.T) {
	// Counter set during the night window that began 2026-07-27 18:00.
	nightStart := time.Date(2026, 7, 27, 18, 0, 0, 0, time.UTC)
	s := State{DispatchedTonight: 4, NightStartedAt: time.Date(2026, 7, 27, 22, 0, 0, 0, time.UTC)}

	// A 02:00 retry tick is still that same occurrence.
	if got := s.DispatchesSince(nightStart); got != 4 {
		t.Errorf("same occurrence should keep the count, got %d", got)
	}

	// The next evening's occurrence is a fresh budget.
	nextNight := time.Date(2026, 7, 28, 18, 0, 0, 0, time.UTC)
	if got := s.DispatchesSince(nextNight); got != 0 {
		t.Errorf("a new occurrence should reset, got %d", got)
	}
}

// The old accounting bucketed on a hardcoded 12:00 pivot, so a morning tick
// resolved to the previous night and inherited its spent counter — blocking
// the whole daytime path while the budget rung had full headroom. The boundary
// is now the window's own start, which has no such pivot.
func TestDispatchesSinceHasNoNoonPivot(t *testing.T) {
	s := State{DispatchedTonight: 5, NightStartedAt: time.Date(2026, 7, 27, 22, 0, 0, 0, time.UTC)}

	// 08:00 the next morning is inside the *day* window, whose occurrence
	// began at 07:00 — after the recorded count, so nothing is inherited.
	dayStart := time.Date(2026, 7, 28, 7, 0, 0, 0, time.UTC)
	if got := s.DispatchesSince(dayStart); got != 0 {
		t.Errorf("the day window must not inherit the night's counter, got %d", got)
	}
}

func TestDispatchesSinceZeroStateIsZero(t *testing.T) {
	if got := (State{}).DispatchesSince(time.Date(2026, 7, 27, 18, 0, 0, 0, time.UTC)); got != 0 {
		t.Errorf("got %d", got)
	}
}
