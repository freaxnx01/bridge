# Claude Desktop setup (without mcp-remote)

How to connect Claude Desktop on a new machine to the bridge MCP server
using `bridge-mcp-proxy.mjs` instead of `mcp-remote`. For why this proxy
exists, see [client-connections.md](client-connections.md#claude-desktop--bridge).

Claude Code doesn't need any of this. It speaks HTTP natively, so use
`claude mcp add --transport http ...` instead (see
[client-connections.md](client-connections.md)).

## What you get

```
Claude Desktop ──stdio──▶ node bridge-mcp-proxy.mjs ──one HTTPS POST per message──▶ https://bridge-mcp.home.freaxnx01.ch/mcp
```

| File (in [`contrib/claude-desktop/`](../contrib/claude-desktop/)) | Role | Needed? |
|---|---|---|
| `bridge-mcp-proxy.mjs` | stdio ↔ Streamable-HTTP adapter, zero dependencies | **yes** |
| `bridge-mcp-watchdog.ps1` | Detects a missing or stuck proxy and shows a notification | optional, Windows only |
| `bridge-mcp-watchdog.vbs` | Silent launcher for the watchdog, so no console window flashes | only with the watchdog |

### Differences from mcp-remote

- **No persistent connection.** Every JSON-RPC message is its own POST. A
  network blip fails only the calls made during the blip, and the next call
  works again. Desktop never respawns a dead MCP process, and this design
  means it doesn't have to.

- **Safe retries.** A request is retried for up to 60 s only if it can't have
  reached the server (DNS or connect errors, HTTP 502/503) or if it isn't a
  `tools/call`. A `tools/call` that may already have run is never replayed.

- **Session recovery.** If the server answers 404 to a stale `Mcp-Session-Id`,
  the proxy runs `initialize` again in the background and retries the call.

### Limitations

- **Static bearer token only.** There is no OAuth flow, which bridge doesn't
  need.

- **No server-initiated stream.** There is no `GET` SSE channel, so the server
  can only send notifications inside the response to a request.

- **No live progress.** SSE responses are read in full before they're
  forwarded.

The proxy isn't specific to bridge. It works with any Streamable-HTTP MCP
server that accepts a bearer header and copes with one POST per message. Pass
that server's URL as the argument.

## Prerequisites

- **Node.js LTS ≥ 18**, because the proxy needs the built-in `fetch`. Use an
  **LTS** release: Node v25 intermittently hit a libuv assertion on the
  proxy's shutdown path.

- **A bridge MCP bearer token.** It's the same token Claude Code uses.

- **For the watchdog only:** PowerShell 7 at
  `C:\Program Files\PowerShell\7\pwsh.exe`.

## Setup (Windows)

### 1. Install a pinned Node LTS

A portable install keeps the proxy's runtime independent of whatever `node`
is on `PATH`:

```powershell
$v    = 'v24.20.0'   # any current LTS
$dest = "$env:USERPROFILE\.local\node-lts"
New-Item -ItemType Directory -Force $dest | Out-Null
Invoke-WebRequest "https://nodejs.org/dist/$v/node-$v-win-x64.zip" -OutFile "$env:TEMP\node-lts.zip" -UseBasicParsing
Expand-Archive "$env:TEMP\node-lts.zip" $dest -Force
$node = "$dest\node-$v-win-x64\node.exe"
& $node --version
```

### 2. Copy the scripts

Run this from a checkout of this repo:

```powershell
$bin = "$env:USERPROFILE\.local\bin"
New-Item -ItemType Directory -Force $bin | Out-Null
Copy-Item contrib\claude-desktop\* $bin
```

### 3. Smoke-test before you touch Desktop

```powershell
$env:AUTH_HEADER = 'Bearer <token>'
@'
{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"smoke","version":"1"}}}
{"jsonrpc":"2.0","method":"notifications/initialized"}
{"jsonrpc":"2.0","id":2,"method":"tools/list"}
'@ | & $node "$bin\bridge-mcp-proxy.mjs" https://bridge-mcp.home.freaxnx01.ch/mcp
Remove-Item Env:AUTH_HEADER
```

Expected output is a `started` log line, a JSON line with `"id":1` and
`serverInfo`, a JSON line with `"id":2` and the tool list, and then
`stdin closed; exiting`.

| Error you see | Meaning |
|---|---|
| `HTTP 401 ... (token rejected)` | Wrong token, or it's missing the `Bearer ` prefix |
| `bridge unreachable (ENOTFOUND)` after ~60 s | DNS, VPN or proxy can't reach the homelab domain |

### 4. Point Desktop at the proxy

Back up first:

```powershell
$cfgPath = "$env:APPDATA\Claude\claude_desktop_config.json"
Copy-Item $cfgPath "$cfgPath.bak-$(Get-Date -Format yyyyMMdd)"
```

Add the following, or replace any existing `mcp-remote` entry, under
`mcpServers`. Use absolute paths and doubled backslashes:

```json
"bridge": {
  "command": "C:\\Users\\<you>\\.local\\node-lts\\node-v24.20.0-win-x64\\node.exe",
  "args": [
    "C:\\Users\\<you>\\.local\\bin\\bridge-mcp-proxy.mjs",
    "https://bridge-mcp.home.freaxnx01.ch/mcp"
  ],
  "env": { "AUTH_HEADER": "Bearer <token>" }
}
```

The token goes in `env`, never in `args`, because command lines are readable
by other processes.

Optional tuning, also set in `env`:

| Variable | Default | Effect |
|---|---|---|
| `BRIDGE_PROXY_RETRY_SECONDS` | `60` | Retry budget for calls that are safe to resend |
| `BRIDGE_PROXY_TIMEOUT_SECONDS` | `300` | Per-call HTTP timeout |

### 5. Restart Desktop fully and verify

Closing the window isn't enough. Quit from the tray icon, or end every
`claude.exe` under `WindowsApps`, then start Desktop again.

```powershell
Get-Content "$env:LOCALAPPDATA\bridge-mcp-proxy\proxy.log" -Tail 5
```

You should see `started (node v24...) -> https://bridge-mcp...` with no
`[WARNING: no AUTH_HEADER]`. In a Desktop chat, the bridge tools should be
listed.

### 6. Optional: the watchdog

The watchdog runs every 5 minutes. When Desktop is running, it checks that a
bridge proxy process exists. If there's none, it shows a single balloon
notification ("Restart Claude Desktop"), and it won't repeat that
notification until the problem clears. Because the proxy is stateless, an
alive proxy counts as healthy. The script still contains the socket-based
stuck detection for `mcp-remote`, in case you roll back.

If your server URL differs, edit `$ServerUrl` at the top of
`bridge-mcp-watchdog.ps1`. Then register the task as your user:

```powershell
$user      = "$env:USERDOMAIN\$env:USERNAME"
$action    = New-ScheduledTaskAction -Execute 'C:\Windows\System32\wscript.exe' `
               -Argument "//nologo //B `"$env:USERPROFILE\.local\bin\bridge-mcp-watchdog.vbs`""
$logon     = New-ScheduledTaskTrigger -AtLogOn -User $user; $logon.Delay = 'PT2M'
$every5    = New-ScheduledTaskTrigger -Once -At (Get-Date).Date -RepetitionInterval (New-TimeSpan -Minutes 5)
$settings  = New-ScheduledTaskSettingsSet -Hidden -MultipleInstances IgnoreNew -StartWhenAvailable `
               -ExecutionTimeLimit (New-TimeSpan -Minutes 10) -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries
$principal = New-ScheduledTaskPrincipal -UserId $user -LogonType Interactive
Register-ScheduledTask -TaskName 'bridge-mcp-watchdog' -Action $action -Trigger $logon, $every5 `
  -Settings $settings -Principal $principal `
  -Description 'Detects a missing or stuck bridge-mcp proxy spawned by Claude Desktop.'
```

The task has to be **Interactive**: the notification needs a desktop session.
The `.vbs` launcher keeps the 5-minute runs from flashing a console window.

Check it with:

```powershell
Start-ScheduledTask bridge-mcp-watchdog
Get-Content "$env:LOCALAPPDATA\bridge-mcp-watchdog\watchdog.log" -Tail 3
```

Useful switches when you run it by hand:

| Switch | Effect |
|---|---|
| `-DryRun` | Only reports what it would kill |
| `-RenotifyHours N` | Repeats the notification every N hours |
| `-AutoRestartDesktop` | Restarts Desktop unattended. This is disruptive: it kills in-flight Desktop and Claude Code-in-Desktop work |

## macOS / Linux

The proxy works unchanged. Its log goes to `$HOME/bridge-mcp-proxy/proxy.log`.
Steps 1 to 5 apply with:

- `command` set to an absolute path to a Node LTS binary, because Desktop
  doesn't inherit your shell `PATH`.

- The config file at `~/Library/Application Support/Claude/claude_desktop_config.json`
  on macOS.

The watchdog is Windows-only, so skip it.

## Rolling back to mcp-remote

Restore the backup from step 4, or use this entry, then restart Desktop:

```json
"bridge": {
  "command": "npx",
  "args": ["-y", "mcp-remote", "https://bridge-mcp.home.freaxnx01.ch/mcp",
           "--header", "Authorization:${AUTH_HEADER}"],
  "env": { "AUTH_HEADER": "Bearer <token>" }
}
```

## Troubleshooting

Run the `bridge-mcp-check` Claude Code skill. It checks the whole chain layer
by layer: server, token, CLI config, Desktop config, proxy process, end to end,
then logs. The logs it reads:

- `%LOCALAPPDATA%\bridge-mcp-proxy\proxy.log`. It rotates at 2 MB.

- `%LOCALAPPDATA%\bridge-mcp-watchdog\watchdog.log`

Recent Desktop builds no longer write `mcp-server-*.log`.
