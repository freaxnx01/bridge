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

// maxPRFiles bounds get_pr's file list to one API page; FilesTruncated
// reports when the PR changed more.
const maxPRFiles = 100

type getPRInput struct {
	Forge  string `json:"forge" jsonschema:"forge hosting the repo: github (forgejo returns a warning)"`
	Owner  string `json:"owner" jsonschema:"repository owner"`
	Repo   string `json:"repo" jsonschema:"repository name"`
	Number int    `json:"number" jsonschema:"pull request number"`
}

type prChecks struct {
	CheckRuns []forge.CheckRun     `json:"check_runs"`
	Statuses  []forge.CommitStatus `json:"statuses"`
}

type getPROutput struct {
	PR             *forge.PullRequest `json:"pr,omitempty"`
	Files          []forge.PRFile     `json:"files"`
	FilesTruncated bool               `json:"files_truncated,omitempty"`
	Checks         prChecks           `json:"checks"`
	Warnings       []string           `json:"warnings,omitempty"`
}

// handleGetPR returns one PR with its changed files and the checks on its
// head commit. Only the PR fetch is fatal: a failed files, check-runs or
// statuses fetch becomes a warning on an otherwise complete result.
func (d Deps) handleGetPR(ctx context.Context, _ *mcp.CallToolRequest, in getPRInput) (*mcp.CallToolResult, getPROutput, error) {
	if err := requireRepo("get_pr", in.Owner, in.Repo); err != nil {
		return nil, getPROutput{}, err
	}
	if in.Number <= 0 {
		return nil, getPROutput{}, invalidInput(fmt.Errorf("get_pr: number must be positive"))
	}
	client := d.ClientFor(in.Forge, in.Owner)
	if client == nil {
		return nil, getPROutput{}, fmt.Errorf("forge %q not configured", in.Forge)
	}
	reader, ok := client.(prReader)
	if !ok {
		return nil, getPROutput{Files: []forge.PRFile{}, Warnings: unsupported(in.Forge, "get_pr")}, nil
	}
	ctx, cancel := context.WithTimeout(ctx, toolTimeout)
	defer cancel()
	pr, err := reader.GetPullRequest(ctx, in.Owner, in.Repo, in.Number)
	if err != nil {
		return nil, getPROutput{}, fmt.Errorf("get pr %s/%s#%d: %w", in.Owner, in.Repo, in.Number, err)
	}
	return nil, prDetails(ctx, reader, in, pr), nil
}

// prDetails fetches files, check-runs and statuses for pr, recording each
// failure as a warning instead of failing the call.
func prDetails(ctx context.Context, reader prReader, in getPRInput, pr forge.PullRequest) getPROutput {
	out := getPROutput{
		PR:     &pr,
		Files:  []forge.PRFile{},
		Checks: prChecks{CheckRuns: []forge.CheckRun{}, Statuses: []forge.CommitStatus{}},
	}
	if files, err := reader.ListPullRequestFiles(ctx, in.Owner, in.Repo, in.Number); err != nil {
		out.Warnings = append(out.Warnings, fmt.Sprintf("files: %v", err))
	} else {
		out.Files, out.FilesTruncated = capFiles(files, pr.ChangedFiles)
	}
	if runs, err := reader.ListCheckRuns(ctx, in.Owner, in.Repo, pr.HeadSHA); err != nil {
		out.Warnings = append(out.Warnings, fmt.Sprintf("check-runs: %v", err))
	} else if runs != nil {
		out.Checks.CheckRuns = runs
	}
	if statuses, err := reader.ListCommitStatuses(ctx, in.Owner, in.Repo, pr.HeadSHA); err != nil {
		out.Warnings = append(out.Warnings, fmt.Sprintf("statuses: %v", err))
	} else if statuses != nil {
		out.Checks.Statuses = statuses
	}
	return out
}

// capFiles keeps at most maxPRFiles and reports whether the PR changed more
// files than are returned.
func capFiles(files []forge.PRFile, changedFiles int) ([]forge.PRFile, bool) {
	if files == nil {
		files = []forge.PRFile{}
	}
	if len(files) > maxPRFiles {
		files = files[:maxPRFiles]
	}
	return files, changedFiles > len(files)
}
