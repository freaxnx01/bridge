# File toolset over REST — design

**Issue:** #278
**Date:** 2026-09-09
**Status:** approved

## Problem

`read_file`, `list_tree`, `search_code` and `put_file` are reachable only over MCP
(`internal/mcp/server.go:19-92`). The REST surface is `/api/overview`, `/api/repos`,
`/api/repos/{owner}/{name}`, `/api/capture/` and `/api/agents`, `/api/events`
(`cmd/bridge/serve.go:163-168`, `internal/web/server.go:29-42`).

A plain HTTP client therefore cannot read, list or write repository files. FlowHub is
such a client: [flowhub#84](https://github.com/freaxnx01/flowhub/issues/84) needs to take
a captured article, **search a vault for a related note, decide whether a new page is
warranted, choose the folder, and author the content**. Every step of that needs the file
tools, and none of it is reachable from where FlowHub sits.

## Rejected: a single `note` capture endpoint

The original framing of #278 was `POST /api/capture/note` taking `path`, `heading` and
`text`. It cannot work: the caller does not know `path` or `heading` — only a search of
the target repo determines them. An endpoint that assumes the answer to the hard part is
not useful to the client that has the hard part.

Give a client the four tools and it can do the whole job. Give it `note` and it cannot
start.

## Non-goals

- No change to the MCP surface or its behaviour.
- No relaxation of any existing write guard.
- No new capability: this exposes what already exists over a second transport.

## Decisions

### D1 — The REST handler lives in `internal/mcp`, not `internal/api`

The input structs (`readFileInput`, `listTreeInput`, `searchCodeInput`, `putFileInput`)
and the `Deps.handleX` methods are unexported. Reaching them from `internal/api` means
exporting a dozen types and four methods, and every future field addition then has to be
mirrored.

Instead `internal/mcp/rest.go` exposes `RESTHandler(deps Deps) http.Handler`, wired in
`serve.go`. Same package, same handlers, same structs. **The two transports cannot drift,
because there is only one implementation.** This is the unusual choice in the design and
it is deliberate: package placement follows the coupling, not the layer name.

### D2 — `POST /api/tools/{name}`, dispatched by a prefix switch

Mirrors `CaptureHandler` (`internal/api/capture.go:34-43`): trim the prefix, switch on
the remainder, unknown → 404. Tool names are the MCP names verbatim, so there is no
translation layer to get wrong and the two surfaces document each other.

POST for reads as well as writes — the inputs are JSON bodies, matching MCP.

### D3 — Guards preserved by construction, plus two that are not

The path allowlist and the `confirm=true` draft gate live *inside* `handlePutFile`, and
`sha` is required to update an existing file. Calling the same handler keeps all three
without restating them.

Two guards are enforced today by **registration**, not by the handler, and therefore have
to be added explicitly at the REST edge:

- **`put_file` is registered only when `!deps.ReadOnly`** (`internal/mcp/server.go:82-93`).
  A read-only server must answer **403** on `/api/tools/put_file`, not expose a write path
  MCP deliberately withholds.
- **Bearer auth.** `/api/capture/` is wrapped in `requireBearer(apiToken, …)`
  (`cmd/bridge/serve.go:167`); `/api/tools/` gets the same wrapper. All four tools are
  guarded, reads included — the repo's existing stance is that anything touching repo
  content is authenticated.

### D4 — Forgejo's `search_code` gap is surfaced, not smoothed over

`search_code` is GitHub-only; Forgejo has no code-search REST API, and a Forgejo target
lands in `warnings` rather than returning a silent empty result. That behaviour is
preserved verbatim over REST.

This matters to the calling client: of the three Obsidian vaults, only `obsidian-it` is on
GitHub. A client searching a Forgejo vault must fall back to `list_tree` + `read_file`, and
it can only know to do that if the warning survives the REST hop.

## Architecture

| Component | Responsibility |
|---|---|
| `internal/mcp/rest.go` — **new** | `RESTHandler(deps Deps) http.Handler`; decode → `handleX` → encode |
| `cmd/bridge/serve.go` — modify | register `/api/tools/` behind `requireBearer` |

Request and response bodies are the existing `*Input` / `*Output` structs, marshalled as
JSON. The handlers return `(*mcp.CallToolResult, XOutput, error)`; REST discards the first
and serialises the second.

## Error handling

| Condition | Status | Body |
|---|---|---|
| Unknown tool name | 404 | `unknown tool` |
| Non-POST | 405 | `method not allowed` |
| Malformed JSON | 400 | `invalid JSON body` |
| Missing/!bearer | 401 | existing `requireBearer` behaviour |
| `put_file` on a read-only server | 403 | `write tools are disabled on this server` |
| Handler returns an error | 500 | the error text, as elsewhere in `internal/api` |
| `put_file` without `confirm` | 200 | the handler's draft response — **not** an error |
| `put_file` with a stale `sha` | 500 | the forge's rejection, surfaced rather than retried |

A stale `sha` failing loudly is the point, not a rough edge: it is what stops a write
clobbering a concurrent human edit.

## Testing

- One table-driven test per tool: happy path, malformed body, wrong method.
- `put_file` on `Deps{ReadOnly: true}` → 403, and **nothing is written**.
- Unknown tool name → 404.
- Missing bearer → 401 for every tool, reads included.
- A Forgejo `search_code` target still returns its warning through the REST response.
- No existing MCP test changes — if one does, the transports have drifted.
