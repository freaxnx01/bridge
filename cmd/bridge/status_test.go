package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/freaxnx01/bridge/internal/core"
)

func TestStatusHuman(t *testing.T) {
	cache := t.TempDir()
	cacheDir := filepath.Join(cache, "bridge")
	_ = os.MkdirAll(cacheDir, 0o755)
	_ = os.WriteFile(filepath.Join(cacheDir, "presence.json"), []byte(`{"mode":"away"}`), 0o644)
	_ = os.WriteFile(filepath.Join(cacheDir, "sync.json"), []byte(`{"unpushed":["x"]}`), 0o644)

	cmd := bridgeCmd("status")
	cmd.Env = append(os.Environ(),
		"XDG_CACHE_HOME="+cache,
		"BRIDGE_TMUX_FIXTURE=",
	)
	var sout stringBuf
	cmd.Stdout = &sout
	if err := cmd.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	s := sout.String()
	if !contains(s, "presence:") || !contains(s, "away") || !contains(s, "unpushed:") {
		t.Errorf("missing keys in %s", s)
	}
}

func TestStatusJSON(t *testing.T) {
	cache := t.TempDir()
	cmd := bridgeCmd("status", "--json")
	cmd.Env = append(os.Environ(), "XDG_CACHE_HOME="+cache)
	var sout stringBuf
	cmd.Stdout = &sout
	if err := cmd.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	var st map[string]any
	if err := json.Unmarshal([]byte(sout.String()), &st); err != nil {
		t.Fatalf("json: %v in %s", err, sout.String())
	}
	for _, k := range []string{"sessions", "presence", "sync", "version"} {
		if _, ok := st[k]; !ok {
			t.Errorf("missing key %s in %+v", k, st)
		}
	}
}

func TestStatusSlimOmitsTable(t *testing.T) {
	cache := t.TempDir()
	cmd := bridgeCmd("status", "--slim")
	cmd.Env = append(os.Environ(), "XDG_CACHE_HOME="+cache)
	var sout stringBuf
	cmd.Stdout = &sout
	if err := cmd.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	s := sout.String()
	if contains(s, "slot") && contains(s, "kind") {
		t.Errorf("--slim should not emit the detail table header, got:\n%s", s)
	}
}

func TestComposeStatusRowsJoinsSlotAndSession(t *testing.T) {
	now := time.Unix(2_000_000, 0).UTC()
	slots := []core.Slot{
		{ID: "bridge", Repo: "bridge", Agent: "claude", Created: time.Unix(1_900_000, 0).UTC()},
		{ID: "stale", Repo: "foo", Agent: "claude", Created: time.Unix(1_000_000, 0).UTC()},
	}
	sessions := []core.Session{
		{SlotID: "bridge", State: "attached", TmuxName: "bridge", PID: 4242, Age: 100 * time.Second},
		// "extra" is a tmux session running outside the slot registry — must NOT
		// appear in the table, since LiveSessions is unfiltered and would
		// otherwise leak unrelated shells / admin sessions into bridge status.
		{SlotID: "extra", State: "detached", TmuxName: "extra", PID: 9999, Age: 10 * time.Minute},
	}
	got := composeStatusRows(slots, sessions, now)
	want := []statusRow{
		{Slot: "bridge", Kind: "slot", Repo: "bridge", Agent: "claude", Age: humanDuration(now.Sub(slots[0].Created)), State: "attached", TmuxName: "bridge", PID: 4242},
		{Slot: "stale", Kind: "slot", Repo: "foo", Agent: "claude", Age: humanDuration(now.Sub(slots[1].Created)), State: "—", TmuxName: "—"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v\nwant %+v", got, want)
	}
}

func TestComposeStatusRowsExcludesUntaggedTmuxSessions(t *testing.T) {
	// Explicit regression test: any tmux session whose name doesn't match a
	// slot ID is silently dropped, regardless of how many there are. Adding
	// pane-command filtering to re-surface claude-running sessions is a
	// separate enhancement.
	now := time.Unix(2_000_000, 0).UTC()
	sessions := []core.Session{
		{SlotID: "work-shell"}, {SlotID: "irssi"}, {SlotID: "admin"},
	}
	got := composeStatusRows(nil, sessions, now)
	if len(got) != 0 {
		t.Errorf("expected no rows from sessions-only input, got %+v", got)
	}
}

func TestComposeStatusRowsAppendsWorktreeToRepo(t *testing.T) {
	now := time.Unix(2_000_000, 0).UTC()
	slots := []core.Slot{{ID: "bridge-wt-feat", Repo: "bridge", Worktree: "feat", Created: now.Add(-time.Hour)}}
	got := composeStatusRows(slots, nil, now)
	if len(got) != 1 || got[0].Repo != "bridge [feat]" {
		t.Errorf("got %+v, want repo=bridge [feat]", got)
	}
}
