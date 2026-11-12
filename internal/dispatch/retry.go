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

// RetryPending reports whether bridge has already re-dispatched this issue
// since the run report at `since`, which means the pipeline owes a report and
// retrying again would stack runs.
//
// The author is deliberately not checked: the guard can only ever suppress a
// retry, so a hand-written comment costs an operator a tick, not a run.
func RetryPending(comments []forge.Comment, since time.Time) bool {
	for _, c := range comments {
		if strings.HasPrefix(c.Body, RedispatchCommentPrefix) && c.Created.After(since) {
			return true
		}
	}
	return false
}

// EvaluateRetry decides what the retry pass should do with one issue: NextAction
// over its latest run marker and in-flight runs, with the retry-pending guard on
// top. Only ActionRetry is re-dispatched; every other kind is listed with its
// reason.
func EvaluateRetry(i forge.Issue, comments []forge.Comment, runs []forge.WorkflowRun, now time.Time) Action {
	run, hasRun := LatestRun(comments)
	a := NextAction(IssueState{
		Labels: i.Labels, Run: run, HasRun: hasRun, InFlight: InFlight(i, runs), Now: now,
	})
	if a.Kind == ActionRetry && RetryPending(comments, run.Posted) {
		return Action{ActionWait, "retry pending"}
	}
	return a
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
