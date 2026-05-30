package worktree

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

// fakeRunner records the git invocations and returns canned output/errors
// keyed by the first non -C subcommand.
type fakeRunner struct {
	listOut string
	listErr error
	addErr  error // error returned by the first `worktree add` call
	calls   [][]string
}

func (f *fakeRunner) Run(dir string, args ...string) (string, error) {
	f.calls = append(f.calls, append([]string{dir}, args...))
	switch {
	case len(args) >= 2 && args[0] == "worktree" && args[1] == "list":
		return f.listOut, f.listErr
	case len(args) >= 2 && args[0] == "worktree" && args[1] == "add":
		// Only the first add attempt fails when addErr is set, so the
		// fallback (checkout existing branch) can succeed.
		if f.addErr != nil {
			err := f.addErr
			f.addErr = nil
			return "", err
		}
		return "", nil
	}
	return "", nil
}

const porcelain = `worktree /repo/FlowHub-CAS-AISE
HEAD 01aab51
branch refs/heads/main

worktree /repo/FlowHub-CAS-AISE/.claude/worktrees/doc
HEAD 7329e8d
branch refs/heads/worktree-doc

worktree /repo/FlowHub-CAS-AISE/.claude/worktrees/upload
HEAD 8412c1a
branch refs/heads/worktree-upload
`

func TestResolveFindsExistingByPathBasename(t *testing.T) {
	r := &fakeRunner{listOut: porcelain}
	dir, created, err := Resolve(r, "/repo/FlowHub-CAS-AISE", "doc")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if created {
		t.Errorf("created=true, want false (worktree already exists)")
	}
	if dir != "/repo/FlowHub-CAS-AISE/.claude/worktrees/doc" {
		t.Errorf("dir = %q", dir)
	}
	// Must not have attempted to create anything.
	for _, c := range r.calls {
		if len(c) >= 3 && c[1] == "worktree" && c[2] == "add" {
			t.Errorf("unexpected worktree add: %v", c)
		}
	}
}

func TestResolveFindsExistingByWorktreeBranch(t *testing.T) {
	// Match a "worktree-<wt>" branch even when the path basename differs.
	pc := `worktree /repo/r
HEAD aaa
branch refs/heads/main

worktree /repo/r/wt/feat
HEAD bbb
branch refs/heads/worktree-feature-x
`
	r := &fakeRunner{listOut: pc}
	dir, created, err := Resolve(r, "/repo/r", "feature-x")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if created || dir != "/repo/r/wt/feat" {
		t.Errorf("dir=%q created=%v", dir, created)
	}
}

func TestResolveCreatesWhenMissing(t *testing.T) {
	r := &fakeRunner{listOut: porcelain}
	dir, created, err := Resolve(r, "/repo/FlowHub-CAS-AISE", "newone")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !created {
		t.Errorf("created=false, want true")
	}
	want := filepath.Join("/repo/FlowHub-CAS-AISE", ".worktrees", "newone")
	if dir != want {
		t.Errorf("dir = %q, want %q", dir, want)
	}
	// Must have issued a `worktree add ... -b worktree-newone <path>`.
	var added bool
	for _, c := range r.calls {
		joined := strings.Join(c, " ")
		if strings.Contains(joined, "worktree add") {
			added = true
			if !strings.Contains(joined, "-b worktree-newone") {
				t.Errorf("add without expected branch: %v", c)
			}
			if !strings.Contains(joined, want) {
				t.Errorf("add at wrong path: %v", c)
			}
		}
	}
	if !added {
		t.Errorf("no worktree add issued; calls=%v", r.calls)
	}
}

func TestResolveCreateFallsBackToExistingBranch(t *testing.T) {
	// `git worktree add -b worktree-x` fails because the branch already
	// exists; Resolve retries checking out the existing branch.
	r := &fakeRunner{listOut: porcelain, addErr: errors.New("fatal: a branch named 'worktree-x' already exists")}
	dir, created, err := Resolve(r, "/repo/FlowHub-CAS-AISE", "x")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !created {
		t.Errorf("created=false, want true")
	}
	want := filepath.Join("/repo/FlowHub-CAS-AISE", ".worktrees", "x")
	if dir != want {
		t.Errorf("dir=%q want %q", dir, want)
	}
	// Two add attempts: first with -b (failed), second without -b.
	var addCalls []string
	for _, c := range r.calls {
		j := strings.Join(c, " ")
		if strings.Contains(j, "worktree add") {
			addCalls = append(addCalls, j)
		}
	}
	if len(addCalls) != 2 {
		t.Fatalf("want 2 add attempts, got %d: %v", len(addCalls), addCalls)
	}
	if strings.Contains(addCalls[1], "-b ") {
		t.Errorf("fallback add should not use -b: %q", addCalls[1])
	}
	if !strings.Contains(addCalls[1], "worktree-x") {
		t.Errorf("fallback add should check out existing branch worktree-x: %q", addCalls[1])
	}
}

func TestResolveNotGitRepoReturnsError(t *testing.T) {
	r := &fakeRunner{listErr: errors.New("fatal: not a git repository")}
	_, _, err := Resolve(r, "/tmp/plain-dir", "doc")
	if err == nil {
		t.Fatalf("want error for non-git repo, got nil")
	}
}
