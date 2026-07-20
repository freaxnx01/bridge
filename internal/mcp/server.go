package mcp

import (
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// NewServer builds the Bridge MCP server with the four cross-forge tools
// registered. In read-only mode the write tool (create_issue) is not
// registered at all, so there is nothing to bypass.
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
	}

	return srv
}
