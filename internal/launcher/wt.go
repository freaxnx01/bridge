//go:build windows

package launcher

import (
	"errors"

	"github.com/freaxnx01/bridge/internal/agents"
)

// WT uses Windows Terminal's command-line interface to open a new tab in the
// repo directory running the agent. Session reuse semantics differ from tmux:
// WT does not support named persistent sessions, so "attach" opens a fresh tab.
type WT struct{}

func New() Launcher { return &WT{} }

func (WT) LaunchArgv(slot, dir string, agent agents.AgentSpec) ([]string, error) {
	if slot == "" || dir == "" || agent.Bin == "" {
		return nil, errors.New("launcher(wt): missing slot/dir/agent")
	}
	argv := []string{"wt.exe", "new-tab", "--title", slot, "-d", dir, agent.Bin}
	argv = append(argv, agent.Args...)
	return argv, nil
}

func (WT) AttachArgv(slot string) []string {
	return []string{"wt.exe", "new-tab", "--title", slot}
}
