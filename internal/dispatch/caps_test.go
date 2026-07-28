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
	ds := ApplyCaps([]Candidate{cand("quotes", 1), cand("quotes", 2)}, cfg, map[string]int{}, 0, 0)

	if !ds[0].Dispatch {
		t.Errorf("first should dispatch: %+v", ds[0])
	}
	if ds[1].Dispatch || ds[1].Reason != "repo at WIP 1/1" {
		t.Errorf("second: %+v", ds[1])
	}
}

func TestApplyCapsCountsExistingOpenPRs(t *testing.T) {
	cfg := DefaultConfig()
	ds := ApplyCaps([]Candidate{cand("quotes", 1)}, cfg, map[string]int{"quotes": 1}, 1, 0)
	if ds[0].Dispatch {
		t.Errorf("repo already at limit, must skip: %+v", ds[0])
	}
}

func TestApplyCapsGlobal(t *testing.T) {
	cfg := DefaultConfig()
	cs := []Candidate{cand("a", 1), cand("b", 2), cand("c", 3), cand("d", 4)}
	ds := ApplyCaps(cs, cfg, map[string]int{}, 0, 0)

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
	ds := ApplyCaps([]Candidate{cand("a", 1), cand("b", 2)}, cfg, map[string]int{}, 0, 0)
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
	ds := ApplyCaps([]Candidate{cand("a", 1)}, cfg, map[string]int{}, 0, 2)
	if ds[0].Dispatch {
		t.Errorf("night budget spent, must skip: %+v", ds[0])
	}
}

func TestApplyCapsUsesPerRepoOverride(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Limits.Overrides = map[string]int{"quotes": 2}
	ds := ApplyCaps([]Candidate{cand("quotes", 1), cand("quotes", 2)}, cfg, map[string]int{}, 0, 0)
	if !ds[0].Dispatch || !ds[1].Dispatch {
		t.Errorf("override 2 should allow both: %+v %+v", ds[0], ds[1])
	}
}
