package nav

import (
	"context"
	"os/exec"
	"path/filepath"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/freaxnx01/bridge/internal/core"
	"github.com/freaxnx01/bridge/internal/forge"
	"github.com/freaxnx01/bridge/internal/worktree"
)

func loadLocalReposCmd(roots []string) tea.Cmd {
	return func() tea.Msg {
		var rows []repoRow
		seen := map[string]bool{}
		for _, root := range roots {
			repos, err := core.DiscoverRepos(root)
			if err != nil {
				continue
			}
			for _, r := range repos {
				if seen[r.Path] {
					continue
				}
				seen[r.Path] = true
				rows = append(rows, repoRow{label: repoLabel(r), repo: r})
			}
		}
		sortRepoRows(rows)
		return reposMsg{rows: rows}
	}
}

func loadSessionsCmd(slotsPath string) tea.Cmd {
	return func() tea.Msg {
		live, _ := core.LiveSessions()
		slots, _ := core.LoadSlots(slotsPath)
		bySlot := make(map[string]core.Slot, len(slots))
		for _, s := range slots {
			bySlot[s.ID] = s
		}
		now := time.Now()
		rows := make([]sessionRow, 0, len(live))
		for _, s := range live {
			row := sessionRow{
				slotID:       s.SlotID,
				state:        s.State,
				lastAccessed: humanLastAccessed(now.Sub(s.LastActivity)),
			}
			if sl, ok := bySlot[s.SlotID]; ok {
				row.repoLabel = sl.Repo
				row.worktree = sl.Worktree
				row.agent = sl.Agent
			}
			rows = append(rows, row)
		}
		return sessionsMsg{rows: rows}
	}
}

func loadRemoteCmd(cachePath string) tea.Cmd {
	return func() tea.Msg {
		c, err := forge.ReadRepoCache(cachePath)
		if err != nil {
			return remoteErrMsg{err: err}
		}
		rows := make([]repoRow, 0, len(c.Repos))
		for i := range c.Repos {
			ref := c.Repos[i]
			rows = append(rows, repoRow{label: "↓ " + remoteLabel(ref), remote: &ref})
		}
		sortRepoRows(rows)
		return remoteMsg{rows: rows}
	}
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

func loadDashRowsCmd(repo core.Repo, slotsPath string) tea.Cmd {
	return func() tea.Msg {
		wts, _ := worktree.List(worktree.ExecRunner{}, repo.Path)
		slots, _ := core.LoadSlots(slotsPath)
		live, _ := core.LiveSessions()
		return dashRowsMsg{rows: buildDashRows(repo, wts, slots, live, time.Now())}
	}
}

// gitDirtyCmd reports modified-file count and ahead count for one worktree from
// a single `git status --porcelain=v1 --branch` call.
func gitDirtyCmd(path string) tea.Cmd {
	return func() tea.Msg {
		out, err := exec.Command("git", "-C", path, "status", "--porcelain=v1", "--branch").Output()
		if err != nil {
			return dirtyMsg{path: path, err: err}
		}
		return dirtyMsg{path: path, info: parseDirtyStatus(string(out))}
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
// fetch (offline) reports the error; the reducer keeps last-known state.
func gitFetchCmd(path string) tea.Cmd {
	return func() tea.Msg {
		err := exec.Command("git", "-C", path, "fetch", "--quiet").Run()
		return fetchDoneMsg{err: err}
	}
}

func repoLabel(r core.Repo) string {
	switch r.Forge {
	case "github":
		vis := r.Visibility
		if vis == "" {
			vis = "-"
		}
		return "github/" + vis + "/" + r.Name
	case "forgejo":
		return "forgejo/" + r.Name
	default:
		if r.Owner != "" {
			return r.Forge + "/" + r.Owner + "/" + r.Name
		}
		return r.Forge + "/" + r.Name
	}
}

func remoteLabel(r forge.RepoRef) string {
	if r.Forge == "github" {
		vis := r.Visibility
		if vis == "" {
			vis = "-"
		}
		return "github/" + vis + "/" + r.Name
	}
	if r.Owner != "" {
		return r.Forge + "/" + r.Owner + "/" + r.Name
	}
	return r.Forge + "/" + r.Name
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

const issueCacheTTL = 10 * time.Minute

// issueCacheFile is the per-repo cache path for forge/owner/repo, or "" when
// caching is disabled.
func issueCacheFile(cfg Config, forgeName, owner, repo string) string {
	if cfg.IssueCacheDir == "" {
		return ""
	}
	return filepath.Join(cfg.IssueCacheDir, forgeName+"_"+owner+"_"+repo+".json")
}

// fetchIssuesCached returns a repo's open issues, reading the per-repo cache
// first and refreshing via FetchIssues only when the cache is missing or stale.
// Degrades gracefully: a cache miss with no FetchIssues yields the (empty)
// cached slice; a fetch error falls back to the last cached slice.
func fetchIssuesCached(cfg Config, forgeName, owner, repo string) []forge.Issue {
	cacheFile := issueCacheFile(cfg, forgeName, owner, repo)
	var cached forge.IssueCache
	if cacheFile != "" {
		cached, _ = forge.ReadIssueCache(cacheFile)
		if !cached.UpdatedAt.IsZero() && !cached.IsStale(issueCacheTTL) {
			return cached.Issues
		}
	}
	if cfg.FetchIssues == nil {
		return cached.Issues
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	issues, err := cfg.FetchIssues(ctx, forgeName, owner, repo)
	if err != nil {
		return cached.Issues
	}
	if cacheFile != "" {
		_ = forge.WriteIssueCache(cacheFile, forge.IssueCache{UpdatedAt: time.Now(), Issues: issues})
	}
	return issues
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
