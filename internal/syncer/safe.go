package syncer

import (
	"context"
	"strconv"
	"strings"
)

// SkipReason is why a pre-launch SafePull declined to pull. The empty
// string means the pull ran (or there was simply nothing to pull).
type SkipReason string

const (
	SkipDetached   SkipReason = "detached HEAD"
	SkipNoUpstream SkipReason = "no upstream"
	SkipDirty      SkipReason = "dirty working tree"
	SkipDiverged   SkipReason = "diverged from upstream"
	SkipFetchFail  SkipReason = "fetch failed"
)

// SafePullResult reports the outcome of a single-repo pre-launch sync.
type SafePullResult struct {
	// Skipped is "" when the pull ran successfully (including the
	// already-up-to-date case). Non-empty values are skip reasons safe to
	// print as a one-line banner.
	Skipped SkipReason
}

// SafePull does a best-effort `git fetch && git pull --ff-only` for the
// repo at dir's current branch (issue #90 / gap G7). It bails early with
// a skip reason whenever a real pull would be unsafe: detached HEAD,
// missing upstream, dirty working tree, diverged history, or a failing
// fetch. Never returns an error — the launch must proceed regardless.
func (s *Syncer) SafePull(ctx context.Context, dir string) SafePullResult {
	if s.Runner == nil {
		s.Runner = ExecRunner{}
	}
	or, ok := s.Runner.(OutputRunner)
	if !ok {
		// Without stdout access we can't inspect git state; safest is to
		// skip rather than blindly pull.
		return SafePullResult{Skipped: SkipNoUpstream}
	}

	// 1. Detached HEAD has no branch to fast-forward.
	if _, err := or.Output(ctx, dir, "git", "symbolic-ref", "-q", "HEAD"); err != nil {
		return SafePullResult{Skipped: SkipDetached}
	}

	// 2. No upstream → nothing to pull from.
	if _, err := or.Output(ctx, dir, "git", "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{u}"); err != nil {
		return SafePullResult{Skipped: SkipNoUpstream}
	}

	// 3. Dirty working tree → don't risk a half-merged state.
	porc, err := or.Output(ctx, dir, "git", "status", "--porcelain")
	if err != nil {
		return SafePullResult{Skipped: SkipFetchFail}
	}
	if strings.TrimSpace(porc) != "" {
		return SafePullResult{Skipped: SkipDirty}
	}

	// 4. Fetch the current branch's upstream. `--quiet` keeps launch output clean.
	if err := s.Runner.Run(ctx, dir, "git", "fetch", "--quiet"); err != nil {
		return SafePullResult{Skipped: SkipFetchFail}
	}

	// 5. Diverged? ahead>0 AND behind>0 means a non-ff-only pull would be needed.
	ahead, _ := countRevs(ctx, or, dir, "@{u}..HEAD")
	behind, _ := countRevs(ctx, or, dir, "HEAD..@{u}")
	if ahead > 0 && behind > 0 {
		return SafePullResult{Skipped: SkipDiverged}
	}
	if behind == 0 {
		// Already up to date — no need to actually run pull.
		return SafePullResult{}
	}

	// 6. Fast-forward pull. Errors are swallowed: launch must proceed.
	_ = s.Runner.Run(ctx, dir, "git", "pull", "--ff-only", "--quiet")
	return SafePullResult{}
}

func countRevs(ctx context.Context, or OutputRunner, dir, rng string) (int, error) {
	raw, err := or.Output(ctx, dir, "git", "rev-list", "--count", rng)
	if err != nil {
		return 0, err
	}
	n, perr := strconv.Atoi(strings.TrimSpace(raw))
	if perr != nil {
		return 0, perr
	}
	return n, nil
}
