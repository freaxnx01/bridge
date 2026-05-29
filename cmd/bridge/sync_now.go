package main

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/freaxnx01/bridge/internal/store"
	"github.com/freaxnx01/bridge/internal/syncer"
)

func runSyncNow(ctx context.Context, cmd *cobra.Command) error {
	// Serialize concurrent sync runs. `sync now` and `sync --auto` both reach
	// here and both write sync.json; without a flock they can overlap and one
	// writer's content overwrites the other's. Block-and-wait matches the bash
	// bridge behavior. See #38.
	lock, err := store.AcquireLock(filepath.Join(cacheRoot(), "sync.lock"))
	if err != nil {
		return fmt.Errorf("sync: acquire lock: %w", err)
	}
	defer lock.Release()

	repos, err := discoverAllRoots()
	if err != nil {
		return err
	}
	s := &syncer.Syncer{}
	res := s.Run(ctx, repos)
	state := SyncState{
		LastRun:  time.Now().UTC(),
		Unpushed: s.Unpushed(ctx, repos),
	}
	for _, f := range res.Failed {
		state.Queue = append(state.Queue, f.Repo.Name)
	}
	b, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	if err := store.AtomicWrite(filepath.Join(cacheRoot(), "sync.json"), b); err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "synced %d repos (%d failed)\n", len(res.OK), len(res.Failed))
	return nil
}
