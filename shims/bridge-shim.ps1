# bridge-shim.ps1 — dot-sourced from $PROFILE once Phase 3 cuts over.

# Sentinel for the binary so verbs like `sessions attach` / `open` can detect
# they're running under a shell that has the shim loaded. Mirrors the bash
# shim's export. Without this, those verbs emit `exec:`/`cd:` directives that
# nothing consumes — silent no-op.
$env:BRIDGE_SHIM_LOADED = "1"

function bridge {
    $directive = & bridge.exe __preflight @args
    if ($LASTEXITCODE -ne 0) { return }
    switch -Regex ($directive) {
        '^cd:(.+)$'   { Set-Location $matches[1] }
        '^exec:(.+)$' {
            $parts = $matches[1] -split ' ', 2
            $cmd, $rest = $parts[0], $parts[1]
            Start-Process -FilePath $cmd -ArgumentList $rest -NoNewWindow -Wait
        }
        '^noop$'      { & bridge.exe @args }
        default       {
            Write-Error "bridge: unknown directive: $directive"
        }
    }
}
