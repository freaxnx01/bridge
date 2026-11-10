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

func laneFixture() []Lane {
	return []Lane{
		{Name: "auto", Repos: []string{"game-*"}, Autonomous: true},
		{Name: "hitl", Repos: []string{"*"}},
	}
}

func TestResolveLane(t *testing.T) {
	gate := GateState{"game-tschau-sepp": true}

	tests := []struct {
		name       string
		repo       string
		wantLane   string
		wantReason string
	}{
		{"first matching lane wins", "game-tschau-sepp", "auto", ""},
		{"a repo outside the glob falls to the catch-all", "bridge", "hitl", ""},
		{"gate not met downgrades to the next non-autonomous lane", "game-huusli-jagd", "hitl",
			"auto→hitl: agent.yml lacks ai-review-ai-merge: true"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			lane, reason := ResolveLane(laneFixture(), tc.repo, gate)
			if lane.Name != tc.wantLane {
				t.Errorf("lane = %q want %q", lane.Name, tc.wantLane)
			}
			if reason != tc.wantReason {
				t.Errorf("reason = %q want %q", reason, tc.wantReason)
			}
		})
	}
}

func TestResolveLaneNoLanesConfiguredUsesTheDefaultLane(t *testing.T) {
	lane, reason := ResolveLane(nil, "bridge", nil)
	if lane.Name != DefaultLane().Name || reason != "" {
		t.Errorf("lane=%+v reason=%q", lane, reason)
	}
}

func TestResolveLaneDowngradeReachesTheDefaultLaneWhenNothingElseMatches(t *testing.T) {
	// An autonomous lane with no non-autonomous lane behind it must still fall
	// back — to the implicit default — rather than dispatch autonomously.
	lanes := []Lane{{Name: "auto", Repos: []string{"game-*"}, Autonomous: true}}
	lane, reason := ResolveLane(lanes, "game-huusli-jagd", GateState{})
	if lane.Name != DefaultLane().Name {
		t.Errorf("lane = %q want %q", lane.Name, DefaultLane().Name)
	}
	if reason != "auto→default: agent.yml lacks ai-review-ai-merge: true" {
		t.Errorf("reason = %q", reason)
	}
}

func TestResolveLaneMalformedGlobDoesNotMatch(t *testing.T) {
	// Ordering already treats a bad pattern as a non-match; lane resolution must
	// not fail the whole tick on a config typo either.
	lanes := []Lane{{Name: "broken", Repos: []string{"[unclosed"}}, {Name: "hitl", Repos: []string{"*"}}}
	if lane, _ := ResolveLane(lanes, "bridge", nil); lane.Name != "hitl" {
		t.Errorf("lane = %q", lane.Name)
	}
}

func TestAnyAutonomousLaneMatches(t *testing.T) {
	lanes := laneFixture()
	if !AnyAutonomousLaneMatches(lanes, "game-huusli-jagd") {
		t.Error("a repo an autonomous lane claims must be gate-checked even before the gate is known")
	}
	if AnyAutonomousLaneMatches(lanes, "bridge") {
		t.Error("no autonomous lane claims it, so no agent.yml fetch is warranted")
	}
}
