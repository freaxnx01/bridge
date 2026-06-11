package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/spf13/cobra"

	"github.com/freaxnx01/bridge/internal/core"
	"github.com/freaxnx01/bridge/internal/forge"
	"github.com/freaxnx01/bridge/internal/remote"
	"github.com/freaxnx01/bridge/internal/store"
)

var (
	listJSON    bool
	listRemote  bool
	listRefresh bool
)

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List local repos (and optionally remote)",
	RunE:  runList,
}

func init() {
	listCmd.Flags().BoolVar(&listJSON, "json", false, "machine-readable output")
	listCmd.Flags().BoolVarP(&listRemote, "remote", "r", false, "include remote listings")
	listCmd.Flags().BoolVar(&listRefresh, "refresh", false, "force refresh of remote cache")
	rootCmd.AddCommand(listCmd)
}

func cacheRoot() string {
	if v := os.Getenv("XDG_CACHE_HOME"); v != "" {
		return filepath.Join(v, "bridge")
	}
	d, _ := store.Dir()
	return d
}

func runList(cmd *cobra.Command, args []string) error {
	local, err := discoverAllRoots()
	if err != nil {
		return fmt.Errorf("discover: %w", err)
	}
	sort.Slice(local, func(i, j int) bool { return local[i].Path < local[j].Path })

	if !listRemote {
		if listJSON {
			return emitJSON(cmd.OutOrStdout(), local)
		}
		for _, r := range local {
			vis := r.Visibility
			if vis == "" {
				vis = "-"
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%-12s %-12s %-8s %s\n", r.Forge, r.Owner, vis, r.Name)
		}
		return nil
	}

	remote, err := loadOrFetchRemote(cmd.Context(), local, listRefresh)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: remote fetch failed, using cache: %v\n", err)
	}
	if listJSON {
		return emitJSON(cmd.OutOrStdout(), struct {
			Local  []core.Repo     `json:"local"`
			Remote []forge.RepoRef `json:"remote"`
		}{local, remote})
	}
	fmt.Fprintln(cmd.OutOrStdout(), "# local")
	for _, r := range local {
		fmt.Fprintf(cmd.OutOrStdout(), "%-12s %-12s %s\n", r.Forge, r.Owner, r.Name)
	}
	fmt.Fprintln(cmd.OutOrStdout(), "# remote")
	for _, r := range remote {
		fmt.Fprintf(cmd.OutOrStdout(), "%-12s %-12s %s\n", r.Forge, r.Owner, r.Name)
	}
	return nil
}

const remoteTTL = time.Hour

func loadOrFetchRemote(ctx context.Context, local []core.Repo, refresh bool) ([]forge.RepoRef, error) {
	cachePath := filepath.Join(cacheRoot(), "remote.list")
	if !refresh {
		c, err := forge.ReadRepoCache(cachePath)
		if err == nil && !c.IsStale(remoteTTL) && len(c.Repos) > 0 {
			return c.Repos, nil
		}
	}
	return remote.Refresh(ctx, reposRoots(), cachePath)
}

func dirExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

func fileExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}
