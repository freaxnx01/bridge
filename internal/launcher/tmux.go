//go:build !windows

package launcher

import (
	"errors"

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

func (Tmux) AttachArgv(slot string) []string {
	return []string{"tmux", "attach-session", "-t", slot}
}
