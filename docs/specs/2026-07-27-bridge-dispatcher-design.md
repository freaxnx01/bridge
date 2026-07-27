# bridge dispatch — cross-repo autonomous dispatcher

**Date:** 2026-07-27
**Status:** Design approved, not implemented
**Scope:** Subsystem 1 of 3 in the end-to-end HITL dev workflow (dispatcher · notification channel · planning layer)

## Problem

Issues reach repos through FlowHub and are specified by hand via `/enrich`. From
there to a draft PR the workflow is entirely manual: a human opens `/gh:route`,
reads the model policy, and applies `ai-implement` to one issue in one repo. There
is no cross-repo view, no priority rule, no bound on how much work is in flight, and
no unattended path from a ready issue to a PR.

The classification brain already exists — `/gh:route` Step 4b holds the task-shape →
model policy and `scripts/classify-task.sh` executes it inside the pipeline. What is
missing is the **loop**: something with a cross-repo view, ordering rules and WIP
guards that applies the trigger label on its own.

The binding constraint is **not** compute. It is the operator's review capacity. An
unbounded dispatcher converts "no PRs" into "eleven open PRs nobody reviews".

## Solution

A new `bridge dispatch` command: a deterministic, unattended scheduler that runs a
single nightly batch, picks eligible issues across all GitHub repos in priority
order, and applies exactly one label — `ai-implement`. It never decides which model
runs.

### Boundary

```
flowhub          capture → classify → route → issue lands in a repo
   │
   ▼
OPERATOR         /triage → /enrich          ← the only mandatory human gesture
   │
   ▼
bridge dispatch  scheduler ONLY. Owns: eligibility, ordering, guards, retries.
                 Owns nothing about models. Output: add label `ai-implement`.
   │
   ▼
agent-workflow   agent.yml → classify-task.sh picks agent:*/model:*
                 → Claude Code or opencode/OpenRouter → draft PR → quality gates
                 → on failure, writes back `failed:<bucket>`
   │
   ▼
OPERATOR         /gh:prs → /gh:review → merge
```

The seam that matters: **bridge schedules, agent-workflow decides how.** Model
policy stays in the repo that owns the model-comparison evidence, so a new benchmark
round never requires redeploying bridge. Upgrading `classify-task.sh` to its designed
Haiku classifier then improves every path at once, including manual `/gh:implement`.

### Eligibility

```
activeMilestone(repo) := open milestone with the earliest due date, else nil

eligible(issue) =
     !hasLabel("needs-enrichment")
  && !hasLabel("🧊 parked")
  && !hasOpenPR(issue)
  && attempts(issue) < 2
  && (activeMilestone(repo) == nil || issue.milestone == activeMilestone(repo))
```

`hasOpenPR(issue)` means an open PR whose body closes the issue (`Closes #N`), which is
what the pipeline already writes. `repoOpenAgentPRs` counts only those, so a
hand-written PR never consumes a dispatch slot.

Running `/enrich` is the approval. There is no second gesture and no extra label to
forget. Milestones are an **optional narrowing device**: a repo with an active
milestone dispatches only from that milestone; a repo without one dispatches from its
whole enriched backlog. "Make this milestone active" is therefore just setting a due
date — no new state, no bridge UI required.

The nightly cadence gives this a useful property: everything enriched during the day
has a multi-hour window in which it can still be parked before 22:00.

### Ordering

Deterministic ladder, mirroring the existing `/triage` convention so the dispatcher
never ranks work differently than the operator would:

1. milestone due date (soonest first; no milestone sorts last)
2. type — bug/fix > feat > chore/docs
3. size — from a `size:s|m|l` label if present, otherwise treated as `m`
4. age — oldest first, so nothing starves

No LLM, no configuration, fully explainable in `--dry-run` output.

### Guards

Two independent axes. Milestones pick the *pool*; caps limit *concurrency*.

```
dispatchable(issue) = eligible(issue)
  && repoOpenAgentPRs(repo) < per_repo
  && globalOpenAgentPRs()   < global
  && dispatchedThisNight    < max_dispatches_per_night
```

`global` is the operator's review capacity expressed as a number and is the cap that
protects their mornings. `per_repo` prevents conflicting concurrent PRs in one repo.
`max_dispatches_per_night` is a second, independent bound on unattended spend: the
WIP cap alone cannot stop a pathological retry loop between 22:00 and 06:00.

Operator absence needs no special handling. If nobody merges, the global cap fills and
the dispatcher self-throttles.

### Schedule

| Tick | Behaviour |
|---|---|
| 22:00 | Dispatch tick — new work, up to all caps |
| 23:00–06:00, hourly | Retry ticks — advance in-flight issues only, never dispatch new work |
| 06:00–22:00 | Quiet. `bridge dispatch now` is the manual daytime path |

Retry ticks deliberately cannot introduce new work, which keeps the size of the
morning review batch predictable.

An earlier draft made the schedule presence-aware via bridge's existing
`presence` state. That was cut: under a night schedule the clock is already the
presence signal, and the global cap already handles extended absence.

### Failure handling

A failed run frequently produces **no PR at all**. Because WIP is counted in open
agent PRs, the slot would free, the issue would still be eligible, and the next tick
would re-dispatch the same issue indefinitely, burning money silently. The attempt
budget closes this hole.

The pipeline writes its failure bucket back to the issue as `failed:<bucket>`
(`classify-failure` in agent-workflow already produces these buckets).

| Bucket class | Buckets | Dispatcher behaviour |
|---|---|---|
| Transient | `api_auth`, `rate_limit`, `infra` | Retry on the next retry tick with backoff. Does **not** increment `attempt:N`. |
| Substantive | `max_turns`, `gate_failed`, `no_diff` | Increment `attempt:N`. At 2, add `🧊 parked` plus a comment explaining why. |

Parked issues surface in the next morning's `/triage`. Nothing loops, and nothing
fails silently.

### State

Durable state lives in the forge as labels — visible in the web UI, survivable across
a cache wipe, and overridable by hand.

| State | Location |
|---|---|
| Eligibility | Forge labels: `needs-enrichment`, `🧊 parked` |
| Attempt count | Forge label `attempt:N` |
| Failure bucket | Forge label `failed:<bucket>`, written by the pipeline |
| Pause flag, last tick, per-night counter | `~/.cache/bridge/dispatch.json` |
| Limits, overrides, schedule | `~/.config/bridge/dispatch.json` |

`dispatch.json` is a deliberate departure from bridge's "discovery is purely
path-pattern based — no sidecar config" rule. It is justified because limits encode
operator policy that cannot be derived from repo paths. It is the only exception; repo
discovery stays path-based.

JSON rather than TOML: bridge has no TOML dependency, and every other file it writes
(`slots.json`, `presence.json`, `sync.json`) is JSON via `store.AtomicWrite`. The
config format is not worth a new module dependency.

```json
{
  "limits": {
    "global_open_prs": 3,
    "per_repo": 1,
    "max_dispatches_per_night": 5,
    "overrides": { "quotes": 2 }
  },
  "schedule": {
    "dispatch_at": "22:00",
    "retry_until": "06:00"
  }
}
```

### CLI surface

```
bridge dispatch --dry-run   # run eligibility + ordering + caps, change nothing
bridge dispatch now         # one dispatch tick, manual
bridge dispatch --auto      # systemd timer entry point
bridge dispatch pause       # kill switch, persisted
bridge dispatch resume
bridge dispatch status      # caps, in-flight, last tick, next tick
```

Every read command supports `--json`, consistent with `docs/cli-json-schema.md`.

`--dry-run` output:

```
bridge dispatch --dry-run

  quotes      #41  feat: authors filter   → dispatch
  flowhub     #88  fix: classifier retry  → dispatch
  bridge      #35  refactor: nav split    → SKIP (repo at WIP 1/1)
  agent-wf   #112  —                      → SKIP (blocked: needs-enrichment)

2 dispatched, 2 skipped
```

### Implementation shape

New package `internal/dispatch`, reusing `internal/forge` (issues, PRs, milestones — a
milestone read is the only new forge capability required) and `internal/store` (atomic
write, flock). Commands in `cmd/bridge/dispatch.go` via cobra, following existing
conventions including legacy-flag rewriting and `--json`.

Pure decision logic — `eligible()`, `order()`, `applyCaps()` — is separated from all
I/O so it is table-testable without a network.

## Testing

- **Unit** — `eligible()`, `order()` and cap arithmetic as pure functions over fixture
  issue/PR/milestone structs. Table-driven, no network. The bulk of coverage lives here.
- **Integration** — `internal/forge` against recorded HTTP fixtures, asserting the exact
  set of `add-label` calls produced by a tick.
- **Failure paths** — one fixture per failure bucket, asserting transient buckets do not
  increment `attempt:N` and that the second substantive failure parks the issue.
- **Manual gate** — `--dry-run` only for one week before the timer is enabled. An
  autonomous loop that spends money is observed before it is trusted.

## Out of scope for v1

- **Forgejo dispatch.** `ai-implement` runs on GitHub Actions; Forgejo repos have no
  pipeline to dispatch to. Bridge continues to *display* Forgejo issues.
- **Notification / remote-control channel** (Telegram, Slack) — subsystem 2. Partial
  prior art exists in `docs/specs/2026-05-24-bridge-telegram-bot-design.md`.
- **Milestone editing in bridge** — subsystem 3. Milestones are set in the forge UI.
- **Any LLM call inside bridge.** Classification stays in the pipeline.
- **Escalation retries** (retry a failed cheap model with a stronger one). Plausible
  later; excluded now because it can quietly turn a $0.01 issue into a $2 one.

## Resulting operating model

| When | Where | What |
|---|---|---|
| Morning, ~45 min | Desk | `/gh:prs` → `/gh:review` → merge. Triage anything parked overnight. |
| Day / evening, ~45 min | Desk | `/triage` + `/enrich` — loads tonight's queue. |
| 22:00–06:00 | Unattended | Dispatch batch + retry ticks. |
| Anywhere | Phone | Capture into FlowHub. |

The cycle is one legible 24 hours: enriched today → built tonight → reviewed tomorrow
morning. Human time concentrates in enrichment (which determines PR quality) and
review (which is adversarial and deserves a fresh brain). Dispatch cost approaches
zero: the operator approves policy, not individual dispatches.
