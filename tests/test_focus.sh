#!/usr/bin/env bash
# Tests for the full focus-flag implementation (#9).
# Run: bash tests/test_focus.sh
set -u

_HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
_ROOT="$(cd "$_HERE/.." && pwd)"

_tmpbase=$(mktemp -d)
_cache_dir=$(mktemp -d)
trap 'rm -rf "$_tmpbase" "$_cache_dir"' EXIT

# Source bridge.sh with a synthetic environment so module-load code doesn't fail.
BRIDGE_BASE="$_tmpbase" BRIDGE_CACHE="$_cache_dir" \
  . "$_ROOT/bridge.sh" >/dev/null 2>&1 || true
: "${_BRIDGE_FOCUS_CACHE:="$_cache_dir/focus.json"}"
: "${_BRIDGE_FOCUS_TTL:=3600}"

_fail=0
assert_eq() {
  local label="$1" want="$2" got="$3"
  if [ "$want" = "$got" ]; then
    printf 'ok  %s\n' "$label"
  else
    printf 'FAIL %s\n  want: %q\n  got:  %q\n' "$label" "$want" "$got" >&2
    _fail=$((_fail + 1))
  fi
}
assert_contains() {
  local label="$1" needle="$2" haystack="$3"
  if printf '%s\n' "$haystack" | grep -qF "$needle"; then
    printf 'ok  %s\n' "$label"
  else
    printf 'FAIL %s\n  expected to find: %q\n  in: %q\n' "$label" "$needle" "$haystack" >&2
    _fail=$((_fail + 1))
  fi
}
assert_not_contains() {
  local label="$1" needle="$2" haystack="$3"
  if ! printf '%s\n' "$haystack" | grep -qF "$needle"; then
    printf 'ok  %s\n' "$label"
  else
    printf 'FAIL %s\n  expected NOT to find: %q\n  in: %q\n' "$label" "$needle" "$haystack" >&2
    _fail=$((_fail + 1))
  fi
}

# --- Test 1: display_cache basic round-trip ---
cat > "$_BRIDGE_FOCUS_CACHE" <<'JSON'
{
  "fetched_at": 9999999999,
  "repos": [
    {"platform":"GH","name":"owner/myrepo","url":"https://github.com/owner/myrepo","open":3,"mine":1},
    {"platform":"FJ","name":"freax/homelab","url":"https://git.home.freaxnx01.ch/freax/homelab","open":0,"mine":0}
  ],
  "warnings": []
}
JSON
out=$(_bridge_focus_display_cache 2>&1)
assert_contains "display: GH repo name"    "owner/myrepo"     "$out"
assert_contains "display: FJ repo name"    "freax/homelab"    "$out"
assert_contains "display: GH url"          "github.com"       "$out"
assert_contains "display: FJ url"          "freaxnx01.ch"     "$out"
assert_contains "display: issue count"     "3 open"           "$out"
assert_contains "display: mine count"      "1 yours"          "$out"
assert_contains "display: zero open"       "0 open"           "$out"
assert_contains "display: summary footer"  "2 focus repos"    "$out"

# --- Test 2: display_cache — open=-1 shows "? open" ---
cat > "$_BRIDGE_FOCUS_CACHE" <<'JSON'
{
  "fetched_at": 9999999999,
  "repos": [
    {"platform":"GH","name":"owner/broken","url":"https://github.com/owner/broken","open":-1,"mine":-1}
  ],
  "warnings": []
}
JSON
out=$(_bridge_focus_display_cache 2>&1)
assert_contains "display: unknown count"  "? open"  "$out"

# --- Test 3: display_cache — warning footer rendered ---
cat > "$_BRIDGE_FOCUS_CACHE" <<'JSON'
{
  "fetched_at": 9999999999,
  "repos": [{"platform":"GH","name":"o/r","url":"https://github.com/o/r","open":0,"mine":0}],
  "warnings": ["Forgejo: skipped (no FORGEJO_TOKEN)"]
}
JSON
out=$(_bridge_focus_display_cache 2>&1)
assert_contains "display: warning line"  "[!] Forgejo: skipped"  "$out"

# --- Test 4: focus_list — valid cache is used (no API call) ---
cat > "$_BRIDGE_FOCUS_CACHE" <<JSON
{
  "fetched_at": $(date +%s),
  "repos": [{"platform":"GH","name":"owner/cached","url":"https://github.com/owner/cached","open":1,"mine":0}],
  "warnings": []
}
JSON
out=$(_bridge_focus_list 0 2>&1)
assert_contains "TTL: valid cache used"  "owner/cached"  "$out"

# --- Test 5: focus_list — stale cache is NOT used ---
cat > "$_BRIDGE_FOCUS_CACHE" <<JSON
{
  "fetched_at": $(( $(date +%s) - ${_BRIDGE_FOCUS_TTL} - 60 )),
  "repos": [{"platform":"GH","name":"owner/stale","url":"https://github.com/owner/stale","open":1,"mine":0}],
  "warnings": []
}
JSON
# No real targets in $_tmpbase, so fetch produces empty → "no focus repos" path
out=$(_bridge_focus_list 0 2>&1)
assert_not_contains "TTL expiry: stale cache bypassed"  "owner/stale"  "$out"

# --- Test 6: focus_list — --no-cache bypasses valid cache ---
cat > "$_BRIDGE_FOCUS_CACHE" <<JSON
{
  "fetched_at": $(date +%s),
  "repos": [{"platform":"GH","name":"owner/should-skip","url":"https://github.com/owner/should-skip","open":0,"mine":0}],
  "warnings": []
}
JSON
out=$(_bridge_focus_list 1 2>&1)
assert_not_contains "no-cache: bypasses valid cache"  "owner/should-skip"  "$out"

# --- Test 7: focus_add invalidates cache ---
cat > "$_BRIDGE_FOCUS_CACHE" <<'JSON'
{"fetched_at":9999999999,"repos":[],"warnings":[]}
JSON
# Stub helpers so no real API call is made
_bridge_focus_resolve() { printf 'github/fake/public\n'; return 0; }
_bridge_focus_toggle_gh() { return 0; }
_bridge_focus_add "fakerepo" >/dev/null 2>&1
if [ ! -f "$_BRIDGE_FOCUS_CACHE" ]; then
  printf 'ok  cache invalidated on focus_add\n'
else
  printf 'FAIL cache still present after focus_add\n' >&2
  _fail=$((_fail + 1))
fi

# --- Test 8: focus_rm invalidates cache ---
cat > "$_BRIDGE_FOCUS_CACHE" <<'JSON'
{"fetched_at":9999999999,"repos":[],"warnings":[]}
JSON
_bridge_focus_resolve() { printf 'github/fake/public\n'; return 0; }
_bridge_focus_toggle_gh() { return 0; }
_bridge_focus_rm "fakerepo" >/dev/null 2>&1
if [ ! -f "$_BRIDGE_FOCUS_CACHE" ]; then
  printf 'ok  cache invalidated on focus_rm\n'
else
  printf 'FAIL cache still present after focus_rm\n' >&2
  _fail=$((_fail + 1))
fi

if [ "$_fail" -gt 0 ]; then
  echo "FAILED ($_fail)" >&2; exit 1
fi
echo "PASS"
