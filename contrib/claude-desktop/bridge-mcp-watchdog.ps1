<#
.SYNOPSIS
    Watchdog for the bridge-mcp proxy that Claude Desktop spawns.

.DESCRIPTION
    Claude Desktop (>= 1.46388.4) no longer writes mcp-server-*.log, so when its
    mcp-remote child proxy loses its connection and stops recovering, the failure is
    completely silent: the process stays alive, holds no socket, and never reconnects.
    Desktop does NOT respawn a dead proxy, so the only real recovery is a Desktop restart.

    This script makes that failure visible and cleans it up:
      - finds the mcp-remote proxy process for the bridge server
      - treats "alive past the grace period but holding no TCP connection" as stuck
      - confirms the server itself is reachable before blaming the client
      - kills the stuck process tree and tells the user Desktop needs a restart
      - notifies ONCE per episode, not once per run (see Show-ToastOnce)

    Never kills anything named claude.exe by name -- Claude Code's CLI shares that
    name, and killing by name would take out the user's own session.

.NOTES
    Since 2026-10-01 Desktop runs bridge-mcp-proxy.mjs instead of mcp-remote. That proxy
    is stateless (one POST per call, no idle socket), so the socket-count heuristic below
    is skipped for it -- only "no proxy process at all" still applies. The mcp-remote
    branch is kept for rollback to the old config.

    Launch this via bridge-mcp-watchdog.vbs, NOT pwsh.exe directly. The scheduled task
    runs interactively (Show-Toast needs a desktop session), and pwsh is a console app:
    Windows allocates its console before -WindowStyle Hidden is parsed, so running pwsh
    directly flashed a visible window every 5 minutes. wscript.exe is a GUI host and
    allocates no console, so the .vbs wrapper is genuinely silent.

    Notifications are episode-scoped, not run-scoped. The first version toasted on every
    run, so a fault that only a manual Desktop restart can fix produced a balloon every
    5 minutes for hours. Show-ToastOnce keeps a key in notify-state.txt and stays silent
    until Clear-NotifyState runs (the condition resolved). Pass -RenotifyHours N if you
    want a periodic reminder instead of a single one.

    VPN-only servers: when Desktop starts while bridge is unreachable (home office, VPN not
    yet connected), its initialize fails and it drops the server for the rest of that run.
    The no-proxy case therefore checks reachability first and notifies "connect first"
    instead of "restart Desktop"; once the server answers again, the notification key
    changes and the restart prompt follows. A proxy that is alive while the VPN is down is
    left alone -- its calls fail and recover by themselves.

    The server URL comes from the bridge entry in claude_desktop_config.json (override with
    -ServerUrl), so a machine on an SSH tunnel needs no edit here. -DryRun also suppresses
    notifications.

    If you hit a blocker running this, fix it here and update this header so the
    next run does not have to re-derive it.
#>
[CmdletBinding()]
param(
    [int]$GraceMinutes = 10,
    [int]$RenotifyHours = 0,
    [switch]$AutoRestartDesktop,
    [switch]$DryRun,
    # Empty = the URL from the bridge entry's args in claude_desktop_config.json, so a
    # machine that reaches bridge another way (e.g. an SSH tunnel) needs no local edit.
    [string]$ServerUrl = ''
)

$DefaultServerUrl = 'https://bridge-mcp.home.freaxnx01.ch/mcp'
$ConfigPath = Join-Path $env:APPDATA 'Claude\claude_desktop_config.json'
$LogDir     = Join-Path $env:LOCALAPPDATA 'bridge-mcp-watchdog'
$LogFile    = Join-Path $LogDir 'watchdog.log'
$StateFile  = Join-Path $LogDir 'notify-state.txt'
$DesktopAumid = 'Claude_pzs8sxrjxfjjc!Claude'

New-Item -ItemType Directory -Force -Path $LogDir | Out-Null

function Write-Log {
    param([string]$Message, [string]$Level = 'INFO')
    $line = "{0} [{1}] {2}" -f (Get-Date -Format 'yyyy-MM-dd HH:mm:ss'), $Level, $Message
    Add-Content -Path $LogFile -Value $line -Encoding utf8
    Write-Verbose $line
}

# Keep the log from growing without bound; this runs every few minutes forever.
if ((Test-Path $LogFile) -and (Get-Item $LogFile).Length -gt 2MB) {
    Move-Item $LogFile "$LogFile.1" -Force
}

function Show-Toast {
    # WinRT toast APIs are not loadable from PowerShell 7, so use a WinForms balloon,
    # which also lands in the Action Center if the balloon itself is missed.
    param([string]$Text)
    try {
        Add-Type -AssemblyName System.Windows.Forms -ErrorAction Stop
        Add-Type -AssemblyName System.Drawing -ErrorAction Stop
        $icon = New-Object System.Windows.Forms.NotifyIcon
        $icon.Icon = [System.Drawing.SystemIcons]::Warning
        $icon.BalloonTipTitle = 'bridge-mcp watchdog'
        $icon.BalloonTipText = $Text
        $icon.Visible = $true
        $icon.ShowBalloonTip(15000)
        Start-Sleep -Seconds 6
        $icon.Dispose()
    } catch {
        Write-Log "balloon notification unavailable: $($_.Exception.Message)" 'WARN'
    }
}

function Clear-NotifyState {
    # The condition is gone, so the next occurrence counts as a new episode and may notify again.
    if (Test-Path $StateFile) {
        Remove-Item $StateFile -Force -ErrorAction SilentlyContinue
        Write-Log 'condition cleared; notification state reset.'
    }
}

function Show-ToastOnce {
    # This task runs every few minutes forever, so a persisting fault would otherwise toast
    # on every single run (it did: one balloon every 5 minutes until Desktop was restarted).
    # Notify once per episode -- remember which condition was notified and stay silent until
    # it clears via Clear-NotifyState, or, if -RenotifyHours is set, until that many hours pass.
    param([string]$Key, [string]$Text)

    if ($DryRun) {
        Write-Log "[dry-run] would notify '$Key': $Text"
        return
    }

    $last = $null
    if (Test-Path $StateFile) {
        try {
            $parts = (Get-Content $StateFile -Raw -ErrorAction Stop).Trim() -split '\|', 2
            if ($parts.Count -eq 2) {
                $last = [pscustomobject]@{
                    Key  = $parts[0]
                    When = [datetime]::Parse($parts[1], [cultureinfo]::InvariantCulture)
                }
            }
        } catch {
            Write-Log "unreadable notification state, treating as first notification: $($_.Exception.Message)" 'WARN'
        }
    }

    if ($last -and $last.Key -eq $Key) {
        if ($RenotifyHours -le 0) {
            Write-Log "notification suppressed; already notified for '$Key' at $($last.When.ToString('yyyy-MM-dd HH:mm:ss'))."
            return
        }
        $dueAt = $last.When.AddHours($RenotifyHours)
        if ((Get-Date) -lt $dueAt) {
            Write-Log "notification suppressed for '$Key'; next reminder due $($dueAt.ToString('yyyy-MM-dd HH:mm:ss'))."
            return
        }
    }

    Show-Toast $Text
    Set-Content -Path $StateFile -Encoding utf8 -Value ('{0}|{1}' -f $Key, (Get-Date).ToString('o'))
}

function Resolve-ServerUrl {
    if ($ServerUrl) { return $ServerUrl }
    try {
        $cfg = Get-Content $ConfigPath -Raw | ConvertFrom-Json
        $url = @($cfg.mcpServers.bridge.args) | Where-Object { $_ -match '^https?://' } | Select-Object -First 1
        if ($url) { return $url }
    } catch {
        Write-Log "could not read the bridge URL from $ConfigPath : $($_.Exception.Message)" 'WARN'
    }
    return $DefaultServerUrl
}

function Test-BridgeReachable {
    # Any HTTP answer (a 401/405 included) means the network path works; curl reports 000
    # when it got none -- VPN down, SSH tunnel not listening, DNS failure.
    $code = & curl.exe -s -o NUL -w '%{http_code}' --max-time 10 -X POST $ServerUrl 2>$null
    return ($code -and $code -ne '000')
}

function Test-BridgeEndpoint {
    # Proves the server/network side is healthy, so a stuck client is genuinely the client's fault.
    try {
        $cfg  = Get-Content $ConfigPath -Raw | ConvertFrom-Json
        $auth = $cfg.mcpServers.bridge.env.AUTH_HEADER
    } catch {
        Write-Log "could not read AUTH_HEADER from $ConfigPath : $($_.Exception.Message)" 'WARN'
        return $false
    }
    $body = '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"bridge-watchdog","version":"1.0"}}}'
    $code = & curl.exe -s -o NUL -w '%{http_code}' --max-time 15 -X POST $ServerUrl `
        -H "Authorization: $auth" `
        -H 'Content-Type: application/json' `
        -H 'Accept: application/json, text/event-stream' `
        -d $body 2>$null
    return ($code -eq '200')
}

function Get-ProxyProcess {
    Get-CimInstance Win32_Process -Filter "Name='node.exe'" -ErrorAction SilentlyContinue |
        Where-Object { $_.CommandLine -and (
            ($_.CommandLine -match 'mcp-remote' -and $_.CommandLine -match 'bridge-mcp' -and $_.CommandLine -match 'proxy\.js') -or
            $_.CommandLine -match 'bridge-mcp-proxy\.mjs') }
}

function Test-StatelessProxy {
    # bridge-mcp-proxy.mjs opens one HTTP request per call and holds no socket while idle,
    # so "zero connections" is its normal state, not a hang. It never zombies: a failed
    # call just fails and the next one reconnects. Killing it would cause the very outage
    # this watchdog exists to fix, because Desktop does not respawn it.
    param($Process)
    return $Process.CommandLine -match 'bridge-mcp-proxy\.mjs'
}

function Get-DesktopProcess {
    # Match the packaged Desktop binary specifically: Claude Code's CLI is also claude.exe.
    Get-CimInstance Win32_Process -Filter "Name='claude.exe'" -ErrorAction SilentlyContinue |
        Where-Object { $_.CommandLine -and $_.CommandLine -match 'WindowsApps' -and $_.CommandLine -notmatch '--type=' }
}

function Get-EstablishedCount {
    param([int]$ProcessId)
    try {
        $conns = @(Get-NetTCPConnection -OwningProcess $ProcessId -State Established -ErrorAction Stop)
        return $conns.Count
    } catch {
        return 0
    }
}

$ServerUrl = Resolve-ServerUrl

$desktop = @(Get-DesktopProcess)
if ($desktop.Count -eq 0) {
    Write-Log 'Claude Desktop is not running; nothing to supervise.'
    Clear-NotifyState
    return
}

$proxies = @(Get-ProxyProcess)
if ($proxies.Count -eq 0) {
    Write-Log 'Claude Desktop is running but no bridge mcp-remote proxy exists -- bridge is unavailable until Desktop restarts.' 'WARN'
    # Desktop gives up on a server whose initialize failed, e.g. when it started while the
    # VPN was down. Restarting Desktop before the server is reachable just fails again, so
    # say "connect first" -- the key changes once it is reachable, which notifies again.
    if (-not (Test-BridgeReachable)) {
        Write-Log "bridge server $ServerUrl is unreachable (VPN down? tunnel not up?)." 'WARN'
        Show-ToastOnce 'server-unreachable' 'bridge-mcp is unreachable (VPN down?). Connect first -- you will be told when to restart Claude Desktop.'
        return
    }
    Show-ToastOnce 'needs-desktop-restart' 'No bridge proxy is running. Restart Claude Desktop to reconnect bridge-mcp.'
    return
}

if ($proxies.Count -gt 1) {
    Write-Log "$($proxies.Count) bridge proxies are running; Desktop spawns duplicates on launch. Only provably-dead ones are recycled." 'WARN'
}

$now = Get-Date
$stuck = @()
foreach ($p in $proxies) {
    $ageMin = [math]::Round(($now - $p.CreationDate).TotalMinutes, 1)
    if (Test-StatelessProxy $p) {
        Write-Log "proxy pid=$($p.ProcessId) age=${ageMin}m stateless bridge-mcp-proxy (alive = healthy; see %LOCALAPPDATA%\bridge-mcp-proxy\proxy.log)"
        continue
    }
    $conns  = Get-EstablishedCount -ProcessId $p.ProcessId
    Write-Log "proxy pid=$($p.ProcessId) age=${ageMin}m established=$conns"
    if ($ageMin -ge $GraceMinutes -and $conns -eq 0) { $stuck += $p }
}

if ($stuck.Count -eq 0) {
    Write-Log 'all bridge proxies healthy.'
    Clear-NotifyState
    return
}

if (-not (Test-BridgeEndpoint)) {
    Write-Log 'proxies hold no connection, but the bridge endpoint is also unreachable -- network/server side, not a stuck client. Taking no action.' 'WARN'
    return
}

Write-Log "endpoint is healthy but $($stuck.Count) proxy/proxies hold no connection -- treating as stuck." 'WARN'

foreach ($p in $stuck) {
    if ($DryRun) {
        Write-Log "[dry-run] would kill stuck proxy tree pid=$($p.ProcessId)"
        continue
    }
    & taskkill.exe /PID $p.ProcessId /T /F 2>&1 | ForEach-Object { Write-Log $_ }
    Write-Log "killed stuck proxy tree pid=$($p.ProcessId)"
}

if ($DryRun) { return }

if ($AutoRestartDesktop) {
    # Disruptive on purpose: this terminates in-flight Desktop work, including embedded
    # Claude Code sessions. Off by default; enable only if unattended recovery matters more.
    foreach ($d in $desktop) {
        Write-Log "restarting Claude Desktop (pid=$($d.ProcessId))" 'WARN'
        & taskkill.exe /PID $d.ProcessId /T /F 2>&1 | ForEach-Object { Write-Log $_ }
    }
    Start-Sleep -Seconds 5
    Start-Process 'explorer.exe' "shell:appsFolder\$DesktopAumid"
    Write-Log 'Claude Desktop relaunched.'
    Clear-NotifyState
} else {
    # Same key as the no-proxy case on purpose: killing the stuck tree leaves no proxy, so
    # the next run would otherwise toast again for what is one problem and one manual fix.
    Show-ToastOnce 'needs-desktop-restart' 'Cleared a stuck bridge-mcp proxy. Restart Claude Desktop to reconnect.'
    Write-Log 'Desktop does not respawn proxies; a manual Desktop restart is required to reconnect bridge.' 'WARN'
}
