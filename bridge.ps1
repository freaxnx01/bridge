# bridge.ps1 — PowerShell entry point for bridge on Windows.
# Locates bash.exe (Git Bash), sources bridge.sh, and forwards arguments.
# See docs/specs/2026-05-19-bridge-windows-ps-support-design.md.

$ErrorActionPreference = 'Stop'

function Find-Bash {
    if ($env:BRIDGE_BASH -and (Test-Path $env:BRIDGE_BASH)) {
        return $env:BRIDGE_BASH
    }
    $git = Get-Command git.exe -ErrorAction SilentlyContinue
    if ($git) {
        try {
            $execPath = & git.exe --exec-path 2>$null
            if ($execPath) {
                $candidate = Join-Path (Split-Path -Parent (Split-Path -Parent $execPath)) 'bin\bash.exe'
                if (Test-Path $candidate) { return $candidate }
            }
        } catch {}
    }
    foreach ($candidate in @(
        'C:\Program Files\Git\bin\bash.exe',
        'C:\Program Files (x86)\Git\bin\bash.exe'
    )) {
        if (Test-Path $candidate) { return $candidate }
    }
    $where = Get-Command bash.exe -ErrorAction SilentlyContinue
    if ($where) { return $where.Source }
    return $null
}

$bash = Find-Bash
if (-not $bash) {
    Write-Error "bridge: bash.exe not found. Install Git for Windows or set `$env:BRIDGE_BASH."
    exit 127
}

$bridgeSh = Join-Path $PSScriptRoot 'bridge.sh'
if (-not (Test-Path $bridgeSh)) {
    Write-Error "bridge: bridge.sh not found next to bridge.ps1 (looked for $bridgeSh)."
    exit 2
}

# Convert the Windows path to a Git Bash style path so `source` works.
$bridgeShPosix = & $bash --norc -c 'cygpath -u "$1"' _ $bridgeSh

# Forward arguments via $@. The literal 'bridge' is $0 (used by the
# script in error messages).
& $bash --norc -c "source `"$bridgeShPosix`" && bridge `"`$@`"" bridge @args
exit $LASTEXITCODE
