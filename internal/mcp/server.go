package mcp

import (
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// NewServer builds the Bridge MCP server with the seven cross-forge tools
// registered. In read-only mode the write tools (create_issue, create_repo)
// are not registered at all, so there is nothing to bypass.
func NewServer(deps Deps) *mcp.Server {
	srv := mcp.NewServer(&mcp.Implementation{Name: "bridge", Version: "v1"}, nil)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "list_repos",
		Description: "List repositories across the configured GitHub and Forgejo owners (live).",
	}, deps.handleListRepos)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "read_file",
		Description: "Read a file's contents and blob sha from a repo's default branch.",
	}, deps.handleReadFile)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "list_issues",
		Description: "List open issues for a single repository (live).",
	}, deps.handleListIssues)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "list_git_forges",
		Description: "List the configured forge targets, whether each is configured, and which tools it supports. Makes no network requests.",
	}, deps.handleListGitForges)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "cross_forge_status",
		Description: "Return the cross-forge overview snapshot (ranked items, inbox, roadmap).",
	}, deps.handleCrossForgeStatus)

	if !deps.ReadOnly {
		mcp.AddTool(srv, &mcp.Tool{
			Name:        "create_issue",
			Description: "Create an issue. Without confirm=true this returns a draft and creates nothing.",
		}, deps.handleCreateIssue)

		mcp.AddTool(srv, &mcp.Tool{
			Name:        "create_repo",
			Description: "Create a repository. The owner input selects which account's token to use — the repo is created under that token's account, which may differ. Without confirm=true this returns a draft and creates nothing.",
		}, deps.handleCreateRepo)

		mcp.AddTool(srv, &mcp.Tool{
			Name:        "close_issue",
			Description: "Close an issue. Without confirm=true this returns a draft and closes nothing.",
		}, deps.handleCloseIssue)

		mcp.AddTool(srv, &mcp.Tool{
			Name:        "update_issue",
			Description: "Update an issue's title and/or body. Without confirm=true this returns a draft and updates nothing.",
		}, deps.handleUpdateIssue)

		mcp.AddTool(srv, &mcp.Tool{
			Name:        "add_labels",
			Description: "Add labels to an issue. Without confirm=true this returns a draft and adds nothing.",
		}, deps.handleAddLabels)

		mcp.AddTool(srv, &mcp.Tool{
			Name:        "comment_issue",
			Description: "Post a comment on an issue. Without confirm=true this returns a draft and posts nothing.",
		}, deps.handleCommentIssue)
	}

	return srv
}
