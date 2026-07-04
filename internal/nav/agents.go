package nav

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/freaxnx01/bridge/internal/agentview"
)

// agentRow is one rendered row on the Agents screen.
type agentRow struct {
	dot    string // ● busy · ○ otherwise
	kind   string
	name   string
	status string
	repo   string // shortened cwd
	age    string
}

// shortRepo abbreviates an absolute cwd for display: a leading home prefix
// becomes "~". home == "" disables the substitution.
func shortRepo(cwd, home string) string {
	if home != "" && cwd == home {
		return "~"
	}
	if home != "" && strings.HasPrefix(cwd, home+"/") {
		return "~" + strings.TrimPrefix(cwd, home)
	}
	return cwd
}

// buildAgentRows turns agentview.Session values into display rows: a status dot,
// the shortened repo, and a humanized age relative to now. Pure for testability.
func buildAgentRows(sessions []agentview.Session, home string, now time.Time) []agentRow {
	rows := make([]agentRow, 0, len(sessions))
	for _, s := range sessions {
		dot := "○"
		if s.Status == "busy" {
			dot = "●"
		}
		rows = append(rows, agentRow{
			dot:    dot,
			kind:   s.Kind,
			name:   s.Name,
			status: s.Status,
			repo:   shortRepo(s.CWD, home),
			age:    humanLastAccessed(now.Sub(s.StartedAt)),
		})
	}
	return rows
}

// loadAgentsCmd fetches live Claude sessions off the Update loop and returns an
// agentsMsg (or agentsErrMsg on failure, distinguishing "claude unavailable" from
// a real parse error so the view can show the right notice).
func loadAgentsCmd() tea.Cmd {
	return func() tea.Msg {
		sessions, err := agentview.List(context.Background(), agentview.ExecRunner{})
		if err != nil {
			return agentsErrMsg{err: err, unavailable: errors.Is(err, agentview.ErrUnavailable)}
		}
		home, _ := os.UserHomeDir()
		return agentsMsg{rows: buildAgentRows(sessions, home, time.Now())}
	}
}

// updateAgentsKeys handles key presses on the Agents screen: navigate, r to
// refresh, esc back to the picker. Read-only (no attach/kill).
func (m Model) updateAgentsKeys(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.screen = screenPicker
		return m, nil
	case "r":
		m.agentsState = loadPending
		return m, loadAgentsCmd()
	case "up", "k":
		if m.agentsSel > 0 {
			m.agentsSel--
		}
	case "down", "j":
		if m.agentsSel < len(m.agents)-1 {
			m.agentsSel++
		}
	case "g", "home":
		m.agentsSel = 0
	case "G", "end":
		if len(m.agents) > 0 {
			m.agentsSel = len(m.agents) - 1
		}
	}
	return m, nil
}

// viewAgents renders the read-only Agents screen: a bordered panel listing every
// live Claude session (status dot, kind, name, status, repo, age), plus loading,
// unavailable, and empty states. Pure — no I/O (age was humanized at load time).
func (m Model) viewAgents() string {
	w := m.width
	title := "bridge · " + envLabel(m.cfg.Environment) + " · Agents"
	if m.agentsState == loadPending {
		return panel(w, title, stMuted.Render("◐ loading claude sessions…"))
	}
	if m.agentsUnavailable {
		body := stWarn.Render("⚠ Claude Agent View unavailable") + "\n" +
			stMuted.Render("is the `claude` CLI installed and on PATH?")
		return panel(w, title, body)
	}
	if len(m.agents) == 0 {
		return panel(w, title, stMuted.Render("No live Claude sessions."))
	}
	var b strings.Builder
	for i, r := range m.agents {
		line := fmt.Sprintf("%s %-11s %-20s %-7s %-28s %s",
			r.dot, trunc(r.kind, 11), trunc(r.name, 20), trunc(r.status, 7), trunc(r.repo, 28), r.age)
		b.WriteString(selectableLine(i == m.agentsSel, line) + "\n")
	}
	sections := []string{
		panel(w, title, strings.TrimRight(b.String(), "\n")),
		m.hintLine("↑↓ move · r refresh · esc back · q quit"),
	}
	return strings.Join(sections, "\n")
}
