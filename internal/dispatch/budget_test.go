package dispatch

import (
	"strings"
	"testing"
)

func TestNewBudgetStateComputesTheLimit(t *testing.T) {
	b := Budget{WindowHours: 5, WindowBudgetUSD: 12, DaytimeCap: 0.8, MeanRunCostUSD: 2}
	s := NewBudgetState(b, true, 3, true)

	if diff := s.LimitUSD - 9.6; diff > 0.0001 || diff < -0.0001 {
		t.Errorf("limit = budget * cap: %v", s.LimitUSD)
	}
	if s.Unknown || !s.Enabled || s.UsedUSD != 3 || s.PerRunUSD != 2 {
		t.Errorf("%+v", s)
	}
}

func TestNewBudgetStateIsUnknownOnBadInput(t *testing.T) {
	ok := Budget{WindowHours: 5, WindowBudgetUSD: 12, DaytimeCap: 0.8, MeanRunCostUSD: 2}
	tests := []struct {
		name      string
		b         Budget
		usedKnown bool
	}{
		{"usage unreadable", ok, false},
		{"no window budget", Budget{WindowBudgetUSD: 0, DaytimeCap: 0.8, MeanRunCostUSD: 2}, true},
		{"no cap", Budget{WindowBudgetUSD: 12, DaytimeCap: 0, MeanRunCostUSD: 2}, true},
		{"no per-run estimate", Budget{WindowBudgetUSD: 12, DaytimeCap: 0.8, MeanRunCostUSD: 0}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if s := NewBudgetState(tc.b, true, 0, tc.usedKnown); !s.Unknown {
				t.Errorf("must fail closed: %+v", s)
			}
		})
	}
}

func TestApplyCapsBudgetRung(t *testing.T) {
	cfg := DefaultConfig()
	budget := func(used float64) BudgetState {
		return NewBudgetState(Budget{WindowBudgetUSD: 10, DaytimeCap: 1.0, MeanRunCostUSD: 2}, true, used, true)
	}

	t.Run("under cap dispatches", func(t *testing.T) {
		ds := ApplyCaps([]Candidate{cand("a", 1)}, cfg, Counts{}, budget(4))
		if !ds[0].Dispatch {
			t.Errorf("%+v", ds[0])
		}
	})

	t.Run("projected cost crossing the line refuses", func(t *testing.T) {
		// 9 used + 2 projected > 10 limit, even though 9 < 10.
		ds := ApplyCaps([]Candidate{cand("a", 1)}, cfg, Counts{}, budget(9))
		if ds[0].Dispatch || !strings.HasPrefix(ds[0].Reason, "budget-exhausted") {
			t.Errorf("%+v", ds[0])
		}
	})

	t.Run("projection accumulates within one tick", func(t *testing.T) {
		// Room for exactly two runs: 5 used + 2 + 2 = 9 <= 10, a third would be 11.
		cs := []Candidate{cand("a", 1), cand("b", 2), cand("c", 3)}
		ds := ApplyCaps(cs, cfg, Counts{}, budget(5))
		if !ds[0].Dispatch || !ds[1].Dispatch {
			t.Fatalf("first two should dispatch: %+v %+v", ds[0], ds[1])
		}
		if ds[2].Dispatch {
			t.Errorf("third must be refused on the accumulated projection: %+v", ds[2])
		}
	})

	t.Run("unknown usage refuses everything", func(t *testing.T) {
		unknown := NewBudgetState(Budget{WindowBudgetUSD: 10, DaytimeCap: 1, MeanRunCostUSD: 2}, true, 0, false)
		ds := ApplyCaps([]Candidate{cand("a", 1)}, cfg, Counts{}, unknown)
		if ds[0].Dispatch || ds[0].Reason != "budget-unknown" {
			t.Errorf("%+v", ds[0])
		}
	})

	t.Run("disabled rung ignores usage entirely", func(t *testing.T) {
		night := NewBudgetState(Budget{WindowBudgetUSD: 10, DaytimeCap: 1, MeanRunCostUSD: 2}, false, 999, true)
		ds := ApplyCaps([]Candidate{cand("a", 1)}, cfg, Counts{}, night)
		if !ds[0].Dispatch {
			t.Errorf("the night window must not consult the budget: %+v", ds[0])
		}
	})
}

func TestApplyCapsBudgetRungRunsBeforeTheOtherCaps(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Limits.MaxDispatchesPerNight = 1
	exhausted := NewBudgetState(Budget{WindowBudgetUSD: 1, DaytimeCap: 1, MeanRunCostUSD: 2}, true, 1, true)

	ds := ApplyCaps([]Candidate{cand("a", 1)}, cfg, Counts{DispatchedTonight: 5}, exhausted)
	if !strings.HasPrefix(ds[0].Reason, "budget-exhausted") {
		t.Errorf("the budget reason must win over the night cap: %q", ds[0].Reason)
	}
}
