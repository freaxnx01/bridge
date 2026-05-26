# bridge-shim.ps1 — dot-sourced from $PROFILE once Phase 3 cuts over.
function bridge {
    $directive = & bridge-go.exe __preflight @args
    if ($LASTEXITCODE -ne 0) { return }
    switch -Regex ($directive) {
        '^cd:(.+)$'   { Set-Location $matches[1] }
        '^exec:(.+)$' {
            $parts = $matches[1] -split ' ', 2
            $cmd, $rest = $parts[0], $parts[1]
            Start-Process -FilePath $cmd -ArgumentList $rest -NoNewWindow -Wait
        }
        '^noop$'      { & bridge-go.exe @args }
        default       {
            Write-Error "bridge: unknown directive: $directive"
        }
    }
}
