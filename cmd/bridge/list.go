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

func reposRoot() string {
	if v := os.Getenv("BRIDGE_REPOS_ROOT"); v != "" {
		return v
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "projects", "repos")
}

func cacheRoot() string {
	if v := os.Getenv("XDG_CACHE_HOME"); v != "" {
		return filepath.Join(v, "bridge")
	}
	d, _ := store.Dir()
	return d
}

func runList(cmd *cobra.Command, args []string) error {
	root := reposRoot()
	local, err := core.DiscoverRepos(root)
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
	owners := uniqueOwners(local)
	var all []forge.RepoRef
	var firstErr error
	if api := os.Getenv("BRIDGE_GITHUB_API"); api != "" || os.Getenv("GH_TOKEN") != "" {
		c := forge.NewGithubClient(os.Getenv("GH_TOKEN"), api)
		for _, o := range owners["github"] {
			r, err := c.ListRepos(ctx, o)
			if err != nil {
				if firstErr == nil {
					firstErr = err
				}
				continue
			}
			all = append(all, r...)
		}
	}
	if api := os.Getenv("BRIDGE_GITLAB_API"); api != "" || os.Getenv("GITLAB_TOKEN") != "" {
		c := forge.NewGitlabClient(os.Getenv("GITLAB_TOKEN"), api)
		for _, o := range owners["gitlab"] {
			r, err := c.ListRepos(ctx, o)
			if err != nil {
				if firstErr == nil {
					firstErr = err
				}
				continue
			}
			all = append(all, r...)
		}
	}
	if api := os.Getenv("BRIDGE_FORGEJO_API"); api != "" || os.Getenv("FORGEJO_TOKEN") != "" {
		c := forge.NewForgejoClient(os.Getenv("FORGEJO_TOKEN"), api)
		for _, o := range owners["forgejo"] {
			r, err := c.ListRepos(ctx, o)
			if err != nil {
				if firstErr == nil {
					firstErr = err
				}
				continue
			}
			all = append(all, r...)
		}
	}
	_ = forge.WriteRepoCache(cachePath, forge.RepoCache{UpdatedAt: time.Now(), Repos: all})
	return all, firstErr
}

func uniqueOwners(local []core.Repo) map[string][]string {
	seen := map[string]map[string]bool{}
	for _, r := range local {
		if seen[r.Forge] == nil {
			seen[r.Forge] = map[string]bool{}
		}
		seen[r.Forge][r.Owner] = true
	}
	out := map[string][]string{}
	for forge, owners := range seen {
		for o := range owners {
			out[forge] = append(out[forge], o)
		}
	}
	return out
}
