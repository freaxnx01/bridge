package nav

import (
	"context"
	"errors"

	tea "github.com/charmbracelet/bubbletea"
)

// ovPane identifies the focused pane on the Overview screen.
type ovPane int

const (
	ovRankedPane ovPane = iota
	ovInboxPane
)

var errNoOverview = errors.New("overview not configured")

// buildOverviewCmd runs the injected BuildOverview aggregator off the Update
// loop. Nil callback yields an error message so the screen shows a notice
// rather than hanging on a spinner.
func (m Model) buildOverviewCmd() tea.Cmd {
	build := m.cfg.BuildOverview
	return func() tea.Msg {
		if build == nil {
			return overviewErrMsg{err: errNoOverview}
		}
		snap, err := build(context.Background())
		if err != nil {
			return overviewErrMsg{err: err}
		}
		return overviewMsg{snap: snap}
	}
}
