package nav

import (
	"os"
	"os/exec"
	"strings"

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
		return m, nil
	case sessionsMsg:
		m.sessions = msg.rows
		return m, nil
	case remoteMsg:
		m.remoteRepos = msg.rows
		m.remoteState = loadOK
		return m, nil
	case remoteErrMsg:
		m.remoteState = loadErr
		m.status = "remote unavailable (cached rows shown)"
		return m, nil

	case dashRowsMsg:
		m.dashRows = msg.rows
		if m.dashSel >= len(m.dashRows) {
			m.dashSel = 0
		}
		return m, m.dirtyCmds()
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

	case tea.KeyMsg:
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

// --- Minimal helpers (completed in Tasks 9/10). Keep tight: only what compiles
// + passes this task's tests. Task 9 REPLACES updatePicker/updateDash with full
// versions and adds updateModal/visibleRepos; Task 10 replaces launchRow. ---

// visibleRepos is the filtered local+remote row list shown in the picker.
func (m Model) visibleRepos() []repoRow {
	all := append(append([]repoRow{}, m.localRepos...), m.remoteRepos...)
	return filterRepos(all, m.filter.Value())
}

func (m Model) updatePicker(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c", "q":
		if m.pickerFocus == focusList {
			return m, tea.Quit
		}
		// fallthrough to filter editing for 'q' typed into the filter
	case "esc":
		return m, tea.Quit
	}

	if m.pickerFocus == focusFilter {
		switch msg.Type {
		case tea.KeyDown:
			m.pickerFocus = focusList
			m.filter.Blur()
			m.pickerSel = 0
			return m, nil
		case tea.KeyEnter:
			m.pickerFocus = focusList
			m.filter.Blur()
			return m, nil
		}
		var cmd tea.Cmd
		m.filter, cmd = m.filter.Update(msg)
		return m, cmd
	}

	// focusList
	rows := m.visibleRepos()
	switch msg.String() {
	case "up", "k":
		if len(rows) > 0 {
			m.pickerSel = (m.pickerSel + len(rows) - 1) % len(rows)
		}
	case "down", "j":
		if len(rows) > 0 {
			m.pickerSel = (m.pickerSel + 1) % len(rows)
		}
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
		sel := rows[m.pickerSel]
		if sel.remote != nil {
			m.status = "cloning " + sel.label + "…"
			return m, cloneCmd(m.cfg.Clone, *sel.remote)
		}
		return m.enterDash(sel.repo)
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
	case "up", "k":
		if n := len(m.dashRows) + 1; n > 0 { // +1 for the "+ create" row
			m.dashSel = (m.dashSel + n - 1) % n
		}
	case "down", "j":
		if n := len(m.dashRows) + 1; n > 0 {
			m.dashSel = (m.dashSel + 1) % n
		}
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
	return m, nil
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
		return m, createWorktreeCmd(m.repo, name)
	case tea.KeyBackspace:
		if n := len(m.modal.name); n > 0 {
			m.modal.name = m.modal.name[:n-1]
		}
		return m, nil
	case tea.KeyRunes:
		m.modal.name += string(msg.Runes)
		return m, nil
	}
	return m, nil
}

// enterDash switches to the dashboard for repo and loads its rows.
func (m Model) enterDash(repo core.Repo) (tea.Model, tea.Cmd) {
	m.screen = screenDash
	m.repo = repo
	m.dashSel = 0
	m.dashRows = nil
	m.status = "ready"
	return m, loadDashRowsCmd(repo, m.cfg.SlotsPath)
}

// slotIDFor derives the tmux slot/session name for a worktree row: the repo
// name + worktree, filtered to tmux-safe characters.
func (m Model) slotIDFor(row dashRow) string {
	base := m.repo.Name
	if row.worktree != "" {
		base = m.repo.Name + "-" + row.worktree
	}
	return tmuxSafe(base)
}

// launchArgvFor returns the argv to attach an existing session, or to create +
// launch the default agent in a session-less worktree. Honours $TMUX (nested
// switch-client) the same way the open path does.
func (m Model) launchArgvFor(row dashRow) ([]string, error) {
	l := launcher.New()
	if row.hasSession && row.slotID != "" {
		return l.AttachArgv(row.slotID), nil
	}
	name := m.cfg.DefaultAgent
	if name == "" {
		name = "claude"
	}
	spec, err := agents.Resolve(name)
	if err != nil {
		return nil, err
	}
	if len(m.cfg.AgentArgs) > 0 {
		spec.Args = append(append([]string{}, spec.Args...), m.cfg.AgentArgs...)
	}
	slot := m.slotIDFor(row)
	if os.Getenv("TMUX") != "" {
		return l.LaunchArgvNested(slot, row.path, spec)
	}
	return l.LaunchArgv(slot, row.path, spec)
}

func (m Model) launchRow(row dashRow) (tea.Model, tea.Cmd) {
	argv, err := m.launchArgvFor(row)
	if err != nil {
		m.status = err.Error()
		return m, nil
	}
	c := exec.Command(argv[0], argv[1:]...)
	return m, tea.ExecProcess(c, func(err error) tea.Msg { return execDoneMsg{err: err} })
}

// tmuxSafe drops characters tmux session names disallow; empty -> "repo".
func tmuxSafe(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '-', r == '_', r == '.':
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
