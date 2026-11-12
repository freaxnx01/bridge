# Cockpit (WebUI), the Inbox and the Pipeline board

The cockpit is the Svelte page served by `bridge serve` (port 7777). It opens on
the **Inbox**: the issues that need a person, with everything that is merely
running or retrying as a one-line status. The **Pipeline board** — what is
running, what is ready to dispatch, what is held, and what still needs
enrichment, across all local clones — is the second area, reachable from the
header, which carries the Inbox card count as a badge. Design:
[`docs/superpowers/specs/2026-10-24-bridge-cockpit-design.md`](superpowers/specs/2026-10-24-bridge-cockpit-design.md)
and [`2026-11-11-issue-inbox-design.md`](superpowers/specs/2026-11-11-issue-inbox-design.md),
wireframes and flows: [`docs/design/cockpit/`](design/cockpit/).

## Inbox

The unit of work is the **issue**. Since the pipeline posts a run marker on every
run (agent-workflow ADR-021), bridge has one answer to "does this issue need a
person?": `dispatch.NextAction` over the issue's latest marker. The Inbox is that
answer on a page. It is read-only — Retry, Retry with and Park are a follow-up.

### Candidates

An open issue is an inbox candidate when its repo is a **wired GitHub repo**
(the board's `Wired` rule) and it carries any of `ai-implement`, `ai:failed`,
`ai:partial`, `ai:done`, `ai:paused`, `ai:review-blocked`, `ai:review-failed`,
`ai:runner-blocked`, `ai:checks-blocked`, `parked`, `🧊 parked`. Anything else
can only be `ActionNone`, so it is skipped without reading its comments. Forgejo
and Azure DevOps have no run marker and never appear.

### Where an issue lands

| `NextAction` | Where |
|---|---|
| `ActionHuman`, reason `parked` | **Parked**, a folded group |
| `ActionHuman`, any other reason | **Cards**, kind `human` (●) |
| `ActionRetry` | **Cards**, kind `retry` (↻) |
| `ActionWait` | **Running & waiting**, one line each |
| `ActionNone` | not shown |

The reasons are `Action.Reason` verbatim; the row-by-row mapping that produces
them is in [`dispatch.md`](dispatch.md) ("When a run fails").

Cards sort by urgency group, oldest first within a group:

1. Work exists and waits for review: `review blocked`, `review failed`, `checks blocked`, `partial`.
2. A run stopped: every other `human` reason except group 4.
3. `retry` cards.
4. No information: `no run marker`, `retry never reported`.

A card shows `repo#n`, the title, the reason, and — when the issue has a marker
— `agent · model · cost · #attempt`, plus Issue / PR / Run links. Its age is the
marker's `posted`, or the issue's `updated` when there is none. Tapping a card or
a waiting line opens the issue detail: every trusted marker on the issue, newest
first, read live unless the collector already read that thread in the last
minute. Esc or ← closes it.

### Freshness

Comments are read only for candidates, and only when the issue's `updated` has
moved since the last read — a new comment moves it, so an unchanged issue costs
no call. A failed read keeps the stale thread (the card stays) and shows a
`Could not read comments: …` line; the next tick retries it. When the runs tick
sees a run that was in flight for a candidate finish, it re-reads that repo right
away, so a fresh report shows within about a minute instead of at the next
10-minute issues tick.

## Board columns

| Column | Contents |
|---|---|
| Running | Pipeline workflow runs (queued / in progress), issues labelled `enrichment-ongoing`, and live agent sessions. The count covers active work only: idle sessions fold into **Idle sessions**, and an `enrichment-ongoing` issue unchanged for 6 h+ folds into **Stuck enriching** (a label a finished run never cleared). A pipeline run shows its runtime in minutes, orange from 30 min, red from 60 min |
| Ready | Issues that pass `dispatch.Eligible`, in dispatch order |
| Held | Otherwise eligible issues blocked by `outside active milestone` or `open PR` |
| Needs enrichment | Issues labelled `needs-enrichment` or `❓ to-be-defined` |

"Ready" is exactly `dispatch.Eligible`. Since the dispatch change in this release
it also requires an `## Implementation Plan` heading in the issue body (see
[`dispatch.md`](dispatch.md)). Ready/Held are only computed for GitHub repos that
carry `.github/workflows/agent.yml` or `agent-implement.yml` ("wired").

## Labels read

Classification is first match wins:

1. `parked` (legacy `🧊 parked`) or `roadmap`: ignored.
2. `enrichment-ongoing`: Running (kind `enrichment`).
3. `needs-enrichment` or `❓ to-be-defined`: Needs enrichment.
4. `ai-implement`: already dispatched, not a candidate (its workflow run shows under Running).
5. Otherwise, on a wired GitHub repo: `dispatch.Eligible` decides. Eligible: Ready. Rejected with `outside active milestone` or `open PR`: Held. Rejected with `no implementation plan` or `empty body`: Needs enrichment, with that reason shown as a badge on the card (these can be enriched; Enrich accepts them). Any other rejection is dropped. On unwired or non-GitHub repos such issues are dropped.

## Refresh intervals and rate limits

| Source | Interval |
|---|---|
| issues | 10 min |
| runs | 60 s |
| sessions | 5 s |

Each source keeps its last good snapshot; a failing source is listed in `errors`
while the old data stays on the board. To spare the GitHub rate limit:

- PRs and milestones are fetched only for wired repos that have at least one gate candidate (an issue not carrying any of the labels above).
- Workflow runs are fetched only for repos that have an `ai-implement` issue.
- The issue detail re-reads a thread at most once a minute; inside that window
  it is answered from the collector's comment cache. The endpoint is open, so
  without the window any page the operator visits could loop it and exhaust the
  token's hourly REST quota.

## API

All JSON. `GET` is open (LAN), `POST` is guarded (see Auth).

### `GET /api/pipeline`

```json
{
  "running": [{"kind": "pipeline|enrichment|session", "repo": "owner/name", "issue": 12,
               "title": "", "url": "", "status": "", "since": "RFC3339",
               "sessionId": "", "cwd": ""}],
  "ready": [IssueCard], "held": [IssueCard], "needsEnrichment": [IssueCard],
  "dispatchPaused": false,
  "collectedAt": {"issues": "RFC3339", "runs": "RFC3339", "sessions": "RFC3339"},
  "intervalsSeconds": {"issues": 600, "runs": 60, "sessions": 5},
  "errors": [{"source": "issues owner/name", "error": "", "since": "RFC3339"}],
  "skipped": [{"forge": "ado", "repos": 10}]
}
```

`IssueCard` is `{forge, repo, number, title, url, labels[], reason?}`; `reason`
is set on Held cards. Empty sections are `[]`, never `null`.

`skipped` lists forges the board has no client for (today: Azure DevOps), one
entry per forge with its repo count, sorted by forge; those repos are not
errors. A GitHub or Forgejo repo without a token still shows under `errors`. `issue`, `url`,
`sessionId` and `cwd` are omitted when empty.

### `GET /api/inbox`

Open and read-only, like `GET /api/pipeline`.

```json
{
  "cards": [{"repo": "owner/name", "number": 412, "title": "", "url": "",
             "kind": "human|retry|wait|parked", "reason": "review blocked",
             "since": "RFC3339",
             "run": {"attempt": 2, "counts": true, "retryAttempt": 0,
                     "class": "task_failure", "decision": "stop", "status": "ai:failed",
                     "subtype": "", "agent": "claude", "model": "opus-5", "turns": 71,
                     "costUsd": 3.1, "pr": 415, "runUrl": "", "posted": "RFC3339"}}],
  "waiting": [InboxCard], "parked": [InboxCard],
  "collectedAt": "RFC3339",
  "errors": {"comments owner/name#5": "…"}
}
```

`run` is omitted when the issue has no readable marker. Empty sections are `[]`,
never `null`; `errors` is omitted when empty. `collectedAt` is the issues tick's.

### `GET /api/inbox/{owner}/{repo}/{number}`

One issue plus its full run history, newest first. The issue must be one of the
collector's inbox candidates — the endpoint is open, so it reads nothing the
inbox does not already list. The thread is read live unless it was read less
than a minute ago (see Refresh intervals and rate limits):

```json
{"issue": {"repo": "owner/name", "number": 412, "title": "", "url": "", "labels": []},
 "runs": [RunRecord]}
```

| Status | Meaning |
|---|---|
| 400 | `number` is not a positive integer |
| 404 | Malformed path, the repo is not a wired GitHub repo the collector knows, or the issue is not one of its inbox candidates |
| 405 | Wrong method (`Allow: GET`) |
| 502 | The forge call failed; the detail is in the server log, never in the body |

### `POST /api/pipeline/refresh`

Triggers a background re-collect of all three sources. Returns `202` with no body.

### `POST /api/pipeline/enrich` and `POST /api/pipeline/dispatch`

Body (max 4 KiB): `{"repo": "owner/name", "number": 12}`. Success (`200`):
`{"slot": "...", "alreadyRunning": false, "rcUrl": "..."}` (`rcUrl` omitted when
absent). The issue must currently be on the board: in Needs enrichment for
enrich, in Ready for dispatch.

| Status | Meaning |
|---|---|
| 400 | Invalid JSON, `number` not positive, or `repo` not `owner/name` |
| 401 | Missing or wrong bearer token |
| 403 | Origin missing or not allowed |
| 405 | Wrong method (`Allow` header set) |
| 409 | `not on the board` (also for an unknown repo), `ambiguous repo name` (two clones share the basename), or `launch already in progress` for the same repo and number |
| 422 | Enrich on a non-GitHub issue (headless enrich is GitHub-only) |
| 503 | `BRIDGE_API_TOKEN` is not set (writes fail closed) |
| 500 | `launch failed` (details are in the server log) |

### SSE

`GET /api/events` emits `pipeline-updated` whenever the board changes and
`inbox-updated` whenever the inbox changes; the page then re-fetches
`/api/pipeline` or `/api/inbox`. Each event fires only when its own payload
changed, so a board-only change does not wake the inbox and back.

SSE fires only on content changes, so on a quiet board it goes silent. The page
therefore also re-fetches `/api/pipeline` on a timer every `intervalsSeconds.sessions`
seconds (at least 5 s, 5 s before the first load) to keep `collectedAt` current
and avoid false stale warnings. Overlapping collects on the server (ticker vs.
manual refresh) coalesce: a second call while one is running returns at once.
The Inbox page polls `/api/inbox` every 60 s for the same reason.

## What the buttons run

Both start a **paid Claude session** and ask for confirmation first.

- **Enrich:** `bridge launch <name> -w enrich-<N> --agent claude --rc --prompt "/enrich <N> --headless"`
- **Dispatch:** `bridge launch <name> -w dispatch-<N> --agent claude --rc --prompt "/gh:implement <N>"`

(The server also passes `--rc-wait 15s --json` and runs the child with a 60 s
timeout.) `launch --prompt` hands the text to claude as its initial prompt; it is
rejected for any other agent.

## Auth

- Writes need `BRIDGE_API_TOKEN` as `Authorization: Bearer <token>`. If it is unset, the write endpoints answer `503` and do nothing.
- The `Origin` header must be `localhost:<port>`, `127.0.0.1:<port>`, or an entry in `BRIDGE_COCKPIT_ORIGINS` (comma-separated `host[:port]`). The allowlist is explicit and never derived from the request's `Host`, which defends against DNS rebinding.
- `bridge-serve.service` binds `0.0.0.0:7777` because FlowHub needs `/api/capture` over the LAN. Reads are therefore LAN-open, as they were before; writes still need token and origin.
- The page stores the token in the browser after you enter it once with the 🔑 button.

## Known limits

- The board has clients for GitHub and Forgejo only. Clones on any other forge
  (Azure DevOps today) carry no issues on the board; they show as one muted
  line per forge, from `skipped` in `GET /api/pipeline`, and are not errors. A
  GitHub or Forgejo repo whose owner has no token is still an error.
- The first sessions collect takes about 35 s.
- Subagent visibility is not built yet.
- A session card shows an "Open RC" link only when its sessionId looks like `session_…`.
- A run names its issue only by display title, so a **pipeline retry** — a
  `workflow_dispatch` run whose display title is the workflow name — cannot be
  attributed to an issue. Such an issue's latest marker already says
  `decision: retry`, which reads as "pipeline retrying", so the Inbox is still
  right.
- Two open issues with the **same title** in one repo both read as running while
  one run is going, for the same reason.
- Issues whose last run predates agent-workflow v2.2.0 carry no marker and read
  "no run marker" (urgency group 4). There is no fallback parser for the visible
  report: a pre-marker report carries neither class nor decision.
- The Inbox is read-only. Retry, Retry with another agent/model and Park are a
  follow-up (#374), as are blocked interactive sessions (#375).

## Rollout (operator steps)

1. `just build`
2. Add `BRIDGE_COCKPIT_ORIGINS=bridge.home.freaxnx01.ch` to `~/.config/bridge-serve/env` (`BRIDGE_API_TOKEN` is already set there).
3. Restart `bridge-serve.service`.
4. Add the Traefik route `bridge.home.freaxnx01.ch` to `192.168.1.108:7777` behind Authentik forward-auth (`homelab-service-routing` skill).
5. Open the page and enter the token once with the 🔑 button.
6. Click Enrich on one GitHub issue and check that a session card appears under Running.
