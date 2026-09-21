package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"path/filepath"
	"regexp"
	"time"

	"github.com/freaxnx01/bridge/internal/dispatch"
	"github.com/freaxnx01/bridge/internal/forge"
	"github.com/freaxnx01/bridge/internal/store"
)

// laneGateTTL bounds how stale an agent.yml reading may be. A 30-minute tick
// against a handful of repos would otherwise be a needless API storm, and a
// repo's merge policy changes on the scale of weeks.
const laneGateTTL = 6 * time.Hour

const agentWorkflowPath = ".github/workflows/agent.yml"

// aiMergeRE matches the one line check-ai-merge-gate.sh keys on. Matching the
// line rather than parsing YAML is deliberate: bridge carries no YAML
// dependency, and agreeing with the pipeline's own check matters more than
// understanding the document.
var aiMergeRE = regexp.MustCompile(`(?m)^[^\S\n]*ai-review-ai-merge:[^\S\n]*true[^\S\n]*$`)

// agentYAMLOptsIntoAIMerge reports whether a repo's agent.yml wires the
// ai-merge gate on.
func agentYAMLOptsIntoAIMerge(b []byte) bool { return aiMergeRE.Match(b) }

type laneGateEntry struct {
	Autonomous bool      `json:"autonomous"`
	CheckedAt  time.Time `json:"checked_at"`
}

// laneGateCache is the on-disk memo of each repo's gate reading.
type laneGateCache struct {
	Repos map[string]laneGateEntry `json:"repos"`
}

// fresh returns a cached reading and whether it is still inside the TTL.
func (c laneGateCache) fresh(repo string, now time.Time) (bool, bool) {
	e, ok := c.Repos[repo]
	if !ok || now.Sub(e.CheckedAt) >= laneGateTTL {
		return false, false
	}
	return e.Autonomous, true
}

func laneGatePath() string { return filepath.Join(cacheRoot(), "lane-gate.json") }

// loadLaneGateCache reads the memo. A missing or unreadable file is a cold
// start, not an error — the worst case is one extra fetch per repo.
func loadLaneGateCache(path string) laneGateCache {
	c := laneGateCache{Repos: map[string]laneGateEntry{}}
	b, err := store.ReadFile(path)
	if err != nil || len(b) == 0 {
		return c
	}
	if err := json.Unmarshal(b, &c); err != nil || c.Repos == nil {
		return laneGateCache{Repos: map[string]laneGateEntry{}}
	}
	return c
}

func saveLaneGateCache(path string, c laneGateCache) error {
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return store.AtomicWrite(path, b)
}

// resolveGate answers, for every repo some autonomous lane claims, whether its
// agent.yml opts into ai-merge. Repos no autonomous lane claims are never
// fetched — they cannot land in an autonomous lane anyway.
//
// Every failure path answers false: an unreadable policy must route work to a
// human, never into unattended merge.
func resolveGate(ctx context.Context, repos []repoInput, lanes []dispatch.Lane, now time.Time) dispatch.GateState {
	gate := dispatch.GateState{}
	cache := loadLaneGateCache(laneGatePath())
	changed := false

	for _, r := range repos {
		if !dispatch.AnyAutonomousLaneMatches(lanes, r.Name) {
			continue
		}
		if v, ok := cache.fresh(r.Name, now); ok {
			gate[r.Name] = v
			continue
		}
		gh, ok := clientFor("github").(*forge.GithubClient)
		if !ok || gh == nil {
			gate[r.Name] = false
			continue
		}
		content, _, found, err := gh.GetFile(ctx, r.Owner, r.Name, agentWorkflowPath)
		if err != nil {
			slog.Warn("dispatch: cannot read agent.yml — repo falls back to human review",
				"repo", r.Name, "error", err)
			gate[r.Name] = false
			continue
		}
		v := found && agentYAMLOptsIntoAIMerge(content)
		gate[r.Name] = v
		cache.Repos[r.Name] = laneGateEntry{Autonomous: v, CheckedAt: now}
		changed = true
	}

	if changed {
		if err := saveLaneGateCache(laneGatePath(), cache); err != nil {
			// A cache that will not persist costs one fetch per tick, nothing more.
			slog.Warn("dispatch: cannot write the lane gate cache", "error", err)
		}
	}
	return gate
}
