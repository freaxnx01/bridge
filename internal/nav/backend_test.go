package nav

import (
	"context"
	"errors"
	"strings"
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

func TestUpdate_ExecDoneMsgWithError_ShowsItInTheStatusLine(t *testing.T) {
	// Backends that do their work when the plan runs (Herdr) report failure
	// through execDoneMsg, not from the Update call that built the plan. If
	// this message drops the error, every such failure is silent.
	m := initialModel(Config{Backend: &fakeBackend{}})
	m.screen = screenDash
	m.repo = core.Repo{Name: "bridge", Path: "/repos/bridge"}

	out, _ := m.Update(execDoneMsg{err: errors.New("herdr: pane never became available")})
	got := out.(Model).status
	if !strings.Contains(got, "pane never became available") {
		t.Errorf("status = %q, want it to carry the execDoneMsg error", got)
	}
}

func TestUpdate_ExecDoneMsgWithoutError_LeavesStatusAlone(t *testing.T) {
	m := initialModel(Config{Backend: &fakeBackend{}})
	m.screen = screenDash
	m.repo = core.Repo{Name: "bridge", Path: "/repos/bridge"}
	m.status = "ready"

	out, _ := m.Update(execDoneMsg{})
	if got := out.(Model).status; got != "ready" {
		t.Errorf("status = %q, want it untouched on a clean return", got)
	}
}
