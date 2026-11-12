# Cockpit Dispatch Insight Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Record every dispatch tick (with *why* each issue ranked where it did) and show verdicts, gates and a week of tick history in the `bridge serve` cockpit.

**Architecture:** The decision path that `runDispatch` assembles inline moves into one pure `dispatch.Evaluate`, used by both the CLI tick and a new live preview in the cockpit collector. A new `internal/dispatchrun` package holds the tick's I/O (lane gate fetch, budget measurement, tick log, history summary). The Svelte cockpit gets a gate strip, verdict badges on Ready cards, and a Dispatcher area.

**Tech Stack:** Go (stdlib, cobra), Svelte 5 + Vitest in `web/`.

**Spec:** [`docs/superpowers/specs/2026-11-12-cockpit-dispatch-insight-design.md`](../specs/2026-11-12-cockpit-dispatch-insight-design.md)

## Global Constraints

- Logic in `internal/`; `cmd/bridge` only wires. No new Go modules, no new npm packages.
- Stdlib `testing`, table-driven, hand-rolled fakes; `go test -race ./...`, `go vet ./...`, `gofmt -l .` (empty), `golangci-lint run` all clean.
- Frontend: `cd web && npm test` (Vitest) and `npm run build` green.
- `bridge dispatch` text output stays byte-identical; `--json` changes are additive only.
- A dry-run tick changes no dispatcher state. The tick log is never read as dispatch input.
- A tick never fails or changes exit code because the tick log could not be written (`slog.Warn` only).
- Tick log: `~/.cache/bridge/dispatch-ticks.jsonl` (via `cacheRoot()`), JSON Lines, retention **30 days**. Paused ticks and `--auto` ticks with no acting lane are not logged.
- New endpoints are open `GET`s; no new `POST`s.
- Live preview refreshes with the issues source (10 min) and uses the *next window* semantics (`GateNextWindow`).
- `App.svelte` / `Header.svelte` edits limited to adding one area (`dispatcher`) — #368 edits the same files.

## Review Focus

1. **Fresh install, no tick log file yet** → history endpoint returns empty `summary`/`ticks` (`[]`, not `null`), status 200. Test in Task 6.
2. **Timer writing while serve reads** (partial last line) → reader skips the malformed line, returns the rest. Test in Task 5.
3. **`dispatch.json` malformed while serve runs** → preview error listed under source `dispatch`, last good preview kept, board still served without new badges. Test in Task 8.
4. **A window is open right now** → `GateNextWindow` evaluates at `now`, not at the next opening (otherwise badges lie inside the window). Test in Task 3.
5. **Bad `{at}` or `days` in the URL** → 400 with JSON error; well-formed but unknown `at` → 404. Test in Task 7.

---

### Task 1: Move tick helpers and timing into `internal/dispatch`

Pure refactor; no behaviour change.

**Files:**
- Create: `internal/dispatch/tick.go`, `internal/dispatch/timing.go`
- Create: `internal/dispatch/tick_test.go`, `internal/dispatch/timing_test.go` (moved from `cmd/bridge/dispatch_test.go` cases)
- Modify: `cmd/bridge/dispatch.go` (delete moved code, call the exported versions)

**Interfaces:**
- Produces (exported, same bodies as today's unexported versions in `cmd/bridge/dispatch.go`):
  - `type RepoInput struct { Forge, Owner, Name string; Issues []forge.Issue; Milestones []forge.Milestone; PRs []forge.PullRequest }`
  - `func CollectCandidates(repos []RepoInput) []Candidate`
  - `func CountOpenAgentPRs(repos []RepoInput, autonomous func(repo string) bool) (map[string]int, int)`
  - `func MergeInOrder(ordered []Candidate, groups ...[]Decision) []Decision`
  - `func AssignLanes(cs []Candidate, lanes []Lane, gate GateState)`
  - `func LaneCounts(state State, cfg Config, now time.Time) map[string]int`
  - `func AnyLaneActs(cfg Config, now time.Time) bool`
  - `type Timing struct` (today's `tickTiming`, same fields) and `func ResolveTiming(cfg Config, now time.Time) Timing`
  - `func NightStartForReport(cfg Config, t Timing, now time.Time) time.Time`
  - `func RungLabel(t Timing) string`

- [ ] **Step 1:** Move the tests for these helpers from `cmd/bridge/dispatch_test.go` into `internal/dispatch/tick_test.go` / `timing_test.go`, renamed to the exported names. Run `go test ./internal/dispatch/` → FAIL (undefined).
- [ ] **Step 2:** Move the code; `cmd/bridge/dispatch.go` calls `dispatch.X`. `repoInput` in `cmd/bridge` becomes `dispatch.RepoInput` (update `fetchRepoInputs`, `lane_gate.go`).
- [ ] **Step 3:** Run `go test -race ./...` → PASS. Run `go run ./cmd/bridge dispatch --dry-run` before and after (with `direnv exec ~/repos/github/freaxnx01`) and `diff` the outputs → identical.
- [ ] **Step 4:** Commit `refactor(dispatch): move tick helpers and timing into internal/dispatch (#387)`.

### Task 2: Ranking explanation

**Files:**
- Create: `internal/dispatch/rank.go`, `internal/dispatch/rank_test.go`

**Interfaces:**
- Consumes: `Order`, `repoPriorityRank`, `compareDue`, `typeRank`, `sizeRank` (`order.go`).
- Produces:
  ```go
  type Ladder struct {
      RepoPattern  string    `json:"repo_pattern,omitempty"` // matched repo_priority pattern, "" if none
      Milestone    string    `json:"milestone,omitempty"`
      MilestoneDue time.Time `json:"milestone_due,omitempty"`
      Type         string    `json:"type"` // "bug" | "feat" | "other"
      Size         string    `json:"size"` // "s" | "m" | "l"
      Created      time.Time `json:"created"`
  }
  type Rank struct {
      Position int    // 1-based in Order's output
      Reason   string // deciding rung vs the next candidate; "" for the last
      Ladder   Ladder
  }
  func Explain(ordered []Candidate, repoPriority []string) []Rank // same length and order as ordered
  ```
  `Candidate` gains `Milestone string` (set by `CollectCandidates` to the active milestone title).

- [ ] **Step 1: Write the failing test** `TestExplain_AdjacentCandidates_NamesFirstDifferingRung`, table-driven over pairs `a` (first) and `b` (second) already in order, with `repoPriority = []string{"agent-workflow", "*"}`:
  | case | difference | want `Reason` for a |
  |---|---|---|
  | repo | a in `agent-workflow`, b in `bridge` | `repo priority: agent-workflow vs *` |
  | due | same repo, due 2026-11-20 vs 2026-12-01 | `milestone due: 2026-11-20 vs 2026-12-01` |
  | type | labels `bug` vs `enhancement` | `type: bug vs feat` |
  | size | `size:s` vs unsized | `size: s vs m` |
  | age | created 2026-10-01 vs 2026-10-05 | `age: 2026-10-01 vs 2026-10-05` |
  Plus `TestExplain_LastCandidate_HasNoReason` (single candidate → `Position 1`, `Reason ""`) and `TestExplain_Ladder_FilledFromCandidate` (asserts all six `Ladder` fields for one candidate with `bug`, `size:l`, milestone `M1`).
- [ ] **Step 2:** `go test ./internal/dispatch -run TestExplain` → FAIL.
- [ ] **Step 3:** Implement. Rung comparison order and equality must reuse the same rank functions `Order` uses, so `Explain` can never disagree with `Order`. Due dates format `2006-01-02`; a zero due date renders `none`.
- [ ] **Step 4:** Run → PASS; `go test -race ./...` → PASS.
- [ ] **Step 5:** Commit `feat(dispatch): explain each candidate's rank (#387)`.

### Task 3: `Evaluate` — one decision path for CLI and preview

**Files:**
- Create: `internal/dispatch/evaluate.go`, `internal/dispatch/evaluate_test.go`
- Modify: `internal/dispatch/caps.go` (`Decision` gains `Rank Rank`), `cmd/bridge/dispatch.go` (`runDispatch` uses `Evaluate`; `decisionJSON` gains fields)

**Interfaces:**
- Consumes: Task 1 helpers, Task 2 `Explain`.
- Produces:
  ```go
  type WindowMode int
  const (
      GateNow        WindowMode = iota // --auto: out-of-window lanes skip "outside window"
      GateNextWindow                    // live preview: evaluate at NextActingInstant
      GateNone                          // manual tick: no window split (today's !auto path)
  )
  type TickInput struct {
      Repos     []RepoInput
      Config    Config
      State     State
      Gate      GateState
      UsedUSD   float64 // measured trailing usage; ignored when !UsedKnown
      UsedKnown bool
      Now       time.Time
      Mode      WindowMode
  }
  type LaneGate struct {
      Name       string `json:"name"`
      DryRun     bool   `json:"dryRun"`
      Autonomous bool   `json:"autonomous"`
      Dispatched int    `json:"dispatched"`
      Max        int    `json:"max"` // 0 = unbounded
  }
  type Gates struct {
      At             time.Time      `json:"at"`             // instant evaluated
      WindowOpen     bool           `json:"windowOpen"`     // at Now, not At
      NextOpening    time.Time      `json:"nextOpening,omitempty"`
      RungOn         bool           `json:"rungOn"`
      UsedUSD        float64        `json:"usedUsd"`
      AllowedUSD     float64        `json:"allowedUsd"`     // WindowBudgetUSD * DaytimeCap
      NightCount     int            `json:"nightCount"`
      NightCap       int            `json:"nightCap"`
      GlobalOpen     int            `json:"globalOpen"`
      GlobalCap      int            `json:"globalCap"`
      OpenByRepo     map[string]int `json:"openByRepo"`
      Paused         bool           `json:"paused"`
      LastTick       time.Time      `json:"lastTick,omitempty"`
      Lanes          []LaneGate     `json:"lanes"`
  }
  type Tick struct {
      Decisions []Decision // in Order's order, each with Rank set
      Gates     Gates
  }
  func Evaluate(in TickInput) Tick
  func NextActingInstant(cfg Config, now time.Time) time.Time // now if any lane acts now
  ```
  `decisionJSON` (cmd) gains `Rank int "rank"`, `RankReason string "rank_reason,omitempty"`, `Ladder dispatch.Ladder "ladder"`; it moves to `internal/dispatch` as exported `DecisionJSON` + `func DecisionsJSON([]Decision) []DecisionJSON` because Task 5 logs it.

- [ ] **Step 1: Write failing tests** in `evaluate_test.go`, config with one window `18:00–07:00` (rung off) and one `07:00–18:00` (rung on), `window_budget_usd 12`, `daytime_cap 0.8`, two eligible issues in one repo (`per_repo 1`):
  - `TestEvaluate_GateNowOutsideWindows_SkipsOutsideWindow` — schedule with only `18:00–07:00`, `Now` 14:00 → both decisions `Dispatch false`, `Reason` contains `outside`.
  - `TestEvaluate_GateNextWindowAtAfternoon_EvaluatesAtEvening` — same config, `Now` 14:00, `Mode GateNextWindow` → `Gates.At` = 18:00 same day, `Gates.WindowOpen false`, first decision `Dispatch true`, second `Reason` mentions per-repo cap.
  - `TestEvaluate_GateNextWindowInsideWindow_EvaluatesAtNow` — `Now` 22:00 → `Gates.At == Now`, `WindowOpen true`.
  - `TestEvaluate_RungExhausted_SkipsWithBudgetReason` — both windows, `Now` 14:00, `GateNow`, `UsedUSD 20` → reasons contain `budget-exhausted 20.00/9.60 USD`; `Gates.AllowedUSD == 9.6`.
  - `TestEvaluate_DecisionsCarryRank` — `Decisions[0].Rank.Position == 1`, `Decisions[1].Rank.Position == 2`.
  - `TestEvaluate_DoesNotMutateInputState` — deep-compare `in.State` before/after.
- [ ] **Step 2:** `go test ./internal/dispatch -run TestEvaluate` → FAIL.
- [ ] **Step 3:** Implement `Evaluate` as today's `runDispatch` body from `resolveTiming` to `mergeInOrder`, plus `Explain`, plus `Gates`. For `GateNextWindow`: `at := NextActingInstant(cfg, now)`, then resolve timing and partition at `at` (as `GateNow`); `WindowOpen` uses `now`. The rung's *measured* usage stays the caller's `UsedUSD` (measured at now). `NextActingInstant` scans forward in 1-minute steps up to 24 h using `AnyLaneActs` (simple and exact for minute-granular `HH:MM` spans).
- [ ] **Step 4:** Rewrite `runDispatch`: config/state/pause → early exit for `--auto && !AnyLaneActs` (unchanged) → `fetchRepoInputs` → `resolveGate` → measure budget if `ResolveTiming(cfg, now).RungOn` → `Evaluate` (`GateNow` if `--auto`, else `GateNone`) → render → apply. Delete code now inside `Evaluate`.
- [ ] **Step 5:** Run `go test -race ./...` → PASS; re-run the Task 1 `diff` of `--dry-run` text output → identical. `--dry-run --json` shows `rank`, `rank_reason`, `ladder`.
- [ ] **Step 6:** Commit `feat(dispatch): evaluate a tick in one pure function, with ranks in --json (#387)`.

### Task 4: `internal/dispatchrun` — lane gate and budget measurement

**Files:**
- Create: `internal/dispatchrun/gate.go`, `internal/dispatchrun/budget.go`, tests beside them (moved from `cmd/bridge/lane_gate_test.go` and the budget-measure tests)
- Modify: `cmd/bridge/lane_gate.go` (thin wrapper or deleted), `cmd/bridge/dispatch.go`

**Interfaces:**
- Produces:
  ```go
  type GateFetcher func(ctx context.Context, owner, repo string) (content []byte, found bool, err error)
  func ResolveGate(ctx context.Context, repos []dispatch.RepoInput, lanes []dispatch.Lane, now time.Time, fetch GateFetcher, cachePath string) dispatch.GateState
  func MeasureWindowUSD(ctx context.Context, cfg dispatch.Config, transcriptRoot, ledgerPath string, from, to time.Time) (float64, bool)
  ```
  Bodies are today's `resolveGateWith` (+ cache types, `agentYAMLOptsIntoAIMerge`, `laneGateTTL`) and `measureWindowUSD` with paths injected. `cmd/bridge` keeps `fetchAgentWorkflow`, `laneGatePath`, `transcriptRoot`, `dispatchLedgerPath` and passes them in.

- [ ] **Step 1:** Move the tests, retarget to the new package; `go test ./internal/dispatchrun/` → FAIL.
- [ ] **Step 2:** Move the code.
- [ ] **Step 3:** `go test -race ./...` → PASS.
- [ ] **Step 4:** Commit `refactor(dispatch): move lane gate and budget measurement into internal/dispatchrun (#387)`.

### Task 5: Tick log

**Files:**
- Create: `internal/dispatchrun/ticklog.go`, `internal/dispatchrun/ticklog_test.go`
- Modify: `cmd/bridge/dispatch.go` (append after render, before apply), `cmd/bridge/dispatch_test.go`

**Interfaces:**
- Consumes: `dispatch.Tick`, `dispatch.DecisionsJSON`, `dispatch.Gates`.
- Produces:
  ```go
  const TickRetention = 30 * 24 * time.Hour
  type TickRecord struct {
      At        time.Time               `json:"at"`
      Mode      string                  `json:"mode"`    // "dry-run" | "live" (the --dry-run flag)
      Trigger   string                  `json:"trigger"` // "auto" | "manual"
      Gates     dispatch.Gates          `json:"gates"`
      Decisions []dispatch.DecisionJSON `json:"decisions"`
  }
  func AppendTick(path string, rec TickRecord, now time.Time) error
  func ReadTicks(path string, since time.Time) ([]TickRecord, error) // oldest first; missing file → empty, nil
  ```
  `func tickLogPath() string { return filepath.Join(cacheRoot(), "dispatch-ticks.jsonl") }` in `cmd/bridge`.

- [ ] **Step 1: Write failing tests:**
  - `TestAppendTick_ThenRead_RoundTrips` — two records, `ReadTicks(since zero)` returns both, equal fields.
  - `TestAppendTick_OldestBeyondRetention_PrunesOnAppend` — pre-seed a record 31 days before `now` and one 1 day before; append one at `now`; raw file has 2 lines.
  - `TestReadTicks_MalformedLine_IsSkipped` — file with valid line, `{"at":` partial line, valid line → 2 records, nil error.
  - `TestReadTicks_MissingFile_ReturnsEmpty` — `len == 0`, `err == nil`.
  - `TestReadTicks_Since_FiltersOlder`.
  - In `cmd/bridge/dispatch_test.go`: `TestRunDispatch_TickLogUnwritable_StillSucceeds` — point `XDG_CACHE_HOME` (read by `cacheRoot()`, `cmd/bridge/list.go:38`) at a dir whose `bridge/` subdir is read-only (`t.TempDir` + `os.Chmod 0o500`), fake repos as existing tests do, `--dry-run` → `err == nil`.
- [ ] **Step 2:** Run → FAIL.
- [ ] **Step 3:** Implement. Append: open `O_APPEND|O_CREATE|O_WRONLY` 0o600, one `Write` of the marshalled line + `\n`. Prune only when the first line's `at` is older than `now - TickRetention`: read all, keep newer, write temp file in same dir, `os.Rename`. `runDispatch` builds the record (`Trigger` `auto` iff `--auto`) and on error `slog.Warn("dispatch: cannot write tick log", "error", err)`.
- [ ] **Step 4:** `go test -race ./...` → PASS.
- [ ] **Step 5:** Commit `feat(dispatch): log every evaluated tick to dispatch-ticks.jsonl (#387)`.

### Task 6: History summary

**Files:**
- Create: `internal/dispatchrun/history.go`, `internal/dispatchrun/history_test.go`

**Interfaces:**
- Consumes: `TickRecord`.
- Produces:
  ```go
  type IssueSummary struct {
      Repo           string `json:"repo"`
      Issue          int    `json:"issue"`
      Title          string `json:"title"`
      EvaluatedTicks int    `json:"evaluatedTicks"`
      PickedTicks    int    `json:"pickedTicks"`
      TopReason      string `json:"topReason"`
  }
  type TickCandidate struct { Repo string `json:"repo"`; Issue int `json:"issue"`; Dispatch bool `json:"dispatch"` }
  type TickHeader struct {
      At           time.Time       `json:"at"`
      Mode         string          `json:"mode"`
      Trigger      string          `json:"trigger"`
      PickedCount  int             `json:"pickedCount"`
      SkippedCount int             `json:"skippedCount"`
      Candidates   []TickCandidate `json:"candidates"`
  }
  type History struct { Summary []IssueSummary `json:"summary"`; Ticks []TickHeader `json:"ticks"` }
  func Summarize(ticks []TickRecord) History // never nil slices
  ```

- [ ] **Step 1: Write failing tests:**
  - `TestSummarize_CountsAutoTicksOnly` — one `auto` tick picking #1, one `manual` tick picking #1 → `PickedTicks 1`, `EvaluatedTicks 1`; `Ticks` has 2 headers.
  - `TestSummarize_DryRunLaneWouldDispatch_CountsAsPicked` — decision `dispatch true, dry_run true` → picked.
  - `TestSummarize_TopReason_PrefersRankReasonWhenPicked` — #1 picked twice with `type: bug vs feat`, skipped once `per-repo cap` → `TopReason "type: bug vs feat"`.
  - `TestSummarize_TopReason_FallsBackToSkipReason` — never picked, skipped twice `budget-exhausted …`, once `global cap` → the budget reason.
  - `TestSummarize_Empty_ReturnsEmptySlicesNotNil` — JSON-marshal gives `{"summary":[],"ticks":[]}`.
  - Summary order: `PickedTicks` desc, then `EvaluatedTicks` desc, then repo, issue.
- [ ] **Step 2:** Run → FAIL. **Step 3:** Implement (ties in "most frequent" resolve to the most recent tick's reason). **Step 4:** Run → PASS.
- [ ] **Step 5:** Commit `feat(dispatch): summarize the tick log per issue (#387)`.

### Task 7: History and tick endpoints

**Files:**
- Create: `internal/api/dispatch.go`, `internal/api/dispatch_test.go`
- Modify: `cmd/bridge/serve.go` (register), `cmd/bridge/pipeline.go` (handler constructor)

**Interfaces:**
- Consumes: `dispatchrun.ReadTicks`, `Summarize`, `TickRecord`.
- Produces:
  ```go
  type DispatchHandler struct {
      Ticks func(since time.Time) ([]dispatchrun.TickRecord, error)
      Now   func() time.Time
  }
  // GET /api/dispatch/history?days=N  (N in 1..30, default 7) → dispatchrun.History
  // GET /api/dispatch/ticks/{at}      (at = RFC3339, exact match on record At) → dispatchrun.TickRecord
  func (h *DispatchHandler) ServeHTTP(w http.ResponseWriter, r *http.Request)
  ```
  Registered as `apiMux.Handle("/api/dispatch/", dispatchH)` (open, like `GET /api/pipeline`). Non-GET → 405. Errors use the existing JSON error helper in `internal/api/errors.go`.

- [ ] **Step 1: Write failing tests** (fake `Ticks`):
  - `TestDispatchHistory_DefaultDays_Seven` — asserts `since == Now - 7*24h`.
  - `TestDispatchHistory_NoTicks_EmptyArrays` — body `{"summary":[],"ticks":[]}`, 200.
  - `TestDispatchHistory_BadDays_400` — `days=0`, `days=31`, `days=x`.
  - `TestDispatchTick_Found_200`, `TestDispatchTick_Unknown_404`, `TestDispatchTick_BadTimestamp_400`.
  - `TestDispatch_Post_405`.
- [ ] **Step 2:** Run → FAIL. **Step 3:** Implement; wire in `serve.go` with `Ticks: func(s time.Time) (…) { return dispatchrun.ReadTicks(tickLogPath(), s) }`. **Step 4:** `go test -race ./...` → PASS.
- [ ] **Step 5:** Commit `feat(api): serve dispatch tick history (#387)`.

### Task 8: Live preview in the collector and board

**Files:**
- Modify: `internal/cockpit/collector.go`, `internal/cockpit/board.go`, their tests; `cmd/bridge/pipeline.go` (`cockpitSources`)

**Interfaces:**
- Consumes: `dispatch.Evaluate`, `dispatch.Tick`, `dispatch.Gates`, `dispatch.RepoInput`.
- Produces:
  - `Sources.Preview func(ctx context.Context, repos []dispatch.RepoInput) (dispatch.Tick, error)` (nil → no preview).
  - `Input.Preview *dispatch.Tick`.
  - `IssueCard` gains `Verdict string "verdict,omitempty"` (`"next"` | `"skip"`), `VerdictReason string "verdictReason,omitempty"`, `Lane string "lane,omitempty"`.
  - `Board` gains `Gates *dispatch.Gates "gates,omitempty"`.
  - Collector: after `CollectIssues` finishes, it converts its GitHub `RepoData` to `[]dispatch.RepoInput` and calls `Preview`; on error `setErrLocked("dispatch", err)` and keeps the previous preview.
  - `cockpitSources` `Preview`: re-reads `dispatch.LoadConfig(dispatchConfigPath())` and `dispatch.ReadState(dispatchStatePath())` each call, resolves the gate via `dispatchrun.ResolveGate`, measures budget via `dispatchrun.MeasureWindowUSD` when `ResolveTiming(cfg, now).RungOn`, then `Evaluate` with `Mode: GateNextWindow`; sets `Gates.Paused = state.Paused`, `Gates.LastTick = state.LastTick` (`internal/dispatch/types.go:98`). In `Evaluate` itself, `Gates.Paused`/`LastTick` are filled from `in.State` the same way, so the CLI path gets them too.

- [ ] **Step 1: Write failing tests:**
  - `TestBuildBoard_WithPreview_ReadyInPreviewOrderWithVerdicts` — preview decides #2 (rank 1, dispatch) and #1 (rank 2, skip `per-repo cap 1/1`) → `Ready[0].Number == 2`, `Verdict "next"`; `Ready[1].Verdict "skip"`, `VerdictReason "per-repo cap 1/1"`; `Board.Gates != nil`.
  - `TestBuildBoard_WithoutPreview_UnchangedOrder` — existing ordering assertions hold, `Verdict ""`.
  - `TestCollector_PreviewError_ListedAndPreviousKept` — fake `Preview` succeeds once, then errors → `Board().Errors` has source `dispatch`; Ready verdicts from the first preview still present.
  - `TestCollector_PreviewReceivesOnlyGitHubRepos`.
- [ ] **Step 2:** Run → FAIL. **Step 3:** Implement. A Ready issue missing from the preview (e.g. collected after it) keeps its place at the end with no verdict. **Step 4:** `go test -race ./...` → PASS.
- [ ] **Step 5:** Commit `feat(cockpit): live dispatch verdicts and gates on the pipeline board (#387)`.

### Task 9: Frontend — gate strip and Ready badges

**Files:**
- Create: `web/src/lib/components/GateStrip.svelte`, `GateStrip.test.js`
- Modify: `web/src/lib/components/IssueCard.svelte`, `IssueCard.test.js`, `web/src/routes/Pipeline.svelte`, `Pipeline.test.js`

**Interfaces:**
- Consumes: `/api/pipeline` → `gates`, `ready[].verdict|verdictReason|lane`.
- Produces: `<GateStrip gates={…} />` (props: the `gates` object; renders nothing when undefined) — reused by Task 10.

- [ ] **Step 1: Write failing tests:**
  - `GateStrip`: window closed with `nextOpening` 18:00 → text `window closed · opens 18:00`; `usedUsd 418.27`, `allowedUsd 9.6`, `rungOn true` → `budget 4357%` with a warn class; `nightCount 2`, `nightCap 5` → `night 2/5`; `globalOpen 1/3` → `PRs 1/3`; `paused true` → `paused`; lane `{name:"auto", dryRun:true, dispatched:3, max:12}` → `auto: dry-run 3/12`.
  - `IssueCard`: `verdict "next"` → badge `next`; `verdict "skip"`, `verdictReason "per-repo cap 1/1"` → badge `skip: per-repo cap 1/1`; no verdict → no badge.
  - `Pipeline`: renders `GateStrip` above the columns when `gates` present.
- [ ] **Step 2:** `cd web && npm test` → FAIL. **Step 3:** Implement with existing CSS variables/classes. **Step 4:** `npm test && npm run build` → PASS.
- [ ] **Step 5:** Commit `feat(web): gate strip and dispatch verdict badges (#387)`.

### Task 10: Frontend — Dispatcher area

**Files:**
- Create: `web/src/routes/Dispatcher.svelte`, `Dispatcher.test.js`, `web/src/lib/stores/dispatchHistory.js`, `dispatchHistory.test.js`
- Modify: `web/src/App.svelte` (area `dispatcher`), `web/src/lib/components/Header.svelte` (one nav button `Dispatcher` after `Pipeline`)

**Interfaces:**
- Consumes: `GET /api/dispatch/history?days=N`, `GET /api/dispatch/ticks/{at}`, `GateStrip`, `$pipeline.gates`.
- Produces: `loadHistory(days)`, `loadTick(at)` in `dispatchHistory.js` (via `lib/api.js` `get`).

- [ ] **Step 1: Write failing tests** (mock `fetch`):
  - Period selector `1d / 7d / 30d`, default `7d` → first request `/api/dispatch/history?days=7`.
  - Summary table rows in API order, columns `Issue`, `Picked` (`3 / 14`), `Typical why`.
  - Timeline renders one chip per tick with its `pickedCount`; chip with `pickedCount 0` has a dimmed class; manual ticks show a `manual` marker.
  - Clicking a summary row filters chips to ticks whose `candidates` include that issue and requests `/api/dispatch/ticks/<latest at>` (URL-encoded RFC3339).
  - Tick detail table columns `#`, `Issue`, `Verdict` (`✓ would pick` when `dispatch`, else `✗ skip`), `Why` (`rank_reason` when picked, else `reason`; `lane_reason` appended when set), plus the tick's `GateStrip`.
  - Empty history → text `No dispatch ticks recorded yet.`
  - Header: `Dispatcher` button sets area `dispatcher`.
- [ ] **Step 2:** `npm test` → FAIL. **Step 3:** Implement. **Step 4:** `npm test && npm run build` → PASS; `go test -race ./...` (embedded assets) → PASS.
- [ ] **Step 5:** Commit `feat(web): Dispatcher area with week summary and tick drill-down (#387)`.

### Task 11: Docs and end-to-end check

**Files:**
- Modify: `docs/cockpit.md`, `docs/dispatch.md`, `CHANGELOG.md` (`[Unreleased]` → Added)

- [ ] **Step 1:** `docs/dispatch.md`: section "Tick log" (path, what is logged and not, 30-day retention, `mode`/`trigger` meaning, never read as input); `--json` new fields and the deciding-rung definition (first differing rung vs the next candidate in the full order).
- [ ] **Step 2:** `docs/cockpit.md`: Dispatcher area, gate strip, Ready verdicts (next-window semantics, Ready now in real dispatch order), `gates` on `GET /api/pipeline`, the two new endpoints with example JSON, error source `dispatch`.
- [ ] **Step 3:** Verify: `gofmt -l .` empty, `go vet ./...`, `golangci-lint run`, `go test -race ./...`, `cd web && npm test && npm run build` — all clean. Run `just build`, then `bridge dispatch --dry-run` once and confirm a new line in `~/.cache/bridge/dispatch-ticks.jsonl`.
- [ ] **Step 4:** Commit `docs(dispatch): tick log, ranks and the Dispatcher area (#387)`.
