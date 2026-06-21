package nav

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/freaxnx01/bridge/internal/core"
	"github.com/freaxnx01/bridge/internal/overview"
)

// fixedOverview returns a deterministic snapshot so the rendered Overview frame
// is stable for golden comparison (no forge/network).
func fixedOverview() overview.Snapshot {
	return overview.Snapshot{
		Ranked: []overview.RankedItem{
			{Repo: "bridge", Title: "wire REST api skeleton", Value: 4, Effort: 2, Score: 2.0},
			{Repo: "agent-pipeline", Title: "retry flaky deploy step", Value: 5, Effort: 2, Score: 2.5},
		},
		NeedsWeighting: []overview.RankedItem{{Repo: "mgrabber", Title: "investigate rate limits"}},
		Inbox: []overview.Capture{
			{Source: overview.CaptureRepoTodo, Repo: "bridge", Title: "add tests"},
			{Source: overview.CaptureIdeasLab, Title: "kanban for issues"},
		},
	}
}

func TestFlow_PickerToOverview(t *testing.T) {
	s := newSession(t, Config{
		BuildOverview: func(_ context.Context) (overview.Snapshot, error) {
			return fixedOverview(), nil
		},
	})
	// seed the picker, focus the list, open the Overview, resolve the build cmd
	s.send(reposMsg{rows: []repoRow{{label: "github/public/bridge"}}})
	s.m.pickerFocus = focusList
	s.key("o")
	if s.m.screen != screenOverview {
		t.Fatalf("screen = %d, want screenOverview after 'o'", s.m.screen)
	}
	s.resolve() // run buildOverviewCmd -> overviewMsg -> populate
	if s.m.overviewState != loadOK {
		t.Fatalf("overviewState = %d, want loadOK after resolve", s.m.overviewState)
	}
	assertGolden(t, "picker_to_overview", s.frame())
}

func TestFlow_OverviewNavAndBack(t *testing.T) {
	s := newSession(t, Config{
		BuildOverview: func(_ context.Context) (overview.Snapshot, error) {
			return fixedOverview(), nil
		},
	})
	s.send(reposMsg{rows: []repoRow{{label: "github/public/bridge"}}})
	s.m.pickerFocus = focusList
	s.key("o")
	s.resolve()
	s.key("tab") // ranked -> inbox pane
	if s.m.ovFocus != ovInboxPane {
		t.Errorf("ovFocus = %d, want ovInboxPane after tab", s.m.ovFocus)
	}
	s.key("down") // move within inbox (no panic on bounds)
	s.key("esc")  // back to picker
	if s.m.screen != screenPicker {
		t.Errorf("screen = %d, want screenPicker after esc", s.m.screen)
	}
}

func TestFlow_PickerToDash(t *testing.T) {
	s := newSession(t, Config{})
	// a local repo row (no remote) so enter goes to the dashboard
	s.send(reposMsg{rows: []repoRow{{
		label: "github/public/dashonly",
		repo:  coreRepo("dashonly", t.TempDir()),
	}}})
	s.m.pickerFocus = focusList
	s.key("enter")
	if s.m.screen != screenDash {
		t.Fatalf("screen = %d, want screenDash after enter", s.m.screen)
	}
	// m.repo is set synchronously by openRepoRow, so the dash header renders
	// before any git Cmd is resolved — assert on that, don't resolve git cmds.
	if !strings.Contains(s.frame(), "dashonly") {
		t.Errorf("dash frame missing repo name:\n%s", s.frame())
	}
}

func TestFlow_FilterTyping(t *testing.T) {
	s := newSession(t, Config{})
	s.send(reposMsg{rows: []repoRow{
		{label: "github/public/bridge"},
		{label: "github/public/agent-os"},
	}})
	// The picker starts with the filter focused (initialModel sets
	// pickerFocus = focusFilter), so type directly — no "/" needed (pressing
	// "/" here would insert a literal slash into the filter).
	for _, r := range "agent" {
		s.key(string(r))
	}
	f := s.frame()
	if !strings.Contains(f, "agent-os") || strings.Contains(f, "public/bridge") {
		t.Errorf("filter should show only agent-os, got:\n%s", f)
	}
}

func coreRepo(name, path string) core.Repo {
	return core.Repo{Name: name, Path: path, Forge: "github", Owner: "freaxnx01"}
}

func overviewWithRoadmap() overview.Snapshot {
	s := fixedOverview()
	s.Roadmap = []overview.RoadmapItem{
		{Repo: "bridge", Title: "todo one", Status: "Todo"},
		{Repo: "bridge", Title: "todo two", Status: "Todo"},
		{Repo: "agent-pipeline", Title: "wip one", Status: "In Progress"},
		{Repo: "bridge", Title: "done one", Status: "Done"},
	}
	return s
}

func TestFlow_OverviewWithRoadmap_Golden(t *testing.T) {
	s := newSession(t, Config{
		BuildOverview: func(_ context.Context) (overview.Snapshot, error) {
			return overviewWithRoadmap(), nil
		},
	})
	s.send(reposMsg{rows: []repoRow{{label: "github/public/bridge"}}})
	s.m.pickerFocus = focusList
	s.key("o")
	s.resolve()
	assertGolden(t, "overview_with_roadmap", s.frame())
}

func TestViewOverview_EmptyRoadmapOmitsTier(t *testing.T) {
	m := initialModel(Config{})
	m.screen = screenOverview
	m.width, m.height = 120, 40
	m.overviewState = loadOK
	m.overview = fixedOverview() // no Roadmap
	if strings.Contains(m.viewOverview(), "Roadmap") {
		t.Errorf("empty roadmap should omit the tier:\n%s", m.viewOverview())
	}
}

func TestViewOverview_EmptyStatusGroupLabeled(t *testing.T) {
	m := initialModel(Config{})
	m.screen = screenOverview
	m.width, m.height = 120, 40
	m.overviewState = loadOK
	m.overview = overview.Snapshot{
		Roadmap: []overview.RoadmapItem{{Repo: "bridge", Title: "no status item", Status: ""}},
	}
	out := m.viewOverview()
	if !strings.Contains(out, "No status") {
		t.Errorf("empty-status group should be labeled 'No status':\n%s", out)
	}
}

func TestViewRepoModal_NameAndForgeSteps(t *testing.T) {
	m := initialModel(Config{})
	m.width, m.height = 120, 40
	m.repoModal = &newRepoModal{name: "proj"}
	nameFrame := m.viewPicker()
	if !strings.Contains(nameFrame, "New repo") || !strings.Contains(nameFrame, "name: proj") {
		t.Errorf("name step frame wrong:\n%s", nameFrame)
	}
	m.repoModal.step = repoModalForge
	m.repoModal.sel = 2 // GitHub · Private
	forgeFrame := m.viewPicker()
	for _, want := range []string{"New repo · proj", "Forgejo · Private", "GitHub · Private", "GitHub · Public"} {
		if !strings.Contains(forgeFrame, want) {
			t.Errorf("forge step missing %q:\n%s", want, forgeFrame)
		}
	}
}

func TestFlow_CtrlN_RepoModal_Golden(t *testing.T) {
	s := newSession(t, Config{
		CreateRepo: func(name, forge string, private bool) (core.Repo, error) {
			return core.Repo{Name: name, Path: "/r/" + name, Forge: forge}, nil
		},
	})
	s.send(reposMsg{rows: []repoRow{{label: "github/public/bridge"}}})
	s.m.pickerFocus = focusList
	s.send(tea.KeyMsg{Type: tea.KeyCtrlN})
	for _, r := range "proj" {
		s.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	s.send(tea.KeyMsg{Type: tea.KeyEnter}) // -> forge step
	assertGolden(t, "ctrln_repo_modal_forge", s.frame())
}
