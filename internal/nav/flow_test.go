package nav

import (
	"context"
	"strings"
	"testing"

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
