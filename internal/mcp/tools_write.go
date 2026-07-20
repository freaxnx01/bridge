package mcp

import (
	"context"
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
