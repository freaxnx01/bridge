# bridge dispatch

A scheduled decision engine that selects enriched issues for the agent-workflow pipeline.

**Design doc:** [docs/specs/2026-07-27-bridge-dispatcher-design.md](docs/specs/2026-07-27-bridge-dispatcher-design.md)

## What it does

`bridge dispatch` is the boundary between bridge and agent-workflow:
- **bridge** owns the eligibility rules, dispatch caps, and ordering ladder.
- **agent-workflow** owns model selection, the run pipeline, and per-model retry ticks.

Each tick, dispatch reads open issues from every GitHub repo, applies eligibility filters, sorts by deadline/type/size/age, applies per-repo and global WIP caps, and labels the selected issues with `ai-implement` to let the pipeline pick them up. The dispatcher runs on a systemd timer (22:00 dispatch, hourly retries from 23:00 to 06:00) or manually via `bridge dispatch now`. Runs are **dry-run only** during the first week — the timer is not enabled until the decisions look right.

## Eligibility

An issue is eligible for dispatch if it passes these checks in order:

1. **Not needs-enrichment** — The issue must be labeled `needs-enrichment` if it lacks a clear task description or acceptance criteria. Issues with this label are skipped.
2. **Not parked** — The issue must not be labeled `🧊 parked`. Parked issues are skipped and must be manually unparked by removing the label.
3. **Attempt budget** — The issue must not have an `attempt:N` label with N ≥ 2. A failed run increments the attempt counter; after 2 failed runs, the issue is parked and skipped.
4. **No open PR** — The issue must not have an open pull request that closes it (detected by matching closing keywords in the PR body: "close", "closes", "closed", "fix", "fixes", "fixed", "resolve", "resolves", "resolved"). A hand-written PR never consumes a dispatch slot.
5. **Milestone membership** — If the repo has an open milestone with a due date, the issue must belong to that milestone. Undated milestones are treated as inactive (setting a due date is how the operator marks a milestone active for dispatch).

The first failure reason is returned; dry-run uses this to explain every skip.

## Priority

Issues are sorted by a four-rung ladder before applying caps:

1. **Milestone due date** — Issues in milestones with earlier due dates sort first. Issues with no active milestone sort last.
2. **Type** — Bug/fix issues (labels `bug` or `fix`, case-insensitive) sort first (rank 0), then feature issues (label `feat`, rank 1), then everything else (rank 2).
3. **Size** — Issues labeled `size:s` sort first (rank 0), then `size:m` (rank 1), then `size:l` (rank 2). Unlabeled issues default to rank 1 (medium).
4. **Age** — Older issues (earlier creation date) sort first within the same size bucket.

The sort is stable: equal-rank issues retain their input order.

## Caps

Three independent caps limit dispatch, checked in this order:

1. **Nightly cap** — Bounds unattended spend during non-business hours (22:00–06:00, when the timer runs). Prevents a spike of dispatches with no human oversight. Default: 5 dispatches per night. Resets daily at dispatch time (22:00).
2. **Global open-PR cap** — Limits the operator's review capacity across all repos. Default: 3 open agent PRs total. Once reached, no further dispatch until some close.
3. **Per-repo WIP cap** — Prevents conflicting concurrent PRs in one repo by limiting open agent PRs per repo. Default: 1 per repo. Configured per-repo via overrides in `dispatch.json`. Example: `"overrides": {"quotes": 2}` allows 2 concurrent PRs in the `quotes` repo.

All three must pass before an issue is dispatched. Dry-run shows which cap (if any) caused a skip (the first one that was exceeded in the order above).

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
  "limits": {
    "global_open_prs": 3,
    "per_repo": 1,
    "max_dispatches_per_night": 5,
    "overrides": {
      "quotes": 2,
      "otherepo": 1
    }
  },
  "schedule": {
    "dispatch_at": "22:00",
    "retry_until": "06:00"
  }
}
```

| Key | Type | Default | Meaning |
|---|---|---|---|
| `limits.global_open_prs` | int | 3 | Max open agent PRs across all repos before dispatch is blocked |
| `limits.per_repo` | int | 1 | Default max open agent PRs per repo (applies to all repos unless overridden) |
| `limits.max_dispatches_per_night` | int | 5 | Max dispatches per night window (22:00–06:00) to bound unattended spend |
| `limits.overrides` | object | {} | Per-repo overrides (key = bare repo name, value = per-repo WIP cap) |
| `schedule.dispatch_at` | string | "22:00" | Time of day for the main dispatch tick (HH:MM, 24-hour format) |
| `schedule.retry_until` | string | "06:00" | Last hour to run retry ticks; no ticks after this time |

## Running it

### Flags

- `bridge dispatch` — Default: **applies decisions for real** (adds `ai-implement` labels and comments to GitHub). Use only after validation. To preview without side effects, use `--dry-run`.
- `bridge dispatch --dry-run` — Dry-run mode: decide which issues to dispatch, print decisions, change nothing. Used for testing and the first week of validation. **This flag must be passed explicitly to preview safely.**
- `bridge dispatch --json` — Machine-readable output: one JSON object per decision with repo, issue number, title, dispatch decision, and skip reason. Works with or without `--dry-run`.

### Subcommands

- `bridge dispatch now` — Run one dispatch tick immediately, apply decisions (not dry-run). Honors the pause flag only if `--auto` is set; explicit `now` always runs.
- `bridge dispatch pause` — Stop the dispatcher. Sets a local pause flag that `--auto` checks before each tick. Manual `dispatch now` always runs even when paused.
- `bridge dispatch resume` — Resume the dispatcher. Clears the local pause flag.
- `bridge dispatch status` — Show configured caps, dispatches this night, and last tick time. Reads no network state (in-flight PR counts require a repo fetch, out of scope for v1).

### Systemd timer and service

`docs/systemd/bridge-dispatch.service` and `docs/systemd/bridge-dispatch.timer` are provided. The timer runs `bridge dispatch --auto` at:
- **22:00 (main tick)** — Dispatch new work within caps.
- **23:00, 00:00, 01:00, 02:00, 03:00, 04:00, 05:00, 06:00** — Hourly retries for issues that failed (only if wired; see "When a run fails" above).

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
   - Adjust `dispatch_at` / `retry_until` times if they don't fit your schedule.
3. Once decisions look right after a few days of dry-runs, enable the timer: `systemctl --user enable --now bridge-dispatch.timer`.

Dry-run changes nothing in the forge, so there is no risk. The first real dispatch run is a deliberate choice after validation.
