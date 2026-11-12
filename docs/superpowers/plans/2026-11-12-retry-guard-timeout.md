# Retry Guard Timeout and Open-PR Gate Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Move the retry-pending guard into `dispatch.NextAction` with a 3 h timeout to Human, add an open-PR → Human rule, and make the cockpit inbox and the dispatcher build their state through one constructor so they always agree.

**Architecture:** `IssueState` gains `Redispatched time.Time` and `HasOpenPR bool`; `NextAction` turns a would-be `ActionRetry` into Human/Wait per new rows 15a–15c. A new `dispatch.StateFor` builds `IssueState` from issue, comments, runs and PRs; both `cmd/bridge/dispatch_retry.go` and `internal/cockpit/inbox.go` use it. `RetryPending` and `EvaluateRetry` are removed.

**Tech Stack:** Go (stdlib `testing`, hand-rolled fakes, table-driven tests).

**Spec:** [`docs/superpowers/specs/2026-11-12-retry-guard-timeout-design.md`](../specs/2026-11-12-retry-guard-timeout-design.md)

## Global Constraints

- Timeout is the existing `staleRetryAfter` (3 h) in `internal/dispatch/failure.go:23` — no new constant, no config key.
- Reasons, exact strings: `re-dispatch never reported`, `retry pending`, `open PR`.
- Rule order on a would-be Retry: 15a (stale re-dispatch) → 15b (pending) → 15c (open PR) → Retry.
- "Newer than the marker" is strictly after: `Redispatched.After(Run.Posted)`. "Older than 3 h" is `Now.Sub(Redispatched) > staleRetryAfter`.
- Do not renumber existing rows in `docs/dispatch.md` or code comments; new rows are 15a, 15b, 15c.
- No new modules; stdlib + hand-rolled fakes only; no `testify`.
- Do not change the swap, the booking rule, caps, ordering or the comment text.
- After every task: `gofmt -l .` empty, `go vet ./...` clean, `go test -race ./...` green (full suite).

## Review Focus

- **Re-dispatch comment exactly at the marker's timestamp** — must not count as pending (strictly-after). Pinned in Task 1.
- **Re-dispatch comment exactly 3 h old** — still Wait; only strictly more than 3 h is Human. Pinned in Task 1.
- **Pending re-dispatch plus an open PR** — Wait `retry pending` wins (the re-dispatched run may be what opened the PR). Pinned in Task 1.
- **Open PR that does not close this issue** (mentions another number) — must not gate. Pinned in Task 1 via `HasOpenPR` in `StateFor`.
- **Inbox and dispatcher disagreeing** — the same fixture must give the same kind/reason in both. Pinned in Tasks 2 and 3.

---

### Task 1: Guard rows 15a–15c in `NextAction`, `LastRedispatch`, `StateFor`

**Files:**
- Modify: `internal/dispatch/failure.go` (IssueState, NextAction)
- Modify: `internal/dispatch/retry.go` (replace `RetryPending` and `EvaluateRetry`)
- Test: `internal/dispatch/failure_test.go`, `internal/dispatch/retry_test.go`

**Interfaces:**
- Produces:
  - `IssueState.Redispatched time.Time`, `IssueState.HasOpenPR bool`
  - `func LastRedispatch(comments []forge.Comment) time.Time`
  - `func StateFor(i forge.Issue, comments []forge.Comment, runs []forge.WorkflowRun, prs []forge.PullRequest, now time.Time) IssueState`
- Keeps `RetryPending` and `EvaluateRetry` for now (their only caller moves in Task 2, which deletes them), so every commit builds.

- [ ] **Step 1: Write the failing tests**

In `failure_test.go` add `TestNextAction_RetryGuard` (table, same `now` and `failure(...)` helper style as `TestNextAction`). Base state: labels `["ai-implement","ai:failed"]`, `run := failure("rate_limit","stop",1)` (Posted = now−10 min), `HasRun: true`. Cases (`Redispatched`, `HasOpenPR` → want):

```go
{"row 16 unguarded", time.Time{}, false, Action{ActionRetry, "rate_limit, retries exhausted"}},
{"row 15b: re-dispatch newer than marker", now.Add(-5 * time.Minute), false, Action{ActionWait, "retry pending"}},
{"row 15b: exactly 3h old still waits", now.Add(-staleRetryAfter), false, Action{ActionWait, "retry pending"}},
{"row 15a: older than 3h", now.Add(-staleRetryAfter - time.Second), false, Action{ActionHuman, "re-dispatch never reported"}},
{"re-dispatch at the marker's instant is not pending", run.Posted, false, Action{ActionRetry, "rate_limit, retries exhausted"}},
{"re-dispatch older than marker belongs to the reported run", run.Posted.Add(-time.Minute), false, Action{ActionRetry, "rate_limit, retries exhausted"}},
{"row 15c: open PR", time.Time{}, true, Action{ActionHuman, "open PR"}},
{"pending beats open PR", now.Add(-5 * time.Minute), true, Action{ActionWait, "retry pending"}},
{"stale beats open PR", now.Add(-staleRetryAfter - time.Second), true, Action{ActionHuman, "re-dispatch never reported"}},
```

Plus one case proving the guard only touches Retry: `task_failure`/`stop` with `HasOpenPR: true` and a fresh `Redispatched` → `Action{ActionHuman, "task_failure"}`.

In `retry_test.go`:
- Add `TestLastRedispatch` (table): no comments → zero; one prefixed comment at `retryT0+1m` → that time; two prefixed comments → the newer one; any author counts; `"Dispatched by \`bridge dispatch\`."` → zero; `"note: "+prefix` → zero.
- Add `TestStateFor` asserting each field: `Run`/`HasRun` from `LatestRun`, `InFlight` from a matching in-progress run, `Redispatched` from the comments, `HasOpenPR` true for a PR body `"Closes #43"` and false for `"Closes #44"`, `Now` passed through. Also carry every `TestEvaluateRetry` row over as a `NextAction(StateFor(...))` case in the same test (rate-limit → Retry, in flight → Wait running, newer re-dispatch → Wait retry pending, older one → Retry, task_failure → Human, no marker → Human `no run marker`).

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/dispatch/ -run 'TestNextAction_RetryGuard|TestLastRedispatch|TestStateFor|TestRedispatchComment_ExactText'`
Expected: FAIL — compile errors for `Redispatched`, `HasOpenPR`, `LastRedispatch`, `StateFor`.

- [ ] **Step 3: Implement**

- `failure.go`: add the two `IssueState` fields with doc comments. In `NextAction`, after `runAction`, pass a Retry through `guardRetry(s IssueState, a Action) Action` (≤20 lines, rule order per Global Constraints). Update the `IssueState` and `NextAction` doc comments to mention rows 15a–15c.
- `retry.go`: `LastRedispatch` (newest prefixed comment's `Created`), `StateFor` (calls `LatestRun`, `InFlight`, `LastRedispatch`, `HasOpenPR`).

- [ ] **Step 4: Run the package tests**

Run: `go test -race ./internal/dispatch/`
Expected: PASS. Then `go build ./... && go test -race ./...` — the full suite must stay green.

- [ ] **Step 5: Commit**

```bash
gofmt -l . ; go vet ./internal/dispatch/
git add internal/dispatch/
git commit -m "feat(dispatch): time out the retry-pending guard and gate retries on an open PR (#390)"
```

### Task 2: Dispatcher uses `StateFor`; open-PR and step-1/2 booking tests

**Files:**
- Modify: `cmd/bridge/dispatch_retry.go:79` (`retriesForRepo`)
- Modify: `internal/dispatch/retry.go` (delete `RetryPending`, `EvaluateRetry`)
- Test: `cmd/bridge/dispatch_retry_test.go`, `internal/dispatch/retry_test.go`

**Interfaces:**
- Consumes: `dispatch.StateFor`, `dispatch.NextAction` (Task 1).

- [ ] **Step 1: Write the failing tests**

- Add a `prs string` field to `retryFixture` (JSON array for `GET …/pulls`, default `[]`) and serve it from the existing `/pulls` case.
- `TestRunDispatch_RetryWithOpenPR_NoWrites`: fixture with the rate-limit/stop marker on #43 and `prs` = `[{"number":7,"title":"p","state":"open","body":"Closes #43","head":{"ref":"x","sha":"abc"},"base":{"ref":"main"},"html_url":"p7","created_at":"2026-07-01T00:00:00Z"}]` (fields of `ghPull`, `internal/forge/github.go:1063`). Run one non-dry tick; assert no non-GET request for issue 43 and the JSON/text output carries skip reason `open PR`.
- `TestRunDispatch_RetryStaleRedispatch_NoWritesHumanReason`: marker at T, re-dispatch comment at T+1m where the tick's `now` is more than 3 h after T+1m (use timestamps far in the past, as the existing pending test does); assert no writes and reason `re-dispatch never reported`.
- `TestRunDispatch_RetrySwapFailsEarly_BooksNothing` (table over step 1 = `"POST /repos/freaxnx01/bridge/issues/43/labels"`, step 2 = `"DELETE /repos/freaxnx01/bridge/issues/43/labels/ai-implement"`): same shape as `TestRunDispatch_RetryStep4Fails_BooksAndErrors` — error contains `"step 1"` / `"step 2"`, no `/comments` write, and `usage.LoadLedger(dispatchLedgerPath())` has `len(ledger.Runs) == 0`.
- In `TestCollectRetries_SkipReasons_Table` add rows for `open PR` and `re-dispatch never reported` if that table builds `repoInput` directly (pass `PRs` on the `repoInput`).

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./cmd/bridge/ -run 'TestRunDispatch_Retry|TestCollectRetries'`
Expected: FAIL (compile error on `EvaluateRetry`, or the new reasons not produced).

- [ ] **Step 3: Implement**

In `retriesForRepo` replace `dispatch.EvaluateRetry(i, comments, runs, now)` with `dispatch.NextAction(dispatch.StateFor(i, comments, runs, r.PRs, now))`. Then delete `RetryPending` and `EvaluateRetry` from `internal/dispatch/retry.go`, delete `TestRetryPending` and `TestEvaluateRetry` (their cases live on in `TestLastRedispatch` / `TestStateFor` from Task 1), and change `TestRedispatchComment_ExactText`'s guard assertion to `LastRedispatch(posted).Equal(retryT0.Add(time.Minute))`. `grep -rn "RetryPending\|EvaluateRetry" --include=*.go .` must print nothing.

- [ ] **Step 4: Run the full suite**

Run: `go test -race ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/bridge/ internal/dispatch/
git commit -m "feat(dispatch): retry pass reads open PRs and the guard via StateFor (#390)"
```

### Task 3: Cockpit inbox uses `StateFor`

**Files:**
- Modify: `internal/cockpit/inbox.go:138` (`classify`)
- Test: `internal/cockpit/inbox_test.go`

**Interfaces:**
- Consumes: `dispatch.StateFor`, `dispatch.NextAction`.

- [ ] **Step 1: Write the failing test**

Add rows to `TestBuildInbox_MappingRows_Table` (it needs PRs: extend the table with a `prs []forge.PullRequest` column, set `rd.PRs = tc.prs`):

```go
{"re-dispatch pending", labels ai-implement+ai:failed,
  comments: marker(retryMarker, inboxNow.Add(-2*time.Hour)) + redispatch comment at inboxNow.Add(-90*time.Minute),
  wantBucket "waiting", CardWait, "retry pending"},
{"re-dispatch never reported", same marker at inboxNow.Add(-5*time.Hour), redispatch at inboxNow.Add(-4*time.Hour),
  "cards", CardHuman, "re-dispatch never reported"},
{"open PR", marker at -2h, prs: [{Body: "Closes #1"}], "cards", CardHuman, "open PR"},
```

The redispatch comment is `forge.Comment{Author: "github-actions[bot]", Body: dispatch.RedispatchComment("x"), Created: …}`.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/cockpit/ -run TestBuildInbox_MappingRows_Table`
Expected: FAIL — the new rows come back as `retry` cards.

- [ ] **Step 3: Implement**

In `classify` replace the `LatestRun` + `NextAction(IssueState{…})` block with `s := dispatch.StateFor(i, comments, rd.Runs, rd.PRs, now)`, `action := dispatch.NextAction(s)`, and use `s.Run` / `s.HasRun` where `run` / `hasRun` were used.

- [ ] **Step 4: Run the full suite**

Run: `gofmt -l . ; go vet ./... ; go test -race ./...`
Expected: empty, clean, PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/cockpit/
git commit -m "feat(cockpit): inbox maps stale and open-PR retries like the dispatcher (#390)"
```

### Task 4: Docs and changelog

**Files:**
- Modify: `docs/dispatch.md` ("The mapping" table and notes, "Retry pass" → "Retry pending" paragraph, "Skip reasons" list)
- Modify: `CHANGELOG.md` (`[Unreleased]`)

- [ ] **Step 1: Update `docs/dispatch.md`**

- Insert rows 15a, 15b, 15c between rows 15 and 16 exactly as in the spec's table.
- Add a note under the table: the three rows only apply to a would-be row 16, and pending is checked before open PR because a pending re-dispatch may be what opened the PR.
- Rewrite the **Retry pending** paragraph: the guard is part of `NextAction` (rows 15a–15b), so the cockpit inbox shows the same state; after 3 h (`staleRetryAfter`) without a newer report the issue goes to a person as `re-dispatch never reported` — no automatic second re-dispatch.
- Add `open PR` and `re-dispatch never reported` to the **Skip reasons** list.

- [ ] **Step 2: Update `CHANGELOG.md`**

Under `[Unreleased]` → `Fixed`: "Retry pass: a re-dispatch with no run report for 3 h now goes to a person (`re-dispatch never reported`) instead of being skipped as `retry pending` forever; a failed issue with an open PR closing it is no longer retried (`open PR`). The cockpit inbox shows both the same way (#390)."

- [ ] **Step 3: Verify**

Run: `grep -n "15a\|15b\|15c\|re-dispatch never reported\|open PR" docs/dispatch.md`
Expected: the three rows, the note, and both skip reasons present.

- [ ] **Step 4: Commit**

```bash
git add docs/dispatch.md CHANGELOG.md
git commit -m "docs(dispatch): document the retry guard timeout and open-PR rows (#390)"
```
