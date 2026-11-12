# Retry guard timeout and open-PR gate (#390)

**Issue:** freaxnx01/bridge#390 — follow-up from the automated review of #389 (#253).
**Status:** approved 2026-11-12.

## Why

The retry pass (#253) re-dispatches an issue whose last run stopped on a
retryable class with the pipeline's own retries exhausted (row 16 of the
`NextAction` table in `docs/dispatch.md`). Three gaps came out of the #389
review; the first one must be closed before the dispatch timer leaves
`--dry-run`:

1. **The retry-pending guard never times out.** `dispatch.RetryPending`
   (`internal/dispatch/retry.go:25`) suppresses a retry for as long as a
   ``Re-dispatched by `bridge dispatch` `` comment is newer than the latest run
   marker. A re-dispatched run that never starts, or dies before posting its
   report (the 2026-11-11 runner apt/DNS outage killed several runs in
   `ensure-toolchain.sh` exactly like that), never produces a newer marker. The
   dispatcher then skips the issue as `retry pending` on every tick, forever.
   The guard lives only in `EvaluateRetry` (`internal/dispatch/retry.go:38`),
   while the cockpit inbox calls `NextAction` directly
   (`internal/cockpit/inbox.go:138`), so the inbox shows the same issue as a
   `retry` card forever — the two views disagree and neither asks a person.
2. **No open-PR gate on retries.** `retriesForRepo`
   (`cmd/bridge/dispatch_retry.go:41`) never looks at the repo's open PRs,
   unlike the new-dispatch path (`dispatch.Eligible` → `HasOpenPR`,
   `internal/dispatch/eligible.go:114`). Only the per-repo WIP cap at its
   default `per_repo: 1` prevents a second run next to a PR the failed run
   already opened.
3. **Untested booking rule.** `applyRetry` (`cmd/bridge/dispatch_retry.go:146`)
   books a retry only when the swap succeeds or fails at step 4. Only the step-4
   path is tested; a failure at step 1 or 2 must book nothing.

## Decisions (made by the user)

- **D1 — the guard moves into `NextAction`.** `IssueState` carries the time of
  the latest bridge re-dispatch comment and whether an open PR closes the
  issue. `NextAction` applies both on top of a would-be `ActionRetry`. The
  cockpit inbox and the dispatcher build `IssueState` through one shared
  constructor, so they always agree.
- **D2 — timeout 3 h, then Human.** Reuse `staleRetryAfter` (3 h,
  `internal/dispatch/failure.go:23`, the same bound as the pipeline's own
  lost-retry row 14). After it the issue becomes `ActionHuman` with reason
  `re-dispatch never reported`. There is no automatic second re-dispatch:
  unknown state never spends money.
- **D3 — open PR → Human.** A would-be retry with an open PR closing the issue
  becomes `ActionHuman` with reason `open PR`. The inbox shows it as needing a
  person; the dispatcher never stacks a run next to the PR, whatever
  `per_repo` says.

## Design

### New mapping rows

Inserted between rows 15 and 16 of `docs/dispatch.md` ("The mapping"), so no
existing row number changes. They only ever apply to a state that would
otherwise reach row 16 (Retry); the first matching one wins.

| # | Pipeline state | Kind | Reason |
|---|---|---|---|
| 15a | row 16 would apply, a bridge re-dispatch comment is newer than the marker, and it is older than 3 h | Human | `re-dispatch never reported` |
| 15b | row 16 would apply and a bridge re-dispatch comment is newer than the marker | Wait | `retry pending` |
| 15c | row 16 would apply and an open PR closes the issue | Human | `open PR` |

The order (pending before open PR) is deliberate: a pending re-dispatch can
itself be the run that opened the PR, and while its 3 h are running the right
answer is to wait for its report.

### API changes (package `dispatch`)

- `IssueState` gains two fields:
  - `Redispatched time.Time` — creation time of the newest comment whose body
    starts with `RedispatchCommentPrefix`; zero when there is none.
  - `HasOpenPR bool` — an open PR closes this issue (`HasOpenPR(prs, n)`).
- `func LastRedispatch(comments []forge.Comment) time.Time` replaces
  `RetryPending`. The author is still not checked (the guard can only suppress
  a retry or ask a person).
- `NextAction` applies the 15a–15c guard to a would-be `ActionRetry`. "Newer
  than the marker" is `Redispatched.After(Run.Posted)` (strictly after, as
  `RetryPending` does today). "Older than 3 h" is
  `Now.Sub(Redispatched) > staleRetryAfter`.
- `func StateFor(i forge.Issue, comments []forge.Comment, runs []forge.WorkflowRun, prs []forge.PullRequest, now time.Time) IssueState`
  is the single constructor both callers use: `LatestRun`, `InFlight`,
  `LastRedispatch`, `HasOpenPR`.
- `EvaluateRetry` and `RetryPending` are removed; callers use
  `NextAction(StateFor(...))`.

### Callers

- **Dispatcher** — `retriesForRepo` passes `r.PRs` (already fetched per repo,
  `cmd/bridge/dispatch.go:138`) to `StateFor`. Skip reasons gain
  `re-dispatch never reported` and `open PR` automatically, since a non-Retry
  action's reason is already the skip reason.
- **Cockpit inbox** — `classify` builds its state with
  `dispatch.StateFor(i, comments, rd.Runs, rd.PRs, now)` (`RepoData.PRs`
  exists, `internal/cockpit/board.go:88`) and reads `Run` / `HasRun` from the
  returned state for the card. A 15a or 15c issue becomes a human card, a 15b
  issue a waiting card.

### Not in scope

- No change to the swap, the booking rule, caps, ordering or the comment text.
- No configurable timeout.
- No change to the pipeline (agent-workflow).

## Acceptance criteria

- A row-16 issue with a bridge re-dispatch comment newer than its marker and at
  most 3 h old maps to Wait `retry pending`; older than 3 h, to Human
  `re-dispatch never reported` — in `NextAction`, the dispatcher's skip rows and
  the cockpit inbox alike.
- A row-16 issue with an open PR closing it maps to Human `open PR` in all three;
  the dispatcher makes no write for it.
- A re-dispatch comment older than the marker does not affect the mapping.
- A swap failing at step 1 or step 2 returns an error naming the step, posts no
  comment and books nothing in the spend ledger.
- `RetryPending` and `EvaluateRetry` no longer exist; `docs/dispatch.md`
  documents rows 15a–15c and the new skip reasons; `CHANGELOG.md` has an entry.
- `gofmt -l .` is empty, `go vet ./...` and `go test -race ./...` pass.
