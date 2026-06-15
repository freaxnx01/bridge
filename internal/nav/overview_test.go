package nav

import (
	"context"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/freaxnx01/bridge/internal/overview"
)

func TestUpdatePicker_O_EntersOverview(t *testing.T) {
	m := initialModel(Config{
		BuildOverview: func(_ context.Context) (overview.Snapshot, error) {
			return overview.Snapshot{Ranked: []overview.RankedItem{{Title: "x", Score: 1}}}, nil
		},
	})
	m.pickerFocus = focusList
	out, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("o")})
	got := out.(Model)
	if got.screen != screenOverview {
		t.Fatalf("screen = %d, want screenOverview", got.screen)
	}
	if got.overviewState != loadPending {
		t.Errorf("overviewState = %d, want loadPending", got.overviewState)
	}
	if cmd == nil {
		t.Fatal("entering overview should return a build Cmd")
	}
	if _, ok := cmd().(overviewMsg); !ok {
		t.Fatalf("cmd msg = %T, want overviewMsg", cmd())
	}
}

func TestUpdate_OverviewMsg_PopulatesAndOK(t *testing.T) {
	m := initialModel(Config{})
	m.screen = screenOverview
	snap := overview.Snapshot{Ranked: []overview.RankedItem{{Title: "a"}, {Title: "b"}}}
	out, _ := m.Update(overviewMsg{snap: snap})
	got := out.(Model)
	if got.overviewState != loadOK || len(got.overview.Ranked) != 2 {
		t.Errorf("overview not applied: state=%d ranked=%d", got.overviewState, len(got.overview.Ranked))
	}
}

func TestUpdateOverview_EscReturnsToPicker(t *testing.T) {
	m := initialModel(Config{})
	m.screen = screenOverview
	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if out.(Model).screen != screenPicker {
		t.Errorf("esc should return to picker")
	}
}

func TestUpdateOverview_TabSwitchesPane(t *testing.T) {
	m := initialModel(Config{})
	m.screen = screenOverview
	m.overview = overview.Snapshot{
		Ranked: []overview.RankedItem{{Title: "a"}},
		Inbox:  []overview.Capture{{Title: "c"}},
	}
	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	if out.(Model).ovFocus != ovInboxPane {
		t.Errorf("tab should move focus to inbox pane")
	}
}

func TestUpdateOverview_EnterShowsURLInStatus(t *testing.T) {
	m := initialModel(Config{})
	m.screen = screenOverview
	m.ovFocus = ovRankedPane
	m.overview = overview.Snapshot{Ranked: []overview.RankedItem{{Title: "a", URL: "https://x/1"}}}
	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if got := out.(Model).status; got != "https://x/1" {
		t.Errorf("status = %q, want the item URL", got)
	}
}

func TestViewOverview_RendersTiersAndScores(t *testing.T) {
	m := initialModel(Config{})
	m.screen = screenOverview
	m.width, m.height = 100, 40
	m.overviewState = loadOK
	m.overview = overview.Snapshot{
		Ranked: []overview.RankedItem{
			{Repo: "bridge", Title: "wire api", Value: 4, Effort: 2, Score: 2.0},
		},
		NeedsWeighting: []overview.RankedItem{{Repo: "x", Title: "triage me"}},
		Inbox:          []overview.Capture{{Source: overview.CaptureRepoTodo, Repo: "bridge", Title: "a todo"}},
	}
	out := m.viewOverview()
	for _, want := range []string{"wire api", "2.0", "triage me", "a todo", "Inbox"} {
		if !strings.Contains(out, want) {
			t.Errorf("viewOverview missing %q\n---\n%s", want, out)
		}
	}
}
