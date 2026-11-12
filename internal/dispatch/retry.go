package dispatch

import (
	"strings"
	"time"

	"github.com/freaxnx01/bridge/internal/forge"
)

// RedispatchCommentPrefix opens the comment a tick posts after a successful
// re-dispatch. The guard below keys on it, so the two share one constant.
const RedispatchCommentPrefix = "Re-dispatched by `bridge dispatch`"

// RedispatchComment is the comment a tick posts after re-dispatching an issue.
func RedispatchComment(reason string) string {
	return RedispatchCommentPrefix + " (retry: " + reason + ")."
}

// LastRedispatch returns when bridge last re-dispatched this issue, or the zero
// time when it never did. A re-dispatch newer than the latest run marker means
// the pipeline owes a report, which NextAction's rows 15a-15b act on.
//
// The author is deliberately not checked: the guard can only ever suppress a
// retry or ask a person, so a hand-written comment costs an operator a tick,
// not a run.
func LastRedispatch(comments []forge.Comment) time.Time {
	var last time.Time
	for _, c := range comments {
		if strings.HasPrefix(c.Body, RedispatchCommentPrefix) && c.Created.After(last) {
			last = c.Created
		}
	}
	return last
}

// StateFor builds the state NextAction maps, from everything one tick already
// read about an issue. It is the single constructor the dispatcher's retry pass
// and the cockpit inbox share, so the two never show one issue differently.
func StateFor(i forge.Issue, comments []forge.Comment, runs []forge.WorkflowRun, prs []forge.PullRequest, now time.Time) IssueState {
	run, hasRun := LatestRun(comments)
	return IssueState{
		Labels:       i.Labels,
		Run:          run,
		HasRun:       hasRun,
		InFlight:     InFlight(i, runs),
		Redispatched: LastRedispatch(comments),
		HasOpenPR:    HasOpenPR(prs, i.Number),
		Now:          now,
	}
}

// RetryCandidate reports whether an issue's labels put it into the retry pass,
// plus the reason it was left out. A non-empty reason means "list it as a
// skipped retry"; an empty one means the issue is not a retry issue at all and
// is dropped silently, which is what keeps the pass off every open issue.
//
// Only a dispatched issue with a failed run can reach ActionRetry, so the label
// pair is a pre-filter: it spares every other issue the comment fetch.
func RetryCandidate(labels []string) (bool, string) {
	if hasLabel(labels, LabelRetrying) {
		return false, "retry interrupted"
	}
	if !hasLabel(labels, LabelAIImplement) || !hasLabel(labels, LabelAIFailed) {
		return false, ""
	}
	if IsParked(labels) {
		return false, "parked"
	}
	if hasLabel(labels, LabelAIPaused) {
		return false, "paused"
	}
	return true, ""
}
