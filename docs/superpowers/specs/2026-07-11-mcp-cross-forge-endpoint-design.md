# Bridge cross-forge MCP endpoint — design (#195, first slice)

**Issue:** [#195 — feat(mcp): expose Bridge as a self-hosted cross-forge MCP endpoint](https://github.com/freaxnx01/bridge/issues/195)
**Date:** 2026-07-11
**Status:** approved (brainstorming) — ready for implementation plan

## Goal

Bridge serves a remote (Streamable HTTP) MCP endpoint, self-hosted on the
agent-dev LXC behind Traefik + Authentik, giving a single Claude session (and
other MCP clients) read/write across GitHub + Forgejo repos.

This first slice ships four tools: `list_repos`, `read_file`, `create_issue`,
`cross_forge_status`.

## Settled decisions

1. **MCP protocol layer:** the official Go SDK
   `github.com/modelcontextprotocol/go-sdk` (v1.2.0, GA) — one new dependency,
   approved. It provides `mcp.NewServer`, typed `mcp.AddTool`,
   `mcp.NewStreamableHTTPHandler`, and `auth.RequireBearerToken`.
2. **`list_repos` source:** live `ListRepos` via the existing `internal/forge`
   adapters — not the external `ai-instructions/repos.md` catalog. Always
   current, no cross-repo file plumbing.
3. **Forgejo `read_file`:** add a `GetFile` method to `ForgejoClient`
   (only `GithubClient` has one today).
4. **Transport mount:** a new `bridge mcp serve` subcommand with its own
   listener, mirroring `bridge serve` — independent lifecycle/port so only the
   MCP surface runs on agent-dev.
5. **Headless auth:** a static bearer token (`BRIDGE_MCP_TOKEN`) verified
   in-app from day one, so Claude Code / cron can connect. Authentik/Traefik
   still fronts the browser OAuth flow for the Claude App at the edge.

## Architecture

Two new units, both reusing existing code:

- **`internal/mcp`** — server-construction package. Builds an `*mcp.Server`,
  registers the tools, no transport/process concerns. Depends on a small local
  `forgeClient` interface (defined at the consumer, per the Go overlay),
  satisfied by the existing `forge.GithubClient` / `forge.ForgejoClient`.
- **`cmd/bridge/mcp.go`** — the `bridge mcp serve` subcommand. Wires the server
  into `mcp.NewStreamableHTTPHandler`, wraps it in bearer auth, and runs an
  `http.Server` with `ReadHeaderTimeout`/`ReadTimeout`/`WriteTimeout`/
  `IdleTimeout` plus SIGINT/SIGTERM graceful shutdown — mirroring
  `cmd/bridge/serve.go`.

Token resolution reuses `internal/remote` (`GitHubToken`, `ForgejoToken`) and
the `BRIDGE_*_API` env vars already used by `capture` / `issues` / `create`.
Cross-forge status reuses `internal/overview` (`overview.Build`).

### Consumer interface

```go
// internal/mcp
type forgeClient interface {
    Name() string
    ListRepos(ctx context.Context, owner string) ([]forge.RepoRef, error)
    GetFile(ctx context.Context, owner, repo, path string) (content []byte, sha string, found bool, err error)
    CreateIssue(ctx context.Context, owner, repo, title, body string) (forge.Issue, error)
}
```

Both `*forge.GithubClient` and `*forge.ForgejoClient` satisfy this once Forgejo
gains `GetFile`. Tests pass a hand-rolled fake — no mocking framework.

## Tools

Each tool uses typed input/output structs (`AddTool` derives the JSON schema).

| Tool | Input | Output | Notes |
|---|---|---|---|
| `list_repos` | `{forge?, owner?}` optional filters | `{repos: []RepoRef}` | Live `ListRepos` across configured GitHub + Forgejo owners; concurrent fan-out via `golang.org/x/sync/errgroup` |
| `read_file` | `{forge, owner, repo, path, ref?}` | `{content, sha, found}` | Delegates to per-forge `GetFile` |
| `create_issue` | `{forge, owner, repo, title, body, confirm?}` | draft **or** created issue | Write tool — see write safety |
| `cross_forge_status` | `{}` | `overview.Snapshot` | Reuses `overview.Build` |

`list_repos` default owners come from a configured env list
(`BRIDGE_MCP_OWNERS`, comma/space separated `forge:owner` entries) when the
`owner` input is omitted; explicit input overrides.

## Write safety

- **Read-only mode** (`--read-only` flag / `BRIDGE_MCP_READONLY=1`):
  `create_issue` is **not registered** at all. Nothing to bypass — satisfies
  the AC "read-only mode disables all write tools" by construction.
- **Draft/confirm** (when writes are enabled): `create_issue` called without
  `confirm: true` returns a **structured draft** (resolved forge/owner/repo/
  title/body) and fires nothing. A follow-up call with `confirm: true`
  executes the create. This is data carried in the tool's input schema, not a
  Go flag-argument, so it stays within the clean-code guardrails.

## Auth

`auth.RequireBearerToken(verifier, opts)` middleware wraps the Streamable HTTP
handler. `verifier` is a custom `auth.TokenVerifier` that constant-time-compares
the presented bearer against `BRIDGE_MCP_TOKEN` (via `crypto/subtle`).

- Startup **fails fast** if `BRIDGE_MCP_TOKEN` is unset, unless `--no-auth`
  (localhost-dev only) is passed.
- Authentik/Traefik fronts the browser OAuth flow for the Claude App connector
  at the edge; the in-app bearer check is the baseline that also admits headless
  clients (Claude Code, cron). Reconciling Authentik OAuth ↔ bearer at the edge
  is a deployment concern, out of scope for this slice.

## Config & flags

`bridge mcp serve`:

| Flag | Default | Purpose |
|---|---|---|
| `--port` | `7788` | listen port |
| `--host` | `127.0.0.1` | bind address |
| `--read-only` | `false` | disable/omit write tools |
| `--no-auth` | `false` | skip bearer check (localhost dev only) |

Env: `BRIDGE_MCP_TOKEN`, `BRIDGE_MCP_READONLY`, `BRIDGE_MCP_OWNERS`, plus the
existing forge token/API vars (`GH_TOKEN`, `FORGEJO_TOKEN`, `BRIDGE_GITHUB_API`,
`BRIDGE_FORGEJO_API`, …). Precedence: flag → env → default.

## Files touched

- `go.mod` / `go.sum` — add `github.com/modelcontextprotocol/go-sdk`; `go mod tidy`
- `internal/forge/forgejo.go` (+ `forgejo_test.go`) — add `GetFile`, mirroring
  `github.go`'s contents-API call
- `internal/mcp/server.go` — `NewServer`, tool registration, read-only gating
- `internal/mcp/tools.go` — tool handlers + typed input/output structs
- `internal/mcp/auth.go` — static-bearer `TokenVerifier`
- `internal/mcp/*_test.go` — tool + auth + integration tests
- `cmd/bridge/mcp.go` — `bridge mcp serve` subcommand

## Testing

- `internal/mcp` tools driven by a hand-rolled fake `forgeClient`: `list_repos`
  aggregation (both forges, filter honoured), `read_file` per forge (found /
  not-found), `create_issue` draft-vs-confirm, read-only gating (write tool
  absent).
- Forgejo `GetFile`: `httptest` server test mirroring `github_test.go`.
- Bearer verifier: table test (valid / invalid / missing / malformed header).
- Integration: POST a JSON-RPC `initialize` then `tools/list` through
  `mcp.NewStreamableHTTPHandler` via `httptest`; assert the four tools (three in
  read-only mode) are advertised.
- Full suite green under `go test -race ./...`; `gofmt`, `go vet`,
  `golangci-lint`, `govulncheck` clean.

## Acceptance criteria (from #195)

- [ ] Endpoint reachable through Traefik/Authentik and addable as a Claude
      custom connector
- [ ] `list_repos` returns the catalog; `read_file` works on ≥1 repo per forge
- [ ] `create_issue` files on both a GitHub and a Forgejo repo
- [ ] Read-only mode disables all write tools

## Out of scope (deferred)

PR/commit write-back, milestone mutation, herdr/agent dispatch via MCP, and the
OAuth↔bearer reconciliation at the Authentik edge.
