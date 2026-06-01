package nav

import (
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/freaxnx01/bridge/internal/core"
	"github.com/freaxnx01/bridge/internal/worktree"
)

// humanLastAccessed renders d as at most two descending units (d/h/m).
// Sub-minute durations render as "0m".
func humanLastAccessed(d time.Duration) string {
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

// filterRepos keeps rows whose label contains q (case-insensitive). Empty q
// returns all rows. Result is a new slice; input is not mutated.
func filterRepos(rows []repoRow, q string) []repoRow {
	if strings.TrimSpace(q) == "" {
		return rows
	}
	needle := strings.ToLower(q)
	out := make([]repoRow, 0, len(rows))
	for _, r := range rows {
		if strings.Contains(strings.ToLower(r.label), needle) {
			out = append(out, r)
		}
	}
	return out
}

// slotRepoMatches reports whether a slot's Repo field refers to repo. The
// registry stores Repo as either a bare name ("bridge") or an owner-qualified
// label ("freaxnx01/bridge"), so match on equality or a "/"+name suffix.
func slotRepoMatches(slotRepo string, repo core.Repo) bool {
	if strings.EqualFold(slotRepo, repo.Name) {
		return true
	}
	return strings.HasSuffix(strings.ToLower(slotRepo), "/"+strings.ToLower(repo.Name))
}

// buildDashRows joins the repo's worktrees with the global sessions/slots into
// dashboard rows. A worktree gets a live session when a slot for this repo names
// it and that slot's tmux session is live. Rows with a session sort first by
// last-accessed DESC; session-less worktrees follow, name-sorted. dirtyState is
// loadPending (filled later by dirtyMsg).
func buildDashRows(repo core.Repo, wts []worktree.Entry, slots []core.Slot, sessions []core.Session, now time.Time) []dashRow {
	liveBySlot := make(map[string]core.Session, len(sessions))
	for _, s := range sessions {
		liveBySlot[s.SlotID] = s
	}
	// worktree name -> slot (for this repo only)
	slotByWt := make(map[string]core.Slot)
	for _, sl := range slots {
		if slotRepoMatches(sl.Repo, repo) && sl.Worktree != "" {
			slotByWt[sl.Worktree] = sl
		}
	}
	rows := make([]dashRow, 0, len(wts))
	for _, e := range wts {
		name := filepath.Base(e.Path)
		row := dashRow{worktree: name, branch: e.Branch, path: e.Path, dirtyState: loadPending}
		if sl, ok := slotByWt[name]; ok {
			if sess, live := liveBySlot[sl.ID]; live {
				row.hasSession = true
				row.slotID = sl.ID
				row.agent = sl.Agent
				row.state = sess.State
				row.lastAccessed = humanLastAccessed(now.Sub(sess.LastActivity))
			}
		}
		rows = append(rows, row)
	}
	sortDashRows(rows, liveBySlot)
	return rows
}

// sortDashRows orders sessioned rows first (last-accessed DESC), then
// session-less rows by worktree name. Uses the live session's LastActivity for
// the time comparison.
func sortDashRows(rows []dashRow, liveBySlot map[string]core.Session) {
	activity := func(r dashRow) (time.Time, bool) {
		if !r.hasSession {
			return time.Time{}, false
		}
		return liveBySlot[r.slotID].LastActivity, true
	}
	sort.SliceStable(rows, func(i, j int) bool {
		ai, aok := activity(rows[i])
		bj, bok := activity(rows[j])
		if aok != bok {
			return aok // sessioned before session-less
		}
		if aok && bok {
			return ai.After(bj) // most recent first
		}
		return rows[i].worktree < rows[j].worktree
	})
}

// parseDirtyStatus parses `git status --porcelain=v1 --branch` output into a
// dirtyInfo: modified counts the non-header change lines; ahead is read from the
// "[ahead N]" token in the "## " branch header (0 when absent or no upstream).
func parseDirtyStatus(out string) dirtyInfo {
	info := dirtyInfo{}
	for _, l := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if l == "" {
			continue
		}
		if strings.HasPrefix(l, "## ") {
			if i := strings.Index(l, "[ahead "); i >= 0 {
				rest := l[i+len("[ahead "):]
				num := rest
				for j, c := range rest {
					if c < '0' || c > '9' {
						num = rest[:j]
						break
					}
				}
				info.ahead, _ = strconv.Atoi(num)
			}
			continue
		}
		info.modified++
	}
	info.clean = info.modified == 0
	return info
}

// sortRepoRows orders repo rows ascending by label, case-insensitively and
// ignoring the remote "↓ " prefix so local and remote rows compare on the same
// key. Stable, so equal keys keep their input order.
func sortRepoRows(rows []repoRow) {
	sort.SliceStable(rows, func(i, j int) bool {
		return repoSortKey(rows[i].label) < repoSortKey(rows[j].label)
	})
}

func repoSortKey(label string) string {
	return strings.ToLower(strings.TrimPrefix(label, "↓ "))
}

// parseBranches parses `git branch --sort=-committerdate` output. Each line's
// first two columns are the marker: "* " current HEAD, "+ " checked out in
// another worktree, "  " plain. Input order is preserved.
func parseBranches(out string) []branchInfo {
	var rows []branchInfo
	for _, l := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if l == "" {
			continue
		}
		var b branchInfo
		switch {
		case strings.HasPrefix(l, "* "):
			b.current = true
		case strings.HasPrefix(l, "+ "):
			b.inWorktree = true
		}
		b.name = strings.TrimSpace(l[min(2, len(l)):])
		rows = append(rows, b)
	}
	return rows
}

// parseCommits parses `git log --format=%h%x00%s` output: one commit per line,
// short SHA and subject separated by a NUL byte.
func parseCommits(out string) []commitInfo {
	var rows []commitInfo
	for _, l := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if l == "" {
			continue
		}
		sha, subject, _ := strings.Cut(l, "\x00")
		rows = append(rows, commitInfo{sha: sha, subject: subject})
	}
	return rows
}

// parseStatusFiles parses `git status --porcelain` output: a 2-char XY code, a
// space, then the path. Rename lines carry "old -> new"; the new path is kept.
func parseStatusFiles(out string) []statusFile {
	var rows []statusFile
	for _, l := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if len(l) < 3 {
			continue
		}
		code := l[:2]
		path := l[3:]
		if _, after, ok := strings.Cut(path, " -> "); ok {
			path = after
		}
		rows = append(rows, statusFile{code: code, path: path})
	}
	return rows
}

// windowAround returns the [start,end) sub-range of n items that keeps sel
// visible within a window of at most size items (centred where possible).
func windowAround(n, sel, size int) (start, end int) {
	if size <= 0 || n <= size {
		return 0, n
	}
	if sel < 0 {
		sel = 0
	}
	start = sel - size/2
	if start < 0 {
		start = 0
	}
	end = start + size
	if end > n {
		end = n
		start = end - size
	}
	return start, end
}
