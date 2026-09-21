# Dispatcher Autonomy Lanes — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give `bridge dispatch` per-repo autonomy lanes, so repos whose PRs merge themselves dispatch on their own schedule and caps instead of consuming the operator's review capacity.

**Architecture:** Lanes are config, matched per repo by glob, first match wins. A single ordered `ApplyCaps` walk stays the only cap arithmetic; a candidate's resolved lane selects which bounds apply and which labels a dispatch writes. All decision logic stays pure in `internal/dispatch`; the one new side effect — reading a repo's `agent.yml` to verify it opts into AI merge — lives in `cmd/bridge` behind a cached lookup.

**Tech Stack:** Go (stdlib only — `encoding/json`, `path`, `regexp`, `time`), Cobra CLI, stdlib `testing` with table-driven subtests and hand-rolled fakes.

**Spec:** [`docs/specs/2026-09-21-dispatch-autonomy-lanes-design.md`](../specs/2026-09-21-dispatch-autonomy-lanes-design.md)

## Global Constraints

- **Additive only.** A `dispatch.json` with no `lanes` key must behave exactly as it does today, via an implicit default lane. Every existing test in `internal/dispatch` and `cmd/bridge` must stay green without being edited to accommodate lanes.
- **No new Go module.** The `agent.yml` gate is a `regexp` line match, never a YAML parser. Do not add a dependency; do not change the `go 1.x` line.
- **Purity.** Everything in `internal/dispatch` stays a pure function over plain structs — no clock, no network, no filesystem. `time.Time` is always a parameter. Fetching and caching live in `cmd/bridge`.
- **Fail closed toward human review.** A missing `agent.yml`, a non-matching one, or a failed fetch all downgrade the repo to a non-autonomous lane. Never the other way.
- **The budget rung stays global.** Lane windows are `{from, to}` only. Do not add `budget_rung` to a lane window; the rung is computed from the top-level schedule as `resolveTiming` already does.
- **A dry-run lane observes shared counters but never consumes them** — not budget spend, not the global count, not the per-repo count. Only its own lane counter advances.
- Exact label strings: `ai-implement`, `ai-review-ai-merge`.
- Required green after every task: `gofmt -l .` empty, `go vet ./...`, `golangci-lint run`, `go test -race ./...`.

---

### Task 1: Lane config types and loading

**Files:**
- Modify: `internal/dispatch/types.go`
- Modify: `internal/dispatch/config.go`
- Modify: `internal/dispatch/eligible.go` (label const)
- Test: `internal/dispatch/config_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `Span{From, To string}`; `Window` embedding `Span` plus `BudgetRung bool`; `LaneLimits{PerRepo int, Overrides map[string]int, MaxDispatches int}`; `Lane{Name string, Repos []string, Autonomous bool, DryRun bool, Windows []Span, Labels []string, Limits LaneLimits}`; `Config.Lanes []Lane`; `func DefaultLane() Lane`; `func (l Lane) EffectiveLabels() []string`; const `LabelAIReviewAIMerge = "ai-review-ai-merge"`.

- [ ] **Step 1: Write the failing test**

Append to `internal/dispatch/config_test.go`:

```go
func TestLoadConfigLanes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dispatch.json")
	os.WriteFile(path, []byte(`{"lanes":[
		{"name":"auto","repos":["game-*"],"autonomous":true,"dry_run":true,
		 "windows":[{"from":"00:00","to":"00:00"}],
		 "limits":{"per_repo":1,"max_dispatches":12}},
		{"name":"hitl","repos":["*"]}]}`), 0o600)

	c, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Lanes) != 2 {
		t.Fatalf("lanes: %+v", c.Lanes)
	}
	auto := c.Lanes[0]
	if auto.Name != "auto" || !auto.Autonomous || !auto.DryRun {
		t.Errorf("auto lane: %+v", auto)
	}
	if len(auto.Windows) != 1 || auto.Windows[0].From != "00:00" {
		t.Errorf("lane windows: %+v", auto.Windows)
	}
	if auto.Limits.MaxDispatches != 12 || auto.Limits.PerRepo != 1 {
		t.Errorf("lane limits: %+v", auto.Limits)
	}
	// Lanes are additive: the top-level limits a lane does not restate stay put.
	if c.Limits.GlobalOpenPRs != 3 {
		t.Errorf("top-level limits must survive a lanes-only config: %+v", c.Limits)
	}
}

func TestDefaultConfigHasNoLanes(t *testing.T) {
	// The zero-config case must keep pre-lane behaviour, which the implicit
	// DefaultLane provides. A default lane list would be a silent policy change.
	if got := DefaultConfig().Lanes; len(got) != 0 {
		t.Errorf("default config must not configure lanes: %+v", got)
	}
}

func TestEffectiveLabels(t *testing.T) {
	tests := []struct {
		name string
		lane Lane
		want []string
	}{
		{"autonomous defaults to both labels", Lane{Autonomous: true},
			[]string{LabelAIImplement, LabelAIReviewAIMerge}},
		{"plain lane defaults to the trigger alone", Lane{},
			[]string{LabelAIImplement}},
		{"an explicit list wins", Lane{Autonomous: true, Labels: []string{"ai-implement", "ai-review-human-merge"}},
			[]string{"ai-implement", "ai-review-human-merge"}},
		{"the implicit default lane applies the trigger alone", DefaultLane(),
			[]string{LabelAIImplement}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.lane.EffectiveLabels()
			if !slices.Equal(got, tc.want) {
				t.Errorf("got %v want %v", got, tc.want)
			}
		})
	}
}
```

Add `"slices"` to that file's imports.

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/dispatch/ -run 'TestLoadConfigLanes|TestDefaultConfigHasNoLanes|TestEffectiveLabels' -v`
Expected: FAIL to compile — `undefined: Lane`, `undefined: DefaultLane`, `undefined: LabelAIReviewAIMerge`.

- [ ] **Step 3: Split `Window` into `Span` + rung flag**

In `internal/dispatch/types.go`, replace the `Window` declaration with:

```go
// Span is a range of the local day. From is inclusive, To exclusive; From > To
// wraps past midnight; From == To covers the whole day. It is split out of
// Window because a lane's windows say only *when*, never anything about the
// budget rung — that stays global.
type Span struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// Window is a schedule span plus the rung flag. Embedding Span keeps the JSON
// shape and every existing `w.From` call site unchanged.
type Window struct {
	Span
	BudgetRung bool `json:"budget_rung"`
}
```

In `internal/dispatch/config.go`, `DefaultConfig`'s windows become:

```go
		Schedule: Schedule{Windows: []Window{
			{Span: Span{From: "18:00", To: "07:00"}, BudgetRung: false},
			{Span: Span{From: "07:00", To: "18:00"}, BudgetRung: true},
		}},
```

- [ ] **Step 4: Add the lane types**

Append to `internal/dispatch/types.go`:

```go
// LaneLimits are a lane's overrides of the top-level limits. A zero field
// means "inherit", so a lane states only what it changes.
type LaneLimits struct {
	PerRepo   int            `json:"per_repo,omitempty"`
	Overrides map[string]int `json:"overrides,omitempty"`
	// MaxDispatches bounds one window occurrence of this lane. With a 24h
	// window that is a calendar day; with the default night window it is one
	// night. Zero means unbounded by this rung.
	MaxDispatches int `json:"max_dispatches,omitempty"`
}

// Lane is one autonomy lane: which repos it claims, when it acts, what bounds
// it, and which labels a dispatch in it applies.
type Lane struct {
	Name  string   `json:"name"`
	Repos []string `json:"repos"`
	// Autonomous says this lane's PRs are reviewed and merged by the pipeline.
	// Three things follow from it: the lane is exempt from global_open_prs, its
	// repos are gated on agent.yml opting into ai-merge, and its default labels
	// carry the ai-merge gate label.
	Autonomous bool `json:"autonomous,omitempty"`
	// DryRun runs the lane for real through every decision and applies nothing.
	DryRun  bool       `json:"dry_run,omitempty"`
	Windows []Span     `json:"windows,omitempty"`
	Labels  []string   `json:"labels,omitempty"`
	Limits  LaneLimits `json:"limits,omitempty"`
}
```

Add the lane list to `Config`, directly under `Budget`:

```go
	Lanes []Lane `json:"lanes,omitempty"`
```

- [ ] **Step 5: Add `DefaultLane`, `EffectiveLabels` and the label const**

Append to `internal/dispatch/config.go`:

```go
// DefaultLane is where a repo lands when no configured lane matches. It
// reproduces the pre-lane behaviour exactly: the top-level schedule and
// limits, and the single ai-implement label.
func DefaultLane() Lane {
	return Lane{Name: "default", Repos: []string{"*"}}
}

// EffectiveLabels returns the labels a dispatch in this lane applies, in one
// AddLabels call. An autonomous lane carries the ai-merge gate label alongside
// the trigger; every other lane applies the trigger alone, which is what
// bridge did before lanes existed. An explicit list always wins — applying a
// gate label to a repo that has not wired the matching workflow input is inert
// at best, so bridge never guesses one.
func (l Lane) EffectiveLabels() []string {
	if len(l.Labels) > 0 {
		return l.Labels
	}
	if l.Autonomous {
		return []string{LabelAIImplement, LabelAIReviewAIMerge}
	}
	return []string{LabelAIImplement}
}
```

In `internal/dispatch/eligible.go`, add to the existing `const` block:

```go
	LabelAIReviewAIMerge = "ai-review-ai-merge"
```

- [ ] **Step 6: Run the tests to verify they pass**

Run: `go test ./internal/dispatch/ -v`
Expected: PASS, including every pre-existing test unchanged.

- [ ] **Step 7: Commit**

```bash
git add internal/dispatch/types.go internal/dispatch/config.go internal/dispatch/eligible.go internal/dispatch/config_test.go
git commit -m "feat(dispatch): add lane config types"
```

---

### Task 2: Lane windows

**Files:**
- Modify: `internal/dispatch/window.go`
- Create: `internal/dispatch/lane.go`
- Test: `internal/dispatch/lane_test.go`

**Interfaces:**
- Consumes: `Span`, `Window`, `Lane`, `Schedule` (Task 1).
- Produces: `func (sp Span) Covers(now time.Time) bool`; `func (sp Span) StartOf(now time.Time) time.Time`; `func (l Lane) InWindow(s Schedule, now time.Time) bool`; `func (l Lane) WindowStart(s Schedule, now time.Time) time.Time`. `Window.StartOf` keeps working by promotion — do not delete call sites.

- [ ] **Step 1: Write the failing test**

Create `internal/dispatch/lane_test.go`:

```go
package dispatch

import (
	"testing"
	"time"
)

func TestLaneInWindowInheritsTheSchedule(t *testing.T) {
	s := DefaultConfig().Schedule // 18:00-07:00, 07:00-18:00 — tiles the day
	lane := Lane{Name: "hitl"}

	if !lane.InWindow(s, at(12, 0)) {
		t.Error("a lane with no windows of its own must inherit the schedule's")
	}
}

func TestLaneInWindowUsesItsOwnWindows(t *testing.T) {
	s := DefaultConfig().Schedule
	lane := Lane{Name: "auto", Windows: []Span{{From: "09:00", To: "11:00"}}}

	if !lane.InWindow(s, at(9, 30)) {
		t.Error("inside its own window")
	}
	if lane.InWindow(s, at(12, 0)) {
		t.Error("its own windows replace the schedule's, they do not add to them")
	}
}

func TestLaneInWindowFullDaySpan(t *testing.T) {
	// from == to is the 24h lane the auto lane uses.
	lane := Lane{Name: "auto", Windows: []Span{{From: "00:00", To: "00:00"}}}
	for _, h := range []int{0, 7, 13, 23} {
		if !lane.InWindow(Schedule{}, at(h, 0)) {
			t.Errorf("hour %d must be covered by a 24h span", h)
		}
	}
}

func TestLaneWindowStartIsTheOccurrenceBoundary(t *testing.T) {
	lane := Lane{Name: "auto", Windows: []Span{{From: "00:00", To: "00:00"}}}
	now := at(13, 30)

	got := lane.WindowStart(Schedule{}, now)
	want := time.Date(2026, 9, 7, 0, 0, 0, 0, time.Local)
	if !got.Equal(want) {
		t.Errorf("a 24h lane's occurrence starts at local midnight: got %s want %s", got, want)
	}
}

func TestLaneWindowStartOutsideEveryWindowIsZero(t *testing.T) {
	lane := Lane{Name: "auto", Windows: []Span{{From: "09:00", To: "11:00"}}}
	if got := lane.WindowStart(Schedule{}, at(13, 0)); !got.IsZero() {
		t.Errorf("no occurrence covers now, so there is no boundary: %s", got)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/dispatch/ -run TestLane -v`
Expected: FAIL to compile — `lane.InWindow undefined`, `lane.WindowStart undefined`.

- [ ] **Step 3: Move the span arithmetic onto `Span`**

In `internal/dispatch/window.go`, change the receiver of `StartOf` from `Window` to `Span` and add `Covers`. The body of `StartOf` is unchanged — only `w Window` becomes `sp Span` and `w.From` becomes `sp.From`:

```go
// Covers reports whether now's wall clock falls inside the span.
func (sp Span) Covers(now time.Time) bool {
	from, ok := parseHHMM(sp.From)
	if !ok {
		return false
	}
	to, ok := parseHHMM(sp.To)
	if !ok {
		return false
	}
	return covers(from, to, now.Hour()*60+now.Minute())
}

// StartOf returns the absolute instant at which sp's occurrence covering now
// began. For a span that wraps past midnight this is the previous day's
// boundary when now is on the morning side of it. A malformed From yields the
// zero time, which callers treat as "no usable boundary".
func (sp Span) StartOf(now time.Time) time.Time {
	from, ok := parseHHMM(sp.From)
	...unchanged body, with sp.From in place of w.From...
}
```

Leave `Schedule.InWindow` as it is; `Window` gets `Covers`/`StartOf` for free by embedding, so `timing.Window.StartOf(now)` in `cmd/bridge/dispatch.go` keeps compiling.

- [ ] **Step 4: Add the lane window methods**

Create `internal/dispatch/lane.go`:

```go
package dispatch

import "time"

// spans returns the spans this lane acts in: its own when configured, the
// schedule's otherwise. Inheriting is what keeps a lanes-only config from
// silently changing when the dispatcher runs.
func (l Lane) spans(s Schedule) []Span {
	if len(l.Windows) > 0 {
		return l.Windows
	}
	out := make([]Span, 0, len(s.Windows))
	for _, w := range s.Windows {
		out = append(out, w.Span)
	}
	return out
}

// InWindow reports whether the lane acts at now.
func (l Lane) InWindow(s Schedule, now time.Time) bool {
	for _, sp := range l.spans(s) {
		if sp.Covers(now) {
			return true
		}
	}
	return false
}

// WindowStart returns the start of the lane's current window occurrence — the
// boundary its dispatch counter resets at. Zero when no span covers now.
func (l Lane) WindowStart(s Schedule, now time.Time) time.Time {
	for _, sp := range l.spans(s) {
		if sp.Covers(now) {
			return sp.StartOf(now)
		}
	}
	return time.Time{}
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/dispatch/ -v && go build ./...`
Expected: PASS, and `cmd/bridge` still builds (the `Window.StartOf` call site is satisfied by promotion).

- [ ] **Step 6: Commit**

```bash
git add internal/dispatch/window.go internal/dispatch/lane.go internal/dispatch/lane_test.go
git commit -m "feat(dispatch): give lanes their own windows"
```

---

### Task 3: Lane resolution and the fallback reason

**Files:**
- Modify: `internal/dispatch/lane.go`
- Test: `internal/dispatch/lane_test.go`

**Interfaces:**
- Consumes: `Lane`, `DefaultLane` (Task 1).
- Produces: `type GateState map[string]bool`; `func ResolveLane(lanes []Lane, repo string, gate GateState) (Lane, string)`; `func AnyAutonomousLaneMatches(lanes []Lane, repo string) bool`.

- [ ] **Step 1: Write the failing test**

Append to `internal/dispatch/lane_test.go`:

```go
func laneFixture() []Lane {
	return []Lane{
		{Name: "auto", Repos: []string{"game-*"}, Autonomous: true},
		{Name: "hitl", Repos: []string{"*"}},
	}
}

func TestResolveLane(t *testing.T) {
	gate := GateState{"game-tschau-sepp": true}

	tests := []struct {
		name       string
		repo       string
		wantLane   string
		wantReason string
	}{
		{"first matching lane wins", "game-tschau-sepp", "auto", ""},
		{"a repo outside the glob falls to the catch-all", "bridge", "hitl", ""},
		{"gate not met downgrades to the next non-autonomous lane", "game-huusli-jagd", "hitl",
			"auto→hitl: agent.yml lacks ai-review-ai-merge: true"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			lane, reason := ResolveLane(laneFixture(), tc.repo, gate)
			if lane.Name != tc.wantLane {
				t.Errorf("lane = %q want %q", lane.Name, tc.wantLane)
			}
			if reason != tc.wantReason {
				t.Errorf("reason = %q want %q", reason, tc.wantReason)
			}
		})
	}
}

func TestResolveLaneNoLanesConfiguredUsesTheDefaultLane(t *testing.T) {
	lane, reason := ResolveLane(nil, "bridge", nil)
	if lane.Name != DefaultLane().Name || reason != "" {
		t.Errorf("lane=%+v reason=%q", lane, reason)
	}
}

func TestResolveLaneDowngradeReachesTheDefaultLaneWhenNothingElseMatches(t *testing.T) {
	// An autonomous lane with no non-autonomous lane behind it must still fall
	// back — to the implicit default — rather than dispatch autonomously.
	lanes := []Lane{{Name: "auto", Repos: []string{"game-*"}, Autonomous: true}}
	lane, reason := ResolveLane(lanes, "game-huusli-jagd", GateState{})
	if lane.Name != DefaultLane().Name {
		t.Errorf("lane = %q want %q", lane.Name, DefaultLane().Name)
	}
	if reason != "auto→default: agent.yml lacks ai-review-ai-merge: true" {
		t.Errorf("reason = %q", reason)
	}
}

func TestResolveLaneMalformedGlobDoesNotMatch(t *testing.T) {
	// Ordering already treats a bad pattern as a non-match; lane resolution must
	// not fail the whole tick on a config typo either.
	lanes := []Lane{{Name: "broken", Repos: []string{"[unclosed"}}, {Name: "hitl", Repos: []string{"*"}}}
	if lane, _ := ResolveLane(lanes, "bridge", nil); lane.Name != "hitl" {
		t.Errorf("lane = %q", lane.Name)
	}
}

func TestAnyAutonomousLaneMatches(t *testing.T) {
	lanes := laneFixture()
	if !AnyAutonomousLaneMatches(lanes, "game-huusli-jagd") {
		t.Error("a repo an autonomous lane claims must be gate-checked even before the gate is known")
	}
	if AnyAutonomousLaneMatches(lanes, "bridge") {
		t.Error("no autonomous lane claims it, so no agent.yml fetch is warranted")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/dispatch/ -run 'TestResolveLane|TestAnyAutonomous' -v`
Expected: FAIL to compile — `undefined: GateState`, `undefined: ResolveLane`.

- [ ] **Step 3: Implement resolution**

Append to `internal/dispatch/lane.go` (add `"fmt"`, `"path"` to its imports):

```go
// GateState answers, per repo, whether it meets an autonomous lane's
// precondition — agent.yml opting into ai-merge. Measuring it is the caller's
// job; keeping the fetch out here is what lets ResolveLane stay pure.
type GateState map[string]bool

// ResolveLane returns the lane a repo dispatches in, plus a reason when an
// autonomous lane was downgraded. First matching lane wins. An autonomous lane
// whose repo fails the gate falls through to the next matching non-autonomous
// lane — an unmet precondition must never dispatch into unattended merge.
func ResolveLane(lanes []Lane, repo string, gate GateState) (Lane, string) {
	downgradedFrom := ""
	for _, l := range lanes {
		if !matchesRepo(l.Repos, repo) {
			continue
		}
		if l.Autonomous && !gate[repo] {
			if downgradedFrom == "" {
				downgradedFrom = l.Name
			}
			continue
		}
		return l, downgradeReason(downgradedFrom, l.Name)
	}
	d := DefaultLane()
	return d, downgradeReason(downgradedFrom, d.Name)
}

// AnyAutonomousLaneMatches reports whether some autonomous lane claims this
// repo, which is what makes its agent.yml worth fetching.
func AnyAutonomousLaneMatches(lanes []Lane, repo string) bool {
	for _, l := range lanes {
		if l.Autonomous && matchesRepo(l.Repos, repo) {
			return true
		}
	}
	return false
}

// matchesRepo reports whether any pattern matches the bare repo name. A
// malformed pattern simply does not match, mirroring repoPriorityRank — lane
// assignment must not fail the tick on a config typo.
func matchesRepo(patterns []string, repo string) bool {
	for _, p := range patterns {
		if ok, err := path.Match(p, repo); err == nil && ok {
			return true
		}
	}
	return false
}

func downgradeReason(from, to string) string {
	if from == "" {
		return ""
	}
	return fmt.Sprintf("%s→%s: agent.yml lacks %s: true", from, to, LabelAIReviewAIMerge)
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/dispatch/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/dispatch/lane.go internal/dispatch/lane_test.go
git commit -m "feat(dispatch): resolve a repo's lane, with fallback to human review"
```

---

### Task 4: Per-lane dispatch counters in state

**Files:**
- Modify: `internal/dispatch/types.go`
- Modify: `internal/dispatch/state.go`
- Test: `internal/dispatch/state_test.go`

**Interfaces:**
- Consumes: nothing beyond `State`.
- Produces: `type LaneState struct{ StartedAt time.Time; Dispatched int }`; `State.Lanes map[string]LaneState`; `func (s State) DispatchesInLane(lane string, since time.Time) int`; `func (s *State) RecordLaneDispatch(lane string, since, now time.Time, n int)`.

- [ ] **Step 1: Write the failing test**

Append to `internal/dispatch/state_test.go`:

```go
func TestDispatchesInLane(t *testing.T) {
	since := time.Date(2026, 9, 7, 0, 0, 0, 0, time.Local)
	s := State{Lanes: map[string]LaneState{
		"auto": {StartedAt: since.Add(2 * time.Hour), Dispatched: 4},
	}}

	if got := s.DispatchesInLane("auto", since); got != 4 {
		t.Errorf("counter inside this occurrence: got %d want 4", got)
	}
	if got := s.DispatchesInLane("auto", since.AddDate(0, 0, 1)); got != 0 {
		t.Errorf("a counter from an earlier occurrence must not carry over: got %d", got)
	}
	if got := s.DispatchesInLane("hitl", since); got != 0 {
		t.Errorf("an unknown lane has spent nothing: got %d", got)
	}
	if got := s.DispatchesInLane("auto", time.Time{}); got != 0 {
		t.Errorf("no boundary means nothing to attribute the counter to: got %d", got)
	}
}

func TestRecordLaneDispatchAccumulatesThenResets(t *testing.T) {
	day1 := time.Date(2026, 9, 7, 0, 0, 0, 0, time.Local)
	var s State

	s.RecordLaneDispatch("auto", day1, day1.Add(time.Hour), 2)
	if got := s.DispatchesInLane("auto", day1); got != 2 {
		t.Fatalf("first write: got %d want 2", got)
	}

	s.RecordLaneDispatch("auto", day1, day1.Add(3*time.Hour), 1)
	if got := s.DispatchesInLane("auto", day1); got != 3 {
		t.Errorf("same occurrence must accumulate: got %d want 3", got)
	}

	day2 := day1.AddDate(0, 0, 1)
	s.RecordLaneDispatch("auto", day2, day2.Add(time.Hour), 1)
	if got := s.DispatchesInLane("auto", day2); got != 1 {
		t.Errorf("a new occurrence starts from zero: got %d want 1", got)
	}
}

// A 24h lane's occurrence boundary is calendar arithmetic, so the counter must
// survive a 23-hour day. Reuses the Europe/Zurich spring-forward date the window
// tests pin.
func TestRecordLaneDispatchAcrossASpringForwardDay(t *testing.T) {
	zurich, err := time.LoadLocation("Europe/Zurich")
	if err != nil {
		t.Skipf("tzdata unavailable: %v", err)
	}
	lane := Lane{Name: "auto", Windows: []Span{{From: "00:00", To: "00:00"}}}

	before := time.Date(2026, 3, 29, 1, 0, 0, 0, zurich) // before the 02:00 gap
	after := time.Date(2026, 3, 29, 13, 0, 0, 0, zurich) // same calendar day

	var s State
	s.RecordLaneDispatch(lane.Name, lane.WindowStart(Schedule{}, before), before, 1)
	s.RecordLaneDispatch(lane.Name, lane.WindowStart(Schedule{}, after), after, 1)

	if got := s.DispatchesInLane(lane.Name, lane.WindowStart(Schedule{}, after)); got != 2 {
		t.Errorf("both dispatches belong to the same 23-hour day: got %d want 2", got)
	}

	next := time.Date(2026, 3, 30, 9, 0, 0, 0, zurich)
	if got := s.DispatchesInLane(lane.Name, lane.WindowStart(Schedule{}, next)); got != 0 {
		t.Errorf("the next day starts fresh: got %d", got)
	}
}

func TestRecordLaneDispatchRoundTripsThroughDisk(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dispatch.json")
	day := time.Date(2026, 9, 7, 0, 0, 0, 0, time.Local)

	var s State
	s.RecordLaneDispatch("auto", day, day.Add(time.Hour), 2)
	if err := WriteState(path, s); err != nil {
		t.Fatal(err)
	}

	back, err := ReadState(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := back.DispatchesInLane("auto", day); got != 2 {
		t.Errorf("after reload: got %d want 2", got)
	}
}
```

Ensure `state_test.go` imports `"path/filepath"` and `"time"`.

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/dispatch/ -run 'TestDispatchesInLane|TestRecordLaneDispatch' -v`
Expected: FAIL to compile — `undefined: LaneState`.

- [ ] **Step 3: Add the state type and methods**

In `internal/dispatch/types.go`, append to the file and add the field to `State`:

```go
// LaneState is one lane's dispatch counter plus the window occurrence it
// belongs to. Keyed by lane name in State.Lanes.
type LaneState struct {
	StartedAt  time.Time `json:"started_at,omitempty"`
	Dispatched int       `json:"dispatched"`
}
```

```go
	Lanes map[string]LaneState `json:"lanes,omitempty"`
```

In `internal/dispatch/state.go`, append:

```go
// DispatchesInLane returns how many dispatches lane's current window
// occurrence has already spent. since is that occurrence's start: a counter
// recorded before it belongs to an earlier occurrence and does not carry over,
// the same rule DispatchesSince applies to the nightly counter.
func (s State) DispatchesInLane(lane string, since time.Time) int {
	ls, ok := s.Lanes[lane]
	if !ok || since.IsZero() || ls.StartedAt.IsZero() || ls.StartedAt.Before(since) {
		return 0
	}
	return ls.Dispatched
}

// RecordLaneDispatch adds n dispatches to lane's counter, restarting it when
// the stored counter belongs to an earlier window occurrence.
func (s *State) RecordLaneDispatch(lane string, since, now time.Time, n int) {
	if s.Lanes == nil {
		s.Lanes = make(map[string]LaneState)
	}
	base := s.DispatchesInLane(lane, since)
	ls := s.Lanes[lane]
	if base == 0 {
		ls.StartedAt = now
	}
	ls.Dispatched = base + n
	s.Lanes[lane] = ls
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/dispatch/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/dispatch/types.go internal/dispatch/state.go internal/dispatch/state_test.go
git commit -m "feat(dispatch): track dispatch counts per lane occurrence"
```

---

### Task 5: Lane-aware caps

**Files:**
- Modify: `internal/dispatch/order.go` (Candidate gains the lane)
- Modify: `internal/dispatch/caps.go`
- Test: `internal/dispatch/caps_test.go`

**Interfaces:**
- Consumes: `Lane`, `GateState` (Tasks 1 & 3), `Counts`, `BudgetState`.
- Produces: `Candidate.Lane Lane` and `Candidate.LaneReason string`; `Counts.DispatchedByLane map[string]int`; `ApplyCaps` keeps its existing signature `ApplyCaps(ordered []Candidate, cfg Config, counts Counts, budget BudgetState) []Decision`; `func PartitionByWindow(ordered []Candidate, s Schedule, now time.Time) ([]Candidate, []Decision)`.

- [ ] **Step 1: Write the failing test**

Append to `internal/dispatch/caps_test.go`:

```go
func laneCand(repo string, n int, lane Lane) Candidate {
	c := cand(repo, n)
	c.Lane = lane
	return c
}

func TestApplyCapsAutonomousLaneIgnoresTheGlobalCap(t *testing.T) {
	cfg := DefaultConfig() // global 3
	auto := Lane{Name: "auto", Autonomous: true}
	cs := []Candidate{
		laneCand("game-a", 1, auto), laneCand("game-b", 2, auto),
		laneCand("game-c", 3, auto), laneCand("game-d", 4, auto),
	}

	ds := ApplyCaps(cs, cfg, Counts{GlobalOpen: 3}, BudgetState{})
	for i, d := range ds {
		if !d.Dispatch {
			t.Errorf("index %d: an autonomous lane spends no review capacity: %+v", i, d)
		}
	}
}

func TestApplyCapsAutonomousDispatchesDoNotConsumeTheGlobalCap(t *testing.T) {
	cfg := DefaultConfig() // global 3
	auto := Lane{Name: "auto", Autonomous: true}
	hitl := Lane{Name: "hitl"}
	cs := []Candidate{
		laneCand("game-a", 1, auto), laneCand("game-b", 2, auto), laneCand("game-c", 3, auto),
		laneCand("bridge", 4, hitl),
	}

	ds := ApplyCaps(cs, cfg, Counts{}, BudgetState{})
	if !ds[3].Dispatch {
		t.Errorf("three auto dispatches must leave the hitl lane its slots: %+v", ds[3])
	}
}

func TestApplyCapsLaneCeiling(t *testing.T) {
	cfg := DefaultConfig()
	auto := Lane{Name: "auto", Autonomous: true, Limits: LaneLimits{MaxDispatches: 2}}
	cs := []Candidate{laneCand("game-a", 1, auto), laneCand("game-b", 2, auto), laneCand("game-c", 3, auto)}

	ds := ApplyCaps(cs, cfg, Counts{}, BudgetState{})
	if !ds[0].Dispatch || !ds[1].Dispatch {
		t.Fatalf("first two: %+v %+v", ds[0], ds[1])
	}
	if ds[2].Dispatch || ds[2].Reason != "lane cap 2/2 (auto)" {
		t.Errorf("third: %+v", ds[2])
	}
}

func TestApplyCapsLaneCeilingCountsWhatTheOccurrenceAlreadySpent(t *testing.T) {
	cfg := DefaultConfig()
	auto := Lane{Name: "auto", Autonomous: true, Limits: LaneLimits{MaxDispatches: 2}}

	ds := ApplyCaps([]Candidate{laneCand("game-a", 1, auto)}, cfg,
		Counts{DispatchedByLane: map[string]int{"auto": 2}}, BudgetState{})
	if ds[0].Dispatch || ds[0].Reason != "lane cap 2/2 (auto)" {
		t.Errorf("%+v", ds[0])
	}
}

func TestApplyCapsLaneOverridesThePerRepoLimit(t *testing.T) {
	cfg := DefaultConfig() // per_repo 1
	lane := Lane{Name: "auto", Limits: LaneLimits{PerRepo: 2}}

	ds := ApplyCaps([]Candidate{laneCand("game-a", 1, lane), laneCand("game-a", 2, lane)},
		cfg, Counts{}, BudgetState{})
	if !ds[0].Dispatch || !ds[1].Dispatch {
		t.Errorf("the lane's per_repo 2 must win over the top-level 1: %+v %+v", ds[0], ds[1])
	}
}

// A dry-run lane must be able to observe the world without changing what the
// live lanes are allowed to do — otherwise the observation week throttles the
// work it is meant to observe.
func TestApplyCapsDryRunLaneConsumesNothingShared(t *testing.T) {
	cfg := DefaultConfig() // global 3
	dry := Lane{Name: "auto", Autonomous: false, DryRun: true}
	hitl := Lane{Name: "hitl"}
	cs := []Candidate{
		laneCand("game-a", 1, dry), laneCand("game-b", 2, dry), laneCand("game-c", 3, dry),
		laneCand("bridge", 4, hitl), laneCand("quotes", 5, hitl), laneCand("flowhub", 6, hitl),
	}

	ds := ApplyCaps(cs, cfg, Counts{}, BudgetState{
		Enabled: true, UsedUSD: 0, LimitUSD: 9.6, PerRunUSD: 2.0,
	})

	for i := 3; i < 6; i++ {
		if !ds[i].Dispatch {
			t.Errorf("hitl candidate %d must be unaffected by the dry-run lane: %+v", i, ds[i])
		}
	}
}

func TestApplyCapsDryRunLaneStillHitsItsOwnCeiling(t *testing.T) {
	cfg := DefaultConfig()
	dry := Lane{Name: "auto", DryRun: true, Limits: LaneLimits{MaxDispatches: 1}}

	ds := ApplyCaps([]Candidate{laneCand("game-a", 1, dry), laneCand("game-b", 2, dry)},
		cfg, Counts{}, BudgetState{})
	if !ds[0].Dispatch {
		t.Fatalf("first: %+v", ds[0])
	}
	if ds[1].Dispatch || ds[1].Reason != "lane cap 1/1 (auto)" {
		t.Errorf("the lane's own counter is private, so it still bites: %+v", ds[1])
	}
}

func TestPartitionByWindow(t *testing.T) {
	s := DefaultConfig().Schedule
	acting := Lane{Name: "auto", Windows: []Span{{From: "00:00", To: "00:00"}}}
	asleep := Lane{Name: "night", Windows: []Span{{From: "22:00", To: "23:00"}}}

	act, skipped := PartitionByWindow(
		[]Candidate{laneCand("game-a", 1, acting), laneCand("bridge", 2, asleep)},
		s, at(13, 0))

	if len(act) != 1 || act[0].Repo != "game-a" {
		t.Errorf("acting: %+v", act)
	}
	if len(skipped) != 1 || skipped[0].Dispatch || skipped[0].Reason != "outside lane window (night)" {
		t.Errorf("skipped: %+v", skipped)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/dispatch/ -run 'TestApplyCaps|TestPartitionByWindow' -v`
Expected: FAIL to compile — `c.Lane undefined`, `undefined: PartitionByWindow`.

- [ ] **Step 3: Put the lane on the candidate**

In `internal/dispatch/order.go`, add to `Candidate`:

```go
	// Lane is the autonomy lane this candidate dispatches in, resolved before
	// ordering. LaneReason records a downgrade so --dry-run can explain it.
	Lane       Lane
	LaneReason string
```

- [ ] **Step 4: Make the caps lane-aware**

Replace the body of `ApplyCaps` in `internal/dispatch/caps.go` and add the two helpers. Update the doc comment above `ApplyCaps` to list the lane ceiling and the two lane exemptions:

```go
func ApplyCaps(ordered []Candidate, cfg Config, counts Counts, budget BudgetState) []Decision {
	perRepo := make(map[string]int, len(counts.OpenPRsByRepo))
	for k, v := range counts.OpenPRsByRepo {
		perRepo[k] = v
	}
	byLane := make(map[string]int, len(counts.DispatchedByLane))
	for k, v := range counts.DispatchedByLane {
		byLane[k] = v
	}
	global := counts.GlobalOpen
	night := counts.DispatchedTonight
	spent := budget.UsedUSD

	out := make([]Decision, 0, len(ordered))
	for _, c := range ordered {
		lane := c.Lane
		limit := effectiveRepoLimit(cfg, lane, c.Repo)
		laneCap := lane.Limits.MaxDispatches
		switch {
		case budget.Enabled && budget.Unknown:
			out = append(out, Decision{c, false, "budget-unknown"})
		case budget.Enabled && spent+budget.PerRunUSD > budget.LimitUSD:
			out = append(out, Decision{c, false,
				fmt.Sprintf("budget-exhausted %.2f/%.2f USD", spent, budget.LimitUSD)})
		case laneCap > 0 && byLane[lane.Name] >= laneCap:
			out = append(out, Decision{c, false,
				fmt.Sprintf("lane cap %d/%d (%s)", byLane[lane.Name], laneCap, lane.Name)})
		case counts.NightCapApplies && !lane.Autonomous && night >= cfg.Limits.MaxDispatchesPerNight:
			out = append(out, Decision{c, false,
				fmt.Sprintf("night cap %d/%d", night, cfg.Limits.MaxDispatchesPerNight)})
		case !lane.Autonomous && global >= cfg.Limits.GlobalOpenPRs:
			out = append(out, Decision{c, false,
				fmt.Sprintf("global cap %d/%d", global, cfg.Limits.GlobalOpenPRs)})
		case perRepo[c.Repo] >= limit:
			out = append(out, Decision{c, false,
				fmt.Sprintf("repo at WIP %d/%d", perRepo[c.Repo], limit)})
		default:
			// The lane's own counter is private to it, so a dry-run lane still
			// advances it and still hits its own ceiling. Everything else here
			// is shared, and a dry-run lane must leave it exactly as it found
			// it — it dispatches nothing, so it costs nothing.
			byLane[lane.Name]++
			if !lane.DryRun {
				perRepo[c.Repo]++
				spent += budget.PerRunUSD
				if !lane.Autonomous {
					global++
					night++
				}
			}
			out = append(out, Decision{c, true, ""})
		}
	}
	return out
}

// effectiveRepoLimit resolves the per-repo WIP limit: the lane's override for
// that repo, then the lane's per_repo, then the top-level config. A lane states
// only what it changes.
func effectiveRepoLimit(cfg Config, lane Lane, repo string) int {
	if n, ok := lane.Limits.Overrides[repo]; ok {
		return n
	}
	if lane.Limits.PerRepo > 0 {
		return lane.Limits.PerRepo
	}
	return cfg.LimitFor(repo)
}

// PartitionByWindow splits ordered candidates into those whose lane acts at
// now and skip decisions for the rest. It is separate from ApplyCaps so the
// cap walk keeps no clock: the schedule is read once, here.
func PartitionByWindow(ordered []Candidate, s Schedule, now time.Time) ([]Candidate, []Decision) {
	var act []Candidate
	var skipped []Decision
	for _, c := range ordered {
		if c.Lane.InWindow(s, now) {
			act = append(act, c)
			continue
		}
		skipped = append(skipped, Decision{c, false,
			fmt.Sprintf("outside lane window (%s)", c.Lane.Name)})
	}
	return act, skipped
}
```

Add `"time"` to `caps.go`'s imports, and add `DispatchedByLane map[string]int` to `Counts` with a comment: `// DispatchedByLane is what each lane's current window occurrence has already spent.`

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/dispatch/ -race -v`
Expected: PASS, including every pre-lane cap test (a zero-value `Lane` is non-autonomous, non-dry-run, uncapped — which is exactly the old behaviour).

- [ ] **Step 6: Commit**

```bash
git add internal/dispatch/order.go internal/dispatch/caps.go internal/dispatch/caps_test.go
git commit -m "feat(dispatch): apply caps per lane"
```

---

### Task 6: The agent.yml gate, fetched and cached

**Files:**
- Create: `cmd/bridge/lane_gate.go`
- Create: `cmd/bridge/lane_gate_test.go`

**Interfaces:**
- Consumes: `dispatch.GateState`, `dispatch.AnyAutonomousLaneMatches` (Task 3); `forge.GithubClient.GetFile(ctx, owner, repo, path) ([]byte, string, bool, error)`; `cacheRoot()`; `store.AtomicWrite`.
- Produces: `func agentYAMLOptsIntoAIMerge(b []byte) bool`; `type laneGateCache struct{ Repos map[string]laneGateEntry }`; `func (c laneGateCache) fresh(repo string, now time.Time) (bool, bool)`; `func loadLaneGateCache(path string) laneGateCache`; `func saveLaneGateCache(path string, c laneGateCache) error`; `func resolveGate(ctx context.Context, repos []repoInput, lanes []dispatch.Lane, now time.Time) dispatch.GateState`.

- [ ] **Step 1: Write the failing test**

Create `cmd/bridge/lane_gate_test.go`:

```go
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
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./cmd/bridge/ -run 'TestAgentYAML|TestLaneGate|TestLoadLaneGate' -v`
Expected: FAIL to compile — `undefined: agentYAMLOptsIntoAIMerge`.

- [ ] **Step 3: Implement the gate and its cache**

Create `cmd/bridge/lane_gate.go`:

```go
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
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./cmd/bridge/ -run 'TestAgentYAML|TestLaneGate|TestLoadLaneGate' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/bridge/lane_gate.go cmd/bridge/lane_gate_test.go
git commit -m "feat(dispatch): gate autonomous lanes on the repo's agent.yml"
```

---

### Task 7: Wire lanes into the tick

**Files:**
- Modify: `cmd/bridge/dispatch.go` (`runDispatch`, `collectCandidates`, `countOpenAgentPRs`, `applyDecisions`)
- Test: `cmd/bridge/dispatch_test.go`

**Interfaces:**
- Consumes: everything from Tasks 1–6.
- Produces: `func assignLanes(cs []dispatch.Candidate, lanes []dispatch.Lane, gate dispatch.GateState)`; `countOpenAgentPRs(repos []repoInput, autonomous func(repo string) bool) (map[string]int, int)`; `applyDecisions(ctx, ds, state, timing, cfg)` unchanged in signature, lane-aware in behaviour.

- [ ] **Step 1: Write the failing test**

Append to `cmd/bridge/dispatch_test.go`:

```go
func TestAssignLanes(t *testing.T) {
	lanes := []dispatch.Lane{
		{Name: "auto", Repos: []string{"game-*"}, Autonomous: true},
		{Name: "hitl", Repos: []string{"*"}},
	}
	cs := []dispatch.Candidate{
		{Repo: "game-tschau-sepp", Issue: forge.Issue{Number: 1}},
		{Repo: "game-huusli-jagd", Issue: forge.Issue{Number: 2}},
		{Repo: "bridge", Issue: forge.Issue{Number: 3}},
	}

	assignLanes(cs, lanes, dispatch.GateState{"game-tschau-sepp": true})

	if cs[0].Lane.Name != "auto" || cs[0].LaneReason != "" {
		t.Errorf("gated repo stays in auto: %+v", cs[0])
	}
	if cs[1].Lane.Name != "hitl" || cs[1].LaneReason == "" {
		t.Errorf("ungated repo falls back with a reason: %+v", cs[1])
	}
	if cs[2].Lane.Name != "hitl" {
		t.Errorf("catch-all: %+v", cs[2])
	}
}

func TestCountOpenAgentPRsExcludesAutonomousRepos(t *testing.T) {
	repos := []repoInput{
		{Forge: "github", Owner: "freaxnx01", Name: "game-tschau-sepp",
			Issues: []forge.Issue{{Number: 1}},
			PRs:    []forge.PullRequest{{Body: "Closes #1"}}},
		{Forge: "github", Owner: "freaxnx01", Name: "bridge",
			Issues: []forge.Issue{{Number: 9}},
			PRs:    []forge.PullRequest{{Body: "Closes #9"}}},
	}
	autonomous := func(repo string) bool { return repo == "game-tschau-sepp" }

	byRepo, global := countOpenAgentPRs(repos, autonomous)

	if global != 1 {
		t.Errorf("only human-reviewed PRs spend review capacity: global=%d want 1", global)
	}
	// The per-repo count still has to include it — per_repo bounds conflicting
	// PRs in one repo, which is true in either lane.
	if byRepo["game-tschau-sepp"] != 1 {
		t.Errorf("per-repo count: %+v", byRepo)
	}
}
```

Ensure the file imports `"github.com/freaxnx01/bridge/internal/dispatch"` and `"github.com/freaxnx01/bridge/internal/forge"`.

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./cmd/bridge/ -run 'TestAssignLanes|TestCountOpenAgentPRsExcludes' -v`
Expected: FAIL to compile — `undefined: assignLanes`, and `countOpenAgentPRs` takes one argument.

- [ ] **Step 3: Add lane assignment and the lane-aware PR count**

In `cmd/bridge/dispatch.go`, add:

```go
// assignLanes resolves each candidate's lane in place, before ordering, so the
// cap walk and the label write read the same decision.
func assignLanes(cs []dispatch.Candidate, lanes []dispatch.Lane, gate dispatch.GateState) {
	for i := range cs {
		cs[i].Lane, cs[i].LaneReason = dispatch.ResolveLane(lanes, cs[i].Repo, gate)
	}
}
```

Change `countOpenAgentPRs` to take the predicate and skip autonomous repos in the global total only:

```go
// countOpenAgentPRs counts open PRs that close one of the repo's own issues.
// Only those are pipeline output, so a hand-written PR never consumes a slot.
//
// An autonomous lane's PRs are left out of the global total entirely: that cap
// is the operator's review capacity, and nobody reviews them. They still count
// per repo, where the bound is conflicting concurrent PRs rather than review.
func countOpenAgentPRs(repos []repoInput, autonomous func(repo string) bool) (map[string]int, int) {
	byRepo := make(map[string]int, len(repos))
	total := 0
	for _, r := range repos {
		for _, pr := range r.PRs {
			for _, i := range r.Issues {
				if dispatch.ClosesIssue(pr.Body, i.Number) {
					byRepo[r.Name]++
					if !autonomous(r.Name) {
						total++
					}
					break
				}
			}
		}
	}
	return byRepo, total
}
```

- [ ] **Step 4: Rewire `runDispatch`**

Replace the block in `runDispatch` from the `ctx := context.Background()` line down to the `ApplyCaps` call with:

```go
	ctx := context.Background()
	repos, err := fetchRepoInputs(ctx)
	if err != nil {
		return err
	}

	gate := resolveGate(ctx, repos, cfg.Lanes, now)
	candidates := collectCandidates(repos)
	assignLanes(candidates, cfg.Lanes, gate)
	acting, outOfWindow := dispatch.PartitionByWindow(
		dispatch.Order(candidates, cfg.RepoPriority), cfg.Schedule, now)

	// The window gate is --auto only, as before: an explicit `dispatch now` is
	// the operator asking for a tick. With lanes the question is per lane, so
	// the tick stops only when no lane acts at all.
	if dispatchAuto && len(acting) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "outside every lane's dispatch window — nothing to do")
		return nil
	}

	// The rung is keyed on what it guards, not on --auto: a manual tick burns
	// the same quota, and a pre-dawn tick burns the window the operator will
	// inherit at the handover.
	usedUSD, usedKnown := 0.0, true
	if timing.RungOn {
		usedUSD, usedKnown = measureWindowUSD(ctx, cfg, timing.MeasureFrom, now)
	}
	budget := dispatch.NewBudgetState(cfg.Budget, timing.RungOn, usedUSD, usedKnown)

	autonomous := func(repo string) bool {
		lane, _ := dispatch.ResolveLane(cfg.Lanes, repo, gate)
		return lane.Autonomous
	}
	openByRepo, globalOpen := countOpenAgentPRs(repos, autonomous)

	decisions := dispatch.ApplyCaps(acting, cfg,
		dispatch.Counts{
			OpenPRsByRepo:     openByRepo,
			GlobalOpen:        globalOpen,
			DispatchedTonight: state.DispatchesSince(nightStartForReport(cfg, timing, now)),
			NightCapApplies:   timing.NightCapApplies,
			DispatchedByLane:  laneCounts(state, cfg, now),
		},
		budget,
	)
	decisions = append(decisions, outOfWindow...)
```

Delete the now-superseded early return `if dispatchAuto && !timing.InWindow { ... }` above — lane windows subsume it, and the default lane inherits `schedule.windows`, so behaviour without lanes is unchanged.

Add the counter helper:

```go
// laneCounts reads each configured lane's spent counter for the occurrence it
// is currently in. The implicit default lane is bounded by the nightly cap, not
// by a lane ceiling, so it needs no entry here.
func laneCounts(state dispatch.State, cfg dispatch.Config, now time.Time) map[string]int {
	out := make(map[string]int, len(cfg.Lanes))
	for _, l := range cfg.Lanes {
		out[l.Name] = state.DispatchesInLane(l.Name, l.WindowStart(cfg.Schedule, now))
	}
	return out
}
```

- [ ] **Step 5: Make `applyDecisions` lane-aware**

In `applyDecisions`, replace the loop body's label call and add the dry-run skip plus the per-lane counter. The `for _, d := range ds` loop becomes:

```go
	laneDispatched := map[string]int{}
	for _, d := range ds {
		if !d.Dispatch {
			continue
		}
		lane := d.Candidate.Lane
		if lane.DryRun {
			// The lane is being observed, not run. It applies nothing, books no
			// spend, and advances no counter — see the spec's dry_run section.
			continue
		}
		gh, ok := clientFor("github").(*forge.GithubClient)
		if !ok || gh == nil {
			continue
		}
		owner, repo, num := d.Candidate.Owner, d.Candidate.Repo, d.Candidate.Issue.Number
		if _, err := gh.AddLabels(ctx, owner, repo, num, lane.EffectiveLabels()); err != nil {
			dispatchErr = fmt.Errorf("label %s#%d: %w", repo, num, err)
			break
		}
		runs = append(runs, usage.Run{
			At: timing.Now, Repo: repo, Issue: num, EstUSD: cfg.Budget.MeanRunCostUSD,
		})
		laneDispatched[lane.Name]++
		if _, err := gh.CommentIssue(ctx, owner, repo, num,
			"Dispatched by `bridge dispatch`."); err != nil {
			dispatchErr = fmt.Errorf("comment %s#%d: %w", repo, num, err)
			break
		}
	}
```

Then, alongside the existing nightly-counter block and before `state.LastTick = timing.Now`, persist the lane counters:

```go
	for _, l := range cfg.Lanes {
		if n := laneDispatched[l.Name]; n > 0 {
			state.RecordLaneDispatch(l.Name, l.WindowStart(cfg.Schedule, timing.Now), timing.Now, n)
		}
	}
```

- [ ] **Step 6: Run the tests to verify they pass**

Run: `go test ./... -race`
Expected: PASS across the repo.

- [ ] **Step 7: Commit**

```bash
git add cmd/bridge/dispatch.go cmd/bridge/dispatch_test.go
git commit -m "feat(dispatch): dispatch each issue in its lane"
```

---

### Task 8: Lane-aware output

**Files:**
- Modify: `cmd/bridge/dispatch.go` (`renderDecisions`, the `--json` emitters, `runDispatchStatus`)
- Test: `cmd/bridge/dispatch_test.go`

**Interfaces:**
- Consumes: `dispatch.Decision`, `Candidate.Lane`, `Candidate.LaneReason`.
- Produces: `func decisionStatus(d dispatch.Decision) string`; `type decisionJSON struct{...}`; `func decisionsJSON(ds []dispatch.Decision) []decisionJSON`.

- [ ] **Step 1: Write the failing test**

Append to `cmd/bridge/dispatch_test.go`:

```go
func TestDecisionStatus(t *testing.T) {
	auto := dispatch.Lane{Name: "auto", Autonomous: true}
	dry := dispatch.Lane{Name: "auto", Autonomous: true, DryRun: true}

	tests := []struct {
		name string
		d    dispatch.Decision
		want string
	}{
		{"live dispatch", dispatch.Decision{
			Candidate: dispatch.Candidate{Lane: auto}, Dispatch: true}, "dispatch"},
		{"dry-run dispatch", dispatch.Decision{
			Candidate: dispatch.Candidate{Lane: dry}, Dispatch: true}, "WOULD dispatch (dry-run)"},
		{"skip", dispatch.Decision{
			Candidate: dispatch.Candidate{Lane: auto}, Reason: "repo at WIP 1/1"},
			"SKIP (repo at WIP 1/1)"},
		{"skip after a lane downgrade shows both", dispatch.Decision{
			Candidate: dispatch.Candidate{Lane: dispatch.Lane{Name: "hitl"},
				LaneReason: "auto→hitl: agent.yml lacks ai-review-ai-merge: true"},
			Reason: "global cap 3/3"},
			"SKIP (auto→hitl: agent.yml lacks ai-review-ai-merge: true; global cap 3/3)"},
		{"a downgrade is still visible on a dispatch", dispatch.Decision{
			Candidate: dispatch.Candidate{Lane: dispatch.Lane{Name: "hitl"},
				LaneReason: "auto→hitl: agent.yml lacks ai-review-ai-merge: true"},
			Dispatch: true},
			"dispatch (auto→hitl: agent.yml lacks ai-review-ai-merge: true)"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := decisionStatus(tc.d); got != tc.want {
				t.Errorf("got %q want %q", got, tc.want)
			}
		})
	}
}

func TestRenderDecisionsShowsTheLaneColumn(t *testing.T) {
	var buf bytes.Buffer
	renderDecisions(&buf, []dispatch.Decision{{
		Candidate: dispatch.Candidate{
			Repo: "game-tschau-sepp", Issue: forge.Issue{Number: 14, Title: "fix: card flip race"},
			Lane: dispatch.Lane{Name: "auto", Autonomous: true, DryRun: true},
		},
		Dispatch: true,
	}})

	out := buf.String()
	if !strings.Contains(out, "auto") || !strings.Contains(out, "WOULD dispatch (dry-run)") {
		t.Errorf("lane column and dry-run status must both show:\n%s", out)
	}
}

func TestDecisionsJSONCarriesTheLane(t *testing.T) {
	js := decisionsJSON([]dispatch.Decision{{
		Candidate: dispatch.Candidate{
			Repo: "game-tschau-sepp", Issue: forge.Issue{Number: 14, Title: "fix: card flip race"},
			Lane: dispatch.Lane{Name: "auto", Autonomous: true, DryRun: true},
		},
		Dispatch: true,
	}})

	if len(js) != 1 {
		t.Fatalf("%+v", js)
	}
	got := js[0]
	if got.Lane != "auto" || !got.DryRun || got.Repo != "game-tschau-sepp" || got.Issue != 14 {
		t.Errorf("%+v", got)
	}
	if !slices.Equal(got.Labels, []string{"ai-implement", "ai-review-ai-merge"}) {
		t.Errorf("labels: %v", got.Labels)
	}
}
```

Ensure the file imports `"bytes"`, `"slices"` and `"strings"`.

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./cmd/bridge/ -run 'TestDecisionStatus|TestRenderDecisionsShows|TestDecisionsJSON' -v`
Expected: FAIL to compile — `undefined: decisionStatus`, `undefined: decisionsJSON`.

- [ ] **Step 3: Implement the renderers**

In `cmd/bridge/dispatch.go`, replace `renderDecisions` and add the helpers (add `"strings"` to the imports):

```go
// decisionStatus renders one decision's outcome. A lane downgrade always shows,
// dispatch or skip: "why is this repo not in the auto lane" is the question the
// observation week exists to answer.
func decisionStatus(d dispatch.Decision) string {
	reason := d.Candidate.LaneReason
	switch {
	case d.Dispatch && d.Candidate.Lane.DryRun:
		return withReason("WOULD dispatch (dry-run)", reason)
	case d.Dispatch:
		return withReason("dispatch", reason)
	default:
		if reason != "" {
			return fmt.Sprintf("SKIP (%s; %s)", reason, d.Reason)
		}
		return fmt.Sprintf("SKIP (%s)", d.Reason)
	}
}

func withReason(status, reason string) string {
	if reason == "" {
		return status
	}
	return fmt.Sprintf("%s (%s)", status, reason)
}

func renderDecisions(w io.Writer, ds []dispatch.Decision) {
	dispatched, skipped := 0, 0
	for _, d := range ds {
		if d.Dispatch {
			dispatched++
		} else {
			skipped++
		}
		fmt.Fprintf(w, "  %-16s #%-4d %-28s %-6s → %s\n",
			d.Candidate.Repo, d.Candidate.Issue.Number,
			truncate(d.Candidate.Issue.Title, 28),
			d.Candidate.Lane.Name, decisionStatus(d))
	}
	fmt.Fprintf(w, "\n%d dispatched, %d skipped\n", dispatched, skipped)
}

// decisionJSON is the machine-readable view of a decision. Decision itself
// carries no json tags and nests a whole forge.Issue; a named view keeps the
// documented shape stable as the struct grows.
type decisionJSON struct {
	Repo       string   `json:"repo"`
	Issue      int      `json:"issue"`
	Title      string   `json:"title"`
	Lane       string   `json:"lane"`
	LaneReason string   `json:"lane_reason,omitempty"`
	Labels     []string `json:"labels"`
	DryRun     bool     `json:"dry_run"`
	Dispatch   bool     `json:"dispatch"`
	Reason     string   `json:"reason,omitempty"`
}

func decisionsJSON(ds []dispatch.Decision) []decisionJSON {
	out := make([]decisionJSON, 0, len(ds))
	for _, d := range ds {
		out = append(out, decisionJSON{
			Repo:       d.Candidate.Repo,
			Issue:      d.Candidate.Issue.Number,
			Title:      d.Candidate.Issue.Title,
			Lane:       d.Candidate.Lane.Name,
			LaneReason: d.Candidate.LaneReason,
			Labels:     d.Candidate.Lane.EffectiveLabels(),
			DryRun:     d.Candidate.Lane.DryRun,
			Dispatch:   d.Dispatch,
			Reason:     d.Reason,
		})
	}
	return out
}
```

In `runDispatch`, the JSON branch becomes `emitJSON(cmd.OutOrStdout(), decisionsJSON(decisions))`.

- [ ] **Step 4: Add the per-lane block to `status`**

In `runDispatchStatus`, add a `laneStatus` slice to the JSON struct and print the same information in the text branch. Append after the existing `per-repo cap` line:

```go
	for _, l := range cfg.Lanes {
		start := l.WindowStart(cfg.Schedule, now)
		spent := state.DispatchesInLane(l.Name, start)
		mode := "live"
		if l.DryRun {
			mode = "dry-run"
		}
		ceiling := "∞"
		if l.Limits.MaxDispatches > 0 {
			ceiling = strconv.Itoa(l.Limits.MaxDispatches)
		}
		fmt.Fprintf(w, "lane %s: %s, autonomous=%t, dispatched %d/%s this window\n",
			l.Name, mode, l.Autonomous, spent, ceiling)
	}
```

Add `"strconv"` to the imports, and add to the JSON status struct:

```go
			Lanes []laneStatusJSON `json:"lanes,omitempty"`
```

with

```go
// laneStatusJSON reports one lane's configuration and spent counter.
type laneStatusJSON struct {
	Name          string `json:"name"`
	Autonomous    bool   `json:"autonomous"`
	DryRun        bool   `json:"dry_run"`
	Dispatched    int    `json:"dispatched"`
	MaxDispatches int    `json:"max_dispatches,omitempty"`
	InWindow      bool   `json:"in_window"`
}

func laneStatuses(cfg dispatch.Config, state dispatch.State, now time.Time) []laneStatusJSON {
	out := make([]laneStatusJSON, 0, len(cfg.Lanes))
	for _, l := range cfg.Lanes {
		out = append(out, laneStatusJSON{
			Name:          l.Name,
			Autonomous:    l.Autonomous,
			DryRun:        l.DryRun,
			Dispatched:    state.DispatchesInLane(l.Name, l.WindowStart(cfg.Schedule, now)),
			MaxDispatches: l.Limits.MaxDispatches,
			InWindow:      l.InWindow(cfg.Schedule, now),
		})
	}
	return out
}
```

and set `Lanes: laneStatuses(cfg, state, now)` in the emitted struct.

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./... -race && gofmt -l . && go vet ./... && golangci-lint run`
Expected: tests PASS, `gofmt -l .` prints nothing, vet and lint clean.

- [ ] **Step 6: Commit**

```bash
git add cmd/bridge/dispatch.go cmd/bridge/dispatch_test.go
git commit -m "feat(dispatch): show the lane in dry-run, json and status output"
```

---

### Task 9: Timer cadence and documentation

**Files:**
- Modify: `docs/systemd/bridge-dispatch.timer`
- Modify: `docs/dispatch.md`
- Modify: `CHANGELOG.md`

**Interfaces:**
- Consumes: the finished feature. Produces: nothing code depends on.

- [ ] **Step 1: Halve the heartbeat**

`docs/systemd/bridge-dispatch.timer` becomes:

```ini
[Unit]
Description=bridge dispatch — half-hourly heartbeat; the dispatch windows live in dispatch.json

[Timer]
OnCalendar=*-*-* *:00,30:00
Persistent=false

[Install]
WantedBy=timers.target
```

- [ ] **Step 2: Document lanes in `docs/dispatch.md`**

Add a `## Autonomy lanes` section after the caps section, covering: the `lanes` array and every field (`name`, `repos`, `autonomous`, `dry_run`, `windows`, `labels`, `limits.per_repo`, `limits.overrides`, `limits.max_dispatches`); that no `lanes` key means today's behaviour through the implicit default lane; that an autonomous lane is exempt from `global_open_prs` both ways and is gated on `ai-review-ai-merge: true` in the repo's `agent.yml` (cached 6h at `~/.cache/bridge/lane-gate.json`, failure falls back to human review); and that `dry_run` observes without consuming. Include the worked config from the spec and a sample `--dry-run` rendering. Update the doc's existing `limits` table row list and the line describing the hourly timer to say half-hourly.

- [ ] **Step 3: Add the changelog entry**

Under `## [Unreleased]` → `### Added` in `CHANGELOG.md`:

```markdown
- `bridge dispatch` autonomy lanes: per-repo `auto` vs `hitl` lanes with their own
  windows, caps and labels. An autonomous lane is exempt from `global_open_prs`
  both ways, is gated on `ai-review-ai-merge: true` in the repo's `agent.yml`
  (falling back to human review when unmet), and can run `dry_run` to be observed
  before it acts. The systemd heartbeat moves to half-hourly. (#304)
```

- [ ] **Step 4: Verify the whole suite one last time**

Run: `gofmt -l . && go vet ./... && golangci-lint run && go test -race ./... && go build ./...`
Expected: all clean.

- [ ] **Step 5: Commit**

```bash
git add docs/systemd/bridge-dispatch.timer docs/dispatch.md CHANGELOG.md
git commit -m "docs(dispatch): document autonomy lanes, halve the timer heartbeat"
```

---

## Before going live (not part of this plan's tasks)

The auto lane ships with `"dry_run": true`. It must stay that way until **both**
hold:

1. [#303](https://github.com/freaxnx01/bridge/issues/303) has shipped — otherwise an
   unlabeled, empty capture-created issue is dispatchable into an auto-merging
   pipeline.
2. A week of `--dry-run` output has been reviewed and the lane assignments and
   caps look right.

Removing `dry_run` is a one-word edit to `~/.config/bridge/dispatch.json`; no code
change and no redeploy.
