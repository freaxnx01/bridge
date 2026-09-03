# nav Herdr Mode Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** When `bridge nav` runs inside a Herdr session, launch coding agents as Herdr tabs — recognized by `herdr agent list` and its idle/working/blocked lifecycle — instead of wrapping them in tmux.

**Architecture:** A `launcher.Backend` seam (three methods: `Launch`, `Attach`, `Live`) with two implementations: the existing tmux/Windows-Terminal argv path, and a new `internal/herdr` package that drives the `herdr` CLI. `cmd/bridge` picks one from the environment and injects it via `nav.Config.Backend`. A `launcher.Plan` value carries *either* an argv that replaces nav's terminal (tmux) *or* a func to run out-of-band (Herdr), so nav survives a Herdr launch.

**Tech Stack:** Go (module `github.com/freaxnx01/bridge`), Cobra CLI, Bubble Tea TUI, stdlib `testing` with hand-rolled fakes. No new module dependencies.

**Spec:** [`docs/specs/2026-09-03-nav-herdr-mode-design.md`](../specs/2026-09-03-nav-herdr-mode-design.md)

## Global Constraints

- **No new Go modules.** Everything here is stdlib plus packages already in `go.mod`. Do not add a JSON library, an assertion library, or a mocking library.
- **No Herdr server required by any test.** Every JSON payload the tests need is reproduced verbatim in this plan. Tests must pass in CI with no `herdr` binary present.
- **Hand-rolled fakes only** — no `testify`, `mockery`, `gomock`.
- **Table-driven with `t.Run` subtests** is the default test shape. Name tests `TestFunc_StateUnderTest_ExpectedBehavior`.
- **Errors return, never `panic`/`os.Exit`/print** below the command layer. Wrap with `%w`, lower-case message, no trailing punctuation.
- **No `_ = call()`** to silence an error. No `//nolint`. No `fmt.Println` debug statements.
- **`context.Context` is the first parameter** of anything doing I/O.
- **Behaviour with `HERDR_ENV` unset must not change.** Existing tests are the guard; none of them may be edited to pass.
- **Gates after every task:** `gofmt -l .` empty, `go vet ./...`, `golangci-lint run`, `go test -race ./...` — the full suite, not just the new test.
- Existing slot id shape is `core.SlotID(repo, worktree)` → `"<repo>"` or `"<repo>-wt-<worktree>"` (`internal/core/slot.go:27`). Do not change it.

---

### Task 1: `launcher.Plan` and `launcher.Backend` with the tmux implementation

Introduces the seam and routes today's tmux behaviour through it, unchanged.

**Files:**
- Create: `internal/launcher/backend.go`
- Create: `internal/launcher/backend_test.go`
- Modify: `internal/launcher/launcher.go:1-2` (package doc widens from "constructs argv" to "starts sessions")

**Interfaces:**
- Consumes: `launcher.Launcher`, `launcher.New()`, `agents.AgentSpec`, `core.Session`, `core.LiveSessions` (all existing).
- Produces: `launcher.Plan`, `launcher.ExecPlan(argv []string) Plan`, `launcher.RunPlan(fn func(context.Context) error) Plan`, `Plan.Argv() []string`, `Plan.Run() func(context.Context) error`, `launcher.Backend` interface, `launcher.NewBackend() Backend`.

- [ ] **Step 1: Write the failing test**

Create `internal/launcher/backend_test.go`:

```go
package launcher

import (
	"context"
	"testing"

	"github.com/freaxnx01/bridge/internal/agents"
)

func TestExecPlan_Argv_ReturnsArgvAndNilRun(t *testing.T) {
	p := ExecPlan([]string{"tmux", "attach-session", "-t", "bridge"})
	if got := p.Argv(); len(got) != 4 || got[0] != "tmux" {
		t.Errorf("Argv() = %v", got)
	}
	if p.Run() != nil {
		t.Error("ExecPlan must not carry a Run func")
	}
}

func TestRunPlan_Run_ReturnsFuncAndNilArgv(t *testing.T) {
	called := false
	p := RunPlan(func(context.Context) error { called = true; return nil })
	if p.Argv() != nil {
		t.Errorf("RunPlan must not carry argv, got %v", p.Argv())
	}
	run := p.Run()
	if run == nil {
		t.Fatal("RunPlan must carry a Run func")
	}
	if err := run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !called {
		t.Error("Run func was not invoked")
	}
}

func TestNewBackend_Launch_WrapsLaunchArgvUnchanged(t *testing.T) {
	b := NewBackend()
	p, err := b.Launch("bridge", "/repos/bridge", agents.AgentSpec{Name: "claude", Bin: "claude"})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	want, err := New().LaunchArgv("bridge", "/repos/bridge", agents.AgentSpec{Name: "claude", Bin: "claude"})
	if err != nil {
		t.Fatalf("LaunchArgv: %v", err)
	}
	got := p.Argv()
	if len(got) != len(want) {
		t.Fatalf("Argv() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Argv() = %v, want %v", got, want)
		}
	}
	if p.Run() != nil {
		t.Error("the tmux backend must produce an ExecPlan, not a RunPlan")
	}
}

func TestNewBackend_Attach_WrapsAttachArgvUnchanged(t *testing.T) {
	p, err := NewBackend().Attach("bridge-wt-foo")
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	want := New().AttachArgv("bridge-wt-foo")
	got := p.Argv()
	if len(got) != len(want) {
		t.Fatalf("Argv() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Argv() = %v, want %v", got, want)
		}
	}
}

func TestNewBackend_Launch_EmptySlotIsAnError(t *testing.T) {
	if _, err := NewBackend().Launch("", "/repos/bridge", agents.AgentSpec{Bin: "claude"}); err == nil {
		t.Error("expected an error for an empty slot")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/launcher/ -run 'TestExecPlan|TestRunPlan|TestNewBackend' -v`
Expected: FAIL to build — `undefined: ExecPlan`, `undefined: RunPlan`, `undefined: NewBackend`.

- [ ] **Step 3: Write the minimal implementation**

Create `internal/launcher/backend.go`:

```go
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
```

- [ ] **Step 4: Widen the package doc**

Modify `internal/launcher/launcher.go:1-2`, replacing the two doc lines:

```go
// Package launcher starts agent sessions. It constructs the argv that a parent
// shell should exec to land the user inside a tmux (or Windows Terminal)
// session, and defines the Backend seam that lets another multiplexer — see
// internal/herdr — host those sessions instead.
package launcher
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/launcher/ -v`
Expected: PASS, including the pre-existing `tmux_test.go` cases.

- [ ] **Step 6: Run the gates**

Run: `gofmt -l . && go vet ./... && golangci-lint run && go test -race ./...`
Expected: no `gofmt` output, no vet or lint findings, all tests pass.

- [ ] **Step 7: Commit**

```bash
git add internal/launcher/backend.go internal/launcher/backend_test.go internal/launcher/launcher.go
git commit -m "feat(launcher): add Plan and Backend seam with the tmux implementation"
```

---

### Task 2: nav launches through `cfg.Backend`

Replaces nav's two direct `launcher.New()` calls and two `core.LiveSessions()` calls with the injected backend, and teaches nav to run a `RunPlan` without giving up its terminal.

**Files:**
- Modify: `internal/nav/types.go` (add `Backend` to `Config`)
- Modify: `internal/nav/model.go:61-78` (`initialModel` defaults the backend)
- Modify: `internal/nav/update.go:871-901` (`launchPlan`), `:911-925` (`launchRow`), `:436-441` (picker attach), `:994-1001` (`execArgvCmd` → `runPlanCmd`)
- Modify: `internal/nav/data.go:51-56` (`loadSessionsCmd`), `:118-126` (`loadDashRowsCmd`)
- Create: `internal/nav/backend_test.go`

**Interfaces:**
- Consumes: `launcher.Backend`, `launcher.Plan`, `launcher.ExecPlan`, `launcher.RunPlan`, `launcher.NewBackend` (Task 1).
- Produces: `nav.Config.Backend launcher.Backend`; `runPlanCmd(p launcher.Plan) tea.Cmd`; `Model.launchPlan(row dashRow) (launcher.Plan, string, string, error)` — plan, slot, agent, err; `loadSessionsCmd(b launcher.Backend, slotsPath string) tea.Cmd`; `loadDashRowsCmd(b launcher.Backend, repo core.Repo, slotsPath string) tea.Cmd`.

- [ ] **Step 1: Write the failing test**

Create `internal/nav/backend_test.go`:

```go
package nav

import (
	"context"
	"errors"
	"testing"

	"github.com/freaxnx01/bridge/internal/agents"
	"github.com/freaxnx01/bridge/internal/core"
	"github.com/freaxnx01/bridge/internal/launcher"
)

// fakeBackend is a hand-rolled launcher.Backend recording what nav asked for.
type fakeBackend struct {
	plan       launcher.Plan
	attachPlan launcher.Plan
	sessions   []core.Session
	err        error

	launchedSlot string
	launchedDir  string
	launchedSpec agents.AgentSpec
	attachedSlot string
}

func (f *fakeBackend) Launch(slot, dir string, spec agents.AgentSpec) (launcher.Plan, error) {
	f.launchedSlot, f.launchedDir, f.launchedSpec = slot, dir, spec
	if f.err != nil {
		return launcher.Plan{}, f.err
	}
	return f.plan, nil
}

func (f *fakeBackend) Attach(slot string) (launcher.Plan, error) {
	f.attachedSlot = slot
	if f.err != nil {
		return launcher.Plan{}, f.err
	}
	return f.attachPlan, nil
}

func (f *fakeBackend) Live() ([]core.Session, error) { return f.sessions, f.err }

func TestInitialModel_NilBackend_DefaultsToTmuxBackend(t *testing.T) {
	m := initialModel(Config{})
	if m.cfg.Backend == nil {
		t.Fatal("initialModel must substitute the default backend for a nil Config.Backend")
	}
	p, err := m.cfg.Backend.Attach("bridge")
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	if argv := p.Argv(); len(argv) == 0 || argv[0] != "tmux" {
		t.Errorf("default backend should produce a tmux exec plan, got %v", argv)
	}
}

func TestLaunchPlan_RowWithLiveSession_AsksBackendToAttach(t *testing.T) {
	fb := &fakeBackend{attachPlan: launcher.ExecPlan([]string{"tmux", "attach-session", "-t", "bridge"})}
	m := initialModel(Config{Backend: fb})
	m.repo = core.Repo{Name: "bridge", Path: "/repos/bridge"}

	_, slot, _, err := m.launchPlan(dashRow{hasSession: true, slotID: "bridge", path: "/repos/bridge"})
	if err != nil {
		t.Fatalf("launchPlan: %v", err)
	}
	if fb.attachedSlot != "bridge" {
		t.Errorf("attached slot = %q, want %q", fb.attachedSlot, "bridge")
	}
	if slot != "" {
		t.Errorf("an attach must report slot == \"\" so no slot is re-registered, got %q", slot)
	}
}

func TestLaunchPlan_RowWithoutSession_AsksBackendToLaunchWithSlotAndDir(t *testing.T) {
	fb := &fakeBackend{plan: launcher.RunPlan(func(context.Context) error { return nil })}
	m := initialModel(Config{Backend: fb, DefaultAgent: "claude"})
	m.repo = core.Repo{Name: "bridge", Path: "/repos/bridge"}

	_, slot, agent, err := m.launchPlan(dashRow{worktree: "foo", path: "/repos/bridge/.worktrees/foo"})
	if err != nil {
		t.Fatalf("launchPlan: %v", err)
	}
	if fb.launchedSlot != "bridge-wt-foo" {
		t.Errorf("launched slot = %q, want %q", fb.launchedSlot, "bridge-wt-foo")
	}
	if fb.launchedDir != "/repos/bridge/.worktrees/foo" {
		t.Errorf("launched dir = %q", fb.launchedDir)
	}
	if fb.launchedSpec.Bin != "claude" {
		t.Errorf("launched spec Bin = %q, want claude", fb.launchedSpec.Bin)
	}
	if slot != "bridge-wt-foo" || agent != "claude" {
		t.Errorf("slot/agent = %q/%q", slot, agent)
	}
}

func TestLaunchPlan_BackendError_Propagates(t *testing.T) {
	fb := &fakeBackend{err: errors.New("boom")}
	m := initialModel(Config{Backend: fb, DefaultAgent: "claude"})
	m.repo = core.Repo{Name: "bridge", Path: "/repos/bridge"}
	if _, _, _, err := m.launchPlan(dashRow{worktree: "foo", path: "/x"}); err == nil {
		t.Error("expected the backend error to propagate")
	}
}

func TestRunPlanCmd_RunPlan_InvokesFuncAndReportsDone(t *testing.T) {
	called := false
	cmd := runPlanCmd(launcher.RunPlan(func(context.Context) error { called = true; return nil }))
	if cmd == nil {
		t.Fatal("runPlanCmd returned nil for a run plan")
	}
	msg := cmd()
	if !called {
		t.Error("the run func was never invoked")
	}
	done, ok := msg.(execDoneMsg)
	if !ok {
		t.Fatalf("msg = %T, want execDoneMsg", msg)
	}
	if done.err != nil {
		t.Errorf("execDoneMsg.err = %v, want nil", done.err)
	}
}

func TestRunPlanCmd_RunPlanError_ReportsErrorInMsg(t *testing.T) {
	cmd := runPlanCmd(launcher.RunPlan(func(context.Context) error { return errors.New("nope") }))
	done, ok := cmd().(execDoneMsg)
	if !ok {
		t.Fatal("want execDoneMsg")
	}
	if done.err == nil {
		t.Error("expected the run error to travel in execDoneMsg")
	}
}

func TestRunPlanCmd_EmptyPlan_ReturnsNil(t *testing.T) {
	if runPlanCmd(launcher.Plan{}) != nil {
		t.Error("an empty plan has nothing to run")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/nav/ -run 'TestInitialModel_NilBackend|TestLaunchPlan_|TestRunPlanCmd_' -v`
Expected: FAIL to build — `unknown field Backend in struct literal`, `undefined: runPlanCmd`, and `launchPlan` returning four values of the wrong types.

- [ ] **Step 3: Add the `Config` field**

In `internal/nav/types.go`, inside `type Config struct`, after the `AgentArgs` field, add:

```go
	// Backend is the session backend nav launches into and reads live sessions
	// from. Nil selects the tmux/Windows-Terminal default. Injected by
	// cmd/bridge so internal/nav stays free of backend selection.
	Backend launcher.Backend
```

Add `"github.com/freaxnx01/bridge/internal/launcher"` to that file's import block.

- [ ] **Step 4: Default the backend in `initialModel`**

In `internal/nav/model.go:61-78`, immediately after the `func initialModel(cfg Config) Model {` line, add:

```go
	if cfg.Backend == nil {
		cfg.Backend = launcher.NewBackend()
	}
```

Add the `launcher` import to `model.go`. Every later reference to `m.cfg.Backend` is now non-nil.

- [ ] **Step 5: Convert `launchPlan` to return a `Plan`**

Replace `internal/nav/update.go:871-901` in full:

```go
// launchPlan decides attach-vs-launch for a row. For a new session it returns
// the slot to register; for an attach it returns slot == "".
func (m Model) launchPlan(row dashRow) (plan launcher.Plan, slot, agent string, err error) {
	b := m.cfg.Backend
	if row.hasSession && row.slotID != "" {
		plan, err = b.Attach(row.slotID)
		return plan, "", "", err
	}
	agent = m.cfg.DefaultAgent
	if agent == "" {
		agent = "claude"
	}
	spec, err := agents.Resolve(agent)
	if err != nil {
		return launcher.Plan{}, "", "", err
	}
	if m.cfg.NameArgs != nil {
		if na := m.cfg.NameArgs(agent, m.repo, row.worktree, row.displayLabel); len(na) > 0 {
			spec.Args = append(append([]string{}, na...), spec.Args...)
		}
	}
	if len(m.cfg.AgentArgs) > 0 {
		spec.Args = append(append([]string{}, spec.Args...), m.cfg.AgentArgs...)
	}
	slot = core.SlotID(m.repo.Name, row.worktree)
	// The backend decides whether this replaces nav's terminal (tmux nests
	// directly via `new-session -A`; $TMUX is cleared in runPlanCmd) or runs
	// out-of-band leaving nav on screen (Herdr).
	plan, err = b.Launch(slot, row.path, spec)
	if err != nil {
		return launcher.Plan{}, "", "", err
	}
	return plan, slot, agent, nil
}
```

- [ ] **Step 6: Point `launchArgvFor` and `launchRow` at the plan**

Replace `internal/nav/update.go:903-925` (the `launchArgvFor` and `launchRow` funcs) in full:

```go
// launchArgvFor returns the argv of an exec plan for a row, for tests and
// callers that assert on the tmux command line. A run plan has no argv.
func (m Model) launchArgvFor(row dashRow) ([]string, error) {
	plan, _, _, err := m.launchPlan(row)
	return plan.Argv(), err
}

func (m Model) launchRow(row dashRow) (tea.Model, tea.Cmd) {
	plan, slot, agent, err := m.launchPlan(row)
	if err != nil {
		m.status = err.Error()
		return m, nil
	}
	exe := runPlanCmd(plan)
	if slot == "" {
		return m, exe // attaching an already-registered session
	}
	reg := registerSlotCmd(m.cfg.SlotsPath, core.Slot{
		ID: slot, Repo: m.repo.Name, Worktree: row.worktree, Agent: agent, Created: time.Now().UTC(),
	})
	return m, tea.Sequence(reg, exe)
}
```

- [ ] **Step 7: Replace `execArgvCmd` with `runPlanCmd`**

Replace `internal/nav/update.go:992-1001` in full:

```go
// runPlanCmd turns a launcher.Plan into a tea.Cmd. An exec plan runs through
// tea.ExecProcess with $TMUX cleared, so a nested tmux attach is permitted and
// nav's terminal is handed over. A run plan executes out-of-band and nav stays
// rendered — the Herdr path, where the agent lands in its own tab.
func runPlanCmd(plan launcher.Plan) tea.Cmd {
	if run := plan.Run(); run != nil {
		return func() tea.Msg { return execDoneMsg{err: run(context.Background())} }
	}
	argv := plan.Argv()
	if len(argv) == 0 {
		return nil
	}
	c := exec.Command(argv[0], argv[1:]...)
	c.Env = tmuxUnset(os.Environ())
	return tea.ExecProcess(c, func(err error) tea.Msg { return execDoneMsg{err: err} })
}
```

Add `"context"` to `update.go`'s imports. Keep the existing `launcher` import
(`internal/nav/update.go:15`) — `launchPlan` now names `launcher.Plan` in its
signature, so it is still used.

- [ ] **Step 8: Route the picker attach through the backend**

Replace `internal/nav/update.go:436-441` (the `case "enter":` body inside the `focusSessions` block):

```go
		case "enter":
			if m.sessionSel >= 0 && m.sessionSel < len(m.sessions) {
				if sl := m.sessions[m.sessionSel].slotID; sl != "" {
					plan, err := m.cfg.Backend.Attach(sl)
					if err != nil {
						m.status = err.Error()
						return m, nil
					}
					return m, runPlanCmd(plan)
				}
			}
```

- [ ] **Step 9: Take the backend in both session loaders**

In `internal/nav/data.go:51-56`, change the signature and the `core.LiveSessions()` call:

```go
func loadSessionsCmd(b launcher.Backend, slotsPath string) tea.Cmd {
	return func() tea.Msg {
		live, _ := b.Live()
		slots, _ := core.LoadSlots(slotsPath)
		return sessionsMsg{rows: buildSessionRows(live, slots, time.Now())}
	}
}
```

In `internal/nav/data.go:118-126`, likewise:

```go
func loadDashRowsCmd(b launcher.Backend, repo core.Repo, slotsPath string) tea.Cmd {
	return func() tea.Msg {
		wts, _ := worktree.List(worktree.ExecRunner{}, repo.Path)
		primary, _ := worktree.Primary(worktree.ExecRunner{}, repo.Path)
		slots, _ := core.LoadSlots(slotsPath)
		live, _ := b.Live()
		return dashRowsMsg{rows: buildDashRows(repo, primary.Branch, wts, slots, live, time.Now())}
	}
}
```

Add the `launcher` import to `data.go`. Then update every call site the compiler flags — `loadSessionsCmd(m.cfg.SlotsPath)` becomes `loadSessionsCmd(m.cfg.Backend, m.cfg.SlotsPath)` (including `Init()` at `internal/nav/model.go:84`), and `loadDashRowsCmd(repo, m.cfg.SlotsPath)` becomes `loadDashRowsCmd(m.cfg.Backend, repo, m.cfg.SlotsPath)`.

- [ ] **Step 10: Run the new tests**

Run: `go test ./internal/nav/ -run 'TestInitialModel_NilBackend|TestLaunchPlan_|TestRunPlanCmd_' -v`
Expected: PASS.

- [ ] **Step 11: Run the full nav suite**

Run: `go test ./internal/nav/ -v`
Expected: PASS. The pre-existing `launch_test.go` and `flow_test.go` cases must pass **without being edited** — they go through `launchArgvFor`, which still yields the tmux argv. If one fails, the bug is in this task's code, not in the test.

- [ ] **Step 12: Run the gates**

Run: `gofmt -l . && go vet ./... && golangci-lint run && go test -race ./...`
Expected: clean.

- [ ] **Step 13: Commit**

```bash
git add internal/nav/
git commit -m "refactor(nav): launch and discover sessions through launcher.Backend"
```

---

### Task 3: `internal/herdr` — CLI runner seam and response decoding

The typed decode layer, built entirely from real captured payloads. No Herdr server needed at any point.

**Files:**
- Create: `internal/herdr/herdr.go`
- Create: `internal/herdr/decode_test.go`
- Create: `internal/herdr/testdata/agent_list.json`
- Create: `internal/herdr/testdata/agent_list_empty.json`
- Create: `internal/herdr/testdata/tab_create.json`
- Create: `internal/herdr/testdata/error_server.json`

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces: `herdr.Runner` (`func(ctx context.Context, args ...string) ([]byte, error)`); `herdr.Client` with field `Run Runner` and `Workspace string`; `herdr.New() *Client`; types `agentInfo` (fields `Agent`, `AgentStatus`, `Cwd`, `PaneID`, `TabID`), `tabCreated` (fields `PaneID`, `TabID`, `Label`); methods `(*Client).agentList(ctx) ([]agentInfo, error)` and `(*Client).tabCreate(ctx, dir, label string) (tabCreated, error)`; sentinel `herdr.ErrNoSession`.

- [ ] **Step 1: Write the fixtures**

Create `internal/herdr/testdata/agent_list.json` — captured verbatim from the live CLI:

```json
{"id":"cli:agent:list","result":{"agents":[{"agent":"claude","agent_status":"working","cwd":"/home/admin/repos/github/freaxnx01/public/bridge","focused":true,"foreground_cwd":"/home/admin/repos/github/freaxnx01/public/bridge","pane_id":"w3:p1","revision":7,"state_change_seq":18,"tab_id":"w3:t1","terminal_id":"term_65a92a97be0874","workspace_id":"w3"},{"agent":"claude","agent_status":"blocked","cwd":"/home/admin/repos/github/freaxnx01/public/bridge/.worktrees/foo","focused":false,"foreground_cwd":"/home/admin/repos/github/freaxnx01/public/bridge/.worktrees/foo","pane_id":"w3:p4","revision":2,"state_change_seq":9,"tab_id":"w3:t2","terminal_id":"term_65a92a97be0999","workspace_id":"w3"}],"type":"agent_list"}}
```

Create `internal/herdr/testdata/agent_list_empty.json`:

```json
{"id":"cli:agent:list","result":{"agents":[],"type":"agent_list"}}
```

Create `internal/herdr/testdata/tab_create.json` — captured verbatim:

```json
{"id":"cli:tab:create","result":{"root_pane":{"agent_status":"unknown","cwd":"/home/admin/repos/github/freaxnx01/public/bridge","focused":false,"foreground_cwd":"/home/admin/repos/github/freaxnx01/public/bridge","pane_id":"w3:p6","revision":0,"tab_id":"w3:t4","terminal_id":"term_65a942c66bccdb","workspace_id":"w3"},"tab":{"agent_status":"unknown","focused":false,"label":"bridge-fixture-probe","number":4,"pane_count":1,"tab_id":"w3:t4","workspace_id":"w3"},"type":"tab_created"}}
```

Create `internal/herdr/testdata/error_server.json` — the exit-1 server-error shape:

```json
{"id":"cli:agent:start","error":{"code":"agent_not_ready","message":"agent did not reach a ready state"}}
```

Note the second entry in `agent_list.json` is a synthesized second agent (a `blocked` one in a worktree). Its *shape* is the captured shape; only the values differ, which is what the tests need in order to exercise more than one row.

- [ ] **Step 2: Write the failing test**

Create `internal/herdr/decode_test.go`:

```go
package herdr

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// fixtureRunner returns a Runner replaying the named testdata file, and
// records the argv it was called with.
func fixtureRunner(t *testing.T, name string) (Runner, *[][]string) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var calls [][]string
	return func(_ context.Context, args ...string) ([]byte, error) {
		calls = append(calls, args)
		return b, nil
	}, &calls
}

func TestAgentList_TwoAgents_DecodesCwdStatusAndIDs(t *testing.T) {
	run, calls := fixtureRunner(t, "agent_list.json")
	c := &Client{Run: run, Workspace: "w3"}

	got, err := c.agentList(context.Background())
	if err != nil {
		t.Fatalf("agentList: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].Cwd != "/home/admin/repos/github/freaxnx01/public/bridge" {
		t.Errorf("Cwd = %q", got[0].Cwd)
	}
	if got[0].AgentStatus != "working" || got[1].AgentStatus != "blocked" {
		t.Errorf("statuses = %q/%q", got[0].AgentStatus, got[1].AgentStatus)
	}
	if got[1].TabID != "w3:t2" || got[1].PaneID != "w3:p4" {
		t.Errorf("ids = %q/%q", got[1].TabID, got[1].PaneID)
	}
	if got[0].Agent != "claude" {
		t.Errorf("Agent = %q", got[0].Agent)
	}
	if len(*calls) != 1 {
		t.Fatalf("expected exactly one CLI call, got %d", len(*calls))
	}
	if a := (*calls)[0]; len(a) < 2 || a[0] != "agent" || a[1] != "list" {
		t.Errorf("argv = %v, want [agent list ...]", a)
	}
}

func TestAgentList_NoAgents_ReturnsEmptyNotError(t *testing.T) {
	run, _ := fixtureRunner(t, "agent_list_empty.json")
	got, err := (&Client{Run: run}).agentList(context.Background())
	if err != nil {
		t.Fatalf("agentList: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("len = %d, want 0", len(got))
	}
}

func TestTabCreate_Success_DecodesRootPaneAndTabIDs(t *testing.T) {
	run, calls := fixtureRunner(t, "tab_create.json")
	c := &Client{Run: run, Workspace: "w3"}

	got, err := c.tabCreate(context.Background(), "/repos/bridge", "bridge")
	if err != nil {
		t.Fatalf("tabCreate: %v", err)
	}
	if got.PaneID != "w3:p6" {
		t.Errorf("PaneID = %q, want w3:p6", got.PaneID)
	}
	if got.TabID != "w3:t4" {
		t.Errorf("TabID = %q, want w3:t4", got.TabID)
	}
	if got.Label != "bridge-fixture-probe" {
		t.Errorf("Label = %q", got.Label)
	}
	argv := (*calls)[0]
	if !containsPair(argv, "--workspace", "w3") {
		t.Errorf("argv %v must pin --workspace w3", argv)
	}
	if !containsPair(argv, "--cwd", "/repos/bridge") {
		t.Errorf("argv %v must pass --cwd", argv)
	}
	if !containsPair(argv, "--label", "bridge") {
		t.Errorf("argv %v must pass --label", argv)
	}
	if !contains(argv, "--no-focus") {
		t.Errorf("argv %v must pass --no-focus so the user's focus is preserved", argv)
	}
}

func TestCall_ServerErrorEnvelope_MapsAgentNotReadyToItsSentinel(t *testing.T) {
	b, err := os.ReadFile(filepath.Join("testdata", "error_server.json"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	run := func(context.Context, ...string) ([]byte, error) {
		return b, &ExitError{Code: 1}
	}
	_, gotErr := (&Client{Run: run}).agentList(context.Background())
	if gotErr == nil {
		t.Fatal("expected an error")
	}
	if !errors.Is(gotErr, ErrAgentNotReady) {
		t.Errorf("err = %v, want it to match ErrAgentNotReady", gotErr)
	}
}

func TestCall_ExitTwo_IsReportedAsABridgeBug(t *testing.T) {
	run := func(context.Context, ...string) ([]byte, error) {
		return []byte("unknown flag: --nope\n"), &ExitError{Code: 2}
	}
	_, err := (&Client{Run: run}).agentList(context.Background())
	if err == nil {
		t.Fatal("expected an error")
	}
	if !errors.Is(err, ErrCLIUsage) {
		t.Errorf("exit 2 must map to ErrCLIUsage (a bridge bug), got %v", err)
	}
}

func contains(argv []string, want string) bool {
	for _, a := range argv {
		if a == want {
			return true
		}
	}
	return false
}

func containsPair(argv []string, flag, value string) bool {
	for i := 0; i < len(argv)-1; i++ {
		if argv[i] == flag && argv[i+1] == value {
			return true
		}
	}
	return false
}
```

- [ ] **Step 3: Run the test to verify it fails**

Run: `go test ./internal/herdr/ -v`
Expected: FAIL to build — the `herdr` package does not exist.

- [ ] **Step 4: Write the minimal implementation**

Create `internal/herdr/herdr.go`:

```go
// Package herdr starts and discovers agent sessions in a Herdr session, via
// the herdr CLI. It implements launcher.Backend, so nav can host agents as
// Herdr tabs — recognized by `herdr agent list` and its lifecycle states —
// instead of wrapping them in tmux.
package herdr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Sentinel errors callers match with errors.Is.
var (
	// ErrNoSession reports that no live agent matches the requested slot.
	ErrNoSession = errors.New("herdr: no live session for slot")
	// ErrAgentNotReady reports that an agent launched but is blocked on a
	// prompt or dialog. The agent exists; it needs user input.
	ErrAgentNotReady = errors.New("herdr: agent started but is not ready")
	// ErrCLIUsage reports a malformed command line — a bridge bug, not a
	// Herdr outage.
	ErrCLIUsage = errors.New("herdr: cli usage error")
)

// ExitError carries a herdr CLI exit status. Exit 1 is a server error whose
// body is a JSON envelope; exit 2 is a CLI syntax error.
type ExitError struct {
	Code int
	Err  error
}

func (e *ExitError) Error() string { return fmt.Sprintf("herdr: exit %d", e.Code) }
func (e *ExitError) Unwrap() error { return e.Err }

// Runner executes a herdr CLI subcommand and returns its stdout. Injected so
// the backend is testable without a Herdr server.
type Runner func(ctx context.Context, args ...string) ([]byte, error)

// Client talks to the running Herdr server through the CLI.
type Client struct {
	Run Runner
	// Workspace pins every created tab to nav's own workspace, so a tab never
	// lands in whichever workspace another Herdr client happens to focus.
	Workspace string
	// retryDelay is the base backoff between `agent start` attempts while a
	// freshly created pane is still running shell init. Zero means the default
	// (see defaultRetryDelay); tests set it small.
	retryDelay time.Duration
}

// New returns a Client driving the herdr binary named by $HERDR_BIN_PATH,
// falling back to "herdr" on $PATH, pinned to $HERDR_WORKSPACE_ID.
func New() *Client {
	bin := os.Getenv("HERDR_BIN_PATH")
	if bin == "" {
		bin = "herdr"
	}
	return &Client{
		Workspace: os.Getenv("HERDR_WORKSPACE_ID"),
		Run: func(ctx context.Context, args ...string) ([]byte, error) {
			out, err := exec.CommandContext(ctx, bin, args...).Output()
			if err == nil {
				return out, nil
			}
			var ee *exec.ExitError
			if errors.As(err, &ee) {
				// Server errors put their JSON envelope on stderr.
				body := out
				if len(body) == 0 {
					body = ee.Stderr
				}
				return body, &ExitError{Code: ee.ExitCode(), Err: err}
			}
			return nil, fmt.Errorf("herdr: run %s: %w", strings.Join(args, " "), err)
		},
	}
}

// envelope is the shared herdr CLI response shape.
type envelope struct {
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// agentInfo is one entry of `herdr agent list`. Agent is empty for a pane with
// no agent, which is how a real agent is told apart from an idle shell — a
// bare pane also reports AgentStatus "unknown".
type agentInfo struct {
	Agent       string `json:"agent"`
	AgentStatus string `json:"agent_status"`
	Cwd         string `json:"cwd"`
	PaneID      string `json:"pane_id"`
	TabID       string `json:"tab_id"`
}

// tabCreated is the useful subset of `herdr tab create`.
type tabCreated struct {
	PaneID string
	TabID  string
	Label  string
}

// call runs one subcommand and unwraps its envelope into out.
func (c *Client) call(ctx context.Context, out any, args ...string) error {
	body, runErr := c.Run(ctx, args...)
	var ex *ExitError
	if runErr != nil && !errors.As(runErr, &ex) {
		return runErr
	}
	if ex != nil && ex.Code == 2 {
		return fmt.Errorf("%w: %s: %s", ErrCLIUsage, strings.Join(args, " "), strings.TrimSpace(string(body)))
	}
	var env envelope
	if err := json.Unmarshal(body, &env); err != nil {
		if ex != nil {
			return fmt.Errorf("herdr: %s failed: %w", strings.Join(args, " "), ex)
		}
		return fmt.Errorf("herdr: decode %s: %w", strings.Join(args, " "), err)
	}
	if env.Error != nil {
		if env.Error.Code == "agent_not_ready" {
			return fmt.Errorf("%w: %s", ErrAgentNotReady, env.Error.Message)
		}
		return fmt.Errorf("herdr: %s: %s (%s)", strings.Join(args, " "), env.Error.Message, env.Error.Code)
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(env.Result, out); err != nil {
		return fmt.Errorf("herdr: decode %s result: %w", strings.Join(args, " "), err)
	}
	return nil
}

func (c *Client) agentList(ctx context.Context) ([]agentInfo, error) {
	var res struct {
		Agents []agentInfo `json:"agents"`
	}
	if err := c.call(ctx, &res, "agent", "list"); err != nil {
		return nil, err
	}
	return res.Agents, nil
}

func (c *Client) tabCreate(ctx context.Context, dir, label string) (tabCreated, error) {
	var res struct {
		RootPane struct {
			PaneID string `json:"pane_id"`
			TabID  string `json:"tab_id"`
		} `json:"root_pane"`
		Tab struct {
			TabID string `json:"tab_id"`
			Label string `json:"label"`
		} `json:"tab"`
	}
	args := []string{"tab", "create"}
	if c.Workspace != "" {
		args = append(args, "--workspace", c.Workspace)
	}
	args = append(args, "--cwd", dir, "--label", label, "--no-focus")
	if err := c.call(ctx, &res, args...); err != nil {
		return tabCreated{}, err
	}
	if res.RootPane.PaneID == "" {
		return tabCreated{}, errors.New("herdr: tab create returned no root pane")
	}
	return tabCreated{PaneID: res.RootPane.PaneID, TabID: res.Tab.TabID, Label: res.Tab.Label}, nil
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/herdr/ -v`
Expected: PASS, all six cases.

The two error cases are named `TestCall_…` because they exercise the shared
`call` envelope handling via `agentList`, not anything specific to that
subcommand.

- [ ] **Step 6: Run the gates**

Run: `gofmt -l . && go vet ./... && golangci-lint run && go test -race ./...`
Expected: clean.

- [ ] **Step 7: Commit**

```bash
git add internal/herdr/
git commit -m "feat(herdr): add CLI client with typed response decoding"
```

---

### Task 4: `SlotIDForPath` — map an agent's cwd to a bridge slot id

**Files:**
- Create: `internal/herdr/path.go`
- Create: `internal/herdr/path_test.go`

**Interfaces:**
- Consumes: `core.SlotID` (`internal/core/slot.go:27`).
- Produces: `herdr.SlotIDForPath(cwd string) string`.

- [ ] **Step 1: Write the failing test**

Create `internal/herdr/path_test.go`:

```go
package herdr

import "testing"

func TestSlotIDForPath_MapsBridgeLayoutToSlotIDs(t *testing.T) {
	tests := []struct {
		name string
		cwd  string
		want string
	}{
		{"repo root", "/home/a/repos/github/freaxnx01/public/bridge", "bridge"},
		{"worktree", "/home/a/repos/github/freaxnx01/public/bridge/.worktrees/foo", "bridge-wt-foo"},
		{"trailing slash on repo root", "/home/a/repos/x/bridge/", "bridge"},
		{"trailing slash on worktree", "/home/a/repos/x/bridge/.worktrees/foo/", "bridge-wt-foo"},
		{"uppercase repo name is preserved", "/home/a/repos/x/BI_ExportSQLiteToCsv", "BI_ExportSQLiteToCsv"},
		{"nested dir inside a repo is not the repo", "/home/a/repos/x/bridge/internal/nav", "nav"},
		{"empty", "", ""},
		{"root", "/", ""},
		{"empty worktree name falls back to the repo", "/home/a/repos/x/bridge/.worktrees", "bridge"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SlotIDForPath(tt.cwd); got != tt.want {
				t.Errorf("SlotIDForPath(%q) = %q, want %q", tt.cwd, got, tt.want)
			}
		})
	}
}
```

Note the `nested dir inside a repo` case: the inversion is layout-based, so a cwd deeper inside a repo maps to that directory's basename, not to the repo. This is accepted and documented — such a slot id simply matches no dashboard row. Do **not** try to walk up looking for a `.git`; `Live()` must stay a pure, filesystem-free mapping so it is testable and fast.

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/herdr/ -run TestSlotIDForPath -v`
Expected: FAIL to build — `undefined: SlotIDForPath`.

- [ ] **Step 3: Write the minimal implementation**

Create `internal/herdr/path.go`:

```go
package herdr

import (
	"path/filepath"
	"strings"

	"github.com/freaxnx01/bridge/internal/core"
)

// worktreeDir is the fixed directory bridge creates worktrees in (CLAUDE.md).
const worktreeDir = ".worktrees"

// SlotIDForPath maps an agent's working directory to the bridge slot id that
// would have launched it, inverting bridge's own layout:
//
//	/…/<repo>                   -> "<repo>"
//	/…/<repo>/.worktrees/<wt>   -> "<repo>-wt-<wt>"
//
// It is a pure function of the path — it never touches the filesystem — so a
// directory deeper inside a repo maps to that directory's own basename and
// simply matches no dashboard row. Returns "" for a path with no usable
// basename.
func SlotIDForPath(cwd string) string {
	clean := filepath.Clean(strings.TrimSpace(cwd))
	if clean == "" || clean == "." || clean == string(filepath.Separator) {
		return ""
	}
	base := filepath.Base(clean)
	parent := filepath.Dir(clean)
	if filepath.Base(parent) == worktreeDir {
		return core.SlotID(filepath.Base(filepath.Dir(parent)), base)
	}
	if base == worktreeDir {
		return core.SlotID(filepath.Base(parent), "")
	}
	return core.SlotID(base, "")
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/herdr/ -run TestSlotIDForPath -v`
Expected: PASS, all nine subtests.

- [ ] **Step 5: Run the gates**

Run: `gofmt -l . && go vet ./... && golangci-lint run && go test -race ./...`
Expected: clean.

- [ ] **Step 6: Commit**

```bash
git add internal/herdr/path.go internal/herdr/path_test.go
git commit -m "feat(herdr): map an agent cwd to a bridge slot id"
```

---

### Task 5: `agentName` — a slot id sanitized into a legal Herdr agent name

Herdr agent names must match `[a-z][a-z0-9_-]{0,31}` and be unique among live agents. Bridge slot ids satisfy neither.

**Files:**
- Create: `internal/herdr/name.go`
- Create: `internal/herdr/name_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `herdr.agentName(slot string, taken []string) string` (package-private; used by `Launch` in Task 8).

- [ ] **Step 1: Write the failing test**

Create `internal/herdr/name_test.go`:

```go
package herdr

import (
	"regexp"
	"testing"
)

var legalName = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)

func TestAgentName_SanitizesToALegalHerdrName(t *testing.T) {
	tests := []struct {
		name  string
		slot  string
		taken []string
		want  string
	}{
		{"already legal", "bridge", nil, "bridge"},
		{"uppercase is lowered", "Avaloq", nil, "avaloq"},
		{"underscores survive", "BI_ExportSQLiteToCsv", nil, "bi_exportsqlitetocsv"},
		{"worktree slot", "bridge-wt-foo", nil, "bridge-wt-foo"},
		{"dots become dashes", "my.repo.name", nil, "my-repo-name"},
		{"leading digit gets a prefix", "3d-engine", nil, "a-3d-engine"},
		{"runs of separators collapse", "a..--b", nil, "a-b"},
		{"trailing separators are trimmed", "repo--", nil, "repo"},
		{
			"over 32 chars is truncated",
			"quilvest-archiverestapi-wt-featurebranch",
			nil,
			"quilvest-archiverestapi-wt-featu",
		},
		{
			"collision gets a numeric suffix",
			"bridge",
			[]string{"bridge"},
			"bridge-2",
		},
		{
			"repeated collisions keep counting",
			"bridge",
			[]string{"bridge", "bridge-2"},
			"bridge-3",
		},
		{
			"a suffix on a max-length name still fits in 32",
			"quilvest-archiverestapi-wt-featurebranch",
			[]string{"quilvest-archiverestapi-wt-featu"},
			"quilvest-archiverestapi-wt-fea-2",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := agentName(tt.slot, tt.taken)
			if got != tt.want {
				t.Errorf("agentName(%q, %v) = %q, want %q", tt.slot, tt.taken, got, tt.want)
			}
			if !legalName.MatchString(got) {
				t.Errorf("%q is not a legal herdr agent name", got)
			}
		})
	}
}

func TestAgentName_EmptySlot_StillProducesALegalName(t *testing.T) {
	got := agentName("", nil)
	if !legalName.MatchString(got) {
		t.Errorf("agentName(\"\") = %q, which is not a legal herdr agent name", got)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/herdr/ -run TestAgentName -v`
Expected: FAIL to build — `undefined: agentName`.

- [ ] **Step 3: Write the minimal implementation**

Create `internal/herdr/name.go`:

```go
package herdr

import (
	"fmt"
	"strings"
)

// maxNameLen is Herdr's agent-name limit: [a-z][a-z0-9_-]{0,31}.
const maxNameLen = 32

// agentName derives a legal, unique Herdr agent name from a bridge slot id.
// Herdr requires [a-z][a-z0-9_-]{0,31} and uniqueness among live agents, which
// slot ids do not guarantee: repo names carry uppercase, and a repo plus a
// worktree easily exceeds 32 characters.
//
// taken is the set of live agent names to avoid; on collision the name is
// truncated further and given a "-N" suffix so the total still fits.
func agentName(slot string, taken []string) string {
	base := sanitizeName(slot)
	if !isTaken(base, taken) {
		return base
	}
	for n := 2; ; n++ {
		suffix := fmt.Sprintf("-%d", n)
		trimmed := strings.TrimRight(clip(base, maxNameLen-len(suffix)), "-_")
		candidate := trimmed + suffix
		if !isTaken(candidate, taken) {
			return candidate
		}
	}
}

// sanitizeName lowercases, replaces illegal runes with "-", collapses runs,
// trims separators, guarantees a leading letter, and clips to the limit.
func sanitizeName(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	out := collapse(b.String())
	out = strings.Trim(out, "-_")
	if out == "" {
		return "agent"
	}
	if first := out[0]; first < 'a' || first > 'z' {
		out = "a-" + out
	}
	return strings.TrimRight(clip(out, maxNameLen), "-_")
}

// collapse squeezes runs of "-" into a single "-".
func collapse(s string) string {
	var b strings.Builder
	var prevDash bool
	for _, r := range s {
		if r == '-' {
			if prevDash {
				continue
			}
			prevDash = true
		} else {
			prevDash = false
		}
		b.WriteRune(r)
	}
	return b.String()
}

func clip(s string, n int) string {
	if n < 1 {
		return ""
	}
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func isTaken(name string, taken []string) bool {
	for _, t := range taken {
		if t == name {
			return true
		}
	}
	return false
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/herdr/ -run TestAgentName -v`
Expected: PASS, all subtests, each name matching `^[a-z][a-z0-9_-]{0,31}$`.

- [ ] **Step 5: Run the gates**

Run: `gofmt -l . && go vet ./... && golangci-lint run && go test -race ./...`
Expected: clean.

- [ ] **Step 6: Commit**

```bash
git add internal/herdr/name.go internal/herdr/name_test.go
git commit -m "feat(herdr): derive legal unique agent names from slot ids"
```

---

### Task 6: `Live()` — discover running agents as `core.Session` values

**Files:**
- Create: `internal/herdr/backend.go`
- Create: `internal/herdr/backend_test.go`
- Create: `internal/herdr/testdata/agent_list_with_bare_pane.json`

**Interfaces:**
- Consumes: `(*Client).agentList` (Task 3), `SlotIDForPath` (Task 4), `core.Session`.
- Produces: `(*Client).Live() ([]core.Session, error)`.

- [ ] **Step 1: Write the fixture**

Create `internal/herdr/testdata/agent_list_with_bare_pane.json` — one real agent plus an entry with no `agent` field, the shape a pane hosting no agent returns:

```json
{"id":"cli:agent:list","result":{"agents":[{"agent":"claude","agent_status":"working","cwd":"/home/a/repos/x/bridge","pane_id":"w3:p1","tab_id":"w3:t1","workspace_id":"w3"},{"agent_status":"unknown","cwd":"/home/a/repos/x/bridge/.worktrees/bar","pane_id":"w3:p9","tab_id":"w3:t9","workspace_id":"w3"}],"type":"agent_list"}}
```

- [ ] **Step 2: Write the failing test**

Create `internal/herdr/backend_test.go`:

```go
package herdr

import (
	"context"
	"testing"
)

func TestLive_TwoAgents_ReturnsSessionsKeyedBySlotID(t *testing.T) {
	run, _ := fixtureRunner(t, "agent_list.json")
	got, err := (&Client{Run: run, Workspace: "w3"}).Live()
	if err != nil {
		t.Fatalf("Live: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].SlotID != "bridge" {
		t.Errorf("SlotID = %q, want bridge", got[0].SlotID)
	}
	if got[1].SlotID != "bridge-wt-foo" {
		t.Errorf("SlotID = %q, want bridge-wt-foo", got[1].SlotID)
	}
	if got[0].State != "working" || got[1].State != "blocked" {
		t.Errorf("states = %q/%q, want working/blocked", got[0].State, got[1].State)
	}
	if !got[0].LastActivity.IsZero() {
		t.Error("Herdr reports no timestamps, so LastActivity must stay zero")
	}
}

func TestLive_PaneWithoutAnAgent_IsSkipped(t *testing.T) {
	run, _ := fixtureRunner(t, "agent_list_with_bare_pane.json")
	got, err := (&Client{Run: run}).Live()
	if err != nil {
		t.Fatalf("Live: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1 — an entry with no agent field is not a session", len(got))
	}
	if got[0].SlotID != "bridge" {
		t.Errorf("SlotID = %q", got[0].SlotID)
	}
}

func TestLive_NoAgents_ReturnsEmpty(t *testing.T) {
	run, _ := fixtureRunner(t, "agent_list_empty.json")
	got, err := (&Client{Run: run}).Live()
	if err != nil {
		t.Fatalf("Live: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("len = %d, want 0", len(got))
	}
}

func TestLive_UnmappablePath_IsSkipped(t *testing.T) {
	run := func(context.Context, ...string) ([]byte, error) {
		return []byte(`{"id":"x","result":{"agents":[{"agent":"claude","agent_status":"idle","cwd":"/","pane_id":"w3:p1","tab_id":"w3:t1"}],"type":"agent_list"}}`), nil
	}
	got, err := (&Client{Run: run}).Live()
	if err != nil {
		t.Fatalf("Live: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("len = %d, want 0 — a cwd with no slot id is not a session", len(got))
	}
}
```

- [ ] **Step 3: Run the test to verify it fails**

Run: `go test ./internal/herdr/ -run TestLive -v`
Expected: FAIL to build — `(*Client).Live` is undefined.

- [ ] **Step 4: Write the minimal implementation**

Create `internal/herdr/backend.go`:

```go
package herdr

import (
	"context"

	"github.com/freaxnx01/bridge/internal/core"
)

// Live reports the agents Herdr currently hosts, as core.Session values keyed
// by the bridge slot id derived from each agent's working directory.
//
// State carries Herdr's lifecycle status verbatim (working, idle, blocked,
// done, unknown) rather than tmux's attached/detached. LastActivity stays the
// zero value: Herdr reports no timestamps.
func (c *Client) Live() ([]core.Session, error) {
	agents, err := c.agentList(context.Background())
	if err != nil {
		return nil, err
	}
	out := make([]core.Session, 0, len(agents))
	for _, a := range agents {
		// An entry with no agent field is a pane hosting no agent; it also
		// reports AgentStatus "unknown", so the field's presence is what
		// distinguishes the two.
		if a.Agent == "" {
			continue
		}
		slot := SlotIDForPath(a.Cwd)
		if slot == "" {
			continue
		}
		out = append(out, core.Session{
			SlotID:   slot,
			TmuxName: slot,
			State:    a.AgentStatus,
		})
	}
	return out, nil
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/herdr/ -run TestLive -v`
Expected: PASS, all four cases.

- [ ] **Step 6: Run the gates**

Run: `gofmt -l . && go vet ./... && golangci-lint run && go test -race ./...`
Expected: clean.

- [ ] **Step 7: Commit**

```bash
git add internal/herdr/backend.go internal/herdr/backend_test.go internal/herdr/testdata/agent_list_with_bare_pane.json
git commit -m "feat(herdr): discover live agents as sessions keyed by slot id"
```

---

### Task 7: `Attach()` — focus the tab hosting a slot's agent

**Files:**
- Modify: `internal/herdr/backend.go` (add `Attach` and the `tabFor` helper)
- Modify: `internal/herdr/backend_test.go` (add the `Attach` cases)

**Interfaces:**
- Consumes: `(*Client).agentList` (Task 3), `SlotIDForPath` (Task 4), `launcher.RunPlan`, `ErrNoSession`.
- Produces: `(*Client).Attach(slot string) (launcher.Plan, error)`; `(*Client).tabFor(ctx context.Context, slot string) (string, error)` returning the tab id.

- [ ] **Step 1: Write the failing test**

Append to `internal/herdr/backend_test.go`:

```go
func TestAttach_LiveSlot_ReturnsARunPlanThatFocusesTheTab(t *testing.T) {
	body, _ := fixtureRunner(t, "agent_list.json")
	var calls [][]string
	run := func(ctx context.Context, args ...string) ([]byte, error) {
		calls = append(calls, args)
		if args[0] == "agent" {
			return body(ctx, args...)
		}
		return []byte(`{"id":"cli:tab:focus","result":{"type":"ok"}}`), nil
	}
	plan, err := (&Client{Run: run, Workspace: "w3"}).Attach("bridge-wt-foo")
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	if plan.Argv() != nil {
		t.Error("a Herdr attach must be a run plan, never an exec plan — nav must not be replaced")
	}
	fn := plan.Run()
	if fn == nil {
		t.Fatal("Attach returned a plan with no Run func")
	}
	if err := fn(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	last := calls[len(calls)-1]
	if len(last) < 3 || last[0] != "tab" || last[1] != "focus" || last[2] != "w3:t2" {
		t.Errorf("argv = %v, want [tab focus w3:t2]", last)
	}
}

func TestAttach_UnknownSlot_ReturnsErrNoSession(t *testing.T) {
	run, _ := fixtureRunner(t, "agent_list.json")
	_, err := (&Client{Run: run}).Attach("not-a-live-slot")
	if !errors.Is(err, ErrNoSession) {
		t.Errorf("err = %v, want ErrNoSession", err)
	}
}
```

Add `"errors"` to that file's imports.

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/herdr/ -run TestAttach -v`
Expected: FAIL to build — `(*Client).Attach` is undefined.

- [ ] **Step 3: Write the minimal implementation**

Append to `internal/herdr/backend.go`:

```go
// Attach focuses the Herdr tab hosting slot's agent. It returns a run plan, so
// nav stays on screen while focus moves to the agent's tab. Returns a wrapped
// ErrNoSession when no live agent matches the slot.
func (c *Client) Attach(slot string) (launcher.Plan, error) {
	tab, err := c.tabFor(context.Background(), slot)
	if err != nil {
		return launcher.Plan{}, err
	}
	return launcher.RunPlan(func(ctx context.Context) error {
		return c.call(ctx, nil, "tab", "focus", tab)
	}), nil
}

// tabFor returns the tab id hosting slot's agent, or a wrapped ErrNoSession.
func (c *Client) tabFor(ctx context.Context, slot string) (string, error) {
	agents, err := c.agentList(ctx)
	if err != nil {
		return "", err
	}
	for _, a := range agents {
		if a.Agent == "" {
			continue
		}
		if SlotIDForPath(a.Cwd) == slot {
			return a.TabID, nil
		}
	}
	return "", fmt.Errorf("%w: %s", ErrNoSession, slot)
}
```

Add `"fmt"` and `"github.com/freaxnx01/bridge/internal/launcher"` to `backend.go`'s imports.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/herdr/ -run TestAttach -v`
Expected: PASS, both cases.

- [ ] **Step 5: Run the gates**

Run: `gofmt -l . && go vet ./... && golangci-lint run && go test -race ./...`
Expected: clean.

- [ ] **Step 6: Commit**

```bash
git add internal/herdr/backend.go internal/herdr/backend_test.go
git commit -m "feat(herdr): attach by focusing the tab hosting a slot's agent"
```

---

### Task 8: `Launch()` — create a tab, start the agent, focus it

The task with the real behaviour: attach-before-create idempotency, the pane-readiness retry, `agent_not_ready` treated as success-with-focus, and the non-agent `code` path.

**Files:**
- Modify: `internal/herdr/backend.go` (add `Launch`, `startAgent`, `kindFor`)
- Modify: `internal/herdr/backend_test.go` (add the `Launch` cases)

**Interfaces:**
- Consumes: everything above — `tabCreate` (Task 3), `agentName` (Task 5), `tabFor`/`ErrNoSession` (Task 7), `launcher.RunPlan`, `ErrAgentNotReady`.
- Produces: `(*Client).Launch(slot, dir string, spec agents.AgentSpec) (launcher.Plan, error)`. With Tasks 6 and 7, `*Client` now satisfies `launcher.Backend`.

- [ ] **Step 1: Write the failing test**

Append to `internal/herdr/backend_test.go`:

```go
// scriptedRunner replays a response per subcommand and records argv. Keys are
// "<group> <sub>", e.g. "tab create". A key listed in failures fails that many
// times (returning failBody with exit 1) before its normal response, which is
// how the pane-readiness retry is exercised without any real timing.
type scriptedRunner struct {
	responses map[string][]byte
	failures  map[string]int
	failBody  map[string][]byte
	attempts  map[string]int
	calls     [][]string
}

// newScriptedRunner builds a runner from testdata files, keyed by subcommand.
func newScriptedRunner(t *testing.T, fixtures map[string]string) *scriptedRunner {
	t.Helper()
	responses := map[string][]byte{}
	for key, file := range fixtures {
		b, err := os.ReadFile(filepath.Join("testdata", file))
		if err != nil {
			t.Fatalf("read fixture %s: %v", file, err)
		}
		responses[key] = b
	}
	return &scriptedRunner{
		responses: responses,
		failures:  map[string]int{},
		failBody:  map[string][]byte{},
		attempts:  map[string]int{},
	}
}

// newLaunchRunner is the common case: a creatable tab and no live agents.
func newLaunchRunner(t *testing.T) *scriptedRunner {
	t.Helper()
	return newScriptedRunner(t, map[string]string{
		"tab create": "tab_create.json",
		"agent list": "agent_list_empty.json",
	})
}

func (s *scriptedRunner) run(_ context.Context, args ...string) ([]byte, error) {
	s.calls = append(s.calls, args)
	key := args[0] + " " + args[1]
	s.attempts[key]++
	if n := s.failures[key]; n > 0 {
		s.failures[key] = n - 1
		return s.failBody[key], &ExitError{Code: 1}
	}
	if body, ok := s.responses[key]; ok {
		return body, nil
	}
	return []byte(`{"id":"x","result":{"type":"ok"}}`), nil
}

func (s *scriptedRunner) argvFor(group, sub string) []string {
	for _, a := range s.calls {
		if len(a) >= 2 && a[0] == group && a[1] == sub {
			return a
		}
	}
	return nil
}

func TestLaunch_NoLiveSession_CreatesTabStartsAgentThenFocuses(t *testing.T) {
	sr := newLaunchRunner(t)
	plan, err := (&Client{Run: sr.run, Workspace: "w3"}).Launch(
		"bridge", "/repos/bridge", agents.AgentSpec{Name: "claude", Bin: "claude"})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	if plan.Argv() != nil {
		t.Error("a Herdr launch must be a run plan — nav must not be replaced")
	}
	if err := plan.Run()(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}

	if got := sr.argvFor("tab", "create"); got == nil {
		t.Fatal("no `tab create` call")
	}
	start := sr.argvFor("agent", "start")
	if start == nil {
		t.Fatal("no `agent start` call")
	}
	if !containsPair(start, "--pane", "w3:p6") {
		t.Errorf("agent start argv %v must target the created root pane w3:p6", start)
	}
	if !containsPair(start, "--kind", "claude") {
		t.Errorf("agent start argv %v must pass --kind claude", start)
	}
	focus := sr.argvFor("tab", "focus")
	if focus == nil || focus[2] != "w3:t4" {
		t.Errorf("tab focus argv = %v, want [tab focus w3:t4]", focus)
	}
}

func TestLaunch_AgentArgs_ArePassedAfterADoubleDash(t *testing.T) {
	sr := newLaunchRunner(t)
	plan, err := (&Client{Run: sr.run, Workspace: "w3"}).Launch(
		"bridge", "/repos/bridge",
		agents.AgentSpec{Name: "claude", Bin: "claude", Args: []string{"-n", "bridge [foo]"}})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	if err := plan.Run()(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	start := sr.argvFor("agent", "start")
	dash := -1
	for i, a := range start {
		if a == "--" {
			dash = i
			break
		}
	}
	if dash < 0 {
		t.Fatalf("argv %v must separate native agent args with --", start)
	}
	rest := start[dash+1:]
	if len(rest) != 2 || rest[0] != "-n" || rest[1] != "bridge [foo]" {
		t.Errorf("args after -- = %v, want [-n \"bridge [foo]\"]", rest)
	}
}

func TestLaunch_SlotAlreadyLive_FocusesInsteadOfCreatingASecondTab(t *testing.T) {
	sr := newScriptedRunner(t, map[string]string{"agent list": "agent_list.json"})
	plan, err := (&Client{Run: sr.run, Workspace: "w3"}).Launch(
		"bridge-wt-foo", "/repos/bridge/.worktrees/foo", agents.AgentSpec{Name: "claude", Bin: "claude"})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	if err := plan.Run()(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if sr.argvFor("tab", "create") != nil {
		t.Error("a live slot must not create a second tab")
	}
	focus := sr.argvFor("tab", "focus")
	if focus == nil || focus[2] != "w3:t2" {
		t.Errorf("tab focus argv = %v, want [tab focus w3:t2]", focus)
	}
}

func TestLaunch_PaneNotReady_RetriesAgentStartThenSucceeds(t *testing.T) {
	sr := newLaunchRunner(t)
	// Fail twice with "pane is not at an interactive prompt", then succeed —
	// the shape of a pane still running shell init.
	sr.failures["agent start"] = 2
	sr.failBody["agent start"] = []byte(`{"id":"cli:agent:start","error":{"code":"pane_not_available","message":"pane is not at an interactive prompt"}}`)

	c := &Client{Run: sr.run, Workspace: "w3", retryDelay: time.Millisecond}
	plan, err := c.Launch("bridge", "/repos/bridge", agents.AgentSpec{Name: "claude", Bin: "claude"})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	if err := plan.Run()(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := sr.attempts["agent start"]; got != 3 {
		t.Errorf("agent start attempts = %d, want 3 (two failures then success)", got)
	}
	if sr.argvFor("tab", "focus") == nil {
		t.Error("a successful retry must still focus the tab")
	}
}

func TestLaunch_PaneNeverReady_GivesUpAndReportsTheError(t *testing.T) {
	sr := newLaunchRunner(t)
	sr.failures["agent start"] = 99 // never settles
	sr.failBody["agent start"] = []byte(`{"id":"cli:agent:start","error":{"code":"pane_not_available","message":"pane is not at an interactive prompt"}}`)

	c := &Client{Run: sr.run, Workspace: "w3", retryDelay: time.Millisecond}
	plan, err := c.Launch("bridge", "/repos/bridge", agents.AgentSpec{Name: "claude", Bin: "claude"})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	if err := plan.Run()(context.Background()); err == nil {
		t.Fatal("expected an error once the attempts are exhausted")
	}
	if got := sr.attempts["agent start"]; got != startAttempts {
		t.Errorf("agent start attempts = %d, want %d", got, startAttempts)
	}
	if sr.argvFor("tab", "focus") != nil {
		t.Error("a failed start must not focus a tab with no agent in it")
	}
	if sr.argvFor("tab", "create") == nil {
		t.Error("the created tab is deliberately left in place as a shell in the right directory")
	}
}

func TestLaunch_AgentNotReady_FocusesTheTabAndReportsSuccess(t *testing.T) {
	sr := newLaunchRunner(t)
	// Fails once, but with agent_not_ready: the agent DID start and is blocked.
	sr.failures["agent start"] = 1
	sr.failBody["agent start"] = []byte(`{"id":"cli:agent:start","error":{"code":"agent_not_ready","message":"agent did not reach a ready state"}}`)

	c := &Client{Run: sr.run, Workspace: "w3", retryDelay: time.Millisecond}
	plan, err := c.Launch("bridge", "/repos/bridge", agents.AgentSpec{Name: "claude", Bin: "claude"})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	if err := plan.Run()(context.Background()); err != nil {
		t.Fatalf("agent_not_ready means the agent exists and needs input — not a failure: %v", err)
	}
	if sr.argvFor("tab", "focus") == nil {
		t.Error("agent_not_ready must still focus the tab so the user can answer the prompt")
	}
	if sr.attempts["agent start"] != 1 {
		t.Errorf("agent start attempts = %d — agent_not_ready must not be retried", sr.attempts["agent start"])
	}
}

func TestLaunch_CodeAgent_UsesPaneRunAndDoesNotStartAnAgent(t *testing.T) {
	sr := newLaunchRunner(t)
	plan, err := (&Client{Run: sr.run, Workspace: "w3"}).Launch(
		"bridge", "/repos/bridge", agents.AgentSpec{Name: "code", Bin: "code", Args: []string{"."}})
	if err != nil {
		t.Fatalf("Launch: %v", err)
	}
	if err := plan.Run()(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if sr.argvFor("agent", "start") != nil {
		t.Error("code is not a Herdr agent kind — it must not go through agent start")
	}
	pr := sr.argvFor("pane", "run")
	if pr == nil {
		t.Fatal("expected a `pane run` call for the code agent")
	}
	if pr[2] != "w3:p6" {
		t.Errorf("pane run target = %q, want w3:p6", pr[2])
	}
	if pr[3] != "code ." {
		t.Errorf("pane run command = %q, want %q", pr[3], "code .")
	}
	if sr.argvFor("tab", "focus") != nil {
		t.Error("the code GUI takes focus itself — nav must not focus the tab")
	}
}

func TestLaunch_EmptySlotOrDir_IsAnError(t *testing.T) {
	sr := newLaunchRunner(t)
	c := &Client{Run: sr.run, Workspace: "w3"}
	if _, err := c.Launch("", "/repos/bridge", agents.AgentSpec{Name: "claude", Bin: "claude"}); err == nil {
		t.Error("expected an error for an empty slot")
	}
	if _, err := c.Launch("bridge", "", agents.AgentSpec{Name: "claude", Bin: "claude"}); err == nil {
		t.Error("expected an error for an empty dir")
	}
}

// Compile-time proof that *Client satisfies the seam nav injects.
var _ launcher.Backend = (*Client)(nil)
```

Add `"os"`, `"path/filepath"`, `"time"`, `"github.com/freaxnx01/bridge/internal/agents"` and `"github.com/freaxnx01/bridge/internal/launcher"` to that file's imports.

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/herdr/ -run TestLaunch -v`
Expected: FAIL to build — `(*Client).Launch` and `Client.retryDelay` are undefined.

- [ ] **Step 3: Write the minimal implementation**

Append to `internal/herdr/backend.go`:

```go
// startAttempts and defaultRetryDelay bound the wait for a freshly created
// pane to reach its interactive prompt. `herdr agent start` requires a settled
// shell, but a new pane is still running profile init — which on a host with
// direnv hooks can take a moment. Five attempts on a doubling delay from 250ms
// spans ~4s of shell startup, well inside a user's patience.
const (
	startAttempts     = 5
	defaultRetryDelay = 250 * time.Millisecond
)

// Launch opens a Herdr tab in dir running spec's agent, then focuses it.
//
// It is idempotent, as launcher.Backend requires: a slot whose agent is already
// live resolves as Attach would, because `herdr tab create` always creates and
// would otherwise leave a duplicate tab behind on every launch.
func (c *Client) Launch(slot, dir string, spec agents.AgentSpec) (launcher.Plan, error) {
	if slot == "" {
		return launcher.Plan{}, errors.New("herdr: empty slot")
	}
	if dir == "" {
		return launcher.Plan{}, errors.New("herdr: empty dir")
	}
	if spec.Bin == "" {
		return launcher.Plan{}, errors.New("herdr: agent has no Bin")
	}
	if plan, err := c.Attach(slot); err == nil {
		return plan, nil
	} else if !errors.Is(err, ErrNoSession) {
		return launcher.Plan{}, err
	}
	return launcher.RunPlan(func(ctx context.Context) error {
		tab, err := c.tabCreate(ctx, dir, slot)
		if err != nil {
			return err
		}
		// A GUI editor is not a Herdr agent kind: run it in the pane and let it
		// take focus itself.
		if _, ok := agentKinds[spec.Name]; !ok {
			cmd := strings.Join(append([]string{spec.Bin}, spec.Args...), " ")
			return c.call(ctx, nil, "pane", "run", tab.PaneID, cmd)
		}
		if err := c.startAgent(ctx, tab.PaneID, slot, spec); err != nil &&
			!errors.Is(err, ErrAgentNotReady) {
			// The tab is left in place: a shell in the right directory beats
			// nothing, and nav reports the error.
			return err
		}
		// Reached on success and on ErrAgentNotReady alike — in the latter case
		// the agent is up and waiting on a prompt, so the user must see it.
		return c.call(ctx, nil, "tab", "focus", tab.TabID)
	}), nil
}

// startAgent runs `herdr agent start`, retrying while the pane is still
// reaching its interactive prompt. ErrAgentNotReady is returned immediately: it
// means the agent did start and is blocked, which retrying cannot improve.
func (c *Client) startAgent(ctx context.Context, pane, slot string, spec agents.AgentSpec) error {
	live, err := c.agentList(ctx)
	if err != nil {
		return err
	}
	taken := make([]string, 0, len(live))
	for _, a := range live {
		if a.Agent != "" {
			taken = append(taken, a.Agent)
		}
	}
	args := []string{"agent", "start", agentName(slot, taken), "--kind", agentKinds[spec.Name], "--pane", pane}
	if len(spec.Args) > 0 {
		args = append(args, "--")
		args = append(args, spec.Args...)
	}
	delay := c.retryDelay
	if delay <= 0 {
		delay = defaultRetryDelay
	}
	var lastErr error
	for attempt := 0; attempt < startAttempts; attempt++ {
		lastErr = c.call(ctx, nil, args...)
		if lastErr == nil || errors.Is(lastErr, ErrAgentNotReady) || errors.Is(lastErr, ErrCLIUsage) {
			return lastErr
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
		delay *= 2
	}
	return fmt.Errorf("herdr: agent start gave up after %d attempts: %w", startAttempts, lastErr)
}
```

Add to `internal/herdr/herdr.go`, below the sentinels:

```go
// agentKinds maps a bridge agent name to its Herdr kind. An agent absent here
// is not a Herdr-recognized agent (VS Code is a GUI launch, not an agent) and
// runs via `pane run` instead.
var agentKinds = map[string]string{
	"claude":   "claude",
	"copilot":  "copilot",
	"opencode": "opencode",
}
```

Add `"errors"`, `"strings"`, `"time"` and `"github.com/freaxnx01/bridge/internal/agents"` to `backend.go`'s imports as needed.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/herdr/ -v`
Expected: PASS, every case in the package.

- [ ] **Step 5: Run the gates**

Run: `gofmt -l . && go vet ./... && golangci-lint run && go test -race ./...`
Expected: clean.

- [ ] **Step 6: Commit**

```bash
git add internal/herdr/
git commit -m "feat(herdr): launch agents as tabs with readiness retry and attach-first idempotency"
```

---

### Task 9: Render the Herdr lifecycle states

`legendEntries` is the declared single source for every status glyph, and `TestLegend_CoversAuditedGlyphs` **fails the build** when it drifts. The table, the two render sites and that test's expected set change together.

**Files:**
- Modify: `internal/nav/view.go:44-48` (`legendEntries`), `:225-229` (picker dot), `:368-375` (dashboard dot)
- Modify: `internal/nav/view_test.go` (the `TestLegend_CoversAuditedGlyphs` expected set)
- Modify: `internal/nav/types.go:69` and `:80` (the `state` field comments)

**Interfaces:**
- Consumes: `core.Session.State` values produced by `(*Client).Live()` (Task 6).
- Produces: `sessionDot(state string) string` in `internal/nav/view.go` — the single place a session state becomes a styled glyph, called by both render sites.

- [ ] **Step 1: Write the failing test**

Append to `internal/nav/view_test.go`:

```go
func TestSessionDot_HerdrStates_MapToDistinctGlyphs(t *testing.T) {
	tests := []struct {
		state string
		want  string
	}{
		{"working", stOk.Render("●")},
		{"blocked", stWarn.Render("●")},
		{"idle", stMuted.Render("○")},
		{"done", stMuted.Render("○")},
		{"unknown", stMuted.Render("·")},
		{"attached", stOk.Render("●")},
		{"detached", stMuted.Render("○")},
		{"", stMuted.Render("·")},
	}
	for _, tt := range tests {
		t.Run(tt.state, func(t *testing.T) {
			if got := sessionDot(tt.state); got != tt.want {
				t.Errorf("sessionDot(%q) = %q, want %q", tt.state, got, tt.want)
			}
		})
	}
}

func TestSessionDot_BlockedIsVisuallyDistinctFromWorking(t *testing.T) {
	if sessionDot("blocked") == sessionDot("working") {
		t.Error("a blocked agent needs the user; it must not render like a working one")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/nav/ -run TestSessionDot -v`
Expected: FAIL to build — `undefined: sessionDot`.

- [ ] **Step 3: Add `sessionDot`**

Add to `internal/nav/view.go`, directly above `legendEntries`:

```go
// sessionDot maps a session state to its styled glyph. It is the single place
// a state becomes a dot, shared by the picker's Active-sessions panel and the
// dashboard's worktree list. Every glyph it can return must have an entry in
// legendEntries below.
//
// tmux reports attached/detached; Herdr reports its agent lifecycle
// (working/idle/blocked/done/unknown). Both vocabularies land here.
func sessionDot(state string) string {
	switch state {
	case "attached", "working":
		return stOk.Render("●")
	case "blocked":
		return stWarn.Render("●")
	case "detached", "idle", "done":
		return stMuted.Render("○")
	default:
		return stMuted.Render("·")
	}
}
```

- [ ] **Step 4: Extend the legend table**

Replace the three `"Session"` entries at `internal/nav/view.go:45-47`:

```go
	{"●", stOk, "session attached (tmux) · agent working (Herdr)", "Session"},
	{"●", stWarn, "agent blocked — waiting on you (Herdr)", "Session"},
	{"○", stMuted, "session detached (tmux) · agent idle or done (Herdr)", "Session"},
	{"·", stMuted, "no session (dashboard row)", "Session"},
```

- [ ] **Step 5: Update the legend guard test**

In `internal/nav/view_test.go`, find `TestLegend_CoversAuditedGlyphs` and add the new blocked glyph to its expected set, matching the existing entries' shape. The guard exists so a glyph can never be added without being documented — satisfy it by declaring the new entry, never by loosening the assertion.

- [ ] **Step 6: Route both render sites through `sessionDot`**

Replace `internal/nav/view.go:225-229` (inside `viewPicker`'s session loop):

```go
			dot := sessionDot(s.state)
```

Replace `internal/nav/view.go:368-375` (inside `dashListBody`'s row loop):

```go
		dot := sessionDot(r.state)
```

- [ ] **Step 7: Update the state field comments**

In `internal/nav/types.go`, replace the two `state` comments:

```go
	state        string // tmux: attached|detached · Herdr: working|idle|blocked|done|unknown
```

at line 69 (`sessionRow`), and at line 80 (`dashRow`) the same with a trailing `· "" when no session`.

- [ ] **Step 8: Run the tests to verify they pass**

Run: `go test ./internal/nav/ -run 'TestSessionDot|TestLegend' -v`
Expected: PASS.

- [ ] **Step 9: Run the full nav suite**

Run: `go test ./internal/nav/ -v`
Expected: PASS. The golden-file view tests must still match — routing through `sessionDot` preserves the exact tmux glyphs, so no golden file should need `-update`. **If a golden test fails, the refactor changed rendering and that is a bug** — fix the code, do not run `-update`.

- [ ] **Step 10: Run the gates**

Run: `gofmt -l . && go vet ./... && golangci-lint run && go test -race ./...`
Expected: clean.

- [ ] **Step 11: Commit**

```bash
git add internal/nav/view.go internal/nav/view_test.go internal/nav/types.go
git commit -m "feat(nav): render Herdr agent lifecycle states, blocked distinctly"
```

---

### Task 10: Wire detection in `cmd/bridge`, and document it

**Files:**
- Modify: `cmd/bridge/nav.go:26-35` (add `Backend` to the `nav.Config` literal)
- Create: `cmd/bridge/backend.go`
- Create: `cmd/bridge/backend_test.go`
- Modify: `README.md` (environment-variable section)
- Modify: `CHANGELOG.md` (`[Unreleased]` / `Added`)

**Interfaces:**
- Consumes: `launcher.NewBackend` (Task 1), `herdr.New` (Tasks 3–8), `nav.Config.Backend` (Task 2).
- Produces: `selectBackend(getenv func(string) string) launcher.Backend` in `cmd/bridge`.

- [ ] **Step 1: Write the failing test**

Create `cmd/bridge/backend_test.go`:

```go
package main

import (
	"testing"

	"github.com/freaxnx01/bridge/internal/herdr"
)

func TestSelectBackend_ResolvesFromTheEnvironment(t *testing.T) {
	tests := []struct {
		name      string
		env       map[string]string
		wantHerdr bool
	}{
		{"nothing set", map[string]string{}, false},
		{"inside herdr", map[string]string{"HERDR_ENV": "1"}, true},
		{"inside herdr but opted out", map[string]string{"HERDR_ENV": "1", "BRIDGE_LAUNCHER": "tmux"}, false},
		{"opted in from outside", map[string]string{"BRIDGE_LAUNCHER": "herdr"}, true},
		{"HERDR_ENV set to something else", map[string]string{"HERDR_ENV": "0"}, false},
		{"unknown override falls back to autodetect", map[string]string{"HERDR_ENV": "1", "BRIDGE_LAUNCHER": "bogus"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := selectBackend(func(k string) string { return tt.env[k] })
			if got == nil {
				t.Fatal("selectBackend returned nil; nav would have no backend")
			}
			_, isHerdr := got.(*herdr.Client)
			if isHerdr != tt.wantHerdr {
				t.Errorf("herdr backend = %v, want %v", isHerdr, tt.wantHerdr)
			}
		})
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./cmd/bridge/ -run TestSelectBackend -v`
Expected: FAIL to build — `undefined: selectBackend`.

- [ ] **Step 3: Write the minimal implementation**

Create `cmd/bridge/backend.go`:

```go
package main

import (
	"github.com/freaxnx01/bridge/internal/herdr"
	"github.com/freaxnx01/bridge/internal/launcher"
)

// selectBackend resolves the session backend nav launches into.
//
// Precedence, highest first: an explicit BRIDGE_LAUNCHER value, then
// HERDR_ENV=1 autodetection (Herdr sets it in every managed pane), then the
// tmux/Windows-Terminal default. getenv is injected so the choice is testable
// without mutating the process environment.
//
// There is deliberately no fallback from Herdr to tmux: inside Herdr, spawning
// tmux is the very thing this backend exists to avoid, so a Herdr failure
// surfaces as an error in nav rather than as a silent tmux session.
func selectBackend(getenv func(string) string) launcher.Backend {
	switch getenv("BRIDGE_LAUNCHER") {
	case "tmux":
		return launcher.NewBackend()
	case "herdr":
		return herdr.New()
	}
	if getenv("HERDR_ENV") == "1" {
		return herdr.New()
	}
	return launcher.NewBackend()
}
```

- [ ] **Step 4: Inject it into `nav.Config`**

In `cmd/bridge/nav.go`, inside the `nav.Config{…}` literal (after `AgentArgs` at line 32), add:

```go
			Backend:      selectBackend(os.Getenv),
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./cmd/bridge/ -run TestSelectBackend -v`
Expected: PASS, all six subtests.

- [ ] **Step 6: Document `BRIDGE_LAUNCHER`**

In `README.md`, in the section listing `BRIDGE_DEFAULT_AGENT` and friends, add an entry in the same style as its neighbours:

```markdown
- `BRIDGE_LAUNCHER` — which multiplexer `bridge nav` launches agent sessions
  into: `tmux` or `herdr`. Unset (the default) autodetects: `herdr` when
  `HERDR_ENV=1` (Herdr sets it in every managed pane), otherwise `tmux`.
  In Herdr mode nav opens one tab per session and stays running in its own
  tab, and the dashboard shows Herdr's agent lifecycle — including which
  agents are **blocked** waiting on you. Precedence: this variable, then
  `HERDR_ENV` autodetection, then the tmux default. Note that the shell
  `bridge open <repo>` path still uses tmux.
```

- [ ] **Step 7: Add the changelog entry**

In `CHANGELOG.md`, under `[Unreleased]` → `Added` (create the heading if absent):

```markdown
- `bridge nav` Herdr mode: inside a Herdr session, agents launch as Herdr tabs
  instead of tmux sessions, so they are recognized by `herdr agent list` and
  its idle/working/blocked lifecycle. Selected automatically via `HERDR_ENV`,
  overridable with `BRIDGE_LAUNCHER=tmux|herdr`.
```

- [ ] **Step 8: Run the gates**

Run: `gofmt -l . && go vet ./... && golangci-lint run && go test -race ./...`
Expected: clean, full suite green.

- [ ] **Step 9: Verify the tmux path is untouched**

Run: `BRIDGE_LAUNCHER=tmux go run ./cmd/bridge nav --once`
Expected: one rendered frame, identical in shape to before this branch. This is the smoke check that the default path still works end-to-end.

- [ ] **Step 10: Commit**

```bash
git add cmd/bridge/backend.go cmd/bridge/backend_test.go cmd/bridge/nav.go README.md CHANGELOG.md
git commit -m "feat(nav): select the Herdr backend from HERDR_ENV, override with BRIDGE_LAUNCHER"
```

---

## Manual verification (outside CI, inside Herdr)

CI has no Herdr server, so these run on a Herdr host after the branch is built. Not a task — a reviewer checklist.

- [ ] `just build`, then `br nav` from a Herdr pane. Enter on a session-less worktree row opens a **new Herdr tab** running Claude and switches to it; `herdr agent list` shows the agent with the correct `cwd`.
- [ ] Switch back to nav's tab. It is **still running** on the dashboard, not replaced.
- [ ] Press Enter on that same row again. Focus moves to the existing tab; `herdr tab list` shows **no duplicate**.
- [ ] Let an agent reach an approval prompt. Its dashboard row shows the **warn-coloured `●`**; `?` shows the blocked entry in the legend.
- [ ] `BRIDGE_LAUNCHER=tmux br nav` from the same pane still launches tmux, proving the escape hatch.
- [ ] Launch an agent, then `herdr tab close` it by hand and refresh nav (`r`). The row loses its session dot.

## Deferred, per the spec's Non-goals

Not implemented here; file as issues after this lands.

1. `bridge open` (shell path) under Herdr — `cmd/bridge/preflight.go:288-298`
2. `bridge sessions` / `bridge status` under Herdr
3. WebUI / `internal/api/repos.go:79` session data under Herdr
4. A Herdr-mode e2e tier using an isolated `herdr --session` server
5. Status-rank sort for dashboard rows in Herdr mode (`blocked` → `working` → `idle`)
6. Workspace-per-repo topology, if real use shows a Herdr workspace means "a project"
7. `slots.json` cross-check in `Live()` to catch worktrees outside `.worktrees/`
