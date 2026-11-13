package main

import (
	"context"
	"errors"
	"fmt"
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
func collectRetries(ctx context.Context, gh retryReader, repos []dispatch.RepoInput, now time.Time) ([]dispatch.Candidate, []dispatch.Decision) {
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
func retriesForRepo(ctx context.Context, gh retryReader, r dispatch.RepoInput, now time.Time) ([]dispatch.Candidate, []dispatch.Decision) {
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
		a := dispatch.NextAction(dispatch.StateFor(i, comments, runs, r.PRs, now))
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
func retryCandidate(r dispatch.RepoInput, i forge.Issue, reason string) dispatch.Candidate {
	return dispatch.Candidate{
		Issue: i, Owner: r.Owner, Repo: r.Name,
		MilestoneDue: dispatch.MilestoneDue(r.Milestones, dispatch.ActiveMilestone(r.Milestones)),
		Retry:        true, RetryReason: reason,
	}
}

func skipRetry(c dispatch.Candidate, reason string) dispatch.Decision {
	return dispatch.Decision{Candidate: c, Dispatch: false, Reason: reason}
}

// tickCandidates gathers one tick's work: the retry pass first, then the new
// dispatches, each ordered by the ladder on its own. Retries lead because a
// failed issue already carries a run's worth of sunk cost, so finishing it
// beats starting something new under the same caps.
//
// The second return is the retry rows that were decided before the caps ran —
// they are reported, never applied.
func tickCandidates(ctx context.Context, repos []dispatch.RepoInput, cfg dispatch.Config, gate dispatch.GateState, now time.Time, retryOnly bool) ([]dispatch.Candidate, []dispatch.Decision) {
	retries, retrySkips := collectRetriesForTick(ctx, repos, now)
	dispatch.AssignLanes(retries, cfg.Lanes, gate)
	for i := range retrySkips {
		retrySkips[i].Candidate.Lane, retrySkips[i].Candidate.LaneReason =
			dispatch.ResolveLane(cfg.Lanes, retrySkips[i].Candidate.Repo, gate)
	}
	ordered := dispatch.Order(retries, cfg.RepoPriority)

	if !retryOnly {
		fresh := dispatch.CollectCandidates(repos)
		dispatch.AssignLanes(fresh, cfg.Lanes, gate)
		ordered = append(ordered, dispatch.Order(fresh, cfg.RepoPriority)...)
	}
	return ordered, retrySkips
}

// collectRetriesForTick resolves the GitHub client the retry pass reads
// through. Without one there is nothing to read, and passing a typed-nil
// client as the interface would turn that into a nil dereference.
func collectRetriesForTick(ctx context.Context, repos []dispatch.RepoInput, now time.Time) ([]dispatch.Candidate, []dispatch.Decision) {
	gh, ok := clientFor("github").(*forge.GithubClient)
	if !ok || gh == nil {
		return nil, nil
	}
	return collectRetries(ctx, gh, repos, now)
}

// applyRetry re-dispatches one issue and reports whether the run has to be
// booked. From step 3 of the swap the pipeline has fired, so everything after
// it is accounting the ledger must not lose — exactly like the label/comment
// split on the new-dispatch path.
func applyRetry(ctx context.Context, gh *forge.GithubClient, d dispatch.Decision) (bool, error) {
	owner, repo, num := d.Candidate.Owner, d.Candidate.Repo, d.Candidate.Issue.Number
	if err := dispatch.Redispatch(ctx, gh, owner, repo, num, d.Candidate.Lane.EffectiveLabels()); err != nil {
		var re *dispatch.RedispatchError
		booked := errors.As(err, &re) && re.Step == 4
		return booked, fmt.Errorf("retry %s#%d: %w", repo, num, err)
	}
	if _, err := gh.CommentIssue(ctx, owner, repo, num,
		dispatch.RedispatchComment(d.Candidate.RetryReason)); err != nil {
		return true, fmt.Errorf("comment %s#%d: %w", repo, num, err)
	}
	return true, nil
}
