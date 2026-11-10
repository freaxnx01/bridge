package dispatch

import (
	"testing"
	"time"
)

func kindName(k ActionKind) string {
	switch k {
	case ActionNone:
		return "None"
	case ActionWait:
		return "Wait"
	case ActionRetry:
		return "Retry"
	case ActionHuman:
		return "Human"
	}
	return "?"
}

// TestNextAction walks the mapping table in
// docs/superpowers/specs/2026-11-10-pipeline-truth-next-action-design.md, one
// case per row. First matching row wins, so the order of the rows is part of
// the contract: the "beats" cases below pin it down.
func TestNextAction(t *testing.T) {
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	dispatched := []string{"ai-implement"}
	failed := []string{"ai-implement", "ai:failed"}

	// failure builds the marker of a run that stopped on class with decision.
	failure := func(class, decision string, attempt int) RunRecord {
		return RunRecord{
			Attempt: attempt, Counts: attempt > 0, Class: class, Decision: decision,
			Status: "ai:failed", Agent: "claude", Posted: now.Add(-10 * time.Minute),
		}
	}

	type tc struct {
		name     string
		labels   []string
		run      RunRecord
		hasRun   bool
		inFlight bool
		want     Action
	}

	cases := []tc{
		// Rows 1-7: labels and in-flight state, before any marker is read.
		{name: "row 1: parked", labels: []string{"parked"},
			want: Action{ActionHuman, "parked"}},
		{name: "row 1: parked, legacy spelling", labels: []string{"🧊 parked"},
			want: Action{ActionHuman, "parked"}},
		{name: "row 1: parked beats paused and in-flight",
			labels: []string{"ai-implement", "parked", "ai:paused"}, inFlight: true,
			want: Action{ActionHuman, "parked"}},
		{name: "row 2: paused", labels: []string{"ai-implement", "ai:paused"},
			want: Action{ActionWait, "paused"}},
		{name: "row 3: in flight", labels: dispatched, inFlight: true,
			want: Action{ActionWait, "running"}},
		{name: "row 3: in flight beats the previous run's marker",
			labels: failed, run: failure("transient", "stop", 1), hasRun: true, inFlight: true,
			want: Action{ActionWait, "running"}},
		{name: "row 4: review blocked", labels: []string{"ai-implement", "ai:review-blocked"},
			want: Action{ActionHuman, "review blocked"}},
		{name: "row 5: review failed", labels: []string{"ai-implement", "ai:review-failed"},
			want: Action{ActionHuman, "review failed"}},
		{name: "row 6: runner blocked", labels: []string{"ai-implement", "ai:runner-blocked"},
			want: Action{ActionHuman, "runner blocked"}},
		{name: "row 7: checks blocked", labels: []string{"ai-implement", "ai:checks-blocked"},
			want: Action{ActionHuman, "checks blocked"}},

		// Rows 8-10: nothing to read.
		{name: "row 8: never dispatched", labels: []string{"feat", "size:m"},
			want: Action{ActionNone, ""}},
		{name: "row 9: dispatched, no report yet", labels: dispatched,
			want: Action{ActionWait, "no run report yet"}},
		{name: "row 10: done without a marker", labels: []string{"ai-implement", "ai:done"},
			want: Action{ActionNone, ""}},
		{name: "row 10: failed without a marker", labels: failed,
			want: Action{ActionHuman, "no run marker"}},
		{name: "row 10: partial without a marker", labels: []string{"ai-implement", "ai:partial"},
			want: Action{ActionHuman, "no run marker"}},

		// Rows 11-13: the marker's own outcome.
		{name: "row 11: marker says done", labels: []string{"ai-implement", "ai:done"},
			run: RunRecord{Attempt: 1, Counts: true, Class: "success", Status: "ai:done",
				Decision: "stop", PR: 42, Posted: now.Add(-time.Minute)}, hasRun: true,
			want: Action{ActionNone, ""}},
		{name: "row 12: marker says partial", labels: []string{"ai-implement", "ai:partial"},
			run: RunRecord{Attempt: 1, Counts: true, Class: "task_failure", Status: "ai:partial",
				Decision: "stop", Posted: now.Add(-time.Minute)}, hasRun: true,
			want: Action{ActionHuman, "partial"}},
		{name: "row 13: out of credits", labels: dispatched,
			run: RunRecord{Class: "out_of_credits", Posted: now.Add(-time.Minute)}, hasRun: true,
			want: Action{ActionWait, "paused"}},

		// Rows 16-20: the pipeline stopped.
		{name: "row 16: transient, retries exhausted", labels: failed,
			run: failure("transient", "stop", 3), hasRun: true,
			want: Action{ActionRetry, "transient, retries exhausted"}},
		{name: "row 16: rate_limit, retries exhausted", labels: failed,
			run: failure("rate_limit", "stop", 2), hasRun: true,
			want: Action{ActionRetry, "rate_limit, retries exhausted"}},
		{name: "row 17: transient, no attempt cap", labels: failed,
			run: failure("transient", "stop", 0), hasRun: true,
			want: Action{ActionHuman, "transient, no attempt cap"}},
		{name: "row 17: rate_limit, no attempt cap", labels: failed,
			run: failure("rate_limit", "stop", 0), hasRun: true,
			want: Action{ActionHuman, "rate_limit, no attempt cap"}},
		{name: "row 18: task_failure", labels: failed,
			run: failure("task_failure", "stop", 2), hasRun: true,
			want: Action{ActionHuman, "task_failure"}},
		{name: "row 19: api_auth", labels: failed,
			run: failure("api_auth", "stop", 0), hasRun: true,
			want: Action{ActionHuman, "api_auth"}},
		{name: "row 19: model_not_found", labels: failed,
			run: failure("model_not_found", "stop", 0), hasRun: true,
			want: Action{ActionHuman, "model_not_found"}},
		{name: "row 19: bug", labels: failed,
			run: failure("bug", "stop", 1), hasRun: true,
			want: Action{ActionHuman, "bug"}},
		{name: "row 20: unknown class", labels: failed,
			run: failure("weird", "stop", 1), hasRun: true,
			want: Action{ActionHuman, "unknown: weird"}},
		{name: "row 20: no decision on a failure", labels: failed,
			run: failure("transient", "", 1), hasRun: true,
			want: Action{ActionHuman, "unknown: transient"}},
	}

	// Rows 14/15: the pipeline said it would retry. Exactly staleRetryAfter old
	// still waits; a second more asks a person.
	for _, class := range []string{"transient", "rate_limit", "task_failure"} {
		fresh := failure(class, "retry", 1)
		fresh.Posted = now.Add(-staleRetryAfter)
		stale := failure(class, "retry", 1)
		stale.Posted = now.Add(-staleRetryAfter - time.Second)
		cases = append(cases,
			tc{name: "row 15: " + class + ", pipeline retrying", labels: failed,
				run: fresh, hasRun: true, want: Action{ActionWait, "pipeline retrying"}},
			tc{name: "row 14: " + class + ", retry never reported", labels: failed,
				run: stale, hasRun: true, want: Action{ActionHuman, "retry never reported"}},
		)
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := NextAction(IssueState{
				Labels: c.labels, Run: c.run, HasRun: c.hasRun, InFlight: c.inFlight, Now: now,
			})
			if got != c.want {
				t.Errorf("got %s %q, want %s %q",
					kindName(got.Kind), got.Reason, kindName(c.want.Kind), c.want.Reason)
			}
		})
	}
}

// Retry is the one kind that spends money unattended, so it may only come from
// a run the pipeline's own attempt cap counted.
func TestNextAction_RetryOnlyWithACountedAttempt(t *testing.T) {
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	for _, class := range []string{"transient", "rate_limit", "task_failure", "api_auth", "bug", "weird"} {
		for _, attempt := range []int{0, 1} {
			s := IssueState{
				Labels: []string{"ai-implement", "ai:failed"}, HasRun: true, Now: now,
				Run: RunRecord{Class: class, Decision: "stop", Attempt: attempt,
					Status: "ai:failed", Posted: now.Add(-time.Minute)},
			}
			got := NextAction(s)
			retryable := attempt > 0 && (class == "transient" || class == "rate_limit")
			if (got.Kind == ActionRetry) != retryable {
				t.Errorf("%s attempt %d: got %s %q, want retry = %v",
					class, attempt, kindName(got.Kind), got.Reason, retryable)
			}
		}
	}
}
