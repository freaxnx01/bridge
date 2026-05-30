package worktree

import (
	"os/exec"
	"path/filepath"
	"testing"
)

// TestExecRunnerCreateThenResolve drives a real git repo end to end: the
// first Resolve creates the worktree, the second finds it.
func TestExecRunnerCreateThenResolve(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	repo := t.TempDir()
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

	r := ExecRunner{}
	dir, created, err := Resolve(r, repo, "doc")
	if err != nil {
		t.Fatalf("first resolve: %v", err)
	}
	if !created {
		t.Errorf("first resolve created=false, want true")
	}
	want := filepath.Join(repo, ".worktrees", "doc")
	if dir != want {
		t.Errorf("dir=%q want %q", dir, want)
	}

	dir2, created2, err := Resolve(r, repo, "doc")
	if err != nil {
		t.Fatalf("second resolve: %v", err)
	}
	if created2 {
		t.Errorf("second resolve created=true, want false (already exists)")
	}
	if dir2 != want {
		t.Errorf("second dir=%q want %q", dir2, want)
	}
}

func TestExecRunnerNonGitDir(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	_, _, err := Resolve(ExecRunner{}, t.TempDir(), "doc")
	if err == nil {
		t.Fatalf("want error for non-git dir")
	}
}
