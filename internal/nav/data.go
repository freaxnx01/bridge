package nav

import (
	"os/exec"
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

func createWorktreeCmd(repo core.Repo, name string) tea.Cmd {
	return func() tea.Msg {
		dir, _, err := worktree.Resolve(worktree.ExecRunner{}, repo.Path, name)
		if err != nil {
			return wtCreatedMsg{err: err}
		}
		return wtCreatedMsg{row: dashRow{worktree: name, branch: "worktree-" + name, path: dir, dirtyState: loadPending}}
	}
}

func cloneCmd(clone func(forge.RepoRef) (core.Repo, error), ref forge.RepoRef) tea.Cmd {
	return func() tea.Msg {
		repo, err := clone(ref)
		return cloneDoneMsg{repo: repo, err: err}
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
