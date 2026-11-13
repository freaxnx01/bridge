package main

import (
	"context"
	"errors"
	"path/filepath"
	"time"

	"github.com/freaxnx01/bridge/internal/dispatch"
	"github.com/freaxnx01/bridge/internal/dispatchrun"
	"github.com/freaxnx01/bridge/internal/forge"
)

const agentWorkflowPath = ".github/workflows/agent.yml"

func laneGatePath() string { return filepath.Join(cacheRoot(), "lane-gate.json") }

// resolveGate answers, for every repo some autonomous lane claims, whether its
// agent.yml opts into ai-merge, reading through the on-disk memo.
func resolveGate(ctx context.Context, repos []dispatch.RepoInput, lanes []dispatch.Lane, now time.Time) dispatch.GateState {
	return dispatchrun.ResolveGate(ctx, repos, lanes, now, fetchAgentWorkflow, laneGatePath())
}

// fetchAgentWorkflow is the real fetcher. An unresolvable client is an error
// rather than "not found": both fall back to human review, but only one of
// them should ever be cached.
func fetchAgentWorkflow(ctx context.Context, owner, repo string) ([]byte, bool, error) {
	gh, ok := clientFor("github").(*forge.GithubClient)
	if !ok || gh == nil {
		return nil, false, errors.New("no GitHub client available")
	}
	content, _, found, err := gh.GetFile(ctx, owner, repo, agentWorkflowPath)
	return content, found, err
}
