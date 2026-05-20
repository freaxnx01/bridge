# Focus Flag — Full Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Complete the remaining #9 acceptance criteria: Forgejo support (list/add/rm), per-repo open-issue counts with "assigned to me", JSON caching with 1-hour TTL and invalidation, `--no-cache` bypass, `clrepo -f <name>` open-by-name, tab completion over the focus list, and partial-failure warning footer.

**Architecture:** Monolithic expansion of `clrepo.sh`. `_clrepo_focus_list` is rewritten to run two parallel fan-outs (repo fetch, then count fetch), write a JSON cache, and call a new `_clrepo_focus_display_cache` helper. `_clrepo_focus_toggle_fj` is added for Forgejo add/rm. `-f` becomes a mode flag so `--no-cache` and a positional `<name>` can follow it. Tab completion in `_clrepo()` reads cached focus names when the previous token was `-f`.

**Tech Stack:** Bash 4+, jq, gh CLI, curl, direnv.

**Spec:** `docs/superpowers/specs/2026-05-20-focus-flag-full-design.md`

---

## File Map

| File | Change |
|---|---|
| `clrepo.sh:186` | Add `_CLREPO_FOCUS_CACHE` + `_CLREPO_FOCUS_TTL` constants |
| `clrepo.sh:1960–2104` | Add `_clrepo_focus_display_cache`, `_clrepo_focus_toggle_fj`; replace `_clrepo_focus_add`/`_clrepo_focus_rm`/`_clrepo_focus_list` |
| `clrepo.sh:2827–2828` | Add `mode_focus=0 focus_no_cache=0` to local declarations |
| `clrepo.sh:2876` | Change `-f` from early-return to mode flag; add `--no-cache` case |
| `clrepo.sh:3068` | Add `mode_focus` guard to CWD-launch condition |
| `clrepo.sh:~3076` | Add `mode_focus` block after `mode_repo_issues` block |
| `clrepo.sh:2982–3020` | Add `mode_focus` to `--attach` and `--pick` conflict checks |
| `clrepo.sh:~2900` | Update `--help` text for `-f` and `--no-cache` |
| `clrepo.sh:3236–3243` | Add focus-name completion in `_clrepo()`; add `--no-cache` to flags string |
| `clrepo.sh:25` | Bump version `1.40.2 → 1.41.0` |
| `CHANGELOG.md` | Add `[1.41.0]` entry |
| `tests/test_focus.sh` | New: cache round-trip, TTL, `--no-cache`, invalidation, warning display |

---

### Task 1: Write failing tests

**Files:**
- Create: `tests/test_focus.sh`

- [ ] **Step 1: Create `tests/test_focus.sh`**

```bash
#!/usr/bin/env bash
# Tests for the full focus-flag implementation (#9).
# Run: bash tests/test_focus.sh
set -u

_HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
_ROOT="$(cd "$_HERE/.." && pwd)"

_tmpbase=$(mktemp -d)
_cache_dir=$(mktemp -d)
trap 'rm -rf "$_tmpbase" "$_cache_dir"' EXIT

# Source clrepo.sh with a synthetic environment so module-load code doesn't fail.
CLREPO_BASE="$_tmpbase" CLREPO_CACHE="$_cache_dir" \
  . "$_ROOT/clrepo.sh" >/dev/null 2>&1 || true

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
cat > "$_CLREPO_FOCUS_CACHE" <<'JSON'
{
  "fetched_at": 9999999999,
  "repos": [
    {"platform":"GH","name":"owner/myrepo","url":"https://github.com/owner/myrepo","open":3,"mine":1},
    {"platform":"FJ","name":"freax/homelab","url":"https://git.home.freaxnx01.ch/freax/homelab","open":0,"mine":0}
  ],
  "warnings": []
}
JSON
out=$(_clrepo_focus_display_cache 2>&1)
assert_contains "display: GH repo name"    "owner/myrepo"     "$out"
assert_contains "display: FJ repo name"    "freax/homelab"    "$out"
assert_contains "display: GH url"          "github.com"       "$out"
assert_contains "display: FJ url"          "freaxnx01.ch"     "$out"
assert_contains "display: issue count"     "3 open"           "$out"
assert_contains "display: mine count"      "1 yours"          "$out"
assert_contains "display: zero open"       "0 open"           "$out"
assert_contains "display: summary footer"  "2 focus repos"    "$out"

# --- Test 2: display_cache — open=-1 shows "? open" ---
cat > "$_CLREPO_FOCUS_CACHE" <<'JSON'
{
  "fetched_at": 9999999999,
  "repos": [
    {"platform":"GH","name":"owner/broken","url":"https://github.com/owner/broken","open":-1,"mine":-1}
  ],
  "warnings": []
}
JSON
out=$(_clrepo_focus_display_cache 2>&1)
assert_contains "display: unknown count"  "? open"  "$out"

# --- Test 3: display_cache — warning footer rendered ---
cat > "$_CLREPO_FOCUS_CACHE" <<'JSON'
{
  "fetched_at": 9999999999,
  "repos": [{"platform":"GH","name":"o/r","url":"https://github.com/o/r","open":0,"mine":0}],
  "warnings": ["Forgejo: skipped (no FORGEJO_TOKEN)"]
}
JSON
out=$(_clrepo_focus_display_cache 2>&1)
assert_contains "display: warning line"  "[!] Forgejo: skipped"  "$out"

# --- Test 4: focus_list — valid cache is used (no API call) ---
cat > "$_CLREPO_FOCUS_CACHE" <<JSON
{
  "fetched_at": $(date +%s),
  "repos": [{"platform":"GH","name":"owner/cached","url":"https://github.com/owner/cached","open":1,"mine":0}],
  "warnings": []
}
JSON
out=$(_clrepo_focus_list 0 2>&1)
assert_contains "TTL: valid cache used"  "owner/cached"  "$out"

# --- Test 5: focus_list — stale cache is NOT used ---
cat > "$_CLREPO_FOCUS_CACHE" <<JSON
{
  "fetched_at": $(( $(date +%s) - ${_CLREPO_FOCUS_TTL} - 60 )),
  "repos": [{"platform":"GH","name":"owner/stale","url":"https://github.com/owner/stale","open":1,"mine":0}],
  "warnings": []
}
JSON
# No real targets in $_tmpbase, so fetch produces empty → "no focus repos" path
out=$(_clrepo_focus_list 0 2>&1)
assert_not_contains "TTL expiry: stale cache bypassed"  "owner/stale"  "$out"

# --- Test 6: focus_list — --no-cache bypasses valid cache ---
cat > "$_CLREPO_FOCUS_CACHE" <<JSON
{
  "fetched_at": $(date +%s),
  "repos": [{"platform":"GH","name":"owner/should-skip","url":"https://github.com/owner/should-skip","open":0,"mine":0}],
  "warnings": []
}
JSON
out=$(_clrepo_focus_list 1 2>&1)
assert_not_contains "no-cache: bypasses valid cache"  "owner/should-skip"  "$out"

# --- Test 7: focus_add invalidates cache ---
cat > "$_CLREPO_FOCUS_CACHE" <<'JSON'
{"fetched_at":9999999999,"repos":[],"warnings":[]}
JSON
# Stub helpers so no real API call is made
_clrepo_focus_resolve() { printf 'github/fake/public\n'; return 0; }
_clrepo_focus_toggle_gh() { return 0; }
_clrepo_focus_add "fakerepo" >/dev/null 2>&1
if [ ! -f "$_CLREPO_FOCUS_CACHE" ]; then
  printf 'ok  cache invalidated on focus_add\n'
else
  printf 'FAIL cache still present after focus_add\n' >&2
  _fail=$((_fail + 1))
fi

# --- Test 8: focus_rm invalidates cache ---
cat > "$_CLREPO_FOCUS_CACHE" <<'JSON'
{"fetched_at":9999999999,"repos":[],"warnings":[]}
JSON
_clrepo_focus_resolve() { printf 'github/fake/public\n'; return 0; }
_clrepo_focus_toggle_gh() { return 0; }
_clrepo_focus_rm "fakerepo" >/dev/null 2>&1
if [ ! -f "$_CLREPO_FOCUS_CACHE" ]; then
  printf 'ok  cache invalidated on focus_rm\n'
else
  printf 'FAIL cache still present after focus_rm\n' >&2
  _fail=$((_fail + 1))
fi

if [ "$_fail" -gt 0 ]; then
  echo "FAILED ($_fail)" >&2; exit 1
fi
echo "PASS"
```

- [ ] **Step 2: Run tests — confirm they fail**

```
bash tests/test_focus.sh
```

Expected: several FAIL lines (functions `_clrepo_focus_display_cache`, `_CLREPO_FOCUS_CACHE` not yet defined). Exit non-zero.

- [ ] **Step 3: Commit the failing tests**

```bash
git add tests/test_focus.sh
git commit -m "test: add failing tests for focus-flag full implementation"
```

---

### Task 2: Constants + `_clrepo_focus_display_cache`

**Files:**
- Modify: `clrepo.sh:186` (insert two lines after `_CLREPO_REMOTE_TTL`)
- Modify: `clrepo.sh:1958` (insert new function before `_clrepo_regex_escape`)

- [ ] **Step 1: Insert constants after line 186**

Find in `clrepo.sh`:
```bash
_CLREPO_REMOTE_TTL=600  # seconds
_CLREPO_UPDATE_TTL=86400  # seconds; staleness for latest-version cache
```

Replace with:
```bash
_CLREPO_REMOTE_TTL=600  # seconds
_CLREPO_UPDATE_TTL=86400  # seconds; staleness for latest-version cache
_CLREPO_FOCUS_CACHE="$_CLREPO_CACHE/focus.json"
_CLREPO_FOCUS_TTL="${CLREPO_FOCUS_TTL:-3600}"
```

- [ ] **Step 2: Insert `_clrepo_focus_display_cache` before `_clrepo_regex_escape`**

Find in `clrepo.sh`:
```bash
# _clrepo_regex_escape <string>
```

Insert immediately before it:
```bash
# Render the focus list from the JSON cache file. Called from
# _clrepo_focus_list (cache hit path) and directly in tests.
_clrepo_focus_display_cache() {
  local data n total_open total_mine warnings_out
  data=$(jq -r '
    .repos[] |
    [.platform, .name, .url, (.open | tostring), (.mine | tostring)] | @tsv
  ' "$_CLREPO_FOCUS_CACHE" 2>/dev/null)

  if [ -z "$data" ]; then
    echo "clrepo: no focus repos found." >&2
    echo "       Tag a repo via 'clrepo --focus-add <name>' or set the 'focus' topic in the platform UI." >&2
    return 0
  fi

  printf 'FOCUS REPOS\n'
  printf -- '─%.0s' {1..56}; printf '\n'

  n=0; total_open=0; total_mine=0
  while IFS=$'\t' read -r platform name url open mine; do
    n=$((n + 1))
    local count_str
    if [ "${open:--1}" -lt 0 ] 2>/dev/null; then
      count_str="? open"
    elif [ "${mine:-0}" -gt 0 ] 2>/dev/null; then
      count_str="$open open · $mine yours"
      total_open=$((total_open + open))
      total_mine=$((total_mine + mine))
    else
      count_str="$open open"
      total_open=$((total_open + ${open:-0}))
    fi
    printf '[%s]  %-36s  %s\n' "$platform" "$name" "$count_str"
    printf '      %s\n' "$url"
  done <<< "$data"

  printf -- '─%.0s' {1..56}; printf '\n'
  if [ "$n" -eq 1 ]; then
    printf '1 focus repo'
  else
    printf '%d focus repos' "$n"
  fi
  if [ "$total_open" -gt 0 ]; then
    printf ' · %d open issues' "$total_open"
    [ "$total_mine" -gt 0 ] && printf ' · %d assigned to you' "$total_mine"
  fi
  printf '\n'

  warnings_out=$(jq -r '.warnings[]?' "$_CLREPO_FOCUS_CACHE" 2>/dev/null)
  if [ -n "$warnings_out" ]; then
    while IFS= read -r w; do
      printf '  [!] %s\n' "$w"
    done <<< "$warnings_out"
  fi
}

```

- [ ] **Step 3: Run tests — display tests should pass**

```
bash tests/test_focus.sh
```

Expected: Tests 1–3 now print `ok`, Tests 4–8 still fail (TTL logic, `_clrepo_focus_list` signature, invalidation not yet wired). Overall still non-zero exit.

- [ ] **Step 4: Syntax check**

```
bash -n clrepo.sh && echo OK
```

Expected: `OK`

- [ ] **Step 5: Commit**

```bash
git add clrepo.sh
git commit -m "feat(clrepo): add focus cache constants and _clrepo_focus_display_cache"
```

---

### Task 3: `_clrepo_focus_toggle_fj` + update add/rm dispatch

**Files:**
- Modify: `clrepo.sh` — insert `_clrepo_focus_toggle_fj` after `_clrepo_focus_toggle_gh`; replace `_clrepo_focus_add` and `_clrepo_focus_rm`

- [ ] **Step 1: Insert `_clrepo_focus_toggle_fj` after `_clrepo_focus_toggle_gh` ends (after line ~2028)**

Find in `clrepo.sh` (end of `_clrepo_focus_toggle_gh`):
```bash
      echo "clrepo: removed 'focus' topic from $nwo"
    fi
  )
}

_clrepo_focus_add() {
```

Insert the new function between them:
```bash
      echo "clrepo: removed 'focus' topic from $nwo"
    fi
  )
}

# Add or remove the 'focus' topic on a Forgejo repo. $1 = rel path under
# git-forgejo/, $2 = "add" or "rm". Uses PUT/DELETE /api/v1/repos/freax/<name>/topics/focus.
_clrepo_focus_toggle_fj() {
  local rel="$1" action="$2"
  (
    cd "$(_clrepo_base_for_rel "$rel")/$rel" 2>/dev/null || exit 1
    command -v direnv >/dev/null && eval "$(direnv export bash 2>/dev/null)"
    [ -z "${FORGEJO_TOKEN:-}" ] && { echo "clrepo: no FORGEJO_TOKEN for Forgejo repo" >&2; exit 1; }
    local name method code
    name=$(basename "$rel")
    [ "$action" = "add" ] && method=PUT || method=DELETE
    code=$(curl -sf -o /dev/null -w '%{http_code}' -X "$method" \
      -H "Authorization: token $FORGEJO_TOKEN" \
      "https://git.home.freaxnx01.ch/api/v1/repos/freax/$name/topics/focus" 2>/dev/null)
    case "$code" in
      20[04]) ;;
      404) echo "clrepo: Forgejo repo 'freax/$name' not found" >&2; exit 1 ;;
      *)   echo "clrepo: Forgejo API error (HTTP $code) on freax/$name" >&2; exit 1 ;;
    esac
    if [ "$action" = "add" ]; then
      echo "clrepo: added 'focus' topic to freax/$name"
    else
      echo "clrepo: removed 'focus' topic from freax/$name"
    fi
  )
}

_clrepo_focus_add() {
```

- [ ] **Step 2: Replace `_clrepo_focus_add` (lines 2029–2037)**

Find:
```bash
_clrepo_focus_add() {
  local rel
  rel=$(_clrepo_focus_resolve "$1") || return 1
  case "$rel" in
    github/*) _clrepo_focus_toggle_gh "$rel" add ;;
    ado/*)    echo "clrepo: focus is unsupported for Azure DevOps. Open via 'clrepo -c $1'." >&2; return 1 ;;
    *)        echo "clrepo: focus not yet supported for '$rel' (Forgejo support deferred — see #9)." >&2; return 1 ;;
  esac
}
```

Replace with:
```bash
_clrepo_focus_add() {
  local rel
  rel=$(_clrepo_focus_resolve "$1") || return 1
  case "$rel" in
    github/*)      _clrepo_focus_toggle_gh "$rel" add || return 1 ;;
    git-forgejo/*) _clrepo_focus_toggle_fj "$rel" add || return 1 ;;
    ado/*)    echo "clrepo: focus is unsupported for Azure DevOps. Open via 'clrepo -c $1'." >&2; return 1 ;;
    *)        echo "clrepo: focus not supported for platform of '$rel'." >&2; return 1 ;;
  esac
  rm -f "$_CLREPO_FOCUS_CACHE"
}
```

- [ ] **Step 3: Replace `_clrepo_focus_rm` (lines 2039–2047)**

Find:
```bash
_clrepo_focus_rm() {
  local rel
  rel=$(_clrepo_focus_resolve "$1") || return 1
  case "$rel" in
    github/*) _clrepo_focus_toggle_gh "$rel" rm ;;
    ado/*)    echo "clrepo: focus is unsupported for Azure DevOps." >&2; return 1 ;;
    *)        echo "clrepo: focus not yet supported for '$rel' (Forgejo support deferred — see #9)." >&2; return 1 ;;
  esac
}
```

Replace with:
```bash
_clrepo_focus_rm() {
  local rel
  rel=$(_clrepo_focus_resolve "$1") || return 1
  case "$rel" in
    github/*)      _clrepo_focus_toggle_gh "$rel" rm || return 1 ;;
    git-forgejo/*) _clrepo_focus_toggle_fj "$rel" rm || return 1 ;;
    ado/*)    echo "clrepo: focus is unsupported for Azure DevOps." >&2; return 1 ;;
    *)        echo "clrepo: focus not supported for platform of '$rel'." >&2; return 1 ;;
  esac
  rm -f "$_CLREPO_FOCUS_CACHE"
}
```

- [ ] **Step 4: Run tests — invalidation tests (7 + 8) should now pass**

```
bash tests/test_focus.sh
```

Expected: Tests 1–3, 7–8 pass. Tests 4–6 still fail (TTL logic in `_clrepo_focus_list` not yet updated).

- [ ] **Step 5: Syntax check**

```
bash -n clrepo.sh && echo OK
```

- [ ] **Step 6: Commit**

```bash
git add clrepo.sh
git commit -m "feat(clrepo): add Forgejo focus toggle + wire add/rm dispatch with cache invalidation"
```

---

### Task 4: Rewrite `_clrepo_focus_list`

**Files:**
- Modify: `clrepo.sh:2054–2104` (replace existing `_clrepo_focus_list`)

This is the largest change. The function gains: cache read, Forgejo fetch, user resolution, per-repo issue counts, cache write, and partial-failure warnings. It replaces the existing function wholesale.

- [ ] **Step 1: Replace `_clrepo_focus_list` (lines 2054–2104)**

Find the entire existing function:
```bash
# List focus-tagged repos across all configured GitHub owners. Dedupes
# targets by (forge, owner) — matches the _clrepo_issues pattern, so an
# owner with both public/ and private/ subdirs spawns one job, not two.
# Tmpfiles use a monotonic counter to avoid sanitization collisions.
# Forgejo, issue counts, and caching are out of scope for the MVP — see #9.
_clrepo_focus_list() {
  local pairs
  pairs=$(_clrepo_targets \
    | awk -F'\t' '$2=="github" {
        key = $2 "\t" $3
        if (!(key in seen)) { seen[key] = 1; print $1 "\t" $3 }
      }')
  if [ -z "$pairs" ]; then
    echo "clrepo: no GitHub forge targets discovered under any of: ${_CLREPO_BASES[*]}" >&2
    return 1
  fi

  local tmpdir
  tmpdir=$(mktemp -d) || return 1
  # shellcheck disable=SC2064
  trap "rm -rf '$tmpdir'" RETURN

  local i=0
  while IFS=$'\t' read -r rel owner; do
    [ -z "$owner" ] && continue
    i=$((i + 1))
    (
      cd "$(_clrepo_base_for_rel "$rel")/$rel" 2>/dev/null || exit 0
      command -v direnv >/dev/null && eval "$(direnv export bash 2>/dev/null)"
      gh repo list "$owner" --topic focus --json nameWithOwner,url --limit 50 2>/dev/null \
        | jq -r '.[] | "GH\t\(.nameWithOwner)\t\(.url)"' \
        > "$tmpdir/$i"
    ) &
  done <<< "$pairs"
  wait

  local out
  out=$(cat "$tmpdir"/* 2>/dev/null | sort -u)
  if [ -z "$out" ]; then
    echo "clrepo: no focus repos found." >&2
    echo "       Tag a repo via 'clrepo --focus-add <name>' or set the 'focus' topic in the GitHub UI." >&2
    return 0
  fi

  printf 'FOCUS REPOS\n'
  printf -- '─%.0s' {1..56}; printf '\n'
  printf '%s\n' "$out" | awk -F'\t' '{ printf "[%s]  %-36s  %s\n", $1, $2, $3 }'
  printf -- '─%.0s' {1..56}; printf '\n'
  local n
  n=$(printf '%s\n' "$out" | wc -l)
  if [ "$n" = 1 ]; then
    printf '1 focus repo\n'
  else
    printf '%d focus repos\n' "$n"
  fi
}
```

Replace with:
```bash
# List focus-tagged repos across configured GH owners and Forgejo. Reads from
# the JSON cache when fresh; fetches and writes a new cache when stale.
# $1 = 1 to bypass cache (--no-cache); omit or 0 to use cache.
_clrepo_focus_list() {
  local force_refresh="${1:-0}"

  # --- Cache read ---
  if [ -f "$_CLREPO_FOCUS_CACHE" ] && [ "$force_refresh" != "1" ]; then
    local _age
    _age=$(( $(date +%s) - $(jq '.fetched_at // 0' "$_CLREPO_FOCUS_CACHE" 2>/dev/null || echo 0) ))
    if [ "$_age" -lt "$_CLREPO_FOCUS_TTL" ]; then
      _clrepo_focus_display_cache
      return
    fi
  fi

  local tmpdir
  tmpdir=$(mktemp -d) || return 1
  # shellcheck disable=SC2064
  trap "rm -rf '$tmpdir'" RETURN

  # --- Phase 1: fetch focus repo lists ---
  local pairs first_rel="" first_owner=""
  pairs=$(_clrepo_targets \
    | awk -F'\t' '$2=="github" {
        key = $2 "\t" $3
        if (!(key in seen)) { seen[key] = 1; print $1 "\t" $3 }
      }')
  [ -n "$pairs" ] && IFS=$'\t' read -r first_rel first_owner \
    <<< "$(printf '%s\n' "$pairs" | head -1)"

  local i=0
  if [ -n "$pairs" ]; then
    while IFS=$'\t' read -r rel owner; do
      [ -z "$owner" ] && continue
      i=$((i + 1))
      (
        cd "$(_clrepo_base_for_rel "$rel")/$rel" 2>/dev/null || exit 0
        command -v direnv >/dev/null && eval "$(direnv export bash 2>/dev/null)"
        gh repo list "$owner" --topic focus --json nameWithOwner,url --limit 50 2>/dev/null \
          | jq -r '.[] | "GH\t\(.nameWithOwner)\t\(.url)"' \
          > "$tmpdir/$i"
      ) &
    done <<< "$pairs"
  fi

  local fj_rel
  fj_rel=$(_clrepo_targets | awk -F'\t' '$2=="forgejo" { print $1; exit }')
  if [ -n "$fj_rel" ]; then
    i=$((i + 1))
    local fj_file="$tmpdir/$i"
    (
      cd "$(_clrepo_base_for_rel "$fj_rel")/$fj_rel" 2>/dev/null || exit 0
      command -v direnv >/dev/null && eval "$(direnv export bash 2>/dev/null)"
      if [ -z "${FORGEJO_TOKEN:-}" ]; then
        printf 'Forgejo: skipped (no FORGEJO_TOKEN)\n' > "$tmpdir/warn_fj"
        exit 0
      fi
      local result
      result=$(curl -sf -H "Authorization: token $FORGEJO_TOKEN" \
        "https://git.home.freaxnx01.ch/api/v1/repos/search?topic=true&q=focus&limit=50" \
        2>/dev/null) || {
          printf 'Forgejo: skipped (curl error)\n' > "$tmpdir/warn_fj"
          exit 0
        }
      printf '%s\n' "$result" \
        | jq -r '.data[] | "FJ\t\(.full_name)\t\(.html_url)"' \
        > "$fj_file"
    ) &
  fi

  wait

  local repos
  repos=$(cat "$tmpdir"/[0-9]* 2>/dev/null | grep -v '^[[:space:]]*$' | sort -u)

  local warn_list=()
  [ -f "$tmpdir/warn_fj" ] && warn_list+=("$(cat "$tmpdir/warn_fj")")

  if [ -z "$repos" ]; then
    echo "clrepo: no focus repos found." >&2
    echo "       Tag a repo via 'clrepo --focus-add <name>' or set the 'focus' topic in the platform UI." >&2
    return 0
  fi

  # --- Phase 2: resolve current users (once each) ---
  local gh_user="" fj_user=""
  if [ -n "$first_rel" ]; then
    gh_user=$(
      cd "$(_clrepo_base_for_rel "$first_rel")/$first_rel" 2>/dev/null || exit 0
      command -v direnv >/dev/null && eval "$(direnv export bash 2>/dev/null)"
      gh api user --jq .login 2>/dev/null || true
    )
  fi
  if [ -n "$fj_rel" ]; then
    fj_user=$(
      cd "$(_clrepo_base_for_rel "$fj_rel")/$fj_rel" 2>/dev/null || exit 0
      command -v direnv >/dev/null && eval "$(direnv export bash 2>/dev/null)"
      [ -z "${FORGEJO_TOKEN:-}" ] && exit 0
      curl -sf -H "Authorization: token $FORGEJO_TOKEN" \
        "https://git.home.freaxnx01.ch/api/v1/user" 2>/dev/null \
        | jq -r '.login // empty' || true
    )
  fi

  # --- Phase 3: per-repo issue counts (parallel) ---
  local count_idx=0
  while IFS=$'\t' read -r platform nwo url; do
    [ -z "$platform" ] && continue
    count_idx=$((count_idx + 1))
    (
      case "$platform" in
        GH)
          if [ -n "$first_rel" ]; then
            cd "$(_clrepo_base_for_rel "$first_rel")/$first_rel" 2>/dev/null || exit 0
            command -v direnv >/dev/null && eval "$(direnv export bash 2>/dev/null)"
          fi
          local r
          r=$(gh issue list --repo "$nwo" --state open \
            --json number,assignees --limit 100 2>/dev/null) \
            || { printf '%s\n' "-1 -1"; exit 0; }
          printf '%s\n' "$r" | jq -r --arg me "$gh_user" '
            (length | tostring) + " " +
            ([.[] | select(any(.assignees[]; .login == $me))] | length | tostring)
          ' 2>/dev/null || printf '%s\n' "-1 -1"
          ;;
        FJ)
          if [ -n "$fj_rel" ]; then
            cd "$(_clrepo_base_for_rel "$fj_rel")/$fj_rel" 2>/dev/null || exit 0
            command -v direnv >/dev/null && eval "$(direnv export bash 2>/dev/null)"
          fi
          [ -z "${FORGEJO_TOKEN:-}" ] && { printf '%s\n' "-1 -1"; exit 0; }
          local name="${nwo#*/}"
          local r
          r=$(curl -sf -H "Authorization: token $FORGEJO_TOKEN" \
            "https://git.home.freaxnx01.ch/api/v1/repos/freax/$name/issues?state=open&type=issues&limit=50" \
            2>/dev/null) || { printf '%s\n' "-1 -1"; exit 0; }
          printf '%s\n' "$r" | jq -r --arg me "$fj_user" '
            (length | tostring) + " " +
            ([.[] | select(.assignee.login == $me)] | length | tostring)
          ' 2>/dev/null || printf '%s\n' "-1 -1"
          ;;
      esac
    ) > "$tmpdir/count_$count_idx" &
  done <<< "$repos"
  wait

  # --- Phase 4: assemble cache JSON ---
  local json_repos=() count_idx=0
  while IFS=$'\t' read -r platform nwo url; do
    [ -z "$platform" ] && continue
    count_idx=$((count_idx + 1))
    local open=-1 mine=-1
    [ -f "$tmpdir/count_$count_idx" ] && read -r open mine < "$tmpdir/count_$count_idx" || true
    json_repos+=("$(jq -n \
      --arg p "$platform" --arg n "$nwo" --arg u "$url" \
      --argjson o "${open:--1}" --argjson m "${mine:--1}" \
      '{"platform":$p,"name":$n,"url":$u,"open":$o,"mine":$m}')")
  done <<< "$repos"

  local json_warnings_arr="[]"
  for w in "${warn_list[@]}"; do
    json_warnings_arr=$(printf '%s' "$json_warnings_arr" \
      | jq --arg w "$w" '. + [$w]')
  done

  local repos_json
  repos_json=$(printf '%s\n' "${json_repos[@]}" | jq -s '.')

  local cache_tmp="$_CLREPO_FOCUS_CACHE.tmp"
  jq -n \
    --argjson repos "$repos_json" \
    --argjson warnings "$json_warnings_arr" \
    --argjson ts "$(date +%s)" \
    '{"fetched_at":$ts,"repos":$repos,"warnings":$warnings}' \
    > "$cache_tmp" 2>/dev/null \
    && mv -f "$cache_tmp" "$_CLREPO_FOCUS_CACHE"

  _clrepo_focus_display_cache
}
```

- [ ] **Step 2: Run all tests**

```
bash tests/test_focus.sh
```

Expected: All 8 tests pass. Output ends with `PASS`.

- [ ] **Step 3: Syntax check**

```
bash -n clrepo.sh && echo OK
```

- [ ] **Step 4: Manual smoke: cache write + read**

```bash
# Source with your real base dir; confirm -f writes a cache
. ./clrepo.sh
clrepo -f              # may take a few seconds; writes ~/.cache/clrepo/focus.json
clrepo -f              # should be instant (cache hit)
clrepo -f --no-cache   # (flag not wired yet — test after Task 5)
```

- [ ] **Step 5: Commit**

```bash
git add clrepo.sh
git commit -m "feat(clrepo): rewrite _clrepo_focus_list with Forgejo, issue counts, and JSON caching"
```

---

### Task 5: `-f` parsing change, `--no-cache`, open-by-name

**Files:**
- Modify: `clrepo.sh:2828` (add locals)
- Modify: `clrepo.sh:2876` (change `-f` case; add `--no-cache` case)
- Modify: `clrepo.sh:2982–3020` (add `mode_focus` to conflict checks)
- Modify: `clrepo.sh:3068` (add `mode_focus` guard to CWD-launch condition)
- Modify: `clrepo.sh:~3076` (add `mode_focus` block)

- [ ] **Step 1: Add locals to `clrepo()` declaration**

Find:
```bash
  local with_remote=0 force_refresh=0 mode_delete=0 worktree="" editor="" remote_control=1 _CLREPO_NO_CHANNEL=0 _CLREPO_FORCED_SLOT="" _CLREPO_NO_SYNC=0 mode_attach=0 mode_pick=0 mode_repo_issues=0
```

Replace with:
```bash
  local with_remote=0 force_refresh=0 mode_delete=0 worktree="" editor="" remote_control=1 _CLREPO_NO_CHANNEL=0 _CLREPO_FORCED_SLOT="" _CLREPO_NO_SYNC=0 mode_attach=0 mode_pick=0 mode_repo_issues=0 mode_focus=0 focus_no_cache=0
```

- [ ] **Step 2: Change the `-f` dispatch case + add `--no-cache`**

Find:
```bash
      -f|--focus-list) _clrepo_focus_list; return ;;
```

Replace with:
```bash
      -f|--focus-list) mode_focus=1; shift ;;
      --no-cache)      focus_no_cache=1; shift ;;
```

- [ ] **Step 3: Add `mode_focus` to the `--attach` conflict-check bad-flags list**

Find:
```bash
    [ -n "$editor" ]                  && bad="${bad:+$bad, }-c/-p/-o/--cd"
    [ "$_CLREPO_NO_CHANNEL" = 1 ]     && bad="${bad:+$bad, }--no-channel"
    [ "$_CLREPO_NO_SYNC" = 1 ]        && bad="${bad:+$bad, }--no-sync"
    [ -n "$_CLREPO_FORCED_SLOT" ]     && bad="${bad:+$bad, }--slot"
    [ "$remote_control" != 1 ]        && bad="${bad:+$bad, }--no-rc"
    [ "$mode_pick" = 1 ]              && bad="${bad:+$bad, }--pick/--connect"
    if [ -n "$bad" ]; then
      echo "clrepo: --attach takes no other flags (got: $bad). Run \`clrepo <repo>\` to launch." >&2
```

Replace with:
```bash
    [ -n "$editor" ]                  && bad="${bad:+$bad, }-c/-p/-o/--cd"
    [ "$_CLREPO_NO_CHANNEL" = 1 ]     && bad="${bad:+$bad, }--no-channel"
    [ "$_CLREPO_NO_SYNC" = 1 ]        && bad="${bad:+$bad, }--no-sync"
    [ -n "$_CLREPO_FORCED_SLOT" ]     && bad="${bad:+$bad, }--slot"
    [ "$remote_control" != 1 ]        && bad="${bad:+$bad, }--no-rc"
    [ "$mode_pick" = 1 ]              && bad="${bad:+$bad, }--pick/--connect"
    [ "$mode_focus" = 1 ]             && bad="${bad:+$bad, }-f/--focus-list"
    if [ -n "$bad" ]; then
      echo "clrepo: --attach takes no other flags (got: $bad). Run \`clrepo <repo>\` to launch." >&2
```

- [ ] **Step 4: Add `mode_focus` to the `--pick` conflict-check bad-flags list**

Find (in the `mode_pick` guard block):
```bash
    [ "$mode_attach" = 1 ]            && bad="${bad:+$bad, }-a/--attach"
    if [ -n "$bad" ]; then
      echo "clrepo: --pick takes no other flags (got: $bad)." >&2
```

Replace with:
```bash
    [ "$mode_attach" = 1 ]            && bad="${bad:+$bad, }-a/--attach"
    [ "$mode_focus" = 1 ]             && bad="${bad:+$bad, }-f/--focus-list"
    if [ -n "$bad" ]; then
      echo "clrepo: --pick takes no other flags (got: $bad)." >&2
```

- [ ] **Step 5: Add `mode_focus` guard to the CWD-launch condition**

Find:
```bash
  if [ "$mode_delete" = 0 ] && [ "$with_remote" = 0 ] && [ "$mode_repo_issues" = 0 ] && { [ "${1:-}" = "." ] || [ $# -eq 0 ]; }; then
```

Replace with:
```bash
  if [ "$mode_delete" = 0 ] && [ "$with_remote" = 0 ] && [ "$mode_repo_issues" = 0 ] && [ "$mode_focus" = 0 ] && { [ "${1:-}" = "." ] || [ $# -eq 0 ]; }; then
```

- [ ] **Step 6: Add `mode_focus` dispatch block**

Find in `clrepo.sh` (the `mode_repo_issues` block — look for):
```bash
  # -i / --repo-issues [name]: print open issues for one repo via `gh issue list`.
```

Insert the following block immediately before it:
```bash
  # -f / --focus-list [name]: list focus repos, or open a named repo.
  if [ "$mode_focus" = 1 ]; then
    if [ -z "${1:-}" ]; then
      _clrepo_focus_list "$focus_no_cache"
      return
    fi
    # name provided — fall through to the existing positional-arg launch path below
  fi

```

- [ ] **Step 7: Syntax check**

```
bash -n clrepo.sh && echo OK
```

- [ ] **Step 8: Verify `--no-cache` works**

```bash
. ./clrepo.sh
clrepo -f              # writes cache (fast on second call)
clrepo -f --no-cache   # forces fresh fetch (slower)
clrepo --no-cache -V   # --no-cache outside -f is silently ignored → prints version
```

Expected: first call populates cache, second ignores it and refetches, third prints version with no error.

- [ ] **Step 9: Run all tests**

```
bash tests/test_focus.sh
```

Expected: `PASS`

- [ ] **Step 10: Commit**

```bash
git add clrepo.sh
git commit -m "feat(clrepo): -f becomes mode flag; add --no-cache; wire open-by-name fall-through"
```

---

### Task 6: Tab completion — `clrepo -f <TAB>` from cache

**Files:**
- Modify: `clrepo.sh:3236–3243` (add focus completion block; add `--no-cache` to flags string)

- [ ] **Step 1: Add `--no-cache` to the flags completion string**

Find in `_clrepo()`:
```bash
    local flags="-r --remote --refresh -D --delete -c --code -p --copilot -o --opencode --cd --remote-control --rc -w --worktree --no-sync --no-channel --slot --status --status-rc --doctor --worktree-status --ws --issues --dashboard -i --repo-issues -f --focus-list --focus-add --focus-rm -B --base --setup-admin --install-admin-commands --free -a --attach --pick --connect -V --version -h --help"
```

Replace with (add `--no-cache` after `--focus-rm`):
```bash
    local flags="-r --remote --refresh -D --delete -c --code -p --copilot -o --opencode --cd --remote-control --rc -w --worktree --no-sync --no-channel --slot --status --status-rc --doctor --worktree-status --ws --issues --dashboard -i --repo-issues -f --focus-list --no-cache --focus-add --focus-rm -B --base --setup-admin --install-admin-commands --free -a --attach --pick --connect -V --version -h --help"
```

- [ ] **Step 2: Insert focus-name completion block after `COMPREPLY=()` and before the `-*` flag check**

Find:
```bash
_clrepo() {
  local cur="${COMP_WORDS[COMP_CWORD]}"
  COMPREPLY=()
  if [[ "$cur" == -* ]]; then
```

Replace with:
```bash
_clrepo() {
  local cur="${COMP_WORDS[COMP_CWORD]}"
  COMPREPLY=()
  local prev="${COMP_WORDS[COMP_CWORD-1]:-}"
  if [[ "$prev" == "-f" || "$prev" == "--focus-list" ]] && [[ "$cur" != -* ]]; then
    if [ -f "$_CLREPO_FOCUS_CACHE" ]; then
      local focus_names
      focus_names=$(jq -r '.repos[].name | split("/")[-1]' \
                    "$_CLREPO_FOCUS_CACHE" 2>/dev/null)
      if [ -n "$focus_names" ]; then
        COMPREPLY=($(compgen -W "$focus_names" -- "$cur"))
        return
      fi
    fi
    # fallthrough: no cache or empty → normal repo name completion below
  fi
  if [[ "$cur" == -* ]]; then
```

- [ ] **Step 3: Syntax check**

```
bash -n clrepo.sh && echo OK
```

- [ ] **Step 4: Verify completion**

```bash
bash <<'EOF'
. ./clrepo.sh
# Seed a fake cache
mkdir -p ~/.cache/clrepo
echo '{"fetched_at":9999999999,"repos":[{"platform":"GH","name":"owner/myrepo","url":"https://github.com/owner/myrepo","open":0,"mine":0}],"warnings":[]}' \
  > ~/.cache/clrepo/focus.json
COMP_WORDS=("clrepo" "-f" "my")
COMP_CWORD=2
_clrepo
printf 'focus completions for "my": %s\n' "${COMPREPLY[*]}"

COMP_WORDS=("clrepo" "-f" "--")
COMP_CWORD=2
_clrepo
printf 'flag completions for "--": %s\n' "${COMPREPLY[*]}"
EOF
```

Expected:
```
focus completions for "my": myrepo
flag completions for "--": --remote --refresh ... --no-cache ...
```

- [ ] **Step 5: Run all tests**

```
bash tests/test_focus.sh
```

Expected: `PASS`

- [ ] **Step 6: Commit**

```bash
git add clrepo.sh
git commit -m "feat(clrepo): tab-complete focus repo names after -f from cache"
```

---

### Task 7: Help text update

**Files:**
- Modify: `clrepo.sh` — update `-f` entry in `--help` heredoc

- [ ] **Step 1: Update the `-f` entry and add `--no-cache`**

Find in the `--help` heredoc:
```bash
  -f, --focus-list      list repos tagged with the 'focus' topic across
                        configured GitHub owners. (MVP — Forgejo, issue
                        counts, caching, and tab completion are pending
                        follow-ups of #9.)
  --focus-add <name>    add the 'focus' topic to a GitHub repo
  --focus-rm <name>     remove the 'focus' topic from a GitHub repo
```

Replace with:
```bash
  -f [name]             list focus repos (all platforms) or open <name>.
      --focus-list      explicit form of -f with no <name>.
      --no-cache        force refresh, bypassing the 1-hour cache (only
                        meaningful with -f).
  --focus-add <name>    tag repo with the 'focus' topic on its platform
  --focus-rm <name>     remove the 'focus' topic
```

- [ ] **Step 2: Verify help output**

```bash
bash -c '. ./clrepo.sh && clrepo --help' | grep -A5 '\-f'
```

Expected: shows the updated focus section.

- [ ] **Step 3: Commit**

```bash
git add clrepo.sh
git commit -m "docs(clrepo): update --help for full focus-flag feature"
```

---

### Task 8: Version bump + CHANGELOG

**Files:**
- Modify: `clrepo.sh:25`
- Modify: `CHANGELOG.md`

- [ ] **Step 1: Bump version**

Find:
```bash
_CLREPO_VERSION="1.40.2"
```

Replace with:
```bash
_CLREPO_VERSION="1.41.0"
```

- [ ] **Step 2: Add CHANGELOG entry**

Open `CHANGELOG.md` and insert at the top (after the header, before `## [1.40.2]`):

```markdown
## [1.41.0] - 2026-05-20

### Added

- Focus topic — full implementation (#9). Completes all remaining acceptance criteria from the feature issue on top of the MVP (PR #23).
  - **Forgejo support:** `--focus-add` and `--focus-rm` now work on Forgejo repos (`git-forgejo/*`) via `PUT/DELETE /api/v1/repos/freax/<name>/topics/focus`. `clrepo -f` fetches focus repos from Forgejo via `/api/v1/repos/search?topic=true&q=focus`.
  - **Open-issue counts:** `-f` output shows `N open · M yours` per repo. Counts fetched in parallel via `gh issue list --repo` (GH) and `/api/v1/repos/.../issues` (FJ). Current user resolved once per run via `gh api user` / `GET /api/v1/user`.
  - **JSON cache:** focus list cached at `~/.cache/clrepo/focus.json` with 1-hour TTL (tunable via `CLREPO_FOCUS_TTL`). Cache is written atomically. `--focus-add` and `--focus-rm` invalidate it on success.
  - **`--no-cache`:** bypass the cache for one run; only meaningful with `-f`.
  - **`clrepo -f <name>`:** opens any local repo by name (tab-completes only focus repos; resolution is against all local repos).
  - **Tab completion:** `clrepo -f <TAB>` completes from the cached focus basenames. Falls back to all-repo completion when cache is absent.
  - **Partial-failure handling:** if Forgejo is unreachable or `FORGEJO_TOKEN` is missing, GH results are still shown and a `[!]` warning appears in the footer. Per-repo count failures show `? open` for that row.
  - **Output format:** two lines per repo — name + count row, then URL. Summary footer with totals.
```

- [ ] **Step 3: Final regression check**

```bash
bash -n clrepo.sh && echo "syntax OK"
bash tests/test_focus.sh
bash tests/test_focus_dedup.sh
bash tests/test_norm_path.sh
bash tests/test_base_flag_scope.sh
bash -c '. ./clrepo.sh && echo "version: $_CLREPO_VERSION"'
```

Expected:
```
syntax OK
PASS
PASS
PASS   (11/11 lines)
PASS
version: 1.41.0
```

- [ ] **Step 4: Commit**

```bash
git add clrepo.sh CHANGELOG.md
git commit -m "$(cat <<'EOF'
feat(clrepo): focus-flag full implementation — Forgejo, issue counts, caching, completion

Completes all remaining #9 acceptance criteria:
- Forgejo: focus-add/rm via PUT/DELETE /api/v1/.../topics/focus;
  focus-list via /api/v1/repos/search?topic=true&q=focus
- Issue counts: parallel per-repo fetch (gh issue list / curl);
  "assigned to me" resolved once per run
- JSON cache at ~/.cache/clrepo/focus.json with CLREPO_FOCUS_TTL
  (1h default); atomic write; invalidated on add/rm
- --no-cache bypass flag
- clrepo -f <name> open-by-name (all local repos, focus completion)
- Tab completion: focus basenames from cache after -f
- Partial-failure: warning footer for unreachable Forgejo
- Output: two-line rows (name+count / url); summary footer

Closes #9.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Self-Review

**Spec coverage check:**
- ✅ Forgejo list: Phase 1 in Task 4
- ✅ Forgejo add/rm: Task 3
- ✅ Issue counts: Phase 3 in Task 4
- ✅ "Assigned to me": Phase 2 (user resolution) + Phase 3 jq filter
- ✅ Cache read/write/TTL: Phase 4 + cache-read block in Task 4
- ✅ `--no-cache`: Task 5
- ✅ Cache invalidation on add/rm: Task 3
- ✅ `-f <name>` open-by-name: Task 5 (fall-through)
- ✅ Tab completion: Task 6
- ✅ Partial-failure warning: `warn_fj` file + `warn_list` array in Task 4
- ✅ Output format (two lines per repo, footer): `_clrepo_focus_display_cache` in Task 2
- ✅ `? open` for failed count jobs: `-1` sentinel, display guard in Task 2
- ✅ Tests: Task 1

**Placeholder scan:** None found.

**Consistency check:** `_clrepo_focus_display_cache` is defined in Task 2, called in Task 4's `_clrepo_focus_list` and in tests — consistent. `_CLREPO_FOCUS_CACHE` defined in Task 2, used in Tasks 3, 4, 6 — consistent. `force_refresh` param: Task 4 uses `"$1"`, Task 5 passes `"$focus_no_cache"` — consistent. `-1` sentinel: set in Task 4 Phase 4, tested in Task 1 Test 2 — consistent.
