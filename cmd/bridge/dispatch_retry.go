package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/freaxnx01/bridge/internal/dispatch"
	"github.com/freaxnx01/bridge/internal/forge"
)

// retryReader is the forge surface the retry pass reads through: one run
// listing per repo, and one comment fetch per candidate.
type retryReader interface {
	GetIssue(ctx context.Context, owner, repo string, number int) (forge.Issue, []forge.Comment, error)
	ListWorkflowRuns(ctx context.Context, owner, repo, branch, headSHA string, limit int) ([]forge.WorkflowRun, error)
}

// collectRetries picks the issues whose last pipeline run stopped for a reason
// a re-dispatch can fix, and the skip rows for the ones it looked at and left
// alone. Non-GitHub repos are skipped silently: only GitHub Actions posts the
// run marker the decision reads.
func collectRetries(ctx context.Context, gh retryReader, repos []repoInput, now time.Time) ([]dispatch.Candidate, []dispatch.Decision) {
	var cands []dispatch.Candidate
	var skips []dispatch.Decision
	for _, r := range repos {
		if r.Forge != "github" {
			continue
		}
		c, s := retriesForRepo(ctx, gh, r, now)
		cands = append(cands, c...)
		skips = append(skips, s...)
	}
	return cands, skips
}

// retriesForRepo evaluates one repo's retry candidates. A repo with none costs
// no API call at all — the label predicate decides that before any fetch.
func retriesForRepo(ctx context.Context, gh retryReader, r repoInput, now time.Time) ([]dispatch.Candidate, []dispatch.Decision) {
	var pending []forge.Issue
	var skips []dispatch.Decision
	for _, i := range r.Issues {
		ok, reason := dispatch.RetryCandidate(i.Labels)
		switch {
		case ok:
			pending = append(pending, i)
		case reason != "":
			skips = append(skips, skipRetry(retryCandidate(r, i, ""), reason))
		}
	}
	if len(pending) == 0 {
		return nil, skips
	}

	runs, err := gh.ListWorkflowRuns(ctx, r.Owner, r.Name, "", "", runsPerRepo)
	if err != nil {
		// Not knowing whether a run is in flight must never read as "not in
		// flight": a retry on top of a running pipeline stacks two runs on one
		// issue, so the whole repo's candidates wait for the next tick.
		slog.Warn("dispatch: cannot read workflow runs — skipping this repo's retries",
			"repo", r.Name, "error", err)
		for _, i := range pending {
			skips = append(skips, skipRetry(retryCandidate(r, i, ""), "runs unreadable"))
		}
		return nil, skips
	}

	var cands []dispatch.Candidate
	for _, i := range pending {
		_, comments, err := gh.GetIssue(ctx, r.Owner, r.Name, i.Number)
		if err != nil {
			slog.Warn("dispatch: cannot read issue comments — skipping this retry",
				"repo", r.Name, "issue", i.Number, "error", err)
			skips = append(skips, skipRetry(retryCandidate(r, i, ""), "issue unreadable"))
			continue
		}
		a := dispatch.EvaluateRetry(i, comments, runs, now)
		if a.Kind != dispatch.ActionRetry {
			reason := a.Reason
			if reason == "" {
				reason = "no action"
			}
			skips = append(skips, skipRetry(retryCandidate(r, i, ""), reason))
			continue
		}
		cands = append(cands, retryCandidate(r, i, a.Reason))
	}
	return cands, skips
}

// retryCandidate builds the candidate for one retry issue, with the same repo
// context collectCandidates gives a new dispatch so both go through one ladder.
func retryCandidate(r repoInput, i forge.Issue, reason string) dispatch.Candidate {
	return dispatch.Candidate{
		Issue: i, Owner: r.Owner, Repo: r.Name,
		MilestoneDue: milestoneDue(r.Milestones, dispatch.ActiveMilestone(r.Milestones)),
		Retry:        true, RetryReason: reason,
	}
}

func skipRetry(c dispatch.Candidate, reason string) dispatch.Decision {
	return dispatch.Decision{Candidate: c, Dispatch: false, Reason: reason}
}
