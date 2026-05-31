package nav

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/freaxnx01/bridge/internal/core"
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

// updatePicker (minimal): only the filter -> list focus transition needed now.
func (m Model) updatePicker(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.pickerFocus == focusFilter {
		if msg.Type == tea.KeyDown {
			m.pickerFocus = focusList
			m.filter.Blur()
			m.pickerSel = 0
			return m, nil
		}
		var cmd tea.Cmd
		m.filter, cmd = m.filter.Update(msg)
		return m, cmd
	}
	return m, nil
}

// updateDash (stub): completed in Task 9.
func (m Model) updateDash(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
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

// launchRow (stub): completed in Task 10 (tea.ExecProcess wiring).
func (m Model) launchRow(row dashRow) (tea.Model, tea.Cmd) {
	return m, nil
}
