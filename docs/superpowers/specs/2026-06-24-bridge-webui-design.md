# Bridge WebUI — Design Spec

**Date:** 2026-06-24
**Roadmap items:** #3 REST API + #4 WebUI with Visualization (designed together)
**Status:** draft

---

## Overview

Bridge WebUI is a browser surface for the bridge personal dev cockpit, serving the same user as `bridge nav` but in a browser (PC + mobile via homelab Traefik). It replaces nothing — the TUI stays for terminal/agent-dev contexts where worktrees and local-only state matter; the WebUI adds a richer, remote-accessible surface.

A new `bridge serve` subcommand starts a single Go HTTP server that serves both a compiled Svelte SPA (embedded in the binary) and a REST API backed by the existing `internal/` packages. Live updates flow via SSE.

Visual PoCs already exist in `docs/design/`:
- `bridge-poc2.html` — per-repo architecture visualizer (Onion/Layer views, blips, dep edges, drill-down)
- `bridge-overview-directions.html` — cross-repo overview (Radar + Word Cloud)

---

## Architecture

### Single binary, embedded SPA

```
bridge serve [--port 7777] [--host 127.0.0.1]
```

- Go HTTP server serves `/api/...` routes and static files from `web/dist/` embedded via `//go:embed web/dist`
- Dev mode: Vite dev server on a separate port, proxies `/api` to the Go server (standard Vite pattern, no CORS config)
- Prod: `just build` runs `web-build` (npm ci + npm run build) then `go build` with ldflags — compiled Svelte assets baked into the binary

### Authentication

None for now. Add Traefik middleware (BasicAuth or ForwardAuth) later without touching the app.

### Directory layout

```
web/                        ← Svelte/Vite project
  src/
    lib/
      components/           ← shared components (RepoCard, CaptureModal, CreateRepoModal, SseProvider)
      stores/               ← SSE-fed reactive Svelte stores
    routes/                 ← pages (Overview, RepoDashboard)
  dist/                     ← compiled output (git-ignored, embedded at build)
  vite.config.js
  package.json
internal/
  api/                      ← HTTP handlers (thin, delegate to existing packages)
    overview.go
    repos.go
    capture.go
    events.go
    errors.go               ← error → status code mapping
  web/                      ← HTTP server wiring, SSE hub, go:embed
    server.go
    hub.go
    embed.go
cmd/bridge/
  serve.go                  ← new Cobra subcommand
```

---

## REST API

All routes under `/api/`. Handlers are thin: parse → call existing `internal/` package → JSON. No business logic in `internal/api/`.

| Method | Path | Description |
|---|---|---|
| `GET` | `/api/overview` | Cross-repo overview (weighted tiers + Projects v2 roadmap tier) |
| `GET` | `/api/repos` | List all repos from config roots |
| `GET` | `/api/repos/{owner}/{name}` | Repo detail: sessions, worktrees, branches, commits, git status, issues |
| `POST` | `/api/repos` | Create repo (mirrors `bridge create`) |
| `POST` | `/api/repos/{owner}/{name}/session` | Launch tmux session |
| `POST` | `/api/capture/idea` | Capture idea (mirrors `bridge capture idea`) |
| `POST` | `/api/capture/issue` | Capture issue (mirrors `bridge capture issue`) |
| `GET` | `/api/events` | SSE stream |

Errors are mapped to status codes in one place (`internal/api/errors.go`). The existing packages (`internal/overview`, `internal/capture`, `internal/remote`, forge clients) are untouched.

---

## SSE

A single `internal/web.Hub` manages connected SSE clients. All clients connect to `GET /api/events`.

**Event types:**

| Event | Payload | When |
|---|---|---|
| `repo-updated` | `{owner, name}` | After any repo mutation or on ticker |
| `overview-updated` | — | After capture/create or on ticker |
| `session-changed` | `{owner, name}` | After session launch |

The hub fires on a background ticker (10s default) plus immediately after any mutation. Clients removed on disconnect. Svelte's `SseProvider` component subscribes once on mount and feeds reactive stores.

---

## Svelte WebUI

### UI workflow gate

**Before any Svelte component code is written**, run the mandatory UI phases:

1. `/ui-brainstorm` → ASCII wireframes approved
2. `/ui-flow` → Mermaid state diagrams approved
3. `/ui-build` → shell → logic → interactions → polish
4. `/ui-review` → checklist passes

The pages and components below are intentionally coarse — wireframes will define the actual layout.

### Pages

**Overview (`/`)** — landing view, three areas:
- **Visualization** — Radar + Word Cloud (`bridge-overview-directions.html` promoted to Svelte components), fed from `/api/overview`. Default landing view.
- **Tier list** — ranked issues (value/effort scored) + Projects v2 Status roadmap tier. Shown alongside or toggled with the visualization.
- **Capture bar** — quick idea/issue input, posts to `/api/capture/*`.

**Repo dashboard (`/repos/:owner/:name`)** — per-repo detail, panels mirroring `bridge nav` layer 2:
- Sessions (active tmux sessions + launch button)
- Worktrees
- Branches + recent commits
- Git status
- Open issues
- **Architecture visualization** — `bridge-poc2.html` Onion/Layer view as a collapsible Svelte component. Stub/mock data initially; wired to real data once a clean-arch resolver exists (deferred).

### Shared components

| Component | Purpose |
|---|---|
| `SseProvider` | Wraps SSE connection, feeds reactive stores; mounts once at app root |
| `RepoCard` | Repo summary, used in overview list and dashboard header |
| `CaptureModal` | Idea/issue capture, reusable from any page |
| `CreateRepoModal` | Create repo (mirrors nav Ctrl+N) |

### Visualization components

Both PoCs are promoted to Svelte components with props replacing hardcoded mock data:

- `<Radar {components} {deps} {activeFeature} {agentId} />` — concentric ring diagram
- `<WordCloud {components} />` — salience-based word cloud
- `<ArchViz {components} {deps} />` — Onion/Layer architecture view (stub data, real data deferred)

---

## `bridge serve` command

```
bridge serve [--port 7777] [--host 127.0.0.1]
```

- Cobra subcommand in `cmd/bridge/serve.go`
- Prints the URL on startup: `Bridge WebUI listening on http://127.0.0.1:7777`
- Starts the SSE hub's background ticker
- Listens for SIGINT/SIGTERM, calls `srv.Shutdown(ctx)` with a 10s bounded context (graceful drain)
- Errors (port in use, embed missing) surface via `RunE` → mapped exit code at root

---

## Build integration

Two new `justfile` recipes:

```
web-build:
    cd web && npm ci && npm run build

build: web-build
    go build -ldflags "..." ./cmd/bridge
```

`just build` always rebuilds the Svelte bundle before compiling Go, so the embed is never stale. `just web-build` can be run alone during frontend development.

---

## Testing

### Go

- Table-driven handler tests via `net/http/httptest` (existing pattern, no new deps)
- SSE hub unit test: N fake clients connect, hub broadcasts, assert all receive
- Full suite under `go test -race ./...`

### Svelte

- **Vitest** (Vite-native, no separate Jest config)
- `@testing-library/svelte` for component rendering assertions
- SSE store tests: mock `EventSource`, assert store values update on event
- No E2E for now — manual golden-path verification sufficient for a personal tool

### CI gate

`just test` runs both:
```
go test -race ./...
cd web && npm test -- --run
```

`npm run build` must succeed before `go build` (enforced by `just build` recipe order).

---

## Follow-ups (out of scope for this plan)

- **Svelte stack overlay in `ai-instructions`** — extend `stacks/go.md` with a `## Svelte (WebUI)` section covering Vite config conventions, Svelte store patterns (SSE-fed), Vitest setup, and `web/` directory layout. Benefits future Go+WebUI projects.
- **Clean-arch resolver** — maps source files to layers/components so the `ArchViz` component can show real data instead of stubs.
- **Authentication** — Traefik middleware (BasicAuth or ForwardAuth/Authelia) added at proxy layer, no app changes required.
- **Mobile layout** — responsive design pass after desktop is working.
- **`bridge serve` as a systemd unit** — for always-on homelab access (similar to bridge-bot's unit).
