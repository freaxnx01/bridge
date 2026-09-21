package main

import (
	"path/filepath"
	"testing"
	"time"
)

func TestAgentYAMLOptsIntoAIMerge(t *testing.T) {
	tests := []struct {
		name string
		yaml string
		want bool
	}{
		{"wired for ai merge", "jobs:\n  agent:\n    with:\n      ai-review-ai-merge: true\n", true},
		{"wired for human merge only", "jobs:\n  agent:\n    with:\n      ai-review-human-merge: true\n", false},
		{"explicitly disabled", "    with:\n      ai-review-ai-merge: false\n", false},
		{"mentioned in a comment", "# ai-review-ai-merge: true\n", false},
		{"no with block at all", "on: issues\n", false},
		{"empty file", "", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := agentYAMLOptsIntoAIMerge([]byte(tc.yaml)); got != tc.want {
				t.Errorf("got %v want %v", got, tc.want)
			}
		})
	}
}

func TestLaneGateCacheFreshness(t *testing.T) {
	now := time.Date(2026, 9, 21, 12, 0, 0, 0, time.Local)
	c := laneGateCache{Repos: map[string]laneGateEntry{
		"fresh": {Autonomous: true, CheckedAt: now.Add(-time.Hour)},
		"stale": {Autonomous: true, CheckedAt: now.Add(-7 * time.Hour)},
	}}

	if v, ok := c.fresh("fresh", now); !ok || !v {
		t.Errorf("inside the TTL: v=%v ok=%v", v, ok)
	}
	if _, ok := c.fresh("stale", now); ok {
		t.Error("past the TTL the entry must be re-fetched")
	}
	if _, ok := c.fresh("unknown", now); ok {
		t.Error("an unseen repo is not cached")
	}
}

func TestLaneGateCacheRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lane-gate.json")
	now := time.Date(2026, 9, 21, 12, 0, 0, 0, time.Local)

	if err := saveLaneGateCache(path, laneGateCache{Repos: map[string]laneGateEntry{
		"game-tschau-sepp": {Autonomous: true, CheckedAt: now},
	}}); err != nil {
		t.Fatal(err)
	}

	back := loadLaneGateCache(path)
	if v, ok := back.fresh("game-tschau-sepp", now.Add(time.Minute)); !ok || !v {
		t.Errorf("after reload: v=%v ok=%v", v, ok)
	}
}

func TestLoadLaneGateCacheMissingFileIsEmpty(t *testing.T) {
	// A missing or corrupt cache is a cold start, never a failed tick.
	c := loadLaneGateCache(filepath.Join(t.TempDir(), "nope.json"))
	if len(c.Repos) != 0 {
		t.Errorf("%+v", c)
	}
}
