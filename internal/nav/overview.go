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

// updateOverviewKeys handles key presses on the Overview screen. Read-mostly:
// navigate, switch panes, esc back, and enter to surface the target URL/path in
// the status line (no browser launch — usually over ssh/tmux).
func (m Model) updateOverviewKeys(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.screen = screenPicker
		return m, nil
	case "tab":
		if m.ovFocus == ovRankedPane {
			m.ovFocus = ovInboxPane
		} else {
			m.ovFocus = ovRankedPane
		}
		return m, nil
	case "up", "k":
		m.ovMove(-1)
	case "down", "j":
		m.ovMove(1)
	case "enter":
		m.status = m.ovSelectedTarget()
	}
	return m, nil
}

func (m *Model) ovMove(delta int) {
	if m.ovFocus == ovRankedPane {
		m.ovRankedSel = clampInt(m.ovRankedSel+delta, 0, len(m.overview.Ranked)-1)
	} else {
		m.ovInboxSel = clampInt(m.ovInboxSel+delta, 0, len(m.overview.Inbox)-1)
	}
}

// ovSelectedTarget returns the URL (ranked) or file path (inbox) of the current
// selection, or "" when the pane is empty.
func (m Model) ovSelectedTarget() string {
	if m.ovFocus == ovRankedPane {
		if m.ovRankedSel < len(m.overview.Ranked) {
			return m.overview.Ranked[m.ovRankedSel].URL
		}
		return ""
	}
	if m.ovInboxSel < len(m.overview.Inbox) {
		return m.overview.Inbox[m.ovInboxSel].Path
	}
	return ""
}
