package herdr

import (
	"context"
	"errors"
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
