package overview

import (
	"testing"
	"time"
)

func TestScoreItem_ValueEffortUrgencyStale(t *testing.T) {
	now := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	due := func(d time.Duration) *time.Time { t := now.Add(d); return &t }
	tests := []struct {
		name          string
		value, effort int
		due           *time.Time
		updated       time.Time
		wantScore     float64
		wantStale     bool
	}{
		{"bang_for_buck", 4, 2, nil, now, 2.0, false},
		{"effort_defaults_to_3", 4, 0, nil, now, 1.33, false},
		{"due_soon_plus1", 4, 2, due(10 * 24 * time.Hour), now, 3.0, false},
		{"due_urgent_plus2", 4, 2, due(2 * 24 * time.Hour), now, 4.0, false},
		{"overdue_plus2", 4, 2, due(-5 * 24 * time.Hour), now, 4.0, false},
		{"stale_high_value", 4, 2, nil, now.Add(-40 * 24 * time.Hour), 2.0, true},
		{"not_stale_low_value", 2, 2, nil, now.Add(-40 * 24 * time.Hour), 1.0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, stale := scoreItem(tt.value, tt.effort, tt.due, tt.updated, now)
			if got != tt.wantScore {
				t.Errorf("score = %v, want %v", got, tt.wantScore)
			}
			if stale != tt.wantStale {
				t.Errorf("stale = %v, want %v", stale, tt.wantStale)
			}
		})
	}
}

func TestWeightFromLabels(t *testing.T) {
	tests := []struct {
		name         string
		labels       []string
		wantV, wantE int
	}{
		{"both", []string{"value/4", "effort/2"}, 4, 2},
		{"value_only", []string{"value/5", "bug"}, 5, 0},
		{"none", []string{"bug", "chore"}, 0, 0},
		{"ignores_bad", []string{"value/x", "effort/9"}, 0, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v, e := weightFromLabels(tt.labels)
			if v != tt.wantV || e != tt.wantE {
				t.Errorf("got (%d,%d), want (%d,%d)", v, e, tt.wantV, tt.wantE)
			}
		})
	}
}
