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
