package dispatch

import (
	"testing"
	"time"

	"github.com/freaxnx01/bridge/internal/forge"
)

func TestCollectCandidates_NonGithubAndIneligible_AreSkipped(t *testing.T) {
	repos := []RepoInput{
		{Forge: "github", Owner: "o", Name: "quotes",
			Issues: []forge.Issue{
				{Number: 41, Body: "a real description\n## Implementation Plan\n- step", Labels: []string{"feat"}},
				{Number: 42, Body: "d", Labels: []string{"needs-enrichment"}},
				{Number: 31, Body: ""}, // capture-created, no body, no labels
			}},
		{Forge: "forgejo", Owner: "f", Name: "notes",
			Issues: []forge.Issue{{Number: 1, Body: "d\n## Implementation Plan\n- step", Labels: []string{"feat"}}}},
	}

	got := CollectCandidates(repos)
	if len(got) != 1 {
		t.Fatalf("got %d candidates: %+v", len(got), got)
	}
	if got[0].Issue.Number != 41 || got[0].Repo != "quotes" {
		t.Errorf("got %+v", got[0])
	}
}

func TestAssignLanes_GatedAndUngatedRepos_ResolveInPlace(t *testing.T) {
	lanes := []Lane{
		{Name: "auto", Repos: []string{"game-*"}, Autonomous: true},
		{Name: "hitl", Repos: []string{"*"}},
	}
	cs := []Candidate{
		{Repo: "game-tschau-sepp", Issue: forge.Issue{Number: 1}},
		{Repo: "game-huusli-jagd", Issue: forge.Issue{Number: 2}},
		{Repo: "bridge", Issue: forge.Issue{Number: 3}},
	}

	AssignLanes(cs, lanes, GateState{"game-tschau-sepp": true})

	if cs[0].Lane.Name != "auto" || cs[0].LaneReason != "" {
		t.Errorf("gated repo stays in auto: %+v", cs[0])
	}
	if cs[1].Lane.Name != "hitl" || cs[1].LaneReason == "" {
		t.Errorf("ungated repo falls back with a reason: %+v", cs[1])
	}
	if cs[2].Lane.Name != "hitl" {
		t.Errorf("catch-all: %+v", cs[2])
	}
}

func TestCountOpenAgentPRs_AutonomousRepo_IsOutOfTheGlobalTotal(t *testing.T) {
	repos := []RepoInput{
		{Forge: "github", Owner: "freaxnx01", Name: "game-tschau-sepp",
			Issues: []forge.Issue{{Number: 1}},
			PRs:    []forge.PullRequest{{Body: "Closes #1"}}},
		{Forge: "github", Owner: "freaxnx01", Name: "bridge",
			Issues: []forge.Issue{{Number: 9}},
			PRs:    []forge.PullRequest{{Body: "Closes #9"}}},
	}
	autonomous := func(repo string) bool { return repo == "game-tschau-sepp" }

	byRepo, global := CountOpenAgentPRs(repos, autonomous)

	if global != 1 {
		t.Errorf("only human-reviewed PRs spend review capacity: global=%d want 1", global)
	}
	// The per-repo count still has to include it — per_repo bounds conflicting
	// PRs in one repo, which is true in either lane.
	if byRepo["game-tschau-sepp"] != 1 {
		t.Errorf("per-repo count: %+v", byRepo)
	}
}

func TestAnyLaneActs_SchedulesAndLanes_ReportWhetherAnythingActs(t *testing.T) {
	now := time.Date(2026, 9, 7, 13, 0, 0, 0, time.Local)
	gap := Schedule{Windows: []Window{{Span: Span{From: "03:00", To: "03:01"}}}}

	tests := []struct {
		name string
		cfg  Config
		want bool
	}{
		{"no lanes, schedule covers now", Config{Schedule: DefaultConfig().Schedule}, true},
		{"no lanes, schedule gap", Config{Schedule: gap}, false},
		{"a 24h lane acts even in a schedule gap", Config{
			Schedule: gap,
			Lanes:    []Lane{{Name: "auto", Windows: []Span{{From: "00:00", To: "00:00"}}}},
		}, true},
		{"lanes all asleep, and the default lane is in a gap", Config{
			Schedule: gap,
			Lanes:    []Lane{{Name: "auto", Windows: []Span{{From: "22:00", To: "23:00"}}}},
		}, false},
		{"lanes asleep but the default lane's schedule covers now", Config{
			Schedule: DefaultConfig().Schedule,
			Lanes:    []Lane{{Name: "auto", Windows: []Span{{From: "22:00", To: "23:00"}}}},
		}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := AnyLaneActs(tc.cfg, now); got != tc.want {
				t.Errorf("got %v want %v", got, tc.want)
			}
		})
	}
}

func TestMergeInOrder_SeparateGroups_LandBackInLadderOrder(t *testing.T) {
	ordered := []Candidate{
		{Repo: "a", Issue: forge.Issue{Number: 1}},
		{Repo: "a", Issue: forge.Issue{Number: 2}},
		{Repo: "b", Issue: forge.Issue{Number: 3}},
	}
	acting := []Decision{{Candidate: ordered[1], Dispatch: true}}
	outOfWindow := []Decision{
		{Candidate: ordered[2], Reason: "outside lane window (auto)"},
		{Candidate: ordered[0], Reason: "outside lane window (auto)"},
	}

	got := MergeInOrder(ordered, acting, outOfWindow)

	if len(got) != 3 {
		t.Fatalf("got %d decisions", len(got))
	}
	for i, want := range []int{1, 2, 3} {
		if got[i].Candidate.Issue.Number != want {
			t.Errorf("decision %d is #%d, want #%d", i, got[i].Candidate.Issue.Number, want)
		}
	}
}

func TestLaneCounts_PerLaneOccurrence_ReadsTheSpentCounter(t *testing.T) {
	cfg := DefaultConfig()
	lane := Lane{Name: "auto", Windows: []Span{{From: "00:00", To: "00:00"}}}
	cfg.Lanes = []Lane{lane}
	now := time.Date(2026, 9, 7, 13, 0, 0, 0, time.Local)

	state := State{}
	state.RecordLaneDispatch("auto", lane.WindowStart(cfg.Schedule, now), now, 3)

	if got := LaneCounts(state, cfg, now)["auto"]; got != 3 {
		t.Errorf("lane counter: got %d want 3", got)
	}
}
