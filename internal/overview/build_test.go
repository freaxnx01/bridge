package overview

import (
	"context"
	"path/filepath"
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
