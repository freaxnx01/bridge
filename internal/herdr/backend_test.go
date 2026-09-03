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
