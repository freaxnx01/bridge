# Bridge MCP — cheat sheet

Quick reference for starting `bridge mcp serve` and wiring it into Claude
Code / Claude Desktop / a MetaMCP proxy. For the design rationale and full
implementation notes, see:

- Spec: [`docs/superpowers/specs/2026-07-11-mcp-cross-forge-endpoint-design.md`](superpowers/specs/2026-07-11-mcp-cross-forge-endpoint-design.md)
- Plan: [`docs/superpowers/plans/2026-07-11-mcp-cross-forge-endpoint.md`](superpowers/plans/2026-07-11-mcp-cross-forge-endpoint.md)
- Code: `cmd/bridge/mcp.go`, `internal/mcp/`

---

## What it is

`bridge mcp serve` runs a **Streamable HTTP MCP server** exposing nine
cross-forge tools over GitHub + Forgejo (six in `--read-only` mode):

| Tool | Purpose | Notes |
|---|---|---|
| `list_repos` | List repos across configured (or requested) owners | Concurrent fan-out; partial failures land in a `warnings` field instead of failing the whole call |
| `read_file` | Read a file's content + blob sha | Default branch only (no `ref` pinning in this slice) |
| `list_tree` | List a directory's entries, or the full tree with `recursive: true` | Default branch only; a `truncated` flag surfaces when GitHub's recursive trees API cuts off past its size limit rather than silently returning a partial tree; an empty repo returns an empty list, not an error |
| `list_issues` | List open issues for a single repo | Needs no capability assertion — part of the tier-1 `ForgeReader` surface, so it works on any wired forge |
| `list_git_forges` | List the configured `(forge, owner)` targets, whether each is configured, and which tools it supports | Read-only, no network requests — resolution is cached per process |
| `create_issue` | Create an issue | **Draft by default** — nothing is created unless called with `confirm: true`. Not registered at all when `--read-only` |
| `create_repo` | Create a repository | **Draft by default**, same `confirm: true` gate. Not registered at all when `--read-only`. The `owner` input selects which account's **token** to use, not the destination — both clients POST to `/user/repos`, so the repo is created under whichever account the token belongs to, which may differ from the requested owner |
| `update_repo` | Update description, topics, visibility, and/or archived state | **Draft by default**, same `confirm: true` gate. `topics` lives on a separate endpoint from the rest — if it fails after the description/private/archived PATCH already succeeded, that's reported as a partial result (`topics_error` alongside a populated `result`), not a top-level error that would discard the successful half. `archived: true` additionally requires the server to run with `--allow-destructive`, since archiving blocks all further writes to the repo |
| `cross_forge_status` | The same cross-forge overview snapshot `bridge nav`/WebUI use | Read-only |

The endpoint is guarded by a **static bearer token** (`BRIDGE_MCP_TOKEN`),
compared in constant time.

---

## Starting the server

```bash
export BRIDGE_MCP_TOKEN="$(openssl rand -hex 24)"   # or your own secret
export BRIDGE_MCP_OWNERS="github:freaxnx01, forgejo:freax"

bridge mcp serve
```

Server logs `Bridge MCP addr=http://127.0.0.1:7788 read_only=false auth=true`
and listens until `SIGINT`/`SIGTERM` (graceful shutdown, 10s drain).

### Flags

| Flag | Default | Purpose |
|---|---|---|
| `--port` | `7788` | Port to listen on |
| `--host` | `127.0.0.1` | Host to bind. Combining `--no-auth` with a non-loopback host is rejected at startup |
| `--read-only` | `false` | Omits `create_issue` and `create_repo` entirely (not just gated — never registered) |
| `--no-auth` | `false` | Skips the bearer check. **Loopback only** — the server refuses to start otherwise |

### Environment variables

| Var | Required? | Purpose |
|---|---|---|
| `BRIDGE_MCP_TOKEN` | yes, unless `--no-auth` | The bearer secret clients must send as `Authorization: Bearer <token>` |
| `BRIDGE_MCP_OWNERS` | no | Default `(forge:owner)` targets for `list_repos` when no `owner` is given in a tool call, e.g. `"github:freaxnx01, forgejo:freax"` (comma/space separated) |
| `BRIDGE_MCP_READONLY` | no | Set to `1` as an alternative to `--read-only` |
| `BRIDGE_GITHUB_API` / `BRIDGE_FORGEJO_API` | no | Override the default API base URLs (self-hosted Forgejo, GitHub Enterprise, etc.) |

Per-owner **GitHub** tokens and the single **Forgejo** token are resolved the
same way the rest of `bridge` resolves them — via `direnv` in each repo's
scope under your configured repo roots (`internal/remote`). If a target's
token can't be resolved, that target is skipped with a warning rather than
failing the whole call (resolution is cached per process, so this only costs
a `direnv exec` once per `(forge, owner)`).

### Smoke-test it

```bash
# Missing bearer -> 401
curl -s -o /dev/null -w '%{http_code}\n' -X POST http://127.0.0.1:7788/

# Valid bearer, initialize -> 200/202
curl -s -o /dev/null -w '%{http_code}\n' -X POST http://127.0.0.1:7788/ \
  -H "Authorization: Bearer $BRIDGE_MCP_TOKEN" \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  --data '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"clientInfo":{"name":"c","version":"v0"},"protocolVersion":"2025-06-18","capabilities":{}}}'
```

### Running it long-term

There's no systemd unit yet (tracked in `TODO.md`) — for now, run it in a
`tmux` pane, under a process supervisor of your choice, or add
`--no-auth`-free `bridge mcp serve` to your own service manager. Since
`WriteTimeout` is intentionally unset (SSE streams are long-lived), don't put
a strict reverse-proxy timeout in front of it either.

---

## Integrating with Claude Code

Claude Code talks to remote MCP servers over HTTP directly — add it once:

```bash
claude mcp add --transport http bridge http://127.0.0.1:7788 \
  --header "Authorization: Bearer $BRIDGE_MCP_TOKEN"
```

- Default scope is local (this machine only). Add `--scope project` to share
  the config via the repo's `.mcp.json`, or `--scope user` for all your
  projects.
- Verify it's connected: `claude mcp list` (or `/mcp` inside a Claude Code
  session).
- Remove it later with `claude mcp remove bridge`.

---

## Integrating with Claude Desktop

Claude Desktop adds remote MCP servers as **custom connectors**:

1. Open **Settings → Connectors** (naming/location may shift between
   versions — look for "Add custom connector" or similar).
2. Enter the server URL: `http://127.0.0.1:7788`.
3. If the UI offers a custom-headers field, set
   `Authorization: Bearer <BRIDGE_MCP_TOKEN>` there.

Desktop's remote-connector UI has historically leaned toward OAuth-style
auth flows rather than static bearer headers — if your installed version
doesn't expose a custom-headers option, the workaround is a small local
reverse proxy that injects the `Authorization` header before forwarding to
`bridge mcp serve` (e.g. a two-line Caddy/nginx config), or use `--no-auth`
purely for local Desktop use since the server refuses to bind non-loopback
without a token anyway. Check Anthropic's current Claude Desktop docs for
the exact steps in your version.

---

## Integrating via MetaMCP (homelab proxy)

If MetaMCP is aggregating MCP servers behind a single endpoint in your
homelab, add `bridge mcp serve` as one of its backend servers rather than
pointing every client at it directly:

1. **Make bridge reachable from MetaMCP.** If MetaMCP runs in a different
   container/LXC than `bridge mcp serve`, loopback (`127.0.0.1`) won't be
   reachable — bind bridge to an address MetaMCP can reach instead (its own
   LXC's LAN IP, or a stable `*.home.freaxnx01.ch` hostname via the same
   Traefik dispatcher + PiHole pattern used for every other homelab service).
   A non-loopback `--host` **requires** a real `BRIDGE_MCP_TOKEN` — the
   server refuses `--no-auth` on anything but loopback (see Flags above), so
   don't disable auth for this path.
2. **Add it as an MCP Server in MetaMCP's admin UI.** Create a new server
   entry with transport **Streamable HTTP** (not stdio), URL pointing at
   `http://<bridge-host>:7788`, and set a custom header
   `Authorization: Bearer <BRIDGE_MCP_TOKEN>` so MetaMCP authenticates on
   bridge's behalf.
3. **Add it to a Namespace, then an Endpoint.** MetaMCP groups servers into
   namespaces and exposes them via an endpoint URL — that endpoint (not
   bridge's URL) is what you point Claude Code/Desktop at, giving one
   aggregated connection instead of a separate `claude mcp add` per backend
   server.

Exact field names/navigation in MetaMCP's UI can shift between versions —
if a step above doesn't match what you see, the underlying requirements
(Streamable HTTP transport, a reachable non-loopback address, the bearer
header) still apply regardless of the UI.

---

## Troubleshooting

| Symptom | Cause |
|---|---|
| Server refuses to start: `BRIDGE_MCP_TOKEN is required` | Set the env var or pass `--no-auth` |
| Server refuses to start: `--no-auth requires a loopback --host` | Don't combine `--no-auth` with `--host 0.0.0.0`/a public IP — use a real token instead |
| `list_repos` returns fewer repos than expected, with entries in `warnings` | A target's forge token couldn't be resolved (missing direnv scope) or its API call failed — check the warning text for which `(forge, owner)` and why |
| `read_file`/`list_repos` with an `owner` but no `forge` errors out | This is intentional — `forge` is required alongside an explicit `owner` to avoid silently guessing which forge |
| `create_issue` call "succeeds" but nothing shows up on GitHub/Forgejo | You didn't pass `confirm: true` — the response is a draft by design |
