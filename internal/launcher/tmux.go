//go:build !windows

package launcher

import (
	"errors"
	"fmt"
	"strings"

	"github.com/freaxnx01/bridge/internal/agents"
)

type Tmux struct{}

func New() Launcher { return &Tmux{} }

func (Tmux) LaunchArgv(slot, dir string, agent agents.AgentSpec) ([]string, error) {
	if slot == "" {
		return nil, errors.New("launcher: empty slot")
	}
	if dir == "" {
		return nil, errors.New("launcher: empty dir")
	}
	if agent.Bin == "" {
		return nil, errors.New("launcher: agent has no Bin")
	}
	argv := []string{"tmux", "new-session", "-A", "-s", slot, "-c", dir, agent.Bin}
	argv = append(argv, agent.Args...)
	return argv, nil
}

func (Tmux) LaunchArgvNested(slot, dir string, agent agents.AgentSpec) ([]string, error) {
	if slot == "" {
		return nil, errors.New("launcher: empty slot")
	}
	if dir == "" {
		return nil, errors.New("launcher: empty dir")
	}
	if agent.Bin == "" {
		return nil, errors.New("launcher: agent has no Bin")
	}
	// We're already inside tmux: `new-session -A` would error out trying to
	// nest. Instead, create the session detached if it doesn't exist, then
	// have the current client switch to it.
	innerParts := append([]string{agent.Bin}, agent.Args...)
	innerCmd := joinShellQuoted(innerParts)
	body := fmt.Sprintf(
		"tmux has-session -t %s 2>/dev/null || tmux new-session -d -s %s -c %s %s; exec tmux switch-client -t %s",
		shellQuote(slot), shellQuote(slot), shellQuote(dir), innerCmd, shellQuote(slot),
	)
	return []string{"sh", "-c", body}, nil
}

func (Tmux) AttachArgv(slot string) []string {
	return []string{"tmux", "attach-session", "-t", slot}
}

// shellQuote mirrors internal/shellbridge.shellQuote but is duplicated here to
// keep launcher independent of shellbridge. Quotes only when needed.
func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	safe := true
	for _, r := range s {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') ||
			r == '_' || r == '-' || r == '.' || r == '/' || r == ':' || r == '@' || r == '+' || r == '=' || r == ',') {
			safe = false
			break
		}
	}
	if safe {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

func joinShellQuoted(parts []string) string {
	q := make([]string, len(parts))
	for i, p := range parts {
		q[i] = shellQuote(p)
	}
	return strings.Join(q, " ")
}
