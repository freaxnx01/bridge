package main

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/freaxnx01/bridge/internal/dispatch"
	"github.com/freaxnx01/bridge/internal/forge"
)

// fakeRetryReader stands in for the GitHub client the retry collector reads
// through, and counts calls so "this costs no extra API call" is testable.
type fakeRetryReader struct {
	issues     map[int][]forge.Comment
	issueErr   map[int]error
	runs       []forge.WorkflowRun
	runsErr    error
	runsCalls  int
	issueCalls map[int]int
}

func (f *fakeRetryReader) GetIssue(_ context.Context, _, _ string, number int) (forge.Issue, []forge.Comment, error) {
	if f.issueCalls == nil {
		f.issueCalls = map[int]int{}
	}
	f.issueCalls[number]++
	if err := f.issueErr[number]; err != nil {
		return forge.Issue{}, nil, err
	}
	return forge.Issue{Number: number}, f.issues[number], nil
}

func (f *fakeRetryReader) ListWorkflowRuns(_ context.Context, _, _, _, _ string, _ int) ([]forge.WorkflowRun, error) {
	f.runsCalls++
	if f.runsErr != nil {
		return nil, f.runsErr
	}
	return f.runs, nil
}

const rateLimitMarker = `{"v":1,"class":"rate_limit","decision":"stop","attempt":1,"status":"ai:failed"}`

var retryMarkerAt = time.Date(2026, 11, 10, 10, 0, 0, 0, time.UTC)

func retryTickNow() time.Time { return retryMarkerAt.Add(time.Hour) }

func markerComment(marker string, at time.Time) forge.Comment {
	return forge.Comment{Author: "github-actions[bot]",
		Body: "report\n<!-- ai-run: " + marker + " -->", Created: at}
}

// failedIssue is the shape the retry pass is built around: dispatched, failed,
// with a run report to read.
func failedIssue(number int, labels ...string) forge.Issue {
	if len(labels) == 0 {
		labels = []string{"ai-implement", "ai:failed"}
	}
	return forge.Issue{Number: number, Title: "T", Labels: labels}
}

func githubRepoInput(issues ...forge.Issue) repoInput {
	return repoInput{Forge: "github", Owner: "freaxnx01", Name: "bridge", Issues: issues}
}

func TestCollectRetries_FailedRateLimitStop_Selected(t *testing.T) {
	gh := &fakeRetryReader{issues: map[int][]forge.Comment{
		43: {markerComment(rateLimitMarker, retryMarkerAt)},
	}}

	cands, skips := collectRetries(context.Background(), gh,
		[]repoInput{githubRepoInput(failedIssue(43))}, retryTickNow())

	if len(cands) != 1 {
		t.Fatalf("want 1 retry candidate, got %d: %+v", len(cands), cands)
	}
	c := cands[0]
	if !c.Retry || c.RetryReason != "rate_limit, retries exhausted" {
		t.Errorf("candidate = %+v", c)
	}
	if c.Issue.Number != 43 || c.Repo != "bridge" || c.Owner != "freaxnx01" {
		t.Errorf("repo context lost: %+v", c)
	}
	if len(skips) != 0 {
		t.Errorf("want no skips, got %+v", skips)
	}
	if gh.runsCalls != 1 {
		t.Errorf("runs must be fetched exactly once per repo, got %d", gh.runsCalls)
	}
	if gh.issueCalls[43] != 1 {
		t.Errorf("issueCalls = %v", gh.issueCalls)
	}
}

func TestCollectRetries_NoPredicateMatch_NoAPICalls(t *testing.T) {
	gh := &fakeRetryReader{}
	repos := []repoInput{
		githubRepoInput(
			forge.Issue{Number: 41, Labels: []string{"ai-implement"}},
			forge.Issue{Number: 42, Labels: []string{"feat"}},
		),
		{Forge: "forgejo", Owner: "f", Name: "notes", Issues: []forge.Issue{failedIssue(1)}},
	}

	cands, skips := collectRetries(context.Background(), gh, repos, retryTickNow())

	if len(cands) != 0 || len(skips) != 0 {
		t.Errorf("nothing to retry: cands=%+v skips=%+v", cands, skips)
	}
	if gh.runsCalls != 0 {
		t.Errorf("a repo with no candidate must cost no API call, got %d", gh.runsCalls)
	}
}

func TestCollectRetries_ParkedFailed_SkippedWithoutFetch(t *testing.T) {
	gh := &fakeRetryReader{}

	cands, skips := collectRetries(context.Background(), gh,
		[]repoInput{githubRepoInput(failedIssue(43, "ai-implement", "ai:failed", "parked"))},
		retryTickNow())

	if len(cands) != 0 {
		t.Errorf("a parked issue is never retried: %+v", cands)
	}
	if len(skips) != 1 || skips[0].Reason != "parked" || skips[0].Dispatch {
		t.Fatalf("skips = %+v", skips)
	}
	if !skips[0].Candidate.Retry {
		t.Error("a skipped retry row must still be marked as one")
	}
	if len(gh.issueCalls) != 0 {
		t.Errorf("the label predicate must decide before any fetch: %v", gh.issueCalls)
	}
}

func TestCollectRetries_RunsUnreadable_FailsClosed(t *testing.T) {
	gh := &fakeRetryReader{
		runsErr: errors.New("boom"),
		issues:  map[int][]forge.Comment{43: {markerComment(rateLimitMarker, retryMarkerAt)}},
	}

	cands, skips := collectRetries(context.Background(), gh,
		[]repoInput{githubRepoInput(failedIssue(43))}, retryTickNow())

	if len(cands) != 0 {
		t.Errorf("not knowing whether a run is in flight must never read as 'not in flight': %+v", cands)
	}
	if len(skips) != 1 || skips[0].Reason != "runs unreadable" {
		t.Fatalf("skips = %+v", skips)
	}
	if len(gh.issueCalls) != 0 {
		t.Errorf("no comment fetch once the repo is unreadable: %v", gh.issueCalls)
	}
}

func TestCollectRetries_IssueUnreadable_OnlyThatOne(t *testing.T) {
	gh := &fakeRetryReader{
		issueErr: map[int]error{43: errors.New("boom")},
		issues:   map[int][]forge.Comment{44: {markerComment(rateLimitMarker, retryMarkerAt)}},
	}
	repos := []repoInput{githubRepoInput(failedIssue(43), failedIssue(44))}

	cands, skips := collectRetries(context.Background(), gh, repos, retryTickNow())

	if len(cands) != 1 || cands[0].Issue.Number != 44 {
		t.Fatalf("the readable candidate must survive: %+v", cands)
	}
	if len(skips) != 1 || skips[0].Candidate.Issue.Number != 43 || skips[0].Reason != "issue unreadable" {
		t.Fatalf("skips = %+v", skips)
	}
}

func TestCollectRetries_SkipReasons_Table(t *testing.T) {
	tests := []struct {
		name       string
		issue      forge.Issue
		comments   []forge.Comment
		runs       []forge.WorkflowRun
		wantReason string
	}{
		{"a run is already going", failedIssue(43),
			[]forge.Comment{markerComment(rateLimitMarker, retryMarkerAt)},
			[]forge.WorkflowRun{{Event: "issues", Status: "queued", DisplayTitle: "T"}},
			"running"},
		{"bridge already re-dispatched it", failedIssue(43),
			[]forge.Comment{
				markerComment(rateLimitMarker, retryMarkerAt),
				{Author: "github-actions[bot]", Body: dispatch.RedispatchComment("x"),
					Created: retryMarkerAt.Add(time.Hour)},
			}, nil, "retry pending"},
		{"a task failure needs a person", failedIssue(43),
			[]forge.Comment{markerComment(
				`{"v":1,"class":"task_failure","decision":"stop","attempt":1,"status":"ai:failed"}`,
				retryMarkerAt)}, nil, "task_failure"},
		{"an interrupted swap", failedIssue(43, "ai-implement", "ai:failed", "ai:retrying"),
			nil, nil, "retry interrupted"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gh := &fakeRetryReader{
				issues: map[int][]forge.Comment{43: tc.comments},
				runs:   tc.runs,
			}

			cands, skips := collectRetries(context.Background(), gh,
				[]repoInput{githubRepoInput(tc.issue)}, retryMarkerAt.Add(2*time.Hour))

			if len(cands) != 0 {
				t.Errorf("want no selection, got %+v", cands)
			}
			if len(skips) != 1 || skips[0].Reason != tc.wantReason {
				t.Fatalf("skips = %+v, want reason %q", skips, tc.wantReason)
			}
		})
	}
}

func TestDecisionStatus_Retry(t *testing.T) {
	const reason = "rate_limit, retries exhausted"
	auto := dispatch.Lane{Name: "auto", Autonomous: true}
	dry := dispatch.Lane{Name: "auto", Autonomous: true, DryRun: true}
	retry := func(lane dispatch.Lane, laneReason string) dispatch.Candidate {
		return dispatch.Candidate{Lane: lane, LaneReason: laneReason, Retry: true, RetryReason: reason}
	}

	tests := []struct {
		name string
		d    dispatch.Decision
		want string
	}{
		{"live retry", dispatch.Decision{Candidate: retry(auto, ""), Dispatch: true},
			"retry: rate_limit, retries exhausted"},
		{"dry-run lane", dispatch.Decision{Candidate: retry(dry, ""), Dispatch: true},
			"WOULD retry: rate_limit, retries exhausted (dry-run)"},
		{"skipped retry", dispatch.Decision{Candidate: retry(auto, ""), Reason: "retry pending"},
			"SKIP retry (retry pending)"},
		{"a lane downgrade shows on a skip", dispatch.Decision{
			Candidate: retry(dispatch.Lane{Name: "hitl"}, "auto→hitl: x"), Reason: "global cap 3/3"},
			"SKIP retry (auto→hitl: x; global cap 3/3)"},
		{"and on a dispatch", dispatch.Decision{
			Candidate: retry(dispatch.Lane{Name: "hitl"}, "auto→hitl: x"), Dispatch: true},
			"retry: rate_limit, retries exhausted (auto→hitl: x)"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := decisionStatus(tc.d); got != tc.want {
				t.Errorf("got %q want %q", got, tc.want)
			}
		})
	}
}

func TestDecisionsJSON_Kind(t *testing.T) {
	const reason = "rate_limit, retries exhausted"
	js := decisionsJSON([]dispatch.Decision{
		{Candidate: dispatch.Candidate{Repo: "bridge", Issue: forge.Issue{Number: 41}}, Dispatch: true},
		{Candidate: dispatch.Candidate{Repo: "bridge", Issue: forge.Issue{Number: 43},
			Retry: true, RetryReason: reason}, Dispatch: true},
		{Candidate: dispatch.Candidate{Repo: "bridge", Issue: forge.Issue{Number: 44},
			Retry: true, RetryReason: reason}, Reason: "global cap 3/3"},
	})

	if len(js) != 3 {
		t.Fatalf("%+v", js)
	}
	if js[0].Kind != "dispatch" || js[0].RetryReason != "" {
		t.Errorf("plain decision: %+v", js[0])
	}
	if js[1].Kind != "retry" || js[1].RetryReason != reason {
		t.Errorf("retry decision: %+v", js[1])
	}
	if js[2].Reason != "global cap 3/3" || js[2].RetryReason != reason {
		t.Errorf("a capped retry needs both reasons: %+v", js[2])
	}

	raw, err := json.Marshal(js[0])
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	if fields["kind"] != "dispatch" {
		t.Errorf(`want "kind":"dispatch" in %s`, raw)
	}
	if _, ok := fields["retry_reason"]; ok {
		t.Errorf("retry_reason must be omitted on a plain dispatch: %s", raw)
	}
}
