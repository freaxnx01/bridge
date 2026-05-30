package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// gitInitRepo creates a git repo at root/github/freaxnx01/public/<name> with
// one empty commit and returns its absolute path. Tests use it to exercise the
// real `git worktree` resolution path (writeFakeRepos makes plain dirs).
func gitInitRepo(t *testing.T, root, name string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	repo := filepath.Join(root, "github", "freaxnx01", "public", name)
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"init", "-q"},
		{"-c", "user.email=t@t", "-c", "user.name=t", "commit", "--allow-empty", "-q", "-m", "init"},
	} {
		c := exec.Command("git", args...)
		c.Dir = repo
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return repo
}

func gitAddWorktree(t *testing.T, repo, relDir, branch string) string {
	t.Helper()
	dir := filepath.Join(repo, relDir)
	c := exec.Command("git", "worktree", "add", "-b", branch, dir)
	c.Dir = repo
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("git worktree add: %v\n%s", err, out)
	}
	return dir
}

// TestPreflightOpenResolvesExistingWorktree reproduces the reported bug: a
// worktree created under .claude/worktrees/<wt> (Claude Code's location) must
// be found by `-w <wt>`, not missed in favor of the nonexistent
// .worktrees/<wt> path (which made tmux fall back to $HOME).
func TestPreflightOpenResolvesExistingWorktree(t *testing.T) {
	root := t.TempDir()
	repo := gitInitRepo(t, root, "myrepo")
	want := gitAddWorktree(t, repo, filepath.Join(".claude", "worktrees", "doc"), "worktree-doc")

	cache := t.TempDir()
	cmd := bridgeCmd("__preflight", "open", "myrepo", "-w", "doc")
	cmd.Env = append(envWithout("TMUX", "BRIDGE_DEFAULT_AGENT", "BRIDGE_DEFAULT_AGENT_ARGS", "BRIDGE_NO_SYNC"),
		"BRIDGE_REPOS_ROOT="+root,
		"XDG_CACHE_HOME="+cache,
		"BRIDGE_NO_SYNC=1",
	)
	out, _ := cmd.Output() // stdout only; the "created worktree" banner is stderr
	s := strings.TrimSpace(string(out))
	if s != "cd:"+want {
		t.Errorf("got %q, want %q", s, "cd:"+want)
	}
}

// TestPreflightOpenCreatesMissingWorktree verifies that `-w <wt>` for a repo
// without that worktree creates one under .worktrees/<wt> and lands there.
func TestPreflightOpenCreatesMissingWorktree(t *testing.T) {
	root := t.TempDir()
	repo := gitInitRepo(t, root, "myrepo")

	cache := t.TempDir()
	cmd := bridgeCmd("__preflight", "open", "myrepo", "-w", "fresh")
	cmd.Env = append(envWithout("TMUX", "BRIDGE_DEFAULT_AGENT", "BRIDGE_DEFAULT_AGENT_ARGS", "BRIDGE_NO_SYNC"),
		"BRIDGE_REPOS_ROOT="+root,
		"XDG_CACHE_HOME="+cache,
		"BRIDGE_NO_SYNC=1",
	)
	out, _ := cmd.Output() // stdout only; the "created worktree" banner is stderr
	s := strings.TrimSpace(string(out))
	want := "cd:" + filepath.Join(repo, ".worktrees", "fresh")
	if s != want {
		t.Errorf("got %q, want %q", s, want)
	}
	// The worktree must actually exist on disk now.
	if _, err := os.Stat(filepath.Join(repo, ".worktrees", "fresh")); err != nil {
		t.Errorf("worktree not created: %v", err)
	}
}
