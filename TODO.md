# Bridge TODO

## Bridge MCP endpoint (#195, merged 2026-07-12)

- [ ] Cheat sheet for starting/operating `bridge mcp serve` and integrating
      it with Claude Code / Claude Desktop: [`docs/mcp-cheatsheet.md`](docs/mcp-cheatsheet.md)
- [x] Spin up `bridge mcp serve` and test it end to end (real client
      connect, all four tools, read-only + write/confirm paths)
- [ ] `bridge mcp serve` systemd unit for always-on access (same follow-up as
      the WebUI one below — could share a unit-file pattern)
- [ ] MCP test with Claude App

## Bridge WebUI — Plan 2 (Svelte UI Components)

Plan 1 (backend + scaffold) is merged on `main` as of 2026-06-25.
Plan 2 covers the actual Svelte pages and components.

**Required gates before writing any component code:**

- [ ] Run `/ui-brainstorm` → get ASCII wireframes approved for:
  - Overview page (`/`): Radar + Word Cloud viz panel, Tier list, Capture bar
  - Repo dashboard (`/repos/:owner/:name`): Sessions, Worktrees, Branches, Git status, Issues, ArchViz stub
- [ ] Run `/ui-flow` → get Mermaid state diagrams approved (Svelte store states + transitions)
- [ ] Write Plan 2 (`docs/superpowers/plans/YYYY-MM-DD-bridge-webui-plan2.md`)

**Then implement (Plan 2 scope):**

- [ ] Promote `bridge-overview-directions.html` (Radar + Word Cloud) to Svelte components
  - `<Radar {components} {deps} {activeFeature} {agentId} />`
  - `<WordCloud {components} />`
- [ ] Overview page (`web/src/routes/Overview.svelte`) — viz + tier list + capture bar
- [ ] Repo dashboard (`web/src/routes/RepoDashboard.svelte`) — panels + ArchViz stub
- [ ] Shared components: `RepoCard`, `CaptureModal`, `CreateRepoModal`, `SseProvider`
- [ ] Wire Svelte router (page routing for `/` and `/repos/:owner/:name`)

**Follow-ups (deferred from Plan 1):**

- [ ] Extend `ai-instructions` Go stack overlay with a `## Svelte (WebUI)` section
- [ ] Add `just test` recipe to run both `go test -race ./...` and `cd web && npm test -- --run`
- [ ] Clean-arch resolver for real ArchViz data (currently stub)
- [ ] `bridge serve` systemd unit for always-on homelab access
- [ ] Auth via Traefik middleware (BasicAuth / ForwardAuth) — no app changes needed

**Overview/Dashboard ideas (uncommitted, not yet in Plan 2 wireframes):**

- [ ] RC sessions
- [ ] Open Issues (filter label)
- [ ] PRs to review
- [ ] Todos, Ideas
- [ ] GH Actions

## bridge dispatch — manual testing (merged main, 2026-07-28)

Full docs: [`docs/dispatch.md`](docs/dispatch.md). Spec: [`docs/specs/2026-07-27-bridge-dispatcher-design.md`](docs/specs/2026-07-27-bridge-dispatcher-design.md).

**Do this before enabling the systemd timer — dry-run only for the first week.**

- [x] Build and install: `just build` (confirm `bridge dispatch --help` shows
      `now`, `pause`, `resume`, `status` subcommands and `--dry-run`/`--json`/`--auto` flags)
- [x] `bridge dispatch --dry-run` against real repos — confirm it prints a
      decision table (repo, issue #, title, dispatch/SKIP + reason) and a
      summary line, and writes nothing (check no label/comment appeared on
      any real issue afterward)
- [x] `bridge dispatch --dry-run --json` — confirm valid JSON output (this
      exercises the persistent-flag fix; previously `--json` failed with
      "unknown flag" on subcommands)
- [x] `bridge dispatch status` and `bridge dispatch status --json` — confirm
      caps, paused flag, dispatched-tonight count, last tick render correctly
- [x] `bridge dispatch pause` then `bridge dispatch status` — confirm paused:
      true; `bridge dispatch resume` flips it back
- [x] `bridge dispatch now` while paused — confirm it still runs (only
      `--auto` should respect the pause flag)
- [x] Pick one low-stakes real issue, remove `needs-enrichment`, run
      `bridge dispatch now` for real (no `--dry-run`) — confirm exactly the
      `ai-implement` label + a "Dispatched by `bridge dispatch`" comment
      appear, and nothing else (no `agent:*`/`model:*` label)
      — done 2026-07-29 on `freaxnx01/agent-workflow#105`
- [x] Run `bridge dispatch now` again immediately on the same issue — confirm
      it is now skipped as "already dispatched" (the in-flight guard added in
      final review) rather than being re-labeled
      — confirmed: issue drops out of the candidate list entirely once
      `ai-implement` is set, comment count stayed at 1
- [x] Force a repo to its per-repo WIP cap (two eligible issues, `per_repo: 1`
      default) — confirm the second is skipped with `"repo at WIP 1/1"`
      — observed live (not forced): `bridge` repo already at 1/1 blocked #114
- [ ] Set `max_dispatches_per_night` low in `~/.config/bridge/dispatch.json`
      and confirm the nightly cap kicks in with `"night cap N/N"`
- [x] Confirm `~/.config/bridge/dispatch.json` overrides are picked up (e.g.
      a per-repo override) and `~/.cache/bridge/dispatch.json` state persists
      across runs (dispatched-tonight count, last tick)
      — bumped `global_open_prs` to 10 to unblock a live test (cap was
      already exceeded: 5 open agent PRs vs. default cap of 3 — worth a
      look separately), confirmed via `status`, then reverted
- [ ] Only after the above look right: install
      `docs/systemd/bridge-dispatch.service` + `.timer`, create
      `~/.config/bridge/dispatch.env` with `GH_TOKEN=...`, `systemctl --user
      enable --now bridge-dispatch.timer`, and confirm a real 22:00 tick (or
      `systemctl --user start bridge-dispatch.service` to trigger manually)
      dispatches as expected — watch for the new `slog.Warn` if the token
      env file is missing/misconfigured

## Agent / Bot Integration (ideas captured 2026-06-24)

Full spec'd items filed as GitHub issues #163–#171 (bot `/ask`, `/status`, `/plan`,
session summaries, auto-labeling, WebUI panels — see issue tracker for these).

- [ ] Test starting a new Claude Code session via the Telegram bot
- [ ] Add support for Hermes Agent
