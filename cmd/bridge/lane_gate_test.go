package main

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/freaxnx01/bridge/internal/dispatch"
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

// resolveGateWith is the one network-touching path in lane resolution, and the
// "never dispatch into unattended merge" guarantee rests on every one of its
// failure modes answering false.
func TestResolveGateWithFailsClosed(t *testing.T) {
	now := time.Date(2026, 9, 21, 12, 0, 0, 0, time.Local)
	lanes := []dispatch.Lane{
		{Name: "auto", Repos: []string{"game-*"}, Autonomous: true},
		{Name: "hitl", Repos: []string{"*"}},
	}
	repos := []repoInput{
		{Forge: "github", Owner: "freaxnx01", Name: "game-wired"},
		{Forge: "github", Owner: "freaxnx01", Name: "game-unwired"},
		{Forge: "github", Owner: "freaxnx01", Name: "game-absent"},
		{Forge: "github", Owner: "freaxnx01", Name: "game-broken"},
		{Forge: "github", Owner: "freaxnx01", Name: "bridge"},
	}

	var fetched []string
	fetch := func(_ context.Context, _, repo string) ([]byte, bool, error) {
		fetched = append(fetched, repo)
		switch repo {
		case "game-wired":
			return []byte("    with:\n      ai-review-ai-merge: true\n"), true, nil
		case "game-unwired":
			return []byte("    with:\n      ai-review-human-merge: true\n"), true, nil
		case "game-absent":
			return nil, false, nil
		default:
			return nil, false, errors.New("dial tcp: i/o timeout")
		}
	}

	path := filepath.Join(t.TempDir(), "lane-gate.json")
	gate := resolveGateWith(context.Background(), repos, lanes, now, fetch, path)

	want := map[string]bool{
		"game-wired":   true,
		"game-unwired": false,
		"game-absent":  false,
		"game-broken":  false,
	}
	for repo, w := range want {
		if gate[repo] != w {
			t.Errorf("%s: got %v want %v", repo, gate[repo], w)
		}
	}
	if _, ok := gate["bridge"]; ok {
		t.Errorf("no autonomous lane claims bridge, so it must not be fetched or answered: %+v", gate)
	}
	if slices.Contains(fetched, "bridge") {
		t.Errorf("fetched a repo no autonomous lane claims: %v", fetched)
	}

	// A transient error must not be cached: the repo would then be pinned out
	// of the lane for the whole TTL over one failed request.
	cache := loadLaneGateCache(path)
	if _, ok := cache.fresh("game-broken", now); ok {
		t.Error("a failed fetch must not write a cache entry")
	}
	if v, ok := cache.fresh("game-wired", now); !ok || !v {
		t.Errorf("a successful read must be cached: v=%v ok=%v", v, ok)
	}
}

func TestResolveGateWithServesFromCacheWithoutFetching(t *testing.T) {
	now := time.Date(2026, 9, 21, 12, 0, 0, 0, time.Local)
	lanes := []dispatch.Lane{{Name: "auto", Repos: []string{"game-*"}, Autonomous: true}}
	path := filepath.Join(t.TempDir(), "lane-gate.json")
	if err := saveLaneGateCache(path, laneGateCache{Repos: map[string]laneGateEntry{
		"game-wired": {Autonomous: true, CheckedAt: now.Add(-time.Hour)},
	}}); err != nil {
		t.Fatal(err)
	}

	fetch := func(_ context.Context, _, repo string) ([]byte, bool, error) {
		t.Errorf("fetched %s despite a fresh cache entry", repo)
		return nil, false, nil
	}

	gate := resolveGateWith(context.Background(),
		[]repoInput{{Forge: "github", Owner: "freaxnx01", Name: "game-wired"}},
		lanes, now, fetch, path)

	if !gate["game-wired"] {
		t.Errorf("cached true must be served: %+v", gate)
	}
}
