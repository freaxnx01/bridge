#!/usr/bin/env bash
# Smoke test for the -B/--base scope guarantee.
#
# Regression: an earlier draft of -B/--base mutated the global _BRIDGE_BASES
# directly via _bridge_collect_bases_with, with no save/restore. The flag's
# "for this invocation only" promise was broken — the override silently
# persisted across subsequent bridge calls in the same shell.
#
# Fix: bridge() declares `local -a _BRIDGE_BASES=("${_BRIDGE_BASES[@]}")` (and
# the matching local for _BRIDGE_BASE), so bash dynamic scoping makes the
# override hit the local shadow that disappears when the function returns.

set -u

_HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
_REPO_ROOT="$(cd "$_HERE/.." && pwd)"

# Use a scratch base dir that exists so the override survives the
# missing-dir warn-and-skip in _bridge_collect_bases.
_scratch=$(mktemp -d)
trap 'rmdir "$_scratch" 2>/dev/null || true' EXIT

# Single subshell so locals/globals share state across calls. tag=value
# lines keep parsing trivial and robust to leading whitespace.
out=$(
  bash -c "
    set -u
    . '$_REPO_ROOT/bridge.sh' >/dev/null 2>&1
    printf 'pre=%s\n'  \"\${_BRIDGE_BASES[*]}\"
    bridge -B '$_scratch' -V >/dev/null 2>&1 || true
    printf 'post=%s\n' \"\${_BRIDGE_BASES[*]}\"
    bridge -V >/dev/null 2>&1 || true
    printf 'next=%s\n' \"\${_BRIDGE_BASES[*]}\"
  "
) || { echo "FAIL: sourcing bridge.sh failed" >&2; exit 1; }

pre=${out#*pre=};  pre=${pre%%$'\n'*}
post=${out#*post=}; post=${post%%$'\n'*}
next=${out#*next=}; next=${next%%$'\n'*}

fail=0
if [ "$post" != "$pre" ]; then
  echo "FAIL: _BRIDGE_BASES leaked after -B override" >&2
  printf '       pre  = %q\n       post = %q\n' "$pre" "$post" >&2
  fail=1
fi
if [ "$next" != "$pre" ]; then
  echo "FAIL: _BRIDGE_BASES still leaked on subsequent bare call" >&2
  printf '       pre  = %q\n       next = %q\n' "$pre" "$next" >&2
  fail=1
fi

# Sanity: confirm the override actually takes effect inside a function-
# scoped local-shadow context. _probe mirrors the pattern bridge() uses;
# inside _probe, _BRIDGE_BASES should equal $_scratch; after _probe
# returns, the outer scope must be unchanged.
during=$(
  bash -c "
    set -u
    . '$_REPO_ROOT/bridge.sh' >/dev/null 2>&1
    _probe() {
      local -a _BRIDGE_BASES=(\"\${_BRIDGE_BASES[@]}\")
      local _BRIDGE_BASE=\"\$_BRIDGE_BASE\"
      _bridge_collect_bases_with '$_scratch'
      printf 'inside=%s\n' \"\${_BRIDGE_BASES[*]}\"
    }
    _probe
    printf 'outside=%s\n' \"\${_BRIDGE_BASES[*]}\"
  "
)
inside=${during#*inside=};   inside=${inside%%$'\n'*}
outside=${during#*outside=}; outside=${outside%%$'\n'*}

if [ "$inside" != "$_scratch" ]; then
  echo "FAIL: override did not take effect inside the local-shadow function" >&2
  printf '       expected: %q\n       got:      %q\n' "$_scratch" "$inside" >&2
  fail=1
fi
if [ "$outside" = "$_scratch" ]; then
  echo "FAIL: probe local leaked to the caller — sanity check broken" >&2
  fail=1
fi

if [ "$fail" -ne 0 ]; then
  exit 1
fi
echo "PASS"
