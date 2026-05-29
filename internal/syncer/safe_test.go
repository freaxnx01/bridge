package syncer

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func key(dir string, args ...string) string {
	return dir + ":git " + joinArgs(args)
}

func TestSafePullCleanFastForward(t *testing.T) {
	dir := "/r/x"
	r := &fakeRunner{
		outputs: map[string]string{
			key(dir, "symbolic-ref", "-q", "HEAD"):                              "refs/heads/main\n",
			key(dir, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{u}"): "origin/main\n",
			key(dir, "status", "--porcelain"):                                   "",
			key(dir, "rev-list", "--count", "@{u}..HEAD"):                       "0\n",
			key(dir, "rev-list", "--count", "HEAD..@{u}"):                       "3\n",
		},
	}
	s := &Syncer{Runner: r}
	res := s.SafePull(context.Background(), dir)
	if res.Skipped != "" {
		t.Errorf("expected pull to run, got Skipped=%q", res.Skipped)
	}
	// Both fetch and pull should have been invoked.
	var sawFetch, sawPull bool
	for _, c := range r.calls {
		if strings.Contains(c, "fetch") {
			sawFetch = true
		}
		if strings.Contains(c, "pull --ff-only") {
			sawPull = true
		}
	}
	if !sawFetch || !sawPull {
		t.Errorf("expected fetch+pull, got calls: %v", r.calls)
	}
}

func TestSafePullAlreadyUpToDateSkipsPull(t *testing.T) {
	dir := "/r/x"
	r := &fakeRunner{
		outputs: map[string]string{
			key(dir, "symbolic-ref", "-q", "HEAD"):                              "refs/heads/main\n",
			key(dir, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{u}"): "origin/main\n",
			key(dir, "status", "--porcelain"):                                   "",
			key(dir, "rev-list", "--count", "@{u}..HEAD"):                       "0\n",
			key(dir, "rev-list", "--count", "HEAD..@{u}"):                       "0\n",
		},
	}
	s := &Syncer{Runner: r}
	res := s.SafePull(context.Background(), dir)
	if res.Skipped != "" {
		t.Errorf("up-to-date should be empty skip, got %q", res.Skipped)
	}
	for _, c := range r.calls {
		if strings.Contains(c, "pull") {
			t.Errorf("up-to-date should skip the pull, got call: %s", c)
		}
	}
}

func TestSafePullDetachedHead(t *testing.T) {
	dir := "/r/x"
	r := &fakeRunner{
		fail: map[string]error{
			key(dir, "symbolic-ref", "-q", "HEAD"): errors.New("exit 1"),
		},
	}
	s := &Syncer{Runner: r}
	if got := s.SafePull(context.Background(), dir); got.Skipped != SkipDetached {
		t.Errorf("got %q, want %q", got.Skipped, SkipDetached)
	}
}

func TestSafePullNoUpstream(t *testing.T) {
	dir := "/r/x"
	r := &fakeRunner{
		outputs: map[string]string{
			key(dir, "symbolic-ref", "-q", "HEAD"): "refs/heads/topic\n",
		},
		fail: map[string]error{
			key(dir, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{u}"): errors.New("no upstream"),
		},
	}
	s := &Syncer{Runner: r}
	if got := s.SafePull(context.Background(), dir); got.Skipped != SkipNoUpstream {
		t.Errorf("got %q, want %q", got.Skipped, SkipNoUpstream)
	}
}

func TestSafePullDirty(t *testing.T) {
	dir := "/r/x"
	r := &fakeRunner{
		outputs: map[string]string{
			key(dir, "symbolic-ref", "-q", "HEAD"):                              "refs/heads/main\n",
			key(dir, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{u}"): "origin/main\n",
			key(dir, "status", "--porcelain"):                                   " M file.go\n",
		},
	}
	s := &Syncer{Runner: r}
	if got := s.SafePull(context.Background(), dir); got.Skipped != SkipDirty {
		t.Errorf("got %q, want %q", got.Skipped, SkipDirty)
	}
}

func TestSafePullDiverged(t *testing.T) {
	dir := "/r/x"
	r := &fakeRunner{
		outputs: map[string]string{
			key(dir, "symbolic-ref", "-q", "HEAD"):                              "refs/heads/main\n",
			key(dir, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{u}"): "origin/main\n",
			key(dir, "status", "--porcelain"):                                   "",
			key(dir, "rev-list", "--count", "@{u}..HEAD"):                       "2\n",
			key(dir, "rev-list", "--count", "HEAD..@{u}"):                       "3\n",
		},
	}
	s := &Syncer{Runner: r}
	if got := s.SafePull(context.Background(), dir); got.Skipped != SkipDiverged {
		t.Errorf("got %q, want %q", got.Skipped, SkipDiverged)
	}
	for _, c := range r.calls {
		if strings.Contains(c, "pull --ff-only") {
			t.Errorf("diverged repo must not pull, got call: %s", c)
		}
	}
}

func TestSafePullFetchFailure(t *testing.T) {
	dir := "/r/x"
	r := &fakeRunner{
		outputs: map[string]string{
			key(dir, "symbolic-ref", "-q", "HEAD"):                              "refs/heads/main\n",
			key(dir, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{u}"): "origin/main\n",
			key(dir, "status", "--porcelain"):                                   "",
		},
		fail: map[string]error{
			key(dir, "fetch", "--quiet"): errors.New("network down"),
		},
	}
	s := &Syncer{Runner: r}
	if got := s.SafePull(context.Background(), dir); got.Skipped != SkipFetchFail {
		t.Errorf("got %q, want %q", got.Skipped, SkipFetchFail)
	}
}

func TestSafePullDefaultsRunner(t *testing.T) {
	// Constructing without Runner should not panic; ExecRunner gets used by
	// default. We pass a non-existent dir so git fails fast and we exit on
	// the symbolic-ref check.
	s := &Syncer{}
	_ = s.SafePull(context.Background(), "/nonexistent/path/for/test")
	if s.Runner == nil {
		t.Errorf("Runner should be defaulted to ExecRunner")
	}
}
