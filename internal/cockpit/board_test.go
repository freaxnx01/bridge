package cockpit

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/freaxnx01/bridge/internal/agentview"
	"github.com/freaxnx01/bridge/internal/dispatch"
	"github.com/freaxnx01/bridge/internal/forge"
)

const readyBody = "spec\n\n## Implementation Plan\n\n- step"

var t0 = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

func issue(n int, labels ...string) forge.Issue {
	return forge.Issue{
		Forge: "github", Repo: "o/r", Number: n, Title: "t", URL: "u",
		Body: readyBody, Labels: labels, Created: t0.Add(time.Duration(n) * time.Hour),
		Updated: t0.Add(time.Duration(n) * time.Minute),
	}
}

func repo(issues ...forge.Issue) RepoData {
	return RepoData{Forge: "github", Owner: "o", Name: "r", Path: "/r/r", Wired: true, Issues: issues}
}

func build(rd ...RepoData) Board { return BuildBoard(Input{Repos: rd}) }

func TestBuildBoard_ParkedAndRoadmap_Dropped(t *testing.T) {
	tests := []struct{ name, label string }{
		{"parked", dispatch.LabelParked},
		{"roadmap", LabelRoadmap},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			b := build(repo(issue(1, tc.label, dispatch.LabelNeedsEnrichment)))
			if len(b.Ready)+len(b.Held)+len(b.NeedsEnrichment)+len(b.Running) != 0 {
				t.Errorf("want dropped: %+v", b)
			}
		})
	}
}

func TestBuildBoard_EnrichmentOngoing_OnlyInRunning(t *testing.T) {
	b := build(repo(issue(1, dispatch.LabelNeedsEnrichment, LabelEnrichmentOngoing)))
	if len(b.Running) != 1 || len(b.NeedsEnrichment) != 0 {
		t.Fatalf("%+v", b)
	}
	r := b.Running[0]
	if r.Kind != KindEnrichment || r.Status != "enriching" || r.Repo != "o/r" || r.Issue != 1 || !r.Since.Equal(t0.Add(time.Minute)) {
		t.Errorf("%+v", r)
	}
}

func TestBuildBoard_ToBeDefined_InNeedsEnrichment_AllForges(t *testing.T) {
	fj := issue(2, dispatch.LabelToBeDefined)
	fj.Forge = "forgejo"
	b := build(repo(issue(1, dispatch.LabelNeedsEnrichment)), RepoData{Forge: "forgejo", Owner: "o", Name: "f", Issues: []forge.Issue{fj}})
	if len(b.NeedsEnrichment) != 2 || b.NeedsEnrichment[1].Forge != "forgejo" || b.NeedsEnrichment[1].Repo != "o/f" {
		t.Errorf("%+v", b.NeedsEnrichment)
	}
}

func TestBuildBoard_EligibleOnWiredGithub_InReady(t *testing.T) {
	b := build(repo(issue(1)))
	if len(b.Ready) != 1 || b.Ready[0].Number != 1 || b.Ready[0].Repo != "o/r" {
		t.Errorf("%+v", b.Ready)
	}
}

func TestBuildBoard_UnwiredRepo_NotInReady(t *testing.T) {
	rd := repo(issue(1))
	rd.Wired = false
	if b := build(rd); len(b.Ready)+len(b.Held) != 0 {
		t.Errorf("%+v", b)
	}
}

func TestBuildBoard_OutsideMilestone_InHeldWithReason(t *testing.T) {
	i := issue(1)
	i.Milestone = "later"
	rd := repo(i)
	rd.Milestones = []forge.Milestone{{Title: "now", DueOn: t0}, {Title: "later", DueOn: t0.Add(time.Hour)}}
	b := build(rd)
	if len(b.Held) != 1 || b.Held[0].Reason != "outside active milestone" || len(b.Ready) != 0 {
		t.Errorf("%+v", b)
	}
}

func TestBuildBoard_AIImplementIssue_NoColumn(t *testing.T) {
	b := build(repo(issue(1, dispatch.LabelAIImplement)))
	if len(b.Ready)+len(b.Held)+len(b.NeedsEnrichment) != 0 {
		t.Errorf("%+v", b)
	}
}

func run(status, event string) forge.WorkflowRun {
	return forge.WorkflowRun{Name: "agent", Event: event, Status: status, Created: t0, URL: "ru"}
}

func TestBuildBoard_InProgressIssuesRun_RunningLinkedToSingleAIImplementIssue(t *testing.T) {
	rd := repo(issue(7, dispatch.LabelAIImplement))
	rd.Runs = []forge.WorkflowRun{run("in_progress", "issues"), run("queued", "issues")}
	b := build(rd)
	if len(b.Running) != 2 {
		t.Fatalf("%+v", b.Running)
	}
	r := b.Running[0]
	if r.Kind != KindPipeline || r.Issue != 7 || r.Title != "t" || r.URL != "ru" || r.Repo != "o/r" {
		t.Errorf("%+v", r)
	}
}

func TestBuildBoard_TwoAIImplementIssues_BareRun(t *testing.T) {
	rd := repo(issue(7, dispatch.LabelAIImplement), issue(8, dispatch.LabelAIImplement))
	rd.Runs = []forge.WorkflowRun{run("in_progress", "issues")}
	r := build(rd).Running[0]
	if r.Issue != 0 || r.Title != "agent" {
		t.Errorf("%+v", r)
	}
}

func TestBuildBoard_CompletedRun_NotRunning(t *testing.T) {
	rd := repo()
	rd.Runs = []forge.WorkflowRun{run("completed", "issues"), run("in_progress", "push")}
	if b := build(rd); len(b.Running) != 0 {
		t.Errorf("%+v", b.Running)
	}
}

func TestBuildBoard_SessionInWorktree_AttributedToRepo(t *testing.T) {
	bridge := RepoData{Owner: "o", Name: "bridge", Path: "/r/bridge"}
	bot := RepoData{Owner: "o", Name: "bridge-bot", Path: "/r/bridge-bot"}
	tests := []struct{ cwd, want string }{
		{"/r/bridge/.worktrees/webui", "o/bridge"},
		{"/r/bridge-bot", "o/bridge-bot"},
		{"/r/bridge", "o/bridge"},
		{"/tmp", ""},
	}
	for _, tc := range tests {
		t.Run(tc.cwd, func(t *testing.T) {
			b := BuildBoard(Input{Repos: []RepoData{bridge, bot}, Sessions: []agentview.Session{
				{CWD: tc.cwd, Name: "n", Status: "busy", StartedAt: t0, SessionID: "s"},
			}})
			r := b.Running[0]
			if r.Kind != KindSession || r.Repo != tc.want || r.Title != "n" || r.SessionID != "s" || r.CWD != tc.cwd {
				t.Errorf("%+v", r)
			}
		})
	}
}

func TestBuildBoard_Running_SortedBySince(t *testing.T) {
	rd := repo()
	rd.Runs = []forge.WorkflowRun{run("queued", "issues")}
	b := BuildBoard(Input{Repos: []RepoData{rd}, Sessions: []agentview.Session{{CWD: "/tmp", StartedAt: t0.Add(-time.Hour)}}})
	if b.Running[0].Kind != KindSession {
		t.Errorf("%+v", b.Running)
	}
}

func TestBuildBoard_EmptyInput_SlicesNotNil(t *testing.T) {
	b := BuildBoard(Input{DispatchPaused: true})
	raw, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	for _, want := range []string{`"running":[]`, `"ready":[]`, `"held":[]`, `"needsEnrichment":[]`, `"errors":[]`, `"dispatchPaused":true`} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %s in %s", want, s)
		}
	}
}

func TestBuildBoard_NoPlanOrEmptyBody_WiredRepo_InNeedsEnrichmentWithReason(t *testing.T) {
	noPlan := issue(1)
	noPlan.Body = "just a description"
	empty := issue(2)
	empty.Body = "  \n"
	b := build(repo(noPlan, empty))
	if len(b.NeedsEnrichment) != 2 || len(b.Ready)+len(b.Held) != 0 {
		t.Fatalf("%+v", b)
	}
	if b.NeedsEnrichment[0].Reason != "no implementation plan" || b.NeedsEnrichment[1].Reason != "empty body" {
		t.Errorf("reasons = %q, %q", b.NeedsEnrichment[0].Reason, b.NeedsEnrichment[1].Reason)
	}
}

func TestBuildBoard_NoPlanOrEmptyBody_UnwiredRepo_Dropped(t *testing.T) {
	noPlan := issue(1)
	noPlan.Body = "just a description"
	empty := issue(2)
	empty.Body = ""
	rd := repo(noPlan, empty)
	rd.Wired = false
	if b := build(rd); len(b.NeedsEnrichment)+len(b.Ready)+len(b.Held) != 0 {
		t.Errorf("%+v", b)
	}
}

func TestBuildBoard_EnrichmentOngoing_CardCarriesIssueURL(t *testing.T) {
	i := issue(1, LabelEnrichmentOngoing)
	i.URL = "https://github.com/o/r/issues/1"
	b := build(repo(i))
	if len(b.Running) != 1 || b.Running[0].URL != i.URL {
		t.Errorf("%+v", b.Running)
	}
}
