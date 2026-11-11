# Retry pass for `bridge dispatch` — design (#253)

Supersedes the 2026-09-03 draft (branch `docs/retry-only-dispatch-253`, never
merged), which predates #367 and the half-hourly timer.

## Why

A failed pipeline run is never picked up again unattended. `NextAction` (#367)
says when a re-dispatch can fix a failure (`ActionRetry`), but nothing calls it,
and nothing could act on it: `dispatch.Eligible` rejects every issue that carries
`ai-implement` ("already dispatched", `internal/dispatch/eligible.go:99`), and the
pipeline starts only on `issues: labeled`, so re-adding a label that is still
present does nothing.

## Corrections to the issue text

- The `failed:*` / `attempt:N` labels, "park at 2" and the transient/substantive
  buckets are gone (#367). The pipeline's run marker and attempt cap own that;
  bridge only reads `NextAction`.
- The timer is a half-hourly heartbeat (`docs/systemd/bridge-dispatch.timer`),
  gated by `schedule.windows` in `dispatch.json`. There is no separate 22:00 tick
  and no hourly retry unit.
- `bridge dispatch now` cannot retry either — the "already dispatched" rule blocks
  it. `docs/dispatch.md` ("Status today") says otherwise; fixed here.

## Decisions (2026-11-11)

1. **Re-dispatch = guarded label swap**, not `workflow_dispatch`: bridge's
   `agent.yml` has no `workflow_dispatch` trigger (agent-workflow #351, #574,
   #583). One shared function, also used by the inbox Retry button (#374).
2. **Retry pass inside every `--auto` tick**, before new dispatches, sharing
   `Order` and the caps. `--retry-only` limits a run to that pass. No second
   systemd unit, no new config key.
3. **Only `ActionRetry`** (row 16 of #367's table). Row 14 ("retry never
   reported") stays Human; its usual cause in bridge is #351, which is linked, not
   worked around.

## Non-goals

- Changing `NextAction`, the pipeline, or `agent.yml`.
- Forgejo / Azure DevOps (no run marker).
- The inbox button (#374) — it reuses `Redispatch` later.
- Going live: the timer runs with a `--dry-run` drop-in during the observation
  week; removing it is an operator decision.

## Retry candidates

Per GitHub repo the tick already fetches (`fetchRepoInputs`), an open issue is a
**retry candidate** when it carries `ai-implement` and `ai:failed`, and none of
`parked` / `🧊 parked` (`IsParked`) / `ai:retrying` / `ai:paused`. *(Pre-filter, my
call: row 16 needs a failed run's marker, so other issues cannot reach it and cost
no extra call.)*

For each candidate:

1. `GetIssue` → comments → `LatestRun`.
2. `InFlight` from the repo's workflow runs (one `ListWorkflowRuns` per repo that
   has a candidate), with the shared rule below.
3. `NextAction(IssueState{Labels, Run, HasRun, InFlight, Now})`.
4. **Retry pending guard:** skip with reason `retry pending` when a comment
   starting with `Re-dispatched by \`bridge dispatch\`` is newer than
   `Run.Posted` — bridge already retried and the pipeline has not reported since.
   *(My call: without it, a re-dispatch whose run cannot be attributed by title,
   or that never starts, is retried again every 30 minutes until a cap bites.
   The guard only ever suppresses a retry, so a forged comment cannot cause one.)*
5. Kind `ActionRetry` → selected for the retry pass with `Action.Reason`.
   Anything else → listed as skipped with its kind/reason (dry-run output only).

## In flight — shared rule

#368 (PR #378) adds `forge.WorkflowRun.DisplayTitle` and an in-flight rule in
`internal/cockpit`: a run with `Event == "issues"`, status `queued` or
`in_progress`, and `DisplayTitle == issue.Title`. This issue moves that rule to
`func InFlight(i forge.Issue, runs []forge.WorkflowRun) bool` in package
`dispatch`, and the cockpit calls it. Behaviour and documented limits unchanged.

## Ordering and caps

Retry candidates go through the existing `dispatch.Order` and `dispatch.ApplyCaps`
like new candidates and count against the same nightly / global / per-repo / lane
caps and the budget rung. **Retries come first** within a tick (*my call*: a failed
issue already holds a slot's worth of sunk cost; finishing it beats starting
something new). A retried issue has no open PR, so it holds no WIP slot until the
run opens one — same as a new dispatch. Each retry is booked in the spend ledger
like a dispatch.

## Re-dispatch

```go
// labelSwapper is the forge surface the swap needs (GithubClient has both).
type labelSwapper interface {
    AddLabels(ctx context.Context, owner, repo string, number int, labels []string) ([]string, error)
    RemoveLabel(ctx context.Context, owner, repo string, number int, label string) error
}

// Redispatch re-triggers the pipeline for an issue that already carries
// ai-implement: the label must be removed and added again, because the
// pipeline starts only on `issues: labeled`.
func Redispatch(ctx context.Context, gh labelSwapper, owner, repo string, number int, labels []string) error
```

Exact call order — the test pins it:

1. `AddLabels([ai:retrying])` — marks the swap in progress.
2. `RemoveLabel(ai-implement)`.
3. `AddLabels(labels)` — the lane's `EffectiveLabels()`; this fires the pipeline.
4. `RemoveLabel(ai:retrying)`.

A failure stops the sequence and returns the error with the step named. Left
behind, `ai:retrying` makes the issue visible and keeps both passes off it
(`Eligible` and the retry predicate return `retry interrupted`) until a person
clears it. The old `ai:failed` label is left alone — the next run report replaces
the status label.

After a successful swap the tick comments
`Re-dispatched by \`bridge dispatch\` (retry: <reason>).` — the guard in step 4
above keys on that prefix.

New constant `LabelRetrying = "ai:retrying"` in `eligible.go`. GitHub creates an
unknown label on first use; no setup step.

## CLI

- `--retry-only` — fourth persistent flag on `bridge dispatch`, combinable with
  `--dry-run`, `--json`, `--auto`. Runs only the retry pass.
- Without it, `--auto` and `now` run the retry pass, then the new-dispatch pass.
- Output: each decision gains a kind (`dispatch` | `retry`); retries show their
  reason. JSON adds `"kind"` and, for retries, `"reason"`.
- The `--auto` pause and window gates apply to the retry pass unchanged.

## Testing

- `dispatch`: table tests for the candidate predicate (labels), `InFlight` (moved
  tests + cockpit still green), the retry-pending guard, `Redispatch` call order
  and stop-on-error for each of the 4 steps (fake `labelSwapper`).
- `cmd/bridge` (httptest fake GitHub, existing pattern): a tick with one retry
  candidate (marker `rate_limit`/`stop`/`attempt:1`) performs exactly
  POST `ai:retrying` → DELETE `ai-implement` → POST `ai-implement` → DELETE
  `ai:retrying` → POST comment, in that order; `--dry-run` performs none;
  `--retry-only` skips an eligible new issue; an in-flight run (title match) and a
  newer bridge comment each prevent the retry; caps count the retry.

## Docs

`docs/dispatch.md`: "When a run fails → Status today" (now wired), a "Retry pass"
subsection (candidates, guard, swap, `ai:retrying` recovery), the flag table, and
the timer section. `internal/dispatch/types.go:36` still says "hourly" — fix.
CHANGELOG.

## Dependencies

- #367 (merged): `NextAction`, `LatestRun`.
- #368 / PR #378: `DisplayTitle` and the in-flight rule this issue moves. **Dispatch
  #253 after #378 merges.**
- agent-workflow v2.2.0 (released): run markers.
- Linked, not fixed here: agent-workflow #351 (no `workflow_dispatch` in consumer
  stubs). #374 (inbox Retry) reuses `Redispatch`.
