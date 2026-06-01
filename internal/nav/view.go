package nav

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

var (
	colAccent = lipgloss.Color("#7aa2f7")
	colMuted  = lipgloss.Color("#6b7280")
	colText   = lipgloss.Color("#c0caf5")
	colOk     = lipgloss.Color("#9ece6a")
	colWarn   = lipgloss.Color("#e0af68")
	colBad    = lipgloss.Color("#f7768e")
	colBorder = lipgloss.Color("#3b3b58")
	colSelBg  = lipgloss.Color("#2a2b3d")

	stTitle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#bb9af7")).Bold(true)
	stMuted  = lipgloss.NewStyle().Foreground(colMuted)
	stText   = lipgloss.NewStyle().Foreground(colText)
	stAccent = lipgloss.NewStyle().Foreground(colAccent).Bold(true)
	stSel    = lipgloss.NewStyle().Background(colSelBg).Foreground(colText)
	stOk     = lipgloss.NewStyle().Foreground(colOk)
	stWarn   = lipgloss.NewStyle().Foreground(colWarn)
	stBad    = lipgloss.NewStyle().Foreground(colBad)
)

func panel(w int, title, body string) string {
	head := stTitle.Render(" " + title + " ")
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colBorder).
		Padding(0, 1).
		Width(w - 2).
		Render(head + "\n\n" + body)
}

func (m Model) View() string {
	if m.width == 0 || m.height == 0 {
		return "initialising…"
	}
	if m.screen == screenPicker {
		return m.viewPicker()
	}
	return m.viewDash()
}

func (m Model) viewPicker() string {
	w := m.width
	var sections []string

	if len(m.sessions) > 0 {
		var b strings.Builder
		for i, s := range m.sessions {
			dot := stMuted.Render("○")
			if s.state == "attached" {
				dot = stOk.Render("●")
			}
			line := fmt.Sprintf("%s %-24s %-16s %-8s %s",
				dot, trunc(s.repoLabel, 24), trunc(s.worktree, 16), s.agent, s.lastAccessed)
			if m.pickerFocus == focusSessions && i == m.sessionSel {
				line = stSel.Render(stAccent.Render("▸ ") + line)
			} else {
				line = "  " + line
			}
			b.WriteString(line + "\n")
		}
		sections = append(sections, panel(w, "Active sessions  "+stMuted.Render("(↑ / tab · ⏎ attach)"), strings.TrimRight(b.String(), "\n")))
	}

	title := "Repos"
	switch m.remoteState {
	case loadPending:
		title = "Repos   " + m.spin.View() + " loading remote…"
	case loadErr:
		title = "Repos   " + stWarn.Render("remote unavailable (cached rows shown)")
	}
	var rb strings.Builder
	rb.WriteString(m.filter.View() + "\n\n")
	rows := m.visibleRepos()
	if len(rows) == 0 {
		rb.WriteString(stMuted.Render("no repos — remote rows appear once loaded"))
	} else {
		sel := m.pickerSel
		if sel >= len(rows) {
			sel = len(rows) - 1
		}
		if sel < 0 {
			sel = 0
		}
		used := 0
		if len(sections) > 0 { // Active sessions panel already added above
			used = lipgloss.Height(strings.Join(sections, "\n"))
		}
		// Budget: terminal height minus the sessions panel and this panel's
		// chrome (borders + title + filter + blanks) + hint + scroll markers.
		maxVisible := m.height - used - 9
		if maxVisible < 3 {
			maxVisible = 3
		}
		start, end := windowAround(len(rows), sel, maxVisible)
		if start > 0 {
			rb.WriteString(stMuted.Render(fmt.Sprintf("  ↑ %d more", start)) + "\n")
		}
		for i := start; i < end; i++ {
			if m.pickerFocus == focusList && i == sel {
				rb.WriteString(stSel.Render(stAccent.Render("▸ ")+rows[i].label) + "\n")
			} else {
				rb.WriteString("  " + stText.Render(rows[i].label) + "\n")
			}
		}
		if end < len(rows) {
			rb.WriteString(stMuted.Render(fmt.Sprintf("  ↓ %d more", len(rows)-end)) + "\n")
		}
	}
	sections = append(sections, panel(w, title, strings.TrimRight(rb.String(), "\n")))

	sections = append(sections, m.hintLine("↑↓ move · tab panes · ⏎ open/attach · / filter · r refresh · q quit"))
	return strings.Join(sections, "\n")
}

func (m Model) viewDash() string {
	w := m.width
	header := panel(w, "bridge nav · "+m.repo.Name, stMuted.Render(m.repo.Path))

	var b strings.Builder
	for i, r := range m.dashRows {
		dot := stMuted.Render("·")
		switch r.state {
		case "attached":
			dot = stOk.Render("●")
		case "detached":
			dot = stMuted.Render("○")
		}
		agent := r.agent
		if agent == "" {
			agent = "—"
		}
		la := r.lastAccessed
		if !r.hasSession {
			la = "(no session)"
		}
		line := fmt.Sprintf("%s %-18s %-14s %-8s %-12s %s",
			dot, trunc(r.worktree, 18), trunc(r.branch, 14), agent, la, m.dirtyView(r))
		if i == m.dashSel {
			line = stSel.Render(line)
		}
		b.WriteString(line + "\n")
	}
	b.WriteString("\n")
	createLine := "  + Create new worktree…"
	if m.dashSel == len(m.dashRows) {
		createLine = stSel.Render(stAccent.Render("▸ ") + "+ Create new worktree…")
	}
	b.WriteString(createLine)

	body := panel(w, "Sessions & Worktrees", strings.TrimRight(b.String(), "\n"))
	hint := stMuted.Render("↑↓ move · ⏎ attach/launch · n new worktree · esc back · q quit")
	footer := m.hintLine("(later: Branches · Recent commits · Git status · Open issues · forge statusbar)")

	out := header + "\n" + body + "\n" + hint + "\n" + footer
	if m.modal != nil {
		out += "\n" + m.viewModal()
	}
	return out
}

func (m Model) dirtyView(r dashRow) string {
	switch r.dirtyState {
	case loadPending:
		return m.spin.View()
	case loadErr:
		return stMuted.Render("?")
	}
	if r.dirty.clean {
		return stOk.Render("✓ clean")
	}
	s := stBad.Render(fmt.Sprintf("●%d", r.dirty.modified))
	if r.dirty.ahead > 0 {
		s += " " + stWarn.Render(fmt.Sprintf("↑%d", r.dirty.ahead))
	}
	return s
}

func (m Model) viewModal() string {
	body := "name: " + m.modal.name + "_\n\n" +
		stMuted.Render("→ creates worktree-"+m.modal.name+" then launches the default agent") + "\n" +
		stMuted.Render("⏎ create & launch    esc cancel")
	if m.modal.err != "" {
		body += "\n" + stBad.Render(m.modal.err)
	}
	return panel(m.width, "New worktree", body)
}

func trunc(s string, n int) string {
	if lipgloss.Width(s) <= n {
		return s
	}
	if n <= 1 {
		return "…"
	}
	return s[:n-1] + "…"
}

// hintLine renders the muted hint left-aligned with the version pinned to the
// bottom-right of the terminal width.
func (m Model) hintLine(left string) string {
	l := stMuted.Render(left)
	if m.cfg.Version == "" || m.width <= 0 {
		return l
	}
	r := stMuted.Render(m.cfg.Version)
	gap := m.width - lipgloss.Width(l) - lipgloss.Width(r)
	if gap < 1 {
		gap = 1
	}
	return l + strings.Repeat(" ", gap) + r
}
