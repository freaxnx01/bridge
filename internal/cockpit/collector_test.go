package cockpit

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/freaxnx01/bridge/internal/agentview"
	"github.com/freaxnx01/bridge/internal/core"
	"github.com/freaxnx01/bridge/internal/dispatch"
	"github.com/freaxnx01/bridge/internal/forge"
)

// fakeWorld backs Sources with maps and call counters.
type fakeWorld struct {
	mu         sync.Mutex
	repos      []core.Repo
	wired      map[string]bool
	issues     map[string][]forge.Issue
	issuesErr  map[string]error
	runs       map[string][]forge.WorkflowRun
	prCalls    map[string]int
	msCalls    map[string]int
	runCalls   map[string]int
	issueCalls map[string]int
	sessions   []agentview.Session
	sessErr    error
	paused     bool
}

func newWorld(repos ...core.Repo) *fakeWorld {
	return &fakeWorld{
		repos: repos, wired: map[string]bool{}, issues: map[string][]forge.Issue{},
		issuesErr: map[string]error{}, runs: map[string][]forge.WorkflowRun{},
		prCalls: map[string]int{}, msCalls: map[string]int{}, runCalls: map[string]int{}, issueCalls: map[string]int{},
	}
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
			return nil, nil
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
		Sessions: func(context.Context) ([]agentview.Session, error) { return w.sessions, w.sessErr },
		Paused:   func() bool { return w.paused },
	}
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
	c := NewCollector(w.sources(), DefaultIntervals(), clock, func() { calls++ })
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
