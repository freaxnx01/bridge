// Package tui is the bridge dashboard TUI (Bubbletea). All three panels
// (Repos, Open Issues, Sessions) are wired to real data:
//   - Repos: core.DiscoverRepos
//   - Issues: the cache populated by `bridge issues`
//   - Sessions: live tmux output cross-referenced with the slot registry
//
// Actions on Enter / slash commands are still stubs; #72 tracks wiring.
package tui

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/freaxnx01/bridge/internal/core"
	"github.com/freaxnx01/bridge/internal/forge"
)

// --- data shapes ---

type repo struct {
	name   string
	issues int
	vis    string // pri / pub
}

type issue struct {
	num   int
	repo  string
	title string
}

type session struct {
	name     string
	state    string // attached / detached / code
	age      string
	repo     string
}

// loadRepos turns DiscoverRepos output into the TUI's display shape.
// "pri"/"pub" comes from the path-pattern Visibility tag; non-github
// forges (gitlab, forgejo) default to "pub" since visibility isn't
// encoded in their path. Issue counts will be 0 until #64's follow-up
// wires real forge calls.
func loadRepos(root string) []repo {
	raw, err := core.DiscoverRepos(root)
	if err != nil {
		return nil
	}
	out := make([]repo, 0, len(raw))
	for _, r := range raw {
		vis := "pub"
		if r.Visibility == "private" {
			vis = "pri"
		}
		display := r.Name
		if r.Owner != "" {
			display = r.Owner + "/" + r.Name
		}
		out = append(out, repo{name: display, issues: 0, vis: vis})
	}
	return out
}

// loadIssues reads the on-disk issue cache populated by `bridge issues`.
// Cache-only — the TUI must not block on a forge call at startup; the user
// runs `bridge issues --refresh` to warm the cache out of band. Missing
// or empty cache returns an empty slice (panel renders with no rows).
func loadIssues(cachePath string) []issue {
	c, err := forge.ReadIssueCache(cachePath)
	if err != nil {
		return nil
	}
	out := make([]issue, 0, len(c.Issues))
	for _, i := range c.Issues {
		out = append(out, issue{num: i.Number, repo: i.Repo, title: i.Title})
	}
	return out
}

// loadSessions returns the live tmux sessions cross-referenced against
// the slot registry. Slots are keyed by ID; live sessions are matched by
// SlotID (which == tmux session name in the current registry shape).
// Stale slots (registered but their tmux session is gone) are dropped.
// Live tmux sessions without a matching slot are still shown — the
// dashboard should reflect ground truth, not just what bridge launched.
func loadSessions(slotsPath string) []session {
	live, _ := core.LiveSessions()
	if len(live) == 0 {
		return nil
	}
	slots, _ := core.LoadSlots(slotsPath)
	bySlotID := make(map[string]core.Slot, len(slots))
	for _, s := range slots {
		bySlotID[s.ID] = s
	}
	out := make([]session, 0, len(live))
	for _, s := range live {
		repo := ""
		if slot, ok := bySlotID[s.SlotID]; ok {
			repo = slot.Repo
			if slot.Worktree != "" {
				repo = repo + ":" + slot.Worktree
			}
		}
		out = append(out, session{
			name:  s.TmuxName,
			state: s.State,
			age:   humanAge(s.Age),
			repo:  repo,
		})
	}
	return out
}

func humanAge(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

// --- styles (btop-inspired) ---

var (
	colBorder    = lipgloss.Color("#3b3b58")
	colBorderHot = lipgloss.Color("#7aa2f7")
	colAccent    = lipgloss.Color("#7aa2f7")
	colMuted     = lipgloss.Color("#6b7280")
	colOk        = lipgloss.Color("#9ece6a")
	colWarn      = lipgloss.Color("#e0af68")
	colBad       = lipgloss.Color("#f7768e")
	colPub       = lipgloss.Color("#9ece6a")
	colPri       = lipgloss.Color("#e0af68")
	colSelBg     = lipgloss.Color("#2a2b3d")
	colTitle     = lipgloss.Color("#bb9af7")
	colText      = lipgloss.Color("#c0caf5")
)

func panelStyle(focused bool) lipgloss.Style {
	border := colBorder
	if focused {
		border = colBorderHot
	}
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(border).
		Padding(0, 1)
}

func titleStyle(focused bool) lipgloss.Style {
	c := colTitle
	if !focused {
		c = colMuted
	}
	return lipgloss.NewStyle().Foreground(c).Bold(true)
}

var (
	mutedStyle  = lipgloss.NewStyle().Foreground(colMuted)
	textStyle   = lipgloss.NewStyle().Foreground(colText)
	accentStyle = lipgloss.NewStyle().Foreground(colAccent).Bold(true)
	selStyle    = lipgloss.NewStyle().Background(colSelBg).Foreground(colText)
	pubStyle    = lipgloss.NewStyle().Foreground(colPub)
	priStyle    = lipgloss.NewStyle().Foreground(colPri)
	okStyle     = lipgloss.NewStyle().Foreground(colOk)
	warnStyle   = lipgloss.NewStyle().Foreground(colWarn)
	badStyle    = lipgloss.NewStyle().Foreground(colBad)
)

// --- model ---

type pane int

const (
	paneRepos pane = iota
	paneIssues
	paneSessions
	paneCount
)

func (p pane) String() string {
	switch p {
	case paneRepos:
		return "Repos"
	case paneIssues:
		return "Open Issues"
	case paneSessions:
		return "Sessions"
	}
	return "?"
}

type model struct {
	width, height int

	focus    pane
	sel      [paneCount]int
	cmdMode  bool
	cmd      textinput.Model
	status   string
	showHelp bool

	repos    []repo
	issues   []issue
	sessions []session
}

func initialModel(repos []repo, issues []issue, sessions []session) model {
	ti := textinput.New()
	ti.Placeholder = "type a /command or text…"
	ti.Prompt = ""
	ti.CharLimit = 200
	ti.Width = 60
	status := "ready"
	if len(repos) == 0 {
		status = "no local repos found under BRIDGE_REPOS_ROOT"
	} else if len(issues) == 0 {
		status = "no cached issues — run `bridge issues --refresh` to warm the cache"
	}
	return model{
		focus:    paneIssues,
		cmd:      ti,
		status:   status,
		repos:    repos,
		issues:   issues,
		sessions: sessions,
	}
}

func (m model) Init() tea.Cmd { return textinput.Blink }

func (m *model) rowCount(p pane) int {
	switch p {
	case paneRepos:
		return len(m.repos)
	case paneIssues:
		return len(m.issues)
	case paneSessions:
		return len(m.sessions)
	}
	return 0
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case tea.KeyMsg:
		// command mode keys
		if m.cmdMode {
			switch msg.String() {
			case "esc":
				m.cmdMode = false
				m.cmd.Blur()
				m.cmd.SetValue("")
				return m, nil
			case "enter":
				v := strings.TrimSpace(m.cmd.Value())
				m.cmdMode = false
				m.cmd.Blur()
				m.cmd.SetValue("")
				return m.runCommand(v)
			}
			var cmd tea.Cmd
			m.cmd, cmd = m.cmd.Update(msg)
			return m, cmd
		}

		// normal mode keys
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "/":
			m.cmdMode = true
			m.cmd.Focus()
			return m, textinput.Blink
		case "?":
			m.showHelp = !m.showHelp
			return m, nil
		case "tab":
			m.focus = (m.focus + 1) % paneCount
			return m, nil
		case "shift+tab":
			m.focus = (m.focus + paneCount - 1) % paneCount
			return m, nil
		case "down", "j":
			n := m.rowCount(m.focus)
			if n > 0 {
				m.sel[m.focus] = (m.sel[m.focus] + 1) % n
			}
			return m, nil
		case "up", "k":
			n := m.rowCount(m.focus)
			if n > 0 {
				m.sel[m.focus] = (m.sel[m.focus] + n - 1) % n
			}
			return m, nil
		case "enter":
			return m, m.actOnSelection()
		}
	}
	return m, nil
}

func (m model) runCommand(v string) (tea.Model, tea.Cmd) {
	if v == "" {
		return m, nil
	}
	if !strings.HasPrefix(v, "/") {
		m.status = fmt.Sprintf("search: %q (not wired yet)", v)
		return m, nil
	}
	parts := strings.Fields(v)
	switch parts[0] {
	case "/q", "/quit", "/exit":
		return m, tea.Quit
	case "/attach":
		m.status = "would attach to selected session (mock)"
	case "/open":
		m.status = "would open selected repo in browser (mock)"
	case "/issue":
		m.status = "would open selected issue in browser (mock)"
	case "/help":
		m.showHelp = true
	default:
		m.status = fmt.Sprintf("unknown command: %s", parts[0])
	}
	return m, nil
}

func (m model) actOnSelection() tea.Cmd {
	switch m.focus {
	case paneRepos:
		m.status = "→ would drill into " + m.repos[m.sel[paneRepos]].name
	case paneIssues:
		i := m.issues[m.sel[paneIssues]]
		m.status = fmt.Sprintf("→ would open #%d in %s", i.num, i.repo)
	case paneSessions:
		s := m.sessions[m.sel[paneSessions]]
		m.status = "→ would attach to " + s.name
	}
	return nil
}

// --- views ---

func (m model) viewRepos(w, h int) string {
	focused := m.focus == paneRepos
	var b strings.Builder
	b.WriteString(titleStyle(focused).Render(" Repos ") + "\n\n")
	for i, r := range m.repos {
		visRaw := r.vis
		visStyled := pubStyle.Render(visRaw)
		if r.vis == "pri" {
			visStyled = priStyle.Render(visRaw)
		}
		name := truncate(r.name, 22)
		namePad := strings.Repeat(" ", max(0, 22-lipgloss.Width(name)))
		countStr := fmt.Sprintf("%2d", r.issues)
		countStyled := mutedStyle.Render(countStr)
		if r.issues > 0 {
			countStyled = accentStyle.Render(countStr)
		}
		marker := "  "
		if focused && i == m.sel[paneRepos] {
			marker = accentStyle.Render("▸ ")
		}
		line := marker + visStyled + "  " + textStyle.Render(name) + namePad + "  " + countStyled
		if focused && i == m.sel[paneRepos] {
			line = selStyle.Render(line)
		}
		b.WriteString(line + "\n")
	}
	return panelStyle(focused).Width(w).Height(h).Render(b.String())
}

func (m model) viewIssues(w, h int) string {
	focused := m.focus == paneIssues
	var b strings.Builder
	b.WriteString(titleStyle(focused).Render(" Open Issues ") + "\n\n")
	innerW := w - 4
	repoW := 22
	titleW := innerW - 2 - 5 - 1 - repoW - 2
	if titleW < 10 {
		titleW = 10
	}
	for i, is := range m.issues {
		numRaw := fmt.Sprintf("#%-4d", is.num)
		num := accentStyle.Render(numRaw)
		repoRaw := truncate(is.repo, repoW)
		repoPad := strings.Repeat(" ", max(0, repoW-lipgloss.Width(repoRaw)))
		repo := mutedStyle.Render(repoRaw) + repoPad
		title := textStyle.Render(truncate(is.title, titleW))
		marker := "  "
		if focused && i == m.sel[paneIssues] {
			marker = accentStyle.Render("▸ ")
		}
		line := marker + num + " " + repo + "  " + title
		if focused && i == m.sel[paneIssues] {
			line = selStyle.Render(line)
		}
		b.WriteString(line + "\n")
	}
	return panelStyle(focused).Width(w).Height(h).Render(b.String())
}

func (m model) viewSessions(w, h int) string {
	focused := m.focus == paneSessions
	var b strings.Builder
	b.WriteString(titleStyle(focused).Render(" Sessions ") + "\n\n")
	for i, s := range m.sessions {
		var dot string
		switch s.state {
		case "attached":
			dot = okStyle.Render("●")
		case "code":
			dot = warnStyle.Render("◆")
		default:
			dot = mutedStyle.Render("○")
		}
		nameRaw := truncate(s.name, 22)
		namePad := strings.Repeat(" ", max(0, 22-lipgloss.Width(nameRaw)))
		stateRaw := fmt.Sprintf("%-9s", s.state)
		ageRaw := fmt.Sprintf("%-10s", s.age)
		repoRaw := truncate(s.repo, 26)
		marker := "  "
		if focused && i == m.sel[paneSessions] {
			marker = accentStyle.Render("▸ ")
		}
		line := marker + dot + " " + textStyle.Render(nameRaw) + namePad + " " +
			mutedStyle.Render(stateRaw) + " " + mutedStyle.Render(ageRaw) + " " +
			mutedStyle.Render(repoRaw)
		if focused && i == m.sel[paneSessions] {
			line = selStyle.Render(line)
		}
		b.WriteString(line + "\n")
	}
	return panelStyle(focused).Width(w).Height(h).Render(b.String())
}

func (m model) viewHeader(w int) string {
	left := accentStyle.Render(" bridge ") + mutedStyle.Render("· dashboard PoC")
	right := mutedStyle.Render(time.Now().Format("2006-01-02 15:04"))
	gap := w - lipgloss.Width(left) - lipgloss.Width(right) - 4
	if gap < 1 {
		gap = 1
	}
	row := left + strings.Repeat(" ", gap) + right
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(colBorder).
		Padding(0, 1).
		Width(w).
		Render(row)
}

func (m model) viewCommand(w int) string {
	prompt := accentStyle.Render(" ❯ ")
	body := ""
	if m.cmdMode {
		body = m.cmd.View()
	} else {
		body = mutedStyle.Render("press / to type a command, ? for help, q to quit")
	}
	row := prompt + body
	border := colBorder
	if m.cmdMode {
		border = colBorderHot
	}
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(border).
		Padding(0, 1).
		Width(w).
		Render(row)
}

func (m model) viewHint(w int) string {
	cmds := mutedStyle.Render("/attach  /open  /issue  /help  /quit")
	keys := mutedStyle.Render("tab pane · ↑↓ row · ⏎ act · / cmd · ? help · q quit")
	status := okStyle.Render("● " + m.status)
	if strings.HasPrefix(m.status, "unknown") {
		status = badStyle.Render("● " + m.status)
	}
	line1 := cmds + "   " + keys
	return lipgloss.NewStyle().Width(w).Render(line1) + "\n" +
		lipgloss.NewStyle().Width(w).Render(status)
}

func (m model) View() string {
	if m.width == 0 || m.height == 0 {
		return "initialising…"
	}
	w := m.width

	header := m.viewHeader(w)

	// repos | issues row — repos 38 wide, issues fills rest
	reposW := 38
	if reposW > w/2 {
		reposW = w / 2
	}
	issuesW := w - reposW
	rowH := 11
	repos := m.viewRepos(reposW, rowH)
	issues := m.viewIssues(issuesW, rowH)
	topRow := lipgloss.JoinHorizontal(lipgloss.Top, repos, issues)

	sessions := m.viewSessions(w, 7)

	cmd := m.viewCommand(w)
	hint := m.viewHint(w)

	body := lipgloss.JoinVertical(lipgloss.Left, header, topRow, sessions, cmd, hint)

	if m.showHelp {
		body += "\n" + mutedStyle.Render(helpText())
	}
	return body
}

func helpText() string {
	return strings.Join([]string{
		"",
		"  HELP",
		"  ────",
		"  tab / shift+tab   switch panel",
		"  ↑ ↓  or  k j      move selection within panel",
		"  enter             default action for selected row",
		"  /                 focus command bar",
		"  /quit /attach /open /issue /help",
		"  ?                 toggle this help",
		"  q                 quit",
		"",
	}, "\n")
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func truncate(s string, n int) string {
	if lipgloss.Width(s) <= n {
		return s
	}
	if n <= 1 {
		return "…"
	}
	return s[:n-1] + "…"
}

// Run launches the dashboard TUI. Each data source is loaded once at
// startup; refresh-on-tick is a follow-up. Empty paths or absent data
// render the affected panel empty.
// `once` renders one fixed-size frame to stdout and returns (smoke-test
// path so CI can exercise the view without a real TTY).
func Run(root, issuesCachePath, slotsPath string, once bool) error {
	repos := loadRepos(root)
	issues := loadIssues(issuesCachePath)
	sessions := loadSessions(slotsPath)
	if once {
		m := initialModel(repos, issues, sessions)
		m.width, m.height = 130, 42
		fmt.Print(m.View())
		fmt.Println()
		return nil
	}
	p := tea.NewProgram(initialModel(repos, issues, sessions), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return err
	}
	return nil
}
