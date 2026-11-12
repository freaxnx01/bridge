package cockpit

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/freaxnx01/bridge/internal/agentview"
	"github.com/freaxnx01/bridge/internal/core"
	"github.com/freaxnx01/bridge/internal/dispatch"
	"github.com/freaxnx01/bridge/internal/forge"
)

// fakeWorld backs Sources with maps and call counters. The single-issue maps
// (comments, issueErr, detailIssue, detailCalls) are keyed "owner/name#number".
type fakeWorld struct {
	mu          sync.Mutex
	repos       []core.Repo
	wired       map[string]bool
	issues      map[string][]forge.Issue
	issuesErr   map[string]error
	runs        map[string][]forge.WorkflowRun
	prs         map[string][]forge.PullRequest
	comments    map[string][]forge.Comment
	issueErr    map[string]error
	detailIssue map[string]forge.Issue
	prCalls     map[string]int
	msCalls     map[string]int
	runCalls    map[string]int
	issueCalls  map[string]int
	detailCalls map[string]int
	sessions    []agentview.Session
	sessErr     error
	paused      bool
}

func newWorld(repos ...core.Repo) *fakeWorld {
	return &fakeWorld{
		repos: repos, wired: map[string]bool{}, issues: map[string][]forge.Issue{},
		issuesErr: map[string]error{}, runs: map[string][]forge.WorkflowRun{},
		prs:      map[string][]forge.PullRequest{},
		comments: map[string][]forge.Comment{}, issueErr: map[string]error{},
		detailIssue: map[string]forge.Issue{},
		prCalls:     map[string]int{}, msCalls: map[string]int{}, runCalls: map[string]int{},
		issueCalls: map[string]int{}, detailCalls: map[string]int{},
	}
}

// issueKeyOf is the key of the single-issue maps.
func issueKeyOf(r core.Repo, number int) string { return fmt.Sprintf("%s#%d", key(r), number) }

// set stores a value under lock; the collector reads these from its fan-out
// goroutines, so the test must not write them bare.
func (w *fakeWorld) set(f func()) {
	w.mu.Lock()
	defer w.mu.Unlock()
	f()
}

func (w *fakeWorld) calls(m map[string]int, k string) int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return m[k]
}

// clock is a manually advanced time source the collector may read concurrently.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func newClock(t time.Time) *clock { return &clock{t: t} }

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// events records the SSE event names the collector published.
type events struct {
	mu   sync.Mutex
	seen []string
}

func (e *events) on(name string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.seen = append(e.seen, name)
}

func (e *events) count(name string) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	n := 0
	for _, s := range e.seen {
		if s == name {
			n++
		}
	}
	return n
}

func (e *events) reset() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.seen = nil
}

func key(r core.Repo) string { return r.Owner + "/" + r.Name }

func ghRepo(name string) core.Repo {
	return core.Repo{Forge: "github", Owner: "o", Name: name, Path: "/r/" + name}
}

func (w *fakeWorld) sources() Sources {
	return Sources{
		Repos: func() ([]core.Repo, error) { return w.repos, nil },
		Wired: func(r core.Repo) bool { return w.wired[key(r)] },
		Issues: func(_ context.Context, r core.Repo) ([]forge.Issue, error) {
			w.mu.Lock()
			defer w.mu.Unlock()
			w.issueCalls[key(r)]++
			if err := w.issuesErr[key(r)]; err != nil {
				return nil, err
			}
			return w.issues[key(r)], nil
		},
		PRs: func(_ context.Context, r core.Repo) ([]forge.PullRequest, error) {
			w.mu.Lock()
			defer w.mu.Unlock()
			w.prCalls[key(r)]++
			return w.prs[key(r)], nil
		},
		Milestones: func(_ context.Context, r core.Repo) ([]forge.Milestone, error) {
			w.mu.Lock()
			defer w.mu.Unlock()
			w.msCalls[key(r)]++
			return nil, nil
		},
		Runs: func(_ context.Context, r core.Repo) ([]forge.WorkflowRun, error) {
			w.mu.Lock()
			defer w.mu.Unlock()
			w.runCalls[key(r)]++
			return w.runs[key(r)], nil
		},
		Issue: func(_ context.Context, r core.Repo, number int) (forge.Issue, []forge.Comment, error) {
			w.mu.Lock()
			defer w.mu.Unlock()
			k := issueKeyOf(r, number)
			w.detailCalls[k]++
			if err := w.issueErr[k]; err != nil {
				return forge.Issue{}, nil, err
			}
			return w.issueLocked(r, number), w.comments[k], nil
		},
		Sessions: func(context.Context) ([]agentview.Session, error) { return w.sessions, w.sessErr },
		Paused:   func() bool { return w.paused },
	}
}

// issueLocked is what a live GetIssue reports: the detailIssue override when
// the test set one, otherwise the listed issue.
func (w *fakeWorld) issueLocked(r core.Repo, number int) forge.Issue {
	if i, ok := w.detailIssue[issueKeyOf(r, number)]; ok {
		return i
	}
	for _, i := range w.issues[key(r)] {
		if i.Number == number {
			return i
		}
	}
	return forge.Issue{}
}

func gh(n int, labels ...string) forge.Issue {
	return forge.Issue{Forge: "github", Number: n, Title: "t", URL: "u", Body: readyBody, Labels: labels, Created: t0}
}

func fixedNow() time.Time { return t0 }

func TestCollector_CollectIssues_PRsOnlyForWiredReposWithCandidates(t *testing.T) {
	w := newWorld(ghRepo("cand"), ghRepo("nocand"), ghRepo("unwired"))
	w.wired["o/cand"], w.wired["o/nocand"] = true, true
	w.issues["o/cand"] = []forge.Issue{gh(1)}
	w.issues["o/nocand"] = []forge.Issue{gh(1, dispatch.LabelNeedsEnrichment)}
	w.issues["o/unwired"] = []forge.Issue{gh(1)}
	c := NewCollector(w.sources(), DefaultIntervals(), fixedNow, nil)

	c.CollectIssues(context.Background())

	if w.prCalls["o/cand"] != 1 || w.msCalls["o/cand"] != 1 {
		t.Errorf("cand PRs/milestones = %d/%d, want 1/1", w.prCalls["o/cand"], w.msCalls["o/cand"])
	}
	if w.prCalls["o/nocand"] != 0 || w.prCalls["o/unwired"] != 0 || w.msCalls["o/unwired"] != 0 {
		t.Errorf("unexpected PR/milestone calls: %v %v", w.prCalls, w.msCalls)
	}
	if got := len(c.Board().Ready); got != 1 {
		t.Errorf("Ready = %d, want 1", got)
	}
}

// A repo whose open issues are all dispatched already has no gate candidate,
// yet its failed issue still needs the open PRs: without them the inbox reads a
// PR-guarded retry as a retry card while the dispatcher skips it.
func TestCollector_CollectIssues_InboxCandidateWithoutGateCandidate_FetchesPRs(t *testing.T) {
	w := newWorld(ghRepo("a"))
	w.wired["o/a"] = true
	w.issues["o/a"] = []forge.Issue{gh(1, dispatch.LabelAIImplement, dispatch.LabelAIFailed)}
	w.comments["o/a#1"] = []forge.Comment{
		marker(`{"v":1,"class":"rate_limit","decision":"stop","attempt":2}`, t0.Add(-2*time.Hour)),
	}
	w.prs["o/a"] = []forge.PullRequest{{Number: 7, Body: "Closes #1"}}
	c := NewCollector(w.sources(), DefaultIntervals(), fixedNow, nil)

	c.CollectIssues(context.Background())

	if w.prCalls["o/a"] != 1 || w.msCalls["o/a"] != 0 {
		t.Errorf("PRs/milestones = %d/%d, want 1/0", w.prCalls["o/a"], w.msCalls["o/a"])
	}
	cards := c.Inbox().Cards
	if len(cards) != 1 {
		t.Fatalf("want one card, got %+v", cards)
	}
	if cards[0].Kind != CardHuman || cards[0].Reason != "open PR" {
		t.Errorf("kind/reason = %q/%q, want %q/%q", cards[0].Kind, cards[0].Reason, CardHuman, "open PR")
	}
}

func TestCollector_CollectRuns_OnlyReposWithAIImplementIssue(t *testing.T) {
	w := newWorld(ghRepo("a"), ghRepo("b"))
	w.issues["o/a"] = []forge.Issue{gh(1, dispatch.LabelAIImplement)}
	w.issues["o/b"] = []forge.Issue{gh(1)}
	c := NewCollector(w.sources(), DefaultIntervals(), fixedNow, nil)
	ctx := context.Background()

	c.CollectIssues(ctx)
	c.CollectRuns(ctx)

	if w.runCalls["o/a"] != 1 || w.runCalls["o/b"] != 0 {
		t.Errorf("runCalls = %v, want a=1 b=0", w.runCalls)
	}
}

func TestCollector_RepoFetchFails_KeepsLastGoodData(t *testing.T) {
	w := newWorld(ghRepo("a"))
	w.wired["o/a"] = true
	w.issues["o/a"] = []forge.Issue{gh(1)}
	c := NewCollector(w.sources(), DefaultIntervals(), fixedNow, nil)
	ctx := context.Background()

	c.CollectIssues(ctx)
	w.issuesErr["o/a"] = errors.New("boom")
	c.CollectIssues(ctx)

	b := c.Board()
	if len(b.Ready) != 1 {
		t.Fatalf("Ready after failure = %d, want 1 (last good)", len(b.Ready))
	}
	if len(b.Errors) != 1 || b.Errors[0].Source != "issues o/a" {
		t.Fatalf("Errors = %+v, want one 'issues o/a'", b.Errors)
	}

	delete(w.issuesErr, "o/a")
	c.CollectIssues(ctx)
	if got := c.Board().Errors; len(got) != 0 {
		t.Errorf("Errors after recovery = %+v, want none", got)
	}
}

func TestCollector_CollectSessions_FailureKeepsLastGood(t *testing.T) {
	w := newWorld()
	w.sessions = []agentview.Session{{SessionID: "s1", Name: "n", Status: "busy", StartedAt: t0}}
	c := NewCollector(w.sources(), DefaultIntervals(), fixedNow, nil)
	ctx := context.Background()

	c.CollectSessions(ctx)
	w.sessErr = errors.New("no claude")
	c.CollectSessions(ctx)

	b := c.Board()
	if len(b.Running) != 1 {
		t.Errorf("Running = %d, want 1 (last good)", len(b.Running))
	}
	if len(b.Errors) != 1 || b.Errors[0].Source != "sessions" {
		t.Errorf("Errors = %+v, want one 'sessions'", b.Errors)
	}
}

func TestCollector_OnChange_FiresOnlyOnContentChange(t *testing.T) {
	w := newWorld(ghRepo("a"))
	w.wired["o/a"] = true
	w.issues["o/a"] = []forge.Issue{gh(1)}
	var calls int
	now := t0
	clock := func() time.Time { now = now.Add(time.Minute); return now }
	c := NewCollector(w.sources(), DefaultIntervals(), clock, func(ev string) {
		if ev == EventPipeline {
			calls++
		}
	})
	ctx := context.Background()

	c.CollectIssues(ctx)
	c.CollectIssues(ctx)

	if calls != 1 {
		t.Errorf("onChange calls = %d, want 1", calls)
	}
	w.issues["o/a"] = append(w.issues["o/a"], gh(2))
	c.CollectIssues(ctx)
	if calls != 2 {
		t.Errorf("onChange calls after change = %d, want 2", calls)
	}
}

func TestCollector_Board_CollectedAtAndIntervalsFilled(t *testing.T) {
	w := newWorld(ghRepo("a"))
	iv := Intervals{Issues: 10 * time.Minute, Runs: 60 * time.Second, Sessions: 5 * time.Second}
	c := NewCollector(w.sources(), iv, fixedNow, nil)

	c.CollectIssues(context.Background())
	c.CollectSessions(context.Background())
	b := c.Board()

	if !b.CollectedAt["issues"].Equal(t0) || !b.CollectedAt["sessions"].Equal(t0) {
		t.Errorf("CollectedAt = %v", b.CollectedAt)
	}
	if _, ok := b.CollectedAt["runs"]; ok {
		t.Errorf("runs collected-at set before CollectRuns")
	}
	want := map[string]int{"issues": 600, "runs": 60, "sessions": 5}
	for k, v := range want {
		if b.IntervalsSeconds[k] != v {
			t.Errorf("IntervalsSeconds[%s] = %d, want %d", k, b.IntervalsSeconds[k], v)
		}
	}
}

func TestCollector_FanOut_NeverExceedsFourConcurrent(t *testing.T) {
	var repos []core.Repo
	for _, n := range []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j"} {
		repos = append(repos, ghRepo(n))
	}
	w := newWorld(repos...)
	var inflight, peak atomic.Int32
	src := w.sources()
	src.Issues = func(_ context.Context, _ core.Repo) ([]forge.Issue, error) {
		n := inflight.Add(1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		inflight.Add(-1)
		return nil, nil
	}
	c := NewCollector(src, DefaultIntervals(), fixedNow, nil)

	c.CollectIssues(context.Background())

	if p := peak.Load(); p > 4 || p < 1 {
		t.Errorf("peak concurrency = %d, want 1..4", p)
	}
}

func TestCollector_CollectRepo_RefreshesOneRepo(t *testing.T) {
	w := newWorld(ghRepo("a"), ghRepo("b"))
	w.wired["o/a"] = true
	w.issues["o/a"] = []forge.Issue{gh(1)}
	c := NewCollector(w.sources(), DefaultIntervals(), fixedNow, nil)

	c.CollectRepo(context.Background(), "o", "a")

	if w.issueCalls["o/a"] != 1 || w.issueCalls["o/b"] != 0 || w.runCalls["o/a"] != 1 {
		t.Errorf("calls issues=%v runs=%v", w.issueCalls, w.runCalls)
	}
	if len(c.Board().Ready) != 1 {
		t.Errorf("Ready = %d, want 1", len(c.Board().Ready))
	}
}

func TestCollector_Run_StopsOnContextCancel(t *testing.T) {
	w := newWorld(ghRepo("a"))
	c := NewCollector(w.sources(), Intervals{Issues: time.Hour, Runs: time.Hour, Sessions: time.Hour}, fixedNow, nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { c.Run(ctx); close(done) }()

	cancel()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
	if w.issueCalls["o/a"] != 1 {
		t.Errorf("initial collect issue calls = %d, want 1", w.issueCalls["o/a"])
	}
}

func TestCollector_Run_SlowSessionsDoNotHoldUpIssues(t *testing.T) {
	w := newWorld(ghRepo("a"))
	release := make(chan struct{})
	src := w.sources()
	src.Sessions = func(context.Context) ([]agentview.Session, error) { <-release; return nil, nil }
	c := NewCollector(src, Intervals{Issues: time.Hour, Runs: time.Hour, Sessions: time.Hour}, fixedNow, nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { c.Run(ctx); close(done) }()
	defer func() { close(release); cancel(); <-done }()

	deadline := time.Now().Add(5 * time.Second)
	for {
		b := c.Board()
		_, hasIssues := b.CollectedAt["issues"]
		_, hasRuns := b.CollectedAt["runs"]
		if hasIssues && hasRuns {
			if _, hasSessions := b.CollectedAt["sessions"]; hasSessions {
				t.Fatal("sessions must still be blocked")
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("issues/runs held up by slow sessions: %v", b.CollectedAt)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestCollector_ConcurrentCollectIssues_Coalesce(t *testing.T) {
	w := newWorld(ghRepo("a"))
	entered := make(chan struct{}, 8)
	release := make(chan struct{})
	var calls atomic.Int32
	src := w.sources()
	src.Issues = func(_ context.Context, _ core.Repo) ([]forge.Issue, error) {
		calls.Add(1)
		entered <- struct{}{}
		<-release
		return nil, nil
	}
	c := NewCollector(src, DefaultIntervals(), fixedNow, nil)

	done := make(chan struct{})
	go func() {
		c.CollectIssues(context.Background())
		close(done)
	}()
	<-entered
	c.CollectIssues(context.Background()) // returns at once: one is running
	if n := calls.Load(); n != 1 {
		t.Fatalf("fetches while one is running = %d, want 1", n)
	}
	close(release)
	<-done

	c.CollectIssues(context.Background()) // the guard is released afterwards
	if n := calls.Load(); n != 2 {
		t.Errorf("fetches after release = %d, want 2", n)
	}
}

func TestCollector_ConcurrentCollectRuns_Coalesce(t *testing.T) {
	w := newWorld(ghRepo("a"))
	w.issues["o/a"] = []forge.Issue{gh(1, dispatch.LabelAIImplement)}
	entered := make(chan struct{}, 8)
	release := make(chan struct{})
	var calls atomic.Int32
	src := w.sources()
	src.Runs = func(_ context.Context, _ core.Repo) ([]forge.WorkflowRun, error) {
		calls.Add(1)
		entered <- struct{}{}
		<-release
		return nil, nil
	}
	c := NewCollector(src, DefaultIntervals(), fixedNow, nil)
	c.CollectIssues(context.Background())

	done := make(chan struct{})
	go func() {
		c.CollectRuns(context.Background())
		close(done)
	}()
	<-entered
	c.CollectRuns(context.Background()) // returns at once: one is running
	if n := calls.Load(); n != 1 {
		t.Fatalf("fetches while one is running = %d, want 1", n)
	}
	close(release)
	<-done
}

// botMarker is a run report comment by the only trusted author.
func botMarker(body string, at time.Time) forge.Comment {
	return forge.Comment{ID: 1, Author: "github-actions[bot]", Created: at,
		Body: "## ai-implement run\n\n<!-- ai-run: " + body + " -->"}
}

func TestCollector_CollectIssues_CommentsOnlyForWiredGithubCandidates(t *testing.T) {
	fj := core.Repo{Forge: "forgejo", Owner: "o", Name: "f", Path: "/r/f"}
	w := newWorld(ghRepo("a"), ghRepo("unwired"), fj)
	w.wired["o/a"], w.wired["o/f"] = true, true
	w.issues["o/a"] = []forge.Issue{gh(1, "feat"), gh(2, dispatch.LabelAIFailed)}
	w.issues["o/unwired"] = []forge.Issue{gh(3, dispatch.LabelAIFailed)}
	w.issues["o/f"] = []forge.Issue{gh(4, dispatch.LabelAIFailed)}
	c := NewCollector(w.sources(), DefaultIntervals(), fixedNow, nil)

	c.CollectIssues(context.Background())

	w.mu.Lock()
	defer w.mu.Unlock()
	want := map[string]int{"o/a#2": 1}
	if len(w.detailCalls) != len(want) || w.detailCalls["o/a#2"] != 1 {
		t.Errorf("detailCalls = %v, want %v", w.detailCalls, want)
	}
}

func TestCollector_CollectIssues_UnchangedUpdated_SkipsCommentFetch(t *testing.T) {
	w := newWorld(ghRepo("a"))
	w.wired["o/a"] = true
	w.issues["o/a"] = []forge.Issue{gh(2, dispatch.LabelAIFailed)}
	c := NewCollector(w.sources(), DefaultIntervals(), fixedNow, nil)
	ctx := context.Background()

	c.CollectIssues(ctx)
	c.CollectIssues(ctx)
	if got := w.calls(w.detailCalls, "o/a#2"); got != 1 {
		t.Fatalf("detail calls = %d, want 1 (cached on unchanged Updated)", got)
	}

	w.set(func() { w.issues["o/a"] = []forge.Issue{bumped(gh(2, dispatch.LabelAIFailed), time.Hour)} })
	c.CollectIssues(ctx)
	if got := w.calls(w.detailCalls, "o/a#2"); got != 2 {
		t.Errorf("detail calls after Updated bump = %d, want 2", got)
	}
}

// bumped returns i with Updated moved forward, the way a new comment moves it.
func bumped(i forge.Issue, d time.Duration) forge.Issue {
	i.Updated = i.Updated.Add(d)
	return i
}

func TestCollector_CommentFetchFails_KeepsStaleAndRetries(t *testing.T) {
	w := newWorld(ghRepo("a"))
	w.wired["o/a"] = true
	w.issues["o/a"] = []forge.Issue{gh(2, dispatch.LabelAIFailed)}
	w.comments["o/a#2"] = []forge.Comment{
		botMarker(`{"v":1,"class":"task_failure","decision":"stop","attempt":2}`, t0),
	}
	c := NewCollector(w.sources(), DefaultIntervals(), fixedNow, nil)
	ctx := context.Background()

	c.CollectIssues(ctx)
	if got := c.Inbox().Cards; len(got) != 1 || got[0].Reason != "task_failure" {
		t.Fatalf("cards after first fetch = %+v, want one task_failure card", got)
	}

	w.set(func() {
		w.issues["o/a"] = []forge.Issue{bumped(gh(2, dispatch.LabelAIFailed), time.Hour)}
		w.issueErr["o/a#2"] = errors.New("boom")
	})
	c.CollectIssues(ctx)
	ib := c.Inbox()
	if len(ib.Cards) != 1 {
		t.Fatalf("cards after failed fetch = %+v, want the stale card kept", ib.Cards)
	}
	if ib.Errors["comments o/a#2"] == "" {
		t.Errorf("Errors = %v, want a comments o/a#2 entry", ib.Errors)
	}

	w.set(func() { delete(w.issueErr, "o/a#2") })
	c.CollectIssues(ctx)
	if got := w.calls(w.detailCalls, "o/a#2"); got != 3 {
		t.Errorf("detail calls = %d, want 3 (retried despite unchanged Updated)", got)
	}
	if got := c.Inbox().Errors; len(got) != 0 {
		t.Errorf("Errors after recovery = %v, want none", got)
	}
}

func TestCollector_CommentFetchFails_NoCache_IssueNotShown(t *testing.T) {
	w := newWorld(ghRepo("a"))
	w.wired["o/a"] = true
	w.issues["o/a"] = []forge.Issue{gh(2, dispatch.LabelAIFailed)}
	w.issueErr["o/a#2"] = errors.New("boom")
	c := NewCollector(w.sources(), DefaultIntervals(), fixedNow, nil)

	c.CollectIssues(context.Background())

	ib := c.Inbox()
	if len(ib.Cards) != 0 || len(ib.Waiting) != 0 {
		t.Errorf("cards=%+v waiting=%+v, want both empty", ib.Cards, ib.Waiting)
	}
	if ib.Errors["comments o/a#2"] == "" {
		t.Errorf("Errors = %v, want a comments o/a#2 entry", ib.Errors)
	}
}

func TestCollector_Inbox_EventFiresOnlyOnInboxChange(t *testing.T) {
	w := newWorld(ghRepo("a"))
	w.wired["o/a"] = true
	w.issues["o/a"] = []forge.Issue{gh(1)}
	var ev events
	c := NewCollector(w.sources(), DefaultIntervals(), fixedNow, ev.on)
	ctx := context.Background()

	c.CollectIssues(ctx)
	ev.reset()

	w.set(func() { w.issues["o/a"] = []forge.Issue{gh(1, dispatch.LabelAIFailed)} })
	c.CollectIssues(ctx)
	if got := ev.count(EventInbox); got != 1 {
		t.Fatalf("%s events = %d, want 1", EventInbox, got)
	}

	ev.reset()
	c.CollectIssues(ctx)
	if got := ev.count(EventInbox); got != 0 {
		t.Errorf("%s events on an unchanged inbox = %d, want 0", EventInbox, got)
	}
}

func TestCollector_Inbox_StaleRetryCrossesThreshold_FiresInboxEvent(t *testing.T) {
	w := newWorld(ghRepo("a"))
	w.wired["o/a"] = true
	w.issues["o/a"] = []forge.Issue{gh(2, dispatch.LabelAIFailed)}
	w.comments["o/a#2"] = []forge.Comment{
		botMarker(`{"v":1,"class":"transient","decision":"retry","attempt":1}`, t0),
	}
	clk := newClock(t0.Add(3 * time.Hour))
	var ev events
	c := NewCollector(w.sources(), DefaultIntervals(), clk.now, ev.on)
	ctx := context.Background()

	c.CollectIssues(ctx)
	if got := c.Inbox().Waiting; len(got) != 1 || got[0].Reason != "pipeline retrying" {
		t.Fatalf("waiting at the threshold = %+v, want one 'pipeline retrying'", got)
	}

	clk.advance(time.Second)
	ev.reset()
	c.CollectSessions(ctx)

	if got := ev.count(EventInbox); got != 1 {
		t.Errorf("%s events = %d, want 1", EventInbox, got)
	}
	cards := c.Inbox().Cards
	if len(cards) != 1 || cards[0].Reason != "retry never reported" {
		t.Errorf("cards = %+v, want one 'retry never reported'", cards)
	}
}

func TestCollector_CollectRuns_InFlightRunFinished_RefreshesRepo(t *testing.T) {
	w := newWorld(ghRepo("a"))
	w.wired["o/a"] = true
	w.issues["o/a"] = []forge.Issue{gh(1, dispatch.LabelAIImplement)}
	w.runs["o/a"] = []forge.WorkflowRun{{Event: "issues", Status: "in_progress", DisplayTitle: "t", URL: "u1"}}
	c := NewCollector(w.sources(), DefaultIntervals(), fixedNow, nil)
	ctx := context.Background()

	c.CollectIssues(ctx)
	c.CollectRuns(ctx)
	if got := w.calls(w.issueCalls, "o/a"); got != 1 {
		t.Fatalf("issue calls while the run is going = %d, want 1", got)
	}

	w.set(func() {
		w.runs["o/a"] = []forge.WorkflowRun{{Event: "issues", Status: "completed", DisplayTitle: "t", URL: "u1"}}
	})
	c.CollectRuns(ctx)

	if got := w.calls(w.issueCalls, "o/a"); got != 2 {
		t.Errorf("issue calls after the run finished = %d, want 2 (repo refreshed)", got)
	}
}

func TestCollector_CollectRuns_UnrelatedRunFinished_NoRefresh(t *testing.T) {
	w := newWorld(ghRepo("a"))
	w.wired["o/a"] = true
	w.issues["o/a"] = []forge.Issue{gh(1, dispatch.LabelAIImplement)}
	w.runs["o/a"] = []forge.WorkflowRun{{Event: "issues", Status: "in_progress", DisplayTitle: "other", URL: "u1"}}
	c := NewCollector(w.sources(), DefaultIntervals(), fixedNow, nil)
	ctx := context.Background()

	c.CollectIssues(ctx)
	c.CollectRuns(ctx)
	w.set(func() {
		w.runs["o/a"] = []forge.WorkflowRun{{Event: "issues", Status: "completed", DisplayTitle: "other", URL: "u1"}}
	})
	c.CollectRuns(ctx)

	if got := w.calls(w.issueCalls, "o/a"); got != 1 {
		t.Errorf("issue calls = %d, want 1 (the run was never this issue's)", got)
	}
}

func TestCollector_IssueDetail_Table(t *testing.T) {
	fj := core.Repo{Forge: "forgejo", Owner: "o", Name: "f", Path: "/r/f"}
	newCollector := func(t *testing.T) (*Collector, *fakeWorld, *clock) {
		t.Helper()
		w := newWorld(ghRepo("a"), ghRepo("unwired"), fj)
		w.wired["o/a"], w.wired["o/f"] = true, true
		w.issues["o/a"] = []forge.Issue{gh(2, dispatch.LabelAIFailed), gh(3)}
		w.detailIssue["o/a#2"] = forge.Issue{Number: 2, Title: "live title", URL: "u", Labels: []string{"ai:failed"}}
		w.comments["o/a#2"] = []forge.Comment{
			botMarker(`{"v":1,"class":"rate_limit","decision":"retry"}`, t0),
			botMarker(`{"v":1,"class":"task_failure","decision":"stop"}`, t0.Add(time.Hour)),
		}
		clk := newClock(t0)
		c := NewCollector(w.sources(), DefaultIntervals(), clk.now, nil)
		c.CollectIssues(context.Background())
		return c, w, clk
	}

	t.Run("unknown repo", func(t *testing.T) {
		c, _, _ := newCollector(t)
		if _, err := c.IssueDetail(context.Background(), "o", "nope", 1); !errors.Is(err, ErrUnknownRepo) {
			t.Errorf("err = %v, want ErrUnknownRepo", err)
		}
	})
	t.Run("non-github forge", func(t *testing.T) {
		c, _, _ := newCollector(t)
		if _, err := c.IssueDetail(context.Background(), "o", "f", 1); !errors.Is(err, ErrUnknownRepo) {
			t.Errorf("err = %v, want ErrUnknownRepo", err)
		}
	})
	t.Run("unwired github repo", func(t *testing.T) {
		c, _, _ := newCollector(t)
		if _, err := c.IssueDetail(context.Background(), "o", "unwired", 1); !errors.Is(err, ErrUnknownRepo) {
			t.Errorf("err = %v, want ErrUnknownRepo", err)
		}
	})
	t.Run("issue that is not an inbox candidate", func(t *testing.T) {
		c, w, _ := newCollector(t)
		if _, err := c.IssueDetail(context.Background(), "o", "a", 3); !errors.Is(err, ErrNotCandidate) {
			t.Errorf("err = %v, want ErrNotCandidate", err)
		}
		if got := w.calls(w.detailCalls, "o/a#3"); got != 0 {
			t.Errorf("detail calls for a non-candidate = %d, want 0 (no forge read)", got)
		}
	})
	t.Run("issue the snapshot does not list", func(t *testing.T) {
		c, _, _ := newCollector(t)
		if _, err := c.IssueDetail(context.Background(), "o", "a", 99); !errors.Is(err, ErrNotCandidate) {
			t.Errorf("err = %v, want ErrNotCandidate", err)
		}
	})
	t.Run("cached thread within the TTL", func(t *testing.T) {
		c, w, clk := newCollector(t)
		before := w.calls(w.detailCalls, "o/a#2")
		clk.advance(detailTTL - time.Second)

		d, err := c.IssueDetail(context.Background(), "o", "a", 2)
		if err != nil {
			t.Fatal(err)
		}
		if got := w.calls(w.detailCalls, "o/a#2"); got != before {
			t.Errorf("detail calls = %d, want %d (served from the cache)", got, before)
		}
		if len(d.Runs) != 2 || d.Runs[0].Class != "task_failure" {
			t.Errorf("Runs = %+v, want 2, newest first", d.Runs)
		}
	})
	t.Run("live read once the TTL passed", func(t *testing.T) {
		c, w, clk := newCollector(t)
		before := w.calls(w.detailCalls, "o/a#2")
		clk.advance(detailTTL)

		d, err := c.IssueDetail(context.Background(), "o", "a", 2)
		if err != nil {
			t.Fatal(err)
		}
		if got := w.calls(w.detailCalls, "o/a#2"); got != before+1 {
			t.Errorf("detail calls = %d, want %d (one live read)", got, before+1)
		}
		if d.Issue.Title != "live title" || d.Issue.Repo != "o/a" {
			t.Errorf("Issue = %+v, want the live title", d.Issue)
		}
		if len(d.Runs) != 2 || d.Runs[0].Class != "task_failure" {
			t.Errorf("Runs = %+v, want 2, newest first", d.Runs)
		}
	})
	t.Run("source error", func(t *testing.T) {
		c, w, clk := newCollector(t)
		boom := errors.New("boom")
		w.set(func() { w.issueErr["o/a#2"] = boom })
		clk.advance(detailTTL)

		_, err := c.IssueDetail(context.Background(), "o", "a", 2)
		if !errors.Is(err, boom) {
			t.Fatalf("err = %v, want it to wrap the source error", err)
		}
		if errors.Is(err, ErrUnknownRepo) || errors.Is(err, ErrNotCandidate) {
			t.Errorf("err = %v, want it not to read as a not-found error", err)
		}
	})
}

func adoRepo(name string) core.Repo {
	return core.Repo{Forge: "ado", Owner: "bossDMS", Name: name, Path: "/r/" + name}
}

func TestCollector_UnsupportedForge_SkippedNotError(t *testing.T) {
	w := newWorld(ghRepo("a"), adoRepo("x"), adoRepo("y"))
	w.issuesErr["bossDMS/x"] = fmt.Errorf("%w: ado", ErrForgeUnsupported)
	w.issuesErr["bossDMS/y"] = fmt.Errorf("%w: ado", ErrForgeUnsupported)
	w.issuesErr["o/a"] = errors.New("boom")
	c := NewCollector(w.sources(), DefaultIntervals(), fixedNow, nil)

	c.CollectIssues(context.Background())

	b := c.Board()
	if len(b.Errors) != 1 || b.Errors[0].Source != "issues o/a" {
		t.Fatalf("Errors = %+v, want only 'issues o/a'", b.Errors)
	}
	want := []SkippedForge{{Forge: "ado", Repos: 2}}
	if !slices.Equal(b.Skipped, want) {
		t.Fatalf("Skipped = %+v, want %+v", b.Skipped, want)
	}
}

func TestCollector_RepoTurnsUnsupported_ClearsOldError(t *testing.T) {
	w := newWorld(adoRepo("x"))
	w.issuesErr["bossDMS/x"] = errors.New("boom")
	c := NewCollector(w.sources(), DefaultIntervals(), fixedNow, nil)
	ctx := context.Background()
	c.CollectIssues(ctx)

	w.set(func() { w.issuesErr["bossDMS/x"] = fmt.Errorf("%w: ado", ErrForgeUnsupported) })
	c.CollectIssues(ctx)

	if got := c.Board().Errors; len(got) != 0 {
		t.Fatalf("Errors = %+v, want none", got)
	}
}

func TestBoard_NoUnsupported_SkippedIsEmptyArray(t *testing.T) {
	c := NewCollector(newWorld(ghRepo("a")).sources(), DefaultIntervals(), fixedNow, nil)
	c.CollectIssues(context.Background())
	raw, err := json.Marshal(c.Board())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(raw, []byte(`"skipped":[]`)) {
		t.Fatalf("board JSON = %s, want \"skipped\":[]", raw)
	}
}
