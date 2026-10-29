package main

import (
	"context"
	"os"

	"github.com/freaxnx01/bridge/internal/launch"
)

// The launch logic lives in internal/launch (shared with `bridge bot`). These
// cmd-layer names stay so preflight, `bridge launch` and their tests read
// unchanged.
type (
	// launchTarget is everything needed to start (or reattach) a session.
	launchTarget = launch.Target
	// errRepoLookup marks unknown/ambiguous repo names; callers print it and exit 2.
	errRepoLookup = launch.RepoLookupError
	// errAgentLookup marks an unknown/invalid --agent; callers print it and exit 2.
	errAgentLookup = launch.AgentLookupError
)

// launchDeps wires internal/launch to this binary's discovery, cache dir,
// session listing (BRIDGE_TMUX_FIXTURE-aware) and stderr notes.
func launchDeps() launch.Deps {
	return launch.Deps{
		Repos:     reposWithMeta,
		CacheRoot: cacheRoot(),
		Live:      loadSessions,
		Getenv:    os.Getenv,
		Stderr:    os.Stderr,
	}
}

func resolveLaunchTarget(name, worktree, agentName string) (launchTarget, error) {
	return launch.ResolveTarget(context.Background(), launchDeps(), name, worktree, agentName)
}

// finalizeLaunch applies the shared launch side effects (label, relabel hook,
// pre-launch sync, slot upsert); see launch.Finalize.
func finalizeLaunch(t *launchTarget, noSync bool) {
	launch.Finalize(context.Background(), launchDeps(), t, noSync)
}
