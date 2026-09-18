package dispatch

import (
	"testing"
	"time"
)

func at(hour, min int) time.Time {
	return time.Date(2026, 9, 7, hour, min, 0, 0, time.Local)
}

func TestInWindow(t *testing.T) {
	s := DefaultConfig().Schedule // 18:00-07:00 no rung, 07:00-18:00 rung

	tests := []struct {
		name     string
		now      time.Time
		wantIn   bool
		wantRung bool
	}{
		{"midday is the rung window", at(12, 0), true, true},
		{"late evening wraps into the night window", at(23, 30), true, false},
		{"after midnight is still the night window", at(2, 0), true, false},
		{"day window start is inclusive", at(7, 0), true, true},
		{"day window end is exclusive", at(18, 0), true, false},
		{"night window start is inclusive", at(18, 1), true, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w, ok := s.InWindow(tc.now)
			if ok != tc.wantIn {
				t.Fatalf("in=%v want %v", ok, tc.wantIn)
			}
			if ok && w.BudgetRung != tc.wantRung {
				t.Errorf("rung=%v want %v (window %+v)", w.BudgetRung, tc.wantRung, w)
			}
		})
	}
}

func TestInWindowNoWindowsMeansNeverInWindow(t *testing.T) {
	if _, ok := (Schedule{}).InWindow(at(12, 0)); ok {
		t.Error("an empty window list must not match")
	}
}

func TestInWindowSkipsMalformedEntries(t *testing.T) {
	s := Schedule{Windows: []Window{
		{From: "not-a-time", To: "18:00", BudgetRung: true},
		{From: "07:00", To: "18:00"},
	}}
	w, ok := s.InWindow(at(12, 0))
	if !ok {
		t.Fatal("the well-formed window must still match")
	}
	if w.BudgetRung {
		t.Error("the malformed window must be skipped, not used")
	}
}

func TestInWindowGapIsNotInAnyWindow(t *testing.T) {
	s := Schedule{Windows: []Window{{From: "07:00", To: "12:00"}}}
	if _, ok := s.InWindow(at(15, 0)); ok {
		t.Error("15:00 is outside the only window")
	}
}

// --- Window.StartOf -------------------------------------------------------

func TestWindowStartOfResolvesTheCoveringOccurrence(t *testing.T) {
	night := Window{From: "18:00", To: "07:00"}
	day := Window{From: "07:00", To: "18:00"}

	tests := []struct {
		name string
		w    Window
		now  time.Time
		want time.Time
	}{
		{"evening is today's occurrence", night, at(23, 0), at(18, 0)},
		{"after midnight belongs to yesterday's occurrence", night, at(4, 0), at(18, 0).AddDate(0, 0, -1)},
		{"exactly at the boundary starts now", night, at(18, 0), at(18, 0)},
		{"day window", day, at(12, 0), at(7, 0)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.w.StartOf(tc.now); !got.Equal(tc.want) {
				t.Errorf("got %v want %v", got, tc.want)
			}
		})
	}
}

func TestWindowStartOfMalformedFromIsZero(t *testing.T) {
	if got := (Window{From: "nope", To: "07:00"}).StartOf(at(12, 0)); !got.IsZero() {
		t.Errorf("a malformed From must yield the zero time, got %v", got)
	}
}

// --- Schedule.RungGuard ---------------------------------------------------

func TestRungGuard(t *testing.T) {
	s := DefaultConfig().Schedule // 18:00-07:00 no rung, 07:00-18:00 rung

	tests := []struct {
		name      string
		now       time.Time
		wantOn    bool
		wantGuard time.Time
	}{
		{
			// Inside the rung window the guarded instant is now itself, so the
			// measured span is the plain trailing window.
			name: "inside the rung window guards now", now: at(12, 0),
			wantOn: true, wantGuard: at(12, 0),
		},
		{
			// 04:00 + 5h = 09:00, past the 07:00 handover: this spend is still
			// inside the operator's window when they sit down.
			name: "pre-dawn shoulder guards the day start", now: at(4, 0),
			wantOn: true, wantGuard: at(7, 0),
		},
		{
			// 01:00 + 5h = 06:00, aged out before 07:00 — burn freely.
			name: "deep night is unguarded", now: at(1, 0),
			wantOn: false, wantGuard: at(7, 0),
		},
		{
			// Exactly window_hours out is the first guarded instant.
			name: "shoulder starts exactly window_hours before", now: at(2, 0),
			wantOn: true, wantGuard: at(7, 0),
		},
		{
			// The evening rolls over to tomorrow's day window, far outside it.
			name: "evening guards tomorrow and is unguarded", now: at(20, 0),
			wantOn: false, wantGuard: at(7, 0).AddDate(0, 0, 1),
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			guard, on := s.RungGuard(tc.now, 5)
			if on != tc.wantOn {
				t.Fatalf("applies=%v want %v (guard %v)", on, tc.wantOn, guard)
			}
			if !guard.Equal(tc.wantGuard) {
				t.Errorf("guard=%v want %v", guard, tc.wantGuard)
			}
		})
	}
}

func TestRungGuardNoRungWindowIsNeverGuarded(t *testing.T) {
	s := Schedule{Windows: []Window{{From: "18:00", To: "07:00"}}}
	guard, on := s.RungGuard(at(4, 0), 5)
	if on || !guard.IsZero() {
		t.Errorf("with no budget_rung window there is nothing to guard: guard=%v on=%v", guard, on)
	}
}

// Window boundaries are wall-clock times, so resolving one must use calendar
// arithmetic. Adding a duration to midnight is off by an hour on a DST day —
// in opposite directions in spring and autumn — and because InWindow compares
// wall-clock minutes, the two would disagree about the same window twice a
// year. That reintroduced the inherited-night-counter bug the nightly cap fix
// was about: a boundary resolving to the previous day makes DispatchesSince
// return the previous night's count.
func TestWindowBoundariesSurviveDSTTransitions(t *testing.T) {
	zurich, err := time.LoadLocation("Europe/Zurich")
	if err != nil {
		t.Skip("no tzdata available:", err)
	}
	night := Window{From: "18:00", To: "07:00"}
	rung := Schedule{Windows: []Window{night, {From: "07:00", To: "18:00", BudgetRung: true}}}

	tests := []struct {
		name          string
		now           time.Time
		wantStart     time.Time
		wantRungGuard time.Time
	}{
		{
			// 2027-03-28 is 23h long in Zurich: midnight+18h lands at 19:00,
			// which reads as "after now" and rolls the start back a full day.
			name:          "spring forward evening",
			now:           time.Date(2027, 3, 28, 18, 30, 0, 0, zurich),
			wantStart:     time.Date(2027, 3, 28, 18, 0, 0, 0, zurich),
			wantRungGuard: time.Date(2027, 3, 29, 7, 0, 0, 0, zurich),
		},
		{
			name:          "spring forward pre-dawn",
			now:           time.Date(2027, 3, 28, 4, 0, 0, 0, zurich),
			wantStart:     time.Date(2027, 3, 27, 18, 0, 0, 0, zurich),
			wantRungGuard: time.Date(2027, 3, 28, 7, 0, 0, 0, zurich),
		},
		{
			// 2027-10-31 is 25h long: midnight+18h lands at 17:00, so the
			// rung would disarm an hour before the real handover.
			name:          "fall back evening",
			now:           time.Date(2027, 10, 31, 18, 30, 0, 0, zurich),
			wantStart:     time.Date(2027, 10, 31, 18, 0, 0, 0, zurich),
			wantRungGuard: time.Date(2027, 11, 1, 7, 0, 0, 0, zurich),
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := night.StartOf(tc.now); !got.Equal(tc.wantStart) {
				t.Errorf("StartOf = %v, want %v", got, tc.wantStart)
			}
			guard, _ := rung.RungGuard(tc.now, 5)
			if !guard.Equal(tc.wantRungGuard) {
				t.Errorf("RungGuard = %v, want %v", guard, tc.wantRungGuard)
			}
		})
	}
}

// The two helpers must agree about a window on a DST day: InWindow decides
// whether the nightly cap applies, StartOf decides which occurrence its
// counter belongs to, and a disagreement is what silently blocks dispatch.
func TestInWindowAndStartOfAgreeAcrossDST(t *testing.T) {
	zurich, err := time.LoadLocation("Europe/Zurich")
	if err != nil {
		t.Skip("no tzdata available:", err)
	}
	s := DefaultConfig().Schedule

	for _, now := range []time.Time{
		time.Date(2027, 3, 28, 18, 30, 0, 0, zurich),
		time.Date(2027, 3, 28, 4, 0, 0, 0, zurich),
		time.Date(2027, 10, 31, 18, 30, 0, 0, zurich),
		time.Date(2027, 10, 31, 2, 30, 0, 0, zurich),
	} {
		w, ok := s.InWindow(now)
		if !ok {
			t.Fatalf("%v: default windows tile the day, so something must cover it", now)
		}
		start := w.StartOf(now)
		if start.After(now) {
			t.Errorf("%v: StartOf returned a future boundary %v — InWindow says this window covers now", now, start)
		}
	}
}
