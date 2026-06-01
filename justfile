default:
    @just --list

# Pull latest, rebuild + reinstall bridge (binary + shim), print version.
build:
    git pull
    make install
    bridge --version

# Sync current branch with its remote: rebase onto upstream, then push local commits.
sync:
    git pull --rebase --autostash
    git push

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
