package nav

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/freaxnx01/bridge/internal/agents"
	"github.com/freaxnx01/bridge/internal/core"
	"github.com/freaxnx01/bridge/internal/launcher"
)

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case reposMsg:
		m.localRepos = msg.rows
		return m, m.issueCountCmds(msg.rows)
	case sessionsMsg:
		m.sessions = msg.rows
		return m, nil
	case remoteMsg:
		m.remoteRepos = msg.rows
		m.remoteState = loadOK
		return m, m.issueCountCmds(msg.rows)
	case remoteErrMsg:
		m.remoteState = loadErr
		m.status = "remote unavailable (cached rows shown)"
		return m, nil

	case dashRowsMsg:
		m.dashRows = msg.rows
		if m.dashSel >= len(m.dashRows) {
			m.dashSel = 0
		}
		m.details = map[string]*worktreeDetails{} // fresh rows -> reload panels
		m, detailCmd := m.ensureDetails()
		return m, tea.Batch(m.dirtyCmds(), detailCmd, gitFetchCmd(m.repo.Path))
	case fetchDoneMsg:
		if msg.err != nil {
			return m, nil // offline / fetch failed: keep last-known status
		}
		return m, m.dirtyCmds() // re-read status against the now-fresh remote refs
	case dirtyMsg:
		for i := range m.dashRows {
			if m.dashRows[i].path == msg.path {
				if msg.err != nil {
					m.dashRows[i].dirtyState = loadErr
				} else {
					m.dashRows[i].dirty = msg.info
					m.dashRows[i].dirtyState = loadOK
				}
			}
		}
		return m, nil
	case branchesMsg:
		if d := m.details[msg.path]; d != nil {
			if msg.err != nil {
				d.branchesState = loadErr
			} else {
				d.branches = msg.branches
				d.branchesState = loadOK
			}
		}
		return m, nil
	case commitsMsg:
		if d := m.details[msg.path]; d != nil {
			if msg.err != nil {
				d.commitsState = loadErr
			} else {
				d.commits = msg.commits
				d.commitsState = loadOK
			}
		}
		return m, nil
	case statusMsg:
		if d := m.details[msg.path]; d != nil {
			if msg.err != nil {
				d.statusState = loadErr
			} else {
				d.status = msg.files
				d.statusState = loadOK
			}
		}
		return m, nil

	case cloneDoneMsg:
		if msg.err != nil {
			m.status = "clone failed: " + msg.err.Error()
			return m, nil
		}
		return m.enterDash(msg.repo)
	case wtCreatedMsg:
		if msg.err != nil {
			if m.modal != nil {
				m.modal.err = msg.err.Error()
			} else {
				m.status = "worktree create failed: " + msg.err.Error()
			}
			return m, nil
		}
		m.modal = nil
		return m.launchRow(msg.row)
	case execDoneMsg:
		// Returned from a detached tmux attach/launch: refresh the screen we're on.
		if m.screen == screenDash {
			return m, loadDashRowsCmd(m.repo, m.cfg.SlotsPath)
		}
		return m, loadSessionsCmd(m.cfg.SlotsPath)
	case slotRegisteredMsg:
		return m, nil

	case issueCountMsg:
		for i := range m.localRepos {
			if k, _, _, _, ok := rowForgeKey(m.localRepos[i]); ok && k == msg.key {
				m.localRepos[i].issueCount = msg.count
				m.localRepos[i].issueState = loadOK
			}
		}
		for i := range m.remoteRepos {
			if k, _, _, _, ok := rowForgeKey(m.remoteRepos[i]); ok && k == msg.key {
				m.remoteRepos[i].issueCount = msg.count
				m.remoteRepos[i].issueState = loadOK
			}
		}
		return m, nil
	case repoIssuesMsg:
		m.issues = make([]issueRow, 0, len(msg.issues))
		for _, is := range msg.issues {
			m.issues = append(m.issues, issueRow{number: is.Number, title: is.Title})
		}
		m.issuesState = loadOK
		if m.issueSel >= len(m.issues) {
			m.issueSel = 0
		}
		if len(m.issues) == 0 && m.dashFocus == dashFocusIssues {
			m.dashFocus = dashFocusWorktrees
		}
		return m, nil

	case tea.KeyMsg:
		if m.cfg.DebugKeys != "" {
			logKey(m.cfg.DebugKeys, msg)
		}
		if m.screen == screenPicker {
			return m.updatePicker(msg)
		}
		return m.updateDash(msg)
	}
	// spinner.TickMsg flows here; forward it.
	var cmd tea.Cmd
	m.spin, cmd = m.spin.Update(msg)
	return m, cmd
}

// dirtyCmds fires a gitDirty Cmd per dashboard row.
func (m Model) dirtyCmds() tea.Cmd {
	cmds := make([]tea.Cmd, 0, len(m.dashRows))
	for _, r := range m.dashRows {
		cmds = append(cmds, gitDirtyCmd(r.path))
	}
	return tea.Batch(cmds...)
}

// visibleRepos is the filtered local+remote row list shown in the picker.
// Remote rows already cloned locally are dropped so a repo isn't listed twice.
func (m Model) visibleRepos() []repoRow {
	all := append([]repoRow{}, m.localRepos...)
	all = append(all, dedupRemoteRows(m.localRepos, m.remoteRepos)...)
	return filterRepos(all, m.filter.Value())
}

func (m Model) updatePicker(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c", "q":
		if m.pickerFocus != focusFilter {
			return m, tea.Quit
		}
		// fallthrough to filter editing for 'q' typed into the filter
	case "esc":
		return m, tea.Quit
	case "tab":
		return m.cyclePickerFocus(), nil
	case "shift+tab":
		return m.cyclePickerFocusBack(), nil
	}

	if m.pickerFocus == focusSessions {
		switch msg.String() {
		case "up", "k":
			if m.sessionSel > 0 {
				m.sessionSel--
			}
		case "down", "j":
			if m.sessionSel < len(m.sessions)-1 {
				m.sessionSel++
			} else {
				m.pickerFocus = focusFilter
				m.filter.Focus()
			}
		case "g", "home":
			m.sessionSel = 0
		case "G", "end":
			if len(m.sessions) > 0 {
				m.sessionSel = len(m.sessions) - 1
			}
		case "/":
			m.pickerFocus = focusFilter
			m.filter.Focus()
		case "enter":
			if m.sessionSel >= 0 && m.sessionSel < len(m.sessions) {
				if sl := m.sessions[m.sessionSel].slotID; sl != "" {
					return m, execArgvCmd(launcher.New().AttachArgv(sl))
				}
			}
		}
		return m, nil
	}

	if m.pickerFocus == focusFilter {
		switch msg.Type {
		case tea.KeyUp:
			if len(m.sessions) > 0 {
				m.pickerFocus = focusSessions
				m.filter.Blur()
				m.sessionSel = len(m.sessions) - 1
				return m, nil
			}
		case tea.KeyDown:
			m.pickerFocus = focusList
			m.filter.Blur()
			m.pickerSel = 0
			return m, nil
		case tea.KeyEnter:
			if rows := m.visibleRepos(); len(rows) == 1 {
				return m.openRepoRow(rows[0])
			}
			m.pickerFocus = focusList
			m.filter.Blur()
			return m, nil
		case tea.KeyHome, tea.KeyPgUp:
			// Home/PgUp from the filter jump into the list at the top.
			m.pickerFocus = focusList
			m.filter.Blur()
			m.pickerSel = 0
			return m, nil
		case tea.KeyEnd:
			m.pickerFocus = focusList
			m.filter.Blur()
			if n := len(m.visibleRepos()); n > 0 {
				m.pickerSel = n - 1
			}
			return m, nil
		case tea.KeyPgDown:
			m.pickerFocus = focusList
			m.filter.Blur()
			m.pickerSel = clampInt(m.listPage(), 0, len(m.visibleRepos())-1)
			return m, nil
		}
		var cmd tea.Cmd
		m.filter, cmd = m.filter.Update(msg)
		m.pickerSel = 0
		return m, cmd
	}

	// focusList
	rows := m.visibleRepos()
	switch msg.String() {
	case "up", "k":
		if m.pickerSel <= 0 {
			// at the top of the list: step back up into the filter
			m.pickerFocus = focusFilter
			m.filter.Focus()
			return m, nil
		}
		m.pickerSel--
	case "down", "j":
		if m.pickerSel < len(rows)-1 {
			m.pickerSel++
		}
	case "home", "g":
		m.pickerSel = 0
	case "end", "G":
		if len(rows) > 0 {
			m.pickerSel = len(rows) - 1
		}
	case "pgup", "ctrl+u":
		m.pickerSel = clampInt(m.pickerSel-m.listPage(), 0, len(rows)-1)
	case "pgdown", "ctrl+d":
		m.pickerSel = clampInt(m.pickerSel+m.listPage(), 0, len(rows)-1)
	case "/":
		m.pickerFocus = focusFilter
		m.filter.Focus()
	case "r":
		m.remoteState = loadPending
		return m, loadRemoteCmd(m.cfg.RemoteCache)
	case "enter":
		if len(rows) == 0 {
			return m, nil
		}
		return m.openRepoRow(rows[m.pickerSel])
	}
	return m, nil
}

func (m Model) updateDash(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Modal captures keys first.
	if m.modal != nil {
		return m.updateModal(msg)
	}
	switch msg.String() {
	case "ctrl+c", "q":
		return m, tea.Quit
	case "esc":
		m.screen = screenPicker
		m.pickerFocus = focusList
		return m, loadSessionsCmd(m.cfg.SlotsPath)
	case "tab", "shift+tab":
		// Toggle between the worktree list and the open-issues pane (only when
		// the repo has issues to land on).
		if len(m.issues) > 0 {
			if m.dashFocus == dashFocusWorktrees {
				m.dashFocus = dashFocusIssues
				m.issueSel = clampInt(m.issueSel, 0, len(m.issues)-1)
			} else {
				m.dashFocus = dashFocusWorktrees
			}
		}
		return m, nil
	}
	if m.dashFocus == dashFocusIssues {
		return m.updateDashIssues(msg)
	}
	return m.updateDashWorktrees(msg)
}

// updateDashWorktrees handles keys when the worktree list (left pane) is focused.
func (m Model) updateDashWorktrees(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		if n := len(m.dashRows) + 1; n > 0 { // +1 for the "+ create" row
			m.dashSel = (m.dashSel + n - 1) % n
		}
	case "down", "j":
		if n := len(m.dashRows) + 1; n > 0 {
			m.dashSel = (m.dashSel + 1) % n
		}
	case "home", "g":
		m.dashSel = 0
	case "end", "G":
		m.dashSel = len(m.dashRows)
	case "pgup", "ctrl+u":
		m.dashSel = clampInt(m.dashSel-m.listPage(), 0, len(m.dashRows))
	case "pgdown", "ctrl+d":
		m.dashSel = clampInt(m.dashSel+m.listPage(), 0, len(m.dashRows))
	case "n":
		m.modal = &newWorktreeModal{}
		return m, nil
	case "enter":
		// The last selectable index is the "+ create" row.
		if m.dashSel == len(m.dashRows) {
			m.modal = &newWorktreeModal{}
			return m, nil
		}
		if m.dashSel < len(m.dashRows) {
			return m.launchRow(m.dashRows[m.dashSel])
		}
	}
	return m.ensureDetails()
}

// updateDashIssues handles keys when the open-issues pane (right) is focused.
// Enter creates a worktree named after the issue and launches a session in it.
func (m Model) updateDashIssues(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "up", "k":
		if m.issueSel > 0 {
			m.issueSel--
		}
	case "down", "j":
		if m.issueSel < len(m.issues)-1 {
			m.issueSel++
		}
	case "home", "g":
		m.issueSel = 0
	case "end", "G":
		if len(m.issues) > 0 {
			m.issueSel = len(m.issues) - 1
		}
	case "pgup", "ctrl+u":
		m.issueSel = clampInt(m.issueSel-m.listPage(), 0, len(m.issues)-1)
	case "pgdown", "ctrl+d":
		m.issueSel = clampInt(m.issueSel+m.listPage(), 0, len(m.issues)-1)
	case "enter":
		if m.issueSel >= 0 && m.issueSel < len(m.issues) {
			return m.launchIssue(m.issues[m.issueSel])
		}
	}
	return m, nil
}

// launchIssue creates (or resolves) a worktree named after the issue and
// launches a session in it, labelled "#<num> [<short title>]".
func (m Model) launchIssue(ir issueRow) (tea.Model, tea.Cmd) {
	wt, label := issueWorktreeName(ir.number, ir.title)
	m.status = fmt.Sprintf("creating worktree for #%d…", ir.number)
	return m, createWorktreeCmd(m.repo, wt, label)
}

func (m Model) updateModal(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEsc:
		m.modal = nil
		return m, nil
	case tea.KeyEnter:
		name := strings.TrimSpace(m.modal.name)
		if name == "" {
			m.modal.err = "name required"
			return m, nil
		}
		return m, createWorktreeCmd(m.repo, name, "")
	case tea.KeyBackspace:
		if r := []rune(m.modal.name); len(r) > 0 {
			m.modal.name = string(r[:len(r)-1])
		}
		return m, nil
	case tea.KeyRunes:
		m.modal.name += string(msg.Runes)
		return m, nil
	}
	return m, nil
}

// enterDash switches to the dashboard for repo and loads its rows + open issues.
func (m Model) enterDash(repo core.Repo) (tea.Model, tea.Cmd) {
	m.screen = screenDash
	m.repo = repo
	m.dashSel = 0
	m.dashRows = nil
	m.dashFocus = dashFocusWorktrees
	m.issues = nil
	m.issueSel = 0
	m.status = "ready"
	cmds := []tea.Cmd{loadDashRowsCmd(repo, m.cfg.SlotsPath)}
	if c := m.repoIssuesCmd(repo); c != nil {
		m.issuesState = loadPending
		cmds = append(cmds, c)
	} else {
		m.issuesState = loadOK // nothing to load: an empty, settled pane
	}
	return m, tea.Batch(cmds...)
}

// repoIssuesCmd returns a Cmd loading the repo's open issues, or nil when issue
// loading is unconfigured or the repo lacks forge identifiers.
func (m Model) repoIssuesCmd(repo core.Repo) tea.Cmd {
	if m.cfg.IssueCacheDir == "" && m.cfg.FetchIssues == nil {
		return nil
	}
	if repo.Forge == "" || repo.Owner == "" || repo.Name == "" {
		return nil
	}
	return loadRepoIssuesCmd(m.cfg, repo.Forge, repo.Owner, repo.Name)
}

// launchPlan decides attach-vs-launch for a row. For a new session it returns
// the slot to register; for an attach it returns slot == "".
func (m Model) launchPlan(row dashRow) (argv []string, slot, agent string, err error) {
	l := launcher.New()
	if row.hasSession && row.slotID != "" {
		return l.AttachArgv(row.slotID), "", "", nil
	}
	agent = m.cfg.DefaultAgent
	if agent == "" {
		agent = "claude"
	}
	spec, err := agents.Resolve(agent)
	if err != nil {
		return nil, "", "", err
	}
	if m.cfg.NameArgs != nil {
		if na := m.cfg.NameArgs(agent, m.repo, row.worktree, row.displayLabel); len(na) > 0 {
			spec.Args = append(append([]string{}, na...), spec.Args...)
		}
	}
	if len(m.cfg.AgentArgs) > 0 {
		spec.Args = append(append([]string{}, spec.Args...), m.cfg.AgentArgs...)
	}
	slot = core.SlotID(m.repo.Name, row.worktree)
	// nav runs the launch through tea.ExecProcess (it owns the terminal), so it
	// nests tmux directly via `new-session -A` rather than emitting a
	// switch-client directive like the shell open path (issue #114). $TMUX is
	// cleared in launchRow so the nested attach is permitted.
	argv, err = l.LaunchArgv(slot, row.path, spec)
	if err != nil {
		return nil, "", "", err
	}
	return argv, slot, agent, nil
}

// launchArgvFor returns the argv to attach an existing session, or to create +
// launch the default agent in a session-less worktree.
func (m Model) launchArgvFor(row dashRow) ([]string, error) {
	argv, _, _, err := m.launchPlan(row)
	return argv, err
}

func (m Model) launchRow(row dashRow) (tea.Model, tea.Cmd) {
	argv, slot, agent, err := m.launchPlan(row)
	if err != nil {
		m.status = err.Error()
		return m, nil
	}
	exe := execArgvCmd(argv)
	if slot == "" {
		return m, exe // attaching an already-registered session
	}
	reg := registerSlotCmd(m.cfg.SlotsPath, core.Slot{
		ID: slot, Repo: m.repo.Name, Worktree: row.worktree, Agent: agent, Created: time.Now().UTC(),
	})
	return m, tea.Sequence(reg, exe)
}

// tmuxUnset returns env with TMUX and TMUX_PANE removed so a child tmux command
// nests inside the current server instead of refusing to run.
func tmuxUnset(env []string) []string {
	out := make([]string, 0, len(env))
	for _, e := range env {
		if strings.HasPrefix(e, "TMUX=") || strings.HasPrefix(e, "TMUX_PANE=") {
			continue
		}
		out = append(out, e)
	}
	return out
}

// listPage is how many rows a PgUp/PgDown moves the selection — roughly one
// screenful, derived from the terminal height.
func (m Model) listPage() int {
	if p := m.height - 10; p > 1 {
		return p
	}
	return 1
}

// clampInt clamps v to [lo, hi]; an empty range (hi < lo) yields lo.
func clampInt(v, lo, hi int) int {
	switch {
	case hi < lo:
		return lo
	case v < lo:
		return lo
	case v > hi:
		return hi
	default:
		return v
	}
}

// cyclePickerFocus advances Tab focus: filter -> list -> sessions (when any) ->
// filter, keeping the filter's text-input focus state in sync.
func (m Model) cyclePickerFocus() Model {
	switch m.pickerFocus {
	case focusFilter:
		m.pickerFocus = focusList
		m.filter.Blur()
	case focusList:
		if len(m.sessions) > 0 {
			m.pickerFocus = focusSessions
			m.sessionSel = clampInt(m.sessionSel, 0, len(m.sessions)-1)
		} else {
			m.pickerFocus = focusFilter
			m.filter.Focus()
		}
	default: // focusSessions
		m.pickerFocus = focusFilter
		m.filter.Focus()
	}
	return m
}

// execArgvCmd runs argv via tea.ExecProcess with $TMUX cleared so a tmux
// attach/new-session nests under the current server (see launchRow / issue #114).
func execArgvCmd(argv []string) tea.Cmd {
	if len(argv) == 0 {
		return nil
	}
	c := exec.Command(argv[0], argv[1:]...)
	c.Env = tmuxUnset(os.Environ())
	return tea.ExecProcess(c, func(err error) tea.Msg { return execDoneMsg{err: err} })
}

// openRepoRow opens a picker row: clone a remote row, else enter its dashboard.
func (m Model) openRepoRow(row repoRow) (tea.Model, tea.Cmd) {
	if row.remote != nil {
		m.status = "cloning " + row.label + "…"
		return m, cloneCmd(m.cfg.Clone, *row.remote)
	}
	return m.enterDash(row.repo)
}

// cyclePickerFocusBack reverses cyclePickerFocus (Shift+Tab):
// filter -> sessions (when any) -> list -> filter.
func (m Model) cyclePickerFocusBack() Model {
	switch m.pickerFocus {
	case focusFilter:
		m.filter.Blur()
		if len(m.sessions) > 0 {
			m.pickerFocus = focusSessions
			m.sessionSel = clampInt(m.sessionSel, 0, len(m.sessions)-1)
		} else {
			m.pickerFocus = focusList
		}
	case focusSessions:
		m.pickerFocus = focusList
	default: // focusList
		m.pickerFocus = focusFilter
		m.filter.Focus()
	}
	return m
}

// selectedWorktreePath is the path of the highlighted worktree row, or "" when
// the trailing "+ Create new worktree…" row is selected (no worktree).
func (m Model) selectedWorktreePath() string {
	if m.dashSel < 0 || m.dashSel >= len(m.dashRows) {
		return ""
	}
	return m.dashRows[m.dashSel].path
}

// ensureDetails kicks an async load of the highlighted worktree's three detail
// panels when its data isn't cached yet. A cache hit (entry already present, any
// state) or the "+ create" row returns no Cmd. The new entry's zero-value
// loadStates are loadPending, so the view shows spinners until the msgs land.
func (m Model) ensureDetails() (Model, tea.Cmd) {
	path := m.selectedWorktreePath()
	if path == "" {
		return m, nil
	}
	if m.details == nil {
		m.details = map[string]*worktreeDetails{}
	}
	if _, ok := m.details[path]; ok {
		return m, nil
	}
	m.details[path] = &worktreeDetails{}
	return m, tea.Batch(
		gitBranchesCmd(path),
		gitCommitsCmd(path),
		gitStatusCmd(path),
	)
}

// issueCountCmds fires loadIssueCountCmd for each row that has forge identifiers,
// when at least one of IssueCacheDir or FetchIssues is configured.
func (m Model) issueCountCmds(rows []repoRow) tea.Cmd {
	if m.cfg.IssueCacheDir == "" && m.cfg.FetchIssues == nil {
		return nil
	}
	var cmds []tea.Cmd
	for _, r := range rows {
		key, forgeName, owner, name, ok := rowForgeKey(r)
		if !ok {
			continue
		}
		cmds = append(cmds, loadIssueCountCmd(m.cfg, key, forgeName, owner, name))
	}
	return tea.Batch(cmds...)
}

// logKey appends a key diagnostic line to path (BRIDGE_NAV_DEBUG).
func logKey(path string, k tea.KeyMsg) {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "string=%q type=%d runes=%q alt=%v\n", k.String(), int(k.Type), string(k.Runes), k.Alt)
}
