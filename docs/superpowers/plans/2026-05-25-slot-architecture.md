# Slot Architecture Redesign Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Decouple the slot system from per-slot Telegram bots — replace 4 per-slot Telegram functions with a single `_bridge_notify` helper that reads from `bridge-bot.json`, remove the `MAX_SLOTS` hard ceiling, and remove all displacement logic.

**Architecture:** The slot allocator scans `slots.json` for the lowest unused integer key with no upper bound. All Telegram notifications are sent through a single `_bridge_notify` helper that resolves its token from `~/.cache/bridge/bridge-bot.json` via Passbolt. `slot-tokens.json` and `_BRIDGE_MAX_SLOTS` are removed entirely.

**Tech Stack:** Bash, Python 3 (inline snippets), Telegram Bot API (curl), Passbolt CLI, bats-core (tests).

**Spec:** `docs/superpowers/specs/2026-05-25-slot-architecture-design.md`
**Issue:** [#32](https://github.com/freaxnx01/bridge/issues/32)

---

## File Map

| File | Change |
|---|---|
| `bridge.sh` | Rewrite allocator; add `_bridge_notify`; delete 4 Telegram functions; update launch path |
| `bridge-hooks/notify.sh` | Replace `_bridge_telegram_page` call with `_bridge_notify` |
| `bridge-watcher.sh` | Replace `_bridge_telegram_page` call with `_bridge_notify` |
| `bridge-autosync.sh` | Replace `_autosync_telegram token text` with self-contained bot.json lookup |
| `setup-claude-channels.sh` | Remove per-slot bot token section (section 2) and `MAX` variable |
| `tests/unit/test_slot_allocate.bats` | New — bats tests for unlimited allocator |
| `CHANGELOG.md` | New entry for version bump |

---

## Task 1: Write failing tests for the unlimited slot allocator

**Files:**
- Create: `tests/unit/test_slot_allocate.bats`

- [ ] **Step 1: Create the test file**

```bash
cat > /path/to/bridge/tests/unit/test_slot_allocate.bats << 'BATS'
#!/usr/bin/env bats

load '../helpers/load'

setup() {
  bridge_load_lib
  # Each test gets a fresh slots.json
  echo '{"slots":{}}' > "$_BRIDGE_SLOTS_FILE"
}

# Helper: mark slot N as busy (fake entry — no real tmux/pid so reconcile ignores it)
_mark_busy() {
  local n="$1" repo="${2:-testrepo}"
  python3 -c "
import json, time
f = '$_BRIDGE_SLOTS_FILE'
with open(f) as fh: d = json.load(fh)
d['slots']['$n'] = {'repo': '$repo', 'pid': 999999999, 'started_at': int(time.time()), 'session': None}
with open(f, 'w') as fh: json.dump(d, fh)
"
}

@test "allocates slot 1 when slots.json is empty" {
  _bridge_slot_allocate
  [ "$_SLOT" = "1" ]
}

@test "allocates lowest free slot when some are busy" {
  _mark_busy 1
  _mark_busy 3
  _bridge_slot_allocate
  [ "$_SLOT" = "2" ]
}

@test "allocates slot beyond 6 when slots 1-6 are all busy" {
  for n in 1 2 3 4 5 6; do _mark_busy "$n"; done
  _bridge_slot_allocate
  [ "$_SLOT" = "7" ]
}

@test "allocates slot beyond existing max when all low slots busy" {
  for n in 1 2 3 4 5 6 7 8 9 10; do _mark_busy "$n"; done
  _bridge_slot_allocate
  [ "$_SLOT" = "11" ]
}

@test "forced slot succeeds when target is free" {
  _bridge_slot_allocate 3
  [ "$_SLOT" = "3" ]
}

@test "forced slot fails when target is busy" {
  _mark_busy 3
  run _bridge_slot_allocate 3
  [ "$status" -ne 0 ]
}

@test "_BRIDGE_MAX_SLOTS is not defined" {
  run bash -c 'source "$1"; [[ -z "${_BRIDGE_MAX_SLOTS+x}" ]]' _ "$REPO_ROOT/bridge.sh"
  [ "$status" -eq 0 ]
}

@test "_BRIDGE_SLOT_TOKENS is not defined" {
  run bash -c 'source "$1"; [[ -z "${_BRIDGE_SLOT_TOKENS+x}" ]]' _ "$REPO_ROOT/bridge.sh"
  [ "$status" -eq 0 ]
}
BATS
```

- [ ] **Step 2: Run the tests to verify they fail**

```bash
cd /path/to/bridge
bats tests/unit/test_slot_allocate.bats
```

Expected: multiple FAILs (allocator still has MAX_SLOTS loop and displacement).

---

## Task 2: Rewrite `_bridge_slot_allocate` in `bridge.sh`

**Files:**
- Modify: `bridge.sh:207-211` (variable declarations)
- Modify: `bridge.sh:820-941` (allocator function)

- [ ] **Step 1: Remove `_BRIDGE_MAX_SLOTS` and `_BRIDGE_SLOT_TOKENS` from the variable block**

At `bridge.sh` around line 207, find this block:

```bash
# --- Slot / Telegram channel config ---
_BRIDGE_MAX_SLOTS="${BRIDGE_MAX_SLOTS:-6}"
_BRIDGE_SLOTS_FILE="$_BRIDGE_CACHE/slots.json"
_BRIDGE_SLOTS_LOCK="$_BRIDGE_CACHE/slots.lock"
_BRIDGE_SLOT_TOKENS="$_BRIDGE_CACHE/slot-tokens.json"
_BRIDGE_OWNER="$_BRIDGE_CACHE/owner.json"
```

Replace with:

```bash
# --- Slot / Telegram channel config ---
_BRIDGE_SLOTS_FILE="$_BRIDGE_CACHE/slots.json"
_BRIDGE_SLOTS_LOCK="$_BRIDGE_CACHE/slots.lock"
_BRIDGE_OWNER="$_BRIDGE_CACHE/owner.json"
```

- [ ] **Step 2: Replace the body of `_bridge_slot_allocate`**

Find the entire function from `# Allocate a slot. Sets _SLOT and _SLOT_TOKEN.` through the closing `}` at around line 941. Replace it with:

```bash
# Allocate a slot. Sets _SLOT. Optional: $1 = forced slot number.
_bridge_slot_allocate() {
  local forced="${1:-}"
  local slots_json

  exec {_lock_fd}>"$_BRIDGE_SLOTS_LOCK"
  flock "$_lock_fd"

  # Reconcile dead slots (tmux session is source of truth when recorded;
  # otherwise fall back to PID liveness for foreground-mode records)
  _bridge_slots_init
  python3 -c "
import json, os, subprocess
f = '$_BRIDGE_SLOTS_FILE'
with open(f) as fh: d = json.load(fh)
changed = False
for k, v in list(d.get('slots', {}).items()):
    if not v: continue
    sess = v.get('session') or ''
    if sess:
        alive = subprocess.run(['tmux', 'has-session', '-t', sess],
                               stdout=subprocess.DEVNULL,
                               stderr=subprocess.DEVNULL).returncode == 0
    else:
        try: os.kill(int(v.get('pid', 0)), 0); alive = True
        except (ProcessLookupError, ValueError): alive = False
        except PermissionError: alive = True
    if not alive:
        d['slots'][k] = None
        changed = True
if changed:
    with open(f, 'w') as fh: json.dump(d, fh, indent=2)
" 2>/dev/null

  slots_json=$(cat "$_BRIDGE_SLOTS_FILE")

  if [ -n "$forced" ]; then
    local busy
    busy=$(echo "$slots_json" | python3 -c "
import json, sys
d = json.load(sys.stdin)
v = d.get('slots', {}).get('$forced')
if v: print(v.get('repo', '?'))
" 2>/dev/null)
    if [ -n "$busy" ]; then
      echo "bridge: slot $forced is busy with $busy — use a different slot or bridge --free $forced" >&2
      flock -u "$_lock_fd"
      return 1
    fi
    _SLOT="$forced"
  else
    # Find lowest unused slot number — no upper bound
    _SLOT=$(echo "$slots_json" | python3 -c "
import json, sys
d = json.load(sys.stdin)
occupied = {int(k) for k, v in d.get('slots', {}).items() if v}
n = 1
while n in occupied:
    n += 1
print(n)
" 2>/dev/null)
    _SLOT="${_SLOT:-1}"
  fi

  flock -u "$_lock_fd"

  # Install per-slot hooks. The watcher is started in _bridge_slot_record
  # (after slots.json is updated) to avoid racing with the watcher's
  # "no active slots → self-exit" path.
  _bridge_install_hooks "$_SLOT"
}
```

- [ ] **Step 3: Run the allocator tests**

```bash
bats tests/unit/test_slot_allocate.bats
```

Expected: all 8 tests PASS.

- [ ] **Step 4: Run the full unit test suite to check for regressions**

```bash
bats tests/unit/
```

Expected: all existing tests still PASS.

- [ ] **Step 5: Commit**

```bash
git add bridge.sh tests/unit/test_slot_allocate.bats
git commit -m "feat(slots): remove MAX_SLOTS cap and displacement logic"
```

---

## Task 3: Add `_bridge_notify`, remove 4 dead Telegram functions

**Files:**
- Modify: `bridge.sh` (around lines 1070–1270)

- [ ] **Step 1: Add `_bridge_notify` immediately after `_bridge_should_page`**

Find the end of `_bridge_should_page` (the closing `}` before the old `_bridge_telegram_page` comment). Insert this new function after it:

```bash
# Send text via bridge-bot's Telegram bot to the configured owner.
# Args: $1 = message text. Best-effort; never fails the caller.
# Reads bot token from bridge-bot.json via Passbolt, owner from bridge-bot.json.
_bridge_notify() {
  local text="$1"
  [ -z "$text" ] && return 0

  local bot_cfg="$_BRIDGE_CACHE/bridge-bot.json"
  [ -f "$bot_cfg" ] || return 0

  local pb_id owner_id
  read -r pb_id owner_id < <(python3 -c "
import json
try:
    with open('$bot_cfg') as f: d = json.load(f)
    print(d.get('passbolt_resource_id', ''), d.get('telegram_owner_id', ''))
except Exception:
    print('', '')
" 2>/dev/null)

  [ -z "$pb_id" ] && return 0
  [ -z "$owner_id" ] && return 0

  local token
  token=$(passbolt get resource --id "$pb_id" 2>/dev/null | awk -F": " '/^Password:/ {print $2}')
  [ -z "$token" ] && return 0

  curl -sf -X POST "https://api.telegram.org/bot${token}/sendMessage" \
    -H "Content-Type: application/json" \
    -d "$(python3 -c "import json,sys; print(json.dumps({'chat_id': '$owner_id', 'text': sys.stdin.read()}))" <<< "$text")" \
    >/dev/null 2>&1 || true
}
```

- [ ] **Step 2: Delete `_bridge_telegram_setup` (the full function, ~40 lines)**

Find and delete from `# Call Telegram API to set bot name and pin a banner message.` through its closing `}`.

- [ ] **Step 3: Delete `_bridge_admin_status_update` (the full function, ~45 lines)**

Find and delete from `# Refresh admin bot (#0) title to mirror aggregate slot status:` through its closing `}`.

- [ ] **Step 4: Delete `_bridge_telegram_cleanup` (the full function, ~20 lines)**

Find and delete from `# Best-effort cleanup: reset bot name, send close message.` through its closing `}`.

- [ ] **Step 5: Delete `_bridge_telegram_page` (the full function, ~30 lines)**

Find and delete from `# Send arbitrary text via slot $1's bot to the configured owner.` through its closing `}`.

- [ ] **Step 6: Remove `_bridge_admin_status_update` calls from `_bridge_slot_record` and `_bridge_slot_free`**

In `_bridge_slot_record`, find and delete this line:
```bash
  # Refresh admin bot title to reflect new aggregate state.
  _bridge_admin_status_update
```

In `_bridge_slot_free`, find and delete:
```bash
  # Refresh admin bot title to reflect new aggregate state.
  _bridge_admin_status_update
```

- [ ] **Step 7: Verify bridge.sh sources cleanly and unit tests still pass**

```bash
bash -c 'source /path/to/bridge/bridge.sh' && echo OK
bats tests/unit/
```

Expected: `OK` and all tests PASS.

- [ ] **Step 8: Commit**

```bash
git add bridge.sh
git commit -m "feat(slots): add _bridge_notify, remove per-slot Telegram functions"
```

---

## Task 4: Update the launch path in `bridge.sh`

**Files:**
- Modify: `bridge.sh` (around lines 2976–3050)

Context: The launch path has two branches — tmux mode and foreground mode. Both call `_bridge_telegram_setup` on start and `_bridge_telegram_cleanup` on end. These are replaced with `_bridge_notify`.

- [ ] **Step 1: Remove `export TELEGRAM_BOT_TOKEN="$_SLOT_TOKEN"`**

Find this line (around line 2982):
```bash
  export TELEGRAM_BOT_TOKEN="$_SLOT_TOKEN"
```
Delete it.

- [ ] **Step 2: Replace `_bridge_telegram_setup` call in tmux branch with `_bridge_notify`**

Find (around line 3005):
```bash
    _bridge_telegram_setup "$_SLOT" "$repo" "$worktree" "$_SLOT_TOKEN"
```
Replace with:
```bash
    _bridge_notify "$(printf '📍 Session started\nSlot: s%s\nRepo: %s\nWorktree: %s\nBranch: %s\nPath: %s\nStarted: %s' \
      "$_SLOT" "$repo" "${worktree:-—}" \
      "$(git rev-parse --abbrev-ref HEAD 2>/dev/null || echo '—')" \
      "$PWD" "$(date -Iseconds)")"
```

- [ ] **Step 3: Update the `session-closed` hook to drop the token**

Find (around line 3016):
```bash
    tmux set-hook -t "$session" session-closed "run-shell '$_BRIDGE_DIR/bridge-autosync.sh $session $_SLOT_TOKEN; $HOME/.cache/bridge/cleanup.sh $_SLOT $_SLOT_TOKEN'"
```
Replace with:
```bash
    tmux set-hook -t "$session" session-closed "run-shell '$_BRIDGE_DIR/bridge-autosync.sh $session'"
```

- [ ] **Step 4: Replace `_bridge_telegram_setup` call in foreground branch with `_bridge_notify`**

Find (around line 3042):
```bash
    _bridge_telegram_setup "$_SLOT" "$repo" "$worktree" "$_SLOT_TOKEN"
```
Replace with:
```bash
    _bridge_notify "$(printf '📍 Session started\nSlot: s%s\nRepo: %s\nWorktree: %s\nBranch: %s\nPath: %s\nStarted: %s' \
      "$_SLOT" "$repo" "${worktree:-—}" \
      "$(git rev-parse --abbrev-ref HEAD 2>/dev/null || echo '—')" \
      "$PWD" "$(date -Iseconds)")"
```

- [ ] **Step 5: Update foreground autosync call to drop token**

Find:
```bash
    command -v _bridge_autosync >/dev/null && _bridge_autosync "$PWD" "$_SLOT_TOKEN"
```
Replace with:
```bash
    command -v _bridge_autosync >/dev/null && _bridge_autosync "$PWD"
```

- [ ] **Step 6: Replace `_bridge_telegram_cleanup` call in foreground branch with `_bridge_notify`**

Find:
```bash
    _bridge_telegram_cleanup "$_SLOT" "$_SLOT_TOKEN"
```
Replace with:
```bash
    _bridge_notify "$(printf '🛑 Session s%s closed (%s)' "$_SLOT" "$repo")"
```

- [ ] **Step 7: Verify no remaining references to `_SLOT_TOKEN` or removed functions**

```bash
grep -n "_SLOT_TOKEN\|_bridge_telegram_setup\|_bridge_telegram_cleanup\|_bridge_telegram_page\|_bridge_admin_status_update\|_BRIDGE_MAX_SLOTS\|_BRIDGE_SLOT_TOKENS" /path/to/bridge/bridge.sh
```

Expected: **no output** (zero matches).

- [ ] **Step 8: Source check and unit tests**

```bash
bash -c 'source /path/to/bridge/bridge.sh' && echo OK
bats tests/unit/
```

Expected: `OK` and all PASS.

- [ ] **Step 9: Commit**

```bash
git add bridge.sh
git commit -m "feat(slots): update launch path — _bridge_notify replaces per-slot setup/cleanup"
```

---

## Task 5: Update `bridge-hooks/notify.sh` and `bridge-watcher.sh`

**Files:**
- Modify: `bridge-hooks/notify.sh:99,107`
- Modify: `bridge-watcher.sh:141`

- [ ] **Step 1: Replace `_bridge_telegram_page` calls in `notify.sh`**

`notify.sh` has two call sites. Both pass `"$SLOT"` and `"$TEXT"`.

Find (line ~99):
```bash
      _bridge_telegram_page "$SLOT" "$TEXT"
```
Replace with:
```bash
      _bridge_notify "$TEXT"
```

Find (line ~107):
```bash
      _bridge_telegram_page "$SLOT" "$TEXT"
```
Replace with:
```bash
      _bridge_notify "$TEXT"
```

Also remove the comment on line 17 that says `_bridge_telegram_page`:
```bash
# Source bridge for _bridge_should_page, _bridge_telegram_page, etc.
```
Replace with:
```bash
# Source bridge for _bridge_should_page, _bridge_notify, etc.
```

- [ ] **Step 2: Replace `_bridge_telegram_page` call in `bridge-watcher.sh`**

Find (line ~141):
```bash
    _bridge_telegram_page "$slot" "$body"
```
Replace with:
```bash
    _bridge_notify "$body"
```

- [ ] **Step 3: Verify no remaining `_bridge_telegram_page` references**

```bash
grep -rn "_bridge_telegram_page" /path/to/bridge/
```

Expected: **no output**.

- [ ] **Step 4: Commit**

```bash
git add bridge-hooks/notify.sh bridge-watcher.sh
git commit -m "feat(slots): route hook/watcher notifications through _bridge_notify"
```

---

## Task 6: Update `bridge-autosync.sh`

**Files:**
- Modify: `bridge-autosync.sh`

`_autosync_telegram` currently takes `(token, text)`. The token is pre-resolved by the caller. After this change it resolves the token itself from `bridge-bot.json`.

- [ ] **Step 1: Replace `_autosync_telegram` with a self-contained version**

Find the existing `_autosync_telegram` function:
```bash
_autosync_telegram() {
  local token="$1" text="$2"
  [ -z "$token" ] && return 0
  [ -f "$_BRIDGE_OWNER" ] || return 0
  local owner_id
  owner_id=$(python3 -c "
import json
try:
    with open('$_BRIDGE_OWNER') as f: d = json.load(f)
    print(d.get('telegram_user_id', ''))
except Exception:
    pass
" 2>/dev/null)
  [ -z "$owner_id" ] && return 0
  curl -sf -X POST "https://api.telegram.org/bot${token}/sendMessage" \
    -H "Content-Type: application/json" \
    -d "$(python3 -c "import json,sys; print(json.dumps({'chat_id':'$owner_id','text':'''$text'''}))" 2>/dev/null)" \
    >/dev/null 2>&1 || true
}
```

Replace with:

```bash
_autosync_telegram() {
  local text="$1"
  [ -z "$text" ] && return 0
  local bot_cfg="$_BRIDGE_CACHE/bridge-bot.json"
  [ -f "$bot_cfg" ] || return 0
  local pb_id owner_id
  read -r pb_id owner_id < <(python3 -c "
import json
try:
    with open('$bot_cfg') as f: d = json.load(f)
    print(d.get('passbolt_resource_id', ''), d.get('telegram_owner_id', ''))
except Exception:
    print('', '')
" 2>/dev/null)
  [ -z "$pb_id" ] && return 0
  [ -z "$owner_id" ] && return 0
  local token
  token=$(passbolt get resource --id "$pb_id" 2>/dev/null | awk -F": " '/^Password:/ {print $2}')
  [ -z "$token" ] && return 0
  curl -sf -X POST "https://api.telegram.org/bot${token}/sendMessage" \
    -H "Content-Type: application/json" \
    -d "$(python3 -c "import json,sys; print(json.dumps({'chat_id': '$owner_id', 'text': sys.stdin.read()}))" <<< "$text")" \
    >/dev/null 2>&1 || true
}
```

- [ ] **Step 2: Update `_autosync_telegram` call sites to drop the token argument**

Find all three call sites and drop the first (token) argument:

```bash
# Old:
_autosync_telegram "$token" "⚠️ autosync FAILED in ${repo_name} on ${branch}: commit error"
# New:
_autosync_telegram "⚠️ autosync FAILED in ${repo_name} on ${branch}: commit error"

# Old:
_autosync_telegram "$token" "⚠️ autosync FAILED in ${repo_name} on ${branch}: push rejected"
# New:
_autosync_telegram "⚠️ autosync FAILED in ${repo_name} on ${branch}: push rejected"

# Old:
_autosync_telegram "$token" "💾 autosync: pushed ${count} change(s) to ${branch} in ${repo_name}"
# New:
_autosync_telegram "💾 autosync: pushed ${count} change(s) to ${branch} in ${repo_name}"
```

- [ ] **Step 3: Update `_bridge_autosync` signature to drop the `token` parameter**

Find:
```bash
_bridge_autosync() {
  local repo_path="$1" token="${2:-}"
```
Replace with:
```bash
_bridge_autosync() {
  local repo_path="$1"
```

- [ ] **Step 4: Update script-mode invocation to drop the `token` parameter**

Find near the bottom of the file (script mode):
```bash
token="${2:-}"
```
Delete that line. Also find and delete any subsequent `_bridge_autosync "$repo_path" "$token"` calls in script mode, replacing with `_bridge_autosync "$repo_path"`.

- [ ] **Step 5: Verify no remaining token argument passing in autosync**

```bash
grep -n "token" /path/to/bridge/bridge-autosync.sh
```

Expected: only the `_autosync_telegram` function body references `token` as a local variable resolved from bridge-bot.json.

- [ ] **Step 6: Commit**

```bash
git add bridge-autosync.sh
git commit -m "feat(slots): autosync reads bot token from bridge-bot.json directly"
```

---

## Task 7: Update `setup-claude-channels.sh`

**Files:**
- Modify: `setup-claude-channels.sh`

- [ ] **Step 1: Remove the `MAX` variable**

Find:
```bash
MAX="${BRIDGE_MAX_SLOTS:-6}"
```
Delete it.

- [ ] **Step 2: Remove the `TOKENS` variable and `slot-tokens.json` setup**

Find:
```bash
TOKENS="$CACHE/slot-tokens.json"
```
Delete it.

- [ ] **Step 3: Remove section 2 entirely — the per-slot bot token loop**

Find and delete from the section 2 header through the section boundary:

```bash
# --- 2. Per-slot bot tokens (Passbolt resource IDs) ---
echo "Per-slot bot tokens — paste the Passbolt resource ID for each bot."
echo "  Slot 0 = admin bot (BotFather-named). Empty = skip; existing values shown as default."
tokens_json=$(json_read "$TOKENS")
result_json="$tokens_json"

for n in $(seq 0 "$MAX"); do
  cur=$(printf '%s' "$tokens_json" | json_get "$n")
  if [ "$n" = 0 ]; then
    echo "  slot 0 (admin bot — empty disables admin-bot title management)"
  else
    echo "  echo "  slot $n (@claude_freax_s${n}_bot)"
  fi
  pb_id=$(prompt_default "    Passbolt id" "$cur")
  if [ -z "$pb_id" ]; then
    echo "    (skipped)"
    continue
  fi
  if validate_passbolt "$pb_id"; then
    result_json=$(printf '%s' "$result_json" | json_set "$n" "$pb_id")
    echo "    ✓ token resolved"
  else
    echo "    ✗ Passbolt id did not resolve to a token — keeping previous (if any)" >&2
  fi
done

printf '%s' "$result_json" | write_atomic "$TOKENS"

slot_list=$(python3 -c '
import json, sys
d = json.load(open(sys.argv[1]))
print(" ".join(sorted(d, key=lambda x: int(x) if x.isdigit() else 10**9)))
' "$TOKENS")

echo
echo "✓ slot-tokens.json: ${slot_list:-(empty)}"
```

Delete all of the above and renumber the remaining section header from `# --- 3. bridge-bot` to `# --- 2. bridge-bot`.

Also update the opening echo that lists slot range:
```bash
echo "  slots: 1..$MAX"
```
Delete this line (it references the now-removed `$MAX`).

- [ ] **Step 4: Verify setup script syntax**

```bash
bash -n /path/to/bridge/setup-claude-channels.sh && echo OK
```

Expected: `OK`.

- [ ] **Step 5: Commit**

```bash
git add setup-claude-channels.sh
git commit -m "feat(slots): remove per-slot bot token setup from setup-claude-channels.sh"
```

---

## Task 8: Bump version, update CHANGELOG, close issue

**Files:**
- Modify: `bridge.sh` (version bump near top)
- Modify: `CHANGELOG.md`

Current version: `1.41.10`. This is a minor feature (new capability, removes an architectural constraint) → bump to `1.42.0`.

- [ ] **Step 1: Bump `_BRIDGE_VERSION` in `bridge.sh`**

Find:
```bash
_BRIDGE_VERSION="1.41.10"
```
Replace with:
```bash
_BRIDGE_VERSION="1.42.0"
```

- [ ] **Step 2: Add CHANGELOG entry**

Open `CHANGELOG.md` and add at the top (after the `## [Unreleased]` line if present, otherwise before the first `## [` entry):

```markdown
## [1.42.0] - 2026-05-25

### Added
- `_bridge_notify` helper: single Telegram notification channel via bridge-bot token.
  All lifecycle events (session start, idle, usage limit, session end, autosync) now
  route through `bridge-bot.json` — no per-slot bots required.

### Changed
- Slot allocator now has no upper bound: slots are allocated dynamically as the lowest
  unused integer key in `slots.json`. `BRIDGE_MAX_SLOTS` env var removed.
- `setup-claude-channels.sh` no longer prompts for per-slot bot tokens.
- `bridge-autosync.sh` resolves the bot token from `bridge-bot.json` directly;
  the `token` parameter has been dropped from `_bridge_autosync` and script mode.
- `session-closed` tmux hook no longer passes a bot token to `bridge-autosync.sh`.

### Removed
- `_BRIDGE_MAX_SLOTS` variable and all references.
- `_BRIDGE_SLOT_TOKENS` / `slot-tokens.json` — no longer read or written.
- `_bridge_telegram_setup`, `_bridge_telegram_cleanup`, `_bridge_telegram_page`,
  `_bridge_admin_status_update` functions.
- Slot displacement logic (oldest-session auto-kill with 5s countdown).
```

- [ ] **Step 3: Run unit tests one final time**

```bash
bats tests/unit/
```

Expected: all PASS.

- [ ] **Step 4: Commit**

```bash
git add bridge.sh CHANGELOG.md
git commit -m "feat(slots): v1.42.0 — unlimited slots, single Telegram channel via bridge-bot"
```

- [ ] **Step 5: Close issue #32**

```bash
gh issue close 32 --comment "Implemented in v1.42.0. Slots are now unlimited (no displacement). All Telegram notifications route through bridge-bot. Per-slot bots and slot-tokens.json removed."
```

---

## Self-Review

**Spec coverage check:**

| Spec requirement | Task |
|---|---|
| Remove `_BRIDGE_MAX_SLOTS` hard ceiling | Task 2 |
| Remove displacement logic | Task 2 |
| Add `_bridge_notify` reading from `bridge-bot.json` | Task 3 |
| Delete `_bridge_telegram_setup/cleanup/page/admin_status_update` | Task 3 |
| Update `_bridge_slot_record` / `_bridge_slot_free` (remove admin calls) | Task 3 |
| Update launch path (start/close notifications) | Task 4 |
| Update `notify.sh` | Task 5 |
| Update `bridge-watcher.sh` | Task 5 |
| Update `bridge-autosync.sh` | Task 6 |
| Update `setup-claude-channels.sh` | Task 7 |
| Bump version + CHANGELOG | Task 8 |

All spec requirements covered. ✓
