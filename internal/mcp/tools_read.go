package mcp

import (
	"context"
	"fmt"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/sync/errgroup"

	"github.com/freaxnx01/bridge/internal/forge"
	"github.com/freaxnx01/bridge/internal/overview"
)

type listReposInput struct {
	Forge string `json:"forge,omitempty" jsonschema:"optional forge filter: github or forgejo"`
	Owner string `json:"owner,omitempty" jsonschema:"optional owner filter; overrides the configured default owners"`
}

type listReposOutput struct {
	Repos []forge.RepoRef `json:"repos"`
	// Warnings reports targets that were skipped (forge unconfigured) or
	// failed (e.g. an expired token) instead of failing the whole call —
	// results from any other, healthy target are still returned in Repos.
	Warnings []string `json:"warnings,omitempty"`
}

type readFileInput struct {
	Forge string `json:"forge" jsonschema:"forge hosting the repo: github or forgejo"`
	Owner string `json:"owner" jsonschema:"repository owner"`
	Repo  string `json:"repo" jsonschema:"repository name"`
	Path  string `json:"path" jsonschema:"file path within the repo (default branch)"`
}

type readFileOutput struct {
	Content string `json:"content"`
	SHA     string `json:"sha"`
	Found   bool   `json:"found"`
}

type crossForgeStatusInput struct{}

// handleListRepos aggregates repos across all matching targets concurrently.
// A target that is unconfigured (ClientFor returns nil) or whose ListRepos
// call fails does not fail the whole call: it is recorded in Warnings and
// the results from any other, healthy target are still returned.
func (d Deps) handleListRepos(ctx context.Context, _ *mcp.CallToolRequest, in listReposInput) (*mcp.CallToolResult, listReposOutput, error) {
	targets, err := d.targets(in)
	if err != nil {
		return nil, listReposOutput{}, err
	}
	var (
		mu       sync.Mutex
		all      []forge.RepoRef
		warnings []string
	)
	var g errgroup.Group
	for _, t := range targets {
		t := t
		g.Go(func() error {
			client := d.ClientFor(t.Forge, t.Owner)
			if client == nil {
				mu.Lock()
				warnings = append(warnings, fmt.Sprintf("%s:%s not configured (missing token or forge unavailable)", t.Forge, t.Owner))
				mu.Unlock()
				return nil
			}
			repos, err := client.ListRepos(ctx, t.Owner)
			if err != nil {
				mu.Lock()
				warnings = append(warnings, fmt.Sprintf("list repos %s/%s: %v", t.Forge, t.Owner, err))
				mu.Unlock()
				return nil
			}
			mu.Lock()
			all = append(all, repos...)
			mu.Unlock()
			return nil
		})
	}
	_ = g.Wait() // per-target failures are captured as warnings above; Go funcs always return nil
	return nil, listReposOutput{Repos: all, Warnings: warnings}, nil
}

func (d Deps) handleReadFile(ctx context.Context, _ *mcp.CallToolRequest, in readFileInput) (*mcp.CallToolResult, readFileOutput, error) {
	client := d.ClientFor(in.Forge, in.Owner)
	if client == nil {
		return nil, readFileOutput{}, fmt.Errorf("forge %q not configured", in.Forge)
	}
	files, ok := client.(fileReader)
	if !ok {
		return nil, readFileOutput{}, fmt.Errorf("forge %q does not support reading files", in.Forge)
	}
	content, sha, found, err := files.GetFile(ctx, in.Owner, in.Repo, in.Path)
	if err != nil {
		return nil, readFileOutput{}, fmt.Errorf("read %s/%s/%s: %w", in.Owner, in.Repo, in.Path, err)
	}
	return nil, readFileOutput{Content: string(content), SHA: sha, Found: found}, nil
}

func (d Deps) handleCrossForgeStatus(ctx context.Context, _ *mcp.CallToolRequest, _ crossForgeStatusInput) (*mcp.CallToolResult, overview.Snapshot, error) {
	snap, err := d.BuildOverview(ctx)
	if err != nil {
		return nil, overview.Snapshot{}, fmt.Errorf("build cross-forge status: %w", err)
	}
	return nil, snap, nil
}

type listIssuesInput struct {
	Forge string `json:"forge" jsonschema:"forge hosting the repo: github or forgejo"`
	Owner string `json:"owner" jsonschema:"repository owner"`
	Repo  string `json:"repo" jsonschema:"repository name"`
}

type listIssuesOutput struct {
	Issues []forge.Issue `json:"issues"`
}

// handleListIssues returns the open issues of a single repo. Scope is
// deliberately one repo rather than a fan-out across configured targets:
// cross_forge_status already aggregates, and fanning out here would multiply
// to repos × issues per call. Needs no capability assertion — ListOpenIssues
// is part of ForgeReader.
func (d Deps) handleListIssues(ctx context.Context, _ *mcp.CallToolRequest, in listIssuesInput) (*mcp.CallToolResult, listIssuesOutput, error) {
	client := d.ClientFor(in.Forge, in.Owner)
	if client == nil {
		return nil, listIssuesOutput{}, fmt.Errorf("forge %q not configured", in.Forge)
	}
	issues, err := client.ListOpenIssues(ctx, in.Owner, in.Repo)
	if err != nil {
		return nil, listIssuesOutput{}, fmt.Errorf("list issues %s/%s: %w", in.Owner, in.Repo, err)
	}
	return nil, listIssuesOutput{Issues: issues}, nil
}

type listGitForgesInput struct{}

// forgeStatus describes one configured (forge, owner) target. Capabilities and
// Reason are mutually exclusive: an unconfigured target has a reason and no
// capabilities, a configured one the reverse.
type forgeStatus struct {
	Forge        string   `json:"forge"`
	Owner        string   `json:"owner"`
	Configured   bool     `json:"configured"`
	Capabilities []string `json:"capabilities,omitempty"`
	Reason       string   `json:"reason,omitempty"`
}

type listGitForgesOutput struct {
	Forges   []forgeStatus `json:"forges"`
	ReadOnly bool          `json:"read_only"`
}

// isWriteTool reports whether a tool name is registered only when
// Deps.ReadOnly is false. A function rather than a package-level map so there
// is no mutable global state.
func isWriteTool(name string) bool {
	switch name {
	case "create_issue", "create_repo":
		return true
	default:
		return false
	}
}

// advertisedCapabilities is Capabilities narrowed to what this server actually
// registered. Capabilities reports write tools regardless of ReadOnly by
// design, so filtering them here keeps list_git_forges from advertising a tool
// a read-only server never registered.
func (d Deps) advertisedCapabilities(r ForgeReader) []string {
	all := Capabilities(r)
	if !d.ReadOnly {
		return all
	}
	reads := make([]string, 0, len(all))
	for _, c := range all {
		if !isWriteTool(c) {
			reads = append(reads, c)
		}
	}
	return reads
}

// handleListGitForges reports the configured targets so a client does not have
// to guess a (forge, owner) pair. It makes no network requests: ClientFor is
// wrapped by a resolve-once cache, so after the first resolution per target
// this is a map lookup. A live API probe was rejected — it would turn
// discovery into N round-trips and conflate "not configured" with a transient
// API failure.
func (d Deps) handleListGitForges(_ context.Context, _ *mcp.CallToolRequest, _ listGitForgesInput) (*mcp.CallToolResult, listGitForgesOutput, error) {
	forges := make([]forgeStatus, 0, len(d.DefaultOwners))
	for _, t := range d.DefaultOwners {
		status := forgeStatus{Forge: t.Forge, Owner: t.Owner}
		if client := d.ClientFor(t.Forge, t.Owner); client != nil {
			status.Configured = true
			status.Capabilities = d.advertisedCapabilities(client)
		} else {
			// Same wording as handleListRepos's warning, so the two tools
			// describe the same condition identically.
			status.Reason = "missing token or forge unavailable"
		}
		forges = append(forges, status)
	}
	return nil, listGitForgesOutput{Forges: forges, ReadOnly: d.ReadOnly}, nil
}
