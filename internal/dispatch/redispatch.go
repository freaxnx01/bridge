package dispatch

import (
	"context"
	"fmt"
)

// labelSwapper is the forge surface the swap needs (forge.GithubClient has both).
type labelSwapper interface {
	AddLabels(ctx context.Context, owner, repo string, number int, labels []string) ([]string, error)
	RemoveLabel(ctx context.Context, owner, repo string, number int, label string) error
}

// RedispatchError names the step of the label swap that failed, so the caller
// can tell a swap that never fired the pipeline from one that already did.
type RedispatchError struct {
	Step int // 1..4, in the order Redispatch performs them
	Op   string
	Err  error
}

func (e *RedispatchError) Error() string {
	return fmt.Sprintf("redispatch step %d (%s): %v", e.Step, e.Op, e.Err)
}

func (e *RedispatchError) Unwrap() error { return e.Err }

// Redispatch re-triggers the pipeline for an issue that already carries
// ai-implement. The label has to be removed and added again because the
// pipeline starts only on `issues: labeled` — re-adding a label that is still
// present is a no-op on GitHub and fires no run.
//
// ai:retrying brackets the swap: left behind by a failure, it marks the issue
// as half-swapped and keeps both dispatch passes off it until a person clears
// it. The status label (ai:failed) is never touched — the next run report
// replaces it.
func Redispatch(ctx context.Context, gh labelSwapper, owner, repo string, number int, labels []string) error {
	steps := []struct {
		op   string
		call func() error
	}{
		{"add " + LabelRetrying, func() error {
			_, err := gh.AddLabels(ctx, owner, repo, number, []string{LabelRetrying})
			return err
		}},
		{"remove " + LabelAIImplement, func() error {
			return gh.RemoveLabel(ctx, owner, repo, number, LabelAIImplement)
		}},
		{"add labels", func() error {
			_, err := gh.AddLabels(ctx, owner, repo, number, labels)
			return err
		}},
		{"remove " + LabelRetrying, func() error {
			return gh.RemoveLabel(ctx, owner, repo, number, LabelRetrying)
		}},
	}
	for i, s := range steps {
		if err := s.call(); err != nil {
			return &RedispatchError{Step: i + 1, Op: s.op, Err: err}
		}
	}
	return nil
}
