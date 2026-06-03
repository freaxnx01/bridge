// Package tui is the bridge dashboard TUI (Bubbletea). All three panels
// (Repos, Open Issues, Sessions) are wired to real data and Enter is a
// real action:
//   - Repos: core.DiscoverRepos. Enter → `tmux new-session -A -s <slug> -c <path>`.
//   - Issues: the cache populated by `bridge issues`. Enter → `xdg-open <url>`
//     (background) so control returns to the parent shell.
//   - Sessions: live tmux output cross-referenced with the slot registry.
//     Enter → `tmux attach-session -t <name>`.
//
// The tmux-shaped actions execute via syscall.Exec, so they replace the
// `bridge tui` process — the user's terminal becomes the new process
// directly with no nested shells.
package tui

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"
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
	path   string // filesystem path — needed for the tmux launch on Enter
	issues int
	vis    string // pri / pub
}

type issue struct {
	num   int
	repo  string
	title string
	url   string // for the browser-open action
}

type session struct {
	name  string
	state string // attached / detached / code
	age   string
	repo  string
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
		out = append(out, repo{name: display, path: r.Path, issues: 0, vis: vis})
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
		out = append(out, issue{num: i.Number, repo: i.Repo, title: i.Title, url: i.URL})
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

// renderPanel wraps panelStyle with the +2 width/+2 height correction for
// the border. lipgloss's .Width/.Height set the *content* dimensions
// (padding included, border excluded), so passing the outer dimensions
// without subtracting the border emits 2 trailing cells per row and an
// extra blank line — the .Width-vs-rounded-border glitch tracked in #73.
func renderPanel(focused bool, outerW, outerH int, content string) string {
	// Trim the builders' trailing newline so content height is exactly
	// title + rows; a stray blank line would push the panel one row past
	// outerH and clip the layout's top on short terminals (#103).
	content = strings.TrimRight(content, "\n")
	return panelStyle(focused).Width(outerW - 2).Height(outerH - 2).Render(content)
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

// actionKind tags what to do after the TUI exits. Set on the model by
// Enter or a slash command, then drained by Run() after p.Run() returns.
// Done this way (post-quit dispatch) rather than tea.ExecProcess because
// open-repo wants to *replace* our process via syscall.Exec, which
// tea.ExecProcess doesn't expose cleanly.
type actionKind int

const (
	actNone actionKind = iota
	actLaunchRepo
	actAttachSession
	actOpenURL
)

type pendingAction struct {
	kind actionKind
	// argv for actLaunchRepo / actAttachSession (executed via syscall.Exec).
	// url for actOpenURL (xdg-open in background).
	argv []string
	url  string
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

	action pendingAction
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
			return m.actOnSelection()
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
		m.focus = paneSessions
		return m.actOnSelection()
	case "/open":
		m.focus = paneRepos
		return m.actOnSelection()
	case "/issue":
		m.focus = paneIssues
		return m.actOnSelection()
	case "/help":
		m.showHelp = true
	default:
		m.status = fmt.Sprintf("unknown command: %s", parts[0])
	}
	return m, nil
}

// actOnSelection sets m.action and returns tea.Quit so Run() can dispatch
// after the TUI tears down. Returning early with a status-only update is
// used for rows that can't act (e.g. an Issues row with no URL).
func (m model) actOnSelection() (tea.Model, tea.Cmd) {
	switch m.focus {
	case paneRepos:
		if len(m.repos) == 0 {
			m.status = "no repos to open"
			return m, nil
		}
		r := m.repos[m.sel[paneRepos]]
		// `tmux new-session -A -s <slug> -c <path>` creates the session if
		// missing, attaches if it exists. Slug = repo basename (filtered to
		// tmux-safe chars). Wiring BRIDGE_DEFAULT_AGENT is a follow-up.
		slug := tmuxSafe(r.name)
		m.action = pendingAction{
			kind: actLaunchRepo,
			argv: []string{"tmux", "new-session", "-A", "-s", slug, "-c", r.path},
		}
		return m, tea.Quit
	case paneIssues:
		if len(m.issues) == 0 {
			m.status = "no issues to open"
			return m, nil
		}
		i := m.issues[m.sel[paneIssues]]
		if i.url == "" {
			m.status = fmt.Sprintf("issue #%d has no URL in the cache", i.num)
			return m, nil
		}
		m.action = pendingAction{kind: actOpenURL, url: i.url}
		return m, tea.Quit
	case paneSessions:
		if len(m.sessions) == 0 {
			m.status = "no sessions to attach"
			return m, nil
		}
		s := m.sessions[m.sel[paneSessions]]
		m.action = pendingAction{
			kind: actAttachSession,
			argv: []string{"tmux", "attach-session", "-t", s.name},
		}
		return m, tea.Quit
	}
	return m, nil
}

// tmuxSafe drops characters tmux session names disallow. Cheap; not a
// full sanitiser. Empty input falls back to "repo".
func tmuxSafe(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	if b.Len() == 0 {
		return "repo"
	}
	return b.String()
}

// --- views ---

func (m model) viewRepos(w, h int) string {
	focused := m.focus == paneRepos
	var b strings.Builder
	b.WriteString(titleStyle(focused).Render(" Repos ") + "\n\n")
	start, end := windowRange(len(m.repos), panelRows(h), m.sel[paneRepos])
	for i := start; i < end; i++ {
		r := m.repos[i]
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
	return renderPanel(focused, w, h, b.String())
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
	start, end := windowRange(len(m.issues), panelRows(h), m.sel[paneIssues])
	for i := start; i < end; i++ {
		is := m.issues[i]
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
	return renderPanel(focused, w, h, b.String())
}

func (m model) viewSessions(w, h int) string {
	focused := m.focus == paneSessions
	var b strings.Builder
	b.WriteString(titleStyle(focused).Render(" Sessions ") + "\n\n")
	start, end := windowRange(len(m.sessions), panelRows(h), m.sel[paneSessions])
	for i := start; i < end; i++ {
		s := m.sessions[i]
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
	return renderPanel(focused, w, h, b.String())
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

// Dashboard layout heights. chromeH is the fixed cost of the header (3), the
// command bar (3) and the hint line (2). minPanelH is the smallest a list panel
// can be and still show its border + title + one row; minPanelsH is the floor
// for the two stacked panels together, below which the view degrades to a
// "terminal too small" hint instead of clipping the top (#103).
const (
	chromeH    = 8
	minPanelH  = 5
	minPanelsH = minPanelH * 2
)

func (m model) View() string {
	if m.width == 0 || m.height == 0 {
		return "initialising…"
	}
	w := m.width

	// The chrome — header, command bar, hint — has a fixed height; the rest of
	// m.height is split between the repos/issues row and the sessions panel.
	// Deriving these from m.height (rather than hardcoding 11/7) keeps the top
	// from scrolling off the alt-screen on short terminals (#103).
	avail := m.height - chromeH
	if avail < minPanelsH {
		return m.viewTooSmall()
	}
	sessionsH := clampInt(avail/3, minPanelH, 7)
	rowH := clampInt(avail-sessionsH, minPanelH, 11)

	header := m.viewHeader(w)

	// repos | issues row — repos 38 wide, issues fills rest
	reposW := 38
	if reposW > w/2 {
		reposW = w / 2
	}
	issuesW := w - reposW
	repos := m.viewRepos(reposW, rowH)
	issues := m.viewIssues(issuesW, rowH)
	topRow := lipgloss.JoinHorizontal(lipgloss.Top, repos, issues)

	sessions := m.viewSessions(w, sessionsH)

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

// viewTooSmall renders a centred hint when the terminal has too few rows for
// the dashboard layout, sized to exactly fill (and not exceed) the screen.
func (m model) viewTooSmall() string {
	msg := badStyle.Render(fmt.Sprintf("terminal too small — %d rows, need %d+", m.height, chromeH+minPanelsH))
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, msg)
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// windowRange returns the [start, end) slice of `total` rows that fits in
// `capacity` visible rows while keeping index `sel` on screen (scroll-follow),
// so a list longer than its panel doesn't overflow the dashboard height (#103).
func windowRange(total, capacity, sel int) (int, int) {
	if capacity <= 0 {
		return 0, 0
	}
	if total <= capacity {
		return 0, total
	}
	start := sel - capacity/2
	if start < 0 {
		start = 0
	}
	if start+capacity > total {
		start = total - capacity
	}
	return start, start + capacity
}

// panelRows is how many list rows fit in a panel of outer height h: the two
// border lines and the two-line title leave h-4 for rows.
func panelRows(h int) int {
	return h - 4
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
	finalModel, err := p.Run()
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return err
	}
	if m, ok := finalModel.(model); ok {
		return dispatchAction(m.action)
	}
	return nil
}

// dispatchAction is called after the TUI has fully torn down. For
// tmux-shaped actions we syscall.Exec — the user's terminal becomes
// the new process directly. For URL actions we shell out in the
// background so control returns to the parent shell immediately.
func dispatchAction(a pendingAction) error {
	switch a.kind {
	case actNone:
		return nil
	case actOpenURL:
		opener := os.Getenv("BROWSER")
		if opener == "" {
			opener = "xdg-open"
		}
		return exec.Command(opener, a.url).Start()
	case actLaunchRepo, actAttachSession:
		if len(a.argv) == 0 {
			return nil
		}
		bin, err := exec.LookPath(a.argv[0])
		if err != nil {
			return fmt.Errorf("%s: %w", a.argv[0], err)
		}
		return syscall.Exec(bin, a.argv, os.Environ())
	}
	return nil
}
