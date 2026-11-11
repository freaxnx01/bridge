# Retry Pass for `bridge dispatch` Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Every `bridge dispatch` tick (`--auto`, `now`, plain) first re-dispatches GitHub issues whose last pipeline run stopped with `ActionRetry`, via a guarded label swap, sharing `Order` and the caps with new dispatches. `--retry-only` limits a run to that pass.

**Architecture:** Package `dispatch` gains the pure pieces: the in-flight rule moved from the cockpit, the retry-candidate predicate, the retry-pending guard, a classifier `EvaluateRetry`, and `Redispatch`, the 4-step label swap over a small `labelSwapper` interface. `cmd/bridge` gains a retry collector (`collectRetries`) over a consumer interface. It also gains `Candidate.Retry`/`RetryReason`, so the existing `Order` → `partitionForTick` → `ApplyCaps` → render → `applyDecisions` flow carries retries unchanged: ordered retries are concatenated *before* ordered new candidates. `applyDecisions` branches on `Candidate.Retry`.

**Tech Stack:** Go (as pinned in `go.mod`), Cobra, stdlib `testing` (table-driven `t.Run`, hand-rolled fakes), `net/http/httptest`.

**Spec:** `docs/superpowers/specs/2026-11-11-retry-only-dispatch-design.md`

## Global Constraints

- Go: stdlib `testing` only, table-driven, hand-rolled fakes / `httptest`. **No** testify/mockery/gomock. **No new Go modules.** Test names `TestFunc_State_Expected`.
- GitHub only. Non-GitHub repos never produce a retry candidate and get no extra API call.
- **No change** to `NextAction`, `ApplyCaps`, `Order`, the pipeline, `agent.yml`, or the systemd timer/service units.
- Retry candidate: labels contain `ai-implement` **and** `ai:failed`, and none of `parked`, `🧊 parked` (`IsParked`), `ai:retrying`, `ai:paused`. Only `ActionRetry` is re-dispatched.
- `LabelRetrying = "ai:retrying"` (in `internal/dispatch/eligible.go`).
- Swap order, exact: (1) `AddLabels([ai:retrying])` → (2) `RemoveLabel(ai-implement)` → (3) `AddLabels(lane.EffectiveLabels())` → (4) `RemoveLabel(ai:retrying)`. A failure stops the sequence. `ai:failed` is never touched.
- Comment after a successful swap, exact: ``Re-dispatched by `bridge dispatch` (retry: <reason>).``. The prefix ``Re-dispatched by `bridge dispatch` `` (without the trailing space) is one exported const shared by the comment and the guard.
- Guard: a comment of **any** author whose body starts with the prefix and whose `Created` is strictly after `Run.Posted` turns `ActionRetry` into skip reason `retry pending`.
- In flight = a run with `Event == "issues"`, `Status` `queued` or `in_progress`, and `DisplayTitle == issue.Title`. The rule is moved from the cockpit to `dispatch`, behaviour unchanged.
- Retries come first in a tick and count against the same budget/lane/nightly/global/per-repo caps. Each retry is booked in the spend ledger exactly like a dispatch.
- `--retry-only` is the fourth **persistent** flag on `bridge dispatch`, combinable with `--dry-run`, `--json`, `--auto`. The pause and window gates are unchanged.
- TDD: failing test first. Never change an existing test to go green. **No existing test changes in this plan.** The one behaviour change to existing code (`Eligible` returns `retry interrupted` for `ai:retrying`) has no existing test covering it. The cockpit tests stay green **unmodified** after the move.
- Gates after every task, all green before commit: `gofmt -l .` empty, `go vet ./...`, `golangci-lint run`, `go test -race ./...`. While iterating use `just test-fast` (skips `cmd/bridge`, which takes ~150 s) or `go test ./cmd/bridge -run '<pattern>'`. Run the full suite once per task before committing. `just test` also needs `bats`, so the Go gates above are the ones that count.
- Conventional Commits, reference `#253`; scopes `dispatch`, `cockpit`, `docs`.

## Review Focus

1. **The runs fetch fails** for a repo with retry candidates. Not knowing whether a run is in flight must never read as "not in flight": every candidate of that repo is skipped with `runs unreadable`. Test in Task 3.
2. **A swap interrupted after step 2** leaves `ai:retrying` on the issue and `ai-implement` gone, with the plan heading still in the body. The next tick must not dispatch it as a *new* issue (`Eligible` → `retry interrupted`), and the retry pass lists it as `retry interrupted`. Tests in Task 1.
3. **Step 4 fails** (removing `ai:retrying`). The pipeline already fired at step 3, so the run is booked in the ledger and the error is surfaced with the step named. Test in Task 4.
4. **A retry in a `dry_run` lane** makes zero forge writes, but the lane counter still advances and nothing is booked, exactly like a dry-run dispatch. Test in Task 4.
5. **A `GetIssue` failure on one candidate** skips only that candidate (`issue unreadable`). The other retry candidates and the new-dispatch pass still run. Test in Task 3.

---

### Task 1: In-flight rule, `ai:retrying`, retry-candidate predicate

**Files:**
- Create: `internal/dispatch/inflight.go`, `internal/dispatch/inflight_test.go`, `internal/dispatch/retry.go`, `internal/dispatch/retry_test.go`
- Modify: `internal/dispatch/eligible.go` (const block `:12-19`, `Eligible` before the `ai-implement` check at `:99`), `internal/dispatch/eligible_test.go` (new test function only)
- Modify: `internal/cockpit/inbox.go:139,207-217` (delete `inFlight`/`isIssueRunFor`, call `dispatch.InFlight`), `internal/cockpit/collector.go:406` (`dispatch.IsIssueRunFor`)

**Interfaces:**
- Produces (package `dispatch`):
  - `func IsIssueRunFor(i forge.Issue, r forge.WorkflowRun) bool` and `func InFlight(i forge.Issue, runs []forge.WorkflowRun) bool`. Body and doc comment are moved verbatim from `cockpit`.
  - `const LabelRetrying = "ai:retrying"` in the `eligible.go` label block.
  - `func RetryCandidate(labels []string) (bool, string)`. Checks run in order:
    1. `ai:retrying` → `(false, "retry interrupted")`, whatever the other labels.
    2. Not both `ai-implement` and `ai:failed` → `(false, "")`. Not a retry issue, so it is silently dropped.
    3. `IsParked` → `(false, "parked")`.
    4. `ai:paused` → `(false, "paused")`.
    5. Otherwise → `(true, "")`.

    A non-empty reason means "list it as skipped". An empty one means "not part of the retry pass".
- `Eligible`: new rule `hasLabel(i.Labels, LabelRetrying)` → `false, "retry interrupted"`, placed **after** `IsParked` and **before** the `ai-implement` check.

- [ ] **Step 1: Write the failing tests**
  - `TestIsIssueRunFor` (table, issue title `"T"`):
    - `{Event:"issues", Status:"in_progress", DisplayTitle:"T"}` → true.
    - `Status:"queued"` → true.
    - `Status:"completed"` → false.
    - `Event:"workflow_dispatch"` → false.
    - `DisplayTitle:"other"` → false.
  - `TestInFlight`:
    - nil runs → false.
    - `[completed "T", in_progress "other", in_progress "T"]` → true.
    - the first two only → false.
  - `TestRetryCandidate` (table → `ok, reason`):
    - `[ai-implement ai:failed]` → `true, ""`.
    - `[ai-implement ai:failed feat]` → `true, ""`.
    - `[ai-implement]` → `false, ""`.
    - `[ai:failed]` → `false, ""`.
    - `[]` → `false, ""`.
    - `[ai-implement ai:failed parked]` → `false, "parked"`.
    - `[ai-implement ai:failed 🧊 parked]` → `false, "parked"`.
    - `[ai-implement ai:failed ai:paused]` → `false, "paused"`.
    - `[ai-implement ai:failed ai:retrying]` → `false, "retry interrupted"`.
    - `[ai:retrying]` → `false, "retry interrupted"`.
    - `[ai:retrying parked]` → `false, "retry interrupted"`.
  - `TestEligible_RetryingLabel_RetryInterrupted` (new function; `TestEligible` stays untouched). Body `"x\n## Implementation Plan\n- a"`:
    - `[feat ai:retrying]` → `false, "retry interrupted"`. This is the swap interrupted after step 2.
    - `[ai-implement ai:retrying]` → `false, "retry interrupted"`, beating `already dispatched`.
    - `[parked ai:retrying]` → `false, "parked"`.
- [ ] **Step 2: Run to verify failure** — `go test ./internal/dispatch -run 'IsIssueRunFor|InFlight|RetryCandidate|Eligible_Retrying' -v` → FAIL (undefined).
- [ ] **Step 3: Implement** the move and the rules above. In `cockpit`, delete both unexported functions and call the `dispatch` ones. `slices` stays imported in `inbox.go` because other code uses it. Run `goimports`.
- [ ] **Step 4: Run the gates.** `go test -race ./internal/dispatch ./internal/cockpit` must pass with `internal/cockpit/*_test.go` unmodified (`git diff --stat` shows no cockpit test file). Then the full Go gates.
- [ ] **Step 5: Commit**
```bash
git add internal/dispatch/inflight.go internal/dispatch/inflight_test.go internal/dispatch/retry.go internal/dispatch/retry_test.go internal/dispatch/eligible.go internal/dispatch/eligible_test.go internal/cockpit/inbox.go internal/cockpit/collector.go
git commit -m "feat(dispatch): add retry-candidate predicate and shared in-flight rule (#253)"
```

### Task 2: Retry-pending guard, `EvaluateRetry`, `Redispatch`

**Files:**
- Modify: `internal/dispatch/retry.go`, `internal/dispatch/retry_test.go`
- Create: `internal/dispatch/redispatch.go`, `internal/dispatch/redispatch_test.go`

**Interfaces:**
- Consumes: `InFlight`, `LabelRetrying` (Task 1); `LatestRun`, `NextAction`, `IssueState`, `Action*`.
- Produces (package `dispatch`):
  - ``const RedispatchCommentPrefix = "Re-dispatched by `bridge dispatch`"``
  - `func RedispatchComment(reason string) string` returns `RedispatchCommentPrefix + " (retry: " + reason + ")."`.
  - `func RetryPending(comments []forge.Comment, since time.Time) bool` is true when some comment, of any author, has `strings.HasPrefix(c.Body, RedispatchCommentPrefix)` and `c.Created.After(since)`.
  - `func EvaluateRetry(i forge.Issue, comments []forge.Comment, runs []forge.WorkflowRun, now time.Time) Action` builds `NextAction(IssueState{Labels: i.Labels, Run, HasRun (from LatestRun(comments)), InFlight: InFlight(i, runs), Now: now})`. If that result is `ActionRetry` and `RetryPending(comments, run.Posted)`, it returns `Action{ActionWait, "retry pending"}` instead. Otherwise it returns the result unchanged.
  - The swap surface and the typed step error:
    ```go
    type labelSwapper interface {
        AddLabels(ctx context.Context, owner, repo string, number int, labels []string) ([]string, error)
        RemoveLabel(ctx context.Context, owner, repo string, number int, label string) error
    }
    type RedispatchError struct { Step int; Op string; Err error } // Step 1..4
    func (e *RedispatchError) Error() string // fmt "redispatch step %d (%s): %v"
    func (e *RedispatchError) Unwrap() error
    func Redispatch(ctx context.Context, gh labelSwapper, owner, repo string, number int, labels []string) error
    ```
  - `Op` values by step: `"add ai:retrying"`, `"remove ai-implement"`, `"add labels"`, `"remove ai:retrying"`. Any failure returns `*RedispatchError`. The doc comment on `Redispatch` states that the pipeline starts only on `issues: labeled`, so the label must be removed and re-added.

- [ ] **Step 1: Write the failing tests.** Comments come from a helper `botMarker(json string, at time.Time) forge.Comment` (author `github-actions[bot]`, body `"report\n<!-- ai-run: " + json + " -->"`). `t0 := time.Date(2026, 11, 10, 10, 0, 0, 0, time.UTC)`, `now := t0.Add(time.Hour)`.
  - `TestRedispatchComment_ExactText`: `RedispatchComment("rate_limit, retries exhausted")` == ``"Re-dispatched by `bridge dispatch` (retry: rate_limit, retries exhausted)."``, and `RetryPending` sees it when it is posted after `since`.
  - `TestRetryPending` (table, `since = t0`):
    - no comments → false.
    - prefix comment at `t0+1m` → true.
    - prefix comment at exactly `t0` → false.
    - prefix comment at `t0-1m` → false.
    - prefix comment by `"someone"` at `t0+1m` → true.
    - ``"Dispatched by `bridge dispatch`."`` at `t0+1m` → false.
    - ``"note: Re-dispatched by `bridge dispatch` (retry: x)."`` → false, because the prefix must start the body.
  - `TestEvaluateRetry` (table; issue `Title "T"`, labels `[ai-implement ai:failed]`; marker at `t0`):
    - `{"v":1,"class":"rate_limit","decision":"stop","attempt":1,"status":"ai:failed"}` → `{ActionRetry, "rate_limit, retries exhausted"}`.
    - `transient`/`stop`/`attempt:1` → `{ActionRetry, "transient, retries exhausted"}`.
    - the `rate_limit` marker plus run `{issues, in_progress, "T"}` → `{ActionWait, "running"}`.
    - the `rate_limit` marker plus `RedispatchComment("x")` at `t0+1m` → `{ActionWait, "retry pending"}`.
    - the same comment at `t0-1m` → `ActionRetry`.
    - `task_failure`/`stop`/`attempt:1` → `{ActionHuman, "task_failure"}`.
    - no marker → `{ActionHuman, "no run marker"}`.
  - `TestRedispatch_CallOrder`: a hand-rolled `fakeSwapper` records `"add [ai:retrying]"`, `"remove ai-implement"`, … and asserts that owner, repo and number pass through. With `labels = [ai-implement ai-review-ai-merge]`, the recorded calls equal exactly `["add [ai:retrying]", "remove ai-implement", "add [ai-implement ai-review-ai-merge]", "remove ai:retrying"]` and `err == nil`.
  - `TestRedispatch_StopsAtFailingStep` (table, `failAt` 1..4; the fake returns `boom` on call number `failAt`):
    - `len(calls) == failAt`.
    - `errors.As(err, &re)` holds, with `re.Step == failAt`.
    - `errors.Is(err, boom)` holds.
    - `strings.Contains(err.Error(), fmt.Sprintf("step %d", failAt))` holds.
- [ ] **Step 2: Run to verify failure** — `go test ./internal/dispatch -run 'Redispatch|RetryPending|EvaluateRetry' -v` → FAIL (undefined).
- [ ] **Step 3: Implement** as specified. `Redispatch` stays at most 20 lines: loop over a `[]struct{op string; call func() error}` with 4 entries.
- [ ] **Step 4: Run the Go gates.** Expected: green.
- [ ] **Step 5: Commit**
```bash
git add internal/dispatch/retry.go internal/dispatch/retry_test.go internal/dispatch/redispatch.go internal/dispatch/redispatch_test.go
git commit -m "feat(dispatch): add retry-pending guard and guarded label-swap Redispatch (#253)"
```

### Task 3: Retry collector and decision kind (no wiring yet)

**Files:**
- Modify: `internal/dispatch/order.go` (`Candidate` gains two fields)
- Create: `cmd/bridge/dispatch_retry.go`, `cmd/bridge/dispatch_retry_test.go`
- Modify: `cmd/bridge/dispatch.go` (`decisionStatus` `:24-37`, `decisionJSON`/`decisionsJSON` `:64-92`)

**Interfaces:**
- Consumes: `RetryCandidate`, `EvaluateRetry` (Tasks 1–2); `runsPerRepo` (`cmd/bridge/pipeline.go:99`); `milestoneDue`, `dispatch.ActiveMilestone`.
- Produces:
  - `dispatch.Candidate` gains `Retry bool` and `RetryReason string` (the `Action.Reason` that selected it). The zero value is a new dispatch, so every existing `Candidate{}` literal keeps its meaning.
  - In `cmd/bridge`:
    ```go
    type retryReader interface {
        GetIssue(ctx context.Context, owner, repo string, number int) (forge.Issue, []forge.Comment, error)
        ListWorkflowRuns(ctx context.Context, owner, repo, branch, headSHA string, limit int) ([]forge.WorkflowRun, error)
    }
    func collectRetries(ctx context.Context, gh retryReader, repos []repoInput, now time.Time) ([]dispatch.Candidate, []dispatch.Decision)
    ```
    It returns the selected retry candidates and the skipped ones as `Decision{Candidate{…, Retry: true}, false, reason}`.
  - `decisionJSON` gains `Kind string \`json:"kind"\`` (`"dispatch"` | `"retry"`) and `RetryReason string \`json:"retry_reason,omitempty"\``.

`collectRetries` rules:
- Skip any repo with `Forge != "github"`.
- Per issue (the listed one from `ListOpenIssues`), call `RetryCandidate`. A non-empty reason becomes a skip decision with no API call. `ok` adds the issue to the repo's pending list.
- A repo with no pending issue gets **no** API call. Otherwise call `ListWorkflowRuns(owner, name, "", "", runsPerRepo)` once.
  - On error, skip every pending issue with `"runs unreadable"`, call `slog.Warn`, and make no `GetIssue` calls.
- Per pending issue, call `GetIssue` for its comments.
  - On error, skip that issue only with `"issue unreadable"` and call `slog.Warn`.
- Then call `EvaluateRetry(listedIssue, comments, runs, now)`.
  - `ActionRetry` → selected: `Candidate{Issue, Owner, Repo, MilestoneDue: milestoneDue(r.Milestones, ActiveMilestone(r.Milestones)), Retry: true, RetryReason: a.Reason}`.
  - Anything else → skip with `a.Reason`, or `"no action"` when the reason is empty.

`decisionStatus`: the non-retry branches stay byte-identical. For `Candidate.Retry`:
- dispatch → `withReason("retry: "+RetryReason, LaneReason)`.
- dry-run lane → `withReason("WOULD retry: "+RetryReason+" (dry-run)", LaneReason)`.
- skip → `"SKIP retry (" + [LaneReason + "; "] + Reason + ")"`.

`renderDecisions` and its `N dispatched, M skipped` summary stay unchanged, and retries count as dispatched.

- [ ] **Step 1: Write the failing tests** (all in `dispatch_retry_test.go`; `fakeRetryReader` holds `issues map[int][]forge.Comment`, `issueErr map[int]error`, `runs []forge.WorkflowRun`, `runsErr error`, and the counters `runsCalls`, `issueCalls map[int]int`). Fixture: repo `{Forge:"github", Owner:"freaxnx01", Name:"bridge"}`; failed issue = labels `[ai-implement ai:failed]`, title `"T"`; marker `rate_limit`/`stop`/`attempt:1` by `github-actions[bot]`.
  - `TestCollectRetries_FailedRateLimitStop_Selected`: one selected candidate with `Retry && RetryReason == "rate_limit, retries exhausted"`, `runsCalls == 1`, `issueCalls[43] == 1`, no skips.
  - `TestCollectRetries_NoPredicateMatch_NoAPICalls`: issues `[ai-implement]` and `[feat]`, plus a `forgejo` repo with a failed issue → nothing returned and `runsCalls == 0`.
  - `TestCollectRetries_ParkedFailed_SkippedWithoutFetch`: `[ai-implement ai:failed parked]` → skip `"parked"` and `issueCalls` empty.
  - `TestCollectRetries_RunsUnreadable_FailsClosed`: `runsErr` set → skip `"runs unreadable"`, no selection, `issueCalls` empty.
  - `TestCollectRetries_IssueUnreadable_OnlyThatOne`: `#43` errors and `#44` is fine → `#43` skip `"issue unreadable"`, `#44` selected.
  - `TestCollectRetries_SkipReasons_Table`. Each row gives skip reason and no selection:
    - run `{issues, queued, "T"}` → `"running"`.
    - `dispatch.RedispatchComment("x")` an hour after the marker → `"retry pending"`.
    - `task_failure` marker → `"task_failure"`.
    - `[ai-implement ai:failed ai:retrying]` → `"retry interrupted"`.
  - `TestDecisionStatus_Retry` (table; `TestDecisionStatus` stays untouched). Each row is `Candidate{Retry: true, RetryReason: "rate_limit, retries exhausted", Lane: …}`:
    - live dispatch → `"retry: rate_limit, retries exhausted"`.
    - dry-run lane → `"WOULD retry: rate_limit, retries exhausted (dry-run)"`.
    - skip `"retry pending"` → `"SKIP retry (retry pending)"`.
    - skip with `LaneReason "auto→hitl: x"` and `"global cap 3/3"` → `"SKIP retry (auto→hitl: x; global cap 3/3)"`.
    - dispatch with that lane reason → `"retry: rate_limit, retries exhausted (auto→hitl: x)"`.
  - `TestDecisionsJSON_Kind`:
    - a plain decision → `Kind == "dispatch"`, `RetryReason == ""`, and the marshalled JSON has `"kind":"dispatch"` and no `retry_reason` key.
    - a retry decision → `Kind == "retry"`, `RetryReason == "rate_limit, retries exhausted"`.
    - a cap-skipped retry → `Reason == "global cap 3/3"` and `RetryReason` both set.
- [ ] **Step 2: Run to verify failure** — `go test ./cmd/bridge -run 'CollectRetries|DecisionStatus|DecisionsJSON' -v` → FAIL (undefined).
- [ ] **Step 3: Implement.** Keep `collectRetries` small: `retriesForRepo(ctx, gh, r, now)` plus `skipRetry(c, reason) dispatch.Decision`.
- [ ] **Step 4: Run the Go gates**, including the full `go test -race ./cmd/bridge` once. Expected: green, with every existing `dispatch_test.go` test unmodified.
- [ ] **Step 5: Commit**
```bash
git add internal/dispatch/order.go cmd/bridge/dispatch_retry.go cmd/bridge/dispatch_retry_test.go cmd/bridge/dispatch.go
git commit -m "feat(dispatch): collect retry candidates and tag decisions with a kind (#253)"
```

### Task 4: Wire the retry pass into the tick, `--retry-only`, apply the swap

**Files:**
- Modify: `cmd/bridge/dispatch.go` (flag vars and `init` `:138-177`, `runDispatch` `:262-339`, `applyDecisions` `:499-568`)
- Modify: `cmd/bridge/dispatch_retry.go`, `cmd/bridge/dispatch_retry_test.go`

**Interfaces:**
- Consumes: `collectRetries`, `Candidate.Retry/RetryReason` (Task 3); `dispatch.Redispatch`, `RedispatchError`, `RedispatchComment` (Task 2).
- Produces:
  - `var dispatchRetryOnly bool`, registered as a persistent flag: `--retry-only`, help `"run only the retry pass"`.
  - `func tickCandidates(ctx context.Context, repos []repoInput, cfg dispatch.Config, gate dispatch.GateState, now time.Time, retryOnly bool) ([]dispatch.Candidate, []dispatch.Decision)` in `dispatch_retry.go`. It returns `ordered = Order(retries) ++ Order(new)` (with `new` empty when `retryOnly`) and the pre-skipped retry decisions.
    - Lanes are assigned on both slices before ordering: `assignLanes` on candidates, and the same `ResolveLane` on each skipped decision's candidate.
    - It calls `collectRetries` only when `clientFor("github").(*forge.GithubClient)` is ok and non-nil. Never pass a typed-nil client as the interface.
  - `func applyRetry(ctx context.Context, gh *forge.GithubClient, d dispatch.Decision) (booked bool, err error)`:
    1. Call `Redispatch(…, d.Candidate.Lane.EffectiveLabels())`.
    2. On error, `booked = errors.As(err, &re) && re.Step == 4`, and the error is wrapped as `fmt.Errorf("retry %s#%d: %w", …)`.
    3. On success, call `CommentIssue(RedispatchComment(d.Candidate.RetryReason))`.
       - On failure, return `true` and `fmt.Errorf("comment %s#%d: %w", …)`.

`runDispatch`: replace the lines `collectCandidates` → `assignLanes` → `Order` with `ordered, retrySkips := tickCandidates(...)`. After `mergeInOrder`, use `decisions = append(decisions, retrySkips...)`. These skip rows render and serialize in **every** mode and are never applied. The pause gate, window gate, budget, `countOpenAgentPRs`, `ApplyCaps` and the dry-run stop stay unchanged.

`applyDecisions` loop: after the existing dry-run-lane `continue` and client lookup, `if d.Candidate.Retry`:
- `booked, err := applyRetry(...)`.
- If `booked`, append the `usage.Run` and `laneDispatched[lane.Name]++` exactly as the dispatch path does.
- If `err != nil`, set `dispatchErr = err` and `break`.
- `continue`.

The existing dispatch path is untouched.

- [ ] **Step 1: Write the failing tests** (`dispatch_retry_test.go`).
  - Env as in `TestRunDispatchBooksTheRunWhenTheCommentFails`: `writeFakeGithubRepo`, `BRIDGE_REPOS_ROOT`, `BRIDGE_GITHUB_API`, `GH_TOKEN`, temp `XDG_*`, `BRIDGE_CLAUDE_PROJECTS` absent, and **`pinUnattendedSchedule(t)` in every `runDispatch` test**.
  - Helper `newRetryGithub(t, f retryFixture) (*httptest.Server, *writeLog)`:
    - `writeLog` is a mutex-guarded `[]string` of `"METHOD path"` for every non-GET request, plus captured POST bodies, because the handler runs on another goroutine under `-race`.
    - It serves `GET …/issues`, `…/milestones`, `…/pulls`, `GET …/issues/43`, `GET …/issues/43/comments` and `GET …/actions/runs` (`{"workflow_runs":[…]}`).
    - `…/issues/43/comments` serves comments on GET and records on POST.
    - Writes answer `[{"name":"ai-implement"}]` (labels POST), 200 (DELETE) and 201 (comment). `f.failWrite` (a `"METHOD path"`) answers 500.
    - Any other request → `t.Errorf`.
  - Issue `#43`:
    - Labels `[ai-implement ai:failed]`, title `"T"`, body with `## Implementation Plan`.
    - Its comments hold a marker `{"v":1,"class":"rate_limit","decision":"stop","attempt":1,"status":"ai:failed"}` by `github-actions[bot]` at `2026-11-10T10:00:00Z`.
  - `TestRunDispatch_RetryCandidate_SwapsLabelsInOrder`. Writes equal exactly:
    1. `POST /repos/freaxnx01/bridge/issues/43/labels` (body labels `["ai:retrying"]`)
    2. `DELETE /repos/freaxnx01/bridge/issues/43/labels/ai-implement`
    3. `POST /repos/freaxnx01/bridge/issues/43/labels` (labels `["ai-implement"]`)
    4. `DELETE /repos/freaxnx01/bridge/issues/43/labels/ai:retrying`
    5. `POST /repos/freaxnx01/bridge/issues/43/comments`, body ``"Re-dispatched by `bridge dispatch` (retry: rate_limit, retries exhausted)."``

    Afterwards the ledger holds 1 run (`Issue 43`) and the output contains `retry: rate_limit, retries exhausted`.
  - `TestRunDispatch_RetryDryRun_NoWrites`: `setDispatchFlags(t, false, true)` → empty `writeLog`, and the output contains `retry: rate_limit`.
  - `TestRunDispatch_RetryOnly_SkipsEligibleNewIssue`: add eligible `#41` (`[bug]`, plan body), set `--retry-only` via a new `setRetryOnly(t)` helper with cleanup, live run. `writeLog` touches only `#43`, and the output has no `#41`.
  - `TestRunDispatch_RetryInFlight_NoWrites`: runs `[{"event":"issues","status":"in_progress","display_title":"T"}]` → empty `writeLog`, output contains `SKIP retry (running)`.
  - `TestRunDispatch_RetryPendingComment_NoWrites`: add ``"Re-dispatched by `bridge dispatch` (retry: x)."`` at `2026-11-10T11:00:00Z` → empty `writeLog`, output contains `SKIP retry (retry pending)`.
  - `TestRunDispatch_RetryCountsAgainstCaps_RetriesFirst`: add eligible `#41` `[bug]` in the same repo (default `per_repo: 1`), live run.
    - `#43` gets the 5 writes and `#41` gets none.
    - `--json` output: element 0 is `{"issue":43,"kind":"retry","dispatch":true}` and element 1 is `{"issue":41,"kind":"dispatch","dispatch":false,"reason":"repo at WIP 1/1"}`.
  - `TestRunDispatch_RetryStep4Fails_BooksAndErrors`: `failWrite = "DELETE /repos/freaxnx01/bridge/issues/43/labels/ai:retrying"` → the error contains `step 4`, there is no comment POST, and the ledger holds 1 run.
  - `TestApplyDecisions_RetryInDryRunLane_NoWritesCounterAdvances`: mirror `TestApplyDecisionsPersistsTheDryRunLaneCounter` with one `Retry: true` decision. Add a `httptest` server that `t.Errorf`s on any request (set `BRIDGE_GITHUB_API`/`GH_TOKEN`). The lane counter is 1 and the ledger is empty.
  - `TestDispatchSubcommands_RetryOnlyIsPersistent`: `rootCmd.SetArgs([]string{"dispatch", "now", "--retry-only", "--dry-run"})` with `BRIDGE_BASE` an empty temp dir runs with no error. Cleanup resets all four flag vars.
- [ ] **Step 2: Run to verify failure** — `go test ./cmd/bridge -run 'Retry' -v` → FAIL (`dispatchRetryOnly`, `tickCandidates`, `applyRetry` undefined).
- [ ] **Step 3: Implement** as specified. `runDispatch` must not grow: the gather step moves into `tickCandidates`.
- [ ] **Step 4: Run the Go gates.** Run the full `go test -race ./...`, ~150 s for `cmd/bridge`. Expected: green, and `TestRunDispatch_FullPipeline_AppliesOnlyEligibleLabel` and its siblings pass unmodified (their `#42` has no `ai:failed`, so no extra GET).
- [ ] **Step 5: Commit**
```bash
git add cmd/bridge/dispatch.go cmd/bridge/dispatch_retry.go cmd/bridge/dispatch_retry_test.go
git commit -m "feat(dispatch): run the retry pass in every tick and add --retry-only (#253)"
```

### Task 5: Docs and changelog

**Files:**
- Modify: `docs/dispatch.md` ("When a run fails" → "Status today" `:258-260`; flags list `:345-349`; "Systemd timer and service" `:362`), `internal/dispatch/types.go:36`, `CHANGELOG.md` (`[Unreleased]` → `Added`)

- [ ] **Step 1: Edit `docs/dispatch.md`**
  - Replace "Status today" with: every tick runs the retry pass before new dispatches, and `--retry-only` limits a run to that pass.
  - Add a `### Retry pass` subsection under "When a run fails" covering:
    - candidates (the label predicate, GitHub only).
    - the in-flight rule (shared with the cockpit).
    - only `ActionRetry`; row 14 stays Human (agent-workflow #351).
    - the `retry pending` guard and its comment prefix.
    - the 4-step swap and why it is needed (`issues: labeled`).
    - retries first, shared caps, booked in the ledger.
    - skip reasons: `retry interrupted`, `runs unreadable`, `issue unreadable`, `retry pending`, `running`, plus `NextAction` reasons.
    - **recovery:** a leftover `ai:retrying` keeps both passes off the issue until a person removes it, after checking that `ai-implement` is present or re-adding it.
  - Flags list: add `--retry-only`; `--json` now carries `kind` and, for retries, `retry_reason`.
  - Timer section, `:362`: replace the "no separate retry-only mode yet … future work" sentences with the retry pass running in every in-window tick under the same bounds. No new unit. During the observation week the timer runs with a `--dry-run` drop-in, and removing it is an operator decision.
- [ ] **Step 2:** In `types.go:36`, change "bare hourly heartbeat" to "bare half-hourly heartbeat".
- [ ] **Step 3:** CHANGELOG `Added`: "`bridge dispatch`: retry pass — every tick re-dispatches failed issues whose run marker says a retry can fix them (guarded `ai:retrying` label swap, shared caps, retries first); `--retry-only` runs only that pass; decisions carry a `kind`. (#253)"
- [ ] **Step 4: Run the gates.** Run `gofmt -l .`, `go vet ./...` and `golangci-lint run` (the types.go comment), and check that the doc renders with no broken anchors (`grep -n "Retry pass" docs/dispatch.md`).
- [ ] **Step 5: Commit**
```bash
git add docs/dispatch.md internal/dispatch/types.go CHANGELOG.md
git commit -m "docs(dispatch): document the retry pass and --retry-only (#253)"
```
