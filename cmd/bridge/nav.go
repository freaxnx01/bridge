package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/spf13/cobra"

	"github.com/freaxnx01/bridge/internal/agents"
	"github.com/freaxnx01/bridge/internal/core"
	"github.com/freaxnx01/bridge/internal/forge"
	"github.com/freaxnx01/bridge/internal/nav"
	"github.com/freaxnx01/bridge/internal/navdata"
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
			RecentPath:   filepath.Join(cacheRoot(), "mru"),
			DefaultAgent: os.Getenv("BRIDGE_DEFAULT_AGENT"),
			AgentArgs:    strings.Fields(os.Getenv("BRIDGE_DEFAULT_AGENT_ARGS")),
			Backend:      selectBackend(os.Getenv, os.Stderr),
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
			CreateRepo: func(name, forgeName string, private bool) (core.Repo, error) {
				repo, _, err := createAndClone(context.Background(), name, forgeName, private)
				return repo, err
			},
			FetchIssues: newIssueFetcher(issueClientResolver(clientFor, clientForMCP(reposRoots()))),
			FetchRemote: func(ctx context.Context) ([]forge.RepoRef, error) {
				return remote.Refresh(ctx, reposRoots(), filepath.Join(cacheRoot(), "remote.list"))
			},
			IssueCacheDir: filepath.Join(cacheRoot(), "issues"),
			Environment:   os.Getenv("BRIDGE_ENV"),
			BuildOverview: func(ctx context.Context) (overview.Snapshot, error) {
				repos := overviewRepos()
				return overview.Build(ctx, overview.Config{
					Environment: os.Getenv("BRIDGE_ENV"),
					Repos:       repos,
					IdeasLabDir: ideasLabDir(),
					FetchIssues: func(ctx context.Context) ([]overview.Issue, error) {
						return fetchAllOpenIssues(ctx, repos)
					},
					FetchRoadmap: roadmapFetcher(),
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

// newIssueFetcher returns the navdata.IssueSource fetcher nav and `bridge
// bot` share: it lists a repo's open issues with the client resolve picks for
// (forge, owner). No client is navdata.ErrNoForgeClient — never "no issues",
// which would be cached as fresh and hide the repo's issues for the TTL.
func newIssueFetcher(resolve func(forgeName, owner string) forge.Client) func(ctx context.Context, forgeName, owner, repo string) ([]forge.Issue, error) {
	return func(ctx context.Context, forgeName, owner, repo string) ([]forge.Issue, error) {
		c := resolve(forgeName, owner)
		if c == nil {
			return nil, fmt.Errorf("%s/%s: %w", forgeName, owner, navdata.ErrNoForgeClient)
		}
		return c.ListOpenIssues(ctx, owner, repo)
	}
}

// issueClientResolver picks the issue client for (forge, owner): fromEnv
// (clientFor: GH_TOKEN/FORGEJO_TOKEN/… in the process env) wins; otherwise
// fromDirenv (clientForMCP: the token from the forge's direnv scope, like the
// MCP server), so a service without tokens in its env still finds them. The
// direnv lookup walks the roots and spawns direnv, so a found client is
// memoized per forge:owner — until its ListOpenIssues gets a 401/403 (rotated
// or revoked token), which drops it so the next call re-resolves. nil is never
// memoized: a missing token or a transient direnv failure is retried.
func issueClientResolver(fromEnv func(forgeName string) forge.Client, fromDirenv func(forgeName, owner string) forge.Client) func(forgeName, owner string) forge.Client {
	var (
		mu    sync.Mutex
		cache = map[string]forge.Client{}
	)
	return func(forgeName, owner string) forge.Client {
		if c := fromEnv(forgeName); c != nil {
			return c
		}
		key := forgeName + ":" + owner
		mu.Lock()
		defer mu.Unlock()
		if c, ok := cache[key]; ok {
			return c
		}
		c := fromDirenv(forgeName, owner)
		if c == nil {
			return nil
		}
		w := &dropOnAuthError{Client: c}
		w.drop = func() {
			mu.Lock()
			defer mu.Unlock()
			if cache[key] == forge.Client(w) {
				delete(cache, key)
			}
		}
		cache[key] = w
		return w
	}
}

// dropOnAuthError calls drop when ListOpenIssues fails with a 401/403.
type dropOnAuthError struct {
	forge.Client
	drop func()
}

func (d *dropOnAuthError) ListOpenIssues(ctx context.Context, owner, repo string) ([]forge.Issue, error) {
	is, err := d.Client.ListOpenIssues(ctx, owner, repo)
	if forge.IsAuthError(err) {
		d.drop()
	}
	return is, err
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

// roadmapFetcher returns a FetchRoadmap callback for the board named by
// BRIDGE_PROJECT ("owner/number"), or nil when unset/malformed (roadmap tier
// disabled). The token comes from BRIDGE_PROJECT_TOKEN, falling back to
// GH_TOKEN — it must carry the `project` scope.
func roadmapFetcher() func(ctx context.Context) ([]overview.RoadmapItem, error) {
	owner, number, ok := parseProjectRef(os.Getenv("BRIDGE_PROJECT"))
	if !ok {
		return nil
	}
	tok := os.Getenv("BRIDGE_PROJECT_TOKEN")
	if tok == "" {
		tok = os.Getenv("GH_TOKEN")
	}
	return func(ctx context.Context) ([]overview.RoadmapItem, error) {
		c := forge.NewGithubClient(tok, os.Getenv("BRIDGE_GITHUB_API"))
		items, err := c.ListProjectV2Items(ctx, owner, number)
		if err != nil {
			return nil, err
		}
		out := make([]overview.RoadmapItem, 0, len(items))
		for _, it := range items {
			out = append(out, overview.RoadmapItem{
				Repo:   it.Repo,
				Title:  it.Title,
				URL:    it.URL,
				Status: it.Status,
			})
		}
		return out, nil
	}
}

// parseProjectRef parses "owner/number" (e.g. "freaxnx01/5").
func parseProjectRef(s string) (owner string, number int, ok bool) {
	owner, num, found := strings.Cut(s, "/")
	if !found || owner == "" {
		return "", 0, false
	}
	n, err := strconv.Atoi(num)
	if err != nil || n <= 0 {
		return "", 0, false
	}
	return owner, n, true
}
