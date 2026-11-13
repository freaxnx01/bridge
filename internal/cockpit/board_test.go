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
		{"parked legacy", dispatch.LabelParkedLegacy},
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

// The pipeline parks with the ASCII label, so a parked issue that is otherwise a
// perfectly good dispatch candidate must still reach no column at all.
func TestBuildBoard_ParkedIssue_NotAGateCandidate(t *testing.T) {
	b := build(repo(issue(1, dispatch.LabelParked)))
	if len(b.Ready)+len(b.Held)+len(b.NeedsEnrichment)+len(b.Running) != 0 {
		t.Errorf("want dropped: %+v", b)
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

func TestBuildBoard_RunningCards_CarryForge(t *testing.T) {
	ado := RepoData{Forge: "ado", Owner: "p", Name: "s", Path: "/r/s"}
	pipelineRepo := func() Input {
		rd := repo(issue(7, dispatch.LabelAIImplement))
		rd.Runs = []forge.WorkflowRun{run("in_progress", "issues")}
		return Input{Repos: []RepoData{rd}}
	}
	tests := []struct {
		name string
		in   Input
		want string
	}{
		{"pipeline run", pipelineRepo(), "github"},
		{"enrichment", Input{Repos: []RepoData{repo(issue(1, LabelEnrichmentOngoing))}}, "github"},
		{"session in an ado repo", Input{Repos: []RepoData{ado}, Sessions: []agentview.Session{
			{CWD: "/r/s/sub", Name: "n", StartedAt: t0},
		}}, "ado"},
		{"session outside every repo", Input{Repos: []RepoData{ado}, Sessions: []agentview.Session{
			{CWD: "/tmp", Name: "n", StartedAt: t0},
		}}, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			b := BuildBoard(tc.in)
			if len(b.Running) != 1 {
				t.Fatalf("Running = %+v, want one card", b.Running)
			}
			if b.Running[0].Forge != tc.want {
				t.Errorf("Forge = %q, want %q", b.Running[0].Forge, tc.want)
			}
			raw, err := json.Marshal(b.Running[0])
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.Contains(string(raw), `"forge"`); got != (tc.want != "") {
				t.Errorf("body %s, want a forge field: %v", raw, tc.want != "")
			}
		})
	}
}

// previewTick is a dispatch preview deciding the issues given, in the order
// given, with the first one dispatching.
func previewTick(ds ...dispatch.Decision) *dispatch.Tick {
	return &dispatch.Tick{
		Decisions: ds,
		Gates:     dispatch.Gates{At: t0, WindowOpen: true, GlobalCap: 3},
	}
}

func previewDecision(number int, dispatchIt bool, reason string) dispatch.Decision {
	return dispatch.Decision{
		Candidate: dispatch.Candidate{
			Owner: "o", Repo: "r", Issue: issue(number),
			Lane: dispatch.Lane{Name: "auto"},
		},
		Dispatch: dispatchIt,
		Reason:   reason,
		Rank:     dispatch.Rank{Position: number},
	}
}

func TestBuildBoard_WithPreview_ReadyInPreviewOrderWithVerdicts(t *testing.T) {
	b := BuildBoard(Input{
		Repos: []RepoData{repo(issue(1), issue(2))},
		Preview: previewTick(
			previewDecision(2, true, ""),
			previewDecision(1, false, "repo at WIP 1/1"),
		),
	})

	if len(b.Ready) != 2 {
		t.Fatalf("ready = %+v", b.Ready)
	}
	if b.Ready[0].Number != 2 || b.Ready[0].Verdict != "next" {
		t.Errorf("first card = %+v", b.Ready[0])
	}
	if b.Ready[1].Number != 1 || b.Ready[1].Verdict != "skip" || b.Ready[1].VerdictReason != "repo at WIP 1/1" {
		t.Errorf("second card = %+v", b.Ready[1])
	}
	if b.Ready[0].Lane != "auto" {
		t.Errorf("lane = %q", b.Ready[0].Lane)
	}
	if b.Gates == nil || !b.Gates.WindowOpen {
		t.Errorf("gates = %+v", b.Gates)
	}
}

func TestBuildBoard_WithoutPreview_UnchangedOrderAndNoVerdicts(t *testing.T) {
	b := build(repo(issue(2), issue(1)))

	if len(b.Ready) != 2 {
		t.Fatalf("ready = %+v", b.Ready)
	}
	// The board's own ladder order: oldest first, #1 created before #2.
	if b.Ready[0].Number != 1 || b.Ready[1].Number != 2 {
		t.Errorf("order = %d, %d", b.Ready[0].Number, b.Ready[1].Number)
	}
	for _, c := range b.Ready {
		if c.Verdict != "" || c.VerdictReason != "" {
			t.Errorf("card = %+v", c)
		}
	}
	if b.Gates != nil {
		t.Errorf("gates = %+v", b.Gates)
	}
}

// A Ready issue the preview never saw — collected after it ran — keeps its
// place at the end rather than disappearing from the column.
func TestBuildBoard_ReadyIssueMissingFromPreview_KeptWithoutAVerdict(t *testing.T) {
	b := BuildBoard(Input{
		Repos:   []RepoData{repo(issue(1), issue(2))},
		Preview: previewTick(previewDecision(2, true, "")),
	})

	if len(b.Ready) != 2 {
		t.Fatalf("ready = %+v", b.Ready)
	}
	if b.Ready[1].Number != 1 || b.Ready[1].Verdict != "" {
		t.Errorf("trailing card = %+v", b.Ready[1])
	}
}

// A lane downgrade is the question the observation week exists to answer, so
// it shows on the card whatever the verdict.
func TestBuildBoard_PreviewLaneDowngrade_ShowsOnTheCard(t *testing.T) {
	d := previewDecision(1, true, "")
	d.Candidate.LaneReason = "auto→hitl: agent.yml does not opt into ai-merge"

	b := BuildBoard(Input{Repos: []RepoData{repo(issue(1))}, Preview: previewTick(d)})

	if b.Ready[0].LaneReason != d.Candidate.LaneReason {
		t.Errorf("laneReason = %q", b.Ready[0].LaneReason)
	}
}
