# Pipeline-Truth NextAction Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make bridge read run outcomes only from the agent-workflow pipeline's run marker, map them onto none / wait / retry / human with one table, recognise the pipeline's ASCII `parked` label, and remove the unused `failed:*` / `attempt:N` model.

**Architecture:** Three focused changes in `internal/dispatch`: a parked helper used by `Eligible` and the cockpit; a new `runrecord.go` that decodes and trusts the marker; a rewritten pure `NextAction(IssueState) Action` in `failure.go`. Docs follow. No caller wiring (#253) and no UI (#368).

**Tech Stack:** Go (module as pinned in `go.mod`), stdlib `testing` with table-driven `t.Run` subtests and hand-rolled fakes (no testify), `encoding/json`, `just test`, `golangci-lint`, `go test -race ./...`.

**Spec:** `docs/superpowers/specs/2026-11-10-pipeline-truth-next-action-design.md`

## Global Constraints

- The pipeline is the only source of truth for attempts and failure class. bridge writes no `failed:*`, `attempt:N` or `🧊 parked` label anywhere after this change.
- Parked = label `parked` **or** `🧊 parked`. `LabelParked = "parked"`, `LabelParkedLegacy = "🧊 parked"`.
- Marker: last non-empty line of a comment, prefix exactly `<!-- ai-run: `, suffix exactly ` -->`, JSON object with `"v":1`. Unknown fields ignored; any other `v` → not a marker.
- Trusted marker author: exactly `github-actions[bot]` (constant `pipelineReportAuthor`). Any other author → ignored.
- `staleRetryAfter = 3 * time.Hour`.
- `NextAction` is pure: no I/O, no label or comment writes. Mapping rows and reason strings exactly as in the spec table (rows 1–20); first match wins.
- Unknown or missing data never yields `ActionRetry`.
- Tests for removed behaviour (`Attempts`, `FailureBucket`, `IsTransient`, the old `NextAction`, the `attempt:2` eligibility case) are deleted with the code — the behaviour is removed by decision (#367), not changed to make a test pass. Every other existing test passes unmodified.
- Required after every task: `gofmt -l .` empty, `go vet ./...`, `golangci-lint run`, `go test -race ./...` green.
- Conventional Commits, scope `dispatch` (Task 1 may use `dispatch` too); reference `#367`.

## Review Focus

1. A failed issue that is re-dispatched still carries its old `ai:failed` label and old marker — with `InFlight: true` it must read `Wait running`, never `Retry`. Test in Task 3 (row 3 beats rows 10–20).
2. A comment by a non-pipeline author containing a perfectly valid marker, posted *after* the real report — must be ignored, and the older pipeline marker returned. Test in Task 2.
3. A marker whose model id contains `-->`, written by the pipeline as `\u003e` / `\u003c` JSON escapes (`"x--\u003ey\u003cz"`) — must decode to the literal string. Test in Task 2.
4. A `decision: retry` marker older than 3 h with no newer report — `Human retry never reported`, at exactly `Posted + 3h` still `Wait` (strictly greater turns Human). Test in Task 3.
5. `transient` / `rate_limit` + `stop` with `Attempt == 0` (cap off) — `Human`, not `Retry`. Test in Task 3.

---

### Task 1: Recognise both parked spellings

**Files:**
- Modify: `internal/dispatch/eligible.go:12-20` (constants), `:107` (Eligible)
- Modify: `internal/cockpit/board.go:149,269`
- Modify: `docs/cockpit.md:27`
- Test: `internal/dispatch/eligible_test.go`, `internal/cockpit/board_test.go`

**Interfaces:**
- Produces: `const LabelParked = "parked"`, `const LabelParkedLegacy = "🧊 parked"`, `func IsParked(labels []string) bool` in package `dispatch`.

- [ ] **Step 1: Write the failing tests**
  - `TestIsParked` (table): `[]` → false; `["parked"]` → true; `["🧊 parked"]` → true; `["feat","parked"]` → true; `["parked-later"]` → false; `["🧊"]` → false.
  - `eligible_test.go` `TestEligible` table: add case `"parked (pipeline ASCII)"` — same issue as the existing `"parked"` case but `Labels: []string{"parked"}` → `false, "parked"`. Keep the existing `🧊 parked` case unchanged.
  - `board_test.go` `TestBuildBoard_ParkedAndRoadmap_Dropped`: add row `{"parked legacy", dispatch.LabelParkedLegacy}`. Add `TestBuildBoard_ParkedIssue_NotAGateCandidate`: an issue labelled only `parked` on a wired GitHub repo with a plan body lands in no column.

- [ ] **Step 2: Run to verify failure**
  Run: `go test ./internal/dispatch ./internal/cockpit -run 'Parked|Eligible' -v`
  Expected: FAIL — `IsParked` / `LabelParkedLegacy` undefined.

- [ ] **Step 3: Implement** — constants and `IsParked` in `eligible.go`; `Eligible` and both `board.go` call sites use `dispatch.IsParked(i.Labels)` / `dispatch.IsParked(labels)` instead of matching `LabelParked` alone. `docs/cockpit.md:27` → "`parked` (legacy `🧊 parked`) or `roadmap`: ignored."

- [ ] **Step 4: Run to verify pass** — `just test` (or `go test -race ./...`), `golangci-lint run`. Expected: PASS.

- [ ] **Step 5: Commit**
```bash
git add internal/dispatch/eligible.go internal/dispatch/eligible_test.go internal/cockpit/board.go internal/cockpit/board_test.go docs/cockpit.md
git commit -m "fix(dispatch): treat the pipeline's ASCII parked label as parked (#367)"
```

### Task 2: Decode and trust the run marker

**Files:**
- Create: `internal/dispatch/runrecord.go`, `internal/dispatch/runrecord_test.go`

**Interfaces:**
- Consumes: `forge.Comment{ID, Author, Body, Created}` (`internal/forge/client.go:133`).
- Produces: `type RunRecord struct` with exactly the spec's fields (`Attempt, RetryAttempt, Turns, PR int`; `Counts bool`; `Class, Decision, Status, Subtype, Agent, Model, RunURL string`; `CostUSD float64`; `Posted time.Time`); `func ParseRunMarker(body string) (RunRecord, bool)`; `func LatestRun(comments []forge.Comment) (RunRecord, bool)`; `const pipelineReportAuthor = "github-actions[bot]"`.

JSON keys map as in the agent-workflow marker: `attempt, counts, retry_attempt, class, decision, status, subtype, agent, model, turns, cost_usd, pr, run_url` (`v` read for the version check only; `max_turns, duration_ms, plan_coverage` may be ignored).

- [ ] **Step 1: Write the failing tests** (table-driven; marker fixture strings built with a small helper `marker(json string) string` returning `"## ai-implement run\n\n…\n[View workflow run](u)\n<!-- ai-run: " + json + " -->"`)

  `TestParseRunMarker`:
  - full v1 object → ok; `Attempt==2, Counts==true, RetryAttempt==1, Class=="task_failure", Decision=="stop", Status=="ai:failed", Turns==60, CostUSD==2.341, PR==42, RunURL=="https://x/runs/1"`.
  - trailing blank lines after the marker → ok.
  - marker not on the last non-empty line (text after it) → not ok.
  - `"v":2` → not ok; missing `v` → not ok.
  - malformed JSON → not ok; prefix `<!--ai-run:` (no spaces) → not ok.
  - unknown field `"future":1` → ok.
  - model `"x--\u003ey\u003cz"` (JSON escapes, as the pipeline writes them) → ok, `Model == "x-->y<z"`.
  - absent optional fields (paused: no `attempt`, no `status`, `counts:false`) → ok, zero values.

  `TestLatestRun`:
  - no comments → not ok.
  - one pipeline comment → ok, `Posted == comment.Created`.
  - two pipeline comments → the one with the later `Created`; equal `Created` → the later slice element.
  - newer comment by `"someone"` with a valid marker + older pipeline comment → returns the pipeline one.
  - newest pipeline comment without a marker (e.g. `## ai-implement parked`) + older pipeline comment with marker → returns the older marker.

- [ ] **Step 2: Run to verify failure** — `go test ./internal/dispatch -run 'RunMarker|LatestRun' -v` → FAIL (undefined).

- [ ] **Step 3: Implement** in `runrecord.go`. `ParseRunMarker`: trim trailing whitespace, take the substring after the last `\n`, check prefix/suffix, `json.Unmarshal` into an unexported wire struct with `V *int`; reject unless `V != nil && *V == 1`. `LatestRun`: filter by `pipelineReportAuthor`, keep the best `(Created, index)` that parses, set `Posted`. Doc comment on the constant cites agent-workflow ADR-021's consumer trust rule.

- [ ] **Step 4: Run to verify pass** — `go test -race ./...`, `golangci-lint run`.

- [ ] **Step 5: Commit**
```bash
git add internal/dispatch/runrecord.go internal/dispatch/runrecord_test.go
git commit -m "feat(dispatch): decode the pipeline's run marker from trusted comments (#367)"
```

### Task 3: NextAction on pipeline state; remove the label model

**Files:**
- Modify: `internal/dispatch/failure.go` (rewrite)
- Modify: `internal/dispatch/eligible.go` — remove `attemptPrefix`, `failedPrefix`, `Attempts`, and the `Attempts(i.Labels) >= 2` rule (`:113-115`)
- Modify: `internal/dispatch/failure_test.go` (rewrite), `internal/dispatch/eligible_test.go` (delete `TestAttempts` and the `"attempt budget spent"` case)

**Interfaces:**
- Consumes: `RunRecord` (Task 2), `IsParked` (Task 1).
- Produces: `type ActionKind int` with `ActionNone, ActionWait, ActionRetry, ActionHuman`; `type Action struct{ Kind ActionKind; Reason string }`; `type IssueState struct{ Labels []string; Run RunRecord; HasRun bool; InFlight bool; Now time.Time }`; `func NextAction(s IssueState) Action`; `const staleRetryAfter = 3 * time.Hour`. Removed: `FailureBucket`, `IsTransient`, `transientBuckets`, `maxAttempts`, old `Action` fields.

- [ ] **Step 1: Write the failing test** — `TestNextAction` table with one case per spec row (1–20), named `"row N: <state>"`, each asserting `Kind` **and** `Reason` exactly as the spec table. Use a fixed `now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)`. Required cases beyond one-per-row:
  - row 1 for both `parked` and `🧊 parked`; row 1 beats `ai:paused` and `InFlight`.
  - row 3: labels `ai-implement, ai:failed`, `HasRun` with `Class:"transient", Decision:"stop", Attempt:1`, `InFlight: true` → `Wait "running"`.
  - row 10 both branches: `ai:done` → None `""`; `ai:failed` → Human `"no run marker"`.
  - row 14 vs 15: `Posted = now.Add(-3*time.Hour)` → Wait `"pipeline retrying"`; `Posted = now.Add(-3*time.Hour - time.Second)` → Human `"retry never reported"`; for each of `transient`, `rate_limit`, `task_failure`.
  - row 16: `rate_limit`, `stop`, `Attempt: 2` → Retry `"rate_limit, retries exhausted"`; row 17: same with `Attempt: 0` → Human `"rate_limit, no attempt cap"`.
  - row 19 for each of `api_auth`, `model_not_found`, `bug`; row 20: `Class: "weird"` → Human `"unknown: weird"`; `Class: "transient", Decision: ""` → Human `"unknown: transient"`.
  - `TestEligible`: new case labels `["attempt:5"]` with a plan body → eligible (`true, ""`) — the label no longer means anything.

- [ ] **Step 2: Run to verify failure** — `go test ./internal/dispatch -run 'NextAction|Eligible' -v` → FAIL (compile errors on the new types).

- [ ] **Step 3: Implement** `NextAction` as an ordered sequence of guard-clause checks mirroring the table (one small helper per group if it keeps functions ≤ 20 lines, e.g. `labelAction`, `runAction`). Delete the removed identifiers and their tests.

- [ ] **Step 4: Run to verify pass** — `gofmt -l .`, `go vet ./...`, `golangci-lint run`, `go test -race ./...`; `grep -rn 'attempt:\|failed:\|FailureBucket\|IsTransient' --include=*.go .` returns nothing.

- [ ] **Step 5: Commit**
```bash
git add internal/dispatch/failure.go internal/dispatch/failure_test.go internal/dispatch/eligible.go internal/dispatch/eligible_test.go
git commit -m "refactor(dispatch): derive NextAction from the pipeline's run marker (#367)"
```

### Task 4: Document the one model

**Files:**
- Modify: `docs/dispatch.md` — "Eligibility" item 4 (`:70`), "Labels" table (`:156-172`), "When a run fails" (`:174-196`)
- Modify: `CHANGELOG.md` — `## [Unreleased]`

- [ ] **Step 1: `docs/dispatch.md`**
  - Eligibility item 4: not labelled `parked` (legacy `🧊 parked` is still read).
  - Labels table: drop the `attempt:*` and six `failed:*` rows; `parked` row "Set by: agent-workflow (`check-attempt-cap.sh`) at its attempt cap"; add rows for `ai:done` / `ai:failed` / `ai:partial`, `ai:paused`, `ai:review-blocked`, `ai:review-failed`, `ai:runner-blocked`, `ai:checks-blocked`, all "Set by: agent-workflow pipeline".
  - "When a run fails": replace both subsections with: the pipeline is the source of truth; the run marker (agent-workflow ADR-021) and the trusted-author rule; the spec's mapping table (rows 1–20, Kind and Reason); the `InFlight` and 3 h stale notes; "Status today: `NextAction` is computed but not yet called by a tick — #253 wires `--retry-only`."
- [ ] **Step 2: CHANGELOG** — `### Changed`: NextAction follows the pipeline's classes and attempt count (#367). `### Removed`: `failed:*` / `attempt:N` labels. `### Fixed`: pipeline-parked (`parked`) issues are no longer re-dispatched.
- [ ] **Step 3: Verify** — `grep -n 'failed:\|attempt:' docs/dispatch.md` → only historical mentions inside the "When a run fails" prose, none in the label table; `just test` green.
- [ ] **Step 4: Commit**
```bash
git add docs/dispatch.md CHANGELOG.md
git commit -m "docs(dispatch): document the pipeline-truth failure mapping (#367)"
```

## Operator step after merge (not for the implementing agent)

Relabel the three open issues carrying `🧊 parked` (freaxnx01/bridge#87, freaxnx01/bridge#197, freaxnx01/game-tschau-sepp#36) to `parked`, then delete the `🧊 parked` label definition in each repo that has it. Code reads both spellings, so the order is free.
