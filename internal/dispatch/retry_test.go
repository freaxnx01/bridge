package dispatch

import (
	"testing"
	"time"

	"github.com/freaxnx01/bridge/internal/forge"
)

func TestRetryCandidate(t *testing.T) {
	tests := []struct {
		name       string
		labels     []string
		wantOK     bool
		wantReason string
	}{
		{"a failed dispatched issue", []string{"ai-implement", "ai:failed"}, true, ""},
		{"extra labels do not matter", []string{"ai-implement", "ai:failed", "feat"}, true, ""},
		{"dispatched but not failed", []string{"ai-implement"}, false, ""},
		{"failed but no longer dispatched", []string{"ai:failed"}, false, ""},
		{"no labels at all", nil, false, ""},
		{"parked", []string{"ai-implement", "ai:failed", "parked"}, false, "parked"},
		{"parked, legacy spelling", []string{"ai-implement", "ai:failed", "🧊 parked"}, false, "parked"},
		{"paused", []string{"ai-implement", "ai:failed", "ai:paused"}, false, "paused"},
		{"an interrupted swap", []string{"ai-implement", "ai:failed", "ai:retrying"}, false, "retry interrupted"},
		{"a swap interrupted after step 2", []string{"ai:retrying"}, false, "retry interrupted"},
		{"retrying beats parked", []string{"ai:retrying", "parked"}, false, "retry interrupted"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ok, reason := RetryCandidate(tc.labels)
			if ok != tc.wantOK || reason != tc.wantReason {
				t.Errorf("got (%v, %q) want (%v, %q)", ok, reason, tc.wantOK, tc.wantReason)
			}
		})
	}
}

// botMarker is a pipeline run report carrying the given marker JSON.
func botMarker(json string, at time.Time) forge.Comment {
	return forge.Comment{
		Author:  "github-actions[bot]",
		Body:    "report\n<!-- ai-run: " + json + " -->",
		Created: at,
	}
}

var (
	retryT0  = time.Date(2026, 11, 10, 10, 0, 0, 0, time.UTC)
	retryNow = retryT0.Add(time.Hour)
)

func TestRedispatchComment_ExactText(t *testing.T) {
	got := RedispatchComment("rate_limit, retries exhausted")
	want := "Re-dispatched by `bridge dispatch` (retry: rate_limit, retries exhausted)."
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
	// The guard keys on the very text the tick posts, so the two must agree.
	posted := []forge.Comment{{Author: "github-actions[bot]", Body: got, Created: retryT0.Add(time.Minute)}}
	if !RetryPending(posted, retryT0) {
		t.Error("the comment the tick posts must satisfy the guard")
	}
}

func TestRetryPending(t *testing.T) {
	comment := func(author, body string, at time.Time) forge.Comment {
		return forge.Comment{Author: author, Body: body, Created: at}
	}
	prefixed := RedispatchComment("x")

	tests := []struct {
		name     string
		comments []forge.Comment
		want     bool
	}{
		{"no comments", nil, false},
		{"a newer re-dispatch comment", []forge.Comment{
			comment("github-actions[bot]", prefixed, retryT0.Add(time.Minute))}, true},
		{"posted at the very instant of the run report", []forge.Comment{
			comment("github-actions[bot]", prefixed, retryT0)}, false},
		{"older than the run report", []forge.Comment{
			comment("github-actions[bot]", prefixed, retryT0.Add(-time.Minute))}, false},
		{"any author counts, the guard only ever suppresses", []forge.Comment{
			comment("someone", prefixed, retryT0.Add(time.Minute))}, true},
		{"the first-dispatch comment is not a retry", []forge.Comment{
			comment("github-actions[bot]", "Dispatched by `bridge dispatch`.", retryT0.Add(time.Minute))}, false},
		{"the prefix must start the body", []forge.Comment{
			comment("someone", "note: "+prefixed, retryT0.Add(time.Minute))}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := RetryPending(tc.comments, retryT0); got != tc.want {
				t.Errorf("got %v want %v", got, tc.want)
			}
		})
	}
}

func TestEvaluateRetry(t *testing.T) {
	issue := forge.Issue{Number: 43, Title: "T", Labels: []string{LabelAIImplement, LabelAIFailed}}
	rateLimit := `{"v":1,"class":"rate_limit","decision":"stop","attempt":1,"status":"ai:failed"}`
	running := forge.WorkflowRun{Event: "issues", Status: "in_progress", DisplayTitle: "T"}

	tests := []struct {
		name       string
		comments   []forge.Comment
		runs       []forge.WorkflowRun
		wantKind   ActionKind
		wantReason string
	}{
		{"rate-limited run with its retries exhausted",
			[]forge.Comment{botMarker(rateLimit, retryT0)}, nil,
			ActionRetry, "rate_limit, retries exhausted"},
		{"a transient failure likewise",
			[]forge.Comment{botMarker(`{"v":1,"class":"transient","decision":"stop","attempt":1,"status":"ai:failed"}`, retryT0)}, nil,
			ActionRetry, "transient, retries exhausted"},
		{"a run is already going for it",
			[]forge.Comment{botMarker(rateLimit, retryT0)}, []forge.WorkflowRun{running},
			ActionWait, "running"},
		{"bridge already re-dispatched since the report",
			[]forge.Comment{botMarker(rateLimit, retryT0),
				{Author: "github-actions[bot]", Body: RedispatchComment("x"), Created: retryT0.Add(time.Minute)}}, nil,
			ActionWait, "retry pending"},
		{"an older re-dispatch comment belongs to the reported run",
			[]forge.Comment{botMarker(rateLimit, retryT0),
				{Author: "github-actions[bot]", Body: RedispatchComment("x"), Created: retryT0.Add(-time.Minute)}}, nil,
			ActionRetry, "rate_limit, retries exhausted"},
		{"a task failure is a person's call",
			[]forge.Comment{botMarker(`{"v":1,"class":"task_failure","decision":"stop","attempt":1,"status":"ai:failed"}`, retryT0)}, nil,
			ActionHuman, "task_failure"},
		{"no run marker at all", nil, nil, ActionHuman, "no run marker"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := EvaluateRetry(issue, tc.comments, tc.runs, retryNow)
			if got.Kind != tc.wantKind || got.Reason != tc.wantReason {
				t.Errorf("got %+v want {%v %q}", got, tc.wantKind, tc.wantReason)
			}
		})
	}
}
