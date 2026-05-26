package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSlotsPruneDropsDeadEntries(t *testing.T) {
	cache := t.TempDir()
	bridgeCache := filepath.Join(cache, "bridge")
	_ = os.MkdirAll(bridgeCache, 0o755)
	// Seed slots.json: one entry whose tmux session is alive, one stale.
	_ = os.WriteFile(filepath.Join(bridgeCache, "slots.json"), []byte(`{"slots":[
		{"id":"alive","repo":"x","agent":"claude","created":"2026-01-01T00:00:00Z"},
		{"id":"dead","repo":"y","agent":"claude","created":"2026-01-01T00:00:00Z"}
	]}`), 0o644)
	// Tmux fixture lists only the alive session.
	tmuxFix := filepath.Join(t.TempDir(), "tmux-ls")
	_ = os.WriteFile(tmuxFix, []byte("alive|0|1764000000\n"), 0o644)

	cmd := bridgeCmd("slots", "prune")
	cmd.Env = append(os.Environ(),
		"XDG_CACHE_HOME="+cache,
		"BRIDGE_TMUX_FIXTURE="+tmuxFix,
		"BRIDGE_NOW=1764000100",
	)
	var sout stringBuf
	cmd.Stdout = &sout
	if err := cmd.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(sout.String(), "dropped dead") {
		t.Errorf("expected drop notice in: %s", sout.String())
	}

	b, _ := os.ReadFile(filepath.Join(bridgeCache, "slots.json"))
	var f struct{ Slots []map[string]any }
	_ = json.Unmarshal(b, &f)
	if len(f.Slots) != 1 || f.Slots[0]["id"] != "alive" {
		t.Errorf("unexpected slots.json after prune: %s", b)
	}
}

func TestSlotsPruneNoStale(t *testing.T) {
	cache := t.TempDir()
	bridgeCache := filepath.Join(cache, "bridge")
	_ = os.MkdirAll(bridgeCache, 0o755)
	_ = os.WriteFile(filepath.Join(bridgeCache, "slots.json"), []byte(`{"slots":[
		{"id":"alive","repo":"x","agent":"claude","created":"2026-01-01T00:00:00Z"}
	]}`), 0o644)
	tmuxFix := filepath.Join(t.TempDir(), "tmux-ls")
	_ = os.WriteFile(tmuxFix, []byte("alive|0|1764000000\n"), 0o644)

	cmd := bridgeCmd("slots", "prune")
	cmd.Env = append(os.Environ(),
		"XDG_CACHE_HOME="+cache,
		"BRIDGE_TMUX_FIXTURE="+tmuxFix,
		"BRIDGE_NOW=1764000100",
	)
	var sout stringBuf
	cmd.Stdout = &sout
	if err := cmd.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(sout.String(), "no stale entries") {
		t.Errorf("expected 'no stale entries' in: %s", sout.String())
	}
}

func TestSlotsPruneRefusesWithoutTmux(t *testing.T) {
	cache := t.TempDir()
	bridgeCache := filepath.Join(cache, "bridge")
	_ = os.MkdirAll(bridgeCache, 0o755)
	// Seed: one entry. If the guard fails open, prune would wipe it.
	_ = os.WriteFile(filepath.Join(bridgeCache, "slots.json"), []byte(`{"slots":[
		{"id":"alive","repo":"x","agent":"claude","created":"2026-01-01T00:00:00Z"}
	]}`), 0o644)

	// Empty PATH = tmux binary not found. No BRIDGE_TMUX_FIXTURE either, so the
	// guard must engage rather than fall through to the (nil, nil) silent-prune
	// path. The TMPDIR holds the test's go-built binary, not tmux.
	cmd := bridgeCmd("slots", "prune")
	cmd.Env = []string{
		"XDG_CACHE_HOME=" + cache,
		"PATH=/var/empty",
	}
	var sout, serr stringBuf
	cmd.Stdout = &sout
	cmd.Stderr = &serr
	err := cmd.Run()
	if err == nil {
		t.Fatalf("expected non-zero exit; stdout=%q stderr=%q", sout.String(), serr.String())
	}
	if !strings.Contains(serr.String(), "tmux not found") {
		t.Errorf("expected 'tmux not found' in stderr, got: %s", serr.String())
	}

	// Registry must be untouched.
	b, _ := os.ReadFile(filepath.Join(bridgeCache, "slots.json"))
	if !strings.Contains(string(b), "alive") {
		t.Errorf("guard failed: registry was modified: %s", b)
	}
}

func TestSlotsListMarksLive(t *testing.T) {
	cache := t.TempDir()
	bridgeCache := filepath.Join(cache, "bridge")
	_ = os.MkdirAll(bridgeCache, 0o755)
	_ = os.WriteFile(filepath.Join(bridgeCache, "slots.json"), []byte(`{"slots":[
		{"id":"alive","repo":"x","agent":"claude","created":"2026-01-01T00:00:00Z"},
		{"id":"dead","repo":"y","agent":"claude","created":"2026-01-01T00:00:00Z"}
	]}`), 0o644)
	tmuxFix := filepath.Join(t.TempDir(), "tmux-ls")
	_ = os.WriteFile(tmuxFix, []byte("alive|0|1764000000\n"), 0o644)

	cmd := bridgeCmd("slots")
	cmd.Env = append(os.Environ(),
		"XDG_CACHE_HOME="+cache,
		"BRIDGE_TMUX_FIXTURE="+tmuxFix,
		"BRIDGE_NOW=1764000100",
	)
	var sout stringBuf
	cmd.Stdout = &sout
	if err := cmd.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	out := sout.String()
	// Expect '*' on the alive row and ' ' (or no '*') on the dead row.
	lines := strings.Split(out, "\n")
	var aliveLine, deadLine string
	for _, l := range lines {
		if strings.Contains(l, "alive") {
			aliveLine = l
		}
		if strings.Contains(l, "dead") {
			deadLine = l
		}
	}
	if !strings.HasPrefix(aliveLine, "*") {
		t.Errorf("alive line missing '*': %q", aliveLine)
	}
	if strings.HasPrefix(deadLine, "*") {
		t.Errorf("dead line should not have '*': %q", deadLine)
	}
}
