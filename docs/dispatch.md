# bridge dispatch

A scheduled decision engine that selects enriched issues for the agent-workflow pipeline.

**Design docs:** [docs/specs/2026-07-27-bridge-dispatcher-design.md](docs/specs/2026-07-27-bridge-dispatcher-design.md), [docs/specs/2026-09-07-dispatch-usage-budget-design.md](docs/specs/2026-09-07-dispatch-usage-budget-design.md)

## What it does

`bridge dispatch` is the boundary between bridge and agent-workflow:
- **bridge** owns the eligibility rules, dispatch caps, and ordering ladder.
- **agent-workflow** owns model selection, the run pipeline, and per-model retry ticks.

Each tick, dispatch reads open issues from every GitHub repo, applies eligibility filters, sorts by repo priority/deadline/type/size/age, applies the usage-budget rung plus per-repo and global WIP caps, and labels the selected issues with `ai-implement` to let the pipeline pick them up. The dispatcher runs on an hourly systemd timer, checks the current time against `schedule.windows`, and no-ops outside every configured window — or runs manually via `bridge dispatch now`, which is not window-gated. Runs are **dry-run only** during the first week — the timer is not enabled until the decisions look right.

## Schedule windows

`schedule.windows` in `dispatch.json` is the single source of truth for when dispatch acts — the systemd timer is a bare hourly heartbeat with no schedule of its own (see "Systemd timer and service" below). Each window is `{"from": "HH:MM", "to": "HH:MM", "budget_rung": bool}`:

- `from` is inclusive, `to` is exclusive.
- `from > to` wraps past midnight (e.g. `18:00`–`07:00` covers the overnight span).
- `budget_rung` turns the usage-budget rung on for ticks that fall in that window.

`--auto` (the systemd entry point) returns before any repo fetch when the current time matches no configured window. An explicit `bridge dispatch now` is the operator asking for a tick and is never window-gated, mirroring how `now` already ignores the pause flag.

## Usage-budget rung

Autonomous dispatch and interactive work (spec writing, `/enrich`, issue triage) draw on the same 5-hour rolling Claude subscription quota. The usage-budget rung reserves headroom for the operator during the day so an unattended run can't eat the window needed for the next spec.

It measures combined trailing-window consumption from two local sources:
- **Interactive** — Claude Code transcripts under `~/.claude/projects/**/*.jsonl`, priced by a per-model rate table (four terms: input, output, cache read, cache write).
- **Pipeline** — a local ledger of runs `bridge dispatch` itself dispatched, each priced at a calibrated `mean_run_cost_usd`.

The rung runs **ahead of** the nightly/global/per-repo caps in `ApplyCaps` — it protects the operator, not the machine, so once the window is spent nothing else about a candidate matters. The projected cost of a candidate run accumulates within a single tick, so a tick with headroom for two runs cannot slip a third through.

### When the rung applies — windows *and* their shoulder

The rung is armed in two situations, both derived from `schedule.windows`:

- **Inside a `budget_rung: true` window.** The measured span is the plain trailing quota window, `[now - window_hours, now]`.
- **Inside the `window_hours` immediately before such a window starts** — its *shoulder*. The measured span ends at the coming handover instead: `[window_start - window_hours, now]`.

The shoulder exists because the subscription quota window is **rolling**, not reset at a fixed hour. Spend at 05:00 is still inside the window at 07:00, so an unattended run late in the night hands the operator a window that is already spent — exactly the headroom the rung is meant to reserve. Measuring against the handover means only the spend that *survives* to 07:00 counts, so the early night stays unbounded while the last `window_hours` are budgeted against the operator's morning.

With the default 5h window and a 07:00 day start: a 01:00 run is unguarded (it ages out by 06:00), a 05:00 run is guarded (it is still counted at 07:00).

Outside both — genuinely deep in the night, or in a schedule gap — the rung is off and usage is not measured. A configuration with no `budget_rung: true` window anywhere therefore disables the rung entirely; that is a deliberate opt-out, not a failure.

### Window configuration footguns

The defaults tile the whole day, so none of these arise unless you hand-write `schedule.windows`. All four are consequences of the rules above rather than bugs, but they are easy to trip:

- **A gap outside any shoulder has *neither* bound** — but only for a manual tick. The rung is off (nothing to guard within reach) and the nightly cap is off (no covering window), so a `bridge dispatch now` there is limited only by the global and per-repo WIP caps. An `--auto` tick never gets that far: it returns with "outside dispatch window" before any cap is consulted. So the exposure is a hand-run command in a gap, not the timer.
- **`from == to` covers the whole day**, not zero minutes. `{"from":"07:00","to":"07:00"}` is an always-on window — the opposite of what the `[from, to)` rule suggests at a glance.
- **Splitting the night into two `budget_rung: false` windows doubles the nightly cap.** The counter resets at each window occurrence's own start, so `18:00`–`22:00` plus `22:00`–`07:00` gives `max_dispatches_per_night` twice per night, once per window.
- **Window order matters when windows overlap.** `InWindow` takes the *first* match, so listing a non-rung window ahead of an overlapping rung window makes the rung window unreachable — and `RungGuard` then rolls forward to the next day's start, leaving the rung off for hours.

**Fail closed.** If usage cannot be measured (unreadable transcripts, a corrupt ledger, or nonsensical budget config), every candidate on an active-rung window is refused with `budget-unknown` — unreadable usage is never treated as zero used.

Skip reasons surfaced by `--dry-run` and `--json`:
- `budget-exhausted <used>/<limit> USD` — the candidate's projected cost would cross the line
- `budget-unknown` — usage could not be measured while the rung is active

## Eligibility

An issue is eligible for dispatch if it passes these checks in order:

1. **Not needs-enrichment** — The issue must NOT be labeled `needs-enrichment` (i.e. it must already have a clear task description or acceptance criteria). Issues carrying this label are skipped until enriched.
2. **Not parked** — The issue must not be labeled `🧊 parked`. Parked issues are skipped and must be manually unparked by removing the label.
3. **Not already dispatched** — The issue must not already carry the `ai-implement` label. This guards against re-labeling/re-commenting an issue on every tick when a prior dispatch failed without producing a PR (see "When a run fails" below) — the open-PR check alone can't catch that case, since no PR exists.
4. **Attempt budget** — The issue must not have an `attempt:N` label with N ≥ 2. A failed run increments the attempt counter; after 2 failed runs, the issue is parked and skipped.
5. **No open PR** — The issue must not have an open pull request that closes it (detected by matching closing keywords in the PR body: "close", "closes", "closed", "fix", "fixes", "fixed", "resolve", "resolves", "resolved"). A hand-written PR never consumes a dispatch slot.
6. **Milestone membership** — If the repo has an open milestone with a due date, the issue must belong to that milestone. Undated milestones are treated as inactive (setting a due date is how the operator marks a milestone active for dispatch).

The first failure reason is returned; dry-run uses this to explain every skip.

## Priority

Issues are sorted by a five-rung ladder before applying caps:

1. **Repo priority** — If `repo_priority` is configured, a repo's rank is the index of the first pattern it matches (Go `path.Match` glob syntax — literal names match exactly; entries containing `*`, `?`, `[...]` match as patterns), scanned in list order. Repos matching no pattern sort after every configured entry. If `repo_priority` is unset or empty, this rung is skipped and ordering falls straight through to milestone due date, identical to the pre-existing behavior.
2. **Milestone due date** — Issues in milestones with earlier due dates sort first. Issues with no active milestone sort last.
3. **Type** — Bug/fix issues (labels `bug` or `fix`, case-insensitive) sort first (rank 0), then feature issues (label `feat`, rank 1), then everything else (rank 2).
4. **Size** — Issues labeled `size:s` sort first (rank 0), then `size:m` (rank 1), then `size:l` (rank 2). Unlabeled issues default to rank 1 (medium).
5. **Age** — Older issues (earlier creation date) sort first within the same size bucket.

The sort is stable: equal-rank issues retain their input order.

## Caps

Four independent bounds limit dispatch, checked in this order:

1. **Usage-budget rung** — See "Usage-budget rung" above. Active inside a `budget_rung: true` window and in the `window_hours` shoulder before it; refuses with `budget-exhausted`/`budget-unknown`.
2. **Nightly cap** — Bounds unattended spend. Prevents a spike of dispatches with no human oversight. Default: 5 dispatches. It applies **only while the covering window has `budget_rung: false`** — during a rung window the budget itself is the bound, and applying both would refuse daytime work using the night's spent counter. The counter resets at the start of the covering window's own occurrence (for `18:00`–`07:00`, the 18:00 boundary), so a 02:00 retry still belongs to the evening that preceded it.
3. **Global open-PR cap** — Limits the operator's review capacity across all repos. Default: 3 open agent PRs total. Once reached, no further dispatch until some close.
4. **Per-repo WIP cap** — Prevents conflicting concurrent PRs in one repo by limiting open agent PRs per repo. Default: 1 per repo. Configured per-repo via overrides in `dispatch.json`. Example: `"overrides": {"quotes": 2}` allows 2 concurrent PRs in the `quotes` repo.

All four must pass before an issue is dispatched. Dry-run shows which bound (if any) caused a skip (the first one that was exceeded in the order above).

## Labels

Bridge dispatch checks and creates these labels:

| Label | Meaning | Set by |
|---|---|---|
| `needs-enrichment` | Issue lacks clear task description; skip until enriched | Manual (operator) |
| `🧊 parked` | Issue exhausted the attempt budget; skip until manually unparked | Agent-workflow / future retry-tick component |
| `ai-implement` | Selected for dispatch; ready for the pipeline | Bridge dispatch (on selection) |
| `attempt:1`, `attempt:2` | Attempt count; incremented after each failed substantive run | Agent-workflow / future retry-tick component |
| `failed:api_auth` | Last run failed due to GitHub API auth error | Agent-workflow pipeline |
| `failed:rate_limit` | Last run failed due to GitHub API rate limit | Agent-workflow pipeline |
| `failed:infra` | Last run failed due to infrastructure error | Agent-workflow pipeline |
| `failed:max_turns` | Last run hit the max-turn limit without producing a PR | Agent-workflow pipeline |
| `failed:gate_failed` | Last run produced code but failed a pre-merge gate | Agent-workflow pipeline |
| `failed:no_diff` | Last run produced no code changes | Agent-workflow pipeline |
| `size:s`, `size:m`, `size:l` | Issue complexity estimate (small, medium, large) | Manual (operator) |

## When a run fails

When the agent-workflow pipeline labels an issue with `failed:<bucket>`, the failure is categorized as either **transient** or **substantive**. The distinction determines how a retry-tick component (when built and wired) will handle retries.

### Transient failures (no attempt cost when retried)

Transient failures indicate a temporary environmental issue, not a problem with the issue itself:
- `failed:api_auth` — GitHub API auth failure (bad/expired token, etc.)
- `failed:rate_limit` — GitHub API rate limit hit
- `failed:infra` — Infrastructure error (network timeout, service unavailable, etc.)

When a future retry-tick component is wired, transient failures will be retried immediately without incrementing the attempt counter.

### Substantive failures (count toward the 2-attempt budget)

Substantive failures indicate a real problem with the issue or task definition:
- `failed:max_turns` — Run hit the max-turn limit without producing a PR
- `failed:gate_failed` — Run produced code but failed a pre-merge gate (linting, tests, etc.)
- `failed:no_diff` — Run produced no code changes

When a future retry-tick component is wired, substantive failures will increment the attempt counter. After 2 failed substantive runs, the issue will be labeled `🧊 parked` and skipped by dispatch until manually unparked.

**Status today:** The retry-tick logic is fully specified (`NextAction` in `internal/dispatch/failure.go` computes the label actions and retry decision), but it is not yet wired to an automated systemd timer or a `--retry-only` dispatch mode. For now, retries of any kind require manual `bridge dispatch now` runs.

## Config

Configuration lives at `~/.config/bridge/dispatch.json` (or `$XDG_CONFIG_HOME/bridge/dispatch.json`). A missing file uses built-in defaults. All keys are optional; unset keys keep their defaults.

Example with every key:

```json
{
  "schedule": {
    "windows": [
      {"from": "18:00", "to": "07:00", "budget_rung": false},
      {"from": "07:00", "to": "18:00", "budget_rung": true}
    ]
  },
  "budget": {
    "window_hours": 5,
    "window_budget_usd": 12.0,
    "daytime_cap": 0.80,
    "mean_run_cost_usd": 2.0,
    "pricing": {
      "claude-opus-4-7": {
        "input": 15.0, "output": 75.0,
        "cache_read": 1.5, "cache_write": 18.75
      }
    }
  },
  "limits": {
    "global_open_prs": 3,
    "per_repo": 1,
    "max_dispatches_per_night": 5,
    "overrides": {
      "quotes": 2,
      "otherepo": 1
    }
  },
  "repo_priority": ["agent-workflow", "ai-instructions", "*", "game-*"]
}
```

Rates in `budget.pricing` are USD per million tokens; `pricing` overrides the built-in table per model, and omitted models keep their compiled-in defaults.

| Key | Type | Default | Meaning |
|---|---|---|---|
| `schedule.windows` | array of objects | see example above | Spans of the local day dispatch acts in; `from` inclusive, `to` exclusive, `from > to` wraps past midnight; `budget_rung` turns the usage-budget rung on for that window |
| `budget.window_hours` | float | 5 | Length of the trailing quota window the rung measures |
| `budget.window_budget_usd` | float | 12.0 | Calibrated USD-equivalent cost of a full 5h subscription window — pin this against `/usage` (see "Calibration" below) |
| `budget.daytime_cap` | float | 0.80 | Fraction of `window_budget_usd` the rung allows before refusing; the remainder is reserved for the operator |
| `budget.mean_run_cost_usd` | float | 2.0 | Calibrated per-run cost, used both for the ledger and for projecting a candidate's cost |
| `budget.pricing` | object | {} | Per-model rate overrides (`input`/`output`/`cache_read`/`cache_write`, USD/Mtok); unnamed models keep their built-in defaults |
| `limits.global_open_prs` | int | 3 | Max open agent PRs across all repos before dispatch is blocked |
| `limits.per_repo` | int | 1 | Default max open agent PRs per repo (applies to all repos unless overridden) |
| `limits.max_dispatches_per_night` | int | 5 | Max dispatches per night window to bound unattended spend |
| `limits.overrides` | object | {} | Per-repo overrides (key = bare repo name, value = per-repo WIP cap) |
| `repo_priority` | array of strings | [] (rung skipped) | Ordered list of repo-name patterns (`path.Match` glob syntax); a repo's dispatch priority is the index of the first pattern it matches, scanned in order. Unmatched repos rank after every entry. Empty/absent disables this rung entirely |

**Upgrade note.** The retired `schedule.dispatch_at` / `schedule.retry_until` keys are ignored — `encoding/json` skips unknown fields, and `schedule.windows` falls back to the defaults shown above, so a pre-existing config keeps loading. After upgrading, reinstall the timer for the windows to take effect: `systemctl --user daemon-reload && systemctl --user restart bridge-dispatch.timer`.

## Calibration

`window_budget_usd` and `mean_run_cost_usd` are empirical constants — there is no API reporting the subscription window's remaining headroom, so both are proxies that need pinning against real usage:

1. Run for about a week without enabling the timer (or with it enabled and the rung watched closely).
2. At a few points across a 5h window, compare `bridge dispatch status` against `/usage` in an interactive session, and adjust `window_budget_usd` so the rung's reported utilization tracks `/usage`.
3. After a handful of real pipeline runs, compare their `**Cost:**` figures in the run-report comments against `mean_run_cost_usd` and adjust.

## Known approximations

These are inherent to a proxy measurement, not bugs:

- **The rolling 5h window is not aligned to the actual subscription reset.** It is a moving lookback approximating a fixed-boundary quota, so it can be conservative near a real reset.
- **A run's cost is booked at dispatch time**, though the run burns quota over the following minutes. This errs toward blocking, the safe direction.
- **Hand-labelled `ai-implement` runs and pipeline retries are not counted** in the ledger — only runs `bridge dispatch` itself applied the label to. The operator doing that by hand is present and aware.

## Running it

### Flags

- `bridge dispatch` — Default: **applies decisions for real** (adds `ai-implement` labels and comments to GitHub). Use only after validation. To preview without side effects, use `--dry-run`.
- `bridge dispatch --dry-run` — Dry-run mode: decide which issues to dispatch, print decisions, change nothing. Used for testing and the first week of validation. **This flag must be passed explicitly to preview safely.**
- `bridge dispatch --json` — Machine-readable output: one JSON object per decision with repo, issue number, title, dispatch decision, and skip reason. Works with or without `--dry-run`.

### Subcommands

- `bridge dispatch now` — Run one dispatch tick immediately, apply decisions (not dry-run). Honors the pause flag only if `--auto` is set; explicit `now` always runs.
- `bridge dispatch pause` — Stop the dispatcher. Sets a local pause flag that `--auto` checks before each tick. Manual `dispatch now` always runs even when paused.
- `bridge dispatch resume` — Resume the dispatcher. Clears the local pause flag.
- `bridge dispatch status` — Show configured caps, dispatches this night, trailing-window usage/limit/utilization, and last tick time. Makes no network call (in-flight PR counts require a repo fetch, out of scope for v1; usage comes from local transcripts and the local ledger).

### Systemd timer and service

`docs/systemd/bridge-dispatch.service` and `docs/systemd/bridge-dispatch.timer` are provided. The timer is a bare hourly heartbeat — `bridge dispatch --auto` fires every hour on the hour, checks the current time against `schedule.windows`, and returns before any repo fetch on a tick outside every window. The windows in `dispatch.json`, not the timer, are the schedule.

**Every in-window tick — day or night — runs the same full dispatch path, bounded by the same usage-budget/nightly/global/per-repo bounds** (the budget rung only ever applies on windows with `budget_rung: true`, see "Usage-budget rung" above). There is no separate retry-only mode yet (see "When a run fails" above) — a dedicated `--retry-only` mode is still future work. What keeps repeated ticks from re-labeling and re-commenting an issue that already failed without producing a PR is the "not already dispatched" eligibility guard (an issue already carrying `ai-implement` is skipped), not a retry-specific code path.

The service requires a GitHub token in its environment. Systemd user units do **not** inherit your shell/direnv env, so create `~/.config/bridge/dispatch.env` (referenced by the service's `EnvironmentFile=`) containing at least:
```
GH_TOKEN=ghp_your_token_here
```
Without this file, `clientFor` resolves no GitHub client, every repo is silently skipped, and the tick reports "0 dispatched, 0 skipped" with exit 0 — a green timer doing nothing. Check `journalctl --user -u bridge-dispatch` for a `no GitHub client available` warning if dispatch looks inert.

To install:
```bash
cp docs/systemd/bridge-dispatch.* ~/.config/systemd/user/
systemctl --user daemon-reload
systemctl --user enable --now bridge-dispatch.timer
```

To check status:
```bash
systemctl --user status bridge-dispatch.timer
systemctl --user status bridge-dispatch.service
journalctl --user -u bridge-dispatch -n 20
```

## First week

**Do not enable the timer during the first week.** Instead:

1. Run `bridge dispatch --dry-run` by hand every few hours and review the decisions.
2. Adjust the config (`~/.config/bridge/dispatch.json`) as needed:
   - Raise/lower caps if decisions look too aggressive or too conservative.
   - Add repo overrides if certain repos need higher WIP limits.
   - Adjust `schedule.windows` if the day/night boundaries don't fit your schedule.
3. Once decisions look right after a few days of dry-runs, enable the timer: `systemctl --user enable --now bridge-dispatch.timer`.

Dry-run changes nothing in the forge, so there is no risk. The first real dispatch run is a deliberate choice after validation.
