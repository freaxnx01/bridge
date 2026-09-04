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
//
// Launch and Attach are called from inside nav's Update — the Bubble Tea event
// loop — so they MUST NOT perform I/O. They only build a Plan; every subprocess
// call, network round trip or retry belongs inside that Plan's execution, which
// nav runs as a tea.Cmd. An implementation that shells out while building a
// Plan blocks every re-render and keypress, and hangs nav for as long as the
// call takes. Pure argument validation is fine and should stay synchronous, so
// nav can report it immediately.
//
// Live is the exception: it does I/O by definition, and nav only ever calls it
// from inside a tea.Cmd (loadSessionsCmd, loadDashRowsCmd).
type Backend interface {
	// Launch prepares a launch of spec in dir under slot. It must be
	// idempotent: a slot that is already live resolves as Attach would. That
	// check belongs inside the returned Plan, not here — a decision made while
	// building the Plan is already stale by the time nav runs it.
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
