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
