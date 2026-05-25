package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// --- mock data ---

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

var (
	mockRepos = []repo{
		{"public/bridge", 1, "pub"},
		{"private/ingest-pipeline", 4, "pri"},
		{"private/dms-core", 7, "pri"},
		{"public/ai-instructions", 0, "pub"},
	}
	mockIssues = []issue{
		{30, "public/bridge", "feat(dashboard): TUI / styled output for --dashboard"},
		{142, "private/ingest-pipeline", "fix: retry on transient 503 from upstream"},
		{139, "private/ingest-pipeline", "feat: parquet writer backpressure"},
		{138, "private/ingest-pipeline", "chore: bump pyarrow to 17.x"},
		{91, "private/dms-core", "perf: index rebuild parallelism"},
		{88, "private/dms-core", "feat: stamp-level retention overrides"},
		{84, "private/dms-core", "fix: trustee group resolution on rename"},
	}
	mockSessions = []session{
		{"bridge:main", "attached", "2h ago", "public/bridge"},
		{"ingest:bug-142", "detached", "5m ago", "private/ingest-pipeline"},
		{"dms:perf-91", "code", "yesterday", "private/dms-core"},
	}
)

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
}

func initialModel() model {
	ti := textinput.New()
	ti.Placeholder = "type a /command or text…"
	ti.Prompt = ""
	ti.CharLimit = 200
	ti.Width = 60
	return model{
		focus:  paneIssues,
		cmd:    ti,
		status: "ready",
	}
}

func (m model) Init() tea.Cmd { return textinput.Blink }

func (m *model) rowCount(p pane) int {
	switch p {
	case paneRepos:
		return len(mockRepos)
	case paneIssues:
		return len(mockIssues)
	case paneSessions:
		return len(mockSessions)
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
		m.status = "→ would drill into " + mockRepos[m.sel[paneRepos]].name
	case paneIssues:
		i := mockIssues[m.sel[paneIssues]]
		m.status = fmt.Sprintf("→ would open #%d in %s", i.num, i.repo)
	case paneSessions:
		s := mockSessions[m.sel[paneSessions]]
		m.status = "→ would attach to " + s.name
	}
	return nil
}

// --- views ---

func (m model) viewRepos(w, h int) string {
	focused := m.focus == paneRepos
	var b strings.Builder
	b.WriteString(titleStyle(focused).Render(" Repos ") + "\n\n")
	for i, r := range mockRepos {
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
	for i, is := range mockIssues {
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
	for i, s := range mockSessions {
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

func main() {
	if len(os.Args) > 1 && os.Args[1] == "--once" {
		m := initialModel()
		m.width, m.height = 130, 42
		fmt.Print(m.View())
		fmt.Println()
		return
	}
	p := tea.NewProgram(initialModel(), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
