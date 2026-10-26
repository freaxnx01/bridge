//go:build !windows

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/freaxnx01/bridge/internal/bot"
	"github.com/freaxnx01/bridge/internal/core"
	"github.com/freaxnx01/bridge/internal/forge"
	"github.com/freaxnx01/bridge/internal/launch"
	"github.com/freaxnx01/bridge/internal/navdata"
	"github.com/freaxnx01/bridge/internal/remote"
)

// rcWait is how long a bot start waits for claude's Remote Control link
// (inside the 30 s action timeout).
const rcWait = 20 * time.Second

// botEnv is the bot's I/O: each field performs one bot.Action.
type botEnv struct {
	loadRepos  func(ctx context.Context, refresh bool) (local, remote []navdata.RepoEntry, mru []string, err error)
	dashRows   func(ctx context.Context, repo core.Repo) []navdata.DashRow
	status     func(ctx context.Context, path string) (navdata.Dirty, error)
	fetch      func(ctx context.Context, repo core.Repo) error
	issues     func(ctx context.Context, repo core.Repo) []forge.Issue
	start      func(ctx context.Context, a bot.Start) bot.Started
	stop       func(ctx context.Context, slot string) error
	clone      navdata.CloneFunc
	createRepo navdata.CreateRepoFunc
}

// newBotEnv wires the bot to the same data and launch paths nav and
// `bridge launch` use.
func newBotEnv() botEnv {
	cache := cacheRoot()
	remotePath := filepath.Join(cache, "remote.list")
	// The service has no forge tokens in its env: resolve them via direnv.
	fetchIssues := newIssueFetcher(issueClientResolver(clientFor, clientForMCP(reposRoots())))
	return botEnv{
		loadRepos: func(ctx context.Context, refresh bool) ([]navdata.RepoEntry, []navdata.RepoEntry, []string, error) {
			roots := reposRoots()
			var rem []navdata.RepoEntry
			var err error
			if refresh {
				var refs []forge.RepoRef
				refs, err = remote.Refresh(ctx, roots, remotePath)
				rem = navdata.RemoteEntries(refs)
			}
			if !refresh || (err != nil && len(rem) == 0) {
				// No refresh, or a total refresh failure: show the cache.
				cached, cerr := navdata.RemoteRepos(remotePath)
				rem, err = cached, errors.Join(err, cerr)
			}
			return navdata.LocalRepos(ctx, roots), rem, navdata.RecentPaths(filepath.Join(cache, "mru")), err
		},
		dashRows: func(ctx context.Context, repo core.Repo) []navdata.DashRow {
			return navdata.DashRows(ctx, loadSessions, repo, filepath.Join(cache, "slots.json"))
		},
		status: navdata.GitDirty,
		fetch: func(ctx context.Context, repo core.Repo) error {
			return navdata.GitFetch(ctx, repo.Path, repo.Forge)
		},
		issues: func(ctx context.Context, repo core.Repo) []forge.Issue {
			src := navdata.IssueSource{CacheDir: filepath.Join(cache, "issues"), Fetch: fetchIssues}
			return navdata.IssuesCached(ctx, src, repo.Forge, repo.Owner, repo.Name)
		},
		start: startForBot,
		stop: func(ctx context.Context, slot string) error {
			return launch.Stop(ctx, launchDeps(), slot)
		},
		clone: func(_ context.Context, ref forge.RepoRef) (core.Repo, error) {
			dir, err := cloneRemoteRepo(ref)
			if err != nil {
				return core.Repo{}, err
			}
			return repoFromClonedRef(reposRoot(), ref, dir), nil
		},
		createRepo: func(ctx context.Context, name, forgeName string, private bool) (core.Repo, error) {
			repo, _, err := createAndClone(ctx, name, forgeName, private)
			return repo, err
		},
	}
}

// startForBot creates the worktree when asked, then starts (or finds) the
// session detached with Remote Control and waits for its link. The agent is
// BRIDGE_DEFAULT_AGENT (+ _ARGS), falling back to claude like nav; Remote
// Control is only requested for claude. No pre-launch sync (nav parity).
func startForBot(ctx context.Context, a bot.Start) bot.Started {
	d := launchDeps()
	if a.Create {
		if _, err := navdata.CreateWorktree(ctx, a.Repo, a.Worktree, a.Label); err != nil {
			return bot.Started{Err: errors.New(navdata.WorktreeErrMessage(err))}
		}
	}
	agent := ""
	if os.Getenv("BRIDGE_DEFAULT_AGENT") == "" {
		agent = "claude"
	}
	t, err := launch.TargetFor(ctx, d, a.Repo, a.Worktree, agent)
	if err != nil {
		return bot.Started{Err: err}
	}
	t.Label = a.Label
	rc := t.Spec.Name == "claude"
	running, err := launch.StartDetached(ctx, d, &t, rc, true)
	if err != nil {
		return bot.Started{Slot: t.Slot, Err: err}
	}
	res := bot.Started{Slot: t.Slot, AlreadyRunning: running}
	if rc {
		res.RCURL = launch.WaitRCURL(ctx, d, t.Slot, rcWait)
		res.Exited = res.RCURL == "" && !launch.SessionLive(d, t.Slot)
	}
	return res
}

// within runs fn but gives up when ctx ends, so a call that ignores ctx
// (cloneRemoteRepo) can never keep an action — and the chat's Busy — open
// past its timeout. The abandoned call finishes in the background.
func within[T any](ctx context.Context, fn func() (T, error)) (T, error) {
	type result struct {
		v   T
		err error
	}
	ch := make(chan result, 1)
	go func() {
		v, err := fn()
		ch <- result{v, err}
	}()
	select {
	case r := <-ch:
		return r.v, r.err
	case <-ctx.Done():
		var zero T
		return zero, fmt.Errorf("timed out: %w", ctx.Err())
	}
}

// exec performs one action and returns its result event (nil for none).
func (e botEnv) exec(ctx context.Context, a bot.Action) bot.Event {
	switch a := a.(type) {
	case bot.LoadRepos:
		type repos struct {
			local, remote []navdata.RepoEntry
			mru           []string
		}
		r, err := within(ctx, func() (repos, error) {
			l, rm, m, lerr := e.loadRepos(ctx, a.Refresh)
			return repos{l, rm, m}, lerr
		})
		return bot.ReposLoaded{Local: r.local, Remote: r.remote, MRU: r.mru, Err: err}
	case bot.LoadDash:
		rows, _ := within(ctx, func() ([]navdata.DashRow, error) { return e.dashRows(ctx, a.Repo), nil })
		if rows == nil {
			rows = []navdata.DashRow{}
		}
		return bot.DashLoaded{Repo: a.Repo, Rows: rows}
	case bot.LoadStatus:
		st := make(map[string]bot.RowStatus, len(a.Paths))
		for _, p := range a.Paths {
			d, err := within(ctx, func() (navdata.Dirty, error) { return e.status(ctx, p) })
			st[p] = bot.RowStatus{Dirty: d, Err: err != nil}
		}
		return bot.StatusLoaded{Repo: a.Repo, Status: st}
	case bot.Fetch:
		_, err := within(ctx, func() (struct{}, error) { return struct{}{}, e.fetch(ctx, a.Repo) })
		return bot.FetchDone{Repo: a.Repo, Err: err}
	case bot.LoadIssues:
		is, _ := within(ctx, func() ([]forge.Issue, error) { return e.issues(ctx, a.Repo), nil })
		return bot.IssuesLoaded{Repo: a.Repo, Issues: is}
	case bot.Start:
		res, err := within(ctx, func() (bot.Started, error) { return e.start(ctx, a), nil })
		if err != nil {
			return bot.Started{Err: err}
		}
		return res
	case bot.Stop:
		_, err := within(ctx, func() (struct{}, error) { return struct{}{}, e.stop(ctx, a.Slot) })
		return bot.Stopped{Slot: a.Slot, Err: err}
	case bot.Clone:
		repo, err := within(ctx, func() (core.Repo, error) { return e.clone(ctx, a.Ref) })
		return bot.Cloned{Repo: repo, Err: err}
	case bot.CreateRepo:
		repo, err := within(ctx, func() (core.Repo, error) { return e.createRepo(ctx, a.Name, a.Forge, a.Private) })
		return bot.RepoCreated{Repo: repo, Err: err}
	}
	return nil
}
