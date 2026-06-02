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

// dashTwoColMin is the minimum terminal width for the master-detail dashboard;
// below it the dashboard renders list-only (today's layout), unchanged.
const dashTwoColMin = 90

func panel(w int, title, body string) string {
	head := stTitle.Render(" " + title + " ")
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colBorder).
		Padding(0, 1).
		Width(w - 2).
		Render(head + "\n\n" + body)
}

// panelH is panel with an explicit total height (borders included) so a column
// can be stretched to match the other column for a clean two-column frame.
func panelH(w, h int, title, body string) string {
	head := stTitle.Render(" " + title + " ")
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colBorder).
		Padding(0, 1).
		Width(w - 2).
		Height(h - 2).
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

	sections = append(sections, m.hintLine("↑↓ move · g/G first/last · ⏎ open/attach · / filter · tab panes · q quit"))
	return strings.Join(sections, "\n")
}

func (m Model) viewDash() string {
	w := m.width
	header := panel(w, "bridge nav · "+m.repo.Name, stMuted.Render(m.repo.Path))

	var body string
	if w < dashTwoColMin {
		body = panel(w, "Sessions & Worktrees", m.dashListBody(false))
	} else {
		leftW := clampInt(w*5/12, 40, 64)
		rightW := w - leftW
		listBody := m.dashListBody(true)
		// Stretch the shorter column so both close their bottom border on the
		// same line: render each at natural height, take the taller, re-render.
		h := max(lipgloss.Height(panel(leftW, "Sessions & Worktrees", listBody)), lipgloss.Height(m.detailColumn(rightW, 0)))
		left := panelH(leftW, h, "Sessions & Worktrees", listBody)
		right := m.detailColumn(rightW, h)
		body = lipgloss.JoinHorizontal(lipgloss.Top, left, right)
	}

	hint := m.hintLine("↑↓ move · g/G first/last · ⏎ attach/launch · n new worktree · esc back · q quit")
	footer := stMuted.Render("(later: Open issues · forge statusbar)")

	out := header + "\n" + body + "\n" + hint + "\n" + footer
	if m.modal != nil {
		out += "\n" + m.viewModal()
	}
	return out
}

// dashListBody renders the worktree rows + the "+ create" row. compact drops the
// branch/agent/last-accessed columns so the list fits the narrower left column
// of the two-column layout; the full form is today's single-column layout.
func (m Model) dashListBody(compact bool) string {
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
		var line string
		if compact {
			line = fmt.Sprintf("%s %-18s %-7s %s", dot, trunc(r.worktree, 18), trunc(agent, 7), m.dirtyView(r))
		} else {
			la := r.lastAccessed
			if !r.hasSession {
				la = "(no session)"
			}
			line = fmt.Sprintf("%s %-18s %-14s %-8s %-12s %s",
				dot, trunc(r.worktree, 18), trunc(r.branch, 14), agent, la, m.dirtyView(r))
		}
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
	return strings.TrimRight(b.String(), "\n")
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

// detailColumn renders the three stacked detail panels for the highlighted
// worktree, or a hint when the "+ create" row is selected. minH stretches the
// column to at least that total height (the last panel absorbs the slack) so it
// bottom-aligns with the worktree list; minH <= 0 renders at natural height.
func (m Model) detailColumn(w, minH int) string {
	path := m.selectedWorktreePath()
	if path == "" {
		hint := stMuted.Render("select a worktree to see its branches, commits & status")
		return stretchPanel(w, minH, "Details", hint)
	}
	per := (m.height - 14) / 3
	if per < 3 {
		per = 3
	}
	d := m.details[path]
	branches := panel(w, "Branches", m.branchesBody(d, per))
	commits := panel(w, "Recent commits", m.commitsBody(d, per))
	statusBody := m.statusBody(d, per)
	statusH := minH - lipgloss.Height(branches) - lipgloss.Height(commits)
	status := stretchPanel(w, statusH, "Git status", statusBody)
	return lipgloss.JoinVertical(lipgloss.Left, branches, commits, status)
}

// stretchPanel renders a panel at least minH tall, falling back to its natural
// height when minH is smaller (or non-positive).
func stretchPanel(w, minH int, title, body string) string {
	nat := panel(w, title, body)
	if minH <= lipgloss.Height(nat) {
		return nat
	}
	return panelH(w, minH, title, body)
}

// panelState renders the spinner/unavailable text shared by the three panels
// while their data is loading or after an error; ok == true means render rows.
func (m Model) panelState(state loadState) (text string, ok bool) {
	switch state {
	case loadPending:
		return m.spin.View() + " loading…", false
	case loadErr:
		return stMuted.Render("unavailable"), false
	default:
		return "", true
	}
}

func (m Model) branchesBody(d *worktreeDetails, max int) string {
	if d == nil {
		return m.spin.View() + " loading…"
	}
	if text, ok := m.panelState(d.branchesState); !ok {
		return text
	}
	if len(d.branches) == 0 {
		return stMuted.Render("(only this worktree)")
	}
	var b strings.Builder
	shown, more := windowList(len(d.branches), max)
	for i := 0; i < shown; i++ {
		br := d.branches[i]
		switch {
		case br.current:
			b.WriteString(stAccent.Render("* " + br.name))
		case br.inWorktree:
			b.WriteString(stOk.Render("+ " + br.name))
		default:
			b.WriteString(stText.Render("  " + br.name))
		}
		b.WriteString("\n")
	}
	if more > 0 {
		b.WriteString(stMuted.Render(fmt.Sprintf("  … +%d more", more)))
	}
	return strings.TrimRight(b.String(), "\n")
}

func (m Model) commitsBody(d *worktreeDetails, max int) string {
	if d == nil {
		return m.spin.View() + " loading…"
	}
	if text, ok := m.panelState(d.commitsState); !ok {
		return text
	}
	if len(d.commits) == 0 {
		return stMuted.Render("(no commits)")
	}
	var b strings.Builder
	shown, more := windowList(len(d.commits), max)
	for i := 0; i < shown; i++ {
		c := d.commits[i]
		b.WriteString(stWarn.Render(c.sha) + " " + stText.Render(c.subject) + "\n")
	}
	if more > 0 {
		b.WriteString(stMuted.Render(fmt.Sprintf("… +%d more", more)))
	}
	return strings.TrimRight(b.String(), "\n")
}

func (m Model) statusBody(d *worktreeDetails, max int) string {
	if d == nil {
		return m.spin.View() + " loading…"
	}
	if text, ok := m.panelState(d.statusState); !ok {
		return text
	}
	if len(d.status) == 0 {
		return stOk.Render("✓ clean")
	}
	var b strings.Builder
	shown, more := windowList(len(d.status), max)
	for i := 0; i < shown; i++ {
		f := d.status[i]
		b.WriteString(stBad.Render(f.code) + " " + stText.Render(f.path) + "\n")
	}
	if more > 0 {
		b.WriteString(stMuted.Render(fmt.Sprintf("… +%d more", more)))
	}
	return strings.TrimRight(b.String(), "\n")
}

// windowList returns how many of n items to show given max rows, reserving one
// row for a "… +N more" line when there is overflow. more is the hidden count.
func windowList(n, max int) (shown, more int) {
	if max < 1 {
		max = 1
	}
	if n <= max {
		return n, 0
	}
	shown = max - 1
	if shown < 1 {
		shown = 1
	}
	return shown, n - shown
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
