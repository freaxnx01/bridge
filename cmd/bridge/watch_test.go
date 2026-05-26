package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWatchSingleIteration(t *testing.T) {
	root := writeFakeRepos(t)
	cache := t.TempDir()
	cmd := bridgeCmd("watch")
	cmd.Env = append(os.Environ(),
		"BRIDGE_REPOS_ROOT="+root,
		"XDG_CACHE_HOME="+cache,
		"BRIDGE_TEST_MAX_ITERATIONS=1",
		// Also need a short tick interval so the test exits via tick after ~30s would be too slow.
		"BRIDGE_TEST_TICK_MS=100",
	)
	if err := cmd.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cache, "bridge", "watch.last")); err != nil {
		t.Errorf("expected watch.last: %v", err)
	}
}

func TestWatchStatusReportsNotRunning(t *testing.T) {
	cache := t.TempDir()
	cmd := bridgeCmd("watch", "--status")
	cmd.Env = append(os.Environ(), "XDG_CACHE_HOME="+cache)
	out, _ := cmd.CombinedOutput()
	if string(out) == "" {
		t.Error("expected output")
	}
}
