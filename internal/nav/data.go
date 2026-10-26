package nav

import (
	"context"
	"os/exec"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/freaxnx01/bridge/internal/core"
	"github.com/freaxnx01/bridge/internal/forge"
	"github.com/freaxnx01/bridge/internal/launcher"
	"github.com/freaxnx01/bridge/internal/navdata"
	"github.com/freaxnx01/bridge/internal/worktree"
)

func loadLocalReposCmd(roots []string) tea.Cmd {
	return func() tea.Msg {
		return reposMsg{rows: toRepoRows(navdata.LocalRepos(context.Background(), roots))}
	}
}

// loadRecentCmd reads the MRU file (read-only) and returns its paths, most-
// recent-first. A read error is swallowed to an empty list, matching nav's
// best-effort loaders.
func loadRecentCmd(path string) tea.Cmd {
	return func() tea.Msg {
		return recentMsg{paths: navdata.RecentPaths(path)}
	}
}

func loadSessionsCmd(b launcher.Backend, slotsPath string) tea.Cmd {
	return func() tea.Msg {
		live, _ := b.Live()
		slots, _ := core.LoadSlots(slotsPath)
		return sessionsMsg{rows: buildSessionRows(live, slots, time.Now())}
	}
}

// buildSessionRows pairs each live tmux session with its slot record (if any) to
// build the "Active sessions" panel rows. Sessions created outside bridge have no
// slot, so the name column falls back to the tmux session name (slotID) to keep
// every row identifiable rather than blank.
func buildSessionRows(live []core.Session, slots []core.Slot, now time.Time) []sessionRow {
	return fromSessionRows(navdata.SessionRows(live, slots, now))
}

func loadRemoteCmd(cachePath string) tea.Cmd {
	return func() tea.Msg {
		es, err := navdata.RemoteRepos(cachePath)
		if err != nil {
			return remoteErrMsg{err: err}
		}
		return remoteMsg{rows: toRepoRows(es)}
	}
}

// remoteRows builds sorted, ↓-prefixed picker rows from forge repo refs.
func remoteRows(refs []forge.RepoRef) []repoRow {
	return toRepoRows(navdata.RemoteEntries(refs))
}

// registerSlotCmd records a launched session in the slot registry so the
// dashboard, `bridge sessions`, and `slots prune` can associate the worktree
// with its tmux session. Best-effort, matching the open path (non-fatal).
func registerSlotCmd(slotsPath string, slot core.Slot) tea.Cmd {
	return func() tea.Msg {
		_ = core.UpsertSlot(slotsPath, slot) // best-effort, matches the open path (non-fatal)
		return slotRegisteredMsg{}
	}
}

func loadDashRowsCmd(b launcher.Backend, repo core.Repo, slotsPath string) tea.Cmd {
	return func() tea.Msg {
		return dashRowsMsg{rows: fromDashRows(navdata.DashRows(context.Background(), b.Live, repo, slotsPath))}
	}
}

// gitDirtyCmd reports modified-file count and ahead count for one worktree from
// a single `git status --porcelain=v1 --branch` call.
func gitDirtyCmd(path string) tea.Cmd {
	return func() tea.Msg {
		d, err := navdata.GitDirty(context.Background(), path)
		if err != nil {
			return dirtyMsg{path: path, err: err}
		}
		return dirtyMsg{path: path, info: toDirtyInfo(d)}
	}
}

// createWorktreeCmd resolves (creating if needed) worktree name and returns a
// dashRow to launch. label, when non-empty, overrides the session display name
// (issue launches pass "#123 [<title>]"); "" keeps the default "<repo> [<wt>]".
func createWorktreeCmd(repo core.Repo, name, label string) tea.Cmd {
	return func() tea.Msg {
		dir, _, err := worktree.Resolve(worktree.ExecRunner{}, repo.Path, name)
		if err != nil {
			return wtCreatedMsg{err: err}
		}
		return wtCreatedMsg{row: dashRow{worktree: name, branch: "worktree-" + name, path: dir, displayLabel: label, dirtyState: loadPending}}
	}
}

func cloneCmd(clone func(forge.RepoRef) (core.Repo, error), ref forge.RepoRef) tea.Cmd {
	return func() tea.Msg {
		repo, err := clone(ref)
		return cloneDoneMsg{repo: repo, err: err}
	}
}

// gitBranchesCmd lists the repo's branches (most-recent committerdate first) for
// the Branches panel, marking the current ("*") and worktree-occupied ("+")
// branches. Runs against the worktree path so "*" reflects that checkout's HEAD.
func gitBranchesCmd(path string) tea.Cmd {
	return func() tea.Msg {
		out, err := exec.Command("git", "-C", path, "branch", "--sort=-committerdate").Output()
		if err != nil {
			return branchesMsg{path: path, err: err}
		}
		return branchesMsg{path: path, branches: parseBranches(string(out))}
	}
}

// gitCommitsCmd reads the worktree HEAD's recent commits for the Recent-commits
// panel. The fixed -n cap bounds output; the view truncates to fit.
func gitCommitsCmd(path string) tea.Cmd {
	return func() tea.Msg {
		out, err := exec.Command("git", "-C", path, "log", "--format=%h%x00%s", "-n", "20").Output()
		if err != nil {
			// A repo with no commits also errors here; render it as "unavailable"
			// rather than guessing — fresh worktrees always have commits.
			return commitsMsg{path: path, err: err}
		}
		return commitsMsg{path: path, commits: parseCommits(string(out))}
	}
}

// gitStatusCmd reads the worktree's changed-file list for the Git-status panel.
// Distinct from gitDirtyCmd, which only counts files for the per-row indicator.
func gitStatusCmd(path string) tea.Cmd {
	return func() tea.Msg {
		out, err := exec.Command("git", "-C", path, "status", "--porcelain").Output()
		if err != nil {
			return statusMsg{path: path, err: err}
		}
		return statusMsg{path: path, files: parseStatusFiles(string(out))}
	}
}

// gitFetchCmd freshens the repo's remote-tracking refs so ahead/behind are
// accurate. Runs once per dashboard — worktrees of a repo share the object
// store, so one fetch updates remote refs for all of them. Non-fatal: a failed
// fetch (offline / missing credential) reports the error; the reducer keeps
// last-known state.
func gitFetchCmd(path, forgeName string) tea.Cmd {
	return func() tea.Msg {
		return fetchDoneMsg{err: navdata.GitFetch(context.Background(), path, forgeName)}
	}
}

// buildFetchCmd assembles the `git fetch` invocation for a worktree. For forges
// that authenticate with a PAT/token (ado, github, forgejo) it runs through
// `direnv exec <path> git -c <helper> …` so the credential helper reads the
// token direnv injects from the repo's .envrc — mirroring the clone path. When
// direnv is unavailable (e.g. Windows) it runs plain git with the same helper,
// which then reads the token from the process env. It always sets
// GIT_TERMINAL_PROMPT=0 so a missing or invalid credential fails fast instead of
// leaking an interactive password prompt into the Bubble Tea UI.
func buildFetchCmd(path, forgeName string, direnvAvailable bool) *exec.Cmd {
	return navdata.BuildFetchCmd(context.Background(), path, forgeName, direnvAvailable)
}

// rowForgeKey returns a stable key and forge/owner/name for a picker row.
// Returns ok=false when the row lacks identifiers needed for issue loading.
func rowForgeKey(r repoRow) (key, forgeName, owner, name string, ok bool) {
	if r.remote != nil {
		if r.remote.Forge == "" || r.remote.Owner == "" || r.remote.Name == "" {
			return "", "", "", "", false
		}
		return r.remote.Forge + "/" + r.remote.Owner + "/" + r.remote.Name,
			r.remote.Forge, r.remote.Owner, r.remote.Name, true
	}
	if r.repo.Forge == "" || r.repo.Owner == "" || r.repo.Name == "" {
		return "", "", "", "", false
	}
	return r.repo.Forge + "/" + r.repo.Owner + "/" + r.repo.Name,
		r.repo.Forge, r.repo.Owner, r.repo.Name, true
}

// fetchIssuesCached returns a repo's open issues, reading the per-repo cache
// first and refreshing via FetchIssues only when the cache is missing or stale.
// Degrades gracefully: a cache miss with no FetchIssues yields the (empty)
// cached slice; a fetch error falls back to the last cached slice.
func fetchIssuesCached(cfg Config, forgeName, owner, repo string) []forge.Issue {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return navdata.IssuesCached(ctx, navdata.IssueSource{CacheDir: cfg.IssueCacheDir, Fetch: cfg.FetchIssues}, forgeName, owner, repo)
}

// loadIssueCountCmd loads a repo's open-issue count for a picker row.
func loadIssueCountCmd(cfg Config, key, forgeName, owner, repo string) tea.Cmd {
	return func() tea.Msg {
		return issueCountMsg{key: key, count: len(fetchIssuesCached(cfg, forgeName, owner, repo))}
	}
}

// loadRepoIssuesCmd loads the full open-issue list for the dashboard repo.
func loadRepoIssuesCmd(cfg Config, forgeName, owner, repo string) tea.Cmd {
	return func() tea.Msg {
		return repoIssuesMsg{issues: fetchIssuesCached(cfg, forgeName, owner, repo)}
	}
}

// refreshRemoteCmd re-queries the forge via the injected FetchRemote callback
// and rebuilds the ↓-prefixed remote rows. When FetchRemote is nil (callback
// not wired), it falls back to re-reading the on-disk cache.
func (m Model) refreshRemoteCmd() tea.Cmd {
	if m.cfg.FetchRemote == nil {
		return loadRemoteCmd(m.cfg.RemoteCache)
	}
	fetch := m.cfg.FetchRemote
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		refs, err := fetch(ctx)
		if err != nil {
			// Refresh returns partial refs alongside the first error when one
			// forge fails but others succeed; keep those fresh rows.
			return remoteErrMsg{err: err, rows: remoteRows(refs)}
		}
		return remoteMsg{rows: remoteRows(refs)}
	}
}

// createRepoCmd runs the injected CreateRepo callback off the Update loop for
// the modal's selected name + forge×visibility choice.
func (m Model) createRepoCmd() tea.Cmd {
	create := m.cfg.CreateRepo
	ch := repoForgeChoices[m.repoModal.sel]
	name := strings.TrimSpace(m.repoModal.name)
	return func() tea.Msg {
		repo, err := create(name, ch.forge, ch.private)
		return repoCreatedMsg{repo: repo, err: err}
	}
}
