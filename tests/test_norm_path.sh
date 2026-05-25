#!/usr/bin/env bash
# Self-contained tests for the path helpers in bridge.sh.
# Run: bash tests/test_norm_path.sh
set -u

_HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck disable=SC1091
. "$_HERE/../bridge.sh" >/dev/null 2>&1 || true

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

# --- _bridge_is_windows ---
( OSTYPE=linux-gnu;  _bridge_is_windows && exit 1 || exit 0 ) && \
  echo "ok  _bridge_is_windows: linux-gnu => false" || \
  { echo "FAIL _bridge_is_windows: linux-gnu should be false" >&2; _fail=$((_fail+1)); }

( OSTYPE=msys;       _bridge_is_windows && exit 0 || exit 1 ) && \
  echo "ok  _bridge_is_windows: msys => true" || \
  { echo "FAIL _bridge_is_windows: msys should be true" >&2; _fail=$((_fail+1)); }

( OSTYPE=cygwin;     _bridge_is_windows && exit 0 || exit 1 ) && \
  echo "ok  _bridge_is_windows: cygwin => true" || \
  { echo "FAIL _bridge_is_windows: cygwin should be true" >&2; _fail=$((_fail+1)); }

# --- _bridge_norm_path: no-op on POSIX ---
got=$(OSTYPE=linux-gnu _bridge_norm_path '/home/me/repos')
assert_eq "norm_path posix passthrough"     '/home/me/repos'   "$got"

got=$(OSTYPE=linux-gnu _bridge_norm_path 'C:\Develop\Repos')
assert_eq "norm_path posix no convert"      'C:\Develop\Repos' "$got"

# --- _bridge_norm_path: Windows-style on Windows (with cygpath shim) ---
_tmpdir=$(mktemp -d)
cat >"$_tmpdir/cygpath" <<'SH'
#!/usr/bin/env bash
case "$1" in
  -u)
    in="$2"
    in="${in//\\//}"
    drive="${in%%:*}"
    rest="${in#*:}"
    if [ "$drive" != "$in" ] && [ ${#drive} = 1 ]; then
      drive_lc=$(printf '%s' "$drive" | tr '[:upper:]' '[:lower:]')
      printf '/%s%s\n' "$drive_lc" "$rest"
    else
      printf '%s\n' "$in"
    fi
    ;;
  -w)
    in="$2"
    if [[ "$in" =~ ^/([a-z])/(.*)$ ]]; then
      drive_uc=$(printf '%s' "${BASH_REMATCH[1]}" | tr '[:lower:]' '[:upper:]')
      rest=${BASH_REMATCH[2]//\//\\}
      printf '%s:\\%s\n' "$drive_uc" "$rest"
    else
      printf '%s\n' "$in"
    fi
    ;;
  *) printf '%s\n' "$2" ;;
esac
SH
chmod +x "$_tmpdir/cygpath"

got=$(OSTYPE=msys PATH="$_tmpdir:$PATH" _bridge_norm_path 'C:\Develop\Repos')
assert_eq "norm_path windows backslash"     '/c/Develop/Repos' "$got"

got=$(OSTYPE=msys PATH="$_tmpdir:$PATH" _bridge_norm_path 'C:/Develop/Repos')
assert_eq "norm_path windows forward-slash" '/c/Develop/Repos' "$got"

got=$(OSTYPE=msys PATH="$_tmpdir:$PATH" _bridge_norm_path '/c/Develop/Repos')
assert_eq "norm_path windows already posix" '/c/Develop/Repos' "$got"

# --- _bridge_norm_path: pure-Bash fallback when cygpath missing ---
got=$(OSTYPE=msys PATH=/usr/bin:/bin _bridge_norm_path 'C:\Develop\Repos')
assert_eq "norm_path fallback (no cygpath)" '/c/Develop/Repos' "$got"

# --- _bridge_display_path ---
got=$(OSTYPE=linux-gnu _bridge_display_path '/home/me/repos')
assert_eq "display_path posix passthrough"  '/home/me/repos'   "$got"

got=$(OSTYPE=msys PATH="$_tmpdir:$PATH" _bridge_display_path '/c/Develop/Repos')
assert_eq "display_path windows -> C:\\"    'C:\Develop\Repos' "$got"

rm -rf "$_tmpdir"

if [ "$_fail" -gt 0 ]; then
  echo "FAILED ($_fail)" >&2
  exit 1
fi
echo "PASS"
