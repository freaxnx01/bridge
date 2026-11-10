package forge

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"time"
)

func repoPath(owner, repo string) string {
	return "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(repo)
}

// GetPullRequest returns one pull request, including the fields only the
// single-PR endpoint reports (mergeable_state, changed_files).
func (c *GithubClient) GetPullRequest(ctx context.Context, owner, repo string, number int) (PullRequest, error) {
	var raw ghPull
	if err := c.get(ctx, fmt.Sprintf("%s/pulls/%d", repoPath(owner, repo), number), &raw); err != nil {
		return PullRequest{}, err
	}
	return raw.toPullRequest(), nil
}

// ListPullRequestFiles returns the first page (100) of a pull request's
// changed files.
func (c *GithubClient) ListPullRequestFiles(ctx context.Context, owner, repo string, number int) ([]PRFile, error) {
	var raw []struct {
		Filename  string `json:"filename"`
		Additions int    `json:"additions"`
		Deletions int    `json:"deletions"`
	}
	if err := c.get(ctx, fmt.Sprintf("%s/pulls/%d/files?per_page=100", repoPath(owner, repo), number), &raw); err != nil {
		return nil, err
	}
	out := make([]PRFile, 0, len(raw))
	for _, f := range raw {
		out = append(out, PRFile{Path: f.Filename, Additions: f.Additions, Deletions: f.Deletions})
	}
	return out, nil
}

// ListCheckRuns returns the first page (100) of check-runs on a commit.
func (c *GithubClient) ListCheckRuns(ctx context.Context, owner, repo, sha string) ([]CheckRun, error) {
	var raw struct {
		CheckRuns []struct {
			Name       string `json:"name"`
			Status     string `json:"status"`
			Conclusion string `json:"conclusion"`
			App        struct {
				Slug string `json:"slug"`
			} `json:"app"`
		} `json:"check_runs"`
	}
	path := repoPath(owner, repo) + "/commits/" + url.PathEscape(sha) + "/check-runs?per_page=100"
	if err := c.get(ctx, path, &raw); err != nil {
		return nil, err
	}
	out := make([]CheckRun, 0, len(raw.CheckRuns))
	for _, r := range raw.CheckRuns {
		out = append(out, CheckRun{Name: r.Name, Status: r.Status, Conclusion: r.Conclusion, App: r.App.Slug})
	}
	return out, nil
}

// ListCommitStatuses returns the combined status's per-context entries — the
// latest status for each context on the commit.
func (c *GithubClient) ListCommitStatuses(ctx context.Context, owner, repo, sha string) ([]CommitStatus, error) {
	var raw struct {
		Statuses []struct {
			Context     string `json:"context"`
			State       string `json:"state"`
			Description string `json:"description"`
			TargetURL   string `json:"target_url"`
		} `json:"statuses"`
	}
	if err := c.get(ctx, repoPath(owner, repo)+"/commits/"+url.PathEscape(sha)+"/status", &raw); err != nil {
		return nil, err
	}
	out := make([]CommitStatus, 0, len(raw.Statuses))
	for _, s := range raw.Statuses {
		out = append(out, CommitStatus{Context: s.Context, State: s.State, Description: s.Description, URL: s.TargetURL})
	}
	return out, nil
}

type ghWorkflowRun struct {
	Name       string `json:"name"`
	Event      string `json:"event"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	Actor      struct {
		Login string `json:"login"`
	} `json:"actor"`
	TriggeringActor struct {
		Login string `json:"login"`
	} `json:"triggering_actor"`
	HeadBranch string    `json:"head_branch"`
	HeadSHA    string    `json:"head_sha"`
	CreatedAt  time.Time `json:"created_at"`
	HTMLURL    string    `json:"html_url"`
}

// ListWorkflowRuns returns up to limit Actions runs, newest first, optionally
// narrowed to a branch and/or head commit. Empty filters are omitted.
func (c *GithubClient) ListWorkflowRuns(ctx context.Context, owner, repo, branch, headSHA string, limit int) ([]WorkflowRun, error) {
	q := url.Values{}
	if branch != "" {
		q.Set("branch", branch)
	}
	if headSHA != "" {
		q.Set("head_sha", headSHA)
	}
	q.Set("per_page", strconv.Itoa(limit))
	var raw struct {
		WorkflowRuns []ghWorkflowRun `json:"workflow_runs"`
	}
	if err := c.get(ctx, repoPath(owner, repo)+"/actions/runs?"+q.Encode(), &raw); err != nil {
		return nil, err
	}
	out := make([]WorkflowRun, 0, len(raw.WorkflowRuns))
	for _, r := range raw.WorkflowRuns {
		out = append(out, WorkflowRun{
			Name: r.Name, Event: r.Event, Status: r.Status, Conclusion: r.Conclusion,
			Actor: r.Actor.Login, TriggeringActor: r.TriggeringActor.Login,
			HeadBranch: r.HeadBranch, HeadSHA: r.HeadSHA, Created: r.CreatedAt, URL: r.HTMLURL,
		})
	}
	return out, nil
}
