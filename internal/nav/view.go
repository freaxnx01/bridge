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
	if m.screen == screenOverview {
		return m.viewOverview()
	}
	return m.viewDash()
}

func (m Model) viewPicker() string {
	if m.repoModal != nil {
		return m.viewRepoModal()
	}
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
			tag := repoIssueTag(rows[i])
			if m.pickerFocus == focusList && i == sel {
				rb.WriteString(stSel.Render(stAccent.Render("▸ ")+rows[i].label+tag) + "\n")
			} else {
				rb.WriteString("  " + stText.Render(rows[i].label) + tag + "\n")
			}
		}
		if end < len(rows) {
			rb.WriteString(stMuted.Render(fmt.Sprintf("  ↓ %d more", len(rows)-end)) + "\n")
		}
	}
	sections = append(sections, panel(w, title, strings.TrimRight(rb.String(), "\n")))

	sections = append(sections, m.hintLine("↑↓ move · g/G first/last · ⏎ open/attach · / filter · r refresh · tab panes · q quit"))
	return strings.Join(sections, "\n")
}

func (m Model) viewDash() string {
	w := m.width
	headerTitle := "bridge nav · " + m.repo.Name
	if n := m.repoIssueCount(); n > 0 {
		headerTitle += "  " + stWarn.Render(fmt.Sprintf("●%d open", n))
	}
	if len(m.notes) > 0 {
		headerTitle += "  " + stAccent.Render("✎ "+m.notesNames())
	}
	header := panel(w, headerTitle, stMuted.Render(m.repo.Path))

	var body string
	if w < dashTwoColMin {
		// Narrow layout has no detail column, so stack the three backlog panes
		// below the worktree list. They are always shown (with placeholders) so
		// the layout is stable and a missing TODO.md is visible.
		parts := []string{
			panel(w, "Sessions & Worktrees", m.dashListBody(false)),
			m.issuesPanel(w, 0),
			m.ideasPanel(w, 0),
			m.todosPanel(w, 0),
		}
		body = strings.Join(parts, "\n")
	} else {
		leftW := clampInt(w*5/12, 40, 64)
		rightW := w - leftW
		listBody := m.dashListBody(true)
		// Right column has two modes: the selected worktree's Details when the
		// worktree list is focused, otherwise the three stacked backlog panes.
		rightAt := func(h int) string {
			if m.dashFocus == dashFocusWorktrees {
				return m.detailColumn(rightW, h)
			}
			return m.backlogColumn(rightW, h)
		}
		// Stretch the shorter column so both close their bottom border on the
		// same line: render each at natural height, take the taller, re-render.
		h := max(lipgloss.Height(panel(leftW, "Sessions & Worktrees", listBody)), lipgloss.Height(rightAt(0)))
		left := panelH(leftW, h, "Sessions & Worktrees", listBody)
		body = lipgloss.JoinHorizontal(lipgloss.Top, left, rightAt(h))
	}

	hint := m.hintLine("↑↓ move · tab panes · ⏎ attach/launch · n new worktree · esc back · q quit")

	out := header + "\n" + body + "\n" + hint
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
		if i == m.dashSel && m.dashFocus == dashFocusWorktrees {
			line = stSel.Render(line)
		}
		b.WriteString(line + "\n")
	}
	b.WriteString("\n")
	createLine := "  + Create new worktree…"
	if m.dashSel == len(m.dashRows) && m.dashFocus == dashFocusWorktrees {
		createLine = stSel.Render(stAccent.Render("▸ ") + "+ Create new worktree…")
	}
	b.WriteString(createLine)
	return strings.TrimRight(b.String(), "\n")
}

// dirtyView renders the per-worktree git indicator. Precedence: loading/error
// first, then no-upstream, then the modified/ahead/behind tokens (zeros
// omitted), then "✓ clean" when nothing diverges.
func (m Model) dirtyView(r dashRow) string {
	switch r.dirtyState {
	case loadPending:
		return m.spin.View()
	case loadErr:
		return stMuted.Render("?")
	}
	var tokens []string
	if r.dirty.modified > 0 {
		tokens = append(tokens, stBad.Render(fmt.Sprintf("●%d", r.dirty.modified)))
	}
	if r.dirty.noUpstream {
		// No upstream means ahead/behind are undefined; show the marker instead.
		tokens = append(tokens, stMuted.Render("⤳ no upstream"))
	} else {
		if r.dirty.ahead > 0 {
			tokens = append(tokens, stWarn.Render(fmt.Sprintf("↑%d", r.dirty.ahead)))
		}
		if r.dirty.behind > 0 {
			tokens = append(tokens, stAccent.Render(fmt.Sprintf("↓%d", r.dirty.behind)))
		}
	}
	if len(tokens) == 0 {
		return stOk.Render("✓ clean")
	}
	return strings.Join(tokens, " ")
}

func (m Model) viewRepoModal() string {
	mo := m.repoModal
	if mo.step == repoModalName {
		body := "name: " + mo.name + "_\n\n" + stMuted.Render("⏎ next · esc cancel")
		if mo.err != "" {
			body += "\n" + stBad.Render(mo.err)
		}
		return panel(m.width, "New repo", body)
	}
	var b strings.Builder
	for i, ch := range repoForgeChoices {
		if i == mo.sel {
			b.WriteString(stSel.Render(stAccent.Render("▸ ")+ch.label) + "\n")
		} else {
			b.WriteString("  " + stText.Render(ch.label) + "\n")
		}
	}
	if mo.creating {
		b.WriteString("\n" + stMuted.Render("⏳ creating…"))
	} else {
		b.WriteString("\n" + stMuted.Render("↑↓ pick · ⏎ create · esc back"))
	}
	if mo.err != "" {
		b.WriteString("\n" + stBad.Render(mo.err))
	}
	return panel(m.width, "New repo · "+mo.name, strings.TrimRight(b.String(), "\n"))
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

// issuesPanel renders the open-issues pane (a single bordered panel) for the
// dashboard repo, windowed around the selection and stretched to at least minH.
// The selection highlight shows only when the issues pane holds focus.
func (m Model) issuesPanel(w, minH int) string {
	title := "Open issues"
	if m.dashFocus == dashFocusIssues {
		title += "  " + stMuted.Render("(⏎ start worktree)")
	}
	return stretchPanel(w, minH, title, m.issuesBody(w, minH))
}

func (m Model) issuesBody(w, minH int) string {
	switch m.issuesState {
	case loadPending:
		return m.spin.View() + " loading…"
	case loadErr:
		return stMuted.Render("unavailable")
	}
	if len(m.issues) == 0 {
		return stOk.Render("✓ no open issues")
	}
	// Reserve panel chrome (border + title + blank lines + overflow markers) when
	// budgeting visible rows; fall back to a small window at natural height.
	maxVisible := minH - 6
	if maxVisible < 3 {
		maxVisible = 3
	}
	start, end := windowAround(len(m.issues), m.issueSel, maxVisible)
	var b strings.Builder
	if start > 0 {
		b.WriteString(stMuted.Render(fmt.Sprintf("  ↑ %d more", start)) + "\n")
	}
	for i := start; i < end; i++ {
		ir := m.issues[i]
		text := trunc(fmt.Sprintf("#%d %s", ir.number, ir.title), w-6)
		if m.dashFocus == dashFocusIssues && i == m.issueSel {
			b.WriteString(stSel.Render(stAccent.Render("▸ ")+text) + "\n")
		} else {
			b.WriteString("  " + stText.Render(text) + "\n")
		}
	}
	if end < len(m.issues) {
		b.WriteString(stMuted.Render(fmt.Sprintf("  ↓ %d more", len(m.issues)-end)) + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// notesNames joins the present note file names for the header chip (e.g.
// "ideas.md · TODO.md").
func (m Model) notesNames() string {
	names := make([]string, 0, len(m.notes))
	for _, nf := range m.notes {
		names = append(names, nf.name)
	}
	return strings.Join(names, " · ")
}

// backlogColumn stacks the three backlog panes (Open Issues, Ideas, Todos) as
// the dashboard's right column when a backlog pane is focused. Each pane gets
// roughly a third of the height; the last absorbs the slack so the column
// bottom-aligns with the worktree list. minH <= 0 renders at natural height.
func (m Model) backlogColumn(w, minH int) string {
	per := (m.height - 14) / 3
	if per < 3 {
		per = 3
	}
	issues := m.issuesPanel(w, per)
	ideas := m.ideasPanel(w, per)
	todosH := minH - lipgloss.Height(issues) - lipgloss.Height(ideas)
	todos := m.todosPanel(w, todosH)
	return lipgloss.JoinVertical(lipgloss.Left, issues, ideas, todos)
}

// ideasPanel renders the repo-root ideas.md pane (a single bordered panel),
// scrolled by ideasScroll and stretched to at least minH.
func (m Model) ideasPanel(w, minH int) string {
	return m.noteFilePanel(w, minH, dashFocusIdeas, "Ideas", ideasFileName, m.ideaNote(), m.ideasScroll)
}

// todosPanel renders the repo-root TODO.md pane (a single bordered panel),
// scrolled by todosScroll and stretched to at least minH.
func (m Model) todosPanel(w, minH int) string {
	return m.noteFilePanel(w, minH, dashFocusTodos, "Todos", todoFileName, m.todoNote(), m.todosScroll)
}

// noteFilePanel renders one backlog note file as a bordered, scrollable panel.
// The title carries the on-disk file name when present, a (truncated) marker for
// a file clipped at notesMaxBytes, and a scroll hint when focused. wantName names
// the file for the "no <name>" placeholder when absent.
func (m Model) noteFilePanel(w, minH int, focus dashFocus, label, wantName string, nf *noteFile, scroll int) string {
	title := label
	if nf != nil {
		title += "  " + stMuted.Render("· "+nf.name)
		if nf.truncated {
			title += "  " + stMuted.Render("(truncated)")
		}
	}
	if m.dashFocus == focus {
		title += "  " + stMuted.Render("(↑↓ scroll)")
	}
	return stretchPanel(w, minH, title, m.noteFileBody(w, minH, wantName, nf, scroll))
}

// noteFileBody renders a single note file's windowed text. It shares the
// loading/error states with the other backlog notes (one notesState load), then
// shows a placeholder when the file is absent, the binary marker, or the
// scrolled text window with overflow markers.
func (m Model) noteFileBody(w, minH int, wantName string, nf *noteFile, scroll int) string {
	if text, ok := m.panelState(m.notesState); !ok {
		return text
	}
	if nf == nil {
		return stMuted.Render("no " + wantName)
	}
	if nf.binary {
		return stMuted.Render("(binary or non-text content)")
	}
	if len(nf.lines) == 0 {
		return stMuted.Render("(empty)")
	}
	// Reserve panel chrome (border + title + blank lines + overflow markers) when
	// budgeting visible rows; fall back to a small window at natural height.
	maxVisible := minH - 6
	if maxVisible < 3 {
		maxVisible = 3
	}
	start := clampInt(scroll, 0, max(0, len(nf.lines)-maxVisible))
	end := min(len(nf.lines), start+maxVisible)
	var b strings.Builder
	if start > 0 {
		b.WriteString(stMuted.Render(fmt.Sprintf("  ↑ %d more", start)) + "\n")
	}
	for i := start; i < end; i++ {
		b.WriteString(stText.Render(trunc(nf.lines[i], w-4)) + "\n")
	}
	if end < len(nf.lines) {
		b.WriteString(stMuted.Render(fmt.Sprintf("  ↓ %d more", len(nf.lines)-end)) + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
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

// repoIssueCount returns the loaded open-issue count for the current dashboard repo.
func (m Model) repoIssueCount() int {
	key := m.repo.Forge + "/" + m.repo.Owner + "/" + m.repo.Name
	for _, r := range m.localRepos {
		if k, _, _, _, ok := rowForgeKey(r); ok && k == key {
			return r.issueCount
		}
	}
	return 0
}

// repoIssueTag returns a short styled issue-count suffix for a picker row, or ""
// when the count is zero or not yet loaded.
func repoIssueTag(r repoRow) string {
	if r.issueState != loadOK || r.issueCount <= 0 {
		return ""
	}
	return "  " + stWarn.Render(fmt.Sprintf("●%d", r.issueCount))
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
