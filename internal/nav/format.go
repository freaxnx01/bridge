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

// dedupRemoteRows drops remote ("↓") rows whose repository is already cloned
// locally, so a cloned repo isn't listed twice in the picker. Identity is
// forge+owner+name compared case-insensitively, because the local owner is
// derived from the on-disk path while the remote owner comes from the forge
// API and their casing can differ. See #124.
func dedupRemoteRows(local, remote []repoRow) []repoRow {
	have := make(map[string]bool, len(local))
	for _, r := range local {
		have[repoRowKey(r)] = true
	}
	out := make([]repoRow, 0, len(remote))
	for _, r := range remote {
		if have[repoRowKey(r)] {
			continue
		}
		out = append(out, r)
	}
	return out
}

// repoRowKey is the case-insensitive forge+owner+name identity of a repo row,
// taken from its remote ref when present, otherwise its local repo.
func repoRowKey(r repoRow) string {
	forge, owner, _, name := rowParts(r)
	return strings.ToLower(forge + "\x00" + owner + "\x00" + name)
}

// rowParts returns the forge, owner, visibility, and name of a repo row, from
// its remote ref when present, otherwise its local repo.
func rowParts(r repoRow) (forge, owner, vis, name string) {
	if r.remote != nil {
		return r.remote.Forge, r.remote.Owner, r.remote.Visibility, r.remote.Name
	}
	return r.repo.Forge, r.repo.Owner, r.repo.Visibility, r.repo.Name
}

// disambiguateOwners rewrites labels for rows that base-render identically (same
// forge/visibility/name) but belong to different owners — e.g. freaxnx01 and
// acme both having ai-instructions, which otherwise both show as
// github/public/ai-instructions and look like the #124 duplicate. Such rows get
// the owner injected (github/<vis>/<owner>/<name>) so they're distinguishable;
// rows with a unique base label keep the clean owner-less form. Returns a new
// slice; the input rows are not mutated.
func disambiguateOwners(rows []repoRow) []repoRow {
	idxByLabel := map[string][]int{}
	ownersByLabel := map[string]map[string]bool{}
	for i, r := range rows {
		key := repoSortKey(r.label)
		idxByLabel[key] = append(idxByLabel[key], i)
		if ownersByLabel[key] == nil {
			ownersByLabel[key] = map[string]bool{}
		}
		_, owner, _, _ := rowParts(r)
		ownersByLabel[key][strings.ToLower(owner)] = true
	}
	out := append([]repoRow{}, rows...)
	for key, idxs := range idxByLabel {
		if len(ownersByLabel[key]) < 2 {
			continue // unique repo (or same owner): keep the clean label
		}
		for _, i := range idxs {
			out[i].label = ownerQualifiedLabel(out[i])
		}
	}
	return out
}

// ownerQualifiedLabel returns r's label with the owner injected for
// disambiguation. For github (whose base label drops the owner) it becomes
// github/<vis>/<owner>/<name>; other forges already carry the owner, so their
// existing label is returned unchanged. The remote "↓ " prefix is preserved.
func ownerQualifiedLabel(r repoRow) string {
	forge, owner, vis, name := rowParts(r)
	if forge != "github" {
		return r.label
	}
	if vis == "" {
		vis = "-"
	}
	display := "github/" + vis + "/" + owner + "/" + name
	if r.remote != nil {
		return "↓ " + display
	}
	return display
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
// dirtyInfo. The "## " header carries upstream state: a branch with no "..."
// upstream token has no remote tracking (noUpstream); the "[ahead N, behind M]"
// bracket gives the divergence counts (0 when absent). Non-header lines are
// changed files. behind is only accurate after a fetch freshens remote refs.
func parseDirtyStatus(out string) dirtyInfo {
	info := dirtyInfo{}
	for _, l := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if l == "" {
			continue
		}
		if strings.HasPrefix(l, "## ") {
			header := l[len("## "):]
			info.noUpstream = !strings.Contains(header, "...")
			if i := strings.IndexByte(header, '['); i >= 0 {
				if j := strings.IndexByte(header[i:], ']'); j >= 0 {
					bracket := header[i+1 : i+j]
					info.ahead = trackToken(bracket, "ahead ")
					info.behind = trackToken(bracket, "behind ")
				}
			}
			continue
		}
		info.modified++
	}
	info.clean = info.modified == 0
	return info
}

// trackToken reads the integer following key (e.g. "ahead ") inside a git status
// branch-header bracket such as "ahead 2, behind 3"; returns 0 when key absent.
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
