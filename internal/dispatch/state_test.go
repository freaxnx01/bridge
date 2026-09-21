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

func TestDispatchesSinceZeroBoundaryReportsNothing(t *testing.T) {
	s := State{DispatchedTonight: 5, NightStartedAt: time.Date(2026, 7, 27, 22, 0, 0, 0, time.UTC)}
	if got := s.DispatchesSince(time.Time{}); got != 0 {
		t.Errorf("no window occurrence to attribute to, want 0, got %d", got)
	}
}

func TestDispatchesInLane(t *testing.T) {
	since := time.Date(2026, 9, 7, 0, 0, 0, 0, time.Local)
	s := State{Lanes: map[string]LaneState{
		"auto": {StartedAt: since.Add(2 * time.Hour), Dispatched: 4},
	}}

	if got := s.DispatchesInLane("auto", since); got != 4 {
		t.Errorf("counter inside this occurrence: got %d want 4", got)
	}
	if got := s.DispatchesInLane("auto", since.AddDate(0, 0, 1)); got != 0 {
		t.Errorf("a counter from an earlier occurrence must not carry over: got %d", got)
	}
	if got := s.DispatchesInLane("hitl", since); got != 0 {
		t.Errorf("an unknown lane has spent nothing: got %d", got)
	}
	if got := s.DispatchesInLane("auto", time.Time{}); got != 0 {
		t.Errorf("no boundary means nothing to attribute the counter to: got %d", got)
	}
}

func TestRecordLaneDispatchAccumulatesThenResets(t *testing.T) {
	day1 := time.Date(2026, 9, 7, 0, 0, 0, 0, time.Local)
	var s State

	s.RecordLaneDispatch("auto", day1, day1.Add(time.Hour), 2)
	if got := s.DispatchesInLane("auto", day1); got != 2 {
		t.Fatalf("first write: got %d want 2", got)
	}

	s.RecordLaneDispatch("auto", day1, day1.Add(3*time.Hour), 1)
	if got := s.DispatchesInLane("auto", day1); got != 3 {
		t.Errorf("same occurrence must accumulate: got %d want 3", got)
	}

	day2 := day1.AddDate(0, 0, 1)
	s.RecordLaneDispatch("auto", day2, day2.Add(time.Hour), 1)
	if got := s.DispatchesInLane("auto", day2); got != 1 {
		t.Errorf("a new occurrence starts from zero: got %d want 1", got)
	}
}

// A 24h lane's occurrence boundary is calendar arithmetic, so the counter must
// survive a 23-hour day. Reuses the Europe/Zurich spring-forward date the window
// tests pin.
func TestRecordLaneDispatchAcrossASpringForwardDay(t *testing.T) {
	zurich, err := time.LoadLocation("Europe/Zurich")
	if err != nil {
		t.Skipf("tzdata unavailable: %v", err)
	}
	lane := Lane{Name: "auto", Windows: []Span{{From: "00:00", To: "00:00"}}}

	before := time.Date(2026, 3, 29, 1, 0, 0, 0, zurich) // before the 02:00 gap
	after := time.Date(2026, 3, 29, 13, 0, 0, 0, zurich) // same calendar day

	var s State
	s.RecordLaneDispatch(lane.Name, lane.WindowStart(Schedule{}, before), before, 1)
	s.RecordLaneDispatch(lane.Name, lane.WindowStart(Schedule{}, after), after, 1)

	if got := s.DispatchesInLane(lane.Name, lane.WindowStart(Schedule{}, after)); got != 2 {
		t.Errorf("both dispatches belong to the same 23-hour day: got %d want 2", got)
	}

	next := time.Date(2026, 3, 30, 9, 0, 0, 0, zurich)
	if got := s.DispatchesInLane(lane.Name, lane.WindowStart(Schedule{}, next)); got != 0 {
		t.Errorf("the next day starts fresh: got %d", got)
	}
}

func TestRecordLaneDispatchRoundTripsThroughDisk(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dispatch.json")
	day := time.Date(2026, 9, 7, 0, 0, 0, 0, time.Local)

	var s State
	s.RecordLaneDispatch("auto", day, day.Add(time.Hour), 2)
	if err := WriteState(path, s); err != nil {
		t.Fatal(err)
	}

	back, err := ReadState(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := back.DispatchesInLane("auto", day); got != 2 {
		t.Errorf("after reload: got %d want 2", got)
	}
}
