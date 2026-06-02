package core

import (
	"bufio"
	"errors"
	"os/exec"
	"strings"
)

// KnownAgentCommands names the foreground pane commands that mark a tmux
// session as "agent-running" for the purposes of bridge status. Sessions
// without at least one matching pane are excluded from the status table
// even when live.
//
// Names are matched case-insensitively against tmux's pane_current_command
// (basename of the foreground process). Add to this list when integrating
// a new agent; the launcher's agents.AgentSpec.Bin should match.
var KnownAgentCommands = map[string]bool{
	"claude":        true,
	"copilot":       true,
	"copilot-cli":   true,
	"opencode":      true,
	"code":          true,
	"code-insiders": true,
	"node":          true, // claude/copilot wrappers commonly show as node
}

// LivePaneCommands enumerates tmux sessions and returns a map of
// session_name → list of pane_current_command values across all panes.
// Empty/missing tmux → (nil, nil), matching LiveSessions semantics.
func LivePaneCommands() (map[string][]string, error) {
	cmd := exec.Command("tmux", "list-panes", "-a", "-F", "#{session_name}|#{pane_current_command}")
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return nil, nil
		}
		if errors.Is(err, exec.ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return ParsePaneList(string(out)), nil
}

// ParsePaneList is the pure-function half of LivePaneCommands. Accepts the
// raw tmux output and returns the session→commands map.
func ParsePaneList(raw string) map[string][]string {
	out := map[string][]string{}
	sc := bufio.NewScanner(strings.NewReader(raw))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "|", 2)
		if len(parts) != 2 {
			continue
		}
		out[parts[0]] = append(out[parts[0]], parts[1])
	}
	return out
}

// SessionRunsKnownAgent returns true if any pane in commands matches a known
// agent name (case-insensitive). Used to decide whether an untagged tmux
// session belongs in `bridge status`.
func SessionRunsKnownAgent(commands []string) bool {
	for _, c := range commands {
		if KnownAgentCommands[strings.ToLower(strings.TrimSpace(c))] {
			return true
		}
	}
	return false
}
