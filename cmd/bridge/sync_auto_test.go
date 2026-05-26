package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSyncAutoSingleIteration(t *testing.T) {
	root := writeFakeRepos(t)
	cache := t.TempDir()
	bindir := t.TempDir()
	_ = os.WriteFile(filepath.Join(bindir, "git"), []byte("#!/bin/sh\nexit 0\n"), 0o755)

	cmd := bridgeCmd("sync", "--auto")
	cmd.Env = append(os.Environ(),
		"BRIDGE_REPOS_ROOT="+root,
		"XDG_CACHE_HOME="+cache,
		"PATH="+bindir+":"+os.Getenv("PATH"),
		"BRIDGE_DAEMON_MAX_ITERATIONS=1",
	)
	if err := cmd.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cache, "bridge", "sync.json")); err != nil {
		t.Errorf("expected sync.json: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cache, "bridge", "sync.pid")); !os.IsNotExist(err) {
		t.Errorf("expected pidfile removed, stat err = %v", err)
	}
}
