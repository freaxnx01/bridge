# `GET /api/digest` Implementation Plan (#380)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `GET /api/digest?since=<RFC3339>` returns, for a window, the following, all from collector snapshots with no per-request forge call:

- the dispatcher's tick health (expected vs. seen `:00`/`:30` slots, gaps, inert ticks, journal missing);
- one item per issue with pipeline activity, with its compressed story, `dispatched by`, group and Needs-you flag;
- the summary counts (dispatched, PRs opened, merged, stopped, spend, tick outcomes);
- the journal lines and the "didn't happen" lines.

The collector fires SSE `digest-updated` when the digest's inputs change.

**Architecture:**

- `internal/forge` exposes `PullRequest.MergedAt`. `internal/dispatch` gains `ClosingIssues`.
- `internal/cockpit` gains two pure files:
  - `digest_ticks.go` (`BuildTickHealth`);
  - `digest.go` (`BuildDigest` and the payload types).
- `digest_collect.go` holds the collector side:
  - recent PRs (one `state=all` page per wired GitHub repo, on the issues tick);
  - closed-issue threads, read once per pipeline PR `Updated`;
  - the journal, re-read when its stamp changes, on the runs tick;
  - the input fingerprint that drives `digest-updated`.
- `collector.go` gets only small hooks:
  - three optional `Sources` funcs;
  - one `digest` field;
  - one call each in `CollectIssues` / `CollectRuns`;
  - one event in `publish`.
- `internal/api/digest.go` is the handler. `cmd/bridge` wires the sources and the route.

**Tech Stack:** Go (as pinned in `go.mod`), stdlib `net/http`, `encoding/json`, `crypto/sha256`, `testing` (table-driven, hand-rolled fakes, `httptest`).

**Spec:** `docs/superpowers/specs/2026-11-11-digest-endpoint-design.md`

## Global Constraints

- **Dispatch order: #253 → #379 → this issue.**
  - This plan consumes #379's journal: `dispatch.JournalRecord`, `dispatch.JournalDecision`, `dispatch.ReadJournal(dir, since)`, the `Trigger*`/`Outcome*`/`Decision*`/`Kind*` constants, `dispatch.JournalVersion`, and `dispatchJournalDir()` in `cmd/bridge`.
  - #379 is parked until #253 merges. **This issue is dispatched only after #379's PR has merged.**
  - Task 0 is a hard precondition. If any of those symbols is missing on `main`, stop and comment "blocked: #379 not merged" on #380. Make no code changes, and do not re-implement #379.
- **#366 (in flight, `ai-implement`) edits `internal/cockpit/collector.go` and `board.go`** (adds `Board.Skipped`).
  - Keep every digest change in new files. In `collector.go`, touch only the named hooks in Task 4.
  - Do not edit `board.go`, `BuildBoard`, `Board`, or the existing `Sources` fields.
  - If #366 has merged first, rebase and re-apply the hooks against its shape. Do not change its behaviour.
- **#385 (being enriched) adds clear-lock endpoints.** It overlaps on:
  - route registration in `cmd/bridge/serve.go`, next to `/api/inbox`;
  - `internal/api`;
  - `docs/cockpit.md`'s API section.

  Add `/api/digest` as its own line and its own doc section. Do not reorder or rewrite neighbouring routes or sections.
- **No per-request forge call.** `GET /api/digest` reads only collector state. All new forge reads happen on the collector's existing ticks.
- Rate-limit budget:
  - one `ListPullRequests(…, "all")` per wired GitHub repo per issues tick (10 min);
  - one `GetIssue` per closed issue a pipeline PR closes, re-read only when that PR's `Updated` moves;
  - nothing else new.
- The new `Sources` funcs (`RecentPRs`, `Journal`, `JournalStamp`) are **optional**: nil disables that part. The existing test fake `fakeWorld.sources()` and every existing test stay **unmodified**.
- The board's and inbox's JSON, intervals and SSE events are unchanged.
- Go rules:
  - stdlib `testing` only, table-driven, hand-rolled fakes. **No** testify/mockery/gomock. **No new Go modules.**
  - `ctx` first where I/O happens. No package-level mutable state. Errors wrapped with `%w`.
  - Test names follow `TestFunc_State_Expected`.
- Gates after every task, all green before commit:
  - `gofmt -l .` empty;
  - `go vet ./...`;
  - `golangci-lint run`;
  - `go test -race ./...`.
- Conventional Commits referencing `#380`. Scopes: `forge`, `dispatch`, `cockpit`, `serve`, `docs`.

## Review Focus

1. **Self-merges count as merged.** A pipeline PR merged in the window, on an issue no longer open, gives an item in `done`, `merged` +1, and its markers' cost in `spendUsd` (Task 3 table, Task 4 collector test).
2. **Every "nothing" has a reason:**
   - a missing journal → `ticks.present: false`;
   - a gap → `ticks.gaps`;
   - inert ticks are flagged, and paused/outside-window ticks are not inert (Task 2).
3. **`by` attribution:**
   - a journal dispatch before `since` still attributes a run inside the window;
   - a run with no journal dispatch is `session` (Task 3).
4. **No forge call on the request path.** `Collector.Digest` never calls a `Sources` func (Task 4 test counts calls).
5. **Existing tests unmodified.**

---

### Task 0: Precondition — #379 is on `main`

- [ ] **Step 1:** Run
  ```bash
  git fetch origin && git log --oneline origin/main | grep -E '#379' | head
  grep -n 'func ReadJournal' internal/dispatch/journal.go
  grep -n 'type JournalRecord struct' internal/dispatch/journal.go
  grep -n 'func dispatchJournalDir' cmd/bridge/dispatch_journal.go
  grep -n 'func (c \*Collector) Inbox' internal/cockpit/collector.go
  ```
  Every command must print at least one line. If any prints nothing, **stop**: comment on #380 "blocked: #379 not merged", and end the run with no code changes.
- [ ] **Step 2:** Read `internal/dispatch/journal.go` and check these names against it:
  - the constant names used below: `TriggerTimer`, `TriggerManual`, `OutcomeDecided`, `OutcomeNoCandidates`, `OutcomeOutsideWindow`, `DecisionDispatched`, `DecisionWouldDispatch`, `DecisionSkipped`, `DecisionNotApplied`, `KindRetry`, `JournalVersion`;
  - the field names: `At`, `Trigger`, `Mode`, `RetryOnly`, `Outcome`, `Error`, `Repos`, `Decisions`, and per decision `Owner`, `Repo`, `Issue`, `Title`, `Kind`, `Lane`, `Decision`, `Reason`.

  If one was renamed, use the shipped name everywhere below. Do not add aliases.

### Task 1: `forge.PullRequest.MergedAt` and `dispatch.ClosingIssues`

**Files:**
- Modify: `internal/forge/client.go` (the `PullRequest` struct), `internal/forge/github.go` (`ghPull.toPullRequest`)
- Modify: `internal/forge/github_test.go`
- Modify: `internal/dispatch/eligible.go`, `internal/dispatch/eligible_test.go`

**Interfaces (produces):**

```go
// forge.PullRequest gains (after Merged):
MergedAt time.Time `json:"merged_at,omitempty"` // zero when not merged

// package dispatch
// ClosingIssues returns the issue numbers prBody closes with a GitHub closing
// keyword, in first-seen order, without duplicates.
func ClosingIssues(prBody string) []int
```

- [ ] **Step 1: Write the failing tests**

```go
// internal/forge/github_test.go
func TestGithubListPullRequests_MergedAt_MappedOrZero(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[
		  {"number":7,"state":"closed","merged_at":"2026-09-30T08:00:00Z","user":{"login":"a"}},
		  {"number":8,"state":"open","merged_at":null,"user":{"login":"b"}}
		]`))
	}))
	defer srv.Close()

	prs, err := NewGithubClient("token", srv.URL).ListPullRequests(context.Background(), "o", "r", "all")
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 9, 30, 8, 0, 0, 0, time.UTC)
	if !prs[0].MergedAt.Equal(want) || !prs[1].MergedAt.IsZero() {
		t.Fatalf("MergedAt = %v, %v", prs[0].MergedAt, prs[1].MergedAt)
	}
}
```

```go
// internal/dispatch/eligible_test.go
func TestClosingIssues_Table(t *testing.T) {
	tests := []struct {
		name string
		body string
		want []int
	}{
		{"none", "Refs #4", nil},
		{"one", "Closes #12", []int{12}},
		{"keywords", "fixes #3\nResolved #5 and closed #7", []int{3, 5, 7}},
		{"boundary", "Closes #410", []int{410}},
		{"dedup", "Closes #9. Also fixes #9", []int{9}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ClosingIssues(tt.body); !slices.Equal(got, tt.want) {
				t.Fatalf("ClosingIssues(%q) = %v, want %v", tt.body, got, tt.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run to verify failure:** `go test ./internal/forge ./internal/dispatch -run 'MergedAt|ClosingIssues'` should FAIL (undefined).
- [ ] **Step 3: Implement**
  - In `toPullRequest`, set `MergedAt` from `p.MergedAt` when it is non-nil.
  - Write `ClosingIssues` over `closesRE.FindAllStringSubmatch`, with `strconv.Atoi(m[2])`, keeping first-seen order and skipping duplicates. Leave `ClosesIssue` untouched.
- [ ] **Step 4: Gates.**
- [ ] **Step 5: Commit** `feat(forge): expose merged_at and the issues a PR closes (#380)`

### Task 2: Tick health (pure)

**Files:**
- Create: `internal/cockpit/digest_ticks.go`, `internal/cockpit/digest_ticks_test.go`

**Interfaces (produces, package `cockpit`):**

```go
// TimerSlot is bridge-dispatch.timer's period (OnCalendar=*-*-* *:00,30:00).
// TestTimerSlot_MatchesUnitFile fails when the unit changes.
const TimerSlot = 30 * time.Minute

// TickGrace is how long after a slot its tick may take to land before the
// slot counts as expected.
const TickGrace = 5 * time.Minute

// JournalState is the collector's last read of the dispatch journal.
type JournalState struct {
	Present bool                     // the journal dir exists
	Records []dispatch.JournalRecord // oldest first, V == dispatch.JournalVersion
}

type TickGap struct {
	From    time.Time `json:"from"`
	To      time.Time `json:"to"` // last missing slot + TimerSlot
	Missing int       `json:"missing"`
}

type TickHealth struct {
	Present    bool       `json:"present"`
	RangeStart *time.Time `json:"rangeStart,omitempty"`
	Expected   int        `json:"expected"`
	Seen       int        `json:"seen"`
	Gaps       []TickGap  `json:"gaps"`
	Inert      int        `json:"inert"`
	LastInert  *time.Time `json:"lastInert,omitempty"`
}

// BuildTickHealth compares the timer's expected slots in [later of since and
// the first record, now] with the timer records seen, and counts inert ticks
// in [since, now].
func BuildTickHealth(j JournalState, since, now time.Time) TickHealth

// isInert: outcome decided or no_candidates with no repos seen.
func isInert(r dispatch.JournalRecord) bool
```

Implementation notes:
- Return `{Present: j.Present, Gaps: []TickGap{}}` when the journal is not present or has no records.
- `start` is the later of `since` and `j.Records[0].At`.
- The first slot is `start.Truncate(TimerSlot)`: the slot that contains the range start. A fresh install's first timer tick at `S+20s` therefore fills slot `S` instead of starting the range one slot late.
- Walk the slots `S` while `!S.Add(TickGrace).After(now)`.
- Slot `S` is seen iff some record has `Trigger == dispatch.TriggerTimer` and `!At.Before(S) && At.Before(S.Add(TimerSlot))`.
- A missing slot extends the last gap when `gap.To.Equal(S)` (then `To += TimerSlot`, `Missing++`). Otherwise it appends `{S, S+TimerSlot, 1}`.
- Inert counting uses records with `!At.Before(since) && !At.After(now)`.

- [ ] **Step 1: Write the failing tests**

```go
package cockpit

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/freaxnx01/bridge/internal/dispatch"
)

var tickBase = time.Date(2026, 11, 10, 22, 0, 0, 0, time.UTC)

func tick(trigger string, at time.Time, outcome string, repos ...string) dispatch.JournalRecord {
	return dispatch.JournalRecord{V: dispatch.JournalVersion, At: at, Trigger: trigger, Mode: dispatch.ModeLive, Outcome: outcome, Repos: repos}
}

func timerTicks(from time.Time, n int) []dispatch.JournalRecord {
	var out []dispatch.JournalRecord
	for i := range n {
		out = append(out, tick(dispatch.TriggerTimer, from.Add(time.Duration(i)*TimerSlot+20*time.Second), dispatch.OutcomeOutsideWindow))
	}
	return out
}

func TestBuildTickHealth_Table(t *testing.T) {
	// Ticks 22:00..23:00 seen, 23:30..01:30 missing, 02:00 seen.
	gapped := append(timerTicks(tickBase, 3), tick(dispatch.TriggerTimer, tickBase.Add(4*time.Hour+10*time.Second), dispatch.OutcomeDecided, "o/a"))
	tests := []struct {
		name         string
		j            JournalState
		since, now   time.Time
		wantExpected int
		wantSeen     int
		wantGaps     []TickGap
	}{
		{"missing journal", JournalState{}, tickBase, tickBase.Add(time.Hour), 0, 0, []TickGap{}},
		{"all seen", JournalState{Present: true, Records: timerTicks(tickBase, 3)}, tickBase, tickBase.Add(time.Hour + TickGrace), 3, 3, []TickGap{}},
		{"current slot inside grace not expected", JournalState{Present: true, Records: timerTicks(tickBase, 2)}, tickBase, tickBase.Add(time.Hour + time.Minute), 2, 2, []TickGap{}},
		{"gap merged", JournalState{Present: true, Records: gapped}, tickBase, tickBase.Add(4*time.Hour + TickGrace), 9, 4,
			[]TickGap{{From: tickBase.Add(90 * time.Minute), To: tickBase.Add(4 * time.Hour), Missing: 5}}},
		{"range starts at first record, not since", JournalState{Present: true, Records: timerTicks(tickBase, 2)}, tickBase.Add(-6 * time.Hour), tickBase.Add(30*time.Minute + TickGrace), 2, 2, []TickGap{}},
		{"manual records fill no slot", JournalState{Present: true, Records: []dispatch.JournalRecord{
			tick(dispatch.TriggerManual, tickBase.Add(time.Minute), dispatch.OutcomeDecided, "o/a"),
		}}, tickBase, tickBase.Add(30*time.Minute + TickGrace), 2, 0, []TickGap{{From: tickBase, To: tickBase.Add(time.Hour), Missing: 2}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := BuildTickHealth(tt.j, tt.since, tt.now)
			if h.Present != tt.j.Present || h.Expected != tt.wantExpected || h.Seen != tt.wantSeen {
				t.Fatalf("present/expected/seen = %v/%d/%d, want %v/%d/%d", h.Present, h.Expected, h.Seen, tt.j.Present, tt.wantExpected, tt.wantSeen)
			}
			if len(h.Gaps) != len(tt.wantGaps) {
				t.Fatalf("gaps = %+v, want %+v", h.Gaps, tt.wantGaps)
			}
			for i := range h.Gaps {
				if !h.Gaps[i].From.Equal(tt.wantGaps[i].From) || !h.Gaps[i].To.Equal(tt.wantGaps[i].To) || h.Gaps[i].Missing != tt.wantGaps[i].Missing {
					t.Fatalf("gap %d = %+v, want %+v", i, h.Gaps[i], tt.wantGaps[i])
				}
			}
		})
	}
}
```

  In the "manual records fill no slot" case, the first record is at 22:01, so the range starts there. Slots 22:00 (which contains the start) and 22:30 are expected, and neither is filled by the manual record.

```go
func TestBuildTickHealth_Inert_OnlyDecidedOrNoCandidatesWithNoRepos(t *testing.T) {
	recs := []dispatch.JournalRecord{
		tick(dispatch.TriggerTimer, tickBase, dispatch.OutcomeNoCandidates),               // inert
		tick(dispatch.TriggerTimer, tickBase.Add(TimerSlot), dispatch.OutcomeDecided),     // inert
		tick(dispatch.TriggerTimer, tickBase.Add(2*TimerSlot), dispatch.OutcomeDecided, "o/a"),
		tick(dispatch.TriggerTimer, tickBase.Add(3*TimerSlot), dispatch.OutcomePaused),
		tick(dispatch.TriggerTimer, tickBase.Add(4*TimerSlot), dispatch.OutcomeOutsideWindow),
		tick(dispatch.TriggerTimer, tickBase.Add(5*TimerSlot), dispatch.OutcomeError),
	}
	h := BuildTickHealth(JournalState{Present: true, Records: recs}, tickBase, tickBase.Add(3*time.Hour))
	if h.Inert != 2 || h.LastInert == nil || !h.LastInert.Equal(tickBase.Add(TimerSlot)) {
		t.Fatalf("inert = %d, last = %v", h.Inert, h.LastInert)
	}
}

func TestTimerSlot_MatchesUnitFile(t *testing.T) {
	raw, err := os.ReadFile("../../docs/systemd/bridge-dispatch.timer")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "OnCalendar=*-*-* *:00,30:00\n") || TimerSlot != 30*time.Minute {
		t.Fatal("bridge-dispatch.timer's OnCalendar no longer matches TimerSlot: update both together")
	}
}
```

- [ ] **Step 2: Run to verify failure:** `go test ./internal/cockpit -run 'TickHealth|TimerSlot'` should FAIL.
- [ ] **Step 3: Implement** `digest_ticks.go` as specified. Keep each func ≤ 20 lines: `expectedSlots`, `seenAt`, `addMissing`, `isInert`, `inWindow`.
- [ ] **Step 4: Gates.**
- [ ] **Step 5: Commit** `feat(cockpit): compute expected vs. seen dispatcher ticks (#380)`

### Task 3: `BuildDigest` (pure)

**Files:**
- Create: `internal/cockpit/digest.go`, `internal/cockpit/digest_test.go`

**Interfaces (produces, package `cockpit`):**

```go
const (
	EventDigest      = "digest-updated"
	DigestHorizon    = 7 * 24 * time.Hour
	PipelinePRAuthor = "github-actions[bot]" // agent-workflow find-pipeline-pr.sh default allowlist

	BySession = "session" // the other two values are dispatch.TriggerTimer / TriggerManual

	GroupDone    = "done"
	GroupGoing   = "going"
	GroupStopped = "stopped"

	StepDispatched = "dispatched"
	StepRetried    = "retried"
	StepRun        = "run"
	StepPROpened   = "pr_opened"
	StepMerged     = "merged"
	StepPRClosed   = "pr_closed"
)

// ActivityIssue is one issue whose run markers the digest can read: an open
// inbox candidate from the snapshot, or a closed issue a pipeline PR closes.
type ActivityIssue struct {
	Repo     string // "owner/name"
	Number   int
	Title    string
	URL      string
	Runs     []dispatch.RunRecord // oldest first (dispatch.Runs)
	InFlight bool
}

type DigestInput struct {
	Since, Now  time.Time
	Issues      []ActivityIssue
	PRs         map[string][]forge.PullRequest // "owner/name" → recent PRs, any state
	Journal     JournalState
	NeedsYou    map[string]bool // digestKey of every Inbox().Cards entry
	Waiting     map[string]bool // digestKey of every Inbox().Waiting entry
	Truncated   []string
	CollectedAt map[string]time.Time
	Errors      map[string]string
}

type Digest struct {
	Since        time.Time            `json:"since"`
	SinceClamped bool                 `json:"sinceClamped"`
	Now          time.Time            `json:"now"`
	Summary      DigestSummary        `json:"summary"`
	Ticks        TickHealth           `json:"ticks"`
	Items        []DigestItem         `json:"items"`
	DidntHappen  []TickLine           `json:"didntHappen"`
	Journal      []TickLine           `json:"journal"`
	Truncated    []string             `json:"truncated"`
	CollectedAt  map[string]time.Time `json:"collectedAt"`
	Errors       map[string]string    `json:"errors,omitempty"`
}

type DigestSummary struct {
	Dispatched   int            `json:"dispatched"`
	PRsOpened    int            `json:"prsOpened"`
	Merged       int            `json:"merged"`
	Stopped      int            `json:"stopped"`
	SpendUSD     float64        `json:"spendUsd"`
	TickOutcomes map[string]int `json:"tickOutcomes"`
}

type DigestItem struct {
	Repo       string      `json:"repo"`
	Number     int         `json:"number"`
	Title      string      `json:"title"`
	URL        string      `json:"url"`
	Group      string      `json:"group"`
	By         string      `json:"by"`
	InNeedsYou bool        `json:"inNeedsYou"`
	PR         *DigestPR   `json:"pr,omitempty"`
	CostUSD    float64     `json:"costUsd"`
	Last       time.Time   `json:"last"`
	Story      []StoryStep `json:"story"`
}

type DigestPR struct {
	Number   int        `json:"number"`
	URL      string     `json:"url"`
	State    string     `json:"state"` // "open" | "merged" | "closed"
	Draft    bool       `json:"draft"`
	Created  time.Time  `json:"created"`
	MergedAt *time.Time `json:"mergedAt,omitempty"`
}

type StoryStep struct {
	At       time.Time `json:"at"`
	Kind     string    `json:"kind"`
	By       string    `json:"by,omitempty"`
	Lane     string    `json:"lane,omitempty"`
	Status   string    `json:"status,omitempty"`
	Class    string    `json:"class,omitempty"`
	Decision string    `json:"decision,omitempty"`
	Attempt  int       `json:"attempt,omitempty"`
	CostUSD  float64   `json:"costUsd,omitempty"`
	PR       int       `json:"pr,omitempty"`
}

type TickLine struct {
	At            time.Time                  `json:"at"`
	Trigger       string                     `json:"trigger"`
	Mode          string                     `json:"mode"`
	RetryOnly     bool                       `json:"retryOnly,omitempty"`
	Outcome       string                     `json:"outcome"`
	Error         string                     `json:"error,omitempty"`
	Repos         int                        `json:"repos"`
	Dispatched    int                        `json:"dispatched"`
	WouldDispatch int                        `json:"wouldDispatch"`
	NotApplied    int                        `json:"notApplied"`
	Skipped       int                        `json:"skipped"`
	SkipReasons   map[string]int             `json:"skipReasons"`
	Decisions     []dispatch.JournalDecision `json:"decisions"` // non-skipped only
	Inert         bool                       `json:"inert"`
}

func digestKey(repo string, number int) string // "owner/name#n"

// BuildDigest joins journal, markers and PRs for [since, now]. It never
// returns nil slices or maps (Errors aside).
func BuildDigest(in DigestInput) Digest
```

Algorithm (spec §4–6). Split it into helpers of ≤ 20 lines each:
- `clampSince(since, now)`: if `since` is before `now − DigestHorizon`, use that bound and set `SinceClamped`.
- `tickLines(records, since, now)`: newest first. `didntHappen(lines)` keeps lines with `Dispatched == 0 && Outcome != OutcomeOutsideWindow`.
- `pipelinePRs(in.PRs, issues)` gives `map[digestKey][]forge.PullRequest`. A PR belongs to issue N of its repo when:
  - its author is `PipelinePRAuthor` and `dispatch.ClosingIssues(body)` contains N, **or**
  - a run marker on N has `PR == pr.Number`.
- Item candidates are the union of:
  - journal `dispatched` decisions in the window;
  - markers with `Posted` in the window;
  - pipeline PRs with `Created` or `MergedAt` in the window.
- `storySteps(...)` covers:
  - journal dispatches in the window (`retried` when `Kind == dispatch.KindRetry`, `By` = the record's trigger);
  - runs in the window;
  - `pr_opened` / `merged`;
  - `pr_closed` for a closed unmerged PR whose `Updated` is in the window.

  Sort the steps stable by `At`.
- `attribute(...)`:
  - If the first step is a dispatch, use its `By`.
  - Otherwise use the latest journal `dispatched` decision for the key with `At ≤ first.At` and `At` after the latest marker posted before `first.At`. That search covers the **whole** journal, not only the window.
  - Otherwise `BySession`.
- `group(...)` follows the spec rows in order:
  1. A PR merged in the window → done.
  2. The latest marker has `Status == dispatch.LabelAIDone` → done.
  3. `InFlight || Waiting[key] || latest.Decision == "retry"`, or the last step is a dispatch → going.
  4. `latest.Decision == "stop"` → stopped.
  5. Otherwise → going.
- Item `PR` is the pipeline PR with the latest `Created`. `State` is `merged` when `!MergedAt.IsZero()`, otherwise `pr.State`.
- An issue missing from `Issues` takes its title from the journal decision and its URL `https://github.com/<repo>/issues/<n>`.
- Sort items by `Last`, newest first.
- Summary:
  - `Dispatched` = the journal `dispatched` decisions in the window, plus one per item whose `By == BySession`.
  - `PRsOpened` and `Merged` count distinct `repo#pr` pipeline PRs.
  - `Stopped` = items in group stopped.
  - `SpendUSD` = Σ `item.CostUSD`.
  - `TickOutcomes` counts the window's lines by outcome.

- [ ] **Step 1: Write the failing tests** (`digest_test.go`)

```go
package cockpit

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/freaxnx01/bridge/internal/dispatch"
	"github.com/freaxnx01/bridge/internal/forge"
)

var (
	dgNow   = time.Date(2026, 11, 11, 7, 0, 0, 0, time.UTC)
	dgSince = dgNow.Add(-12 * time.Hour) // 19:00 yesterday
)

func jDecision(repo string, n int, decision, kind string) dispatch.JournalDecision {
	owner, name, _ := strings.Cut(repo, "/")
	return dispatch.JournalDecision{Owner: owner, Repo: name, Issue: n, Title: "T", Kind: kind, Lane: "auto", Decision: decision, Reason: "r"}
}

func jRecord(trigger string, at time.Time, ds ...dispatch.JournalDecision) dispatch.JournalRecord {
	return dispatch.JournalRecord{V: dispatch.JournalVersion, At: at, Trigger: trigger, Mode: dispatch.ModeLive,
		Outcome: dispatch.OutcomeDecided, Repos: []string{"o/a"}, Decisions: ds}
}

func run(at time.Time, status, decision string, cost float64, pr int) dispatch.RunRecord {
	return dispatch.RunRecord{Posted: at, Status: status, Decision: decision, CostUSD: cost, PR: pr, Class: "task_failure", Attempt: 1}
}

func pipePR(n, closes int, created time.Time, merged time.Time) forge.PullRequest {
	return forge.PullRequest{Number: n, Author: PipelinePRAuthor, Body: "Closes #" + itoa(closes), State: "open",
		Created: created, Updated: created, MergedAt: merged, URL: "https://x/pull/" + itoa(n)}
}

func itoa(n int) string { return strconv.Itoa(n) }

func onlyItem(t *testing.T, d Digest) DigestItem {
	t.Helper()
	if len(d.Items) != 1 {
		t.Fatalf("items = %+v, want exactly one", d.Items)
	}
	return d.Items[0]
}

func TestBuildDigest_GroupAndBy_Table(t *testing.T) {
	at := func(h int) time.Time { return dgSince.Add(time.Duration(h) * time.Hour) }
	dispatchTimer := JournalState{Present: true, Records: []dispatch.JournalRecord{
		jRecord(dispatch.TriggerTimer, at(1), jDecision("o/a", 5, dispatch.DecisionDispatched, dispatch.KindDispatch)),
	}}
	tests := []struct {
		name      string
		in        DigestInput
		wantGroup string
		wantBy    string
	}{
		{"self-merged, closed issue, timer", DigestInput{
			Issues:  []ActivityIssue{{Repo: "o/a", Number: 5, Title: "T", Runs: []dispatch.RunRecord{run(at(2), "ai:done", "", 2.5, 9)}}},
			PRs:     map[string][]forge.PullRequest{"o/a": {pipePR(9, 5, at(2), at(3))}},
			Journal: dispatchTimer,
		}, GroupDone, dispatch.TriggerTimer},
		{"ai:done with open PR", DigestInput{
			Issues:  []ActivityIssue{{Repo: "o/a", Number: 5, Runs: []dispatch.RunRecord{run(at(2), "ai:done", "", 1, 9)}}},
			PRs:     map[string][]forge.PullRequest{"o/a": {pipePR(9, 5, at(2), time.Time{})}},
			Journal: dispatchTimer,
		}, GroupDone, dispatch.TriggerTimer},
		{"retrying", DigestInput{
			Issues:  []ActivityIssue{{Repo: "o/a", Number: 5, Runs: []dispatch.RunRecord{run(at(2), "ai:failed", "retry", 1, 0)}}},
			Journal: dispatchTimer,
		}, GroupGoing, dispatch.TriggerTimer},
		{"dispatched, no run yet", DigestInput{Journal: dispatchTimer}, GroupGoing, dispatch.TriggerTimer},
		{"stopped, run by a session", DigestInput{
			Issues: []ActivityIssue{{Repo: "o/a", Number: 5, Runs: []dispatch.RunRecord{run(at(2), "ai:failed", "stop", 3, 0)}}},
		}, GroupStopped, BySession},
		{"manual dispatch before since still attributes", DigestInput{
			Issues: []ActivityIssue{{Repo: "o/a", Number: 5, Runs: []dispatch.RunRecord{run(at(1), "ai:failed", "stop", 1, 0)}}},
			Journal: JournalState{Present: true, Records: []dispatch.JournalRecord{
				jRecord(dispatch.TriggerManual, at(-2), jDecision("o/a", 5, dispatch.DecisionDispatched, dispatch.KindDispatch)),
			}},
		}, GroupStopped, dispatch.TriggerManual},
		{"in flight", DigestInput{
			Issues:  []ActivityIssue{{Repo: "o/a", Number: 5, InFlight: true, Runs: []dispatch.RunRecord{run(at(2), "ai:failed", "stop", 1, 0)}}},
			Journal: dispatchTimer,
		}, GroupGoing, dispatch.TriggerTimer},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.in.Since, tt.in.Now = dgSince, dgNow
			it := onlyItem(t, BuildDigest(tt.in))
			if it.Group != tt.wantGroup || it.By != tt.wantBy {
				t.Fatalf("group/by = %s/%s, want %s/%s (story %+v)", it.Group, it.By, tt.wantGroup, tt.wantBy, it.Story)
			}
		})
	}
}

func TestBuildDigest_Summary_CountsWindowOnly(t *testing.T) {
	at := func(h int) time.Time { return dgSince.Add(time.Duration(h) * time.Hour) }
	in := DigestInput{
		Since: dgSince, Now: dgNow,
		Issues: []ActivityIssue{
			{Repo: "o/a", Number: 5, Runs: []dispatch.RunRecord{run(at(-3), "ai:failed", "retry", 7, 0), run(at(2), "ai:done", "", 2.5, 9)}},
			{Repo: "o/a", Number: 6, Runs: []dispatch.RunRecord{run(at(4), "ai:failed", "stop", 1.25, 0)}},
		},
		PRs: map[string][]forge.PullRequest{"o/a": {pipePR(9, 5, at(2), at(3)), pipePR(3, 1, at(-30), time.Time{})}},
		Journal: JournalState{Present: true, Records: []dispatch.JournalRecord{
			jRecord(dispatch.TriggerTimer, at(1),
				jDecision("o/a", 5, dispatch.DecisionDispatched, dispatch.KindDispatch),
				jDecision("o/a", 7, dispatch.DecisionWouldDispatch, dispatch.KindDispatch),
				jDecision("o/a", 8, dispatch.DecisionSkipped, dispatch.KindDispatch)),
		}},
		NeedsYou: map[string]bool{"o/a#6": true},
	}
	d := BuildDigest(in)
	s := d.Summary
	// #5 timer-dispatched, #6 run by a session; #7 would-dispatch and #8 skipped make no item.
	if s.Dispatched != 2 || s.PRsOpened != 1 || s.Merged != 1 || s.Stopped != 1 || s.SpendUSD != 3.75 {
		t.Fatalf("summary = %+v", s)
	}
	if len(d.Items) != 2 || d.Items[0].Number != 6 || !d.Items[0].InNeedsYou || d.Items[1].InNeedsYou {
		t.Fatalf("items = %+v", d.Items)
	}
	if s.TickOutcomes[dispatch.OutcomeDecided] != 1 {
		t.Fatalf("tickOutcomes = %v", s.TickOutcomes)
	}
	line := d.Journal[0]
	if line.Dispatched != 1 || line.WouldDispatch != 1 || line.Skipped != 1 || line.SkipReasons["r"] != 1 || len(line.Decisions) != 2 {
		t.Fatalf("journal line = %+v", line)
	}
	if len(d.DidntHappen) != 0 {
		t.Fatalf("didntHappen = %+v, want none (the tick dispatched)", d.DidntHappen)
	}
}

func TestBuildDigest_DidntHappen_SkipsOutsideWindowAndDispatchingTicks(t *testing.T) {
	quiet := jRecord(dispatch.TriggerTimer, dgSince.Add(time.Hour), jDecision("o/a", 7, dispatch.DecisionWouldDispatch, dispatch.KindDispatch))
	outside := dispatch.JournalRecord{V: 1, At: dgSince.Add(2 * time.Hour), Trigger: dispatch.TriggerTimer, Outcome: dispatch.OutcomeOutsideWindow}
	d := BuildDigest(DigestInput{Since: dgSince, Now: dgNow, Journal: JournalState{Present: true, Records: []dispatch.JournalRecord{quiet, outside}}})
	if len(d.DidntHappen) != 1 || !d.DidntHappen[0].At.Equal(quiet.At) || len(d.Journal) != 2 || !d.Journal[0].At.Equal(outside.At) {
		t.Fatalf("didntHappen = %+v, journal = %+v", d.DidntHappen, d.Journal)
	}
}

func TestBuildDigest_SinceOlderThanHorizon_Clamped(t *testing.T) {
	d := BuildDigest(DigestInput{Since: dgNow.Add(-30 * 24 * time.Hour), Now: dgNow})
	if !d.SinceClamped || !d.Since.Equal(dgNow.Add(-DigestHorizon)) {
		t.Fatalf("since = %v clamped = %v", d.Since, d.SinceClamped)
	}
}

func TestBuildDigest_HandWrittenPR_NotCounted(t *testing.T) {
	pr := pipePR(4, 5, dgSince.Add(time.Hour), dgSince.Add(2*time.Hour))
	pr.Author = "freaxnx01"
	d := BuildDigest(DigestInput{Since: dgSince, Now: dgNow, PRs: map[string][]forge.PullRequest{"o/a": {pr}}})
	if d.Summary.PRsOpened != 0 || d.Summary.Merged != 0 || len(d.Items) != 0 {
		t.Fatalf("a hand-written PR counted: %+v", d)
	}
}

func TestBuildDigest_Empty_SerializesEmptyArrays(t *testing.T) {
	raw, err := json.Marshal(BuildDigest(DigestInput{Since: dgSince, Now: dgNow}))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{`"items":[]`, `"didntHappen":[]`, `"journal":[]`, `"truncated":[]`, `"gaps":[]`, `"tickOutcomes":{}`, `"present":false`} {
		if !strings.Contains(string(raw), f) {
			t.Errorf("missing %s in %s", f, raw)
		}
	}
}
```

- [ ] **Step 2: Run to verify failure:** `go test ./internal/cockpit -run 'BuildDigest'` should FAIL.
- [ ] **Step 3: Implement** `digest.go` with the algorithm above.
- [ ] **Step 4: Gates.**
- [ ] **Step 5: Commit** `feat(cockpit): build the overnight digest from journal, markers and PRs (#380)`

### Task 4: Collector — digest sources, closed threads, `digest-updated`

**Files:**
- Create: `internal/cockpit/digest_collect.go`, `internal/cockpit/digest_collect_test.go`
- Modify: `internal/cockpit/collector.go` (the named hooks only)

**Hooks in `collector.go` (the only edits there):**
1. Append three fields at the end of `Sources`, each with a doc comment saying it may be nil:
   ```go
   RecentPRs    func(ctx context.Context, r core.Repo) ([]forge.PullRequest, error) // one page, any state, updated desc
   Journal      func() (records []dispatch.JournalRecord, present bool, err error)
   JournalStamp func() (string, error) // changes whenever a record is appended
   ```
2. Add one field at the end of `Collector`: `digest digestState`.
3. In `CollectIssues`, between `c.pruneComments(next)` and `c.publish()`, add `c.collectRecent(ctx)`.
4. In `CollectRuns`, before its `c.publish()`, add `c.collectJournal()`.
5. In `publish`, compute `digest := c.digestChanged()` after `rebuildLocked` and outside `c.mu`. Then, after the existing events, add `if digest { c.onChange(EventDigest) }`.

**Interfaces (produces, in `digest_collect.go`):**

```go
type closedThread struct {
	prUpdated time.Time
	issue     forge.Issue
	comments  []forge.Comment
}

// digestState is the digest's own data, guarded by its own mutex. Lock order:
// c.mu before digest.mu, never the reverse.
type digestState struct {
	mu          sync.Mutex
	prs         map[string][]forge.PullRequest
	truncated   map[string]bool
	closed      map[issueKey]closedThread
	journal     JournalState
	stamp       string
	collectedAt map[string]time.Time // "prs", "journal"
	errs        map[string]string
	fingerprint [sha256.Size]byte
}

func (c *Collector) collectRecent(ctx context.Context) // no-op when RecentPRs is nil
func (c *Collector) collectJournal()                   // no-op when Journal or JournalStamp is nil
func (c *Collector) digestChanged() bool               // fingerprint of the inputs, not of a since-dependent payload

// Digest builds the digest for [since, now] from collector state alone; it
// never calls a Sources func.
func (c *Collector) Digest(since time.Time) Digest
```

Implementation notes:
- `collectRecent`:
  1. Under `c.mu`, take the wired GitHub repos from `c.repos` and the open issue numbers of each.
  2. `fanOut` over them. Each call runs `RecentPRs` and keeps the PRs with `Updated ≥ now − DigestHorizon`. A repo is truncated when the raw page holds 100 PRs and the last one's `Updated` is inside the horizon.
  3. For each kept PR with `Author == PipelinePRAuthor`, take every `ClosingIssues` number that is **not** open. Reuse the cached `closedThread` when its `prUpdated` equals `pr.Updated`. Otherwise call `c.src.Issue(ctx, r, n)`.
  4. On error, keep the stale thread and set `errs["closed o/n#k"]`.
  5. Under `digest.mu`, swap in the results. On a `RecentPRs` error, keep the last good PRs and set `errs["prs o/n"]`. Delete the error key on success. Prune closed entries that no kept PR references. Set `collectedAt["prs"]`.
- `collectJournal`:
  1. Call `JournalStamp`. On error, set `errs["journal"]` and return.
  2. If the stamp equals the stored one and `collectedAt["journal"]` is set, return.
  3. Otherwise call `Journal`. Drop records whose `V != dispatch.JournalVersion`. Store `JournalState{Present, Records}`, the stamp and `collectedAt["journal"]`, and delete or set `errs["journal"]`.
- The `digestInput` helper:
  1. Under `c.mu`, build `ActivityIssue`s for every wired GitHub repo's issues whose `Comments` were read: `dispatch.Runs(comments)` and `inFlight(i, rd.Runs)`. Take `NeedsYou`/`Waiting` from `c.inbox.Cards` / `c.inbox.Waiting`, and `collectedAt["issues"]`.
  2. Under `digest.mu`, add closed threads not already open, the PRs, the journal, the sorted truncated repos and the errors (all copied).
- `digestChanged` hashes `json.Marshal` of the `digestInput` with `Journal.Records` replaced by the stamp and `CollectedAt` cleared. It returns whether the hash differs from the stored one, and stores the new hash.

- [ ] **Step 1: Write the failing tests** (`digest_collect_test.go`; reuse `newWorld`, `ghRepo`, `gh`, `marker`, `events`, `newClock` from the existing tests)

```go
package cockpit

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/freaxnx01/bridge/internal/core"
	"github.com/freaxnx01/bridge/internal/dispatch"
	"github.com/freaxnx01/bridge/internal/forge"
)

// digestWorld adds the digest sources on top of fakeWorld.
type digestWorld struct {
	*fakeWorld
	mu        sync.Mutex
	prs       map[string][]forge.PullRequest
	prsErr    error
	recCalls  int
	journal   []dispatch.JournalRecord
	present   bool
	stamp     string
	jCalls    int
}

func (d *digestWorld) sources() Sources {
	s := d.fakeWorld.sources()
	s.RecentPRs = func(_ context.Context, r core.Repo) ([]forge.PullRequest, error) {
		d.mu.Lock()
		defer d.mu.Unlock()
		d.recCalls++
		return d.prs[key(r)], d.prsErr
	}
	s.Journal = func() ([]dispatch.JournalRecord, bool, error) {
		d.mu.Lock()
		defer d.mu.Unlock()
		d.jCalls++
		return d.journal, d.present, nil
	}
	s.JournalStamp = func() (string, error) {
		d.mu.Lock()
		defer d.mu.Unlock()
		return d.stamp, nil
	}
	return s
}

func newDigestWorld() *digestWorld {
	w := newWorld(ghRepo("a"), ghRepo("unwired"))
	w.wired["o/a"] = true
	return &digestWorld{fakeWorld: w, prs: map[string][]forge.PullRequest{}}
}

func TestCollector_CollectIssues_RecentPRsOnlyForWiredGitHub(t *testing.T) {
	d := newDigestWorld()
	c := NewCollector(d.sources(), DefaultIntervals(), fixedNow, nil)
	c.CollectIssues(context.Background())
	if d.recCalls != 1 {
		t.Fatalf("RecentPRs calls = %d, want 1 (wired o/a only)", d.recCalls)
	}
}

func TestCollector_ClosedIssueThread_ReadOnceUntilPRUpdatedMoves(t *testing.T) {
	d := newDigestWorld()
	clk := newClock(t0)
	merged := forge.PullRequest{Number: 9, Author: PipelinePRAuthor, Body: "Closes #5", State: "closed",
		Created: t0.Add(-2 * time.Hour), Updated: t0.Add(-time.Hour), MergedAt: t0.Add(-time.Hour)}
	d.prs["o/a"] = []forge.PullRequest{merged} // no collector goroutine runs yet
	d.set(func() {
		d.comments[issueKeyOf(ghRepo("a"), 5)] = []forge.Comment{marker(`{"v":1,"status":"ai:done","cost_usd":2.5,"pr":9}`, t0.Add(-90*time.Minute))}
	})
	c := NewCollector(d.sources(), DefaultIntervals(), clk.now, nil)

	c.CollectIssues(context.Background())
	c.CollectIssues(context.Background())
	if got := d.calls(d.detailCalls, issueKeyOf(ghRepo("a"), 5)); got != 1 {
		t.Fatalf("closed thread reads = %d, want 1", got)
	}
	dg := c.Digest(t0.Add(-12 * time.Hour))
	if dg.Summary.Merged != 1 || dg.Summary.SpendUSD != 2.5 || len(dg.Items) != 1 || dg.Items[0].Group != GroupDone {
		t.Fatalf("digest = %+v", dg)
	}

	merged.Updated = t0.Add(-30 * time.Minute)
	d.mu.Lock()
	d.prs["o/a"] = []forge.PullRequest{merged}
	d.mu.Unlock()
	c.CollectIssues(context.Background())
	if got := d.calls(d.detailCalls, issueKeyOf(ghRepo("a"), 5)); got != 2 {
		t.Fatalf("closed thread reads after PR update = %d, want 2", got)
	}
}

func TestCollector_Journal_ReReadOnlyWhenStampChanges(t *testing.T) {
	d := newDigestWorld()
	d.present, d.stamp = true, "s1"
	d.journal = []dispatch.JournalRecord{{V: 1, At: t0, Trigger: dispatch.TriggerTimer, Outcome: dispatch.OutcomeDecided}, {V: 2, At: t0}}
	c := NewCollector(d.sources(), DefaultIntervals(), fixedNow, nil)

	c.CollectRuns(context.Background())
	c.CollectRuns(context.Background())
	if d.jCalls != 1 {
		t.Fatalf("journal reads = %d, want 1", d.jCalls)
	}
	if dg := c.Digest(t0.Add(-time.Hour)); !dg.Ticks.Present || len(dg.Journal) != 1 {
		t.Fatalf("ticks = %+v, journal = %+v (unknown v must be dropped)", dg.Ticks, dg.Journal)
	}
	d.mu.Lock()
	d.stamp = "s2"
	d.mu.Unlock()
	c.CollectRuns(context.Background())
	if d.jCalls != 2 {
		t.Fatalf("journal reads after stamp change = %d, want 2", d.jCalls)
	}
}

func TestCollector_NilDigestSources_DigestStillServed(t *testing.T) {
	w := newWorld(ghRepo("a"))
	c := NewCollector(w.sources(), DefaultIntervals(), fixedNow, nil)
	c.CollectIssues(context.Background())
	c.CollectRuns(context.Background())
	if dg := c.Digest(t0.Add(-time.Hour)); dg.Ticks.Present || dg.Items == nil {
		t.Fatalf("digest = %+v", dg)
	}
}

func TestCollector_DigestEvent_FiresOnInputChangeOnly(t *testing.T) {
	d := newDigestWorld()
	d.present, d.stamp = true, "s1"
	var ev events
	c := NewCollector(d.sources(), DefaultIntervals(), fixedNow, ev.on)
	c.CollectIssues(context.Background())
	c.CollectRuns(context.Background())
	ev.reset()

	c.CollectRuns(context.Background())
	c.CollectSessions(context.Background())
	if n := ev.count(EventDigest); n != 0 {
		t.Fatalf("digest-updated fired %d times with no input change", n)
	}
	d.mu.Lock()
	d.stamp = "s2"
	d.journal = []dispatch.JournalRecord{{V: 1, At: t0, Trigger: dispatch.TriggerTimer, Outcome: dispatch.OutcomeNoCandidates}}
	d.mu.Unlock()
	c.CollectRuns(context.Background())
	if n := ev.count(EventDigest); n != 1 {
		t.Fatalf("digest-updated fired %d times after a journal append, want 1", n)
	}
}

func TestCollector_RecentPRsError_KeepsLastGoodAndReports(t *testing.T) {
	d := newDigestWorld()
	d.prs["o/a"] = []forge.PullRequest{{Number: 9, Author: PipelinePRAuthor, Body: "Closes #1", Created: t0.Add(-time.Hour), Updated: t0.Add(-time.Hour)}}
	c := NewCollector(d.sources(), DefaultIntervals(), fixedNow, nil)
	c.CollectIssues(context.Background())
	d.mu.Lock()
	d.prsErr = errors.New("boom")
	d.mu.Unlock()
	c.CollectIssues(context.Background())
	dg := c.Digest(t0.Add(-2 * time.Hour))
	if dg.Errors["prs o/a"] != "boom" || dg.Summary.PRsOpened != 1 {
		t.Fatalf("errors = %v, prsOpened = %d", dg.Errors, dg.Summary.PRsOpened)
	}
}
```

  For `d.set`, `d.calls` and `d.comments`: `digestWorld` embeds `*fakeWorld`, so these resolve to the fake's helpers. The fake's `Issue` func serves `w.comments[k]` for the closed issue, even though it is not listed open. The test's own `d.mu` is separate from `fakeWorld.mu`.

- [ ] **Step 2: Run to verify failure:** `go test ./internal/cockpit -run 'Collector_(CollectIssues_Recent|ClosedIssue|Journal|NilDigest|DigestEvent|RecentPRsError)'` should FAIL.
- [ ] **Step 3: Implement** `digest_collect.go` and the five hooks.
- [ ] **Step 4:** Add `TestCollector_Digest_NoSourceCalls`:
  1. Collect once and snapshot the fake's call counters.
  2. Call `c.Digest(...)` ten times.
  3. Assert that no counter moved: `issueCalls`, `prCalls`, `runCalls`, `detailCalls`, `recCalls`, `jCalls`.
- [ ] **Step 5: Gates**, including the whole existing `internal/cockpit` suite, **unmodified**, under `-race`.
- [ ] **Step 6: Commit** `feat(cockpit): collect recent PRs, closed threads and the journal for the digest (#380)`

### Task 5: `GET /api/digest` handler

**Files:**
- Create: `internal/api/digest.go`, `internal/api/digest_test.go`

**Interfaces (produces, package `api`):**

```go
// DigestHandler serves GET /api/digest?since=<RFC3339>, open and read-only.
type DigestHandler struct {
	Digest func(since time.Time) cockpit.Digest
	Now    func() time.Time
}

func (h *DigestHandler) ServeHTTP(w http.ResponseWriter, r *http.Request)
```

Rules:
- Anything but GET → `allowGet` (405 + `Allow: GET`).
- `since` missing → 400 `since is required`.
- `since` that does not parse as `time.RFC3339` → 400 `since must be RFC 3339`.
- `since` after `Now()+1m` → 400 `since is in the future`.
- Otherwise `writeJSON(w, h.Digest(since))`.

- [ ] **Step 1: Write the failing tests**

```go
package api

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/freaxnx01/bridge/internal/cockpit"
)

var digestNow = time.Date(2026, 11, 11, 7, 0, 0, 0, time.UTC)

func newDigestFixture() (*DigestHandler, *[]time.Time) {
	var got []time.Time
	return &DigestHandler{
		Digest: func(since time.Time) cockpit.Digest {
			got = append(got, since)
			return cockpit.BuildDigest(cockpit.DigestInput{Since: since, Now: digestNow})
		},
		Now: func() time.Time { return digestNow },
	}, &got
}

func TestDigestHandler_Status_Table(t *testing.T) {
	tests := []struct {
		name, method, path string
		want               int
	}{
		{"ok", http.MethodGet, "/api/digest?since=2026-11-10T18:00:00Z", http.StatusOK},
		{"offset ok", http.MethodGet, "/api/digest?since=2026-11-10T19:00:00%2B01:00", http.StatusOK},
		{"missing", http.MethodGet, "/api/digest", http.StatusBadRequest},
		{"malformed", http.MethodGet, "/api/digest?since=yesterday", http.StatusBadRequest},
		{"future", http.MethodGet, "/api/digest?since=2026-11-11T08:00:00Z", http.StatusBadRequest},
		{"post", http.MethodPost, "/api/digest?since=2026-11-10T18:00:00Z", http.StatusMethodNotAllowed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, _ := newDigestFixture()
			if w := doReq(h, tt.method, tt.path, ""); w.Code != tt.want {
				t.Fatalf("status = %d, want %d (%s)", w.Code, tt.want, w.Body)
			}
		})
	}
}

func TestDigestHandler_PassesSinceAndServesJSON(t *testing.T) {
	h, got := newDigestFixture()
	w := doReq(h, http.MethodGet, "/api/digest?since=2026-11-10T18:00:00Z", "")
	if len(*got) != 1 || !(*got)[0].Equal(time.Date(2026, 11, 10, 18, 0, 0, 0, time.UTC)) {
		t.Fatalf("since passed = %v", *got)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") || !strings.Contains(w.Body.String(), `"items":[]`) {
		t.Fatalf("content-type %q body %s", ct, w.Body)
	}
}
```

- [ ] **Step 2: Run to verify failure:** `go test ./internal/api -run Digest` should FAIL.
- [ ] **Step 3: Implement** `digest.go`, with each check as a guard clause.
- [ ] **Step 4: Gates.**
- [ ] **Step 5: Commit** `feat(serve): add GET /api/digest (#380)`

### Task 6: Wire sources and route in `cmd/bridge`

**Files:**
- Create: `cmd/bridge/digest_sources.go`, `cmd/bridge/digest_sources_test.go`
- Modify: `cmd/bridge/pipeline.go` (`cockpitSources`: three new entries), `cmd/bridge/serve.go` (one route line after `/api/inbox/`)

**Interfaces (produces, package `main`):**

```go
// readDispatchJournal reads the whole journal; a missing dir is not present.
func readDispatchJournal(dir string) ([]dispatch.JournalRecord, bool, error)

// journalStamp is "<newest .jsonl name>:<size>:<mtime ns>:<entries>", "missing"
// for no dir, "empty" for no day file. It changes on every append and prune.
func journalStamp(dir string) (string, error)
```

In `cockpitSources`:

```go
RecentPRs: func(ctx context.Context, r core.Repo) ([]forge.PullRequest, error) {
	gh, err := githubOnly(resolve, r)
	if gh == nil {
		return nil, err
	}
	return gh.ListPullRequests(ctx, r.Owner, r.Name, "all")
},
Journal:      func() ([]dispatch.JournalRecord, bool, error) { return readDispatchJournal(dispatchJournalDir()) },
JournalStamp: func() (string, error) { return journalStamp(dispatchJournalDir()) },
```

In `serve.go`, directly after `apiMux.Handle("/api/inbox/", inboxH)`:

```go
apiMux.Handle("/api/digest", &api.DigestHandler{Digest: collector.Digest, Now: time.Now}) // open, read-only: served from the collector, never the forge
```

- [ ] **Step 1: Write the failing tests**

```go
package main

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/freaxnx01/bridge/internal/dispatch"
)

func TestReadDispatchJournal_MissingDir_NotPresent(t *testing.T) {
	recs, present, err := readDispatchJournal(filepath.Join(t.TempDir(), "nope"))
	if err != nil || present || recs != nil {
		t.Fatalf("got %v, %v, %v", recs, present, err)
	}
}

func TestReadDispatchJournal_RoundTrip_Present(t *testing.T) {
	dir := t.TempDir()
	at := time.Date(2026, 11, 11, 2, 0, 0, 0, time.UTC)
	if err := dispatch.AppendJournal(dir, dispatch.JournalRecord{At: at, Trigger: dispatch.TriggerTimer, Outcome: dispatch.OutcomeNoCandidates}); err != nil {
		t.Fatal(err)
	}
	recs, present, err := readDispatchJournal(dir)
	if err != nil || !present || len(recs) != 1 || !recs[0].At.Equal(at) {
		t.Fatalf("got %v, %v, %v", recs, present, err)
	}
}

func TestJournalStamp_ChangesOnAppend(t *testing.T) {
	dir := t.TempDir()
	if s, err := journalStamp(filepath.Join(dir, "nope")); err != nil || s != "missing" {
		t.Fatalf("missing: %q %v", s, err)
	}
	if s, err := journalStamp(dir); err != nil || s != "empty" {
		t.Fatalf("empty: %q %v", s, err)
	}
	at := time.Date(2026, 11, 11, 2, 0, 0, 0, time.UTC)
	rec := dispatch.JournalRecord{At: at, Trigger: dispatch.TriggerTimer, Outcome: dispatch.OutcomeNoCandidates}
	if err := dispatch.AppendJournal(dir, rec); err != nil {
		t.Fatal(err)
	}
	s1, err := journalStamp(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := dispatch.AppendJournal(dir, rec); err != nil {
		t.Fatal(err)
	}
	s2, err := journalStamp(dir)
	if err != nil {
		t.Fatal(err)
	}
	if s1 == s2 || s1 == "empty" {
		t.Fatalf("stamp did not change: %q → %q", s1, s2)
	}
}
```

  Also extend `TestCockpitSources_NoClientForOwner_ReportsError`'s pattern into a **new** test `TestCockpitSources_RecentPRs_NoClientForOwner_ReportsError`, so the existing test stays unmodified. It asserts `errors.Is(err, errNoForgeClient)`. Then add `TestCockpitSources_RecentPRs_RequestsStateAll`:
  1. Wire an `httptest` GitHub that records `r.URL.RawQuery` and answers `[]`.
  2. Point a `forge.NewGithubClient("t", srv.URL)` resolver at it.
  3. Assert the query contains `state=all`.

- [ ] **Step 2: Run to verify failure:** `go test ./cmd/bridge -run 'DispatchJournal|JournalStamp|RecentPRs'` should FAIL.
- [ ] **Step 3: Implement**, then run `go build ./...`.
- [ ] **Step 4: Gates** (`cmd/bridge` takes about 150 s, so use `-run` while iterating and the full suite before commit).
- [ ] **Step 5: Commit** `feat(serve): wire the digest sources and route (#380)`

### Task 7: Docs

**Files:**
- Modify: `docs/cockpit.md`, `CHANGELOG.md`

- [ ] **Step 1:** In `docs/cockpit.md`, make these changes:
  1. Add a `### GET /api/digest` section after `GET /api/inbox/{owner}/{repo}/{number}`. Give it:
     - the JSON shape, abbreviated like the inbox one: `since`, `sinceClamped`, `now`, `summary`, `ticks`, `items[]` with `story[]`, `didntHappen[]`, `journal[]`, `truncated[]`, `collectedAt`, `errors`;
     - the status table: 200 / 400 / 405;
     - the item rule, the group rules, `by`, and the counts table from the spec §4–5.
  2. Under **SSE**, add `digest-updated`. It fires on input change only: a gap that appears with time alone shows on the page's next poll.
  3. Under **Refresh intervals and rate limits**, add:
     - recent PRs: one `state=all` page per wired GitHub repo per issues tick;
     - closed-issue threads: once per pipeline PR `Updated`;
     - the journal: a local file, re-read on the runs tick when its stamp changes.
  4. Under **Known limits**, add:
     - the 7-day horizon (`sinceClamped`);
     - `truncated`;
     - a run on an issue closed without a pipeline PR is not seen;
     - pipeline PRs are recognised by author `github-actions[bot]` or a marker's `pr`.
- [ ] **Step 2:** In `CHANGELOG.md` `[Unreleased]` → `Added`, add: "`GET /api/digest`: dispatch and pipeline activity since a timestamp, with tick health (#380)."
- [ ] **Step 3: Gates** (unchanged code, so a quick `go test ./internal/cockpit ./internal/api` is enough). **Commit** `docs(cockpit): document GET /api/digest (#380)`
