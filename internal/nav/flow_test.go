package nav

import (
	"context"
	"testing"

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
