#!/usr/bin/env bash
# Smoke test for the (forge, owner) dedup in _bridge_focus_list.
#
# Regression: a previous version dedupes on (owner, rel), so an owner with
# both public/ and private/ subdirs survived twice and the two parallel
# subshells raced to write the same tmpfile (see PR #15 review).
#
# Strategy: build a synthetic base dir with one GitHub owner that has both
# `public/` and `private/` .envrc'd subdirs, source bridge.sh against it,
# then re-apply the dedup awk from `_bridge_focus_list` to `_bridge_targets`
# output and assert exactly one row per owner.
#
# Runs offline — no API calls, no network.

set -u

_HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
_REPO_ROOT="$(cd "$_HERE/.." && pwd)"

_tmp=$(mktemp -d)
trap 'rm -rf "$_tmp"' EXIT

# Synthetic base: github owner "acme" with both public/ + private/ visibility
# directories, plus a single-subdir owner "solo" as a control. Each visibility
# dir needs a `.envrc` so `_bridge_targets` picks it up.
mkdir -p "$_tmp/github/acme/public"  && : > "$_tmp/github/acme/public/.envrc"
mkdir -p "$_tmp/github/acme/private" && : > "$_tmp/github/acme/private/.envrc"
mkdir -p "$_tmp/github/solo/public"  && : > "$_tmp/github/solo/public/.envrc"

# Source bridge.sh in a subshell pinned to this base.
out=$(
  BRIDGE_BASE="$_tmp" \
  bash -c "
    set -u
    . '$_REPO_ROOT/bridge.sh' >/dev/null 2>&1
    _bridge_targets | awk -F'\t' '\$2==\"github\" {
      key = \$2 \"\\t\" \$3
      if (!(key in seen)) { seen[key] = 1; print \$1 \"\\t\" \$3 }
    }'
  "
) || { echo "FAIL: sourcing bridge.sh failed" >&2; exit 1; }

# Expect exactly two pairs: one for acme, one for solo. The acme pair must
# resolve to a *single* row even though _bridge_targets emits two for it.
acme_count=$(printf '%s\n' "$out" | awk -F'\t' '$2=="acme"' | grep -c '^' || true)
solo_count=$(printf '%s\n' "$out" | awk -F'\t' '$2=="solo"' | grep -c '^' || true)

fail=0
if [ "$acme_count" != 1 ]; then
  echo "FAIL: expected 1 row for owner 'acme' after dedup, got $acme_count" >&2
  echo "      offending output:" >&2
  printf '        %s\n' "$out" >&2
  fail=1
fi
if [ "$solo_count" != 1 ]; then
  echo "FAIL: expected 1 row for owner 'solo' after dedup, got $solo_count" >&2
  fail=1
fi

# Sanity: the rel column for acme should be one of the two valid rels under it.
acme_rel=$(printf '%s\n' "$out" | awk -F'\t' '$2=="acme" {print $1}')
case "$acme_rel" in
  github/acme/public|github/acme/private) : ;;
  *) echo "FAIL: acme rel column unexpected: '$acme_rel'" >&2; fail=1 ;;
esac

if [ "$fail" -ne 0 ]; then
  exit 1
fi

echo "PASS"
