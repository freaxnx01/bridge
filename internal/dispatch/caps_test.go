package dispatch

import (
	"testing"

	"github.com/freaxnx01/bridge/internal/forge"
)

func cand(repo string, n int) Candidate {
	return Candidate{Repo: repo, Issue: forge.Issue{Number: n}}
}

func TestApplyCapsPerRepo(t *testing.T) {
	cfg := DefaultConfig() // per_repo 1, global 3, night 5
	ds := ApplyCaps([]Candidate{cand("quotes", 1), cand("quotes", 2)}, cfg, Counts{}, BudgetState{})

	if !ds[0].Dispatch {
		t.Errorf("first should dispatch: %+v", ds[0])
	}
	if ds[1].Dispatch || ds[1].Reason != "repo at WIP 1/1" {
		t.Errorf("second: %+v", ds[1])
	}
}

func TestApplyCapsCountsExistingOpenPRs(t *testing.T) {
	cfg := DefaultConfig()
	ds := ApplyCaps([]Candidate{cand("quotes", 1)}, cfg, Counts{OpenPRsByRepo: map[string]int{"quotes": 1}, GlobalOpen: 1}, BudgetState{})
	if ds[0].Dispatch {
		t.Errorf("repo already at limit, must skip: %+v", ds[0])
	}
}

func TestApplyCapsGlobal(t *testing.T) {
	cfg := DefaultConfig()
	cs := []Candidate{cand("a", 1), cand("b", 2), cand("c", 3), cand("d", 4)}
	ds := ApplyCaps(cs, cfg, Counts{}, BudgetState{})

	for i := 0; i < 3; i++ {
		if !ds[i].Dispatch {
			t.Errorf("index %d should dispatch: %+v", i, ds[i])
		}
	}
	if ds[3].Dispatch || ds[3].Reason != "global cap 3/3" {
		t.Errorf("fourth: %+v", ds[3])
	}
}

func TestApplyCapsNightlyCeiling(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Limits.MaxDispatchesPerNight = 1
	ds := ApplyCaps([]Candidate{cand("a", 1), cand("b", 2)}, cfg, Counts{NightCapApplies: true}, BudgetState{})
	if !ds[0].Dispatch {
		t.Errorf("first: %+v", ds[0])
	}
	if ds[1].Dispatch || ds[1].Reason != "night cap 1/1" {
		t.Errorf("second: %+v", ds[1])
	}
}

func TestApplyCapsRespectsAlreadyDispatchedTonight(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Limits.MaxDispatchesPerNight = 2
	ds := ApplyCaps([]Candidate{cand("a", 1)}, cfg, Counts{DispatchedTonight: 2, NightCapApplies: true}, BudgetState{})
	if ds[0].Dispatch {
		t.Errorf("night budget spent, must skip: %+v", ds[0])
	}
}

func TestApplyCapsUsesPerRepoOverride(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Limits.Overrides = map[string]int{"quotes": 2}
	ds := ApplyCaps([]Candidate{cand("quotes", 1), cand("quotes", 2)}, cfg, Counts{}, BudgetState{})
	if !ds[0].Dispatch || !ds[1].Dispatch {
		t.Errorf("override 2 should allow both: %+v %+v", ds[0], ds[1])
	}
}

// The nightly cap bounds *unattended* spend, so it may only apply in a window
// whose budget rung is off. Once windows tile the whole day, applying it
// unconditionally blocks the daytime path using the night's spent counter.
func TestApplyCapsNightCapOnlyAppliesToUnattendedWindows(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Limits.MaxDispatchesPerNight = 1

	t.Run("unattended window enforces it", func(t *testing.T) {
		ds := ApplyCaps([]Candidate{cand("a", 1)}, cfg,
			Counts{DispatchedTonight: 1, NightCapApplies: true}, BudgetState{})
		if ds[0].Dispatch || ds[0].Reason != "night cap 1/1" {
			t.Errorf("%+v", ds[0])
		}
	})

	t.Run("rung window ignores it", func(t *testing.T) {
		ds := ApplyCaps([]Candidate{cand("a", 1)}, cfg,
			Counts{DispatchedTonight: 99, NightCapApplies: false}, BudgetState{})
		if !ds[0].Dispatch {
			t.Errorf("the budget rung bounds this window, not the night counter: %+v", ds[0])
		}
	})
}

func laneCand(repo string, n int, lane Lane) Candidate {
	c := cand(repo, n)
	c.Lane = lane
	return c
}

func TestApplyCapsAutonomousLaneIgnoresTheGlobalCap(t *testing.T) {
	cfg := DefaultConfig() // global 3
	auto := Lane{Name: "auto", Autonomous: true}
	cs := []Candidate{
		laneCand("game-a", 1, auto), laneCand("game-b", 2, auto),
		laneCand("game-c", 3, auto), laneCand("game-d", 4, auto),
	}

	ds := ApplyCaps(cs, cfg, Counts{GlobalOpen: 3}, BudgetState{})
	for i, d := range ds {
		if !d.Dispatch {
			t.Errorf("index %d: an autonomous lane spends no review capacity: %+v", i, d)
		}
	}
}

func TestApplyCapsAutonomousDispatchesDoNotConsumeTheGlobalCap(t *testing.T) {
	cfg := DefaultConfig() // global 3
	auto := Lane{Name: "auto", Autonomous: true}
	hitl := Lane{Name: "hitl"}
	cs := []Candidate{
		laneCand("game-a", 1, auto), laneCand("game-b", 2, auto), laneCand("game-c", 3, auto),
		laneCand("bridge", 4, hitl),
	}

	ds := ApplyCaps(cs, cfg, Counts{}, BudgetState{})
	if !ds[3].Dispatch {
		t.Errorf("three auto dispatches must leave the hitl lane its slots: %+v", ds[3])
	}
}

func TestApplyCapsLaneCeiling(t *testing.T) {
	cfg := DefaultConfig()
	auto := Lane{Name: "auto", Autonomous: true, Limits: LaneLimits{MaxDispatches: 2}}
	cs := []Candidate{laneCand("game-a", 1, auto), laneCand("game-b", 2, auto), laneCand("game-c", 3, auto)}

	ds := ApplyCaps(cs, cfg, Counts{}, BudgetState{})
	if !ds[0].Dispatch || !ds[1].Dispatch {
		t.Fatalf("first two: %+v %+v", ds[0], ds[1])
	}
	if ds[2].Dispatch || ds[2].Reason != "lane cap 2/2 (auto)" {
		t.Errorf("third: %+v", ds[2])
	}
}

func TestApplyCapsLaneCeilingCountsWhatTheOccurrenceAlreadySpent(t *testing.T) {
	cfg := DefaultConfig()
	auto := Lane{Name: "auto", Autonomous: true, Limits: LaneLimits{MaxDispatches: 2}}

	ds := ApplyCaps([]Candidate{laneCand("game-a", 1, auto)}, cfg,
		Counts{DispatchedByLane: map[string]int{"auto": 2}}, BudgetState{})
	if ds[0].Dispatch || ds[0].Reason != "lane cap 2/2 (auto)" {
		t.Errorf("%+v", ds[0])
	}
}

func TestApplyCapsLaneOverridesThePerRepoLimit(t *testing.T) {
	cfg := DefaultConfig() // per_repo 1
	lane := Lane{Name: "auto", Limits: LaneLimits{PerRepo: 2}}

	ds := ApplyCaps([]Candidate{laneCand("game-a", 1, lane), laneCand("game-a", 2, lane)},
		cfg, Counts{}, BudgetState{})
	if !ds[0].Dispatch || !ds[1].Dispatch {
		t.Errorf("the lane's per_repo 2 must win over the top-level 1: %+v %+v", ds[0], ds[1])
	}
}

// A dry-run lane must be able to observe the world without changing what the
// live lanes are allowed to do — otherwise the observation week throttles the
// work it is meant to observe.
func TestApplyCapsDryRunLaneConsumesNothingShared(t *testing.T) {
	cfg := DefaultConfig() // global 3
	dry := Lane{Name: "auto", Autonomous: false, DryRun: true}
	hitl := Lane{Name: "hitl"}
	cs := []Candidate{
		laneCand("game-a", 1, dry), laneCand("game-b", 2, dry), laneCand("game-c", 3, dry),
		laneCand("bridge", 4, hitl), laneCand("quotes", 5, hitl), laneCand("flowhub", 6, hitl),
	}

	ds := ApplyCaps(cs, cfg, Counts{}, BudgetState{
		Enabled: true, UsedUSD: 0, LimitUSD: 9.6, PerRunUSD: 2.0,
	})

	for i := 3; i < 6; i++ {
		if !ds[i].Dispatch {
			t.Errorf("hitl candidate %d must be unaffected by the dry-run lane: %+v", i, ds[i])
		}
	}
}

func TestApplyCapsDryRunLaneStillHitsItsOwnCeiling(t *testing.T) {
	cfg := DefaultConfig()
	dry := Lane{Name: "auto", DryRun: true, Limits: LaneLimits{MaxDispatches: 1}}

	ds := ApplyCaps([]Candidate{laneCand("game-a", 1, dry), laneCand("game-b", 2, dry)},
		cfg, Counts{}, BudgetState{})
	if !ds[0].Dispatch {
		t.Fatalf("first: %+v", ds[0])
	}
	if ds[1].Dispatch || ds[1].Reason != "lane cap 1/1 (auto)" {
		t.Errorf("the lane's own counter is private, so it still bites: %+v", ds[1])
	}
}

func TestPartitionByWindow(t *testing.T) {
	s := DefaultConfig().Schedule
	acting := Lane{Name: "auto", Windows: []Span{{From: "00:00", To: "00:00"}}}
	asleep := Lane{Name: "night", Windows: []Span{{From: "22:00", To: "23:00"}}}

	act, skipped := PartitionByWindow(
		[]Candidate{laneCand("game-a", 1, acting), laneCand("bridge", 2, asleep)},
		s, at(13, 0))

	if len(act) != 1 || act[0].Repo != "game-a" {
		t.Errorf("acting: %+v", act)
	}
	if len(skipped) != 1 || skipped[0].Dispatch || skipped[0].Reason != "outside lane window (night)" {
		t.Errorf("skipped: %+v", skipped)
	}
}
