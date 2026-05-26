package main

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/freaxnx01/bridge/internal/core"
	"github.com/freaxnx01/bridge/internal/store"
	"github.com/freaxnx01/bridge/internal/syncer"
)

func runSyncNow(ctx context.Context, cmd *cobra.Command) error {
	repos, err := core.DiscoverRepos(reposRoot())
	if err != nil {
		return err
	}
	s := &syncer.Syncer{}
	res := s.Run(ctx, repos)
	state := SyncState{
		LastRun: time.Now().UTC(),
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
