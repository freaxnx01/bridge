package dispatch

import "time"

// Status and block labels the agent-workflow pipeline sets on an issue
// (scripts/ensure-issue-labels.sh there). Bridge only reads them: the pipeline
// is the single source of truth for how a run ended.
const (
	LabelAIDone          = "ai:done"
	LabelAIFailed        = "ai:failed"
	LabelAIPartial       = "ai:partial"
	LabelAIPaused        = "ai:paused"
	LabelAIReviewBlocked = "ai:review-blocked"
	LabelAIReviewFailed  = "ai:review-failed"
	LabelAIRunnerBlocked = "ai:runner-blocked"
	LabelAIChecksBlocked = "ai:checks-blocked"
)

// staleRetryAfter is the longest a run marker saying "retry" may go without a
// newer report before a person is asked. The pipeline's longest retry delay is
// 5 minutes plus one run, so three hours means the retry was lost — a 403 on
// re-dispatch, or a run that timed out and posted no report at all.
const staleRetryAfter = 3 * time.Hour

// ActionKind is what a retry tick should do with one issue.
type ActionKind int

const (
	ActionNone  ActionKind = iota // nothing to do (no failure, or done)
	ActionWait                    // the pipeline is still on it, or a resume will re-dispatch it
	ActionRetry                   // the pipeline stopped for a reason a re-dispatch can fix
	ActionHuman                   // a person has to look
)

// Action is the outcome of the mapping, with the short, stable reason to show.
type Action struct {
	Kind   ActionKind
	Reason string
}

// IssueState is everything NextAction looks at. Computing InFlight — a workflow
// run for this issue that is queued or in progress — is the caller's job:
// labels alone cannot tell a re-dispatched run from the last finished one,
// because the status label and the marker both survive until the next report.
type IssueState struct {
	Labels   []string
	Run      RunRecord
	HasRun   bool // LatestRun found a marker
	InFlight bool
	Now      time.Time
}

// NextAction maps one issue's pipeline state onto the four outcomes. It is pure:
// it reads nothing and writes nothing. The rows of the mapping are documented in
// docs/dispatch.md ("When a run fails") and the first matching one wins.
//
// Unknown state is never ActionRetry — an unattended re-dispatch spends money,
// so anything bridge cannot read goes to a person instead.
func NextAction(s IssueState) Action {
	if a, ok := labelAction(s); ok {
		return a
	}
	if a, ok := noMarkerAction(s); ok {
		return a
	}
	return runAction(s.Run, s.Now)
}

// labelAction covers the states visible from the labels and the in-flight
// check alone (rows 1-8).
func labelAction(s IssueState) (Action, bool) {
	if IsParked(s.Labels) {
		return Action{ActionHuman, "parked"}, true
	}
	if hasLabel(s.Labels, LabelAIPaused) {
		return Action{ActionWait, "paused"}, true
	}
	if s.InFlight {
		return Action{ActionWait, "running"}, true
	}
	if a, ok := blockedAction(s.Labels); ok {
		return a, true
	}
	if !hasLabel(s.Labels, LabelAIImplement) && statusLabel(s.Labels) == "" {
		return Action{ActionNone, ""}, true
	}
	return Action{}, false
}

// blockedAction covers the pipeline's "a person must look" labels (rows 4-7).
func blockedAction(labels []string) (Action, bool) {
	blocked := []struct{ label, reason string }{
		{LabelAIReviewBlocked, "review blocked"},
		{LabelAIReviewFailed, "review failed"},
		{LabelAIRunnerBlocked, "runner blocked"},
		{LabelAIChecksBlocked, "checks blocked"},
	}
	for _, b := range blocked {
		if hasLabel(labels, b.label) {
			return Action{ActionHuman, b.reason}, true
		}
	}
	return Action{}, false
}

// noMarkerAction covers a dispatched issue with no readable run marker (rows
// 9-10): either the report is still to come, or it predates the marker and
// carries neither class nor retry decision.
func noMarkerAction(s IssueState) (Action, bool) {
	if s.HasRun {
		return Action{}, false
	}
	switch statusLabel(s.Labels) {
	case "":
		return Action{ActionWait, "no run report yet"}, true
	case LabelAIDone:
		return Action{ActionNone, ""}, true
	default:
		return Action{ActionHuman, "no run marker"}, true
	}
}

// runAction maps the marker's outcome and failure class (rows 11-13, 19-20).
func runAction(r RunRecord, now time.Time) Action {
	switch r.Status {
	case LabelAIDone:
		return Action{ActionNone, ""}
	case LabelAIPartial:
		return Action{ActionHuman, "partial"}
	}
	switch r.Class {
	case "out_of_credits": // a pause class; pipeline-resume.sh re-dispatches it
		return Action{ActionWait, "paused"}
	case "transient", "rate_limit", "task_failure":
		return retriedClassAction(r, now)
	case "api_auth", "model_not_found", "bug":
		return Action{ActionHuman, r.Class}
	}
	if r.Class == "" {
		return Action{ActionHuman, "no failure class"}
	}
	return Action{ActionHuman, "unknown: " + r.Class}
}

// retriedClassAction maps the classes the pipeline retries itself (rows 14-18),
// so bridge only picks up what its attempt cap stopped on. task_failure stops at
// a person: the pipeline already retried the turn budget once, and a third try
// is what the cap exists to prevent.
func retriedClassAction(r RunRecord, now time.Time) Action {
	if r.Decision == "retry" {
		if now.Sub(r.Posted) > staleRetryAfter {
			return Action{ActionHuman, "retry never reported"}
		}
		return Action{ActionWait, "pipeline retrying"}
	}
	if r.Decision != "stop" {
		return Action{ActionHuman, "unknown: " + r.Class}
	}
	if r.Class == "task_failure" {
		return Action{ActionHuman, "task_failure"}
	}
	// Attempt is absent when the pipeline's cap was off (max-attempts: 0, a stub
	// run, the pause gate), and then nothing bounds a re-dispatch.
	if r.Attempt == 0 {
		return Action{ActionHuman, r.Class + ", no attempt cap"}
	}
	return Action{ActionRetry, r.Class + ", retries exhausted"}
}

// statusLabel returns the pipeline's outcome label on the issue, or "".
func statusLabel(labels []string) string {
	for _, want := range []string{LabelAIDone, LabelAIFailed, LabelAIPartial} {
		if hasLabel(labels, want) {
			return want
		}
	}
	return ""
}
