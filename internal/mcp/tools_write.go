package mcp

import (
	"context"
	"errors"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/freaxnx01/bridge/internal/forge"
)

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

type createRepoInput struct {
	Forge   string `json:"forge" jsonschema:"forge to create the repo on: github or forgejo"`
	Owner   string `json:"owner" jsonschema:"selects which account's token to use; the repo is created under the account that token belongs to, which may differ from this value"`
	Name    string `json:"name" jsonschema:"new repository name"`
	Private bool   `json:"private,omitempty" jsonschema:"create the repository as private"`
	Confirm bool   `json:"confirm,omitempty" jsonschema:"when false, returns a draft without creating; set true to create"`
}

// createRepoOutput carries the requested owner on a draft (the real one is not
// knowable without making the call) and the actual owner from the returned
// RepoRef on success, so a mismatch is visible to the caller.
type createRepoOutput struct {
	Draft   bool           `json:"draft"`
	Forge   string         `json:"forge"`
	Owner   string         `json:"owner"`
	Name    string         `json:"name"`
	Private bool           `json:"private"`
	Repo    *forge.RepoRef `json:"repo,omitempty"`
}

// handleCreateRepo creates a repository, draft-by-default. The owner input
// selects which account's token to use, not the destination: both client
// implementations POST to /user/repos, which creates under the token's own
// account and sends no owner.
func (d Deps) handleCreateRepo(ctx context.Context, _ *mcp.CallToolRequest, in createRepoInput) (*mcp.CallToolResult, createRepoOutput, error) {
	draft := createRepoOutput{
		Draft: true,
		Forge: in.Forge, Owner: in.Owner, Name: in.Name, Private: in.Private,
	}
	if !in.Confirm {
		return nil, draft, nil
	}
	client := d.ClientFor(in.Forge, in.Owner)
	if client == nil {
		return nil, createRepoOutput{}, fmt.Errorf("forge %q not configured", in.Forge)
	}
	repos, ok := client.(repoCreator)
	if !ok {
		return nil, createRepoOutput{}, fmt.Errorf("forge %q does not support creating repositories", in.Forge)
	}
	repo, err := repos.CreateRepo(ctx, in.Name, in.Private)
	if err != nil {
		// Distinct and actionable, but still wrapped so callers keep errors.Is.
		if errors.Is(err, forge.ErrRepoExists) {
			return nil, createRepoOutput{}, fmt.Errorf("repo %q already exists on %s, choose another name: %w", in.Name, in.Forge, err)
		}
		return nil, createRepoOutput{}, fmt.Errorf("create repo %s: %w", in.Name, err)
	}
	return nil, createRepoOutput{
		Draft: false,
		Forge: in.Forge, Owner: repo.Owner, Name: repo.Name, Private: repo.Visibility == "private",
		Repo: &repo,
	}, nil
}
