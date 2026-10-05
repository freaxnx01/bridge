# Connecting clients to the bridge MCP server

How each Claude client reaches the deployed bridge MCP endpoint
(`https://bridge-mcp.home.freaxnx01.ch/`), and why the two paths differ. For
server flags, tools and transport details see [`mcp-cheatsheet.md`](mcp-cheatsheet.md).

The short version: **Claude Code talks HTTP natively; Claude Desktop does not**,
so Desktop needs a local stdio→HTTP adapter in between.

---

## Claude Code CLI → bridge

```
Claude Code ──HTTPS + "Authorization: Bearer <token>"──▶ https://bridge-mcp.home.freaxnx01.ch/
```

- **Transport:** `http` (Streamable HTTP), stored under `mcpServers.bridge` in
  `~/.claude.json`.
- **Auth:** static bearer token in the entry's `headers`. It sits in
  `~/.claude.json` in cleartext.
- **Lifecycle:** connects at session start. A newly added server is only live
  in the *next* session.

No proxy, no Node, no watchdog.

### Setting it up on a new machine (e.g. a work notebook)

1. Check reachability — **HTTP 401 is the healthy answer** (DNS, TLS and
   routing all fine, just no token):

   ```
   curl.exe -s -o NUL -w "%{http_code}\n" https://bridge-mcp.home.freaxnx01.ch/ --max-time 5
   ```

   A timeout or connection error means the network path (corporate firewall,
   proxy, VPN) is blocking the homelab domain — fix that first.

2. Get the bearer token from an existing machine's `~/.claude.json` via a
   password manager, never chat or email.

3. Register at **user scope**:

   ```
   claude mcp add --scope user --transport http bridge https://bridge-mcp.home.freaxnx01.ch/ \
     --header "Authorization: Bearer <token>"
   ```

4. Start a fresh session and confirm with `claude mcp list` (or `/mcp`).

**Scope gotcha:** without `--scope user`, `claude mcp add` writes a
*local/project* entry keyed to the current directory. The server then silently
doesn't exist in sessions started anywhere else — the tools are just absent,
with no error. When a bridge tool is "missing", check which scope it was
registered at (and which directory you're in) before anything else.

---

## Claude Desktop → bridge

Desktop's `claude_desktop_config.json` can only **launch local stdio
processes**, so a translator sits in the middle:

```
Claude Desktop ──stdio──▶ node bridge-mcp-proxy.mjs ──one HTTPS POST per message──▶ bridge-mcp.home.freaxnx01.ch/mcp
```

Config entry shape (token redacted):

```json
"bridge": {
  "command": "<node-lts>\\node.exe",
  "args": ["<path>\\bridge-mcp-proxy.mjs", "https://bridge-mcp.home.freaxnx01.ch/mcp"],
  "env": { "AUTH_HEADER": "Bearer <token>" }
}
```

- **Adapter:** `bridge-mcp-proxy.mjs`, a zero-dependency stdio↔Streamable-HTTP
  script. Source is in [`contrib/claude-desktop/`](../contrib/claude-desktop/),
  installed to `~/.local/bin` on the Win11 box. It replaced `mcp-remote` on
  2026-10-01. Setup on a new machine: [claude-desktop-setup.md](claude-desktop-setup.md).
- **Why not `mcp-remote`:** it holds a long-lived connection, gives up after two
  reconnect attempts, then stays alive doing nothing. Desktop never respawns a
  dead MCP process, so every network blip meant a Desktop restart (33 in 23
  days).
- **Why the proxy works:** the server is [stateless](mcp-cheatsheet.md#stateless-transport),
  so the proxy sends **one POST per JSON-RPC message** and holds no socket while
  idle. A blip fails only the calls made during it; the next call just works.
  It never replays a `tools/call` that may already have reached the server.
- **Runtime:** pinned to a Node **LTS** install — Node v25 intermittently hit a
  libuv assertion on the proxy's shutdown path.
- **Log:** `%LOCALAPPDATA%\bridge-mcp-proxy\proxy.log`.
- **Safety net:** the `bridge-mcp-watchdog` scheduled task, and the
  `bridge-mcp-check` Claude Code skill for a layered health check when bridge
  goes missing.

None of the Desktop setup is needed for a machine that only uses the CLI.

---

## Comparison

| | Claude Code CLI | Claude Desktop |
|---|---|---|
| Transport to bridge | Native Streamable HTTP | stdio → local proxy → HTTP |
| Config | `~/.claude.json` (`claude mcp add`) | `claude_desktop_config.json` |
| Token location | `headers.Authorization` | `env.AUTH_HEADER` |
| Extra moving parts | none | proxy script, Node LTS, watchdog |
| Picks up config changes | next session | hot-reloads the config file |
