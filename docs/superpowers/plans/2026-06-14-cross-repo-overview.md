# Cross-repo Overview (Plan 1a) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a bridge-nav cross-repo Overview screen that ranks open GitHub Issues by a W3 value/effort score and shows raw file captures (`ideas-lab`, `ideas.md`, `TODO.md`) as an unranked inbox.

**Architecture:** A new client-agnostic `internal/overview` package aggregates sources behind injected callbacks (matching nav's existing `Clone`/`FetchIssues`/`FetchRemote` DI idiom) and returns a plain `Snapshot`. bridge-nav gets a new `screenOverview` that renders the Snapshot in two tiers. `cmd/bridge` wires the real forge calls. The roadmap board (GitHub Projects v2 / GraphQL) is a defined **seam** filled by a later plan (1b) — here it is nil.

**Tech Stack:** Go (stdlib `testing`, table-driven, hand-rolled fakes), Cobra, Bubble Tea / lipgloss. Spec: `docs/superpowers/specs/2026-06-14-cross-repo-overview-design.md`.

**Scope note (read first):** This is **Plan 1a** of sub-project #1. It deliberately excludes the GitHub Projects v2 GraphQL provider — bridge has no GraphQL capability today, so that provider needs its own spike + plan (**1b**). 1a ships a working weighted overview using GitHub **Issues** (label-weighted) + file captures, with the roadmap provider as a nil seam. v1 is **read-mostly**: `enter` surfaces an item's URL/path in the status line (no browser launch — you're usually on ssh/tmux). In-TUI weighting and inbox→Issue promotion are out of scope (spec follow-ups).

---

## File Structure

- **Create** `internal/overview/overview.go` — types (`Snapshot`, `RankedItem`, `Capture`, source enums), `Config`, `Build`.
- **Create** `internal/overview/score.go` — pure scoring (`scoreItem`, `urgencyBoost`, `weightFromLabels`) + constants.
- **Create** `internal/overview/captures.go` — file-source readers (`collectCaptures` and helpers).
- **Create** `internal/overview/*_test.go` — table tests per file.
- **Modify** `internal/nav/types.go` — `screenOverview` const; `BuildOverview` Config field; overview message types.
- **Modify** `internal/nav/model.go` — overview model fields.
- **Create** `internal/nav/overview.go` — `buildOverviewCmd` + overview Update/View helpers (keeps the new screen in its own file).
- **Modify** `internal/nav/update.go` — picker `o` enters overview; route overview-screen keys; handle overview messages.
- **Modify** `internal/nav/view.go` — dispatch `screenOverview` to the new view.
- **Modify** `internal/nav/*_test.go` — Update/scoring/render tests (new test file `internal/nav/overview_test.go`).
- **Modify** `cmd/bridge/nav.go` — assemble `overview.Config` (issues across repos, ideas-lab dir, nil roadmap) and inject `BuildOverview`.

---

## Task 1: `internal/overview` scoring (pure)

**Files:**
- Create: `internal/overview/score.go`
- Create: `internal/overview/overview.go` (types only, in this task)
- Test: `internal/overview/score_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/overview/score_test.go`:

```go
package overview

import (
	"testing"
	"time"
)

func TestScoreItem_ValueEffortUrgencyStale(t *testing.T) {
	now := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	due := func(d time.Duration) *time.Time { t := now.Add(d); return &t }
	tests := []struct {
		name           string
		value, effort  int
		due            *time.Time
		updated        time.Time
		wantScore      float64
		wantStale      bool
	}{
		{"bang_for_buck", 4, 2, nil, now, 2.0, false},
		{"effort_defaults_to_3", 4, 0, nil, now, 1.33, false},
		{"due_soon_plus1", 4, 2, due(10 * 24 * time.Hour), now, 3.0, false},
		{"due_urgent_plus2", 4, 2, due(2 * 24 * time.Hour), now, 4.0, false},
		{"overdue_plus2", 4, 2, due(-5 * 24 * time.Hour), now, 4.0, false},
		{"stale_high_value", 4, 2, nil, now.Add(-40 * 24 * time.Hour), 2.0, true},
		{"not_stale_low_value", 2, 2, nil, now.Add(-40 * 24 * time.Hour), 1.0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, stale := scoreItem(tt.value, tt.effort, tt.due, tt.updated, now)
			if got != tt.wantScore {
				t.Errorf("score = %v, want %v", got, tt.wantScore)
			}
			if stale != tt.wantStale {
				t.Errorf("stale = %v, want %v", stale, tt.wantStale)
			}
		})
	}
}

func TestWeightFromLabels(t *testing.T) {
	tests := []struct {
		name          string
		labels        []string
		wantV, wantE  int
	}{
		{"both", []string{"value/4", "effort/2"}, 4, 2},
		{"value_only", []string{"value/5", "bug"}, 5, 0},
		{"none", []string{"bug", "chore"}, 0, 0},
		{"ignores_bad", []string{"value/x", "effort/9"}, 0, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v, e := weightFromLabels(tt.labels)
			if v != tt.wantV || e != tt.wantE {
				t.Errorf("got (%d,%d), want (%d,%d)", v, e, tt.wantV, tt.wantE)
			}
		})
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/overview/ -run TestScoreItem -v`
Expected: FAIL — build error, `internal/overview` has no Go files / undefined `scoreItem`.

- [ ] **Step 3: Write the types and scoring**

Create `internal/overview/overview.go` (types only for now):

```go
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
```

Create `internal/overview/score.go`:

```go
package overview

import (
	"math"
	"strconv"
	"strings"
	"time"
)

const (
	defaultEffort       = 3
	staleValueThreshold = 4
	staleAfter          = 30 * 24 * time.Hour
	urgentWithin        = 3 * 24 * time.Hour
	soonWithin          = 14 * 24 * time.Hour
)

// scoreItem computes the W3 score and stale flag for a structured item.
// score = round(value/effort + urgencyBoost, 2). effort 0 => defaultEffort.
// stale is a flag (not a score term): a high-value item untouched for a while.
func scoreItem(value, effort int, due *time.Time, updated, now time.Time) (float64, bool) {
	if effort <= 0 {
		effort = defaultEffort
	}
	s := float64(value)/float64(effort) + urgencyBoost(due, now)
	s = math.Round(s*100) / 100
	stale := value >= staleValueThreshold && now.Sub(updated) > staleAfter
	return s, stale
}

// urgencyBoost adds weight as a due date approaches (or passes).
func urgencyBoost(due *time.Time, now time.Time) float64 {
	if due == nil {
		return 0
	}
	switch d := due.Sub(now); {
	case d <= urgentWithin: // due within 3 days or overdue
		return 2
	case d <= soonWithin: // due within 14 days
		return 1
	default:
		return 0
	}
}

// weightFromLabels extracts value/effort from "value/N" and "effort/N" labels.
// Missing or malformed labels yield 0 (value 0 => unweighted; effort 0 =>
// default applied during scoring). N must be 1..5.
func weightFromLabels(labels []string) (value, effort int) {
	for _, l := range labels {
		if n, ok := labelNum(l, "value/"); ok {
			value = n
		}
		if n, ok := labelNum(l, "effort/"); ok {
			effort = n
		}
	}
	return value, effort
}

func labelNum(label, prefix string) (int, bool) {
	if !strings.HasPrefix(label, prefix) {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimPrefix(label, prefix))
	if err != nil || n < 1 || n > 5 {
		return 0, false
	}
	return n, true
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/overview/ -run 'TestScoreItem|TestWeightFromLabels' -v`
Expected: PASS (both).

- [ ] **Step 5: Commit**

```bash
git add internal/overview/overview.go internal/overview/score.go internal/overview/score_test.go
git commit -m "feat(overview): types + W3 value/effort scoring

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 2: File-capture sources (inbox)

**Files:**
- Create: `internal/overview/captures.go`
- Test: `internal/overview/captures_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/overview/captures_test.go`:

```go
package overview

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/freaxnx01/bridge/internal/core"
)

func TestCollectCaptures_AllSources(t *testing.T) {
	root := t.TempDir()
	// ideas-lab: one file per idea
	ideasLab := filepath.Join(root, "ideas-lab")
	mustWrite(t, filepath.Join(ideasLab, "2026-06-01-kanban.md"), "# Kanban for issues\n\nrough\n")
	// a repo with ideas.md + TODO.md (bullets)
	repoPath := filepath.Join(root, "bridge")
	mustWrite(t, filepath.Join(repoPath, "ideas.md"), "# Ideas\n\n- multi-pane focus model\n- weighted overview\n")
	mustWrite(t, filepath.Join(repoPath, "TODO.md"), "- [ ] wire api skeleton\n- [x] done thing\n- [ ] add tests\n")

	cfg := Config{
		IdeasLabDir: ideasLab,
		Repos:       []core.Repo{{Name: "bridge", Path: repoPath}},
		Now:         func() time.Time { return time.Now() },
	}
	got := collectCaptures(cfg)

	countBy := map[CaptureSource]int{}
	for _, c := range got {
		countBy[c.Source]++
	}
	if countBy[CaptureIdeasLab] != 1 {
		t.Errorf("ideas-lab captures = %d, want 1", countBy[CaptureIdeasLab])
	}
	if countBy[CaptureRepoIdeas] != 2 {
		t.Errorf("ideas.md captures = %d, want 2", countBy[CaptureRepoIdeas])
	}
	if countBy[CaptureRepoTodo] != 2 { // only the two "- [ ]" lines, not "- [x]"
		t.Errorf("TODO.md captures = %d, want 2", countBy[CaptureRepoTodo])
	}
	// ideas-lab title comes from the first heading/line
	for _, c := range got {
		if c.Source == CaptureIdeasLab && c.Title != "Kanban for issues" {
			t.Errorf("ideas-lab title = %q, want %q", c.Title, "Kanban for issues")
		}
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/overview/ -run TestCollectCaptures -v`
Expected: FAIL — undefined `collectCaptures` / `Config`.

- [ ] **Step 3: Implement the capture readers**

Create `internal/overview/captures.go`:

```go
package overview

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/freaxnx01/bridge/internal/core"
)

// Config is everything Build needs for one environment. Forge access is
// injected (callbacks) so this package stays client/token-free, matching nav's
// DI idiom.
type Config struct {
	Environment  string      // "Personal" / "Business" (display only)
	Repos        []core.Repo // discovered repos in this environment
	IdeasLabDir  string      // path to ideas-lab idea files; "" disables
	FetchIssues  func(ctx context.Context) ([]Issue, error)
	FetchRoadmap func(ctx context.Context) ([]RankedItem, error) // nil => no board (1a)
	Now          func() time.Time
}

// Issue is the minimal open-issue shape Build needs (decoupled from forge.Issue
// so this package has no forge dependency; cmd/bridge adapts).
type Issue struct {
	Repo    string
	Title   string
	URL     string
	Labels  []string
	Updated time.Time
}

func (cfg Config) now() time.Time {
	if cfg.Now != nil {
		return cfg.Now()
	}
	return time.Now()
}

// collectCaptures reads every raw-capture file source into a flat, recency-
// sorted slice (newest first). Missing files/dirs are skipped silently.
func collectCaptures(cfg Config) []Capture {
	now := cfg.now()
	var out []Capture
	out = append(out, ideasLabCaptures(cfg.IdeasLabDir, now)...)
	for _, r := range cfg.Repos {
		out = append(out, bulletCaptures(filepath.Join(r.Path, "ideas.md"), CaptureRepoIdeas, r.Name, now)...)
		out = append(out, todoCaptures(filepath.Join(r.Path, "TODO.md"), r.Name, now)...)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Age < out[j].Age })
	return out
}

// ideasLabCaptures treats each *.md file in dir as one capture, titled by its
// first markdown heading or first non-empty line.
func ideasLabCaptures(dir string, now time.Time) []Capture {
	if dir == "" {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []Capture
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".md") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		out = append(out, Capture{
			Source: CaptureIdeasLab,
			Title:  fileTitle(path, e.Name()),
			Path:   path,
			Age:    age(path, now),
		})
	}
	return out
}

// bulletCaptures reads top-level "- "/"* " bullets from a list file.
func bulletCaptures(path string, src CaptureSource, repo string, now time.Time) []Capture {
	lines, ok := readLines(path)
	if !ok {
		return nil
	}
	a := age(path, now)
	var out []Capture
	for _, l := range lines {
		if t, ok := bulletText(l); ok {
			out = append(out, Capture{Source: src, Repo: repo, Title: t, Path: path, Age: a})
		}
	}
	return out
}

// todoCaptures reads only unchecked "- [ ]" lines from a TODO file.
func todoCaptures(path, repo string, now time.Time) []Capture {
	lines, ok := readLines(path)
	if !ok {
		return nil
	}
	a := age(path, now)
	var out []Capture
	for _, l := range lines {
		s := strings.TrimSpace(l)
		if strings.HasPrefix(s, "- [ ]") {
			out = append(out, Capture{
				Source: CaptureRepoTodo,
				Repo:   repo,
				Title:  strings.TrimSpace(strings.TrimPrefix(s, "- [ ]")),
				Path:   path,
				Age:    a,
			})
		}
	}
	return out
}

func bulletText(line string) (string, bool) {
	s := strings.TrimSpace(line)
	for _, p := range []string{"- ", "* "} {
		if strings.HasPrefix(s, p) && !strings.HasPrefix(s, "- [") {
			return strings.TrimSpace(strings.TrimPrefix(s, p)), true
		}
	}
	return "", false
}

func fileTitle(path, fallback string) string {
	lines, ok := readLines(path)
	if !ok {
		return fallback
	}
	for _, l := range lines {
		s := strings.TrimSpace(l)
		if s == "" {
			continue
		}
		return strings.TrimSpace(strings.TrimLeft(s, "# "))
	}
	return fallback
}

func readLines(path string) ([]string, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	body := strings.ReplaceAll(string(data), "\r\n", "\n")
	return strings.Split(body, "\n"), true
}

func age(path string, now time.Time) time.Duration {
	fi, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return now.Sub(fi.ModTime())
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/overview/ -run TestCollectCaptures -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/overview/captures.go internal/overview/captures_test.go
git commit -m "feat(overview): file-capture inbox sources (ideas-lab, ideas.md, TODO.md)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 3: `Build` orchestration

**Files:**
- Modify: `internal/overview/overview.go` (add `Build`)
- Test: `internal/overview/build_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/overview/build_test.go`:

```go
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
		Repos:       []core.Repo{{Name: "bridge", Path: repoPath}},
		Now:         func() time.Time { return now },
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
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/overview/ -run TestBuild -v`
Expected: FAIL — undefined `Build`.

- [ ] **Step 3: Implement `Build`**

Append to `internal/overview/overview.go`:

```go
import (
	"context"
	"fmt"
	"sort"
)

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
```

Note: `internal/overview/overview.go` now needs the `context`, `fmt`, `sort`
imports merged into its existing import block (it already imports `time`). Run
`goimports -w internal/overview/overview.go` to merge cleanly.

- [ ] **Step 4: Run the full package suite**

Run: `go test ./internal/overview/ -v`
Expected: PASS (scoring, captures, build).

- [ ] **Step 5: Commit**

```bash
git add internal/overview/overview.go internal/overview/build_test.go
git commit -m "feat(overview): Build orchestration (ranked issues + inbox + roadmap seam)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 4: nav model + Config wiring for the Overview screen

**Files:**
- Modify: `internal/nav/types.go` (screen const, Config field, message types)
- Modify: `internal/nav/model.go` (model fields)
- Create: `internal/nav/overview.go` (`buildOverviewCmd`)

- [ ] **Step 1: Add the screen constant, Config field, and messages**

In `internal/nav/types.go`, extend the screen enum:

```go
const (
	screenPicker screen = iota
	screenDash
	screenOverview
)
```

Add to the `Config` struct (after `FetchRemote`, before `IssueCacheDir`):

```go
	// BuildOverview aggregates this environment's cross-repo Snapshot (issues +
	// roadmap + file captures). Nil disables the Overview screen. Injected by
	// cmd/bridge so internal/nav stays forge-token-free.
	BuildOverview func(ctx context.Context) (overview.Snapshot, error)
```

Add the import `"github.com/freaxnx01/bridge/internal/overview"` to `types.go`.

Add message types near the other `…Msg` declarations:

```go
type overviewMsg struct{ snap overview.Snapshot }
type overviewErrMsg struct{ err error }
```

- [ ] **Step 2: Add model fields**

In `internal/nav/model.go`, add to the `Model` struct (near the other screen state):

```go
	overview      overview.Snapshot
	overviewState loadState
	ovFocus       ovPane // which overview pane has focus
	ovRankedSel   int
	ovInboxSel    int
```

Add the import `"github.com/freaxnx01/bridge/internal/overview"` to `model.go`, and define the pane enum in `internal/nav/overview.go` (next step).

- [ ] **Step 3: Create `internal/nav/overview.go` with the build command + pane enum**

Create `internal/nav/overview.go`:

```go
package nav

import (
	"context"

	tea "github.com/charmbracelet/bubbletea"
)

// ovPane identifies the focused pane on the Overview screen.
type ovPane int

const (
	ovRankedPane ovPane = iota
	ovInboxPane
)

// buildOverviewCmd runs the injected BuildOverview aggregator off the Update
// loop. Nil callback yields an error message so the screen shows a notice
// rather than hanging on a spinner.
func (m Model) buildOverviewCmd() tea.Cmd {
	build := m.cfg.BuildOverview
	return func() tea.Msg {
		if build == nil {
			return overviewErrMsg{err: errNoOverview}
		}
		snap, err := build(context.Background())
		if err != nil {
			return overviewErrMsg{err: err}
		}
		return overviewMsg{snap: snap}
	}
}
```

Add the sentinel error to `internal/nav/overview.go`:

```go
import "errors"

var errNoOverview = errors.New("overview not configured")
```

(Merge the two import blocks into one with `goimports -w internal/nav/overview.go`.)

- [ ] **Step 4: Verify it compiles**

Run: `go build ./internal/nav/ && gofmt -l internal/nav/`
Expected: builds; no gofmt output. (The screen isn't wired into Update/View yet — that's Tasks 5-6 — but it must compile.)

- [ ] **Step 5: Commit**

```bash
git add internal/nav/types.go internal/nav/model.go internal/nav/overview.go
git commit -m "feat(nav): overview model state + BuildOverview wiring + buildOverviewCmd

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 5: Overview screen Update (enter, navigate, act, back)

**Files:**
- Modify: `internal/nav/update.go` (picker `o` entry; overview message handling; overview key routing)
- Create/Modify: `internal/nav/overview_test.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/nav/overview_test.go`:

```go
package nav

import (
	"context"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/freaxnx01/bridge/internal/overview"
)

func TestUpdatePicker_O_EntersOverview(t *testing.T) {
	m := initialModel(Config{
		BuildOverview: func(_ context.Context) (overview.Snapshot, error) {
			return overview.Snapshot{Ranked: []overview.RankedItem{{Title: "x", Score: 1}}}, nil
		},
	})
	m.pickerFocus = focusList
	out, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("o")})
	got := out.(Model)
	if got.screen != screenOverview {
		t.Fatalf("screen = %d, want screenOverview", got.screen)
	}
	if got.overviewState != loadPending {
		t.Errorf("overviewState = %d, want loadPending", got.overviewState)
	}
	if cmd == nil {
		t.Fatal("entering overview should return a build Cmd")
	}
	if _, ok := cmd().(overviewMsg); !ok {
		t.Fatalf("cmd msg = %T, want overviewMsg", cmd())
	}
}

func TestUpdate_OverviewMsg_PopulatesAndOK(t *testing.T) {
	m := initialModel(Config{})
	m.screen = screenOverview
	snap := overview.Snapshot{Ranked: []overview.RankedItem{{Title: "a"}, {Title: "b"}}}
	out, _ := m.Update(overviewMsg{snap: snap})
	got := out.(Model)
	if got.overviewState != loadOK || len(got.overview.Ranked) != 2 {
		t.Errorf("overview not applied: state=%d ranked=%d", got.overviewState, len(got.overview.Ranked))
	}
}

func TestUpdateOverview_EscReturnsToPicker(t *testing.T) {
	m := initialModel(Config{})
	m.screen = screenOverview
	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if out.(Model).screen != screenPicker {
		t.Errorf("esc should return to picker")
	}
}

func TestUpdateOverview_TabSwitchesPane(t *testing.T) {
	m := initialModel(Config{})
	m.screen = screenOverview
	m.overview = overview.Snapshot{
		Ranked: []overview.RankedItem{{Title: "a"}},
		Inbox:  []overview.Capture{{Title: "c"}},
	}
	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	if out.(Model).ovFocus != ovInboxPane {
		t.Errorf("tab should move focus to inbox pane")
	}
}

func TestUpdateOverview_EnterShowsURLInStatus(t *testing.T) {
	m := initialModel(Config{})
	m.screen = screenOverview
	m.ovFocus = ovRankedPane
	m.overview = overview.Snapshot{Ranked: []overview.RankedItem{{Title: "a", URL: "https://x/1"}}}
	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if got := out.(Model).status; got != "https://x/1" {
		t.Errorf("status = %q, want the item URL", got)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/nav/ -run TestUpdateOverview -v`
Expected: FAIL — overview keys not handled (screen stays/keys ignored).

- [ ] **Step 3: Wire the picker entry, messages, and overview key routing**

In `internal/nav/update.go`, add the picker entry in the `focusList` switch (next to `case "r":`):

```go
		case "o":
			if m.cfg.BuildOverview == nil {
				return m, nil
			}
			m.screen = screenOverview
			m.overviewState = loadPending
			m.ovFocus = ovRankedPane
			m.ovRankedSel = 0
			m.ovInboxSel = 0
			return m, m.buildOverviewCmd()
```

Add message handling alongside the other `case …Msg` blocks at the top of `Update`:

```go
	case overviewMsg:
		m.overview = msg.snap
		m.overviewState = loadOK
		if m.ovRankedSel >= len(m.overview.Ranked) {
			m.ovRankedSel = 0
		}
		return m, nil
	case overviewErrMsg:
		m.overviewState = loadErr
		m.status = "overview unavailable: " + msg.err.Error()
		return m, nil
```

Add overview key routing. Find where `Update` dispatches key messages per screen
(near `if m.screen == screenDash { … }` / the picker key handler) and add a
branch that calls a new `updateOverviewKeys`. In `internal/nav/overview.go`,
implement it:

```go
// updateOverviewKeys handles key presses on the Overview screen. Read-mostly:
// navigate, switch panes, esc back, and enter to surface the target URL/path in
// the status line (no browser launch — usually over ssh/tmux).
func (m Model) updateOverviewKeys(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.screen = screenPicker
		return m, nil
	case "tab":
		if m.ovFocus == ovRankedPane {
			m.ovFocus = ovInboxPane
		} else {
			m.ovFocus = ovRankedPane
		}
		return m, nil
	case "up", "k":
		m.ovMove(-1)
	case "down", "j":
		m.ovMove(1)
	case "enter":
		m.status = m.ovSelectedTarget()
	}
	return m, nil
}

func (m *Model) ovMove(delta int) {
	if m.ovFocus == ovRankedPane {
		m.ovRankedSel = clampInt(m.ovRankedSel+delta, 0, len(m.overview.Ranked)-1)
	} else {
		m.ovInboxSel = clampInt(m.ovInboxSel+delta, 0, len(m.overview.Inbox)-1)
	}
}

// ovSelectedTarget returns the URL (ranked) or file path (inbox) of the current
// selection, or "" when the pane is empty.
func (m Model) ovSelectedTarget() string {
	if m.ovFocus == ovRankedPane {
		if m.ovRankedSel < len(m.overview.Ranked) {
			return m.overview.Ranked[m.ovRankedSel].URL
		}
		return ""
	}
	if m.ovInboxSel < len(m.overview.Inbox) {
		return m.overview.Inbox[m.ovInboxSel].Path
	}
	return ""
}
```

In `update.go`, route to it when on the overview screen. The existing key
dispatch has a per-screen shape; add, before the picker/dash handling:

```go
	if m.screen == screenOverview {
		if key, ok := msg.(tea.KeyMsg); ok {
			if key.String() == "q" || key.String() == "ctrl+c" {
				return m, tea.Quit
			}
			return m.updateOverviewKeys(key)
		}
	}
```

(`clampInt` already exists in nav — it's used by the picker pager.)

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/nav/ -run 'TestUpdateOverview|TestUpdatePicker_O|TestUpdate_OverviewMsg' -v`
Expected: PASS (all five).

- [ ] **Step 5: Run the full nav suite + commit**

Run: `go test ./internal/nav/ && gofmt -l internal/nav/`
Expected: `ok`; no gofmt output.

```bash
git add internal/nav/update.go internal/nav/overview.go internal/nav/overview_test.go
git commit -m "feat(nav): Overview screen Update (enter, navigate, panes, back)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 6: Overview screen View (two-tier render)

**Files:**
- Modify: `internal/nav/view.go` (dispatch screenOverview)
- Modify: `internal/nav/overview.go` (add `viewOverview`)
- Modify: `internal/nav/overview_test.go` (render test)

- [ ] **Step 1: Write the failing test**

Append to `internal/nav/overview_test.go`:

```go
func TestViewOverview_RendersTiersAndScores(t *testing.T) {
	m := initialModel(Config{})
	m.screen = screenOverview
	m.width, m.height = 100, 40
	m.overviewState = loadOK
	m.overview = overview.Snapshot{
		Ranked: []overview.RankedItem{
			{Repo: "bridge", Title: "wire api", Value: 4, Effort: 2, Score: 2.0},
		},
		NeedsWeighting: []overview.RankedItem{{Repo: "x", Title: "triage me"}},
		Inbox:          []overview.Capture{{Source: overview.CaptureRepoTodo, Repo: "bridge", Title: "a todo"}},
	}
	out := m.viewOverview()
	for _, want := range []string{"wire api", "2.0", "triage me", "a todo", "Inbox"} {
		if !strings.Contains(out, want) {
			t.Errorf("viewOverview missing %q\n---\n%s", want, out)
		}
	}
}
```

Add `"strings"` to the test file's imports.

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/nav/ -run TestViewOverview -v`
Expected: FAIL — undefined `m.viewOverview`.

- [ ] **Step 3: Implement `viewOverview` and dispatch it**

In `internal/nav/view.go`, find the screen dispatch in `View()` (e.g.
`if m.screen == screenPicker { return m.viewPicker() }` / dash) and add:

```go
	if m.screen == screenOverview {
		return m.viewOverview()
	}
```

Add `viewOverview` to `internal/nav/overview.go` (reusing the existing
`panel`, `stMuted`, `stSel`, `stAccent`, `stText`, and `hintLine` helpers the
other screens use):

```go
import (
	"fmt"
	"strings"
	"time"
)

// viewOverview renders the two-tier cross-repo overview: a ranked "what matters
// now" list (plus a needs-weighting group) and the raw-capture inbox.
func (m Model) viewOverview() string {
	w := m.width
	title := "bridge · " + envLabel(m.cfg.Environment) + " · Overview"
	if m.overviewState == loadPending {
		return panel(w, title, stMuted.Render("◐ building overview…"))
	}

	var rb strings.Builder
	rb.WriteString(stAccent.Render("What matters now") + "\n")
	if len(m.overview.Ranked) == 0 {
		rb.WriteString(stMuted.Render("  (nothing ranked yet)") + "\n")
	}
	for i, it := range m.overview.Ranked {
		line := fmt.Sprintf("%-4.1f %-14s %s  %s", it.Score, truncate(it.Repo, 14), it.Title, weightBadge(it))
		rb.WriteString(selectableLine(m.ovFocus == ovRankedPane && i == m.ovRankedSel, line) + "\n")
	}
	if len(m.overview.NeedsWeighting) > 0 {
		rb.WriteString(stMuted.Render(fmt.Sprintf("⚖ needs weighting (%d)", len(m.overview.NeedsWeighting))) + "\n")
		for _, it := range m.overview.NeedsWeighting {
			rb.WriteString(stMuted.Render(fmt.Sprintf("   -    %-14s %s", truncate(it.Repo, 14), it.Title)) + "\n")
		}
	}

	var ib strings.Builder
	ib.WriteString(stAccent.Render(fmt.Sprintf("Inbox (raw captures) · %d", len(m.overview.Inbox))) + "\n")
	for i, c := range m.overview.Inbox {
		line := fmt.Sprintf("• %-14s %s  %s", truncate(captureWhere(c), 14), c.Title, humanAge(c.Age))
		ib.WriteString(selectableLine(m.ovFocus == ovInboxPane && i == m.ovInboxSel, line) + "\n")
	}

	sections := []string{
		panel(w, title, strings.TrimRight(rb.String(), "\n")),
		panel(w, "Inbox", strings.TrimRight(ib.String(), "\n")),
		m.hintLine("↑↓ move · tab pane · ⏎ show link/path · esc back · q quit"),
	}
	return strings.Join(sections, "\n")
}

func selectableLine(selected bool, text string) string {
	if selected {
		return stSel.Render(stAccent.Render("▸ ") + text)
	}
	return "  " + stText.Render(text)
}

func weightBadge(it overview.RankedItem) string {
	b := fmt.Sprintf("v%d/e%d", it.Value, effortOrDefault(it.Effort))
	if it.Stale {
		b += " ⚠"
	}
	return stMuted.Render(b)
}

func effortOrDefault(e int) int {
	if e <= 0 {
		return 3
	}
	return e
}

func envLabel(s string) string {
	if s == "" {
		return "bridge"
	}
	return s
}

func captureWhere(c overview.Capture) string {
	switch c.Source {
	case overview.CaptureIdeasLab:
		return "ideas-lab"
	case overview.CaptureRepoTodo:
		return c.Repo + " todo"
	default:
		return c.Repo + " idea"
	}
}

func humanAge(d time.Duration) string {
	days := int(d.Hours()) / 24
	if days <= 0 {
		return "today"
	}
	return fmt.Sprintf("%dd", days)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
```

Add the `"github.com/freaxnx01/bridge/internal/overview"` import to
`internal/nav/overview.go` (it now references `overview.RankedItem`/`Capture`).
Run `goimports -w internal/nav/overview.go`. If `truncate`/`humanAge` names
already exist in nav, reuse the existing ones instead of redeclaring (check with
`grep -n "func truncate\|func humanAge\|func selectableLine" internal/nav/*.go`
first and drop the duplicates).

- [ ] **Step 4: Run the render test + full suite**

Run: `go test ./internal/nav/ -run TestViewOverview -v && go test ./internal/nav/`
Expected: PASS; full nav suite `ok`.

- [ ] **Step 5: Commit**

```bash
git add internal/nav/view.go internal/nav/overview.go internal/nav/overview_test.go
git commit -m "feat(nav): Overview screen two-tier view (ranked + inbox)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 7: cmd/bridge wiring (real issues + ideas-lab) + smoke

**Files:**
- Modify: `cmd/bridge/nav.go` (assemble `overview.Config`, inject `BuildOverview`)

- [ ] **Step 1: Wire `BuildOverview` into the nav Config**

In `cmd/bridge/nav.go`, add to the `nav.Config{…}` literal (after `FetchRemote`):

```go
			BuildOverview: func(ctx context.Context) (overview.Snapshot, error) {
				return overview.Build(ctx, overview.Config{
					Environment: os.Getenv("BRIDGE_ENV"),
					Repos:       overviewRepos(),
					IdeasLabDir: ideasLabDir(),
					FetchIssues: func(ctx context.Context) ([]overview.Issue, error) {
						return fetchAllOpenIssues(ctx, overviewRepos())
					},
					FetchRoadmap: nil, // GitHub Projects v2 provider — Plan 1b
				})
			},
```

Add helper functions to `cmd/bridge/nav.go`:

```go
// overviewRepos returns the repos discovered across all configured roots, the
// set the cross-repo overview aggregates.
func overviewRepos() []core.Repo {
	repos, _ := discoverAllRoots()
	return repos
}

// ideasLabDir resolves the ideas-lab idea directory from BRIDGE_IDEAS_LAB
// (pointing at the ideas-lab repo's ideas/ folder). Empty disables that source.
func ideasLabDir() string {
	return os.Getenv("BRIDGE_IDEAS_LAB")
}

// fetchAllOpenIssues pulls open issues for every repo via the per-forge client,
// adapting forge.Issue to overview.Issue. A repo whose client/listing fails is
// skipped (best-effort, like the rest of nav's forge reads).
func fetchAllOpenIssues(ctx context.Context, repos []core.Repo) ([]overview.Issue, error) {
	var out []overview.Issue
	for _, r := range repos {
		c := clientFor(r.Forge)
		if c == nil {
			continue
		}
		issues, err := c.ListOpenIssues(ctx, r.Owner, r.Name)
		if err != nil {
			continue
		}
		for _, is := range issues {
			out = append(out, overview.Issue{
				Repo:    r.Owner + "/" + r.Name,
				Title:   is.Title,
				URL:     is.URL,
				Labels:  is.Labels,
				Updated: is.Updated,
			})
		}
	}
	return out, nil
}
```

Add imports to `cmd/bridge/nav.go`: `"github.com/freaxnx01/bridge/internal/overview"` (and confirm `core`, `os`, `context` are present — `core` and `context` already are; add `os` if missing).

- [ ] **Step 2: Build + full suite + vet/fmt**

Run:
```bash
go build ./... && go vet ./... && gofmt -l . | grep -v '.worktrees/' ; go test -race ./internal/overview/ ./internal/nav/ ./cmd/bridge/
```
Expected: builds; vet clean; no gofmt output (ignoring the pre-existing `.worktrees/` prototype); tests `ok`.

- [ ] **Step 3: Manual smoke**

Run:
```bash
just build
BRIDGE_IDEAS_LAB="$HOME/projects/repos/github/freaxnx01/private/ideas-lab/ideas" BRIDGE_ENV=Personal bridge nav
```
Then: on the picker press **`o`** → the Overview screen builds; confirm ranked
issues appear sorted by score with `vN/eM` badges, the "needs weighting" group
lists unlabeled issues, and the Inbox shows your `ideas-lab` / `ideas.md` /
`TODO.md` captures. `tab` switches panes, `↑↓` moves, `⏎` shows the URL/path in
the status line, `esc` returns to the picker.
Expected: a working two-tier cross-repo overview. (If you have no `value/N`
labels yet, everything lands in "needs weighting" — that's correct; add a
`value/4` label to an issue and re-enter to see it rank.)

- [ ] **Step 4: Commit**

```bash
git add cmd/bridge/nav.go
git commit -m "feat(bridge): wire cross-repo Overview (issues + ideas-lab) into nav

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 8: Full verification

**Files:** none.

- [ ] **Step 1: Format / vet / test gates**

Run:
```bash
gofmt -l . | grep -v '.worktrees/'   # expect empty
go vet ./...                          # expect clean
go test -race ./...                   # expect all ok, incl. internal/overview
```

- [ ] **Step 2: Lint (best-effort if available)**

Run: `golangci-lint run ./internal/overview/... ./internal/nav/... ./cmd/bridge/...` (if installed). Expect clean for the new code. If not installed, note it — `go vet` is the gate run.

- [ ] **Step 3: Report**

Report the actual command output for Steps 1-2 and the Task 7 manual smoke. Do not claim success without the output.

---

## Notes for the implementer

- **YAGNI:** do not build the GitHub Projects v2 / GraphQL provider — `FetchRoadmap` stays `nil` here (Plan 1b). The seam exists so 1b drops in without touching `Build`'s callers.
- **Read-mostly v1:** `enter` only surfaces the URL/path in the status line. No browser/editor launch (you're usually over ssh/tmux), no in-TUI weight editing, no inbox→Issue promotion — all are spec follow-ups.
- **Reuse, don't duplicate:** before adding `truncate`/`humanAge`/`selectableLine`/`clampInt`, grep nav for existing equivalents and use those (`clampInt` already exists). Drop any redeclaration the compiler flags.
- **DI idiom:** `internal/overview` must stay forge-token-free — all forge access comes through the injected `FetchIssues`/`FetchRoadmap` callbacks, exactly like nav's `Clone`/`FetchIssues`/`FetchRemote`.
- If you hit a blocker, find the fix and note it inline here for the next run.
```
