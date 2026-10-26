package cockpit

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/freaxnx01/bridge/internal/agentview"
	"github.com/freaxnx01/bridge/internal/core"
	"github.com/freaxnx01/bridge/internal/dispatch"
	"github.com/freaxnx01/bridge/internal/forge"
)

// maxConcurrentFetches bounds the per-repo fan-out against the forges.
const maxConcurrentFetches = 4

// Source names used as keys of CollectedAt / IntervalsSeconds.
const (
	sourceIssues   = "issues"
	sourceRuns     = "runs"
	sourceSessions = "sessions"
)

// Sources are the data providers the Collector polls. All funcs must be non-nil.
type Sources struct {
	Repos      func() ([]core.Repo, error)
	Wired      func(r core.Repo) bool // .github/workflows/agent.yml or agent-implement.yml exists under r.Path
	Issues     func(ctx context.Context, r core.Repo) ([]forge.Issue, error)
	PRs        func(ctx context.Context, r core.Repo) ([]forge.PullRequest, error)
	Milestones func(ctx context.Context, r core.Repo) ([]forge.Milestone, error)
	Runs       func(ctx context.Context, r core.Repo) ([]forge.WorkflowRun, error)
	Sessions   func(ctx context.Context) ([]agentview.Session, error)
	Paused     func() bool
}

// Intervals are the refresh periods per source. A zero interval disables the ticker.
type Intervals struct{ Issues, Runs, Sessions time.Duration }

// DefaultIntervals returns the production refresh periods.
func DefaultIntervals() Intervals {
	return Intervals{Issues: 10 * time.Minute, Runs: 60 * time.Second, Sessions: 5 * time.Second}
}

// Collector refreshes the board's sources in the background and serves the
// latest snapshot; a failing source keeps its last good data.
type Collector struct {
	src      Sources
	iv       Intervals
	now      func() time.Time
	onChange func()

	mu          sync.Mutex
	repos       []RepoData
	sessions    []agentview.Session
	collectedAt map[string]time.Time
	errs        map[string]SourceError
	board       Board
	boardJSON   []byte
}

// NewCollector builds a Collector. onChange may be nil.
func NewCollector(src Sources, iv Intervals, now func() time.Time, onChange func()) *Collector {
	c := &Collector{
		src: src, iv: iv, now: now, onChange: onChange,
		collectedAt: map[string]time.Time{}, errs: map[string]SourceError{},
	}
	c.mu.Lock()
	c.rebuildLocked()
	c.mu.Unlock()
	return c
}

// Run collects every source once, then on each interval, until ctx is done.
func (c *Collector) Run(ctx context.Context) {
	c.CollectIssues(ctx)
	c.CollectRuns(ctx)
	c.CollectSessions(ctx)

	issues := newTicker(c.iv.Issues)
	runs := newTicker(c.iv.Runs)
	sessions := newTicker(c.iv.Sessions)
	defer issues.stop()
	defer runs.stop()
	defer sessions.stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-issues.C():
			c.CollectIssues(ctx)
		case <-runs.C():
			c.CollectRuns(ctx)
		case <-sessions.C():
			c.CollectSessions(ctx)
		}
	}
}

// optTicker is a ticker that never fires when its interval is not positive.
type optTicker struct{ t *time.Ticker }

func newTicker(d time.Duration) optTicker {
	if d <= 0 {
		return optTicker{}
	}
	return optTicker{t: time.NewTicker(d)}
}

func (o optTicker) C() <-chan time.Time {
	if o.t == nil {
		return nil
	}
	return o.t.C
}

func (o optTicker) stop() {
	if o.t != nil {
		o.t.Stop()
	}
}

// Board returns a copy of the latest board.
func (c *Collector) Board() Board {
	c.mu.Lock()
	defer c.mu.Unlock()
	b := c.board
	b.Running = slices.Clone(b.Running)
	b.Ready = slices.Clone(b.Ready)
	b.Held = slices.Clone(b.Held)
	b.NeedsEnrichment = slices.Clone(b.NeedsEnrichment)
	b.CollectedAt = maps.Clone(b.CollectedAt)
	b.IntervalsSeconds = maps.Clone(b.IntervalsSeconds)
	b.Errors = slices.Clone(b.Errors)
	return b
}

// CollectIssues refreshes issues for every repo, plus PRs and milestones for
// wired GitHub repos that have a Ready/Held candidate.
func (c *Collector) CollectIssues(ctx context.Context) {
	repos, ok := c.listRepos()
	if !ok {
		return
	}
	results := fanOut(repos, func(r core.Repo) repoResult { return c.fetchIssues(ctx, r) })

	c.mu.Lock()
	old := c.repoIndexLocked()
	next := make([]RepoData, 0, len(repos))
	for i, r := range repos {
		rd, prev := results[i].data, old[repoKey(r)]
		src := "issues " + repoKey(r)
		if results[i].err != nil {
			c.setErrLocked(src, results[i].err)
			if prev != nil {
				next = append(next, *prev)
				continue
			}
			rd = baseRepoData(r, false)
		} else {
			delete(c.errs, src)
			if prev != nil {
				rd.Runs = prev.Runs
			}
		}
		next = append(next, rd)
	}
	c.repos = next
	c.collectedAt[sourceIssues] = c.now()
	c.mu.Unlock()
	c.publish()
}

// CollectRuns refreshes workflow runs for repos that have an open
// ai-implement issue in the last issues snapshot.
func (c *Collector) CollectRuns(ctx context.Context) {
	repos, ok := c.listRepos()
	if !ok {
		return
	}
	c.mu.Lock()
	idx := c.repoIndexLocked()
	var todo []core.Repo
	for _, r := range repos {
		if rd := idx[repoKey(r)]; rd != nil && hasAIImplement(rd.Issues) {
			todo = append(todo, r)
		}
	}
	c.mu.Unlock()

	type runsResult struct {
		runs []forge.WorkflowRun
		err  error
	}
	results := fanOut(todo, func(r core.Repo) runsResult {
		runs, err := c.src.Runs(ctx, r)
		return runsResult{runs, err}
	})

	c.mu.Lock()
	idx = c.repoIndexLocked()
	fetched := map[string]bool{}
	for i, r := range todo {
		fetched[repoKey(r)] = true
		src := "runs " + repoKey(r)
		rd := idx[repoKey(r)]
		if results[i].err != nil {
			c.setErrLocked(src, results[i].err)
			continue
		}
		delete(c.errs, src)
		if rd != nil {
			rd.Runs = results[i].runs
		}
	}
	for k, rd := range idx {
		if !fetched[k] {
			rd.Runs = nil
			delete(c.errs, "runs "+k)
		}
	}
	c.collectedAt[sourceRuns] = c.now()
	c.mu.Unlock()
	c.publish()
}

// CollectSessions refreshes the local agent sessions.
func (c *Collector) CollectSessions(ctx context.Context) {
	sessions, err := c.src.Sessions(ctx)
	c.mu.Lock()
	if err != nil {
		c.setErrLocked(sourceSessions, err)
	} else {
		delete(c.errs, sourceSessions)
		c.sessions = sessions
	}
	c.collectedAt[sourceSessions] = c.now()
	c.mu.Unlock()
	c.publish()
}

// CollectRepo refreshes issues, PRs, milestones and runs of one repo (after an
// action). It does not touch the per-source CollectedAt stamps.
func (c *Collector) CollectRepo(ctx context.Context, owner, name string) {
	repos, ok := c.listRepos()
	if !ok {
		return
	}
	i := slices.IndexFunc(repos, func(r core.Repo) bool { return r.Owner == owner && r.Name == name })
	if i < 0 {
		return
	}
	r := repos[i]
	res := c.fetchIssues(ctx, r)
	var runs []forge.WorkflowRun
	var runsErr error
	if res.err == nil {
		runs, runsErr = c.src.Runs(ctx, r)
	}

	c.mu.Lock()
	c.applyRepoLocked(r, res, runs, runsErr)
	c.mu.Unlock()
	c.publish()
}

// applyRepoLocked merges one repo's fresh results into the snapshot.
func (c *Collector) applyRepoLocked(r core.Repo, res repoResult, runs []forge.WorkflowRun, runsErr error) {
	key := repoKey(r)
	idx := c.repoIndexLocked()
	prev := idx[key]
	if res.err != nil {
		c.setErrLocked("issues "+key, res.err)
		if prev == nil {
			c.repos = append(c.repos, baseRepoData(r, false))
		}
		return
	}
	delete(c.errs, "issues "+key)
	rd := res.data
	if runsErr != nil {
		c.setErrLocked("runs "+key, runsErr)
		if prev != nil {
			rd.Runs = prev.Runs
		}
	} else {
		delete(c.errs, "runs "+key)
		rd.Runs = runs
	}
	if prev != nil {
		*prev = rd
		return
	}
	c.repos = append(c.repos, rd)
}

type repoResult struct {
	data RepoData
	err  error
}

// fetchIssues fetches issues (and PRs + milestones when the repo can have a
// Ready/Held candidate). Any failure fails the whole repo fetch.
func (c *Collector) fetchIssues(ctx context.Context, r core.Repo) repoResult {
	rd := baseRepoData(r, c.src.Wired(r))
	issues, err := c.src.Issues(ctx, r)
	if err != nil {
		return repoResult{err: err}
	}
	rd.Issues = issues
	if rd.Forge == "github" && rd.Wired && hasCandidate(issues) {
		if rd.PRs, err = c.src.PRs(ctx, r); err != nil {
			return repoResult{err: fmt.Errorf("prs: %w", err)}
		}
		if rd.Milestones, err = c.src.Milestones(ctx, r); err != nil {
			return repoResult{err: fmt.Errorf("milestones: %w", err)}
		}
	}
	return repoResult{data: rd}
}

func baseRepoData(r core.Repo, wired bool) RepoData {
	return RepoData{Forge: r.Forge, Owner: r.Owner, Name: r.Name, Path: r.Path, Wired: wired}
}

func (c *Collector) listRepos() ([]core.Repo, bool) {
	repos, err := c.src.Repos()
	c.mu.Lock()
	if err != nil {
		c.setErrLocked("repos", err)
	} else {
		delete(c.errs, "repos")
	}
	c.mu.Unlock()
	if err != nil {
		c.publish()
		return nil, false
	}
	return repos, true
}

// fanOut runs fn for every item with at most maxConcurrentFetches in flight,
// returning results in item order.
func fanOut[T, R any](items []T, fn func(T) R) []R {
	out := make([]R, len(items))
	sem := make(chan struct{}, maxConcurrentFetches)
	var wg sync.WaitGroup
	for i, it := range items {
		sem <- struct{}{}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			out[i] = fn(it)
		}()
	}
	wg.Wait()
	return out
}

func repoKey(r core.Repo) string { return r.Owner + "/" + r.Name }

func (c *Collector) repoIndexLocked() map[string]*RepoData {
	idx := make(map[string]*RepoData, len(c.repos))
	for i := range c.repos {
		idx[c.repos[i].Owner+"/"+c.repos[i].Name] = &c.repos[i]
	}
	return idx
}

// setErrLocked records an error, keeping the original Since while it persists.
func (c *Collector) setErrLocked(source string, err error) {
	since := c.now()
	if prev, ok := c.errs[source]; ok {
		since = prev.Since
	}
	c.errs[source] = SourceError{Source: source, Error: err.Error(), Since: since}
}

func hasCandidate(issues []forge.Issue) bool {
	return slices.ContainsFunc(issues, func(i forge.Issue) bool { return isGateCandidate(i.Labels) })
}

func hasAIImplement(issues []forge.Issue) bool {
	return slices.ContainsFunc(issues, func(i forge.Issue) bool { return hasAny(i.Labels, dispatch.LabelAIImplement) })
}

// publish rebuilds the board and fires onChange (outside the lock) when its
// content, ignoring CollectedAt, changed.
func (c *Collector) publish() {
	c.mu.Lock()
	changed := c.rebuildLocked()
	c.mu.Unlock()
	if changed && c.onChange != nil {
		c.onChange()
	}
}

// rebuildLocked recomputes the board and reports whether it differs from the
// previous one apart from CollectedAt.
func (c *Collector) rebuildLocked() bool {
	b := BuildBoard(Input{Repos: slices.Clone(c.repos), Sessions: c.sessions, DispatchPaused: c.src.Paused()})
	b.IntervalsSeconds = map[string]int{
		sourceIssues:   int(c.iv.Issues / time.Second),
		sourceRuns:     int(c.iv.Runs / time.Second),
		sourceSessions: int(c.iv.Sessions / time.Second),
	}
	b.Errors = slices.SortedFunc(maps.Values(c.errs), func(x, y SourceError) int {
		return strings.Compare(x.Source, y.Source)
	})
	if b.Errors == nil {
		b.Errors = []SourceError{}
	}
	raw, err := json.Marshal(b)
	changed := err != nil || !bytes.Equal(raw, c.boardJSON)
	c.boardJSON = raw
	b.CollectedAt = maps.Clone(c.collectedAt)
	c.board = b
	return changed
}
