// Package overview aggregates a single environment's ideas, roadmap items, and
// todos across all repos into one weighted Snapshot. It is client-agnostic:
// forge access and file roots are injected via Config callbacks, so the same
// Snapshot drives the TUI today and the REST API / WebUI later.
package overview

import (
	"context"
	"fmt"
	"sort"
	"time"
)

// ItemSource identifies where a ranked item came from.
type ItemSource int

const (
	SourceGitHubIssue ItemSource = iota
	SourceProjectsCard
)

// CaptureSource identifies which raw-capture file a Capture came from.
type CaptureSource int

const (
	CaptureIdeasLab CaptureSource = iota
	CaptureRepoIdeas
	CaptureRepoTodo
)

// RankedItem is one structured, weighted item (a GitHub Issue or roadmap card).
type RankedItem struct {
	Source ItemSource
	Repo   string // owner/name; "" for board-level draft cards
	Title  string
	URL    string
	Value  int        // 1..5 manual; 0 => unweighted (goes to NeedsWeighting)
	Effort int        // 1..5 manual; 0 => default applied in scoring
	Due    *time.Time // nil if none
	Score  float64
	Stale  bool
}

// Capture is one raw, unranked capture from a markdown source file.
type Capture struct {
	Source CaptureSource
	Repo   string // "" for ideas-lab
	Title  string
	Path   string        // file path (jump target)
	Age    time.Duration // since file last modified
}

// Snapshot is the full cross-repo overview for one environment.
type Snapshot struct {
	Ranked         []RankedItem // weighted, sorted desc by Score
	NeedsWeighting []RankedItem // structured items with Value == 0
	Inbox          []Capture    // raw captures, grouped by Source+Repo in the view
}

// Build aggregates the environment's structured items (issues + roadmap cards)
// and raw file captures into one Snapshot. Ranked items are sorted by Score
// desc; Value==0 structured items go to NeedsWeighting. Forge errors abort;
// missing files are skipped.
func Build(ctx context.Context, cfg Config) (Snapshot, error) {
	now := cfg.now()
	var snap Snapshot

	if cfg.FetchIssues != nil {
		issues, err := cfg.FetchIssues(ctx)
		if err != nil {
			return snap, fmt.Errorf("fetch issues: %w", err)
		}
		for _, is := range issues {
			value, effort := weightFromLabels(is.Labels)
			item := RankedItem{
				Source: SourceGitHubIssue,
				Repo:   is.Repo,
				Title:  is.Title,
				URL:    is.URL,
				Value:  value,
				Effort: effort,
			}
			if value == 0 {
				snap.NeedsWeighting = append(snap.NeedsWeighting, item)
				continue
			}
			item.Score, item.Stale = scoreItem(value, effort, nil, is.Updated, now)
			snap.Ranked = append(snap.Ranked, item)
		}
	}

	if cfg.FetchRoadmap != nil {
		cards, err := cfg.FetchRoadmap(ctx)
		if err != nil {
			return snap, fmt.Errorf("fetch roadmap: %w", err)
		}
		for _, c := range cards {
			if c.Value == 0 {
				snap.NeedsWeighting = append(snap.NeedsWeighting, c)
				continue
			}
			c.Score, c.Stale = scoreItem(c.Value, c.Effort, c.Due, now, now)
			snap.Ranked = append(snap.Ranked, c)
		}
	}

	sort.SliceStable(snap.Ranked, func(i, j int) bool {
		return snap.Ranked[i].Score > snap.Ranked[j].Score
	})
	snap.Inbox = collectCaptures(cfg)
	return snap, nil
}
