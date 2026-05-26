package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/freaxnx01/bridge/internal/store"
)

// TestSyncNowBlocksOnLock verifies that a second runSyncNow call waits for an
// external holder of sync.lock to release before proceeding. Regression test
// for #38 — without the flock, concurrent `sync now` and `sync --auto` races
// could overwrite each other's sync.json.
func TestSyncNowBlocksOnLock(t *testing.T) {
	root := writeFakeRepos(t)
	cache := t.TempDir()
	bindir := t.TempDir()
	_ = os.WriteFile(filepath.Join(bindir, "git"), []byte("#!/bin/sh\nexit 0\n"), 0o755)

	t.Setenv("BRIDGE_REPOS_ROOT", root)
	t.Setenv("XDG_CACHE_HOME", cache)
	t.Setenv("PATH", bindir+":"+os.Getenv("PATH"))

	// Pre-create the cache dir so AcquireLock can attach the lock file.
	bridgeCache := filepath.Join(cache, "bridge")
	_ = os.MkdirAll(bridgeCache, 0o755)

	lock, err := store.AcquireLock(filepath.Join(bridgeCache, "sync.lock"))
	if err != nil {
		t.Fatalf("pre-acquire lock: %v", err)
	}

	done := make(chan error, 1)
	var buf bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&buf)
	go func() {
		done <- runSyncNow(context.Background(), cmd)
	}()

	// Should block as long as we hold the lock.
	select {
	case err := <-done:
		t.Fatalf("runSyncNow returned while lock held: err=%v", err)
	case <-time.After(150 * time.Millisecond):
	}

	if err := lock.Release(); err != nil {
		t.Fatalf("release: %v", err)
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runSyncNow after release: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("runSyncNow did not complete after lock release")
	}
}
