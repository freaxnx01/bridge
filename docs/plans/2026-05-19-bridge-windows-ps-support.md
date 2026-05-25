# bridge on Windows / PowerShell — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let Windows users run `bridge` from PowerShell with `C:\Develop\Repos`-style paths, by keeping `bridge.sh` canonical and adding a thin `bridge.ps1` shim that invokes it under Git Bash.

**Architecture:** `bridge.sh` gains a platform check and a `cygpath`-based path-normalization helper applied once at `_BRIDGE_BASE` entry (no-op on POSIX). A new `bridge.ps1` locates `bash.exe`, sources `bridge.sh`, forwards arguments, and mirrors the exit code. README documents the Windows install path and the `cd`-doesn't-survive-back-to-PS caveat. ADO is unaffected (already first-class).

**Tech Stack:** Bash 4+, PowerShell 5+, Git for Windows (provides `bash.exe` and `cygpath`).

**Spec:** [`docs/specs/2026-05-19-bridge-windows-ps-support-design.md`](../specs/2026-05-19-bridge-windows-ps-support-design.md) (issue #8)

---

### Task 1: Tests for path-normalization helpers

**Files:**
- Create: `tests/test_norm_path.sh`

These tests assert the behaviour of `_bridge_is_windows`, `_bridge_norm_path`, and `_bridge_display_path` before the helpers exist. The repo has no test framework today; we ship a self-contained Bash assertion script. Run with `bash tests/test_norm_path.sh`; non-zero exit on failure.

- [ ] **Step 1: Create the test script.**

```bash
#!/usr/bin/env bash
# Self-contained tests for the path helpers in bridge.sh.
# Run: bash tests/test_norm_path.sh
set -u

_HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# Source bridge.sh in a way that does not auto-run anything. bridge.sh
# defines functions and sets variables; sourcing is safe.
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
# We force-set OSTYPE locally and call the helper.
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

# --- _bridge_norm_path: Windows-style on Windows ---
# We force the windows branch via OSTYPE=msys and a fake cygpath
# shim earlier on PATH. (Avoids needing cygpath on the dev box.)
_tmpdir=$(mktemp -d)
cat >"$_tmpdir/cygpath" <<'SH'
#!/usr/bin/env bash
# Minimal cygpath stand-in: handles -u for C:\... and forward-slash drive forms.
case "$1" in
  -u)
    in="$2"
    # Backslashes to forward slashes, then C:/foo -> /c/foo.
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
    # /c/foo -> C:\foo
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
```

- [ ] **Step 2: Run the tests to confirm they fail.**

Run: `bash tests/test_norm_path.sh`
Expected: FAIL (helpers undefined; many `command not found` or assertion failures).

- [ ] **Step 3: Commit the failing tests.**

```bash
git add tests/test_norm_path.sh
git commit -m "test: add failing tests for bridge path-normalization helpers"
```

---

### Task 2: Implement platform + path helpers in `bridge.sh`

**Files:**
- Modify: `bridge.sh` (insert new helpers just above line 36, before `_BRIDGE_BASE` is set)

- [ ] **Step 1: Add the three helpers above the `_BRIDGE_BASE` assignment.**

Open `bridge.sh`, find line 36 (`_BRIDGE_BASE="${BRIDGE_BASE:-$HOME/projects/repos}"`), and insert this block immediately above it (after line 35's `_BRIDGE_DIR=...` line):

```bash
# --- Platform + path helpers (Windows/Git-Bash support) ---
# _bridge_is_windows: true (exit 0) when running under Git Bash / MSYS / Cygwin.
# Detection is by $OSTYPE so callers can override in tests.
_bridge_is_windows() {
  case "${OSTYPE:-}" in
    msys*|cygwin*|mingw*) return 0 ;;
    *) return 1 ;;
  esac
}

# _bridge_norm_path <path>
#   POSIX hosts: echo the path unchanged.
#   Windows hosts: convert C:\foo, C:/foo, or /c/foo to /c/foo using
#   cygpath -u. Falls back to a pure-Bash conversion if cygpath is absent.
_bridge_norm_path() {
  local p="$1"
  if ! _bridge_is_windows; then
    printf '%s\n' "$p"
    return 0
  fi
  if command -v cygpath >/dev/null 2>&1; then
    cygpath -u "$p"
    return 0
  fi
  # Fallback: convert backslashes, then C:/foo -> /c/foo.
  p="${p//\\//}"
  if [[ "$p" =~ ^([A-Za-z]):(/.*)?$ ]]; then
    local drive_lc rest
    drive_lc=$(printf '%s' "${BASH_REMATCH[1]}" | tr '[:upper:]' '[:lower:]')
    rest="${BASH_REMATCH[2]:-}"
    printf '/%s%s\n' "$drive_lc" "$rest"
  else
    printf '%s\n' "$p"
  fi
}

# _bridge_display_path <posix-path>
#   POSIX hosts: echo unchanged.
#   Windows hosts: convert to Windows form (C:\foo) via cygpath -w for
#   user-facing messages. Falls back to a pure-Bash conversion.
_bridge_display_path() {
  local p="$1"
  if ! _bridge_is_windows; then
    printf '%s\n' "$p"
    return 0
  fi
  if command -v cygpath >/dev/null 2>&1; then
    cygpath -w "$p"
    return 0
  fi
  if [[ "$p" =~ ^/([A-Za-z])(/.*)?$ ]]; then
    local drive_uc rest
    drive_uc=$(printf '%s' "${BASH_REMATCH[1]}" | tr '[:lower:]' '[:upper:]')
    rest="${BASH_REMATCH[2]:-}"
    rest="${rest//\//\\}"
    printf '%s:%s\n' "$drive_uc" "$rest"
  else
    printf '%s\n' "$p"
  fi
}
```

- [ ] **Step 2: Run the tests to confirm they pass.**

Run: `bash tests/test_norm_path.sh`
Expected: `PASS` (every `ok` line printed; exit 0).

- [ ] **Step 3: Commit.**

```bash
git add bridge.sh
git commit -m "feat(bridge): add platform + path-normalization helpers for Windows"
```

---

### Task 3: Apply normalization to `_BRIDGE_BASE` at entry

**Files:**
- Modify: `bridge.sh:36`

- [ ] **Step 1: Wrap the `_BRIDGE_BASE` assignment with normalization.**

Replace line 36:

```bash
_BRIDGE_BASE="${BRIDGE_BASE:-$HOME/projects/repos}"
```

with:

```bash
# Normalize once at entry so all downstream code uses POSIX paths.
# On Linux/macOS this is a no-op (see _bridge_norm_path).
_BRIDGE_BASE="$(_bridge_norm_path "${BRIDGE_BASE:-$HOME/projects/repos}")"
```

- [ ] **Step 2: Linux smoke test — confirm no regression.**

Run:
```bash
unset BRIDGE_BASE
bash -c '. ./bridge.sh && echo "$_BRIDGE_BASE"'
```
Expected: prints `$HOME/projects/repos` expanded (e.g. `/home/freax/projects/repos`) — identical to before the change.

Run with an override:
```bash
BRIDGE_BASE=/tmp/repos bash -c '. ./bridge.sh && echo "$_BRIDGE_BASE"'
```
Expected: `/tmp/repos`.

- [ ] **Step 3: Re-run the helper tests (defensive — confirms sourcing still works).**

Run: `bash tests/test_norm_path.sh`
Expected: `PASS`.

- [ ] **Step 4: Commit.**

```bash
git add bridge.sh
git commit -m "feat(bridge): normalize BRIDGE_BASE for Windows-style inputs"
```

---

### Task 4: Use `_bridge_display_path` in the prominent user-facing messages

**Files:**
- Modify: `bridge.sh` lines 1455, 1583, 1643, 2620 (four "under $_BRIDGE_BASE" messages)

Only the four most visible messages — the ones a Windows user is most likely to hit and want to copy/paste. Other internal uses keep POSIX paths intentionally.

- [ ] **Step 1: Update line 1455.**

Find:
```bash
echo "bridge: no forge targets discovered under $_BRIDGE_BASE" >&2
```
Replace with:
```bash
echo "bridge: no forge targets discovered under $(_bridge_display_path "$_BRIDGE_BASE")" >&2
```

- [ ] **Step 2: Update line 1583.**

Find:
```bash
echo "bridge: no repos found under $_BRIDGE_BASE" >&2
```
Replace with:
```bash
echo "bridge: no repos found under $(_bridge_display_path "$_BRIDGE_BASE")" >&2
```

- [ ] **Step 3: Update line 1643.**

Find (the second occurrence of this string — should now be the only remaining one after Step 1):
```bash
echo "bridge: no forge targets discovered under $_BRIDGE_BASE" >&2
```
Replace with:
```bash
echo "bridge: no forge targets discovered under $(_bridge_display_path "$_BRIDGE_BASE")" >&2
```

- [ ] **Step 4: Update line 2620.**

Find:
```bash
echo "bridge: '.' requires current dir to be inside a repo under $_BRIDGE_BASE" >&2
```
Replace with:
```bash
echo "bridge: '.' requires current dir to be inside a repo under $(_bridge_display_path "$_BRIDGE_BASE")" >&2
```

- [ ] **Step 5: Confirm no other `under $_BRIDGE_BASE` callsites remain unconverted.**

Run: `grep -n 'under \$_BRIDGE_BASE' bridge.sh`
Expected: empty output.

- [ ] **Step 6: Linux smoke test — error message still readable.**

Run:
```bash
BRIDGE_BASE=/nonexistent/path bash -c '. ./bridge.sh && bridge --list' 2>&1 | head -5
```
Expected: a `bridge: no ... under /nonexistent/path` style message (the display helper is a no-op on Linux, so the path is unchanged).

- [ ] **Step 7: Commit.**

```bash
git add bridge.sh
git commit -m "feat(bridge): display Windows-style base paths in user-facing messages"
```

---

### Task 5: Add the PowerShell shim `bridge.ps1`

**Files:**
- Create: `bridge.ps1`

- [ ] **Step 1: Create the shim.**

```powershell
# bridge.ps1 — PowerShell entry point for bridge on Windows.
# Locates bash.exe (Git Bash), sources bridge.sh, and forwards arguments.
# See docs/specs/2026-05-19-bridge-windows-ps-support-design.md.

$ErrorActionPreference = 'Stop'

function Find-Bash {
    if ($env:BRIDGE_BASH -and (Test-Path $env:BRIDGE_BASH)) {
        return $env:BRIDGE_BASH
    }
    $git = Get-Command git.exe -ErrorAction SilentlyContinue
    if ($git) {
        try {
            $execPath = & git.exe --exec-path 2>$null
            if ($execPath) {
                $candidate = Join-Path (Split-Path -Parent (Split-Path -Parent $execPath)) 'bin\bash.exe'
                if (Test-Path $candidate) { return $candidate }
            }
        } catch {}
    }
    foreach ($candidate in @(
        'C:\Program Files\Git\bin\bash.exe',
        'C:\Program Files (x86)\Git\bin\bash.exe'
    )) {
        if (Test-Path $candidate) { return $candidate }
    }
    $where = Get-Command bash.exe -ErrorAction SilentlyContinue
    if ($where) { return $where.Source }
    return $null
}

$bash = Find-Bash
if (-not $bash) {
    Write-Error "bridge: bash.exe not found. Install Git for Windows or set `$env:BRIDGE_BASH."
    exit 127
}

$bridgeSh = Join-Path $PSScriptRoot 'bridge.sh'
if (-not (Test-Path $bridgeSh)) {
    Write-Error "bridge: bridge.sh not found next to bridge.ps1 (looked for $bridgeSh)."
    exit 2
}

# Convert the Windows path to a Git Bash style path so `source` works.
# $bash --norc -c 'cygpath -u "$1"' _ <path>
$bridgeShPosix = & $bash --norc -c 'cygpath -u "$1"' _ $bridgeSh

# Forward arguments via $@. The literal 'bridge' is $0 (used by the
# script in error messages).
& $bash --norc -c "source `"$bridgeShPosix`" && bridge `"`$@`"" bridge @args
exit $LASTEXITCODE
```

- [ ] **Step 2: Lint-check syntax with PowerShell if available; otherwise visual review.**

If you have PowerShell on the dev box:
```bash
pwsh -NoProfile -Command "Get-Command -Syntax (Resolve-Path ./bridge.ps1)" 2>&1 | head -5
```
Otherwise: read through the file and confirm every `{` has a matching `}` and every backtick-escaped quote inside the final `& $bash -c "..."` is correct (the inner `"$@"` and `$@` must be backtick-escaped so PowerShell does not interpolate them).

- [ ] **Step 3: Commit.**

```bash
git add bridge.ps1
git commit -m "feat(bridge): add PowerShell shim for Windows users"
```

---

### Task 6: README — add Windows / PowerShell section

**Files:**
- Modify: `README.md` (append a new section before the existing "Troubleshooting" / end-of-file content; if no such section, append at the end)

- [ ] **Step 1: Find the right insertion point.**

Run: `grep -n '^## ' README.md`
Look for a natural slot — after the main install/usage section, before troubleshooting / changelog references.

- [ ] **Step 2: Insert this section at the chosen location.**

```markdown
## Windows / PowerShell

`bridge` is a Bash script. On Windows, run it under **Git Bash** (ships with [Git for Windows](https://gitforwindows.org/)). From PowerShell, use the included `bridge.ps1` shim.

**Prerequisites:**

- Git for Windows installed (provides `bash.exe`, `cygpath`, `git`).
- Optional: set `$env:BRIDGE_BASH` to point at a non-default `bash.exe`.

**Setup in PowerShell:**

```powershell
# Pick your base dir. Both Windows and POSIX forms are accepted.
$env:BRIDGE_BASE = 'C:\Develop\Repos'
$env:GITHUB_TOKEN = '...'           # if you use GitHub
$env:AZURE_DEVOPS_EXT_PAT = '...'   # if you use Azure DevOps

# Run directly:
. C:\path\to\bridge\bridge.ps1 --list

# Or define a function in $PROFILE for a `bridge` command:
function bridge { & "C:\path\to\bridge\bridge.ps1" @args }
```

Config lives under `$HOME/.config/bridge/` — on Windows that resolves to `C:\Users\<you>\.config\bridge\` (Git Bash sets `$HOME` to `%USERPROFILE%`).

**Caveat — `cd` doesn't survive back to PowerShell:** `bridge <repo>` changes directory inside the Bash subprocess but does not change your PowerShell session's working directory. Use Git Bash directly if you want `cd` to stick, or `cd` manually in PowerShell afterwards. A future PS-native wrapper could address this.

**Caveat — tab completion:** Bash completion works inside Git Bash. PowerShell-native completion for `bridge.ps1` is not implemented yet.
```

- [ ] **Step 3: Commit.**

```bash
git add README.md
git commit -m "docs: document Windows / PowerShell usage"
```

---

### Task 7: Bump version, update CHANGELOG, final sanity check

**Files:**
- Modify: `bridge.sh:25` (version bump)
- Modify: `CHANGELOG.md` (new entry)

Per `CLAUDE.md`: every change to `bridge.sh` bumps `_BRIDGE_VERSION` with a matching `CHANGELOG.md` entry. This is a new feature → minor bump: `1.30.0` → `1.31.0`.

- [ ] **Step 1: Bump the version.**

In `bridge.sh`, find:
```bash
_BRIDGE_VERSION="1.30.0"
```
Replace with:
```bash
_BRIDGE_VERSION="1.31.0"
```

- [ ] **Step 2: Add a CHANGELOG entry.**

Open `CHANGELOG.md`, locate the format of the most recent entry, and add a new top entry following the existing Keep a Changelog style:

```markdown
## [1.31.0] - 2026-05-19

### Added
- Windows / PowerShell support: `bridge.ps1` shim invokes `bridge.sh` under Git Bash, forwarding arguments and exit codes.
- Path-normalization helpers (`_bridge_norm_path`, `_bridge_display_path`, `_bridge_is_windows`) so `BRIDGE_BASE=C:\Develop\Repos` and POSIX forms both work.
- Self-contained test for the path helpers at `tests/test_norm_path.sh`.

### Changed
- User-facing "under $_BRIDGE_BASE" messages now show Windows-style paths on Windows.
```

- [ ] **Step 3: Final regression sweep on Linux.**

Run all of:
```bash
bash tests/test_norm_path.sh                           # PASS
bash -c '. ./bridge.sh && echo "$_BRIDGE_VERSION"'     # 1.31.0
bash -c '. ./bridge.sh && echo "$_BRIDGE_BASE"'        # your usual base
BRIDGE_BASE=/tmp/x bash -c '. ./bridge.sh && bridge --list' 2>&1 | head -3
```
Each should succeed without errors that didn't exist on `main`.

- [ ] **Step 4: Commit.**

```bash
git add bridge.sh CHANGELOG.md
git commit -m "chore(release): bump to 1.31.0 for Windows / PowerShell support"
```

---

## Manual Windows verification (post-merge, deferred to the user)

Not part of the plan tasks, but the user (test environment is their Windows box) should run, per spec Section 5:

1. PowerShell, fresh terminal, with `$env:BRIDGE_BASE = 'C:\Develop\Repos'` and the appropriate token env vars — confirm `--version`, `--list`, and opening an existing repo all work.
2. Repeat with `$env:BRIDGE_BASE = '/c/Develop/Repos'`.
3. From Git Bash on the same box — same results.
4. ADO smoke: an existing ADO repo resolves/clones.
5. Rename `bash.exe` (or set `$env:BRIDGE_BASH` to a bogus path) → shim exits 127 with a clear message.

Any failures here loop back as follow-up tasks; they are expected to be small.
