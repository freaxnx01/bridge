#!/usr/bin/env bash
# Cheap pre-bats sanity suite: shellcheck + a few runtime smoke tests.
# Catches "script broken at parse time" without the bats dependency.

set -euo pipefail

SCRIPT_DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )"
REPO_ROOT="$( cd "$SCRIPT_DIR/.." && pwd )"

red()   { printf '\033[31m%s\033[0m\n' "$*"; }
green() { printf '\033[32m%s\033[0m\n' "$*"; }
blue()  { printf '\033[34m%s\033[0m\n' "$*"; }

fail=0

blue "==> shellcheck on shell scripts (errors only)"
if command -v shellcheck >/dev/null; then
  # -s bash: bridge.sh has no shebang (it's sourced); tell shellcheck the dialect.
  # --severity=error: only block on real errors. Tighten as the codebase is cleaned up.
  if shellcheck -s bash --severity=error -x \
      "$REPO_ROOT/bridge.sh" \
      "$REPO_ROOT/bridge-autosync.sh" \
      "$REPO_ROOT/bridge-unpushed-warn.sh" \
      "$REPO_ROOT/bridge-watcher.sh" \
      "$REPO_ROOT/setup-claude-channels.sh"; then
    green "shellcheck OK"
  else
    red "shellcheck FAILED"
    fail=1
  fi
else
  red "shellcheck not installed — skipping (install with: apt install shellcheck)"
fi

blue "==> sourcing bridge.sh and probing --version / --help"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
export BRIDGE_CACHE="$TMP/cache" BRIDGE_CONFIG="$TMP/config" BRIDGE_BASE="$TMP/repos"
mkdir -p "$BRIDGE_CACHE" "$BRIDGE_CONFIG" "$BRIDGE_BASE"

# Run in a fresh bash so we don't pollute the caller's shell state.
if bash -c "source '$REPO_ROOT/bridge.sh' && bridge --version" | grep -qE 'bridge [0-9]+\.[0-9]+\.[0-9]+'; then
  green "bridge --version OK"
else
  red "bridge --version FAILED"
  fail=1
fi

if bash -c "source '$REPO_ROOT/bridge.sh' && bridge --help" | grep -q '^Usage: bridge'; then
  green "bridge --help OK"
else
  red "bridge --help FAILED"
  fail=1
fi

if [ "$fail" -ne 0 ]; then
  red "==> smoke FAILED"
  exit 1
fi
green "==> smoke OK"
