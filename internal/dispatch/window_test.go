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
