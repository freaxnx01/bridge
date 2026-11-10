#requires -Version 7
<#
.SYNOPSIS
    Keeps an SSH tunnel to a bridge MCP server open, for machines that can't reach it directly.

.DESCRIPTION
    Forwards http://localhost:<LocalPort>/ to 127.0.0.1:<RemotePort> on -SshHost, where
    `bridge mcp serve` listens on loopback. Claude Desktop's bridge-mcp-proxy.mjs then points
    at http://localhost:<LocalPort>/. Runs forever from a logon scheduled task and restarts
    ssh whenever it exits.

    Uses native OpenSSH with BatchMode, so key-based auth for -SshHost must already work
    (`ssh <host>` without a prompt).

.NOTES
    VPN-only hosts: while the VPN is down every attempt times out. Retries back off from
    10 s to -MaxDelaySeconds and reset once a connection has stayed up for a minute, so a
    day offline costs a few log lines per minute instead of one every few seconds (the first
    version retried every 10 s and logged ~9 lines/minute with no rotation). The log rotates
    at 2 MB.

    Default local port 17788 rather than 7788: on the first Win11 box 7788 was stuck
    (suspected stale WSL mirrored-mode reservation).

    If you hit a blocker running this, fix it here and update this header so the next run
    does not have to re-derive it.
#>
[CmdletBinding()]
param(
    [Parameter(Mandatory)][string]$SshHost,
    [int]$LocalPort = 17788,
    [int]$RemotePort = 7788,
    [int]$MaxDelaySeconds = 60
)

$ssh = 'C:\Windows\System32\OpenSSH\ssh.exe'   # native OpenSSH, NOT a WSL ssh shim
$log = Join-Path $env:LOCALAPPDATA 'bridge-mcp-tunnel.log'
$minDelay = 10
$stableAfter = [timespan]::FromMinutes(1)

function Write-TunnelLog {
    param([string]$Message)
    if ((Test-Path $log) -and (Get-Item $log).Length -gt 2MB) {
        Move-Item $log "$log.1" -Force
    }
    Add-Content $log "$(Get-Date -Format s) $Message"
}

$delay = $minDelay
while ($true) {
    Write-TunnelLog "starting tunnel localhost:$LocalPort -> ${SshHost}:127.0.0.1:$RemotePort"
    $started = Get-Date
    & $ssh -N -L "${LocalPort}:127.0.0.1:${RemotePort}" `
        -o BatchMode=yes `
        -o ServerAliveInterval=15 `
        -o ServerAliveCountMax=3 `
        -o ExitOnForwardFailure=yes `
        -o ConnectTimeout=10 `
        $SshHost 2>&1 | ForEach-Object { Write-TunnelLog "ssh: $_" }
    $exitCode = $LASTEXITCODE

    if ((Get-Date) - $started -ge $stableAfter) {
        $delay = $minDelay
    }
    Write-TunnelLog "ssh exited with $exitCode, retrying in ${delay}s"
    Start-Sleep -Seconds $delay
    $delay = [math]::Min($delay * 2, $MaxDelaySeconds)
}
