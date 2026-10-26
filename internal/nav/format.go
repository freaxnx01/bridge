package nav

import (
	"strings"
	"time"

	"github.com/freaxnx01/bridge/internal/core"
	"github.com/freaxnx01/bridge/internal/navdata"
	"github.com/freaxnx01/bridge/internal/worktree"
)

// lastAccessedFor renders a session's "last accessed" cell.
//
// tmux reports real timestamps, so the cell is the humanized age. Herdr reports
// none — `agent list`, `agent get` and `pane list` carry no time field — which
// leaves LastActivity at the zero value. Humanizing that produced the age of
// the zero time ("106751d 23h") in the UI, so a session with no timestamp shows
// its lifecycle state instead: the useful thing to know about a Herdr agent is
// whether it is working, blocked or idle, not when it was last touched.
func lastAccessedFor(sess core.Session, now time.Time) string {
	return navdata.LastAccessed(sess, now)
}

// humanLastAccessed renders d as at most two descending units (d/h/m).
// Sub-minute durations render as "0m".
func humanLastAccessed(d time.Duration) string { return navdata.HumanLastAccessed(d) }

// dedupRemoteRows drops remote ("↓") rows whose repository is already cloned
// locally, so a cloned repo isn't listed twice in the picker. Identity is
// forge+owner+name compared case-insensitively, because the local owner is
// derived from the on-disk path while the remote owner comes from the forge
// API and their casing can differ. See #124.
func dedupRemoteRows(local, remote []repoRow) []repoRow {
	return navdata.DedupRemote(local, remote)
}

// rowParts returns the forge, owner, visibility, and name of a repo row, from
// its remote ref when present, otherwise its local repo.
func rowParts(r repoRow) (forge, owner, vis, name string) {
	return navdata.Parts(r.Entry())
}

// disambiguateOwners rewrites labels for rows that base-render identically (same
// forge/visibility/name) but belong to different owners — e.g. freaxnx01 and
// acme both having ai-instructions, which otherwise both show as
// github/public/ai-instructions and look like the #124 duplicate. Such rows get
// the owner injected (github/<vis>/<owner>/<name>) so they're distinguishable;
// rows with a unique base label keep the clean owner-less form. Returns a new
// slice; the input rows are not mutated.
func disambiguateOwners(rows []repoRow) []repoRow {
	return navdata.DisambiguateOwners(rows, repoRow.withLabel)
}

// matchesAllTerms reports whether every term is a substring of labelLower. Both
// labelLower and terms must already be lower-cased. No terms is vacuously true.
func matchesAllTerms(labelLower string, terms []string) bool {
	return navdata.MatchesAllTerms(labelLower, terms)
}

// filterRepos keeps rows whose label contains every whitespace-separated term in
// q (case-insensitive, order-independent AND). An empty/whitespace-only query
// returns all rows; a single term behaves like a plain substring filter.
func filterRepos(rows []repoRow, q string) []repoRow { return navdata.FilterRows(rows, q) }

// matchesForge reports whether row r belongs to forge, using rowParts so remote
// (clone-on-select) rows match on their forge ref too. An empty forge ("" = All)
// matches every row.
func matchesForge(r repoRow, forge string) bool { return navdata.MatchesForge(r.Entry(), forge) }

// buildDashRows joins the repo's worktrees with the global sessions/slots into
// dashboard rows. A worktree gets a live session when a slot for this repo names
// it and that slot's tmux session is live. Rows with a session sort first by
// last-accessed DESC; session-less worktrees follow, name-sorted. dirtyState is
// loadPending (filled later by dirtyMsg).
func buildDashRows(repo core.Repo, baseBranch string, wts []worktree.Entry, slots []core.Slot, sessions []core.Session, now time.Time) []dashRow {
	return fromDashRows(navdata.BuildDashRows(repo, baseBranch, wts, slots, sessions, now))
}

// parseDirtyStatus parses `git status --porcelain=v1 --branch` output into a
// dirtyInfo. The "## " header carries upstream state: a branch with no "..."
// upstream token has no remote tracking (noUpstream); the "[ahead N, behind M]"
// bracket gives the divergence counts (0 when absent). Non-header lines are
// changed files. behind is only accurate after a fetch freshens remote refs.
func parseDirtyStatus(out string) dirtyInfo { return toDirtyInfo(navdata.ParseDirtyStatus(out)) }

// sortRepoRows orders repo rows ascending by label, case-insensitively and
// ignoring the remote "↓ " prefix so local and remote rows compare on the same
// key. Stable, so equal keys keep their input order.
func sortRepoRows(rows []repoRow) { navdata.SortRows(rows) }

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
