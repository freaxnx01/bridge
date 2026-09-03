package launcher

import (
	"context"

	"github.com/freaxnx01/bridge/internal/agents"
	"github.com/freaxnx01/bridge/internal/core"
)

// Plan is one prepared launch. Exactly one alternative is set; ExecPlan and
// RunPlan are the only ways to build a valid value.
//
// An exec plan replaces the caller's terminal (tmux, Windows Terminal). A run
// plan performs the launch out-of-band and returns, leaving the caller's TUI on
// screen — which is what a Herdr launch needs.
type Plan struct {
	exec []string
	run  func(context.Context) error
}

// ExecPlan is a launch that replaces the caller's terminal.
func ExecPlan(argv []string) Plan { return Plan{exec: argv} }

// RunPlan is a launch performed out-of-band; the caller stays on screen.
func RunPlan(fn func(context.Context) error) Plan { return Plan{run: fn} }

// Argv is the command to exec, or nil for a run plan.
func (p Plan) Argv() []string { return p.exec }

// Run is the func to call, or nil for an exec plan.
func (p Plan) Run() func(context.Context) error { return p.run }

// Backend starts and discovers agent sessions. It is the seam between nav and
// the multiplexer actually hosting the agents.
type Backend interface {
	// Launch prepares a launch of spec in dir under slot. It must be
	// idempotent: a slot that is already live resolves as Attach would.
	Launch(slot, dir string, spec agents.AgentSpec) (Plan, error)
	// Attach prepares focusing or attaching the existing session for slot.
	Attach(slot string) (Plan, error)
	// Live returns the currently running sessions, each carrying the slot id
	// callers match on.
	Live() ([]core.Session, error)
}

// tmuxBackend adapts the argv-based Launcher to Backend. It is the default:
// today's behaviour, routed through the new seam.
type tmuxBackend struct{ l Launcher }

// NewBackend returns the default backend for this platform (tmux on unix,
// Windows Terminal on windows).
func NewBackend() Backend { return tmuxBackend{l: New()} }

func (b tmuxBackend) Launch(slot, dir string, spec agents.AgentSpec) (Plan, error) {
	argv, err := b.l.LaunchArgv(slot, dir, spec)
	if err != nil {
		return Plan{}, err
	}
	return ExecPlan(argv), nil
}

func (b tmuxBackend) Attach(slot string) (Plan, error) {
	return ExecPlan(b.l.AttachArgv(slot)), nil
}

func (b tmuxBackend) Live() ([]core.Session, error) { return core.LiveSessions() }
