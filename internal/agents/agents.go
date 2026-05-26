// Package agents resolves a user-facing agent name into the command that
// should run inside the launched session.
package agents

import "fmt"

// AgentSpec describes how to launch an agent.
type AgentSpec struct {
	Name string
	Bin  string
	Args []string
}

var registry = map[string]AgentSpec{
	"claude":   {Name: "claude", Bin: "claude"},
	"copilot":  {Name: "copilot", Bin: "copilot"},
	"opencode": {Name: "opencode", Bin: "opencode"},
	"code":     {Name: "code", Bin: "code", Args: []string{"."}},
}

// Resolve returns the spec for name.
func Resolve(name string) (AgentSpec, error) {
	if s, ok := registry[name]; ok {
		return s, nil
	}
	return AgentSpec{}, fmt.Errorf("unknown agent %q (known: claude, copilot, opencode, code)", name)
}

// Default returns the default agent (claude).
func Default() AgentSpec { return registry["claude"] }
