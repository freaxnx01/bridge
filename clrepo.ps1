# clrepo.ps1 — PowerShell entry point for clrepo on Windows.
# Locates bash.exe (Git Bash), sources clrepo.sh, and forwards arguments.
# See docs/specs/2026-05-19-clrepo-windows-ps-support-design.md.

$ErrorActionPreference = 'Stop'

function Find-Bash {
    if ($env:CLREPO_BASH -and (Test-Path $env:CLREPO_BASH)) {
        return $env:CLREPO_BASH
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
    Write-Error "clrepo: bash.exe not found. Install Git for Windows or set `$env:CLREPO_BASH."
    exit 127
}

$clrepoSh = Join-Path $PSScriptRoot 'clrepo.sh'
if (-not (Test-Path $clrepoSh)) {
    Write-Error "clrepo: clrepo.sh not found next to clrepo.ps1 (looked for $clrepoSh)."
    exit 2
}

# Convert the Windows path to a Git Bash style path so `source` works.
$clrepoShPosix = & $bash --norc -c 'cygpath -u "$1"' _ $clrepoSh

# Forward arguments via $@. The literal 'clrepo' is $0 (used by the
# script in error messages).
& $bash --norc -c "source `"$clrepoShPosix`" && clrepo `"`$@`"" clrepo @args
exit $LASTEXITCODE
