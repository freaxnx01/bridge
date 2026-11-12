package cockpit

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
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

// SSE event names the collector publishes, one per payload it serves.
const (
	EventPipeline = "pipeline-updated"
	EventInbox    = "inbox-updated"
)

// ErrUnknownRepo means the requested repo is not a wired GitHub repo in the
// collector's last snapshot.
var ErrUnknownRepo = errors.New("unknown repo")

// ErrNotCandidate means the requested issue is not an inbox candidate of that
// repo in the collector's last snapshot.
var ErrNotCandidate = errors.New("not an inbox candidate")

// ErrForgeUnsupported marks a repo on a forge the board has no client for.
// The collector lists such repos under Board.Skipped instead of Errors.
var ErrForgeUnsupported = errors.New("forge not supported by the board")

// detailTTL bounds how often an issue detail costs a live forge read: a thread
// read this recently is served from the cache instead. The runs tick already
// re-reads a repo within about a minute of a run ending, so the panel stays
// current, and a page looping the open endpoint cannot burn the REST quota.
const detailTTL = time.Minute

// Sources are the data providers the Collector polls. All funcs must be non-nil.
type Sources struct {
	Repos      func() ([]core.Repo, error)
	Wired      func(r core.Repo) bool // .github/workflows/agent.yml or agent-implement.yml exists under r.Path
	Issues     func(ctx context.Context, r core.Repo) ([]forge.Issue, error)
	PRs        func(ctx context.Context, r core.Repo) ([]forge.PullRequest, error)
	Milestones func(ctx context.Context, r core.Repo) ([]forge.Milestone, error)
	Runs       func(ctx context.Context, r core.Repo) ([]forge.WorkflowRun, error)
	// Issue reads one issue plus its full comment thread — the inbox's run
	// markers, and the live issue detail.
	Issue    func(ctx context.Context, r core.Repo, number int) (forge.Issue, []forge.Comment, error)
	Sessions func(ctx context.Context) ([]agentview.Session, error)
	Paused   func() bool
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
	onChange func(event string)

	// issuesBusy / runsBusy coalesce overlapping CollectIssues / CollectRuns
	// calls (ticker vs. refresh): a second call while one runs returns at once.
	issuesBusy atomic.Bool
	runsBusy   atomic.Bool

	// cacheMu guards the comment cache alone: it is filled from the per-repo
	// fan-out goroutines, which run without mu.
	cacheMu  sync.Mutex
	comments map[issueKey]cachedComments

	mu          sync.Mutex
	repos       []RepoData
	sessions    []agentview.Session
	collectedAt map[string]time.Time
	errs        map[string]SourceError
	unsupported map[string]string // repo key → forge the board has no client for
	board       Board
	boardJSON   []byte
	inbox       Inbox
	inboxJSON   []byte
}

// issueKey identifies one issue across repos.
type issueKey struct {
	repo   string // "owner/name"
	number int
}

// cachedComments is one issue's thread plus the Updated it was read at: a new
// comment moves Updated, so an unchanged Updated means an unchanged thread.
// readAt is when the read happened, which is what detailTTL is measured against.
type cachedComments struct {
	updated  time.Time
	readAt   time.Time
	comments []forge.Comment
}

// NewCollector builds a Collector. onChange may be nil; it is called with the
// name of the payload that changed (EventPipeline, EventInbox).
func NewCollector(src Sources, iv Intervals, now func() time.Time, onChange func(event string)) *Collector {
	c := &Collector{
		src: src, iv: iv, now: now, onChange: onChange,
		comments:    map[issueKey]cachedComments{},
		collectedAt: map[string]time.Time{}, errs: map[string]SourceError{},
		unsupported: map[string]string{},
	}
	c.mu.Lock()
	c.rebuildLocked()
	c.mu.Unlock()
	return c
}

// Run collects every source once, then on each interval, until ctx is done.
func (c *Collector) Run(ctx context.Context) {
	// Runs depends on the issues snapshot, so those two stay ordered; the slow
	// sessions listing runs alongside them instead of holding them up.
	var initial sync.WaitGroup
	initial.Add(2)
	go func() {
		defer initial.Done()
		c.CollectIssues(ctx)
		c.CollectRuns(ctx)
	}()
	go func() {
		defer initial.Done()
		c.CollectSessions(ctx)
	}()
	initial.Wait()

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
	b.Skipped = slices.Clone(b.Skipped)
	return b
}

// Inbox returns a copy of the latest inbox.
func (c *Collector) Inbox() Inbox {
	c.mu.Lock()
	defer c.mu.Unlock()
	ib := c.inbox
	ib.Cards = slices.Clone(ib.Cards)
	ib.Waiting = slices.Clone(ib.Waiting)
	ib.Parked = slices.Clone(ib.Parked)
	ib.Errors = maps.Clone(ib.Errors)
	return ib
}

// IssueDetail reads one issue and its run history, live when the cached thread
// is older than detailTTL, so an open detail reflects a report that landed
// since the last tick. The issue must be an inbox candidate of a wired GitHub
// repo in the last snapshot: the detail is the inbox's own detail, and the
// endpoint serving it reads nothing the inbox does not already list.
func (c *Collector) IssueDetail(ctx context.Context, owner, name string, number int) (IssueDetail, error) {
	r, issue, err := c.candidateIssue(owner, name, number)
	if err != nil {
		return IssueDetail{}, err
	}
	repo := repoKey(r)
	k := issueKey{repo, number}
	if comments, ok := c.freshComments(k, c.now()); ok {
		return BuildIssueDetail(repo, issue, comments), nil
	}
	live, comments, err := c.src.Issue(ctx, r, number)
	if err != nil {
		return IssueDetail{}, fmt.Errorf("issue %s#%d: %w", repo, number, err)
	}
	c.storeComments(k, live.Updated, comments)
	return BuildIssueDetail(repo, live, comments), nil
}

// candidateIssue returns the repo plus its snapshot issue when owner/name is a
// wired GitHub repo holding number as an inbox candidate, else ErrUnknownRepo
// or ErrNotCandidate.
func (c *Collector) candidateIssue(owner, name string, number int) (core.Repo, forge.Issue, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, rd := range c.repos {
		if rd.Owner != owner || rd.Name != name || rd.Forge != "github" || !rd.Wired {
			continue
		}
		r := core.Repo{Forge: rd.Forge, Owner: rd.Owner, Name: rd.Name, Path: rd.Path}
		for _, i := range rd.Issues {
			if i.Number == number && isInboxCandidate(i.Labels) {
				return r, i, nil
			}
		}
		return core.Repo{}, forge.Issue{}, fmt.Errorf("%w: %s/%s#%d", ErrNotCandidate, owner, name, number)
	}
	return core.Repo{}, forge.Issue{}, fmt.Errorf("%w: %s/%s", ErrUnknownRepo, owner, name)
}

// CollectIssues refreshes issues for every repo, plus PRs and milestones for
// wired GitHub repos that have a Ready/Held candidate.
func (c *Collector) CollectIssues(ctx context.Context) {
	if !c.issuesBusy.CompareAndSwap(false, true) {
		return
	}
	defer c.issuesBusy.Store(false)
	repos, ok := c.listRepos()
	if !ok {
		return
	}
	results := fanOut(repos, func(r core.Repo) repoResult { return c.fetchIssues(ctx, r) })

	c.mu.Lock()
	old := c.repoIndexLocked()
	next := make([]RepoData, 0, len(repos))
	// Rebuilt from this pass alone, so a repo that disappeared or whose forge
	// became supported leaves no stale entry behind.
	unsupported := make(map[string]string, len(c.unsupported))
	for i, r := range repos {
		rd, prev := results[i].data, old[repoKey(r)]
		src := "issues " + repoKey(r)
		switch {
		case errors.Is(results[i].err, ErrForgeUnsupported):
			unsupported[repoKey(r)] = r.Forge
			delete(c.errs, src)
			rd = baseRepoData(r, false)
		case results[i].err != nil:
			c.setErrLocked(src, results[i].err)
			if prev != nil {
				next = append(next, *prev)
				continue
			}
			rd = baseRepoData(r, false)
		default:
			delete(c.errs, src)
			if prev != nil {
				rd.Runs = prev.Runs
			}
		}
		next = append(next, rd)
	}
	c.repos = next
	c.unsupported = unsupported
	c.collectedAt[sourceIssues] = c.now()
	c.mu.Unlock()
	c.pruneComments(next)
	c.publish()
}

// CollectRuns refreshes workflow runs for repos that have an open
// ai-implement issue in the last issues snapshot.
func (c *Collector) CollectRuns(ctx context.Context) {
	if !c.runsBusy.CompareAndSwap(false, true) {
		return
	}
	defer c.runsBusy.Store(false)
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

	results := fanOut(todo, func(r core.Repo) runsResult {
		runs, err := c.src.Runs(ctx, r)
		return runsResult{runs, err}
	})

	c.mu.Lock()
	finished := c.applyRunsLocked(todo, results)
	c.collectedAt[sourceRuns] = c.now()
	c.mu.Unlock()
	c.publish()
	// A run that just ended has posted its report: read it now rather than at
	// the next 10-minute issues tick.
	for _, r := range finished {
		c.CollectRepo(ctx, r.Owner, r.Name)
	}
}

type runsResult struct {
	runs []forge.WorkflowRun
	err  error
}

// applyRunsLocked merges the fetched runs into the snapshot and returns the
// repos whose in-flight run for an inbox candidate has just ended.
func (c *Collector) applyRunsLocked(todo []core.Repo, results []runsResult) []core.Repo {
	idx := c.repoIndexLocked()
	before := make(map[string]map[string]bool, len(todo))
	for _, r := range todo {
		before[repoKey(r)] = inFlightRunURLs(idx[repoKey(r)])
	}
	fetched := map[string]bool{}
	for i, r := range todo {
		k := repoKey(r)
		fetched[k] = true
		if results[i].err != nil {
			c.setErrLocked("runs "+k, results[i].err)
			continue
		}
		delete(c.errs, "runs "+k)
		if rd := idx[k]; rd != nil {
			rd.Runs = results[i].runs
		}
	}
	for k, rd := range idx {
		if !fetched[k] {
			rd.Runs = nil
			delete(c.errs, "runs "+k)
		}
	}
	return finishedRepos(todo, before, idx)
}

// finishedRepos returns the repos holding a run URL that was in flight for an
// inbox candidate before the swap and is not afterwards.
func finishedRepos(todo []core.Repo, before map[string]map[string]bool, idx map[string]*RepoData) []core.Repo {
	var out []core.Repo
	for _, r := range todo {
		still := inFlightRunURLs(idx[repoKey(r)])
		for url := range before[repoKey(r)] {
			if !still[url] {
				out = append(out, r)
				break
			}
		}
	}
	return out
}

// inFlightRunURLs returns the URLs of the repo's runs that are in flight for
// one of its inbox candidates.
func inFlightRunURLs(rd *RepoData) map[string]bool {
	out := map[string]bool{}
	if rd == nil {
		return out
	}
	for _, i := range rd.Issues {
		if !isInboxCandidate(i.Labels) {
			continue
		}
		for _, r := range rd.Runs {
			if isIssueRunFor(i, r) {
				out[r.URL] = true
			}
		}
	}
	return out
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
	if errors.Is(res.err, ErrForgeUnsupported) {
		c.markUnsupportedLocked(r, prev)
		return
	}
	delete(c.unsupported, key)
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

// markUnsupportedLocked records r as skipped (no client for its forge) and
// drops any earlier source error for it.
func (c *Collector) markUnsupportedLocked(r core.Repo, prev *RepoData) {
	key := repoKey(r)
	delete(c.errs, "issues "+key)
	c.unsupported[key] = r.Forge
	if prev == nil {
		c.repos = append(c.repos, baseRepoData(r, false))
	}
}

// skippedForges groups unsupported repos per forge, sorted by forge.
func skippedForges(unsupported map[string]string) []SkippedForge {
	counts := map[string]int{}
	for _, f := range unsupported {
		counts[f]++
	}
	out := []SkippedForge{}
	for _, f := range slices.Sorted(maps.Keys(counts)) {
		out = append(out, SkippedForge{Forge: f, Repos: counts[f]})
	}
	return out
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
	if rd.Forge != "github" || !rd.Wired {
		return repoResult{data: rd}
	}
	if hasCandidate(issues) {
		if rd.PRs, err = c.src.PRs(ctx, r); err != nil {
			return repoResult{err: fmt.Errorf("prs: %w", err)}
		}
		if rd.Milestones, err = c.src.Milestones(ctx, r); err != nil {
			return repoResult{err: fmt.Errorf("milestones: %w", err)}
		}
	}
	rd.Comments, rd.CommentErrs = c.fetchComments(ctx, r, issues)
	return repoResult{data: rd}
}

// fetchComments reads the comment thread of every inbox candidate, skipping an
// issue whose Updated has not moved since the cached read. A failure is per
// issue and never fails the repo: the stale thread stays and the error shows on
// the card, so a card cannot silently disappear.
func (c *Collector) fetchComments(ctx context.Context, r core.Repo, issues []forge.Issue) (map[int][]forge.Comment, map[int]string) {
	out := map[int][]forge.Comment{}
	var errs map[int]string
	for _, i := range issues {
		if !isInboxCandidate(i.Labels) {
			continue
		}
		k := issueKey{repoKey(r), i.Number}
		if cached, ok := c.cachedComments(k, i.Updated); ok {
			out[i.Number] = cached
			continue
		}
		_, comments, err := c.src.Issue(ctx, r, i.Number)
		if err != nil {
			errs = recordCommentErr(errs, i.Number, err)
			if stale, ok := c.staleComments(k); ok {
				out[i.Number] = stale
			}
			continue
		}
		c.storeComments(k, i.Updated, comments)
		out[i.Number] = comments
	}
	return out, errs
}

func recordCommentErr(errs map[int]string, number int, err error) map[int]string {
	if errs == nil {
		errs = map[int]string{}
	}
	errs[number] = err.Error()
	return errs
}

// cachedComments returns the cached thread when it was read at this Updated.
func (c *Collector) cachedComments(k issueKey, updated time.Time) ([]forge.Comment, bool) {
	c.cacheMu.Lock()
	defer c.cacheMu.Unlock()
	e, ok := c.comments[k]
	if !ok || !e.updated.Equal(updated) {
		return nil, false
	}
	return e.comments, true
}

// freshComments returns the cached thread when it was read less than detailTTL
// ago, whatever Updated it was read at.
func (c *Collector) freshComments(k issueKey, now time.Time) ([]forge.Comment, bool) {
	c.cacheMu.Lock()
	defer c.cacheMu.Unlock()
	e, ok := c.comments[k]
	if !ok || now.Sub(e.readAt) >= detailTTL {
		return nil, false
	}
	return e.comments, true
}

// staleComments returns the cached thread whatever Updated it was read at.
func (c *Collector) staleComments(k issueKey) ([]forge.Comment, bool) {
	c.cacheMu.Lock()
	defer c.cacheMu.Unlock()
	e, ok := c.comments[k]
	return e.comments, ok
}

// storeComments caches a thread under the Updated it was read at. A failed
// read never gets here, so the next tick retries it.
func (c *Collector) storeComments(k issueKey, updated time.Time, comments []forge.Comment) {
	c.cacheMu.Lock()
	defer c.cacheMu.Unlock()
	c.comments[k] = cachedComments{updated: updated, readAt: c.now(), comments: comments}
}

// pruneComments drops the threads of issues that are no longer inbox
// candidates, so a closed or relabelled issue stops costing memory.
func (c *Collector) pruneComments(repos []RepoData) {
	live := map[issueKey]bool{}
	for _, rd := range repos {
		for number := range rd.Comments {
			live[issueKey{repoName(rd), number}] = true
		}
	}
	c.cacheMu.Lock()
	defer c.cacheMu.Unlock()
	maps.DeleteFunc(c.comments, func(k issueKey, _ cachedComments) bool { return !live[k] })
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

// publish rebuilds board and inbox and fires onChange (outside the lock) once
// per payload whose content, ignoring CollectedAt, changed.
func (c *Collector) publish() {
	c.mu.Lock()
	board, inbox := c.rebuildLocked()
	c.mu.Unlock()
	if c.onChange == nil {
		return
	}
	if board {
		c.onChange(EventPipeline)
	}
	if inbox {
		c.onChange(EventInbox)
	}
}

// rebuildLocked recomputes board and inbox and reports, per payload, whether
// it differs from the previous one apart from CollectedAt.
func (c *Collector) rebuildLocked() (board, inbox bool) {
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
	b.Skipped = skippedForges(c.unsupported)
	raw, err := json.Marshal(b)
	board = err != nil || !bytes.Equal(raw, c.boardJSON)
	c.boardJSON = raw
	b.CollectedAt = maps.Clone(c.collectedAt)
	c.board = b
	return board, c.rebuildInboxLocked()
}

// rebuildInboxLocked recomputes the inbox and reports whether it changed. The
// clock is read on every rebuild so a marker going stale (a retry that never
// reported) surfaces on the next tick, with no new data needed.
func (c *Collector) rebuildInboxLocked() bool {
	ib := BuildInbox(InboxInput{Repos: slices.Clone(c.repos), Now: c.now()})
	raw, err := json.Marshal(ib)
	changed := err != nil || !bytes.Equal(raw, c.inboxJSON)
	c.inboxJSON = raw
	ib.CollectedAt = c.collectedAt[sourceIssues]
	c.inbox = ib
	return changed
}
