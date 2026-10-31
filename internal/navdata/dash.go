package navdata

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/freaxnx01/bridge/internal/core"
	"github.com/freaxnx01/bridge/internal/gitauth"
	"github.com/freaxnx01/bridge/internal/worktree"
)

// DashRow is one dashboard row: a worktree (or the pinned base checkout),
// optionally with a live session.
type DashRow struct {
	Worktree     string // basename; "" for the base row
	Branch       string
	Path         string
	Agent        string // "" when no live session
	SlotID       string // "" when no live session
	State        string // tmux: attached|detached · Herdr: working|idle|blocked|done|unknown
	LastAccessed string
	HasSession   bool
	// DisplayLabel overrides the default "<repo> [<wt>]" session name ("#123 [<title>]").
	DisplayLabel string
	// IsBase marks the pinned base-checkout row: it launches in the repo root
	// with the bare "<repo>" slot (Worktree == "").
	IsBase bool
}

// Dirty is a worktree's git status. Behind is only accurate after a fetch.
type Dirty struct {
	Modified   int
	Ahead      int
	Behind     int
	NoUpstream bool
	Clean      bool
}

// SessionRow is one global active-session row.
type SessionRow struct {
	SlotID       string
	RepoLabel    string
	Worktree     string
	Agent        string
	State        string
	LastAccessed string
}

// LastAccessed renders a session's "last accessed" cell: the humanized age,
// or — when the backend reports no timestamp (Herdr) — its lifecycle state.
func LastAccessed(sess core.Session, now time.Time) string {
	if !sess.LastActivity.IsZero() {
		return HumanLastAccessed(now.Sub(sess.LastActivity))
	}
	if sess.State != "" {
		return sess.State
	}
	return "—"
}

// HumanLastAccessed renders d as at most two descending units (d/h/m).
func HumanLastAccessed(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	mins := int(d.Minutes()) % 60
	switch {
	case days > 0:
		return fmt.Sprintf("%dd %dh", days, hours)
	case hours > 0:
		return fmt.Sprintf("%dh %dm", hours, mins)
	default:
		return fmt.Sprintf("%dm", mins)
	}
}

// SessionRows pairs each live session with its slot record. Sessions created
// outside bridge have no slot, so RepoLabel falls back to the slot id.
func SessionRows(live []core.Session, slots []core.Slot, now time.Time) []SessionRow {
	bySlot := make(map[string]core.Slot, len(slots))
	for _, s := range slots {
		bySlot[s.ID] = s
	}
	rows := make([]SessionRow, 0, len(live))
	for _, s := range live {
		row := SessionRow{SlotID: s.SlotID, State: s.State, LastAccessed: LastAccessed(s, now)}
		if sl, ok := bySlot[s.SlotID]; ok {
			row.RepoLabel = sl.Repo
			row.Worktree = sl.Worktree
			row.Agent = sl.Agent
		} else {
			row.RepoLabel = s.SlotID
		}
		rows = append(rows, row)
	}
	return rows
}

// SlotRepoMatches reports whether a slot's Repo field ("bridge" or
// "freaxnx01/bridge") refers to repo.
func SlotRepoMatches(slotRepo string, repo core.Repo) bool {
	if strings.EqualFold(slotRepo, repo.Name) {
		return true
	}
	return strings.HasSuffix(strings.ToLower(slotRepo), "/"+strings.ToLower(repo.Name))
}

// BuildDashRows joins the repo's worktrees with slots and live sessions. The
// base row comes first; sessioned worktrees follow by last activity (newest
// first), then session-less worktrees by name.
func BuildDashRows(repo core.Repo, baseBranch string, wts []worktree.Entry, slots []core.Slot, sessions []core.Session, now time.Time) []DashRow {
	liveBySlot := make(map[string]core.Session, len(sessions))
	for _, s := range sessions {
		liveBySlot[s.SlotID] = s
	}
	slotByWt := make(map[string]core.Slot)
	for _, sl := range slots {
		if SlotRepoMatches(sl.Repo, repo) && sl.Worktree != "" {
			slotByWt[sl.Worktree] = sl
		}
	}
	rows := make([]DashRow, 0, len(wts))
	for _, e := range wts {
		name := filepath.Base(e.Path)
		row := DashRow{Worktree: name, Branch: e.Branch, Path: e.Path}
		if sl, ok := slotByWt[name]; ok {
			if sess, live := liveBySlot[sl.ID]; live {
				row.HasSession = true
				row.SlotID = sl.ID
				row.Agent = sl.Agent
				row.State = sess.State
				row.LastAccessed = LastAccessed(sess, now)
			}
		}
		rows = append(rows, row)
	}
	sortDashRows(rows, liveBySlot)
	return append([]DashRow{baseRow(repo, baseBranch, slots, liveBySlot, now)}, rows...)
}

// baseRow builds the pinned base-checkout row (slot "<repo>", worktree "").
func baseRow(repo core.Repo, branch string, slots []core.Slot, liveBySlot map[string]core.Session, now time.Time) DashRow {
	row := DashRow{IsBase: true, Branch: branch, Path: repo.Path}
	id := core.SlotID(repo.Name, "")
	sess, live := liveBySlot[id]
	if !live {
		return row
	}
	row.HasSession = true
	row.SlotID = id
	row.State = sess.State
	row.LastAccessed = LastAccessed(sess, now)
	for _, sl := range slots {
		if sl.ID == id {
			row.Agent = sl.Agent
			break
		}
	}
	return row
}

func sortDashRows(rows []DashRow, liveBySlot map[string]core.Session) {
	activity := func(r DashRow) (time.Time, bool) {
		if !r.HasSession {
			return time.Time{}, false
		}
		return liveBySlot[r.SlotID].LastActivity, true
	}
	sort.SliceStable(rows, func(i, j int) bool {
		ai, aok := activity(rows[i])
		bj, bok := activity(rows[j])
		if aok != bok {
			return aok
		}
		if aok && bok {
			return ai.After(bj)
		}
		return rows[i].Worktree < rows[j].Worktree
	})
}

// DashRows loads the dashboard rows for repo: its worktrees, the primary
// checkout's branch, the slot registry at slotsPath and the live sessions
// from live (nil => none). Read failures degrade to fewer rows; the base row
// is always present.
func DashRows(ctx context.Context, live func() ([]core.Session, error), repo core.Repo, slotsPath string) []DashRow {
	if ctx.Err() != nil {
		return nil
	}
	r := worktree.ExecRunner{}
	wts, _ := worktree.List(r, repo.Path)
	primary, _ := worktree.Primary(r, repo.Path)
	slots, _ := core.LoadSlots(slotsPath)
	var sessions []core.Session
	if live != nil {
		sessions, _ = live()
	}
	return BuildDashRows(repo, primary.Branch, wts, slots, sessions, time.Now())
}

// ParseDirtyStatus parses `git status --porcelain=v1 --branch` output.
func ParseDirtyStatus(out string) Dirty {
	info := Dirty{}
	for _, l := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if l == "" {
			continue
		}
		if strings.HasPrefix(l, "## ") {
			header := l[len("## "):]
			info.NoUpstream = !strings.Contains(header, "...")
			if i := strings.IndexByte(header, '['); i >= 0 {
				if j := strings.IndexByte(header[i:], ']'); j >= 0 {
					bracket := header[i+1 : i+j]
					info.Ahead = trackToken(bracket, "ahead ")
					info.Behind = trackToken(bracket, "behind ")
				}
			}
			continue
		}
		info.Modified++
	}
	info.Clean = info.Modified == 0
	return info
}

func trackToken(bracket, key string) int {
	i := strings.Index(bracket, key)
	if i < 0 {
		return 0
	}
	rest := bracket[i+len(key):]
	end := len(rest)
	for k, c := range rest {
		if c < '0' || c > '9' {
			end = k
			break
		}
	}
	n, _ := strconv.Atoi(rest[:end])
	return n
}

// GitDirty runs `git status --porcelain=v1 --branch` in path.
func GitDirty(ctx context.Context, path string) (Dirty, error) {
	out, err := exec.CommandContext(ctx, "git", "-C", path, "status", "--porcelain=v1", "--branch").Output()
	if err != nil {
		return Dirty{}, fmt.Errorf("git status %s: %w", path, err)
	}
	return ParseDirtyStatus(string(out)), nil
}

// BuildFetchCmd assembles the `git fetch` for a worktree. Token forges (ado,
// github, forgejo) run through `direnv exec <path> git -c <helper> …` when
// direnv is available so the helper reads the token from the repo's .envrc;
// otherwise plain git with the same helper. GIT_TERMINAL_PROMPT=0 makes a
// missing credential fail fast instead of prompting.
func BuildFetchCmd(ctx context.Context, path, forgeName string, direnvAvailable bool) *exec.Cmd {
	credArgs := gitauth.CredentialArgs(forgeName)
	gitArgs := append(append([]string{}, credArgs...), "-C", path, "fetch", "--quiet")
	var cmd *exec.Cmd
	if credArgs != nil && direnvAvailable {
		cmd = exec.CommandContext(ctx, "direnv", append([]string{"exec", path, "git"}, gitArgs...)...)
	} else {
		cmd = exec.CommandContext(ctx, "git", gitArgs...)
	}
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	return cmd
}

// HaveDirenv reports whether direnv is on PATH.
func HaveDirenv() bool {
	_, err := exec.LookPath("direnv")
	return err == nil
}

// GitFetch freshens the repo's remote-tracking refs so ahead/behind are
// accurate. One fetch serves all worktrees (shared object store).
func GitFetch(ctx context.Context, path, forgeName string) error {
	if err := BuildFetchCmd(ctx, path, forgeName, HaveDirenv()).Run(); err != nil {
		return fmt.Errorf("git fetch %s: %w", path, err)
	}
	return nil
}
