package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/freaxnx01/bridge/internal/dispatch"
	"github.com/freaxnx01/bridge/internal/forge"
	"github.com/freaxnx01/bridge/internal/usage"
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

// ago renders a timestamp d before now the way GitHub's API does. A tick run
// through runDispatch reads the wall clock, so every fixture it reads a
// duration off — the run marker and the re-dispatch comments the 3 h guard
// compares against it — is dated relative to now rather than to retryMarkerAt.
func ago(d time.Duration) string {
	return time.Now().UTC().Add(-d).Format(time.RFC3339)
}

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
		prs        []forge.PullRequest
		wantReason string
	}{
		{"a run is already going", failedIssue(43),
			[]forge.Comment{markerComment(rateLimitMarker, retryMarkerAt)},
			[]forge.WorkflowRun{{Event: "issues", Status: "queued", DisplayTitle: "T"}}, nil,
			"running"},
		{"bridge already re-dispatched it", failedIssue(43),
			[]forge.Comment{
				markerComment(rateLimitMarker, retryMarkerAt),
				{Author: "github-actions[bot]", Body: dispatch.RedispatchComment("x"),
					Created: retryMarkerAt.Add(time.Hour)},
			}, nil, nil, "retry pending"},
		// The table's tick runs two hours on from retryMarkerAt, so a re-dispatch
		// past the 3 h guard has to sit before it.
		{"the re-dispatch never reported back", failedIssue(43),
			[]forge.Comment{
				markerComment(rateLimitMarker, retryMarkerAt.Add(-5*time.Hour)),
				{Author: "github-actions[bot]", Body: dispatch.RedispatchComment("x"),
					Created: retryMarkerAt.Add(-4 * time.Hour)},
			}, nil, nil, "re-dispatch never reported"},
		{"an open PR already closes it", failedIssue(43),
			[]forge.Comment{markerComment(rateLimitMarker, retryMarkerAt)}, nil,
			[]forge.PullRequest{{Number: 7, Body: "Closes #43"}}, "open PR"},
		{"a task failure needs a person", failedIssue(43),
			[]forge.Comment{markerComment(
				`{"v":1,"class":"task_failure","decision":"stop","attempt":1,"status":"ai:failed"}`,
				retryMarkerAt)}, nil, nil, "task_failure"},
		{"an interrupted swap", failedIssue(43, "ai-implement", "ai:failed", "ai:retrying"),
			nil, nil, nil, "retry interrupted"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gh := &fakeRetryReader{
				issues: map[int][]forge.Comment{43: tc.comments},
				runs:   tc.runs,
			}

			in := githubRepoInput(tc.issue)
			in.PRs = tc.prs
			cands, skips := collectRetries(context.Background(), gh,
				[]repoInput{in}, retryMarkerAt.Add(2*time.Hour))

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

// writeLog records every forge write the tick performs, in order. The handler
// runs on the server's goroutine, so the mutex is what keeps -race quiet.
type writeLog struct {
	mu     sync.Mutex
	calls  []string
	bodies []string
}

func (l *writeLog) add(call, body string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls = append(l.calls, call)
	l.bodies = append(l.bodies, body)
}

func (l *writeLog) snapshot() ([]string, []string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.calls), slices.Clone(l.bodies)
}

// retryFixture is one fake repo's state: what the listing, the comment thread
// and the run list return, and which single write fails.
type retryFixture struct {
	issues    string // JSON array for GET …/issues
	comments  string // JSON array for GET …/issues/43/comments
	runs      string // JSON array for the workflow_runs field
	prs       string // JSON array for GET …/pulls
	failWrite string // "METHOD path" answered with 500
}

const retryIssueJSON = `{"number":43,"title":"T",` +
	`"body":"a real description\n## Implementation Plan\n- step","html_url":"u43",` +
	`"labels":[{"name":"ai-implement"},{"name":"ai:failed"}],` +
	`"updated_at":"2026-07-01T00:00:00Z","created_at":"2026-06-01T00:00:00Z"}`

const eligibleIssueJSON = `{"number":41,"title":"eligible issue",` +
	`"body":"a real description\n## Implementation Plan\n- step","html_url":"u41",` +
	`"labels":[{"name":"bug"}],` +
	`"updated_at":"2026-07-01T00:00:00Z","created_at":"2026-06-01T00:00:00Z"}`

// commentJSON renders one comment the way GitHub's API returns it. Built
// rather than hand-written because the run marker is itself JSON, and nesting
// it unescaped inside a body string silently breaks the fixture.
func commentJSON(t *testing.T, id int, author, body, created string) string {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"id":         id,
		"user":       map[string]string{"login": author},
		"body":       body,
		"created_at": created,
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// markerCommentJSON is the pipeline's run report for #43: rate-limited,
// stopped, one attempt spent — NextAction's ActionRetry row. Six hours back, so
// every re-dispatch comment the guard tests date relative to now is newer than
// it whatever the wall-clock date is.
func markerCommentJSON(t *testing.T) string {
	t.Helper()
	return commentJSON(t, 1, "github-actions[bot]",
		"report\n<!-- ai-run: "+rateLimitMarker+" -->", ago(6*time.Hour))
}

func newRetryGithub(t *testing.T, f retryFixture) (*httptest.Server, *writeLog) {
	t.Helper()
	log := &writeLog{}
	if f.issues == "" {
		f.issues = "[" + retryIssueJSON + "]"
	}
	if f.comments == "" {
		f.comments = "[" + markerCommentJSON(t) + "]"
	}
	if f.runs == "" {
		f.runs = "[]"
	}
	if f.prs == "" {
		f.prs = "[]"
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := r.Method + " " + r.URL.Path
		if r.Method != "GET" {
			body, _ := io.ReadAll(r.Body)
			log.add(call, string(body))
			if call == f.failWrite {
				w.WriteHeader(http.StatusInternalServerError)
				w.Write([]byte(`{"message":"boom"}`))
				return
			}
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == "GET" && r.URL.Path == "/repos/freaxnx01/bridge/issues":
			w.Write([]byte(f.issues))
		case r.Method == "GET" && r.URL.Path == "/repos/freaxnx01/bridge/milestones":
			w.Write([]byte(`[]`))
		case r.Method == "GET" && r.URL.Path == "/repos/freaxnx01/bridge/pulls":
			w.Write([]byte(f.prs))
		case r.Method == "GET" && r.URL.Path == "/repos/freaxnx01/bridge/actions/runs":
			w.Write([]byte(`{"workflow_runs":` + f.runs + `}`))
		case r.Method == "GET" && r.URL.Path == "/repos/freaxnx01/bridge/issues/43":
			w.Write([]byte(retryIssueJSON))
		case r.Method == "GET" && r.URL.Path == "/repos/freaxnx01/bridge/issues/43/comments":
			w.Write([]byte(f.comments))
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/labels"):
			w.Write([]byte(`[{"name":"ai-implement"}]`))
		case r.Method == "DELETE" && strings.Contains(r.URL.Path, "/labels/"):
			w.WriteHeader(http.StatusOK)
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/comments"):
			w.WriteHeader(http.StatusCreated)
			w.Write([]byte(`{"id":2,"body":"x","created_at":"2026-11-10T12:00:00Z"}`))
		default:
			t.Errorf("unexpected request: %s", call)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, log
}

// retryTickEnv wires the command layer at the fake forge, with the schedule
// pinned so the tick never depends on the wall-clock hour.
func retryTickEnv(t *testing.T, srv *httptest.Server) {
	t.Helper()
	t.Setenv("BRIDGE_REPOS_ROOT", writeFakeGithubRepo(t, "freaxnx01", "bridge"))
	t.Setenv("BRIDGE_GITHUB_API", srv.URL)
	t.Setenv("GH_TOKEN", "tok")
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("BRIDGE_CLAUDE_PROJECTS", filepath.Join(t.TempDir(), "absent"))
	pinUnattendedSchedule(t)
}

// setRetryOnly turns on --retry-only for one test, like setDispatchFlags does
// for the other three.
func setRetryOnly(t *testing.T) {
	t.Helper()
	dispatchRetryOnly = true
	t.Cleanup(func() { dispatchRetryOnly = false })
}

func runRetryTick(t *testing.T) (string, error) {
	t.Helper()
	cmd := &cobra.Command{}
	var out bytes.Buffer
	cmd.SetOut(&out)
	err := runDispatch(cmd, nil)
	return out.String(), err
}

func TestRunDispatch_RetryCandidate_SwapsLabelsInOrder(t *testing.T) {
	srv, log := newRetryGithub(t, retryFixture{})
	retryTickEnv(t, srv)
	setDispatchFlags(t, false, false)

	out, err := runRetryTick(t)
	if err != nil {
		t.Fatalf("runDispatch: %v", err)
	}

	calls, bodies := log.snapshot()
	want := []string{
		"POST /repos/freaxnx01/bridge/issues/43/labels",
		"DELETE /repos/freaxnx01/bridge/issues/43/labels/ai-implement",
		"POST /repos/freaxnx01/bridge/issues/43/labels",
		"DELETE /repos/freaxnx01/bridge/issues/43/labels/ai:retrying",
		"POST /repos/freaxnx01/bridge/issues/43/comments",
	}
	if !slices.Equal(calls, want) {
		t.Fatalf("writes =\n  %v\nwant\n  %v", calls, want)
	}
	if got := labelsOf(t, bodies[0]); !slices.Equal(got, []string{"ai:retrying"}) {
		t.Errorf("step 1 labels = %v", got)
	}
	if got := labelsOf(t, bodies[2]); !slices.Equal(got, []string{"ai-implement"}) {
		t.Errorf("step 3 labels = %v", got)
	}
	var comment struct {
		Body string `json:"body"`
	}
	if err := json.Unmarshal([]byte(bodies[4]), &comment); err != nil {
		t.Fatal(err)
	}
	wantBody := "Re-dispatched by `bridge dispatch` (retry: rate_limit, retries exhausted)."
	if comment.Body != wantBody {
		t.Errorf("comment = %q want %q", comment.Body, wantBody)
	}

	ledger, lerr := usage.LoadLedger(dispatchLedgerPath())
	if lerr != nil {
		t.Fatal(lerr)
	}
	if len(ledger.Runs) != 1 || ledger.Runs[0].Issue != 43 {
		t.Errorf("a retry is booked like a dispatch: %+v", ledger.Runs)
	}
	if !strings.Contains(out, "retry: rate_limit, retries exhausted") {
		t.Errorf("output must name the retry:\n%s", out)
	}
}

func labelsOf(t *testing.T, body string) []string {
	t.Helper()
	var got struct {
		Labels []string `json:"labels"`
	}
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("label body %q: %v", body, err)
	}
	return got.Labels
}

func TestRunDispatch_RetryDryRun_NoWrites(t *testing.T) {
	srv, log := newRetryGithub(t, retryFixture{})
	retryTickEnv(t, srv)
	setDispatchFlags(t, false, true) // --dry-run

	out, err := runRetryTick(t)
	if err != nil {
		t.Fatalf("runDispatch: %v", err)
	}

	if calls, _ := log.snapshot(); len(calls) != 0 {
		t.Errorf("--dry-run must write nothing: %v", calls)
	}
	if !strings.Contains(out, "retry: rate_limit") {
		t.Errorf("the decision must still be shown:\n%s", out)
	}
}

func TestRunDispatch_RetryOnly_SkipsEligibleNewIssue(t *testing.T) {
	srv, log := newRetryGithub(t, retryFixture{
		issues: "[" + eligibleIssueJSON + "," + retryIssueJSON + "]",
	})
	retryTickEnv(t, srv)
	setDispatchFlags(t, false, false)
	setRetryOnly(t)

	out, err := runRetryTick(t)
	if err != nil {
		t.Fatalf("runDispatch: %v", err)
	}

	calls, _ := log.snapshot()
	for _, c := range calls {
		if strings.Contains(c, "/issues/41") {
			t.Errorf("--retry-only must not touch the new-dispatch pass: %v", calls)
		}
	}
	if len(calls) != 5 {
		t.Errorf("the retry still runs in full: %v", calls)
	}
	if strings.Contains(out, "#41") {
		t.Errorf("a new candidate must not even be listed:\n%s", out)
	}
}

func TestRunDispatch_RetryInFlight_NoWrites(t *testing.T) {
	srv, log := newRetryGithub(t, retryFixture{
		runs: `[{"event":"issues","status":"in_progress","display_title":"T"}]`,
	})
	retryTickEnv(t, srv)
	setDispatchFlags(t, false, false)

	out, err := runRetryTick(t)
	if err != nil {
		t.Fatalf("runDispatch: %v", err)
	}

	if calls, _ := log.snapshot(); len(calls) != 0 {
		t.Errorf("a running pipeline must not be re-dispatched on top of: %v", calls)
	}
	if !strings.Contains(out, "SKIP retry (running)") {
		t.Errorf("output:\n%s", out)
	}
}

func TestRunDispatch_RetryPendingComment_NoWrites(t *testing.T) {
	// The tick runs on the wall clock, and the guard times out after 3 hours, so
	// a pending re-dispatch has to be dated relative to now.
	pending := commentJSON(t, 2, "github-actions[bot]",
		dispatch.RedispatchComment("x"), ago(time.Hour))
	srv, log := newRetryGithub(t, retryFixture{
		comments: "[" + markerCommentJSON(t) + "," + pending + "]",
	})
	retryTickEnv(t, srv)
	setDispatchFlags(t, false, false)

	out, err := runRetryTick(t)
	if err != nil {
		t.Fatalf("runDispatch: %v", err)
	}

	if calls, _ := log.snapshot(); len(calls) != 0 {
		t.Errorf("bridge already retried since the report: %v", calls)
	}
	if !strings.Contains(out, "SKIP retry (retry pending)") {
		t.Errorf("output:\n%s", out)
	}
}

func TestRunDispatch_RetryWithOpenPR_NoWrites(t *testing.T) {
	srv, log := newRetryGithub(t, retryFixture{
		prs: `[{"number":7,"title":"p","state":"open","body":"Closes #43",` +
			`"head":{"ref":"x","sha":"abc"},"base":{"ref":"main"},` +
			`"html_url":"p7","created_at":"2026-07-01T00:00:00Z"}]`,
	})
	retryTickEnv(t, srv)
	setDispatchFlags(t, false, false)

	out, err := runRetryTick(t)
	if err != nil {
		t.Fatalf("runDispatch: %v", err)
	}

	calls, _ := log.snapshot()
	for _, c := range calls {
		if strings.Contains(c, "/issues/43") {
			t.Errorf("a run must not be stacked next to the PR that closes it: %v", calls)
		}
	}
	if !strings.Contains(out, "SKIP retry (open PR)") {
		t.Errorf("output:\n%s", out)
	}
}

func TestRunDispatch_RetryStaleRedispatch_NoWritesHumanReason(t *testing.T) {
	// A re-dispatch that never reported back: four hours on from the comment,
	// past the three the guard waits.
	stale := commentJSON(t, 2, "github-actions[bot]",
		dispatch.RedispatchComment("x"), ago(4*time.Hour))
	srv, log := newRetryGithub(t, retryFixture{
		comments: "[" + markerCommentJSON(t) + "," + stale + "]",
	})
	retryTickEnv(t, srv)
	setDispatchFlags(t, false, false)

	out, err := runRetryTick(t)
	if err != nil {
		t.Fatalf("runDispatch: %v", err)
	}

	if calls, _ := log.snapshot(); len(calls) != 0 {
		t.Errorf("a lost re-dispatch is a person's call, not a second one: %v", calls)
	}
	if !strings.Contains(out, "SKIP retry (re-dispatch never reported)") {
		t.Errorf("output:\n%s", out)
	}
}

// Before step 3 of the swap no pipeline has fired, so a failure there must
// leave the spend ledger untouched — the mirror of the step-4 case above.
func TestRunDispatch_RetrySwapFailsEarly_BooksNothing(t *testing.T) {
	tests := []struct {
		name      string
		failWrite string
		wantStep  string
	}{
		{"step 1 cannot set the bracket label",
			"POST /repos/freaxnx01/bridge/issues/43/labels", "step 1"},
		{"step 2 cannot drop ai-implement",
			"DELETE /repos/freaxnx01/bridge/issues/43/labels/ai-implement", "step 2"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv, log := newRetryGithub(t, retryFixture{failWrite: tc.failWrite})
			retryTickEnv(t, srv)
			setDispatchFlags(t, false, false)

			_, err := runRetryTick(t)
			if err == nil {
				t.Fatal("a failing swap step must surface as an error")
			}
			if !strings.Contains(err.Error(), tc.wantStep) {
				t.Errorf("the error must name the failing step: %v", err)
			}

			calls, _ := log.snapshot()
			for _, c := range calls {
				if strings.HasSuffix(c, "/comments") {
					t.Errorf("no comment after a failed swap: %v", calls)
				}
			}

			ledger, lerr := usage.LoadLedger(dispatchLedgerPath())
			if lerr != nil {
				t.Fatal(lerr)
			}
			if len(ledger.Runs) != 0 {
				t.Errorf("nothing fired, so nothing is booked: %+v", ledger.Runs)
			}
		})
	}
}

func TestRunDispatch_RetryCountsAgainstCaps_RetriesFirst(t *testing.T) {
	srv, log := newRetryGithub(t, retryFixture{
		issues: "[" + eligibleIssueJSON + "," + retryIssueJSON + "]",
	})
	retryTickEnv(t, srv)
	setDispatchFlags(t, true, false) // --json

	out, err := runRetryTick(t)
	if err != nil {
		t.Fatalf("runDispatch: %v", err)
	}

	calls, _ := log.snapshot()
	for _, c := range calls {
		if strings.Contains(c, "/issues/41") {
			t.Errorf("the retry took the repo's only slot: %v", calls)
		}
	}
	if len(calls) != 5 {
		t.Errorf("the retry itself must run in full: %v", calls)
	}

	var got []decisionJSON
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("invalid JSON %q: %v", out, err)
	}
	if len(got) != 2 {
		t.Fatalf("want both decisions, got %+v", got)
	}
	if got[0].Issue != 43 || got[0].Kind != "retry" || !got[0].Dispatch {
		t.Errorf("retries come first: %+v", got[0])
	}
	if got[1].Issue != 41 || got[1].Kind != "dispatch" || got[1].Dispatch ||
		got[1].Reason != "repo at WIP 1/1" {
		t.Errorf("the new candidate hits the per-repo cap: %+v", got[1])
	}
}

func TestRunDispatch_RetryStep4Fails_BooksAndErrors(t *testing.T) {
	srv, log := newRetryGithub(t, retryFixture{
		failWrite: "DELETE /repos/freaxnx01/bridge/issues/43/labels/ai:retrying",
	})
	retryTickEnv(t, srv)
	setDispatchFlags(t, false, false)

	_, err := runRetryTick(t)
	if err == nil {
		t.Fatal("a failing swap step must surface as an error")
	}
	if !strings.Contains(err.Error(), "step 4") {
		t.Errorf("the error must name the failing step: %v", err)
	}

	calls, _ := log.snapshot()
	for _, c := range calls {
		if strings.HasSuffix(c, "/comments") {
			t.Errorf("no comment after a failed swap: %v", calls)
		}
	}

	// Step 3 already fired the pipeline, so the run is real and has to be
	// booked even though the bracket label is still on the issue.
	ledger, lerr := usage.LoadLedger(dispatchLedgerPath())
	if lerr != nil {
		t.Fatal(lerr)
	}
	if len(ledger.Runs) != 1 || ledger.Runs[0].Issue != 43 {
		t.Errorf("the fired run must be booked: %+v", ledger.Runs)
	}
}

// A retry in a dry-run lane mirrors a dry-run dispatch: no forge write at all,
// but the lane's own counter still advances.
func TestApplyDecisions_RetryInDryRunLane_NoWritesCounterAdvances(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		t.Errorf("a dry-run lane must make no request: %s %s", r.Method, r.URL.Path)
	}))
	defer srv.Close()
	t.Setenv("BRIDGE_GITHUB_API", srv.URL)
	t.Setenv("GH_TOKEN", "tok")
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	cfg := dispatch.DefaultConfig()
	lane := dispatch.Lane{Name: "auto", Autonomous: true, DryRun: true,
		Windows: []dispatch.Span{{From: "00:00", To: "00:00"}}}
	cfg.Lanes = []dispatch.Lane{lane}

	now := time.Now()
	ds := []dispatch.Decision{{Candidate: dispatch.Candidate{
		Owner: "freaxnx01", Repo: "game-a", Issue: forge.Issue{Number: 1}, Lane: lane,
		Retry: true, RetryReason: "rate_limit, retries exhausted"}, Dispatch: true}}

	if err := applyDecisions(context.Background(), ds, dispatch.State{}, tickTiming{Now: now}, cfg); err != nil {
		t.Fatalf("applyDecisions: %v", err)
	}

	state, err := dispatch.ReadState(dispatchStatePath())
	if err != nil {
		t.Fatal(err)
	}
	if got := state.DispatchesInLane("auto", lane.WindowStart(cfg.Schedule, now)); got != 1 {
		t.Errorf("dry-run lane counter: got %d want 1", got)
	}
	ledger, err := usage.LoadLedger(dispatchLedgerPath())
	if err != nil {
		t.Fatal(err)
	}
	if len(ledger.Runs) != 0 {
		t.Errorf("a dry-run lane books nothing: %+v", ledger.Runs)
	}
}

func TestDispatchSubcommands_RetryOnlyIsPersistent(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("BRIDGE_BASE", t.TempDir())
	t.Setenv("BRIDGE_CLAUDE_PROJECTS", filepath.Join(t.TempDir(), "absent"))
	t.Cleanup(func() {
		dispatchJSON, dispatchDryRun, dispatchAuto, dispatchRetryOnly = false, false, false, false
	})

	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetArgs([]string{"dispatch", "now", "--retry-only", "--dry-run"})

	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("bridge dispatch now --retry-only --dry-run: %v", err)
	}
}
