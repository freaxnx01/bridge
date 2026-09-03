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
