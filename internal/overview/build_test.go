package overview

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/freaxnx01/bridge/internal/core"
)

func TestBuild_RanksIssuesSplitsUnweightedAndCollectsInbox(t *testing.T) {
	now := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	root := t.TempDir()
	repoPath := filepath.Join(root, "bridge")
	mustWrite(t, filepath.Join(repoPath, "TODO.md"), "- [ ] a todo\n")

	cfg := Config{
		Repos: []core.Repo{{Name: "bridge", Path: repoPath}},
		Now:   func() time.Time { return now },
		FetchIssues: func(_ context.Context) ([]Issue, error) {
			return []Issue{
				{Repo: "bridge", Title: "high bang", URL: "u1", Labels: []string{"value/4", "effort/2"}, Updated: now},
				{Repo: "bridge", Title: "low bang", URL: "u2", Labels: []string{"value/2", "effort/4"}, Updated: now},
				{Repo: "bridge", Title: "unweighted", URL: "u3", Labels: []string{"bug"}, Updated: now},
			}, nil
		},
	}

	snap, err := Build(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(snap.Ranked) != 2 {
		t.Fatalf("ranked = %d, want 2", len(snap.Ranked))
	}
	if snap.Ranked[0].Title != "high bang" { // 2.0 sorts above 0.5
		t.Errorf("ranked[0] = %q, want high bang", snap.Ranked[0].Title)
	}
	if len(snap.NeedsWeighting) != 1 || snap.NeedsWeighting[0].Title != "unweighted" {
		t.Errorf("needs-weighting = %+v, want [unweighted]", snap.NeedsWeighting)
	}
	if len(snap.Inbox) != 1 || snap.Inbox[0].Source != CaptureRepoTodo {
		t.Errorf("inbox = %+v, want one TODO capture", snap.Inbox)
	}
}

func TestBuild_NilFetchIssues_NoError(t *testing.T) {
	snap, err := Build(context.Background(), Config{Now: func() time.Time { return time.Now() }})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(snap.Ranked) != 0 || len(snap.Inbox) != 0 {
		t.Errorf("empty config should yield empty snapshot, got %+v", snap)
	}
}

func TestBuild_RoadmapFetchError_DegradesOtherTiersSurvive(t *testing.T) {
	now := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	root := t.TempDir()
	repoPath := filepath.Join(root, "bridge")
	mustWrite(t, filepath.Join(repoPath, "TODO.md"), "- [ ] a todo\n")

	roadmapErr := errors.New("github graphql: token needs project scope")
	cfg := Config{
		Repos: []core.Repo{{Name: "bridge", Path: repoPath}},
		Now:   func() time.Time { return now },
		FetchIssues: func(_ context.Context) ([]Issue, error) {
			return []Issue{
				{Repo: "bridge", Title: "high bang", URL: "u1", Labels: []string{"value/4", "effort/2"}, Updated: now},
			}, nil
		},
		FetchRoadmap: func(_ context.Context) ([]RoadmapItem, error) {
			return nil, roadmapErr
		},
	}

	snap, err := Build(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Build returned error, want nil (roadmap should degrade): %v", err)
	}
	if snap.RoadmapErr == "" {
		t.Errorf("RoadmapErr = empty, want the fetch error message")
	}
	if !strings.Contains(snap.RoadmapErr, "project scope") {
		t.Errorf("RoadmapErr = %q, want it to contain the fetch error text", snap.RoadmapErr)
	}
	if len(snap.Roadmap) != 0 {
		t.Errorf("Roadmap = %d items, want 0 on fetch error", len(snap.Roadmap))
	}
	if len(snap.Ranked) != 1 {
		t.Errorf("Ranked = %d, want 1 (other tiers must still render)", len(snap.Ranked))
	}
	if len(snap.Inbox) != 1 {
		t.Errorf("Inbox = %d, want 1 (other tiers must still render)", len(snap.Inbox))
	}
}

func TestBuild_Roadmap_StatusOrderedNoScoring(t *testing.T) {
	cfg := Config{
		Now: func() time.Time { return time.Now() },
		FetchRoadmap: func(_ context.Context) ([]RoadmapItem, error) {
			return []RoadmapItem{
				{Repo: "bridge", Title: "done thing", Status: "Done"},
				{Repo: "bridge", Title: "todo thing", Status: "Todo"},
				{Repo: "bridge", Title: "wip thing", Status: "In Progress"},
				{Repo: "bridge", Title: "weird", Status: "Backlog"}, // unknown -> last
			}, nil
		},
	}
	snap, err := Build(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(snap.Roadmap) != 4 {
		t.Fatalf("roadmap = %d, want 4", len(snap.Roadmap))
	}
	gotOrder := []string{snap.Roadmap[0].Status, snap.Roadmap[1].Status, snap.Roadmap[2].Status, snap.Roadmap[3].Status}
	want := []string{"Todo", "In Progress", "Done", "Backlog"}
	for i := range want {
		if gotOrder[i] != want[i] {
			t.Errorf("status order[%d] = %q, want %q", i, gotOrder[i], want[i])
		}
	}
	// roadmap items must NOT leak into the weighted/ranked tiers
	if len(snap.Ranked) != 0 || len(snap.NeedsWeighting) != 0 {
		t.Errorf("roadmap leaked into ranked/needs-weighting: %+v %+v", snap.Ranked, snap.NeedsWeighting)
	}
}
