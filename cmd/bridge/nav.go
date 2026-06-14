package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/freaxnx01/bridge/internal/agents"
	"github.com/freaxnx01/bridge/internal/core"
	"github.com/freaxnx01/bridge/internal/forge"
	"github.com/freaxnx01/bridge/internal/nav"
	"github.com/freaxnx01/bridge/internal/overview"
	"github.com/freaxnx01/bridge/internal/remote"
)

var navOnce bool

var navCmd = &cobra.Command{
	Use:   "nav",
	Short: "Interactive navigator: pick a repo, then manage its sessions & worktrees",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg := nav.Config{
			ReposRoots:   reposRoots(),
			RemoteCache:  filepath.Join(cacheRoot(), "remote.list"),
			SlotsPath:    filepath.Join(cacheRoot(), "slots.json"),
			DefaultAgent: os.Getenv("BRIDGE_DEFAULT_AGENT"),
			AgentArgs:    strings.Fields(os.Getenv("BRIDGE_DEFAULT_AGENT_ARGS")),
			Version:      version,
			DebugKeys:    navDebugPath(),
			Once:         navOnce,
			NameArgs: func(agent string, repo core.Repo, wt, label string) []string {
				// Reuse the open path's claude labelling: prepend -n "<label>" and
				// install the relabel hook so /clear keeps the name. label is empty
				// for normal launches (default "<repo> [<wt>]") and "#123 [<title>]"
				// for issue launches.
				if label == "" {
					label = displayName(repo, wt)
				}
				spec := withClaudeNameLabel(agents.AgentSpec{Name: agent}, label)
				ensureClaudeRelabelLabel(agents.AgentSpec{Name: agent}, label)
				return spec.Args
			},
			Clone: func(ref forge.RepoRef) (core.Repo, error) {
				dir, err := cloneRemoteRepo(ref)
				if err != nil {
					return core.Repo{}, err
				}
				return repoFromClonedRef(reposRoot(), ref, dir), nil
			},
			FetchIssues: func(ctx context.Context, forgeName, owner, repo string) ([]forge.Issue, error) {
				c := clientFor(forgeName)
				if c == nil {
					return nil, nil
				}
				return c.ListOpenIssues(ctx, owner, repo)
			},
			FetchRemote: func(ctx context.Context) ([]forge.RepoRef, error) {
				return remote.Refresh(ctx, reposRoots(), filepath.Join(cacheRoot(), "remote.list"))
			},
			IssueCacheDir: filepath.Join(cacheRoot(), "issues"),
			Environment:   os.Getenv("BRIDGE_ENV"),
			BuildOverview: func(ctx context.Context) (overview.Snapshot, error) {
				return overview.Build(ctx, overview.Config{
					Environment: os.Getenv("BRIDGE_ENV"),
					Repos:       overviewRepos(),
					IdeasLabDir: ideasLabDir(),
					FetchIssues: func(ctx context.Context) ([]overview.Issue, error) {
						return fetchAllOpenIssues(ctx, overviewRepos())
					},
					FetchRoadmap: nil, // GitHub Projects v2 provider — Plan 1b
				})
			},
		}
		return nav.Run(cfg)
	},
}

func init() {
	navCmd.Flags().BoolVar(&navOnce, "once", false, "render one frame to stdout and exit (smoke test, no TTY)")
	rootCmd.AddCommand(navCmd)
}

// navDebugPath resolves BRIDGE_NAV_DEBUG into a key-log file path. "1"/"true"/
// "yes" map to a default temp file; any other non-empty value is used verbatim.
func navDebugPath() string {
	switch v := os.Getenv("BRIDGE_NAV_DEBUG"); v {
	case "":
		return ""
	case "1", "true", "yes":
		return filepath.Join(os.TempDir(), "bridge-nav-keys.log")
	default:
		return v
	}
}

// overviewRepos returns the repos discovered across all configured roots, the
// set the cross-repo overview aggregates.
func overviewRepos() []core.Repo {
	repos, _ := discoverAllRoots()
	return repos
}

// ideasLabDir resolves the ideas-lab idea directory from BRIDGE_IDEAS_LAB
// (pointing at the ideas-lab repo's ideas/ folder). Empty disables that source.
func ideasLabDir() string {
	return os.Getenv("BRIDGE_IDEAS_LAB")
}

// fetchAllOpenIssues pulls open issues for every repo via the per-forge client,
// adapting forge.Issue to overview.Issue. A repo whose client/listing fails is
// skipped (best-effort, like the rest of nav's forge reads).
func fetchAllOpenIssues(ctx context.Context, repos []core.Repo) ([]overview.Issue, error) {
	var out []overview.Issue
	for _, r := range repos {
		c := clientFor(r.Forge)
		if c == nil {
			continue
		}
		issues, err := c.ListOpenIssues(ctx, r.Owner, r.Name)
		if err != nil {
			continue
		}
		for _, is := range issues {
			out = append(out, overview.Issue{
				Repo:    r.Owner + "/" + r.Name,
				Title:   is.Title,
				URL:     is.URL,
				Labels:  is.Labels,
				Updated: is.Updated,
			})
		}
	}
	return out, nil
}
