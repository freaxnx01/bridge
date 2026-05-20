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
  # -s bash: clrepo.sh has no shebang (it's sourced); tell shellcheck the dialect.
  # --severity=error: only block on real errors. Tighten as the codebase is cleaned up.
  if shellcheck -s bash --severity=error -x \
      "$REPO_ROOT/clrepo.sh" \
      "$REPO_ROOT/clrepo-autosync.sh" \
      "$REPO_ROOT/clrepo-unpushed-warn.sh" \
      "$REPO_ROOT/clrepo-watcher.sh" \
      "$REPO_ROOT/setup-claude-channels.sh"; then
    green "shellcheck OK"
  else
    red "shellcheck FAILED"
    fail=1
  fi
else
  red "shellcheck not installed — skipping (install with: apt install shellcheck)"
fi

blue "==> sourcing clrepo.sh and probing --version / --help"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
export CLREPO_CACHE="$TMP/cache" CLREPO_CONFIG="$TMP/config" CLREPO_BASE="$TMP/repos"
mkdir -p "$CLREPO_CACHE" "$CLREPO_CONFIG" "$CLREPO_BASE"

# Run in a fresh bash so we don't pollute the caller's shell state.
if bash -c "source '$REPO_ROOT/clrepo.sh' && clrepo --version" | grep -qE 'clrepo [0-9]+\.[0-9]+\.[0-9]+'; then
  green "clrepo --version OK"
else
  red "clrepo --version FAILED"
  fail=1
fi

if bash -c "source '$REPO_ROOT/clrepo.sh' && clrepo --help" | grep -q '^Usage: clrepo'; then
  green "clrepo --help OK"
else
  red "clrepo --help FAILED"
  fail=1
fi

if [ "$fail" -ne 0 ]; then
  red "==> smoke FAILED"
  exit 1
fi
green "==> smoke OK"
