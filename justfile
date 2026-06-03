set windows-shell := ["pwsh.exe", "-NoLogo", "-Command"]

default:
    @just --list

# Pull latest, rebuild + reinstall bridge (binary + shim), print version.
[unix]
build:
    git pull
    make install
    bridge --version

[windows]
build:
    #!pwsh.exe
    git pull
    $ver = git describe --tags --always --dirty 2>$null
    if (-not $ver) { $ver = 'dev' }
    $cmt = git rev-parse --short HEAD 2>$null
    if (-not $cmt) { $cmt = 'none' }
    $dt = (Get-Date).ToUniversalTime().ToString('yyyy-MM-ddTHH:mm:ssZ')
    go build -ldflags "-X main.version=$ver -X main.commit=$cmt -X main.date=$dt" -o bridge.exe ./cmd/bridge
    if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }
    $dest = Join-Path $env:USERPROFILE '.local\bin'
    New-Item -ItemType Directory -Force -Path $dest | Out-Null
    Copy-Item -Force bridge.exe "$dest\bridge.exe"
    Copy-Item -Force shims\bridge-shim.ps1 "$dest\bridge.ps1"
    Write-Host "Bridge installed to $dest"
    & "$dest\bridge.exe" --version

# Sync current branch with its remote: rebase onto upstream, then push local commits.
sync:
    git pull --rebase --autostash
    git push

# Sync current branch with remote, then rebuild + reinstall bridge.
sync-build: sync build

# Run Go + shim tests.
test:
    make all

# Run Go tests with per-test streaming output (no shim tests).
test-verbose:
    go test -v ./...

# Install the Go toolchain version declared in go.mod into ~/.local/go.
install-go-toolchain:
    #!/usr/bin/env bash
    set -euo pipefail
    needed="$(awk '/^go [0-9]/ {print $2; exit}' go.mod)"
    have="$(go version 2>/dev/null | awk '{print $3}' | sed 's/^go//' || true)"
    if [ "${have:-}" = "$needed" ]; then
        echo "Go $needed already on PATH ($(command -v go))."
        exit 0
    fi
    os="$(uname -s | tr '[:upper:]' '[:lower:]')"
    arch="$(uname -m)"
    case "$arch" in
        x86_64|amd64) arch=amd64 ;;
        aarch64|arm64) arch=arm64 ;;
        *) echo "Unsupported arch: $arch" >&2; exit 1 ;;
    esac
    tarball="go${needed}.${os}-${arch}.tar.gz"
    url="https://go.dev/dl/${tarball}"
    dest="$HOME/.local/go"
    tmp="$(mktemp -d)"
    trap 'rm -rf "$tmp"' EXIT
    echo "Downloading $url"
    curl -fsSL "$url" -o "$tmp/$tarball"
    echo "Installing to $dest"
    rm -rf "$dest"
    mkdir -p "$(dirname "$dest")"
    tar -C "$(dirname "$dest")" -xzf "$tmp/$tarball"
    echo
    echo "Installed Go $needed to $dest."
    echo "Add this to your shell profile (then restart the shell):"
    echo '    export PATH="$HOME/.local/go/bin:$PATH"'
