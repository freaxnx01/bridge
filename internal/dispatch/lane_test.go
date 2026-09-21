package dispatch

import (
	"testing"
	"time"
)

func TestLaneInWindowInheritsTheSchedule(t *testing.T) {
	s := DefaultConfig().Schedule // 18:00-07:00, 07:00-18:00 — tiles the day
	lane := Lane{Name: "hitl"}

	if !lane.InWindow(s, at(12, 0)) {
		t.Error("a lane with no windows of its own must inherit the schedule's")
	}
}

func TestLaneInWindowUsesItsOwnWindows(t *testing.T) {
	s := DefaultConfig().Schedule
	lane := Lane{Name: "auto", Windows: []Span{{From: "09:00", To: "11:00"}}}

	if !lane.InWindow(s, at(9, 30)) {
		t.Error("inside its own window")
	}
	if lane.InWindow(s, at(12, 0)) {
		t.Error("its own windows replace the schedule's, they do not add to them")
	}
}

func TestLaneInWindowFullDaySpan(t *testing.T) {
	// from == to is the 24h lane the auto lane uses.
	lane := Lane{Name: "auto", Windows: []Span{{From: "00:00", To: "00:00"}}}
	for _, h := range []int{0, 7, 13, 23} {
		if !lane.InWindow(Schedule{}, at(h, 0)) {
			t.Errorf("hour %d must be covered by a 24h span", h)
		}
	}
}

func TestLaneWindowStartIsTheOccurrenceBoundary(t *testing.T) {
	lane := Lane{Name: "auto", Windows: []Span{{From: "00:00", To: "00:00"}}}
	now := at(13, 30)

	got := lane.WindowStart(Schedule{}, now)
	want := time.Date(2026, 9, 7, 0, 0, 0, 0, time.Local)
	if !got.Equal(want) {
		t.Errorf("a 24h lane's occurrence starts at local midnight: got %s want %s", got, want)
	}
}

func TestLaneWindowStartOutsideEveryWindowIsZero(t *testing.T) {
	lane := Lane{Name: "auto", Windows: []Span{{From: "09:00", To: "11:00"}}}
	if got := lane.WindowStart(Schedule{}, at(13, 0)); !got.IsZero() {
		t.Errorf("no occurrence covers now, so there is no boundary: %s", got)
	}
}
