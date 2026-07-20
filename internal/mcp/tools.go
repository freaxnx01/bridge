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

// ForgeReader is the tier-1 surface every forge client satisfies. Deps.ClientFor
// returns this, and handlers needing more assert for one of the capability
// interfaces below.
type ForgeReader interface {
	Name() string
	ListRepos(ctx context.Context, owner string) ([]forge.RepoRef, error)
	ListOpenIssues(ctx context.Context, owner, repo string) ([]forge.Issue, error)
}

// fileReader is asserted by read_file.
type fileReader interface {
	GetFile(ctx context.Context, owner, repo, path string) (content []byte, sha string, found bool, err error)
}

// issueCreator is asserted by create_issue.
type issueCreator interface {
	CreateIssue(ctx context.Context, owner, repo, title, body string) (forge.Issue, error)
}

// repoCreator is asserted by create_repo.
type repoCreator interface {
	CreateRepo(ctx context.Context, name string, private bool) (forge.RepoRef, error)
}

// Capabilities returns the names of the MCP tools a resolved client supports.
// It reports tool names rather than method names so a caller can map the result
// directly onto what it may invoke. Returns nil for a nil reader.
//
// Write capabilities are reported regardless of Deps.ReadOnly; filtering them
// to what is actually registered is the caller's job.
func Capabilities(r ForgeReader) []string {
	if r == nil {
		return nil
	}
	capabilities := []string{"list_repos", "list_issues"}
	if _, ok := r.(fileReader); ok {
		capabilities = append(capabilities, "read_file")
	}
	if _, ok := r.(issueCreator); ok {
		capabilities = append(capabilities, "create_issue")
	}
	if _, ok := r.(repoCreator); ok {
		capabilities = append(capabilities, "create_repo")
	}
	return capabilities
}

// Deps are the injected dependencies of the MCP server. ClientFor returns a
// ready per-(forge, owner) reader (token baked in) or nil when that forge is
// unconfigured. BuildOverview produces the cross-forge status snapshot.
type Deps struct {
	ReadOnly      bool
	DefaultOwners []Target
	ClientFor     func(forgeName, owner string) ForgeReader
	BuildOverview func(ctx context.Context) (overview.Snapshot, error)
}

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
// given input: an explicit owner requires an explicit forge (an owner given
// without a forge is ambiguous across github/forgejo and is rejected rather
// than silently guessed); otherwise the configured defaults are used,
// narrowed by an optional forge.
func (d Deps) targets(in listReposInput) ([]Target, error) {
	if in.Owner != "" {
		if in.Forge == "" {
			return nil, fmt.Errorf("owner %q given without forge: specify forge (github or forgejo)", in.Owner)
		}
		return []Target{{Forge: in.Forge, Owner: in.Owner}}, nil
	}
	var out []Target
	for _, t := range d.DefaultOwners {
		if in.Forge != "" && t.Forge != in.Forge {
			continue
		}
		out = append(out, t)
	}
	return out, nil
}

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
	issues, ok := client.(issueCreator)
	if !ok {
		return nil, createIssueOutput{}, fmt.Errorf("forge %q does not support creating issues", in.Forge)
	}
	issue, err := issues.CreateIssue(ctx, in.Owner, in.Repo, in.Title, in.Body)
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
