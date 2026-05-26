// Package syncer drives `git fetch && git pull --ff-only` across a set of repos.
package syncer

import (
	"context"
	"os/exec"

	"github.com/freaxnx01/bridge/internal/core"
)

// Runner runs a command in a directory.
type Runner interface {
	Run(ctx context.Context, dir, name string, args ...string) error
}

type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, dir, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	return cmd.Run()
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
