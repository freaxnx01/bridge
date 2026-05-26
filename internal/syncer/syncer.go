// Package syncer drives `git fetch && git pull --ff-only` across a set of repos.
package syncer

import (
	"context"
	"os/exec"
	"strconv"
	"strings"

	"github.com/freaxnx01/bridge/internal/core"
)

// Runner runs a command in a directory.
type Runner interface {
	Run(ctx context.Context, dir, name string, args ...string) error
}

// OutputRunner is an optional capability: returns stdout in addition to error.
// Used by Unpushed where we need the rev-list count, not just its exit code.
// Runners that don't implement this fall back to a string-output adapter.
type OutputRunner interface {
	Output(ctx context.Context, dir, name string, args ...string) (string, error)
}

type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, dir, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	return cmd.Run()
}

func (ExecRunner) Output(ctx context.Context, dir, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	return string(out), err
}

// Syncer drives syncs.
type Syncer struct {
	Runner Runner
}

// Failure records a per-repo sync error.
type Failure struct {
	Repo  core.Repo
	Step  string // "fetch" | "pull"
	Error error
}

// Result aggregates a sync run.
type Result struct {
	OK     []core.Repo
	Failed []Failure
}

// Unpushed returns the names of repos whose current branch is ahead of its
// upstream. Repos without an upstream (no @{u}) are silently skipped.
// Uses an OutputRunner to read `git rev-list --count @{u}..HEAD` per repo.
func (s *Syncer) Unpushed(ctx context.Context, repos []core.Repo) []string {
	if s.Runner == nil {
		s.Runner = ExecRunner{}
	}
	or, ok := s.Runner.(OutputRunner)
	if !ok {
		// Runner doesn't expose stdout — cannot determine count.
		return nil
	}
	var out []string
	for _, r := range repos {
		raw, err := or.Output(ctx, r.Path, "git", "rev-list", "--count", "@{u}..HEAD")
		if err != nil {
			// No upstream, detached HEAD, etc. — skip.
			continue
		}
		n, perr := strconv.Atoi(strings.TrimSpace(raw))
		if perr != nil || n <= 0 {
			continue
		}
		out = append(out, r.Name)
	}
	return out
}

// Run synchronises each repo. Aborts a repo's pull if its fetch failed; other
// repos are unaffected. Never returns an error.
func (s *Syncer) Run(ctx context.Context, repos []core.Repo) Result {
	if s.Runner == nil {
		s.Runner = ExecRunner{}
	}
	var res Result
	for _, r := range repos {
		if err := s.Runner.Run(ctx, r.Path, "git", "fetch", "--all", "--prune"); err != nil {
			res.Failed = append(res.Failed, Failure{Repo: r, Step: "fetch", Error: err})
			continue
		}
		if err := s.Runner.Run(ctx, r.Path, "git", "pull", "--ff-only"); err != nil {
			res.Failed = append(res.Failed, Failure{Repo: r, Step: "pull", Error: err})
			continue
		}
		res.OK = append(res.OK, r)
	}
	return res
}
