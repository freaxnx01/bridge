package mcp

import (
	"context"
	"fmt"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/freaxnx01/bridge/internal/dispatch"
	"github.com/freaxnx01/bridge/internal/forge"
)

// toolTimeout bounds a whole PR/run tool call. The forge client's 15 s
// http.Client timeout bounds one request; get_pr makes four, so without this
// a stalled forge could hold the MCP call for a minute or more.
const toolTimeout = 30 * time.Second

// unsupported is the warning a PR/run tool returns for a forge that lacks the
// capability — the search_code convention, so the caller sees "unsupported"
// rather than a silent empty result.
func unsupported(forgeName, tool string) []string {
	return []string{fmt.Sprintf("%s does not support %s", forgeName, tool)}
}

func requireRepo(tool, owner, repo string) error {
	if owner == "" || repo == "" {
		return invalidInput(fmt.Errorf("%s: owner and repo are required", tool))
	}
	return nil
}

type listPRsInput struct {
	Forge  string `json:"forge" jsonschema:"forge hosting the repo: github (forgejo returns a warning)"`
	Owner  string `json:"owner" jsonschema:"repository owner"`
	Repo   string `json:"repo" jsonschema:"repository name"`
	State  string `json:"state,omitempty" jsonschema:"open (default), closed or all"`
	Closes int    `json:"closes,omitempty" jsonschema:"optional issue number; keep only PRs whose body closes it (Closes/Fixes/Resolves #N)"`
}

type listPRsOutput struct {
	PRs      []forge.PullRequest `json:"prs"`
	Warnings []string            `json:"warnings,omitempty"`
}

func prState(s string) (string, error) {
	switch s {
	case "":
		return "open", nil
	case "open", "closed", "all":
		return s, nil
	default:
		return "", invalidInput(fmt.Errorf("list_prs: state %q must be open, closed or all", s))
	}
}

// handleListPRs lists one repo's pull requests, most recently updated first,
// one page of 100. closes narrows them with dispatch's own closing-keyword
// rule, so "which PR closes #N" agrees with what dispatch counts.
func (d Deps) handleListPRs(ctx context.Context, _ *mcp.CallToolRequest, in listPRsInput) (*mcp.CallToolResult, listPRsOutput, error) {
	state, err := prState(in.State)
	if err != nil {
		return nil, listPRsOutput{}, err
	}
	if err := requireRepo("list_prs", in.Owner, in.Repo); err != nil {
		return nil, listPRsOutput{}, err
	}
	client := d.ClientFor(in.Forge, in.Owner)
	if client == nil {
		return nil, listPRsOutput{}, fmt.Errorf("forge %q not configured", in.Forge)
	}
	lister, ok := client.(prLister)
	if !ok {
		return nil, listPRsOutput{PRs: []forge.PullRequest{}, Warnings: unsupported(in.Forge, "list_prs")}, nil
	}
	ctx, cancel := context.WithTimeout(ctx, toolTimeout)
	defer cancel()
	prs, err := lister.ListPullRequests(ctx, in.Owner, in.Repo, state)
	if err != nil {
		return nil, listPRsOutput{}, fmt.Errorf("list prs %s/%s: %w", in.Owner, in.Repo, err)
	}
	return nil, listPRsOutput{PRs: prSummaries(prs, in.Closes)}, nil
}

// prSummaries keeps the PRs that close issue closes (all of them when closes
// is 0) and drops their bodies — get_pr returns the body.
func prSummaries(prs []forge.PullRequest, closes int) []forge.PullRequest {
	out := make([]forge.PullRequest, 0, len(prs))
	for _, p := range prs {
		if closes > 0 && !dispatch.ClosesIssue(p.Body, closes) {
			continue
		}
		p.Body = ""
		out = append(out, p)
	}
	return out
}
