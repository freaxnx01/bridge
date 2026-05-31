// Package worktree resolves the directory a `bridge open -w <wt>` launch
// should land in. It consults `git worktree list --porcelain` so an existing
// worktree is found wherever it lives (`.claude/worktrees/`, `.worktrees/`, a
// custom path), and creates one via `git worktree add` when none matches.
package worktree

import (
	"fmt"
	"path/filepath"
	"strings"
)

// Runner executes git in a working directory and returns stdout. The dir is
// passed so the implementation can use `git -C <dir>`; tests inject a fake.
type Runner interface {
	Run(dir string, args ...string) (string, error)
}

// entry is one parsed block of `git worktree list --porcelain`.
type entry struct {
	path   string
	branch string // short branch name, "" when detached
}

// Entry is a worktree of a repo: its checkout path and short branch name
// ("" when detached). The primary working tree is excluded by List.
type Entry struct {
	Path   string
	Branch string
}

// List returns the non-primary worktrees of the repo at repoPath, parsed from
// `git worktree list --porcelain`. The primary working tree (repoPath itself)
// is excluded — nav lists isolated worktrees, not the main checkout.
func List(r Runner, repoPath string) ([]Entry, error) {
	out, err := r.Run(repoPath, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, fmt.Errorf("git worktree list: %w", err)
	}
	main := filepath.Clean(repoPath)
	var entries []Entry
	for _, e := range parsePorcelain(out) {
		if filepath.Clean(e.path) == main {
			continue
		}
		entries = append(entries, Entry{Path: e.path, Branch: e.branch})
	}
	return entries, nil
}

// Resolve returns the directory to launch in for worktree wt of the repo at
// repoPath. It returns created=true when it had to make the worktree. A
// non-nil error means repoPath is not a usable git repo (caller may fall back
// to a plain path convention).
func Resolve(r Runner, repoPath, wt string) (dir string, created bool, err error) {
	out, lerr := r.Run(repoPath, "worktree", "list", "--porcelain")
	if lerr != nil {
		return "", false, fmt.Errorf("git worktree list: %w", lerr)
	}
	mainPath := filepath.Clean(repoPath)
	for _, e := range parsePorcelain(out) {
		// Never resolve to the primary working tree — `-w` exists to isolate
		// from it, so `-w main` / `-w <reponame>` must create a dedicated
		// worktree rather than handing back the repo root.
		if filepath.Clean(e.path) == mainPath {
			continue
		}
		if matches(e, wt) {
			return e.path, false, nil
		}
	}
	// None found — create one under the documented bridge convention.
	target := filepath.Join(repoPath, ".worktrees", wt)
	branch := "worktree-" + wt
	if _, aerr := r.Run(repoPath, "worktree", "add", "-b", branch, target); aerr != nil {
		// Only retry when the branch already exists (a dangling branch from a
		// removed worktree): check it out into the new worktree without -b.
		// Any other failure (target dir exists, no commits yet, bad name) is
		// returned as-is so its real cause isn't masked.
		if !branchExistsErr(aerr) {
			return "", false, fmt.Errorf("git worktree add: %w", aerr)
		}
		if _, aerr2 := r.Run(repoPath, "worktree", "add", target, branch); aerr2 != nil {
			return "", false, fmt.Errorf("git worktree add: %w", aerr2)
		}
	}
	return target, true, nil
}

// branchExistsErr reports whether a `git worktree add -b <branch>` failure was
// caused by the branch already existing — git says "a branch named '<x>'
// already exists". The "branch" qualifier distinguishes it from a target-path
// "'<path>' already exists" failure, which must not trigger the no-`-b` retry.
func branchExistsErr(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "already exists") && strings.Contains(msg, "branch")
}

// matches reports whether worktree entry e is the one named wt. A worktree is
// addressed by its path basename (`.../doc` → "doc") or by a "worktree-<wt>"
// branch (bridge/Claude Code naming) or by an exact branch name.
func matches(e entry, wt string) bool {
	if filepath.Base(e.path) == wt {
		return true
	}
	if e.branch == wt || e.branch == "worktree-"+wt {
		return true
	}
	return false
}

func parsePorcelain(out string) []entry {
	var entries []entry
	var cur *entry
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(line, "worktree "):
			entries = append(entries, entry{path: strings.TrimPrefix(line, "worktree ")})
			cur = &entries[len(entries)-1]
		case strings.HasPrefix(line, "branch ") && cur != nil:
			cur.branch = strings.TrimPrefix(strings.TrimPrefix(line, "branch "), "refs/heads/")
		}
	}
	return entries
}
