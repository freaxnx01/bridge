// Package launcher constructs the argv that the parent shell should exec
// to land the user inside a session.
package launcher

import "github.com/freaxnx01/bridge/internal/agents"

// Launcher is the cross-platform interface.
type Launcher interface {
	// LaunchArgv returns argv for creating-and-attaching a session that runs the agent.
	// Idempotent: if a session named slot already exists, must attach to it.
	LaunchArgv(slot, dir string, agent agents.AgentSpec) ([]string, error)
	// AttachArgv returns argv for attaching to an existing session.
	AttachArgv(slot string) []string
}
