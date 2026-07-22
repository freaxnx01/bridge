package mcp

import (
	"context"
	"errors"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/freaxnx01/bridge/internal/audit"
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
		d.auditLog(audit.Entry{Forge: in.Forge, Owner: in.Owner, Repo: in.Repo, Tool: "create_issue", Confirm: true, Outcome: "error"})
		return nil, createIssueOutput{}, fmt.Errorf("create issue %s/%s: %w", in.Owner, in.Repo, err)
	}
	d.auditLog(audit.Entry{Forge: in.Forge, Owner: in.Owner, Repo: in.Repo, Tool: "create_issue", Confirm: true, Outcome: "success"})
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
		d.auditLog(audit.Entry{Forge: in.Forge, Owner: in.Owner, Repo: in.Name, Tool: "create_repo", Confirm: true, Outcome: "error"})
		// Distinct and actionable, but still wrapped so callers keep errors.Is.
		if errors.Is(err, forge.ErrRepoExists) {
			return nil, createRepoOutput{}, fmt.Errorf("repo %q already exists on %s, choose another name: %w", in.Name, in.Forge, err)
		}
		return nil, createRepoOutput{}, fmt.Errorf("create repo %s: %w", in.Name, err)
	}
	d.auditLog(audit.Entry{Forge: in.Forge, Owner: repo.Owner, Repo: repo.Name, Tool: "create_repo", Confirm: true, Outcome: "success"})
	return nil, createRepoOutput{
		Draft: false,
		Forge: in.Forge, Owner: repo.Owner, Name: repo.Name, Private: repo.Visibility == "private",
		Repo: &repo,
	}, nil
}

type closeIssueInput struct {
	Forge       string `json:"forge" jsonschema:"forge hosting the repo: github or forgejo"`
	Owner       string `json:"owner" jsonschema:"repository owner"`
	Repo        string `json:"repo" jsonschema:"repository name"`
	IssueNumber int    `json:"issue_number" jsonschema:"issue number to close"`
	StateReason string `json:"state_reason,omitempty" jsonschema:"GitHub only: completed, not_planned, or duplicate; ignored on Forgejo"`
	Confirm     bool   `json:"confirm,omitempty" jsonschema:"when false, returns a draft without closing; set true to close"`
}

type closeIssueOutput struct {
	Draft       bool         `json:"draft"`
	Forge       string       `json:"forge"`
	Owner       string       `json:"owner"`
	Repo        string       `json:"repo"`
	IssueNumber int          `json:"issue_number"`
	Issue       *forge.Issue `json:"issue,omitempty"`
}

func (d Deps) handleCloseIssue(ctx context.Context, _ *mcp.CallToolRequest, in closeIssueInput) (*mcp.CallToolResult, closeIssueOutput, error) {
	draft := closeIssueOutput{
		Draft: true,
		Forge: in.Forge, Owner: in.Owner, Repo: in.Repo, IssueNumber: in.IssueNumber,
	}
	if !in.Confirm {
		return nil, draft, nil
	}
	client := d.ClientFor(in.Forge, in.Owner)
	if client == nil {
		return nil, closeIssueOutput{}, fmt.Errorf("forge %q not configured", in.Forge)
	}
	closer, ok := client.(issueCloser)
	if !ok {
		return nil, closeIssueOutput{}, fmt.Errorf("forge %q does not support closing issues", in.Forge)
	}
	issue, err := closer.CloseIssue(ctx, in.Owner, in.Repo, in.IssueNumber, in.StateReason)
	if err != nil {
		d.auditLog(audit.Entry{Forge: in.Forge, Owner: in.Owner, Repo: in.Repo, Tool: "close_issue", Confirm: true, Outcome: "error"})
		return nil, closeIssueOutput{}, fmt.Errorf("close issue %s/%s#%d: %w", in.Owner, in.Repo, in.IssueNumber, err)
	}
	d.auditLog(audit.Entry{Forge: in.Forge, Owner: in.Owner, Repo: in.Repo, Tool: "close_issue", Confirm: true, Outcome: "success"})
	return nil, closeIssueOutput{
		Draft: false,
		Forge: in.Forge, Owner: in.Owner, Repo: in.Repo, IssueNumber: in.IssueNumber,
		Issue: &issue,
	}, nil
}

type updateIssueInput struct {
	Forge       string `json:"forge" jsonschema:"forge hosting the repo: github or forgejo"`
	Owner       string `json:"owner" jsonschema:"repository owner"`
	Repo        string `json:"repo" jsonschema:"repository name"`
	IssueNumber int    `json:"issue_number" jsonschema:"issue number to update"`
	Title       string `json:"title,omitempty" jsonschema:"new title; at least one of title/body is required"`
	Body        string `json:"body,omitempty" jsonschema:"new body (markdown); at least one of title/body is required"`
	Confirm     bool   `json:"confirm,omitempty" jsonschema:"when false, returns a draft without updating; set true to update"`
}

type updateIssueOutput struct {
	Draft       bool         `json:"draft"`
	Forge       string       `json:"forge"`
	Owner       string       `json:"owner"`
	Repo        string       `json:"repo"`
	IssueNumber int          `json:"issue_number"`
	Title       string       `json:"title,omitempty"`
	Body        string       `json:"body,omitempty"`
	Issue       *forge.Issue `json:"issue,omitempty"`
}

func (d Deps) handleUpdateIssue(ctx context.Context, _ *mcp.CallToolRequest, in updateIssueInput) (*mcp.CallToolResult, updateIssueOutput, error) {
	if in.Title == "" && in.Body == "" {
		return nil, updateIssueOutput{}, fmt.Errorf("update issue %s/%s#%d: at least one of title or body is required", in.Owner, in.Repo, in.IssueNumber)
	}
	draft := updateIssueOutput{
		Draft: true,
		Forge: in.Forge, Owner: in.Owner, Repo: in.Repo, IssueNumber: in.IssueNumber,
		Title: in.Title, Body: in.Body,
	}
	if !in.Confirm {
		return nil, draft, nil
	}
	client := d.ClientFor(in.Forge, in.Owner)
	if client == nil {
		return nil, updateIssueOutput{}, fmt.Errorf("forge %q not configured", in.Forge)
	}
	updater, ok := client.(issueUpdater)
	if !ok {
		return nil, updateIssueOutput{}, fmt.Errorf("forge %q does not support updating issues", in.Forge)
	}
	var titlePtr, bodyPtr *string
	if in.Title != "" {
		titlePtr = &in.Title
	}
	if in.Body != "" {
		bodyPtr = &in.Body
	}
	issue, err := updater.UpdateIssue(ctx, in.Owner, in.Repo, in.IssueNumber, titlePtr, bodyPtr)
	if err != nil {
		d.auditLog(audit.Entry{Forge: in.Forge, Owner: in.Owner, Repo: in.Repo, Tool: "update_issue", Confirm: true, Outcome: "error"})
		return nil, updateIssueOutput{}, fmt.Errorf("update issue %s/%s#%d: %w", in.Owner, in.Repo, in.IssueNumber, err)
	}
	d.auditLog(audit.Entry{Forge: in.Forge, Owner: in.Owner, Repo: in.Repo, Tool: "update_issue", Confirm: true, Outcome: "success"})
	return nil, updateIssueOutput{
		Draft: false,
		Forge: in.Forge, Owner: in.Owner, Repo: in.Repo, IssueNumber: in.IssueNumber,
		Title: in.Title, Body: in.Body,
		Issue: &issue,
	}, nil
}

type addLabelsInput struct {
	Forge       string   `json:"forge" jsonschema:"forge hosting the repo: github or forgejo"`
	Owner       string   `json:"owner" jsonschema:"repository owner"`
	Repo        string   `json:"repo" jsonschema:"repository name"`
	IssueNumber int      `json:"issue_number" jsonschema:"issue number to label"`
	Labels      []string `json:"labels" jsonschema:"labels to add (non-empty)"`
	Confirm     bool     `json:"confirm,omitempty" jsonschema:"when false, returns a draft without adding labels; set true to add"`
}

type addLabelsOutput struct {
	Draft       bool     `json:"draft"`
	Forge       string   `json:"forge"`
	Owner       string   `json:"owner"`
	Repo        string   `json:"repo"`
	IssueNumber int      `json:"issue_number"`
	Labels      []string `json:"labels,omitempty"`
}

func (d Deps) handleAddLabels(ctx context.Context, _ *mcp.CallToolRequest, in addLabelsInput) (*mcp.CallToolResult, addLabelsOutput, error) {
	if len(in.Labels) == 0 {
		return nil, addLabelsOutput{}, fmt.Errorf("add labels %s/%s#%d: labels must not be empty", in.Owner, in.Repo, in.IssueNumber)
	}
	draft := addLabelsOutput{
		Draft: true,
		Forge: in.Forge, Owner: in.Owner, Repo: in.Repo, IssueNumber: in.IssueNumber,
		Labels: in.Labels,
	}
	if !in.Confirm {
		return nil, draft, nil
	}
	client := d.ClientFor(in.Forge, in.Owner)
	if client == nil {
		return nil, addLabelsOutput{}, fmt.Errorf("forge %q not configured", in.Forge)
	}
	adder, ok := client.(labelAdder)
	if !ok {
		return nil, addLabelsOutput{}, fmt.Errorf("forge %q does not support adding labels", in.Forge)
	}
	labels, err := adder.AddLabels(ctx, in.Owner, in.Repo, in.IssueNumber, in.Labels)
	if err != nil {
		d.auditLog(audit.Entry{Forge: in.Forge, Owner: in.Owner, Repo: in.Repo, Tool: "add_labels", Confirm: true, Outcome: "error"})
		return nil, addLabelsOutput{}, fmt.Errorf("add labels %s/%s#%d: %w", in.Owner, in.Repo, in.IssueNumber, err)
	}
	d.auditLog(audit.Entry{Forge: in.Forge, Owner: in.Owner, Repo: in.Repo, Tool: "add_labels", Confirm: true, Outcome: "success"})
	return nil, addLabelsOutput{
		Draft: false,
		Forge: in.Forge, Owner: in.Owner, Repo: in.Repo, IssueNumber: in.IssueNumber,
		Labels: labels,
	}, nil
}

type commentIssueInput struct {
	Forge       string `json:"forge" jsonschema:"forge hosting the repo: github or forgejo"`
	Owner       string `json:"owner" jsonschema:"repository owner"`
	Repo        string `json:"repo" jsonschema:"repository name"`
	IssueNumber int    `json:"issue_number" jsonschema:"issue number to comment on"`
	Body        string `json:"body" jsonschema:"comment body (markdown)"`
	Confirm     bool   `json:"confirm,omitempty" jsonschema:"when false, returns a draft without commenting; set true to comment"`
}

type commentIssueOutput struct {
	Draft       bool           `json:"draft"`
	Forge       string         `json:"forge"`
	Owner       string         `json:"owner"`
	Repo        string         `json:"repo"`
	IssueNumber int            `json:"issue_number"`
	Body        string         `json:"body,omitempty"`
	Comment     *forge.Comment `json:"comment,omitempty"`
}

func (d Deps) handleCommentIssue(ctx context.Context, _ *mcp.CallToolRequest, in commentIssueInput) (*mcp.CallToolResult, commentIssueOutput, error) {
	draft := commentIssueOutput{
		Draft: true,
		Forge: in.Forge, Owner: in.Owner, Repo: in.Repo, IssueNumber: in.IssueNumber,
		Body: in.Body,
	}
	if !in.Confirm {
		return nil, draft, nil
	}
	client := d.ClientFor(in.Forge, in.Owner)
	if client == nil {
		return nil, commentIssueOutput{}, fmt.Errorf("forge %q not configured", in.Forge)
	}
	commenter, ok := client.(issueCommenter)
	if !ok {
		return nil, commentIssueOutput{}, fmt.Errorf("forge %q does not support commenting on issues", in.Forge)
	}
	comment, err := commenter.CommentIssue(ctx, in.Owner, in.Repo, in.IssueNumber, in.Body)
	if err != nil {
		d.auditLog(audit.Entry{Forge: in.Forge, Owner: in.Owner, Repo: in.Repo, Tool: "comment_issue", Confirm: true, Outcome: "error"})
		return nil, commentIssueOutput{}, fmt.Errorf("comment issue %s/%s#%d: %w", in.Owner, in.Repo, in.IssueNumber, err)
	}
	d.auditLog(audit.Entry{Forge: in.Forge, Owner: in.Owner, Repo: in.Repo, Tool: "comment_issue", Confirm: true, Outcome: "success"})
	return nil, commentIssueOutput{
		Draft: false,
		Forge: in.Forge, Owner: in.Owner, Repo: in.Repo, IssueNumber: in.IssueNumber,
		Body: in.Body, Comment: &comment,
	}, nil
}
