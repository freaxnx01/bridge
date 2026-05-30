package worktree

import (
	"os/exec"
	"strings"
)

// ExecRunner runs the real git binary with `-C <dir>`. On failure it returns
// an error whose message carries git's stderr so callers can surface why a
// worktree could not be created.
type ExecRunner struct{}

func (ExecRunner) Run(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", &gitError{err: err, out: strings.TrimSpace(string(out))}
	}
	return string(out), nil
}

type gitError struct {
	err error
	out string
}

func (e *gitError) Error() string {
	if e.out != "" {
		return e.out
	}
	return e.err.Error()
}

func (e *gitError) Unwrap() error { return e.err }
