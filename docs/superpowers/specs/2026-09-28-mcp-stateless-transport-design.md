# MCP stateless transport — design

**Issue:** #313
**Date:** 2026-09-28
**Status:** approved

## Problem

`bridge mcp serve` mounts the MCP server with
`sdkmcp.NewStreamableHTTPHandler(…, nil)` — twice, once per auth mode
(`cmd/bridge/mcp.go`, `buildMCPHandler` and `buildOAuthHandler`). A `nil`
options struct means the SDK's default **stateful** Streamable HTTP transport:

- `initialize` mints an `Mcp-Session-Id` held in the handler's in-memory map.
- Every later request must carry a known session ID, or the SDK answers
  **404 `session not found`** (go-sdk v1.6.1 `mcp/streamable.go:301-307`).
- POST responses are `text/event-stream`, and `GET` opens a long-lived
  standalone SSE stream.

So the server holds per-client protocol state that is lost on every restart
(deploy, systemd restart, crash): a client that outlived it keeps sending a
dead session ID and gets 404s until it re-initialises. The state is also
process-local, so it could never be shared across replicas, and the SSE
streams are what reverse proxies and `mcp-remote` handle least well (#232).

None of that state buys anything. No tool uses server→client requests
(sampling, elicitation, roots) or progress/log notifications — grep of
`internal/mcp` and `cmd/bridge/mcp*.go` for `Elicit`, `CreateMessage`,
`ListRoots`, `NotifyProgress`, `Session` finds none. Every tool is a plain
request → response.

## Decision

Run the Streamable HTTP transport **stateless with JSON responses, always**:

```go
&sdkmcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true}
```

through **one** constructor, `newStreamableHandler(srv *sdkmcp.Server)
http.Handler`, used by both `buildMCPHandler` and `buildOAuthHandler`, so the
two auth modes cannot drift.

### Rejected alternatives

- **A `--stateless` flag.** Two transport modes to test and document for no
  user-visible benefit: nothing in bridge needs the stateful features. Rollback
  is `git revert`.
- **Stay stateful, add `SessionTimeout` + an `EventStore`.** Softens the
  symptoms (idle sessions reaped, streams resumable) but sessions still die on
  restart and still cannot be shared across replicas.
- **Stateless without `JSONResponse`.** Keeps a per-POST SSE stream that
  carries exactly one message. JSON is the simpler wire format for proxies and
  clients, and nothing streams.

## Behaviour after the change

| Request | Before (stateful) | After (stateless + JSON) |
|---|---|---|
| POST `initialize` | 200, SSE, mints session | 200, `application/json` |
| POST `tools/call` without prior `initialize`, no session ID | rejected (session not initialised) | 200, result |
| POST with an unknown/stale `Mcp-Session-Id` | **404** `session not found` | 200 — ID not validated |
| GET (standalone stream) | SSE stream (or 400 without session) | **405**, `Allow: POST` |
| DELETE with a session ID | closes the session, 204 | 204, no-op |
| Missing / wrong bearer | 401 | 401 — unchanged, auth runs per request |

The 405 on GET is what the MCP spec requires of a server that offers no SSE
stream; compliant clients (Claude Code, the go-sdk client, `mcp-remote`) fall
back to POST-only.

## Scope boundary — what "stateless" does *not* mean here

Only the **MCP protocol session** goes away. Process-level state stays:

- the per-(forge, owner) token/client cache (`newCachingClientResolver`),
- the audit log file,
- the OAuth store and its state-directory lock.

Making the tool core runnable without local repo roots, direnv or disk state
(e.g. for a VPS deployment) is a separate topic — see #289 / #285.

`internal/mcp` (tools, REST handler) is not touched.

## Components

| File | Change |
|---|---|
| `cmd/bridge/mcp.go` | add `newStreamableHandler`; both builders call it; update the `WriteTimeout` comment |
| `cmd/bridge/mcp_test.go` | new stateless-behaviour tests (below) |
| `docs/mcp-cheatsheet.md` | "Stateless transport" note; fix the SSE-based timeout note |
| `CHANGELOG.md` | `[Unreleased]` → `Changed` entry |

### `WriteTimeout`

The server comment justifies `WriteTimeout: 0` with long-lived SSE streams.
Those are gone, but a single tool call (`cross_forge_status`, `list_repos`
across many owners) can still legitimately run long, and a write deadline would
cut it off mid-response. The value **stays 0**; only the comment and the
matching cheatsheet sentence change to give the real reason.

## Testing

TDD, table-driven, in `cmd/bridge/mcp_test.go`, driving the real handler via
`httptest` with raw JSON-RPC over `net/http` (so the test sees exactly what a
client sees, not what the SDK client smooths over):

1. `tools/call` (`list_git_forges`, which makes no network requests) sent with
   **no** prior `initialize` and **no** `Mcp-Session-Id` → 200 and a JSON-RPC
   result.
2. Same call with a made-up `Mcp-Session-Id` → 200, not 404.
3. POST responses carry `Content-Type: application/json`.
4. GET → 405 with `Allow: POST`.
5. Cases 1–4 run against `buildMCPHandler` in both `--no-auth` mode and
   bearer mode (with the correct `Authorization` header), so the assertions
   cover the handler exactly as `bridge mcp serve` mounts it.
   The OAuth builder is **not** re-tested behind its guard: `internal/oauth`
   exposes no way to mint a valid token from outside the package, and adding
   one is out of scope. Its coverage is structural — it calls the same
   `newStreamableHandler`, and the existing
   `TestBuildOAuthHandler_RoutesAndMiddlewarePlacement` keeps proving the
   routing and 401 placement.
6. Existing `TestBuildMCPHandler_ValidBearerListsTools` (real go-sdk client
   connect + `ListTools`) stays green unchanged — proof that a spec-compliant
   client still works end-to-end.

Full gate: `gofmt -l .`, `go vet ./...`, `golangci-lint run`,
`go test -race ./...`.

## Acceptance criteria

- [ ] Both `buildMCPHandler` and `buildOAuthHandler` build their Streamable
      HTTP handler through a single `newStreamableHandler` with
      `Stateless: true, JSONResponse: true`.
- [ ] A `tools/call` with no prior `initialize` and no session ID succeeds.
- [ ] A request carrying an unknown `Mcp-Session-Id` succeeds instead of 404.
- [ ] POST responses are `application/json`.
- [ ] GET on the MCP endpoint returns 405 with `Allow: POST`.
- [ ] Bearer auth is unchanged (missing bearer → 401 in static mode).
- [ ] The existing go-sdk client test still lists the tools.
- [ ] `docs/mcp-cheatsheet.md` and `CHANGELOG.md` updated; `WriteTimeout`
      comment states the real reason.
- [ ] `gofmt`, `go vet`, `golangci-lint`, `go test -race ./...` all clean.

## Consequences

- Clients survive a server restart without re-initialising.
- The endpoint becomes replica-safe at the protocol layer (process-level state
  above still is not shared).
- Possibly resolves #232 (`mcp-remote` hanging on `put_file`), since the SSE
  response path it tripped over is gone — not claimed; #232 stays open until
  re-tested from Claude Desktop.
- Any future tool that wants server→client requests (elicitation, sampling) or
  streamed progress would need this decision revisited.
