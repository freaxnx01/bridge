package nav

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/freaxnx01/bridge/internal/overview"
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

// ovMove shifts the selection by delta within the focused pane, clamped to range.
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

// viewOverview renders the two-tier cross-repo overview: a ranked "what matters
// now" list (plus a needs-weighting group) and the raw-capture inbox.
func (m Model) viewOverview() string {
	w := m.width
	title := "bridge · " + envLabel(m.cfg.Environment) + " · Overview"
	if m.overviewState == loadPending {
		return panel(w, title, stMuted.Render("◐ building overview…"))
	}

	var rb strings.Builder
	rb.WriteString(stAccent.Render("What matters now") + "\n")
	if len(m.overview.Ranked) == 0 {
		rb.WriteString(stMuted.Render("  (nothing ranked yet)") + "\n")
	}
	for i, it := range m.overview.Ranked {
		line := fmt.Sprintf("%-4.1f %-14s %s  %s", it.Score, trunc(it.Repo, 14), it.Title, weightBadge(it))
		rb.WriteString(selectableLine(m.ovFocus == ovRankedPane && i == m.ovRankedSel, line) + "\n")
	}
	if len(m.overview.NeedsWeighting) > 0 {
		rb.WriteString(stMuted.Render(fmt.Sprintf("⚖ needs weighting (%d)", len(m.overview.NeedsWeighting))) + "\n")
		for _, it := range m.overview.NeedsWeighting {
			rb.WriteString(stMuted.Render(fmt.Sprintf("   -    %-14s %s", trunc(it.Repo, 14), it.Title)) + "\n")
		}
	}

	var ib strings.Builder
	ib.WriteString(stAccent.Render(fmt.Sprintf("Inbox (raw captures) · %d", len(m.overview.Inbox))) + "\n")
	for i, c := range m.overview.Inbox {
		line := fmt.Sprintf("• %-14s %s  %s", trunc(captureWhere(c), 14), c.Title, humanAge(c.Age))
		ib.WriteString(selectableLine(m.ovFocus == ovInboxPane && i == m.ovInboxSel, line) + "\n")
	}

	sections := []string{panel(w, title, strings.TrimRight(rb.String(), "\n"))}
	if len(m.overview.Roadmap) > 0 {
		sections = append(sections, m.viewRoadmap(w))
	}
	sections = append(sections, panel(w, "Inbox", strings.TrimRight(ib.String(), "\n")))
	sections = append(sections, m.hintLine("↑↓ move · tab pane · ⏎ show link/path · esc back · q quit"))
	return strings.Join(sections, "\n")
}

const roadmapGroupCap = 6 // max items listed per Status group before "↓ N more"

// viewRoadmap renders the board's items grouped by Status (board order). Done
// collapses to a count; other groups list up to roadmapGroupCap items.
func (m Model) viewRoadmap(w int) string {
	var b strings.Builder
	b.WriteString(stAccent.Render("Roadmap") + "\n")
	for _, status := range overview.RoadmapStatuses(m.overview.Roadmap) {
		group := overview.RoadmapByStatus(m.overview.Roadmap, status)
		if status == "Done" {
			b.WriteString(stMuted.Render(fmt.Sprintf("Done · %d", len(group))) + "\n")
			continue
		}
		b.WriteString(stText.Render(fmt.Sprintf("%s (%d)", status, len(group))) + "\n")
		for i, it := range group {
			if i >= roadmapGroupCap {
				b.WriteString(stMuted.Render(fmt.Sprintf("  ↓ %d more", len(group)-roadmapGroupCap)) + "\n")
				break
			}
			b.WriteString("  " + stText.Render(fmt.Sprintf("• %-14s %s", trunc(it.Repo, 14), it.Title)) + "\n")
		}
	}
	return panel(w, "Roadmap", strings.TrimRight(b.String(), "\n"))
}

func selectableLine(selected bool, text string) string {
	if selected {
		return stSel.Render(stAccent.Render("▸ ") + text)
	}
	return "  " + stText.Render(text)
}

func weightBadge(it overview.RankedItem) string {
	b := fmt.Sprintf("v%d/e%d", it.Value, effortOrDefault(it.Effort))
	if it.Stale {
		b += " ⚠"
	}
	return stMuted.Render(b)
}

func effortOrDefault(e int) int {
	if e <= 0 {
		return overview.DefaultEffort
	}
	return e
}

func envLabel(s string) string {
	if s == "" {
		return "bridge"
	}
	return s
}

func captureWhere(c overview.Capture) string {
	switch c.Source {
	case overview.CaptureIdeasLab:
		return "ideas-lab"
	case overview.CaptureRepoTodo:
		return c.Repo + " todo"
	default:
		return c.Repo + " idea"
	}
}

func humanAge(d time.Duration) string {
	days := int(d.Hours()) / 24
	if days <= 0 {
		return "today"
	}
	return fmt.Sprintf("%dd", days)
}
