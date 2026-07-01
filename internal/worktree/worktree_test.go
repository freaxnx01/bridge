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

func TestResolveDoesNotMatchMainWorktree(t *testing.T) {
	// `-w main` (or `-w <reponame>`) must not resolve to the primary working
	// tree — that would defeat isolation. The main worktree is skipped, so a
	// dedicated worktree is created instead.
	pc := `worktree /repo/FlowHub-CAS-AISE
HEAD 01aab51
branch refs/heads/main
`
	for _, wt := range []string{"main", "FlowHub-CAS-AISE"} {
		r := &fakeRunner{listOut: pc}
		dir, created, err := Resolve(r, "/repo/FlowHub-CAS-AISE", wt)
		if err != nil {
			t.Fatalf("-w %s: unexpected err: %v", wt, err)
		}
		if dir == "/repo/FlowHub-CAS-AISE" {
			t.Errorf("-w %s resolved to the main worktree (repo root); want a dedicated worktree", wt)
		}
		if !created {
			t.Errorf("-w %s: created=false, want true", wt)
		}
		if want := filepath.Join("/repo/FlowHub-CAS-AISE", ".worktrees", wt); dir != want {
			t.Errorf("-w %s: dir=%q want %q", wt, dir, want)
		}
	}
}

func TestResolveCreateReturnsWorktreeExistsError(t *testing.T) {
	// The target dir already exists but is not a registered worktree: git's
	// `add -b` fails with "'<path>' already exists" (no "branch"). Resolve must
	// return a typed *WorktreeExistsError and NOT retry without -b.
	r := &fakeRunner{listOut: porcelain, addErr: errors.New("fatal: '/repo/FlowHub-CAS-AISE/.worktrees/x' already exists")}
	_, _, err := Resolve(r, "/repo/FlowHub-CAS-AISE", "x")
	if err == nil {
		t.Fatalf("want error, got nil")
	}
	var wex *WorktreeExistsError
	if !errors.As(err, &wex) {
		t.Fatalf("want *WorktreeExistsError, got %T: %v", err, err)
	}
	if wex.Name != "x" {
		t.Errorf("Name = %q, want %q", wex.Name, "x")
	}
	if want := filepath.Join("/repo/FlowHub-CAS-AISE", ".worktrees", "x"); wex.Path != want {
		t.Errorf("Path = %q, want %q", wex.Path, want)
	}
	var addCalls int
	for _, c := range r.calls {
		if strings.Contains(strings.Join(c, " "), "worktree add") {
			addCalls++
		}
	}
	if addCalls != 1 {
		t.Errorf("want exactly 1 add attempt (no blind retry), got %d", addCalls)
	}
}

func TestResolveCreateDoesNotRetryOnUnrelatedError(t *testing.T) {
	// The first `worktree add -b` fails for a reason unrelated to an existing
	// branch (here: must be run in a work tree). Resolve must NOT blindly
	// retry without -b (which would mask the real cause with a confusing
	// "invalid reference" error); it returns the original error after one add.
	r := &fakeRunner{listOut: porcelain, addErr: errors.New("fatal: this operation must be run in a work tree")}
	_, _, err := Resolve(r, "/repo/FlowHub-CAS-AISE", "x")
	if err == nil {
		t.Fatalf("want error, got nil")
	}
	if !strings.Contains(err.Error(), "must be run in a work tree") {
		t.Errorf("error should surface git's original message, got: %v", err)
	}
	var wex *WorktreeExistsError
	if errors.As(err, &wex) {
		t.Errorf("unrelated failure must not be classified as WorktreeExistsError")
	}
	var addCalls int
	for _, c := range r.calls {
		if strings.Contains(strings.Join(c, " "), "worktree add") {
			addCalls++
		}
	}
	if addCalls != 1 {
		t.Errorf("want exactly 1 add attempt (no blind retry), got %d", addCalls)
	}
}

func TestResolveCreateNameContainsBranchStillClassifiesTargetExists(t *testing.T) {
	// A worktree name containing "branch" must not be misread as a branch-exists
	// failure: git's target-exists error echoes the full path, so the bare token
	// "branch" would misclassify. Resolve must still return *WorktreeExistsError
	// and must NOT retry.
	r := &fakeRunner{listOut: porcelain, addErr: errors.New("fatal: '/repo/FlowHub-CAS-AISE/.worktrees/release-branch' already exists")}
	_, _, err := Resolve(r, "/repo/FlowHub-CAS-AISE", "release-branch")
	var wex *WorktreeExistsError
	if !errors.As(err, &wex) {
		t.Fatalf("want *WorktreeExistsError, got %T: %v", err, err)
	}
	if wex.Name != "release-branch" {
		t.Errorf("Name = %q, want %q", wex.Name, "release-branch")
	}
	var addCalls int
	for _, c := range r.calls {
		if strings.Contains(strings.Join(c, " "), "worktree add") {
			addCalls++
		}
	}
	if addCalls != 1 {
		t.Errorf("want exactly 1 add attempt (no blind retry), got %d", addCalls)
	}
}

func TestResolveNotGitRepoReturnsError(t *testing.T) {
	r := &fakeRunner{listErr: errors.New("fatal: not a git repository")}
	_, _, err := Resolve(r, "/tmp/plain-dir", "doc")
	if err == nil {
		t.Fatalf("want error for non-git repo, got nil")
	}
}

func TestList_ParsesPorcelain_ExcludesPrimary(t *testing.T) {
	out := "worktree /repo\nbranch refs/heads/main\n\n" +
		"worktree /repo/.worktrees/fix\nbranch refs/heads/worktree-fix\n\n"
	r := &fakeRunner{listOut: out}
	got, err := List(r, "/repo")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d entries, want 1 (primary excluded)", len(got))
	}
	if got[0].Path != "/repo/.worktrees/fix" || got[0].Branch != "worktree-fix" {
		t.Errorf("entry = %+v, want path=/repo/.worktrees/fix branch=worktree-fix", got[0])
	}
}
