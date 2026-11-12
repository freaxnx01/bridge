package dispatch

import (
	"slices"
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

func TestLastRedispatch(t *testing.T) {
	comment := func(author, body string, at time.Time) forge.Comment {
		return forge.Comment{Author: author, Body: body, Created: at}
	}
	prefixed := RedispatchComment("x")

	tests := []struct {
		name     string
		comments []forge.Comment
		want     time.Time
	}{
		{"no comments", nil, time.Time{}},
		{"one re-dispatch comment", []forge.Comment{
			comment("github-actions[bot]", prefixed, retryT0.Add(time.Minute))}, retryT0.Add(time.Minute)},
		{"two of them, the newer one wins", []forge.Comment{
			comment("github-actions[bot]", prefixed, retryT0.Add(time.Minute)),
			comment("github-actions[bot]", prefixed, retryT0.Add(5*time.Minute))}, retryT0.Add(5 * time.Minute)},
		{"the newer one wins whatever the order", []forge.Comment{
			comment("github-actions[bot]", prefixed, retryT0.Add(5*time.Minute)),
			comment("github-actions[bot]", prefixed, retryT0.Add(time.Minute))}, retryT0.Add(5 * time.Minute)},
		{"any author counts, the guard never spends money", []forge.Comment{
			comment("someone", prefixed, retryT0.Add(time.Minute))}, retryT0.Add(time.Minute)},
		{"the first-dispatch comment is not a retry", []forge.Comment{
			comment("github-actions[bot]", "Dispatched by `bridge dispatch`.", retryT0.Add(time.Minute))}, time.Time{}},
		{"the prefix must start the body", []forge.Comment{
			comment("someone", "note: "+prefixed, retryT0.Add(time.Minute))}, time.Time{}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := LastRedispatch(tc.comments); !got.Equal(tc.want) {
				t.Errorf("got %v want %v", got, tc.want)
			}
		})
	}
}

func TestStateFor_Fields(t *testing.T) {
	issue := forge.Issue{Number: 43, Title: "T", Labels: []string{LabelAIImplement, LabelAIFailed}}
	rateLimit := `{"v":1,"class":"rate_limit","decision":"stop","attempt":1,"status":"ai:failed"}`
	comments := []forge.Comment{
		botMarker(rateLimit, retryT0),
		{Author: "github-actions[bot]", Body: RedispatchComment("x"), Created: retryT0.Add(time.Minute)},
	}
	runs := []forge.WorkflowRun{{Event: "issues", Status: "in_progress", DisplayTitle: "T"}}
	prs := []forge.PullRequest{{Number: 7, Body: "Closes #43"}}

	s := StateFor(issue, comments, runs, prs, retryNow)
	if !slices.Equal(s.Labels, issue.Labels) {
		t.Errorf("Labels: got %v want %v", s.Labels, issue.Labels)
	}
	if !s.HasRun || s.Run.Class != "rate_limit" || !s.Run.Posted.Equal(retryT0) {
		t.Errorf("Run: got %+v hasRun %v", s.Run, s.HasRun)
	}
	if !s.InFlight {
		t.Error("InFlight: got false, want true for a matching in-progress run")
	}
	if !s.Redispatched.Equal(retryT0.Add(time.Minute)) {
		t.Errorf("Redispatched: got %v want %v", s.Redispatched, retryT0.Add(time.Minute))
	}
	if !s.HasOpenPR {
		t.Error("HasOpenPR: got false, want true for a PR closing this issue")
	}
	if !s.Now.Equal(retryNow) {
		t.Errorf("Now: got %v want %v", s.Now, retryNow)
	}

	other := StateFor(issue, comments, nil, []forge.PullRequest{{Number: 7, Body: "Closes #44"}}, retryNow)
	if other.HasOpenPR {
		t.Error("HasOpenPR: a PR closing another issue must not gate this one")
	}
	if other.InFlight {
		t.Error("InFlight: got true with no runs")
	}
}

// TestStateFor_NextAction is the retry pass end to end: the state the
// dispatcher and the cockpit inbox both build, mapped by NextAction.
func TestStateFor_NextAction(t *testing.T) {
	issue := forge.Issue{Number: 43, Title: "T", Labels: []string{LabelAIImplement, LabelAIFailed}}
	rateLimit := `{"v":1,"class":"rate_limit","decision":"stop","attempt":1,"status":"ai:failed"}`
	running := forge.WorkflowRun{Event: "issues", Status: "in_progress", DisplayTitle: "T"}
	redispatch := func(at time.Time) forge.Comment {
		return forge.Comment{Author: "github-actions[bot]", Body: RedispatchComment("x"), Created: at}
	}

	tests := []struct {
		name       string
		comments   []forge.Comment
		runs       []forge.WorkflowRun
		prs        []forge.PullRequest
		wantKind   ActionKind
		wantReason string
	}{
		{"rate-limited run with its retries exhausted",
			[]forge.Comment{botMarker(rateLimit, retryT0)}, nil, nil,
			ActionRetry, "rate_limit, retries exhausted"},
		{"a transient failure likewise",
			[]forge.Comment{botMarker(`{"v":1,"class":"transient","decision":"stop","attempt":1,"status":"ai:failed"}`, retryT0)}, nil, nil,
			ActionRetry, "transient, retries exhausted"},
		{"a run is already going for it",
			[]forge.Comment{botMarker(rateLimit, retryT0)}, []forge.WorkflowRun{running}, nil,
			ActionWait, "running"},
		{"bridge already re-dispatched since the report",
			[]forge.Comment{botMarker(rateLimit, retryT0), redispatch(retryT0.Add(time.Minute))}, nil, nil,
			ActionWait, "retry pending"},
		{"the re-dispatch never reported back",
			[]forge.Comment{botMarker(rateLimit, retryNow.Add(-5*time.Hour)),
				redispatch(retryNow.Add(-4 * time.Hour))}, nil, nil,
			ActionHuman, "re-dispatch never reported"},
		{"an older re-dispatch comment belongs to the reported run",
			[]forge.Comment{botMarker(rateLimit, retryT0), redispatch(retryT0.Add(-time.Minute))}, nil, nil,
			ActionRetry, "rate_limit, retries exhausted"},
		{"an open PR already closes it",
			[]forge.Comment{botMarker(rateLimit, retryT0)}, nil,
			[]forge.PullRequest{{Number: 7, Body: "Closes #43"}},
			ActionHuman, "open PR"},
		{"a PR closing another issue does not gate it",
			[]forge.Comment{botMarker(rateLimit, retryT0)}, nil,
			[]forge.PullRequest{{Number: 7, Body: "Closes #44"}},
			ActionRetry, "rate_limit, retries exhausted"},
		{"a task failure is a person's call",
			[]forge.Comment{botMarker(`{"v":1,"class":"task_failure","decision":"stop","attempt":1,"status":"ai:failed"}`, retryT0)}, nil, nil,
			ActionHuman, "task_failure"},
		{"no run marker at all", nil, nil, nil, ActionHuman, "no run marker"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := NextAction(StateFor(issue, tc.comments, tc.runs, tc.prs, retryNow))
			if got.Kind != tc.wantKind || got.Reason != tc.wantReason {
				t.Errorf("got %+v want {%v %q}", got, tc.wantKind, tc.wantReason)
			}
		})
	}
}
