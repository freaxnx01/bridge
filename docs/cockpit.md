# Cockpit (WebUI) and the Pipeline board

The cockpit is the Svelte page served by `bridge serve` (port 7777). Its first
panel is the **Pipeline board**: what is running, what is ready to dispatch, what
is held, and what still needs enrichment, across all local clones. Design:
[`docs/superpowers/specs/2026-10-24-bridge-cockpit-design.md`](superpowers/specs/2026-10-24-bridge-cockpit-design.md),
wireframes and flows: [`docs/design/cockpit/`](design/cockpit/).

## Board columns

| Column | Contents |
|---|---|
| Running | Pipeline workflow runs (queued / in progress), issues labelled `enrichment-ongoing`, and live agent sessions |
| Ready | Issues that pass `dispatch.Eligible`, in dispatch order |
| Held | Otherwise eligible issues blocked by `outside active milestone`, `attempts exhausted` or `open PR` |
| Needs enrichment | Issues labelled `needs-enrichment` or `❓ to-be-defined` |

"Ready" is exactly `dispatch.Eligible`. Since the dispatch change in this release
it also requires an `## Implementation Plan` heading in the issue body (see
[`dispatch.md`](dispatch.md)). Ready/Held are only computed for GitHub repos that
carry `.github/workflows/agent.yml` or `agent-implement.yml` ("wired").

## Labels read

Classification is first match wins:

1. `🧊 parked` or `roadmap`: ignored.
2. `enrichment-ongoing`: Running (kind `enrichment`).
3. `needs-enrichment` or `❓ to-be-defined`: Needs enrichment.
4. `ai-implement`: already dispatched, not a candidate (its workflow run shows under Running).
5. Otherwise, on a wired GitHub repo: `dispatch.Eligible` decides. Eligible: Ready. Rejected with `outside active milestone`, `attempts exhausted` or `open PR`: Held. Rejected with `no implementation plan` or `empty body`: Needs enrichment, with that reason shown as a badge on the card (these can be enriched; Enrich accepts them). Any other rejection is dropped. On unwired or non-GitHub repos such issues are dropped.

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
  "errors": [{"source": "issues owner/name", "error": "", "since": "RFC3339"}]
}
```

`IssueCard` is `{forge, repo, number, title, url, labels[], reason?}`; `reason`
is set on Held cards. Empty sections are `[]`, never `null`. `issue`, `url`,
`sessionId` and `cwd` are omitted when empty.

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

`GET /api/events` emits `pipeline-updated` whenever a source changes; the page
then re-fetches `/api/pipeline`.

SSE fires only on content changes, so on a quiet board it goes silent. The page
therefore also re-fetches `/api/pipeline` on a timer every `intervalsSeconds.sessions`
seconds (at least 5 s, 5 s before the first load) to keep `collectedAt` current
and avoid false stale warnings. Overlapping collects on the server (ticker vs.
manual refresh) coalesce: a second call while one is running returns at once.

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

- The first sessions collect takes about 35 s.
- Subagent visibility is not built yet.
- A session card shows an "Open RC" link only when its sessionId looks like `session_…`.

## Rollout (operator steps)

1. `just build`
2. Add `BRIDGE_COCKPIT_ORIGINS=bridge.home.freaxnx01.ch` to `~/.config/bridge-serve/env` (`BRIDGE_API_TOKEN` is already set there).
3. Restart `bridge-serve.service`.
4. Add the Traefik route `bridge.home.freaxnx01.ch` to `192.168.1.108:7777` behind Authentik forward-auth (`homelab-service-routing` skill).
5. Open the page and enter the token once with the 🔑 button.
6. Click Enrich on one GitHub issue and check that a session card appears under Running.
