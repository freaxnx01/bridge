# bridge dispatch — autonomy lanes

**Date:** 2026-09-21
**Status:** Design approved, not implemented
**Issue:** [#304](https://github.com/freaxnx01/bridge/issues/304)
**Decision record:** [`2026-07-27-bridge-dispatcher-design.md`](2026-07-27-bridge-dispatcher-design.md), Amendment 1
**Depends on:** [#303](https://github.com/freaxnx01/bridge/issues/303) — required before the auto lane goes live, not before this merges

## Problem

The dispatcher has one lane, designed around the operator's review capacity.
`global_open_prs` is that capacity expressed as a number, and every dispatched
issue consumes a slot until the operator merges its PR.

For repos whose output is low-stakes and self-deploying — browser games on
Pages, with an automated test workflow and `ai-review-ai-merge: true` in their
`agent.yml` — that cap is the wrong constraint. Those PRs are reviewed and
merged by the pipeline; they never touch the operator's mornings, so counting
them against review capacity throttles work for a resource it does not spend.
Three self-merging game PRs can starve the lane that does need review.

The second half of the original complaint — "a 22:00-only tick wastes 23 hours"
— was **already resolved by #254**. `schedule.windows` now tiles the whole day
and the systemd unit is an hourly heartbeat; daytime dispatch is bounded by the
usage-budget rung rather than forbidden by the clock. What remains is cadence
*granularity* (30 minutes rather than 60 for a lane that merges its own work)
and, with it, a per-lane dispatch ceiling that bounds a 24-hour lane the way
`max_dispatches_per_night` bounds an overnight one.

## Solution

Lanes, matched per repo by glob in `dispatch.json`, first match wins. A lane
carries its own schedule, its own caps, its own labels, and one flag saying
whether its work is reviewed by a human or by the pipeline.

```json
"lanes": [
  { "name": "auto", "repos": ["game-*"], "autonomous": true, "dry_run": true,
    "windows": [{ "from": "00:00", "to": "00:00" }],
    "limits": { "per_repo": 1, "max_dispatches": 12 } },
  { "name": "hitl", "repos": ["*"] }
]
```

Everything is additive: **a config with no `lanes` key behaves exactly as it
does today**, through an implicit default lane. That is what keeps the
observation week honest and the rollback a one-line edit.

### `autonomous` — one flag, three consequences

`autonomous: true` is not three knobs. It is a single statement — *this lane's
PRs merge themselves* — and everything else follows from it:

| Consequence | Why it follows |
|---|---|
| Exempt from `global_open_prs` | The cap measures operator review capacity; this lane spends none |
| Gated on `ai-review-ai-merge: true` in the repo's `agent.yml` | A repo that will not auto-merge would silently pile up unreviewed PRs |
| Labels default to `["ai-implement", "ai-review-ai-merge"]` | The gate label the pipeline checks, applied with the trigger |

A non-autonomous lane defaults to `["ai-implement"]` — today's behaviour
exactly. An operator who has wired `ai-review-human-merge: true` into a repo's
`agent.yml` names that label explicitly in the lane's `labels`; bridge does not
apply it by default, because applying a gate label to a repo that has not wired
the matching workflow input is a silent no-op at best.

### Exclusion is two-way

Open agent PRs in an autonomous lane's repos are **not counted** into the global
total at all, not merely exempt from the check. `global_open_prs: 3` then means
what the original spec says it means: three PRs awaiting the operator. A busy
auto lane can never consume the hitl lane's slots.

### The window gate stays `--auto`-only

Lane windows decide whether a lane acts on a **timer** tick. They do not gate a
manual `bridge dispatch now`, which was never window-gated and must stay that
way: a schedule with a deliberate gap is a documented configuration, and
partitioning a manual tick by lane window turns it into a tick that dispatches
nothing at all.

The `--auto` gate is also answered **before any network call** — from the
config and the clock alone — so an out-of-window tick stays the free no-op it
was before lanes, rather than 48 full repo sweeps a day.

*(Added 2026-09-21: the original spec left this implicit, and the plan's Task 7
consequently partitioned unconditionally. Caught in review of PR #309.)*

### Windows, and where the budget rung lives

A lane's `windows` reuse the schedule's `{from, to}` spans, inheriting
`schedule.windows` when omitted. They decide two things: whether the lane acts
at this instant, and — via `StartOf` — which occurrence the lane's dispatch
counter belongs to.

**Lane windows carry no `budget_rung` field.** The rung stays global, computed
from the top-level schedule exactly as #254 built it, and applies to every lane.
The rung protects one shared resource (the subscription's 5-hour window); giving
lanes their own copy of the flag would create two sources of truth about a single
quota, and the one that lied would be whichever was edited second.

### Caps

`limits.max_dispatches` is a hard ceiling per **window occurrence**, enforced
whenever it is configured. With the auto lane's single `00:00 → 00:00` window,
one occurrence is a local calendar day, so the daily cap falls out of the
existing `Window.StartOf` arithmetic — including its DST corrections — rather
than introducing a second counting rule.

The top-level `max_dispatches_per_night` keeps its #254 semantics untouched
(armed only in a window whose rung is off) and bounds the implicit default lane.

Check order inside the single ordered walk:

```
budget rung          → shared quota; refuses everything once spent
lane max_dispatches  → this lane's occurrence ceiling
global_open_prs      → skipped entirely when lane.Autonomous
per_repo             → conflicting concurrent PRs in one repo
```

### Fallback

An autonomous lane whose repo fails the gate falls through to the next
**matching non-autonomous** lane, carrying a reason that `--dry-run` prints:

```
auto→hitl: agent.yml lacks ai-review-ai-merge: true
```

The gate reads `.github/workflows/agent.yml` from the repo's default branch via
the existing `GithubClient.GetFile`, and matches
`(?m)^\s*ai-review-ai-merge:\s*true\s*$` — the same grep `/gh:implement`
performs, and the same file `check-ai-merge-gate.sh` keys on, so bridge and the
pipeline gate cannot disagree. No YAML dependency is added.

The result is cached at `~/.cache/bridge/lane-gate.json` per repo with a 6-hour
TTL, so a 30-minute tick is not an API storm. **A missing file, a parse miss, or
a failed fetch all fall back to hitl** — fail closed toward human review, never
toward unattended merge.

Reading the *local* checkout was rejected: a stale or dirty working copy can
disagree with what the pipeline actually runs, which is precisely the
misconfiguration this gate exists to catch.

### `dry_run` — the observation week, in code

A lane carrying `"dry_run": true` runs the entire pipeline for real on every
timer tick — lane resolution, the `agent.yml` gate, ordering, every cap, the
budget — and reports every decision. It applies no labels, books no ledger run,
and advances no counter.

One rule makes this safe: **a dry-run lane observes the *shared* counters but
never consumes them.** Its decisions are computed against the current budget
spend and global count without mutating either. Without this, a dry-run auto
lane would book hypothetical spend against the shared quota and block real hitl
dispatch — the observation would change what it observes.

The counters that are **private to one lane** are the other half of that rule,
and a dry-run lane does advance and persist them: its own `max_dispatches`
counter, and its per-repo count. Every repo resolves to exactly one lane, so
neither can reach a live lane, and suppressing them would make the preview
*overstate* what the live lane would do — three candidates in one repo all
reporting "WOULD dispatch" under `per_repo: 1`, and a persisted counter stuck
at zero while a half-hourly timer replays a full lane's worth of decisions 48
times a day. An observation week that overstates is worse than none.

*(Amended 2026-09-21 after review of PR #309, which implemented the original
wording faithfully and surfaced both consequences.)*

Enabling the lane after the week is deleting one word, and the week's output was
produced by the same code path that then acts.

## Implementation shape

All of it lands in `internal/dispatch` as pure functions plus a thin fetch/cache
layer in `cmd/bridge`, following the package's existing split.

```go
// internal/dispatch — pure
type Span struct{ From, To string }          // extracted from Window
type Window struct{ Span; BudgetRung bool }  // JSON shape unchanged

type Lane struct {
    Name       string
    Repos      []string
    Autonomous bool
    DryRun     bool
    Windows    []Span
    Labels     []string
    Limits     LaneLimits
}

func ResolveLane(lanes []Lane, repo string, gate GateState) (Lane, string)
func (l Lane) InWindow(now time.Time) bool
```

- `Span` carries `Covers` and `StartOf`; `Window` embeds it, so the config's
  JSON shape does not change and `Schedule.InWindow` is untouched.
- `Candidate`/`Decision` gain the resolved `Lane` and a `LaneReason`.
- `Counts` gains `DispatchedByLane map[string]int`; `GlobalOpen` is computed in
  `cmd/bridge` with autonomous-lane repos left out.
- `State` gains `Lanes map[string]LaneState{ StartedAt, Dispatched }`, read
  through a per-lane analogue of `DispatchesSince(boundary)`. The existing
  `DispatchedTonight` / `NightStartedAt` fields stay, serving the implicit lane.
- `applyDecisions` applies `d.Lane.Labels` in one `AddLabels` call — the method
  already takes `[]string`, so no forge change is needed.

### CLI surface

`--dry-run` gains a lane column, and a fallback's reason rides along on the row:

```
  game-tschau-sepp #14  fix: card flip race   auto  → WOULD dispatch (dry-run)
  bridge           #290 feat: lanes           hitl  → dispatch
  game-huusli-jagd  #9  chore: bump vite      hitl  → SKIP (auto→hitl: agent.yml
                                                      lacks ai-review-ai-merge: true)
```

`--json` decisions gain `lane`, `lane_reason`, `labels`, `dry_run`.
`dispatch status` grows a per-lane block: window, caps, counter, dry-run state,
and the gate cache's age.

### Timer

`docs/systemd/bridge-dispatch.timer` becomes `OnCalendar=*-*-* *:00,30:00`. It
stays a dumb heartbeat; the config remains the only place hours are written down.

## Testing

Table-driven, no network, per the package's existing shape:

- `ResolveLane` — first match wins, glob semantics, gate failure → fallback with
  reason, unreadable gate → fallback, no match → implicit default lane
- the gate regexp against real `agent.yml` fixtures, including a repo wiring
  `ai-review-human-merge` and one wiring neither
- `ApplyCaps` — autonomous lane skips the global check, lane ceiling enforced,
  per-repo still enforced, and a dry-run lane leaves `spent`/`global`/per-repo
  counters unchanged for the lanes that follow it in the walk
- per-lane counter rollover across a window occurrence boundary, including a DST
  day, reusing the fixtures `5bb5e56` pinned
- gate cache TTL with a fake clock and `t.TempDir()`

## Out of scope

- **The enrich lane.** Amendment 1's phase 2 — absorbing agent-workflow's
  `/autopilot` so bridge schedules enrichment too — is a separate issue once v1
  has run.
- **#303's intake fix.** A hard dependency for *going live*, not for merging:
  `dry_run: true` is what makes landing this first safe. The auto lane must not
  be taken out of dry-run until #303 has shipped, or an empty capture-created
  issue could reach an auto-merging pipeline.
- **Per-lane pause.** `bridge dispatch pause` stays global; a lane is stopped by
  editing its `dry_run` or removing it.
- **Forgejo.** Unchanged — `ai-implement` runs on GitHub Actions.

## Consequences

- Regressions reach production without human review in the auto lane. The repo's
  test workflow and `git revert` are the safety net; this is Amendment 1's
  accepted trade, not a new one.
- The morning review batch shrinks to hitl work plus whatever the auto lane
  routed to a human.
- A repo leaves the auto lane by editing `dispatch.json`. No code change, no
  redeploy.
- One extra API call per auto-lane repo per gate-cache TTL, and the cache is a
  new file in `~/.cache/bridge`.
