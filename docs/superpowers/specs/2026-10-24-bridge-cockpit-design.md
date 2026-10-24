# Bridge Cockpit — WebUI overview across all forges, repos and agents

## Intent

`br nav` is the tool for working **on one repo**. What's missing is the view
**across everything**: all forges, all repos, the agent pipeline, the local
Claude sessions on agent-dev and the infrastructure they use. The cockpit is
that view: a WebUI for bridge, served by `bridge serve` and built only on its
REST API.

The question it must answer within five seconds of opening:

> **What is running right now, which open issues still need enrichment, and
> which are ready to dispatch?**

Everything else is one click away from that.

## Anti-goals (what makes it a failure even if it works)

The user named three. Each is a design constraint, not a nice-to-have:

- **Slow.** The page must render in well under a second, whatever the repo
  count. No request handler may fan out to forge APIs synchronously. (Today
  `GET /api/overview` calls `ListOpenIssues` for every repo in sequence on
  every request, see `fetchAllOpenIssues` in `cmd/bridge/nav.go`. That pattern
  must not be copied.)
- **Stale.** Anything shown as "running" that has finished erodes all trust.
  Every panel shows how old its data is, and the "Running" column is the
  freshest data on the page.
- **Noisy.** The landing view shows what needs attention, not everything that
  exists. Healthy, idle things collapse or hide; detail lives one click deeper.

## Scope of this document

1. **The cockpit as a whole:** shell, navigation, data freshness model,
   security for actions, and the order panels arrive in.
2. **The first slice in detail:** the Pipeline board.
3. **The remaining panels as a roadmap.** Each gets its own short design when
   its turn comes; the notes here are what the user asked for, not a spec.

## Constraints and how it gets made

- **Stack (builder's call, building on what exists):** the existing Svelte 5 +
  Vite app in `web/`, built into `internal/web/dist` and embedded in
  `bridge serve`. No new frontend framework. Go side follows the repo's Go
  overlay (stdlib `net/http` mux, handlers in `internal/api`, wiring in
  `cmd/bridge/serve.go`, hand-rolled fakes in tests).
- **REST only.** The page talks to `/api/*` and the SSE stream `/api/events`
  (already exists, `internal/web/hub.go`). It never shells out or talks to a
  forge itself.
- **Runs as** `bridge-serve.service` on agent-dev (default bind
  `127.0.0.1:7777`). Reaching it from other machines goes through Traefik,
  as already planned in TODO.md.
- **agent-dev memory cap** (12 GB, no swap, see user CLAUDE.md): collectors
  must stay light. No full clones, no per-request process storms.
- UI work follows the repo's mandatory phase order: wireframe (`/ui:brainstorm`)
  → flow (`/ui:flow`) → build → review. This design does not replace those
  gates; it is the input to them.

## Cockpit-wide design

### Data freshness model (answers "slow" and "stale")

- **Background collectors, cached snapshots.** Each data source is refreshed
  by a collector goroutine owned by `bridge serve` on its own interval, writing
  a snapshot in memory. Handlers return the latest snapshot immediately.
- **Every snapshot carries `collectedAt`** (and the error of the last failed
  attempt, if any). The UI shows age per panel ("12 s ago", "4 min ago") and
  turns it into a warning when older than twice its interval.
- **SSE pushes "X changed"** events when a snapshot actually changes (not on a
  blind timer as today), and the page re-fetches that panel.
- **Suggested intervals (builder's call, tune later):**
  - local Claude sessions, local enrich runs: ~5 s (cheap, local)
  - GitHub Actions runs in flight: ~30 s, with ETag / conditional requests
  - issues and PRs: ~2–5 min, plus an immediate refresh of one repo after the
    cockpit itself changes something there
  - repo metadata, branch protection, integration, stats: ~15–60 min
- **Manual refresh** per panel for "I know something just changed".

### Actions and security

The board is not read-only: cards have buttons. Because the page then changes
things:

- **Access (decided):** the cockpit is reachable on the LAN through a Traefik
  route (`*.home.freaxnx01.ch`, see the `homelab-service-routing` skill)
  **behind Authentik forward-auth**. `bridge serve` itself keeps binding
  `127.0.0.1`.
- **Writes** additionally go through endpoints behind the existing
  `requireBearer` (`BRIDGE_API_TOKEN`), like `/api/capture/` and `/api/tools/`
  already do, and the server rejects write requests whose `Origin` isn't the
  cockpit's own. How the browser obtains the token is the builder's call (e.g.
  entered once, kept in `localStorage`).
- Any action that **spends money or starts unattended work** (dispatch,
  enrich) asks for confirmation in the page, naming exactly what will happen.

### Shell and navigation

- Landing page = **Pipeline board** (below).
- Top-level areas, added as their panels arrive: **Pipeline**, **Repos**,
  **Issues & PRs**, **Integration**, **Stats**, **Infrastructure**.
- A header strip with at-a-glance signals (counts, warnings: runner offline,
  a stale panel, a session waiting for input) that link into the area.
- Layout and visual design are decided in the `/ui:brainstorm` phase, not here.

## First slice: the Pipeline board

Three columns.

### Which repos (my call)

All discovered repos (`reposWithMeta()`), but each column only for forges
where it makes sense: the pipeline (Actions runs, `ai-implement`, dispatch)
exists only on GitHub, so **Running pipeline runs** and **Ready to dispatch**
cover GitHub repos with agent-workflow wired up (`.github/workflows/agent.yml`
or `agent-implement.yml`). **Needs enrichment** and **local sessions** cover
every forge.

### 1. Running now

Every unit of work in flight, across all repos. Kinds:

| Kind | Source (to confirm in planning) |
|---|---|
| `ai-implement` pipeline run | GitHub Actions runs of the agent workflows (`agent.yml` / `agent-implement.yml`) in progress, plus the issue/PR they belong to |
| Local enrichment (autopilot lane) | `agent-autopilot` on agent-dev (`~/.claude/scripts/autopilot.sh`; its lock/state) |
| Local enrichment (manual) | the headless `/enrich` sessions the Enrich button starts — they are local Claude sessions, see next row |
| Local Claude session | `/api/agents` (`agentview.List`, `claude agents --json`) — repo, worktree, branch, status |
| Subagents inside a local session | the session transcripts under `~/.claude/projects/` |

Per card: repo, issue/PR, what kind, how long it's been running, and the agent
and model when known. There is no enrich workflow in the pipeline today;
enrichment only runs locally. Pipeline runs are found via the Actions runs of
the agent workflow per wired repo (queued / in progress) and linked to their
issue/PR through the run's branch or PR; a run that can't be linked is shown
as a bare run rather than dropped (builder's call). Actions: **Open** (the Actions run, the PR, or the
session's claude.ai Remote Control page when one exists).

Subagent visibility is the shakiest source here. If transcripts don't give a
reliable "active" signal, ship the board without it and add it later; don't
show guesses as "running".

### 2. Ready to dispatch

Open issues that are enriched and eligible but not yet handed to the pipeline.

**One definition of "ready" (decided):** `internal/dispatch.Eligible` becomes
the single source. It is extended with the checks only `ai-funnel.sh` has
today: an issue labelled `❓ to-be-defined` counts as unenriched, and an issue
without an `## Implementation Plan` section in its body is not ready (the
stale-label case). `bridge dispatch` and the cockpit both use it. This is a
deliberate behaviour change to `bridge dispatch`, not only cockpit work;
`ai-funnel.sh` aligning to it is a follow-up in agent-workflow.

Card shows repo, issue, title, size/type labels, and the reason when
`Eligible` says no, plus a board-level note when dispatch is paused. The
first slice does **not** simulate tonight's run (lanes, caps, budget, window)
— my call; that can come with the Stats/Pipeline follow-ups.

Action: **Dispatch (decided):** the full `/gh:implement` path, not a bare
label — readiness check, post the implementation contract, clear
`needs-enrichment` / `❓ to-be-defined`, add `ai-implement`. One source of
truth for that logic: either the endpoint invokes the existing command
headlessly or the logic moves to Go and the command calls bridge; the plan
picks, but it must not end up duplicated. Confirmation in the page first.

### 3. Needs enrichment

Open issues labelled `needs-enrichment` (minus parked and roadmap issues, as
the existing commands already exclude them).

Action: **Enrich (decided):** starts a detached Claude session via
`bridge launch` running `/enrich --headless` on that one issue. It then
appears in Running now as a local session. A manual click deliberately
bypasses the autopilot allowlist (that gate is for unattended runs).
Confirmation in the page first, naming the repo, issue and that it starts a
paid session.

### Noise rules for the board

- Columns sort by what needs the user first (running: longest/oldest first;
  ready: dispatch order; needs-enrichment: the order `/queue` would give).
- Repos with nothing in any column don't appear at all.

## Roadmap: the other panels

In the user's words, captured here so nothing is lost; each gets its own
design.

- **Repos (all forges):** worktrees, branches, uncommitted changes,
  ahead/behind, recent commit log; whether `main` is **branch-protected**
  (also a warning badge in the overview); CHANGELOG `[Unreleased]` and latest
  release notes. Local git state comes from the clones on agent-dev, collected
  locally.
- **Issues & PRs:** all open issues and PRs across forges, PRs awaiting
  review.
- **Integration:** which repos have ai-instructions and agent-workflow wired
  up, the `agent.yml` settings (agent, model, flow, pinned ref) — and
  **steering** them from the page, not only viewing. Source to evaluate:
  `integration-status.sh --json`.
- **Stats:** what runs on which agent/LLM, ship rate, cost. Source to
  evaluate: `ai-stats.sh --json`, `ai-funnel.sh --json`.
- **Infrastructure:**
  - **GitHub runners:** self-hosted (online/offline, idle/busy, current job)
    and GitHub-hosted (queued/running jobs, Actions minutes used vs. quota).
  - **GPU browsers:** the remote Chromium instances with a CDP relay
    (`:9223`, see game-rhyflitzer #304 / #324): up or down via
    `/json/version`, and who is connected if that's knowable.
- **Remote Control:** open a session's claude.ai RC page from any session card.

## Left to the builder

- Endpoint names and JSON shapes (document them in `docs/cli-json-schema.md`
  style).
- Whether collectors reuse the bash collectors (`--json`) or reimplement in Go
  — prefer Go where `internal/` already has the logic (dispatch, forge
  clients), bash where it doesn't and the script is the maintained source.
- Concrete refresh intervals and rate-limit handling.
- Token entry UX for actions.
- How pipeline runs are linked to issues/PRs, and the API-call budget of the
  ~30 s loop.

## Calls made by the author of this document (not decided by the user)

- Repo/forge scope per column (see "Which repos").
- Skip reason = `Eligible`'s reason + paused flag; no run simulation in slice 1.
- Unlinkable pipeline runs shown as bare runs.
- Collector intervals, and preferring Go collectors where `internal/` has the logic.
- Subagent visibility may ship later if transcripts give no reliable signal.
