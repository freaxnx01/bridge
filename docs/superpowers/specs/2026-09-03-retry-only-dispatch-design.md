# Retry-only dispatch mode — design

**Issue:** [#253](https://github.com/freaxnx01/bridge/issues/253)
**Date:** 2026-09-03
**Status:** Approved

## Problem

`internal/dispatch/failure.go` defines `NextAction`, which fully specifies what to
do with an issue whose pipeline run failed: transient buckets (`api_auth`,
`rate_limit`, `infra`) retry without consuming an attempt; substantive ones
(`max_turns`, `gate_failed`, `no_diff`) increment `attempt:N` and park at 2.

Nothing calls it.

The hourly systemd ticks (23:00–06:00) run the identical full dispatch path as the
22:00 main tick, bounded by the same caps. What stops them re-labeling a failed
issue is `Eligible()`'s "already dispatched" guard, not any retry-specific code
path. Any actual retry requires a manual `bridge dispatch now`.

## Retry ownership — bridge owns cross-tick retry only

agent-workflow already retries inside a run: `scripts/retry-dispatch.sh` plus
`classify-failure.sh` re-dispatch on rate-limit/transient/task failures with their
own per-class caps (rate 3, transient 3, task 1).

The two budgets **nest** rather than compete:

```
run fails
  -> agent-workflow retry-dispatch.sh retries in-run (minutes, its own caps)
     -> still failed: writes failed:<bucket> and stops
        -> next bridge hourly tick: NextAction
           -> attempt:N++ / park at 2
```

Bridge picks up only what agent-workflow already gave up on. agent-workflow keeps
its in-run retries unchanged; this issue adds no coupling between the two.

## Design

### 1. Selection — a second predicate, not a patched `Eligible()`

`Eligible()` rejects retry candidates by construction: a failed run's issue still
carries `ai-implement` (`already dispatched`), and once `attempt:2` is set,
`attempts exhausted`. Both guards are correct for new work and wrong for retries,
so retry-only gets its own predicate in a new `internal/dispatch/retry.go`:

`RetryEligible(i forge.Issue) (bool, string)` returns true iff:

1. `FailureBucket(i.Labels) != ""` — there is a `failed:<bucket>` label
2. the issue is not `🧊 parked`
3. the issue does not carry `ai:retrying`

The attempt budget is deliberately **not** rechecked here — `NextAction` already
owns it and parks at 2. Duplicating the threshold in two places is how they drift.

### 2. Decision flow — park before caps, retry after

Parking is not a dispatch and must not consume a cap slot, so the two outcomes of
`NextAction` are applied at different points:

1. Retry candidates → `NextAction` per issue.
2. **Park actions apply immediately** — label changes and comment, no caps consulted.
3. **Retry actions** → existing `Order()` → existing `ApplyCaps()` → apply the swap.

Retries pass through `ApplyCaps` unchanged and count exactly like a new dispatch
against the nightly cap, the global open-PR cap, and the per-repo WIP cap. This is
one code path and it is honest about spend; the accepted consequence is that a
night of failures competes with new work for the nightly budget, and that retries
stop once the nightly cap is reached.

### 3. The label swap

A failed issue still carries `ai-implement`, and the consumer pipeline triggers on
`issues: [labeled]` only — both agent-workflow's own `.github/workflows/agent.yml`
and the documented consumer stub in `docs/CONSUMER-SETUP.md`. There is no
`workflow_dispatch` entry point, so re-adding an already-present label is inert and
a retry must remove and re-add it.

Between those two calls the issue looks un-dispatched. A marker label closes that
window:

```
add    ai:retrying
remove ai-implement
add    ai-implement     <- fires issues:labeled, pipeline runs
remove ai:retrying
```

`NextAction`'s own `failed:<bucket>` removal folds into the same label work rather
than becoming a fifth call.

Both `Eligible()` and `RetryEligible()` gain an `ai:retrying` check that skips with
reason `retry interrupted`. A crash anywhere in the swap therefore strands the
issue **visibly** — it shows up in dry-run output with that reason — instead of
silently re-entering the normal dispatch queue with no `ai-implement` and a cleared
`failed:*` label.

`ai:retrying` is declared as `LabelRetrying` alongside the existing label constants
in `internal/dispatch/eligible.go` (`LabelNeedsEnrichment`, `LabelParked`,
`LabelAIImplement`) and added to the label table in `docs/dispatch.md`. Bridge has
no label-ensuring code path — labels are plain constants — so, like `🧊 parked`, the
label must exist in each dispatched-to repo before it can be applied. Whether to add
a create-if-missing step is out of scope here; the existing labels have the same
property.

### 4. CLI, timer, docs

- `--retry-only` becomes a fourth persistent flag on `dispatchCmd`, alongside
  `--dry-run`, `--json`, and `--auto`. It composes with all three.
- **systemd splits into two unit pairs.** One unit cannot carry two different argv,
  so the existing `bridge-dispatch.{service,timer}` keeps the 22:00 full tick and a
  new `bridge-dispatch-retry.{service,timer}` runs `--auto --retry-only` hourly.
- `docs/dispatch.md`: replace the "Status today" paragraph under *When a run fails*,
  document the flag under *Flags*, and rewrite *Systemd timer and service* for the
  two-unit layout.
- **No new config keys.** Reusing the existing caps is the point of the caps
  decision above.

### 5. Testing

Following the package's existing convention — pure functions over plain structs,
table-driven tests, no network:

- `retry_test.go` — table over `RetryEligible`, one row per skip reason plus the
  accept case.
- Swap-ordering test against a fake forge client asserting the exact four-call
  sequence, plus a crash-after-remove case that leaves `ai:retrying` set.
- Park-before-caps test: an issue at `attempt:1` with a substantive failure parks
  without consuming a nightly slot.
- `NextAction` itself is already covered by `failure_test.go` and is not retested.

## Consequences

- A night of failures competes with new work for the nightly cap; once that cap is
  hit, retries stop until the next night window. Accepted deliberately in exchange
  for a single cap code path.
- Recovery latency for a failed run is up to one hour (the tick interval), on top of
  whatever agent-workflow's in-run retries already spent.
- `ai:retrying` is a new label that outlives a crashed process. It requires manual
  removal if a tick is killed mid-swap; the dry-run reason names it explicitly so
  the state is discoverable rather than mysterious.

## Cross-issue note

[#254](https://github.com/freaxnx01/bridge/issues/254) extends the timer into
07:00–18:00 and adds a daytime usage-budget rung. Once it lands, these hourly retry
ticks fall under that rung during the workday — a retry is spend like any other
dispatch. There is no code dependency in either direction; the two issues can land
in any order.

## Out of scope

- Changing agent-workflow's in-run retry behaviour or its per-class caps.
- Adding a `workflow_dispatch` entry point to the consumer pipeline. Noted as a
  separate observation: agent-workflow's `retry-dispatch.sh` and
  `chain-dispatch.yml` both call `gh workflow run <consumer> -f issue-number=N`,
  which the documented consumer stub cannot service. That is an agent-workflow
  concern, not this issue's.
