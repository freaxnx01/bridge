// cmd/bridge/merge.go
package main

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/freaxnx01/bridge/internal/api"
	"github.com/freaxnx01/bridge/internal/cockpit"
	"github.com/freaxnx01/bridge/internal/core"
	"github.com/freaxnx01/bridge/internal/forge"
)

// defaultPipelineBot is the identity GitHub Actions posts as. A repo whose
// pipeline runs under its own App (PIPELINE_APP_ID) posts re-reviews as that
// App's bot instead, which is why the list is configurable.
const defaultPipelineBot = "github-actions[bot]"

// mergeMethod is the only method a cockpit merge ever uses. The pipeline's
// history lives in the PR, not in its intermediate commits.
const mergeMethod = "squash"

// merger is what the merge path needs from a forge client: the reads the
// decision is made from, plus the two writes it ends in. GithubClient has them
// all; every other client is refused.
type merger interface {
	GetPullRequest(ctx context.Context, owner, repo string, number int) (forge.PullRequest, error)
	ListPRComments(ctx context.Context, owner, repo string, number int) ([]forge.Comment, error)
	ListCheckRuns(ctx context.Context, owner, repo, sha string) ([]forge.CheckRun, error)
	ListCommitStatuses(ctx context.Context, owner, repo, sha string) ([]forge.CommitStatus, error)
	ListPullRequestFiles(ctx context.Context, owner, repo string, number int) ([]forge.PRFile, error)
	MergePullRequest(ctx context.Context, owner, repo string, number int, sha, method string) error
	DeleteBranch(ctx context.Context, owner, repo, ref string) error
}

// mergeCards is what the merge endpoint needs from the collector: the merge
// card the inbox is currently showing for one issue, if any.
type mergeCards interface {
	MergeCard(owner, name string, number int) (cockpit.InboxCard, bool)
}

// pipelineBots parses BRIDGE_PIPELINE_BOTS: a comma-separated list of the
// GitHub logins whose PRs and review comments the merge card trusts. An unset
// or blank value means the Actions bot alone.
func pipelineBots(env string) []string {
	var out []string
	for _, b := range strings.Split(env, ",") {
		if b = strings.TrimSpace(b); b != "" {
			out = append(out, b)
		}
	}
	if len(out) == 0 {
		return []string{defaultPipelineBot}
	}
	return out
}

// mergePR re-reads the PR live and merges it only if the same eligibility rule
// the card applied still holds, pinned to the head SHA the client saw. The
// head branch is deleted afterwards; that failing does not undo the merge.
func mergePR(ctx context.Context, m merger, bots []string, req api.MergeRequest) (api.MergeResult, error) {
	owner, name, _ := strings.Cut(req.Repo, "/")
	pr, err := m.GetPullRequest(ctx, owner, name, req.PR)
	if err != nil {
		return api.MergeResult{}, fmt.Errorf("read pr %s#%d: %w", req.Repo, req.PR, err)
	}
	// Checked before the evidence reads: a PR that moved on is not worth four
	// more calls, and a merge must never land a commit the operator never saw.
	switch {
	case !strings.EqualFold(pr.HeadSHA, req.HeadSHA):
		return api.MergeResult{}, api.ErrNotMergeable{Reason: "head moved"}
	case pr.State != "open":
		return api.MergeResult{}, api.ErrNotMergeable{Reason: "PR is not open"}
	}
	ev, err := prEvidence(ctx, m, bots, owner, name, pr)
	if err != nil {
		return api.MergeResult{}, err
	}
	if !ev.CanMerge {
		return api.MergeResult{}, api.ErrNotMergeable{Reason: ev.Reason}
	}
	// pr.HeadSHA, not req.HeadSHA: they are equal ignoring case, and GitHub
	// gets the SHA in the form it reported.
	if err := m.MergePullRequest(ctx, owner, name, req.PR, pr.HeadSHA, mergeMethod); err != nil {
		var apiErr *forge.APIError
		if errors.As(err, &apiErr) {
			return api.MergeResult{}, api.ErrForgeRefused{Status: apiErr.Status, Message: apiErr.Message}
		}
		return api.MergeResult{}, fmt.Errorf("merge %s#%d: %w", req.Repo, req.PR, err)
	}
	return withBranch(deleteHeadBranch(ctx, m, owner, name, pr)), nil
}

// deleteHeadBranch removes the merged head branch, or says why it did not. The
// delete needs positive evidence that the head lives in the base repo: GitHub
// reports head.repo as null once a fork is deleted, and a blank head repo read
// as "ours" would delete whatever base branch happens to share the head ref's
// name.
func deleteHeadBranch(ctx context.Context, m merger, owner, name string, pr forge.PullRequest) error {
	if pr.HeadRepo == "" || pr.HeadRepo != pr.BaseRepo {
		return fmt.Errorf("head branch is not known to live in %s/%s (head repo %q)", owner, name, pr.HeadRepo)
	}
	if err := m.DeleteBranch(ctx, owner, name, pr.HeadRef); err != nil {
		return fmt.Errorf("delete %s: %w", pr.HeadRef, err)
	}
	return nil
}

// prEvidence reads everything the merge decision needs about one PR and
// applies the shared eligibility rule. The collector's merge-evidence source
// and the merge endpoint both go through here, so the card and the POST can
// never diverge on what "mergeable" means.
func prEvidence(ctx context.Context, m merger, bots []string, owner, name string,
	pr forge.PullRequest) (cockpit.MergeEvidence, error) {
	comments, err := m.ListPRComments(ctx, owner, name, pr.Number)
	if err != nil {
		return cockpit.MergeEvidence{}, fmt.Errorf("pr comments: %w", err)
	}
	runs, err := m.ListCheckRuns(ctx, owner, name, pr.HeadSHA)
	if err != nil {
		return cockpit.MergeEvidence{}, fmt.Errorf("check runs: %w", err)
	}
	statuses, err := m.ListCommitStatuses(ctx, owner, name, pr.HeadSHA)
	if err != nil {
		return cockpit.MergeEvidence{}, fmt.Errorf("commit statuses: %w", err)
	}
	files, err := m.ListPullRequestFiles(ctx, owner, name, pr.Number)
	if err != nil {
		return cockpit.MergeEvidence{}, fmt.Errorf("pr files: %w", err)
	}
	return cockpit.BuildEvidence(pr, comments, runs, statuses, files, bots), nil
}

// liveEvidence is the collector's merge-evidence source: it re-reads the PR
// through the single-PR endpoint (the listing carries no mergeable_state or
// changed_files) and gathers the rest.
func liveEvidence(ctx context.Context, resolve clientResolver, bots []string,
	r core.Repo, listed forge.PullRequest) (cockpit.MergeEvidence, error) {
	m, err := repoMerger(resolve, r)
	if err != nil {
		return cockpit.MergeEvidence{}, err
	}
	pr, err := m.GetPullRequest(ctx, r.Owner, r.Name, listed.Number)
	if err != nil {
		return cockpit.MergeEvidence{}, fmt.Errorf("read pr %s/%s#%d: %w", r.Owner, r.Name, listed.Number, err)
	}
	return prEvidence(ctx, m, bots, r.Owner, r.Name, pr)
}

// repoMerger resolves the repo owner's own client as a merger, the same way
// clearing an enrichment lock resolves its label client.
func repoMerger(resolve clientResolver, r core.Repo) (merger, error) {
	client, err := repoClient(resolve, r)
	if errors.Is(err, errNoForgeClient) {
		return nil, fmt.Errorf("%w: %w", api.ErrNoToken, err)
	}
	if err != nil {
		return nil, err
	}
	m, ok := client.(merger)
	if !ok {
		return nil, fmt.Errorf("%w: %s", api.ErrMergeUnsupported, r.Forge)
	}
	return m, nil
}

// mergeInboxCard is the InboxHandler's Merge: it requires the issue to be a
// merge card of the current snapshot showing exactly this PR, then merges with
// the repo owner's own token. refresh re-collects the repo so the card leaves
// the inbox without waiting for the next issues tick.
func mergeInboxCard(ctx context.Context, cards mergeCards, repos func() ([]core.Repo, error),
	resolve clientResolver, bots []string, req api.MergeRequest) (api.MergeResult, error) {
	owner, name, _ := strings.Cut(req.Repo, "/")
	card, ok := cards.MergeCard(owner, name, req.Number)
	if !ok || card.Kind != cockpit.CardMerge || card.Merge == nil || card.Merge.PR != req.PR {
		return api.MergeResult{}, api.ErrNotMergeable{Reason: "not on the inbox"}
	}
	r, err := mergeRepo(repos, owner, name)
	if err != nil {
		return api.MergeResult{}, err
	}
	m, err := repoMerger(resolve, r)
	if err != nil {
		return api.MergeResult{}, err
	}
	return mergePR(ctx, m, bots, req)
}

// mergeRepo finds the clone the merge applies to.
func mergeRepo(repos func() ([]core.Repo, error), owner, name string) (core.Repo, error) {
	all, err := repos()
	if err != nil {
		return core.Repo{}, fmt.Errorf("list repos: %w", err)
	}
	i := slices.IndexFunc(all, func(r core.Repo) bool { return r.Owner == owner && r.Name == name })
	if i < 0 {
		return core.Repo{}, fmt.Errorf("%w: %s/%s", api.ErrUnknownRepo, owner, name)
	}
	return all[i], nil
}

// withBranch is the result of a landed merge, carrying what became of the head
// branch afterwards.
func withBranch(err error) api.MergeResult {
	if err != nil {
		return api.MergeResult{Merged: true, BranchError: err.Error()}
	}
	return api.MergeResult{Merged: true, BranchDeleted: true}
}
