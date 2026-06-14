// Package overview aggregates a single environment's ideas, roadmap items, and
// todos across all repos into one weighted Snapshot. It is client-agnostic:
// forge access and file roots are injected via Config callbacks, so the same
// Snapshot drives the TUI today and the REST API / WebUI later.
package overview

import "time"

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
