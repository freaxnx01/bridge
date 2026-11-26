// cmd/bridge/merge.go
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
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
	case pr.BaseRef != req.BaseRef:
		return api.MergeResult{}, api.ErrNotMergeable{Reason: "base changed"}
	case pr.State != "open":
		return api.MergeResult{}, api.ErrNotMergeable{Reason: "PR is not open"}
	}
	ev, _, err := prEvidence(ctx, m, bots, owner, name, pr)
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
		return fmt.Errorf("%w: head branch is not known to live in %s/%s (head repo %q)",
			errBranchNotOurs, owner, name, pr.HeadRepo)
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
//
// The file list comes back alongside the evidence with its patches intact — the
// evidence's own copy drops them for the open inbox — so a caller that has to
// pay for the patches anyway can hand them to the diff cache.
func prEvidence(ctx context.Context, m merger, bots []string, owner, name string,
	pr forge.PullRequest) (cockpit.MergeEvidence, []forge.PRFile, error) {
	comments, err := m.ListPRComments(ctx, owner, name, pr.Number)
	if err != nil {
		return cockpit.MergeEvidence{}, nil, fmt.Errorf("pr comments: %w", err)
	}
	runs, err := m.ListCheckRuns(ctx, owner, name, pr.HeadSHA)
	if err != nil {
		return cockpit.MergeEvidence{}, nil, fmt.Errorf("check runs: %w", err)
	}
	statuses, err := m.ListCommitStatuses(ctx, owner, name, pr.HeadSHA)
	if err != nil {
		return cockpit.MergeEvidence{}, nil, fmt.Errorf("commit statuses: %w", err)
	}
	files, err := m.ListPullRequestFiles(ctx, owner, name, pr.Number)
	if err != nil {
		return cockpit.MergeEvidence{}, nil, fmt.Errorf("pr files: %w", err)
	}
	return cockpit.BuildEvidence(pr, comments, runs, statuses, files, bots), files, nil
}

// liveEvidence is the collector's merge-evidence source: it re-reads the PR
// through the single-PR endpoint (the listing carries no mergeable_state or
// changed_files), gathers the rest, and seeds the diff cache with the patches
// that read already paid for.
func liveEvidence(ctx context.Context, resolve clientResolver, bots []string, cache *cockpit.DiffCache,
	r core.Repo, listed forge.PullRequest) (cockpit.MergeEvidence, error) {
	m, err := repoMerger(resolve, r)
	if err != nil {
		return cockpit.MergeEvidence{}, err
	}
	pr, err := m.GetPullRequest(ctx, r.Owner, r.Name, listed.Number)
	if err != nil {
		return cockpit.MergeEvidence{}, fmt.Errorf("read pr %s/%s#%d: %w", r.Owner, r.Name, listed.Number, err)
	}
	ev, files, err := prEvidence(ctx, m, bots, r.Owner, r.Name, pr)
	if err != nil {
		return cockpit.MergeEvidence{}, err
	}
	// The card shows this read's head SHA, so the diff of that head is the one
	// the operator can ask for — and it is already in hand. Without this, every
	// tap re-read a file list the collector had just downloaded and thrown away.
	cache.Put(diffKey(r.Owner, r.Name, pr.Number, pr.HeadSHA),
		cockpit.NewDiff(pr.Number, pr.HeadSHA, files, ev.FilesTruncated))
	return ev, nil
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
	req.BaseRef = card.Merge.BaseRef
	return mergePR(ctx, m, bots, req)
}

// inboxDiff is the DiffHandler's Diff: the per-file diff of the PR the issue's
// merge card is currently showing, read through cache so a card the collector
// already read the files of, or one reopened at the same head, costs no forge
// call. An issue that is not a merge card is refused before anything is
// resolved — the diff reads no arbitrary PR.
func inboxDiff(ctx context.Context, cards mergeCards, repos func() ([]core.Repo, error),
	resolve clientResolver, cache *cockpit.DiffCache, owner, name string, number int) (cockpit.Diff, error) {
	card, ok := cards.MergeCard(owner, name, number)
	if !ok || card.Kind != cockpit.CardMerge || card.Merge == nil {
		return cockpit.Diff{}, fmt.Errorf("%w: %s/%s#%d", cockpit.ErrNotCandidate, owner, name, number)
	}
	ev := card.Merge
	return cache.Get(ctx, diffKey(owner, name, ev.PR, ev.HeadSHA), func(ctx context.Context) (cockpit.Diff, error) {
		return prDiff(ctx, repos, resolve, owner, name, ev)
	})
}

// diffKey identifies one PR's cached diff.
func diffKey(owner, name string, pr int, headSHA string) cockpit.DiffKey {
	return cockpit.DiffKey{Repo: owner + "/" + name, PR: pr, HeadSHA: headSHA}
}

// prDiff reads one PR's diff with the repo owner's own client, for the head the
// card was collected at.
//
// The head is re-read first because the file listing answers for whatever the
// PR's head is now: served under the card's older SHA it would show the
// operator code the review verdict on the card was never given on, and cache it
// under that SHA on top. A head that has moved is refused with ErrHeadMoved;
// the next collection brings the card, and with it the diff, to the new commit.
func prDiff(ctx context.Context, repos func() ([]core.Repo, error), resolve clientResolver,
	owner, name string, ev *cockpit.MergeEvidence) (cockpit.Diff, error) {
	r, err := mergeRepo(repos, owner, name)
	if err != nil {
		return cockpit.Diff{}, err
	}
	m, err := repoMerger(resolve, r)
	if err != nil {
		return cockpit.Diff{}, err
	}
	pr, err := m.GetPullRequest(ctx, owner, name, ev.PR)
	if err != nil {
		return cockpit.Diff{}, fmt.Errorf("read pr %s/%s#%d: %w", owner, name, ev.PR, err)
	}
	if !strings.EqualFold(pr.HeadSHA, ev.HeadSHA) {
		return cockpit.Diff{}, fmt.Errorf("%w: %s/%s#%d is at %s, the card at %s",
			cockpit.ErrHeadMoved, owner, name, ev.PR, pr.HeadSHA, ev.HeadSHA)
	}
	files, err := m.ListPullRequestFiles(ctx, owner, name, ev.PR)
	if err != nil {
		return cockpit.Diff{}, fmt.Errorf("pr files %s/%s#%d: %w", owner, name, ev.PR, err)
	}
	// ev.HeadSHA, not pr.HeadSHA: they name the same commit, and the client
	// drops a diff whose headSha is not the one its card shows, character for
	// character. The card's truncation flag is for that same head, so the diff
	// repeats it rather than deciding it again.
	return cockpit.NewDiff(ev.PR, ev.HeadSHA, files, ev.FilesTruncated), nil
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
// A forge failure's text stays in the log; the client learns only that the
// delete failed. bridge's own refusal to touch a branch it cannot prove is
// ours is safe to show as is.
func withBranch(err error) api.MergeResult {
	switch {
	case err == nil:
		return api.MergeResult{Merged: true, BranchDeleted: true}
	case errors.Is(err, errBranchNotOurs):
		return api.MergeResult{Merged: true, BranchError: err.Error()}
	}
	slog.Error("delete merged head branch failed", "error", err)
	return api.MergeResult{Merged: true, BranchError: "branch delete failed (see the server log)"}
}

// errBranchNotOurs marks a head branch bridge will not delete because nothing
// proves it lives in the base repo.
var errBranchNotOurs = errors.New("branch not deleted")
