package nav

import (
	"fmt"
	"path/filepath"
	"sort"
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
