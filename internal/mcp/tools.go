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

// Target is a (forge, owner) pair queried by list_repos when no owner is given
// in the tool input.
type Target struct {
	Forge string
	Owner string
}

// forgeClient is the consumer-side interface the MCP tools need. Both
// *forge.GithubClient and *forge.ForgejoClient satisfy it structurally.
type forgeClient interface {
	Name() string
	ListRepos(ctx context.Context, owner string) ([]forge.RepoRef, error)
	GetFile(ctx context.Context, owner, repo, path string) (content []byte, sha string, found bool, err error)
	CreateIssue(ctx context.Context, owner, repo, title, body string) (forge.Issue, error)
}

// Deps are the injected dependencies of the MCP server. ClientFor returns a
// ready per-(forge, owner) client (token baked in) or nil when that forge is
// unconfigured. BuildOverview produces the cross-forge status snapshot.
type Deps struct {
	ReadOnly      bool
	DefaultOwners []Target
	ClientFor     func(forgeName, owner string) forgeClient
	BuildOverview func(ctx context.Context) (overview.Snapshot, error)
}

type listReposInput struct {
	Forge string `json:"forge,omitempty" jsonschema:"optional forge filter: github or forgejo"`
	Owner string `json:"owner,omitempty" jsonschema:"optional owner filter; overrides the configured default owners"`
}

type listReposOutput struct {
	Repos []forge.RepoRef `json:"repos"`
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

type createIssueInput struct {
	Forge   string `json:"forge" jsonschema:"forge hosting the repo: github or forgejo"`
	Owner   string `json:"owner" jsonschema:"repository owner"`
	Repo    string `json:"repo" jsonschema:"repository name"`
	Title   string `json:"title" jsonschema:"issue title"`
	Body    string `json:"body,omitempty" jsonschema:"issue body (markdown)"`
	Confirm bool   `json:"confirm,omitempty" jsonschema:"when false, returns a draft without creating; set true to create"`
}

type createIssueOutput struct {
	Draft bool         `json:"draft"`
	Forge string       `json:"forge"`
	Owner string       `json:"owner"`
	Repo  string       `json:"repo"`
	Title string       `json:"title"`
	Body  string       `json:"body,omitempty"`
	Issue *forge.Issue `json:"issue,omitempty"`
}

type crossForgeStatusInput struct{}

// targets returns the (forge, owner) pairs list_repos should query for the
// given input: an explicit owner (with optional forge) overrides the defaults;
// otherwise the configured defaults are used, narrowed by an optional forge.
func (d Deps) targets(in listReposInput) []Target {
	if in.Owner != "" {
		forgeName := in.Forge
		if forgeName == "" {
			forgeName = "github"
		}
		return []Target{{Forge: forgeName, Owner: in.Owner}}
	}
	var out []Target
	for _, t := range d.DefaultOwners {
		if in.Forge != "" && t.Forge != in.Forge {
			continue
		}
		out = append(out, t)
	}
	return out
}

func (d Deps) handleListRepos(ctx context.Context, _ *mcp.CallToolRequest, in listReposInput) (*mcp.CallToolResult, listReposOutput, error) {
	targets := d.targets(in)
	var (
		mu  sync.Mutex
		all []forge.RepoRef
	)
	g, gctx := errgroup.WithContext(ctx)
	for _, t := range targets {
		t := t
		client := d.ClientFor(t.Forge, t.Owner)
		if client == nil {
			continue
		}
		g.Go(func() error {
			repos, err := client.ListRepos(gctx, t.Owner)
			if err != nil {
				return fmt.Errorf("list repos %s/%s: %w", t.Forge, t.Owner, err)
			}
			mu.Lock()
			all = append(all, repos...)
			mu.Unlock()
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, listReposOutput{}, err
	}
	return nil, listReposOutput{Repos: all}, nil
}

func (d Deps) handleReadFile(ctx context.Context, _ *mcp.CallToolRequest, in readFileInput) (*mcp.CallToolResult, readFileOutput, error) {
	client := d.ClientFor(in.Forge, in.Owner)
	if client == nil {
		return nil, readFileOutput{}, fmt.Errorf("forge %q not configured", in.Forge)
	}
	content, sha, found, err := client.GetFile(ctx, in.Owner, in.Repo, in.Path)
	if err != nil {
		return nil, readFileOutput{}, fmt.Errorf("read %s/%s/%s: %w", in.Owner, in.Repo, in.Path, err)
	}
	return nil, readFileOutput{Content: string(content), SHA: sha, Found: found}, nil
}

func (d Deps) handleCreateIssue(ctx context.Context, _ *mcp.CallToolRequest, in createIssueInput) (*mcp.CallToolResult, createIssueOutput, error) {
	draft := createIssueOutput{
		Draft: true,
		Forge: in.Forge, Owner: in.Owner, Repo: in.Repo, Title: in.Title, Body: in.Body,
	}
	if !in.Confirm {
		return nil, draft, nil
	}
	client := d.ClientFor(in.Forge, in.Owner)
	if client == nil {
		return nil, createIssueOutput{}, fmt.Errorf("forge %q not configured", in.Forge)
	}
	issue, err := client.CreateIssue(ctx, in.Owner, in.Repo, in.Title, in.Body)
	if err != nil {
		return nil, createIssueOutput{}, fmt.Errorf("create issue %s/%s: %w", in.Owner, in.Repo, err)
	}
	return nil, createIssueOutput{
		Draft: false,
		Forge: in.Forge, Owner: in.Owner, Repo: in.Repo, Title: in.Title, Body: in.Body,
		Issue: &issue,
	}, nil
}

func (d Deps) handleCrossForgeStatus(ctx context.Context, _ *mcp.CallToolRequest, _ crossForgeStatusInput) (*mcp.CallToolResult, overview.Snapshot, error) {
	snap, err := d.BuildOverview(ctx)
	if err != nil {
		return nil, overview.Snapshot{}, fmt.Errorf("build cross-forge status: %w", err)
	}
	return nil, snap, nil
}
