# bridge presence-aware Telegram pages — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add proactive Telegram pages to bridge for HITL pauses and 5h-limit hits, gated by per-slot tmux client presence with a global `bridge away/back/here` override.

**Architecture:** Two generators (Notification hook for HITL events; tmux-pane watcher daemon for usage limit) feed a per-slot gate that consults a global presence file (`~/.cache/bridge/presence`) and the slot's tmux client count. Hook scripts live in `shell/bridge-hooks/` and are wired into each slot's `~/.claude-s<N>/settings.json` at slot allocation. Pages go through a new `_bridge_telegram_page` helper that reuses the existing per-slot bot token + owner-id lookup.

**Tech Stack:** Bash, Python 3 (one-liners for JSON manipulation, matching existing bridge style), tmux, curl, Telegram Bot HTTPS API.

**Spec:** `docs/superpowers/specs/2026-05-02-bridge-presence-aware-telegram-pages-design.md`

**Project conventions:**
- Conventional Commits required (`feat(bridge): ...`, `fix(bridge): ...`).
- Bump `_BRIDGE_VERSION` in `shell/bridge.sh` per semver. This plan adds a feature → minor bump: `1.8.3 → 1.9.0`. Bumped once in Task 1 Step 5; subsequent commits in this plan keep the version at `1.9.0` since they are all part of the same feature.
- No existing test framework; verify each task by running concrete commands and checking output. Functional bash code is verified inline in the task.

**File structure:**

| Path | Status | Responsibility |
|---|---|---|
| `shell/bridge.sh` | Modified | New helpers (`_bridge_presence_set/show`, `_bridge_should_page`, `_bridge_telegram_page`, `_bridge_install_hooks`, `_bridge_watcher_start`); arg-parser dispatch for new sub-commands; integration into `_bridge_slot_allocate` and `_bridge_slot_free`; help text; version bump. |
| `shell/bridge-hooks/notify.sh` | New | Notification hook. Dispatches on `notification_type`. Handles `idle_prompt` (debounced) and `elicitation_dialog` (immediate). |
| `shell/bridge-hooks/clear-idle.sh` | New | UserPromptSubmit hook. Removes `<slot>.idle-since` marker. |
| `shell/bridge-watcher.sh` | New | Background daemon polling tmux panes for the usage-limit phrase. |
| `shell/BRIDGE.md` | Modified | Document new presence sub-commands and integration points. |

**Runtime state (created on first use):**

| Path | Purpose |
|---|---|
| `~/.cache/bridge/presence` | One line: `auto` / `away` / `here`. Default `auto`. |
| `~/.cache/bridge/sessions/<slot>.idle-since` | Touched on `idle_prompt`, removed on `UserPromptSubmit`. |
| `~/.cache/bridge/sessions/<slot>.limit-paged` | Per-session dedup marker for the watcher. |
| `~/.cache/bridge/watcher.pid` | Watcher PID. |
| `~/.cache/bridge/watcher.log` | Watcher daemon log (rotated by size, max 1 MB). |
| `~/.cache/bridge/hooks.log` | Hook script log (rotated by size, max 1 MB). |
| `~/.cache/bridge/hooks.lock` | Advisory lock for `_bridge_install_hooks` settings.json merges. |

---

## Task 1: Presence helpers (`_bridge_presence_set`, `_bridge_presence_show`)

**Files:**
- Modify: `shell/bridge.sh` — insert new functions immediately above `_bridge_slot_status` (current line ~826).

- [ ] **Step 1: Add `_bridge_presence_set` and `_bridge_presence_show`**

In `shell/bridge.sh`, immediately above the line `_bridge_slot_status() {` (current line ~826), insert:

```bash
# Presence file at $_BRIDGE_CACHE/presence holds one of: auto | away | here.
# Missing or unrecognized → treated as auto.
_BRIDGE_PRESENCE_FILE="$_BRIDGE_CACHE/presence"

# Read the current presence mode. Echoes auto|away|here. Default: auto.
_bridge_presence_mode() {
  local m
  m=$(cat "$_BRIDGE_PRESENCE_FILE" 2>/dev/null | tr -d '[:space:]')
  case "$m" in
    auto|away|here) printf '%s' "$m" ;;
    *)              printf 'auto' ;;
  esac
}

# Set presence mode. $1 must be auto|away|here. Prints a one-line confirmation.
_bridge_presence_set() {
  local mode="$1"
  case "$mode" in
    auto|away|here) ;;
    *) echo "bridge: invalid presence mode '$mode' (expected auto|away|here)" >&2; return 2 ;;
  esac
  mkdir -p "$_BRIDGE_CACHE"
  printf '%s\n' "$mode" > "$_BRIDGE_PRESENCE_FILE"
  echo "bridge: presence set to '$mode'"
}

# Print current presence mode and per-slot effective state.
_bridge_presence_show() {
  local mode
  mode=$(_bridge_presence_mode)
  echo "presence mode: $mode"
  [ -f "$_BRIDGE_SLOTS_FILE" ] || { echo "(no slots configured)"; return; }
  python3 -c "
import json, subprocess
with open('$_BRIDGE_SLOTS_FILE') as f: d = json.load(f)
mode = '$mode'
for n in sorted(d.get('slots', {}).keys(), key=int):
    v = d['slots'][n]
    if not v:
        print(f's{n}: free')
        continue
    sess = v.get('session') or ''
    if mode == 'away':
        eff = 'away (forced)'
    elif mode == 'here':
        eff = 'present (forced)'
    elif sess:
        r = subprocess.run(['tmux','list-clients','-t',sess],
                           stdout=subprocess.PIPE, stderr=subprocess.DEVNULL)
        n_clients = len([l for l in r.stdout.decode().splitlines() if l.strip()])
        eff = 'present' if n_clients > 0 else 'away'
    else:
        eff = 'unknown (no session recorded)'
    print(f's{n}: {eff}  (repo={v.get(\"repo\",\"?\")}, session={sess or \"—\"})')
" 2>/dev/null
}
```

- [ ] **Step 2: Verify `_bridge_presence_mode` returns the right values**

Run:

```bash
source /home/freax/projects/repos/github/freaxnx01/public/config/shell/bridge.sh

# Case 1: file missing → auto
rm -f ~/.cache/bridge/presence
[ "$(_bridge_presence_mode)" = "auto" ] && echo OK1 || echo FAIL1

# Case 2: file = "away" → away
echo away > ~/.cache/bridge/presence
[ "$(_bridge_presence_mode)" = "away" ] && echo OK2 || echo FAIL2

# Case 3: file = "here" → here
echo here > ~/.cache/bridge/presence
[ "$(_bridge_presence_mode)" = "here" ] && echo OK3 || echo FAIL3

# Case 4: file = "garbage" → auto (safe default)
echo garbage > ~/.cache/bridge/presence
[ "$(_bridge_presence_mode)" = "auto" ] && echo OK4 || echo FAIL4

# Cleanup
rm -f ~/.cache/bridge/presence
```

Expected: four lines `OK1`, `OK2`, `OK3`, `OK4`. If any FAIL, fix the function before continuing.

- [ ] **Step 3: Verify `_bridge_presence_set` writes the file and rejects bad input**

Run:

```bash
_bridge_presence_set away
[ "$(cat ~/.cache/bridge/presence)" = "away" ] && echo OK1 || echo FAIL1

_bridge_presence_set bogus 2>/dev/null
[ "$?" = "2" ] && echo OK2 || echo FAIL2

_bridge_presence_set auto
[ "$(cat ~/.cache/bridge/presence)" = "auto" ] && echo OK3 || echo FAIL3

rm -f ~/.cache/bridge/presence
```

Expected: `OK1`, `OK2`, `OK3`. Note that step 2 also prints "bridge: presence set to 'away'" / "'auto'" to stdout — that's expected confirmation output.

- [ ] **Step 4: Verify `_bridge_presence_show` runs without crashing**

Run:

```bash
_bridge_presence_show
```

Expected: prints `presence mode: auto`, then either `(no slots configured)` if `$_BRIDGE_SLOTS_FILE` is absent, or per-slot lines like `s1: free` / `s4: present  (repo=foo, session=foo)`. No tracebacks, no errors.

- [ ] **Step 5: Bump version and commit**

Edit `shell/bridge.sh` line 25:

```bash
_BRIDGE_VERSION="1.9.0"
```

(This single bump covers the entire plan; subsequent tasks will leave it at 1.9.0.)

Then:

```bash
cd /home/freax/projects/repos/github/freaxnx01/public/config
git add shell/bridge.sh
git commit -m "feat(bridge): add presence file helpers"
```

---

## Task 2: Gate function (`_bridge_should_page`)

**Files:**
- Modify: `shell/bridge.sh` — add immediately below the presence helpers from Task 1.

- [ ] **Step 1: Add `_bridge_should_page`**

In `shell/bridge.sh`, immediately below `_bridge_presence_show()`'s closing `}` from Task 1, insert:

```bash
# Decide whether slot $1 should send a Telegram page right now.
# Returns 0 (page) or 1 (silent).
_bridge_should_page() {
  local slot="$1"
  local mode
  mode=$(_bridge_presence_mode)
  case "$mode" in
    away) return 0 ;;
    here) return 1 ;;
    auto)
      # Look up the slot's tmux session name from slots.json
      local sess
      sess=$(python3 -c "
import json
try:
    with open('$_BRIDGE_SLOTS_FILE') as f: d = json.load(f)
    v = d.get('slots', {}).get('$slot')
    print((v or {}).get('session') or '')
except Exception:
    pass
" 2>/dev/null)
      # No recorded session → assume away (page); we'd rather notify than miss
      [ -z "$sess" ] && return 0
      # Count attached clients
      local n
      n=$(tmux list-clients -t "$sess" 2>/dev/null | wc -l)
      [ "$n" -eq 0 ] && return 0 || return 1
      ;;
  esac
}
```

- [ ] **Step 2: Verify gate logic for each presence mode**

Run:

```bash
source /home/freax/projects/repos/github/freaxnx01/public/config/shell/bridge.sh

# away → always page (return 0)
echo away > ~/.cache/bridge/presence
_bridge_should_page 99 && echo OK1 || echo FAIL1

# here → never page (return 1)
echo here > ~/.cache/bridge/presence
_bridge_should_page 99 && echo FAIL2 || echo OK2

# auto + nonexistent slot → page (no recorded session = assume away)
echo auto > ~/.cache/bridge/presence
_bridge_should_page 99 && echo OK3 || echo FAIL3

rm -f ~/.cache/bridge/presence
```

Expected: `OK1`, `OK2`, `OK3`.

- [ ] **Step 3: Verify auto-mode tmux check**

Create a fake tmux session and a fake slots.json entry pointing at it, then verify the gate flips when the session is detached vs. attached.

```bash
# Create a fake tmux session
tmux new-session -d -s bridge_test_gate

# Build a fake slots.json that records this session for slot 99
mkdir -p ~/.cache/bridge
python3 -c "
import json
try:
    with open('$HOME/.cache/bridge/slots.json') as f: d = json.load(f)
except: d = {'slots': {}}
d.setdefault('slots', {})['99'] = {'repo':'test','worktree':None,'pid':$$,'session':'bridge_test_gate'}
with open('$HOME/.cache/bridge/slots.json','w') as f: json.dump(d, f, indent=2)
"

echo auto > ~/.cache/bridge/presence

# Detached → 0 clients → page (return 0)
_bridge_should_page 99 && echo OK_DETACHED || echo FAIL_DETACHED

# (Optional manual: tmux attach -t bridge_test_gate in another terminal,
# rerun _bridge_should_page 99 → should return 1 (present). Skip if no
# easy second terminal — the detached path is the critical one.)

# Cleanup
tmux kill-session -t bridge_test_gate 2>/dev/null
python3 -c "
import json
with open('$HOME/.cache/bridge/slots.json') as f: d = json.load(f)
d['slots'].pop('99', None)
with open('$HOME/.cache/bridge/slots.json','w') as f: json.dump(d, f, indent=2)
"
rm -f ~/.cache/bridge/presence
```

Expected: `OK_DETACHED`.

- [ ] **Step 4: Commit**

```bash
cd /home/freax/projects/repos/github/freaxnx01/public/config
git add shell/bridge.sh
git commit -m "feat(bridge): add presence gate function"
```

---

## Task 3: Wire CLI sub-commands (`away`, `back`, `here`, `presence`)

**Files:**
- Modify: `shell/bridge.sh` — extend the `bridge()` arg parser around line 1200 (where `update` is dispatched).

- [ ] **Step 1: Add sub-command dispatch**

In `shell/bridge.sh`, find the block (around line 1200):

```bash
  # `bridge update` — pull the config repo and re-source. Handled before the
  # update hint and meta-warm so we don't nag the user during an update.
  if [ "${1:-}" = "update" ]; then
    _bridge_update
    return
  fi
```

Immediately after the closing `fi`, insert:

```bash
  # Presence sub-commands. Handled here (before the launch path) so they
  # work from any cwd, regardless of repo membership.
  case "${1:-}" in
    away)     _bridge_presence_set away; return ;;
    back)     _bridge_presence_set auto; return ;;
    here)     _bridge_presence_set here; return ;;
    presence) _bridge_presence_show;     return ;;
  esac
```

- [ ] **Step 2: Verify the sub-commands work end-to-end**

Run:

```bash
# Re-source after the edit
source /home/freax/projects/repos/github/freaxnx01/public/config/shell/bridge.sh

bridge away
[ "$(cat ~/.cache/bridge/presence)" = "away" ] && echo OK1 || echo FAIL1

bridge here
[ "$(cat ~/.cache/bridge/presence)" = "here" ] && echo OK2 || echo FAIL2

bridge back
[ "$(cat ~/.cache/bridge/presence)" = "auto" ] && echo OK3 || echo FAIL3

bridge presence | head -1 | grep -q "presence mode: auto" && echo OK4 || echo FAIL4

rm -f ~/.cache/bridge/presence
```

Expected: `OK1` `OK2` `OK3` `OK4`.

- [ ] **Step 3: Commit**

```bash
cd /home/freax/projects/repos/github/freaxnx01/public/config
git add shell/bridge.sh
git commit -m "feat(bridge): add away/back/here/presence sub-commands"
```

---

## Task 4: Page sender (`_bridge_telegram_page`)

**Files:**
- Modify: `shell/bridge.sh` — add immediately below the gate function from Task 2.

- [ ] **Step 1: Add `_bridge_telegram_page`**

In `shell/bridge.sh`, immediately below `_bridge_should_page()`'s closing `}` from Task 2, insert:

```bash
# Send arbitrary text via slot $1's bot to the configured owner.
# Args: $1 = slot, $2 = message text. Best-effort; never fails the caller.
# Reads the slot bot token from Passbolt via slot-tokens.json, owner from owner.json.
_bridge_telegram_page() {
  local slot="$1" text="$2"
  [ -z "$slot" ] && return 0
  [ -z "$text" ] && return 0

  local pb_id token owner_id
  pb_id=$(python3 -c "
import json
try:
    with open('$_BRIDGE_SLOT_TOKENS') as f: d = json.load(f)
    print(d.get('$slot', ''))
except Exception:
    pass
" 2>/dev/null)
  [ -z "$pb_id" ] && return 0

  token=$(passbolt get resource --id "$pb_id" 2>/dev/null | awk -F": " '/^Password:/ {print $2}')
  [ -z "$token" ] && return 0

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
    -d "$(python3 -c "import json,sys; print(json.dumps({'chat_id': '$owner_id', 'text': sys.stdin.read()}))" <<< "$text")" \
    >/dev/null 2>&1 || true
}
```

- [ ] **Step 2: Verify the function exits cleanly when token is missing**

Run:

```bash
source /home/freax/projects/repos/github/freaxnx01/public/config/shell/bridge.sh

# Slot 99 has no token → function should silently return 0
_bridge_telegram_page 99 "test message"
[ "$?" = "0" ] && echo OK1 || echo FAIL1

# Empty args → should return 0 with no curl call
_bridge_telegram_page "" ""
[ "$?" = "0" ] && echo OK2 || echo FAIL2
```

Expected: `OK1`, `OK2`.

- [ ] **Step 3: Live-test with a real slot (manual, optional but recommended)**

Pick a slot that has a token configured (e.g. `s1`). Run:

```bash
source /home/freax/projects/repos/github/freaxnx01/public/config/shell/bridge.sh
_bridge_telegram_page 1 "🧪 bridge presence-page test (Task 4)"
```

Check the `@claude_freax_s1_bot` chat on Telegram — the message should appear within a few seconds. If nothing arrives:
- `_BRIDGE_SLOT_TOKENS` or `_BRIDGE_OWNER` may be unset/missing
- The slot may not have a token — list with `cat ~/.cache/bridge/slot-tokens.json`
- Run the curl by hand with the token to isolate

- [ ] **Step 4: Commit**

```bash
cd /home/freax/projects/repos/github/freaxnx01/public/config
git add shell/bridge.sh
git commit -m "feat(bridge): add _bridge_telegram_page helper"
```

---

## Task 5: Hook installer (`_bridge_install_hooks`)

**Files:**
- Modify: `shell/bridge.sh` — add immediately below `_bridge_telegram_page` from Task 4.

- [ ] **Step 1: Add `_bridge_install_hooks`**

In `shell/bridge.sh`, immediately below `_bridge_telegram_page()`'s closing `}`, insert:

```bash
# Idempotently merge the Notification + UserPromptSubmit hooks into slot $1's
# settings.json (~/.claude-s<N>/settings.json). The hook commands include the
# slot number as a positional arg so the hook scripts know which slot fired.
_bridge_install_hooks() {
  local slot="$1"
  [ -z "$slot" ] && return 1
  local cfg_dir="$HOME/.claude-s${slot}"
  local cfg="$cfg_dir/settings.json"
  local notify="$_BRIDGE_DIR/bridge-hooks/notify.sh"
  local clear="$_BRIDGE_DIR/bridge-hooks/clear-idle.sh"

  [ -x "$notify" ] || chmod +x "$notify" 2>/dev/null
  [ -x "$clear" ]  || chmod +x "$clear"  2>/dev/null

  mkdir -p "$cfg_dir"
  exec {_lock_fd}>"$_BRIDGE_CACHE/hooks.lock"
  flock "$_lock_fd"
  python3 -c "
import json, os
cfg = '$cfg'
notify_cmd = '$notify $slot'
clear_cmd  = '$clear $slot'

try:
    with open(cfg) as f: d = json.load(f)
except FileNotFoundError:
    d = {}
except json.JSONDecodeError:
    # Corrupt — back up and start fresh
    os.rename(cfg, cfg + '.corrupt')
    d = {}

hooks = d.setdefault('hooks', {})

def has_cmd(entries, cmd):
    for e in entries or []:
        for h in e.get('hooks', []) or []:
            if h.get('command') == cmd: return True
    return False

def add_cmd(key, cmd):
    entries = hooks.setdefault(key, [])
    if has_cmd(entries, cmd): return
    entries.append({'matcher': '', 'hooks': [{'type': 'command', 'command': cmd}]})

add_cmd('Notification',      notify_cmd)
add_cmd('UserPromptSubmit',  clear_cmd)

with open(cfg, 'w') as f: json.dump(d, f, indent=2)
" 2>/dev/null
  flock -u "$_lock_fd"
}
```

- [ ] **Step 2: Verify hook installer is idempotent and creates the right JSON**

Run (uses a throwaway slot dir at `~/.claude-s99` so we don't disturb real slots):

```bash
source /home/freax/projects/repos/github/freaxnx01/public/config/shell/bridge.sh

# Clean throwaway state
rm -rf ~/.claude-s99
mkdir -p ~/.cache/bridge

_bridge_install_hooks 99
[ -f ~/.claude-s99/settings.json ] && echo OK1_FILE || echo FAIL1_FILE

# Both keys present
python3 -c "
import json
d = json.load(open('$HOME/.claude-s99/settings.json'))
assert 'Notification'     in d.get('hooks', {}), 'missing Notification'
assert 'UserPromptSubmit' in d.get('hooks', {}), 'missing UserPromptSubmit'
print('OK2_KEYS')
"

# Run twice — entries should not duplicate
_bridge_install_hooks 99
_bridge_install_hooks 99
python3 -c "
import json
d = json.load(open('$HOME/.claude-s99/settings.json'))
n = len(d['hooks']['Notification'])
u = len(d['hooks']['UserPromptSubmit'])
assert n == 1, f'Notification has {n} entries (expected 1)'
assert u == 1, f'UserPromptSubmit has {u} entries (expected 1)'
print('OK3_IDEMPOTENT')
"

# Existing settings get preserved (regression: don't clobber user keys)
echo '{"statusLine":{"type":"command","command":"oh-my-posh"},"hooks":{}}' > ~/.claude-s99/settings.json
_bridge_install_hooks 99
python3 -c "
import json
d = json.load(open('$HOME/.claude-s99/settings.json'))
assert d['statusLine']['type'] == 'command', 'statusLine clobbered!'
print('OK4_PRESERVE')
"

# Cleanup
rm -rf ~/.claude-s99
```

Expected: `OK1_FILE`, `OK2_KEYS`, `OK3_IDEMPOTENT`, `OK4_PRESERVE`.

- [ ] **Step 3: Commit**

```bash
cd /home/freax/projects/repos/github/freaxnx01/public/config
git add shell/bridge.sh
git commit -m "feat(bridge): add _bridge_install_hooks function"
```

---

## Task 6: `clear-idle.sh` hook script

**Files:**
- Create: `shell/bridge-hooks/clear-idle.sh`

- [ ] **Step 1: Create the directory**

```bash
mkdir -p /home/freax/projects/repos/github/freaxnx01/public/config/shell/bridge-hooks
```

- [ ] **Step 2: Write `clear-idle.sh`**

Create `shell/bridge-hooks/clear-idle.sh`:

```bash
#!/usr/bin/env bash
# UserPromptSubmit hook for bridge presence-aware Telegram pages.
#
# Removes the per-slot idle marker so a debounced page that hasn't
# fired yet is silently cancelled.
#
# Args: $1 = slot number (passed via the hook command in settings.json)
# Stdin: Claude Code hook payload (JSON) — not consumed; drained.

set -u

SLOT="${1:-}"
CACHE="$HOME/.cache/bridge"
LOG="$CACHE/hooks.log"

# Drain stdin so Claude Code doesn't see a SIGPIPE
cat >/dev/null 2>&1 || true

[ -z "$SLOT" ] && {
  printf '[%s] clear-idle: missing slot arg\n' "$(date -Iseconds)" >>"$LOG" 2>/dev/null
  exit 0
}

rm -f "$CACHE/sessions/${SLOT}.idle-since" 2>/dev/null

# Rotate log if > 1MB
if [ -f "$LOG" ] && [ "$(stat -c %s "$LOG" 2>/dev/null || echo 0)" -gt 1048576 ]; then
  mv "$LOG" "${LOG}.1" 2>/dev/null
fi

exit 0
```

Make executable:

```bash
chmod +x /home/freax/projects/repos/github/freaxnx01/public/config/shell/bridge-hooks/clear-idle.sh
```

- [ ] **Step 3: Verify script behavior**

```bash
# Pre-create a marker
mkdir -p ~/.cache/bridge/sessions
touch ~/.cache/bridge/sessions/99.idle-since

# Simulate a hook fire
echo '{"event":"UserPromptSubmit"}' | /home/freax/projects/repos/github/freaxnx01/public/config/shell/bridge-hooks/clear-idle.sh 99

[ ! -f ~/.cache/bridge/sessions/99.idle-since ] && echo OK1_REMOVED || echo FAIL1_REMOVED

# No marker present → still exits 0, no error
echo '{}' | /home/freax/projects/repos/github/freaxnx01/public/config/shell/bridge-hooks/clear-idle.sh 99
[ "$?" = "0" ] && echo OK2_NOOP || echo FAIL2_NOOP

# Missing slot arg → logs and exits 0
echo '{}' | /home/freax/projects/repos/github/freaxnx01/public/config/shell/bridge-hooks/clear-idle.sh
[ "$?" = "0" ] && echo OK3_NOSLOT || echo FAIL3_NOSLOT
grep -q "missing slot arg" ~/.cache/bridge/hooks.log && echo OK4_LOGGED || echo FAIL4_LOGGED
```

Expected: `OK1_REMOVED`, `OK2_NOOP`, `OK3_NOSLOT`, `OK4_LOGGED`.

- [ ] **Step 4: Commit**

```bash
cd /home/freax/projects/repos/github/freaxnx01/public/config
git add shell/bridge-hooks/clear-idle.sh
git commit -m "feat(bridge): add clear-idle.sh UserPromptSubmit hook"
```

---

## Task 7: `notify.sh` hook script

**Files:**
- Create: `shell/bridge-hooks/notify.sh`

- [ ] **Step 1: Write `notify.sh`**

Create `shell/bridge-hooks/notify.sh`:

```bash
#!/usr/bin/env bash
# Notification hook for bridge presence-aware Telegram pages.
#
# Acts only on idle_prompt (debounced 120s) and elicitation_dialog (immediate).
# All other notification types are ignored.
#
# Args: $1 = slot number (passed via the hook command in settings.json)
# Stdin: Claude Code hook payload (JSON) with at least .notification_type or .type.

set -u

SLOT="${1:-}"
CACHE="$HOME/.cache/bridge"
LOG="$CACHE/hooks.log"
DEBOUNCE_SEC=120

# Source bridge for _bridge_should_page, _bridge_telegram_page, etc.
# Self-locating: hook lives at $_BRIDGE_DIR/bridge-hooks/notify.sh
HOOK_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BRIDGE_SH="$(dirname "$HOOK_DIR")/bridge.sh"
# shellcheck disable=SC1090
. "$BRIDGE_SH" 2>/dev/null || {
  printf '[%s] notify: failed to source %s\n' "$(date -Iseconds)" "$BRIDGE_SH" >>"$LOG"
  exit 0
}

mkdir -p "$CACHE/sessions"

log() { printf '[%s] notify(s%s): %s\n' "$(date -Iseconds)" "$SLOT" "$*" >>"$LOG" 2>/dev/null; }

[ -z "$SLOT" ] && { log "missing slot arg"; exit 0; }

# Read full payload
PAYLOAD=$(cat 2>/dev/null || true)
log "payload: $PAYLOAD"

# Extract notification_type (or type, depending on schema). Try both.
NTYPE=$(echo "$PAYLOAD" | python3 -c "
import json, sys
try:
    d = json.load(sys.stdin)
    print(d.get('notification_type') or d.get('type') or '')
except Exception:
    pass
" 2>/dev/null)

log "notification_type=$NTYPE"

# Build the page text from slot metadata + tmux pane snippet
build_page_text() {
  local slot="$1" header="$2"
  python3 -c "
import json, subprocess, re, sys
slot = '$slot'
header = '''$header'''
try:
    with open('$_BRIDGE_SLOTS_FILE') as f: d = json.load(f)
    v = d.get('slots', {}).get(slot) or {}
except Exception:
    v = {}

repo  = v.get('repo')   or '?'
wt    = v.get('worktree') or ''
sess  = v.get('session') or ''

snippet = ''
if sess:
    try:
        out = subprocess.run(['tmux','capture-pane','-p','-t',sess],
                             stdout=subprocess.PIPE, stderr=subprocess.DEVNULL,
                             timeout=2).stdout.decode('utf-8','replace')
        out = re.sub(r'\x1b\[[0-9;]*[mGKH]', '', out)
        lines = [l.rstrip() for l in out.splitlines() if l.strip()]
        snippet = '\n'.join(lines[-12:])[-500:]
    except Exception:
        pass

bracket = f's{slot}/{repo}'
if wt: bracket += f' worktree:{wt}'
text = f'{header} [{bracket}]'
if snippet:
    text += '\n\nLast:\n> ' + snippet.replace('\n', '\n> ')
print(text)
" 2>/dev/null
}

case "$NTYPE" in
  idle_prompt)
    # Touch marker, schedule delayed check
    touch "$CACHE/sessions/${SLOT}.idle-since"
    log "scheduled debounce check in ${DEBOUNCE_SEC}s"
    (
      sleep "$DEBOUNCE_SEC"
      # Marker still present? user hasn't replied since
      [ -f "$CACHE/sessions/${SLOT}.idle-since" ] || exit 0
      # Re-check the gate — user might have attached during the wait
      _bridge_should_page "$SLOT" || { log "gate says present at delayed check, skip"; exit 0; }
      TEXT=$(build_page_text "$SLOT" "🤔 Claude is waiting for input")
      _bridge_telegram_page "$SLOT" "$TEXT"
      log "sent idle_prompt page"
    ) &disown
    ;;
  elicitation_dialog)
    # Immediate, gated
    if _bridge_should_page "$SLOT"; then
      TEXT=$(build_page_text "$SLOT" "🤔 Claude needs input (elicitation)")
      _bridge_telegram_page "$SLOT" "$TEXT"
      log "sent elicitation_dialog page"
    else
      log "gate says present, skip elicitation_dialog"
    fi
    ;;
  *)
    log "ignoring type=$NTYPE"
    ;;
esac

# Rotate log if > 1MB
if [ -f "$LOG" ] && [ "$(stat -c %s "$LOG" 2>/dev/null || echo 0)" -gt 1048576 ]; then
  mv "$LOG" "${LOG}.1" 2>/dev/null
fi

exit 0
```

Make executable:

```bash
chmod +x /home/freax/projects/repos/github/freaxnx01/public/config/shell/bridge-hooks/notify.sh
```

- [ ] **Step 2: Verify the dispatch handles unknown types as no-op**

```bash
mkdir -p ~/.cache/bridge/sessions
rm -f ~/.cache/bridge/sessions/99.idle-since

# Unknown type → ignored, no marker created
echo '{"notification_type":"auth_success"}' \
  | /home/freax/projects/repos/github/freaxnx01/public/config/shell/bridge-hooks/notify.sh 99
[ ! -f ~/.cache/bridge/sessions/99.idle-since ] && echo OK1_AUTH_IGNORED || echo FAIL1
grep -q "ignoring type=auth_success" ~/.cache/bridge/hooks.log && echo OK2_LOGGED || echo FAIL2
```

Expected: `OK1_AUTH_IGNORED`, `OK2_LOGGED`.

- [ ] **Step 3: Verify `idle_prompt` creates the marker and schedules the debounce**

```bash
rm -f ~/.cache/bridge/sessions/99.idle-since

echo '{"notification_type":"idle_prompt"}' \
  | /home/freax/projects/repos/github/freaxnx01/public/config/shell/bridge-hooks/notify.sh 99

[ -f ~/.cache/bridge/sessions/99.idle-since ] && echo OK1_MARKER || echo FAIL1
grep -q "scheduled debounce check" ~/.cache/bridge/hooks.log && echo OK2_LOGGED || echo FAIL2
```

Expected: `OK1_MARKER`, `OK2_LOGGED`.

- [ ] **Step 4: Verify `clear-idle.sh` cancels the pending page**

```bash
# Marker should still exist from Step 3. Clear it.
echo '{}' | /home/freax/projects/repos/github/freaxnx01/public/config/shell/bridge-hooks/clear-idle.sh 99

[ ! -f ~/.cache/bridge/sessions/99.idle-since ] && echo OK_CLEARED || echo FAIL_CLEARED
```

Expected: `OK_CLEARED`. (Note: the background `sleep 120 && check_and_page` is still running from Step 3, but it'll find the marker missing and exit silently. You can verify by waiting 2 minutes — no Telegram message arrives.)

- [ ] **Step 5: Commit**

```bash
cd /home/freax/projects/repos/github/freaxnx01/public/config
git add shell/bridge-hooks/notify.sh
git commit -m "feat(bridge): add notify.sh Notification hook"
```

---

## Task 8: Watcher daemon (`bridge-watcher.sh`)

**Files:**
- Create: `shell/bridge-watcher.sh`
- Modify: `shell/bridge.sh` — add `_bridge_watcher_start` helper.

- [ ] **Step 1: Write `bridge-watcher.sh`**

Create `shell/bridge-watcher.sh`:

```bash
#!/usr/bin/env bash
# bridge usage-limit watcher.
#
# Polls each occupied slot's tmux pane every POLL_SEC for the usage-limit
# phrase. On match (and gate-permitting), sends a Telegram page via the
# slot's bot. Self-exits when no slots are occupied for two consecutive
# polls (60s grace).

set -u

CACHE="$HOME/.cache/bridge"
LOG="$CACHE/watcher.log"
PID_FILE="$CACHE/watcher.pid"
SLOTS_FILE="$CACHE/slots.json"
POLL_SEC=30

# Self-locate bridge.sh and source it for helpers.
HOOK_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BRIDGE_SH="$HOOK_DIR/bridge.sh"
# shellcheck disable=SC1090
. "$BRIDGE_SH" 2>/dev/null || {
  echo "watcher: cannot source $BRIDGE_SH" >&2
  exit 1
}

# Usage-limit detection: literal substring match. Initial pattern is the
# common Claude Code wording. Tune as needed; logs all candidate pane snapshots
# until confirmed (see hooks.log / watcher.log for first real fire).
LIMIT_PATTERNS=(
  "Claude usage limit reached"
  "5-hour limit reached"
)

log() { printf '[%s] %s\n' "$(date -Iseconds)" "$*" >>"$LOG" 2>/dev/null; }

# Refuse to start a second instance
if [ -f "$PID_FILE" ]; then
  if kill -0 "$(cat "$PID_FILE")" 2>/dev/null; then
    log "another watcher (pid $(cat "$PID_FILE")) is already running, exiting"
    exit 0
  fi
fi
echo $$ > "$PID_FILE"
trap 'rm -f "$PID_FILE"; log "watcher exiting"' EXIT

log "watcher starting (pid $$)"

empty_polls=0

while :; do
  # Rotate log if > 1MB
  if [ -f "$LOG" ] && [ "$(stat -c %s "$LOG" 2>/dev/null || echo 0)" -gt 1048576 ]; then
    mv "$LOG" "${LOG}.1" 2>/dev/null
  fi

  # Iterate active slots
  mapfile -t active < <(python3 -c "
import json
try:
    with open('$SLOTS_FILE') as f: d = json.load(f)
    for n, v in (d.get('slots') or {}).items():
        if v and v.get('session'):
            print(f\"{n}\t{v['session']}\")
except Exception:
    pass
" 2>/dev/null)

  if [ "${#active[@]}" -eq 0 ]; then
    empty_polls=$((empty_polls + 1))
    log "no active slots (empty_polls=$empty_polls)"
    [ "$empty_polls" -ge 2 ] && { log "self-exit"; exit 0; }
    sleep "$POLL_SEC"
    continue
  fi
  empty_polls=0

  for entry in "${active[@]}"; do
    slot="${entry%%	*}"
    sess="${entry##*	}"

    # Skip if already paged this session
    [ -f "$CACHE/sessions/${slot}.limit-paged" ] && continue

    # Capture pane (last 2000 lines of scrollback)
    pane=$(tmux capture-pane -p -S -2000 -t "$sess" 2>/dev/null) || continue

    matched=0
    for pat in "${LIMIT_PATTERNS[@]}"; do
      if printf '%s' "$pane" | grep -Fq "$pat"; then
        matched=1
        log "MATCH slot=$slot pattern=$pat"
        break
      fi
    done

    [ "$matched" -eq 1 ] || continue

    # Gate
    if ! _bridge_should_page "$slot"; then
      log "slot=$slot matched but gate says present, skip"
      touch "$CACHE/sessions/${slot}.limit-paged"  # still mark to dedup if user steps away later
      continue
    fi

    # Build snippet via the same logic as notify.sh (inline since we can't easily import it)
    snippet=$(printf '%s' "$pane" | sed 's/\x1b\[[0-9;]*[mGKH]//g' | grep -v '^[[:space:]]*$' | tail -12 | tr -d '\r')
    snippet="${snippet:0:500}"
    repo=$(python3 -c "
import json
try:
    with open('$SLOTS_FILE') as f: d = json.load(f)
    v = (d.get('slots') or {}).get('$slot') or {}
    print(v.get('repo') or '?')
except Exception:
    print('?')
" 2>/dev/null)
    wt=$(python3 -c "
import json
try:
    with open('$SLOTS_FILE') as f: d = json.load(f)
    v = (d.get('slots') or {}).get('$slot') or {}
    print(v.get('worktree') or '')
except Exception:
    pass
" 2>/dev/null)
    bracket="s${slot}/${repo}"
    [ -n "$wt" ] && bracket="$bracket worktree:$wt"

    body="🛑 Usage limit reached [${bracket}]"
    [ -n "$snippet" ] && body="${body}

Last:
> ${snippet//$'\n'/$'\n'> }"

    _bridge_telegram_page "$slot" "$body"
    touch "$CACHE/sessions/${slot}.limit-paged"
    log "sent limit page slot=$slot"
  done

  sleep "$POLL_SEC"
done
```

Make executable:

```bash
chmod +x /home/freax/projects/repos/github/freaxnx01/public/config/shell/bridge-watcher.sh
```

- [ ] **Step 2: Add `_bridge_watcher_start` to `shell/bridge.sh`**

In `shell/bridge.sh`, immediately below `_bridge_install_hooks()`'s closing `}` (added in Task 5), insert:

```bash
# Start the usage-limit watcher daemon if not already running. Idempotent.
_bridge_watcher_start() {
  local pid_file="$_BRIDGE_CACHE/watcher.pid"
  if [ -f "$pid_file" ]; then
    if kill -0 "$(cat "$pid_file")" 2>/dev/null; then
      return 0  # already running
    fi
  fi
  local watcher="$_BRIDGE_DIR/bridge-watcher.sh"
  [ -x "$watcher" ] || chmod +x "$watcher" 2>/dev/null
  ( setsid "$watcher" </dev/null >/dev/null 2>&1 & ) 2>/dev/null
  return 0
}
```

- [ ] **Step 3: Verify the watcher starts and self-exits on empty slots**

The watcher polls every 30s and exits after 2 consecutive empty polls (~60s with grace).

```bash
source /home/freax/projects/repos/github/freaxnx01/public/config/shell/bridge.sh

# Ensure no slots are recorded
mkdir -p ~/.cache/bridge
echo '{"slots":{}}' > ~/.cache/bridge/slots.json

# Start watcher
_bridge_watcher_start
sleep 1
[ -f ~/.cache/bridge/watcher.pid ] && kill -0 "$(cat ~/.cache/bridge/watcher.pid)" 2>/dev/null \
  && echo OK1_RUNNING || echo FAIL1_RUNNING

# Wait for self-exit (~75s to be safe past two 30s polls + grace)
sleep 80
[ ! -f ~/.cache/bridge/watcher.pid ] && echo OK2_EXITED || {
  echo "FAIL2_EXITED — pid still present, log:"
  cat ~/.cache/bridge/watcher.log
}
```

Expected: `OK1_RUNNING`, then after the wait, `OK2_EXITED`.

- [ ] **Step 4: Verify the watcher fires on a fake match**

Set up a fake slot whose tmux session contains the limit phrase, then start the watcher and check the log.

```bash
# Create a fake tmux session that emits the limit phrase
tmux new-session -d -s bridge_test_limit
tmux send-keys -t bridge_test_limit 'echo "Claude usage limit reached at 12:34"; sleep 3600' Enter

# Register slot 99 as occupied with this session
python3 -c "
import json, os
try:
    with open('$HOME/.cache/bridge/slots.json') as f: d = json.load(f)
except: d = {'slots':{}}
d.setdefault('slots',{})['99'] = {'repo':'test-limit','worktree':None,'pid':$$,'session':'bridge_test_limit'}
with open('$HOME/.cache/bridge/slots.json','w') as f: json.dump(d, f, indent=2)
"

# Force away so the gate lets the page through (and ensures we don't page real-self)
echo away > ~/.cache/bridge/presence

# Start watcher
rm -f ~/.cache/bridge/sessions/99.limit-paged ~/.cache/bridge/watcher.pid
source /home/freax/projects/repos/github/freaxnx01/public/config/shell/bridge.sh
_bridge_watcher_start

# Wait for at least one poll
sleep 35

# Check log shows the match
grep -q "MATCH slot=99 pattern=Claude usage limit reached" ~/.cache/bridge/watcher.log \
  && echo OK_MATCH || { echo "FAIL_MATCH — log:"; cat ~/.cache/bridge/watcher.log; }

# Marker created
[ -f ~/.cache/bridge/sessions/99.limit-paged ] && echo OK_MARKER || echo FAIL_MARKER

# (If slot 99 has a real bot token in slot-tokens.json, a Telegram message
# will appear in that bot's chat. If not, the curl in _bridge_telegram_page
# silently no-ops.)

# Cleanup
[ -f ~/.cache/bridge/watcher.pid ] && kill "$(cat ~/.cache/bridge/watcher.pid)" 2>/dev/null
tmux kill-session -t bridge_test_limit 2>/dev/null
python3 -c "
import json
with open('$HOME/.cache/bridge/slots.json') as f: d = json.load(f)
d['slots'].pop('99', None)
with open('$HOME/.cache/bridge/slots.json','w') as f: json.dump(d, f, indent=2)
"
rm -f ~/.cache/bridge/presence ~/.cache/bridge/sessions/99.limit-paged ~/.cache/bridge/watcher.pid
```

Expected: `OK_MATCH`, `OK_MARKER`.

- [ ] **Step 5: Commit**

```bash
cd /home/freax/projects/repos/github/freaxnx01/public/config
git add shell/bridge.sh shell/bridge-watcher.sh
git commit -m "feat(bridge): add usage-limit watcher daemon"
```

---

## Task 9: Wire hook install + watcher start into `_bridge_slot_allocate`

**Files:**
- Modify: `shell/bridge.sh` — add two lines at the end of `_bridge_slot_allocate`.

- [ ] **Step 1: Add the integration**

In `shell/bridge.sh`, find the end of `_bridge_slot_allocate()`. The current closing region (around line 712-718) looks like:

```bash
  flock -u "$_lock_fd"

  if [ -z "$_SLOT_TOKEN" ]; then
    echo "bridge: WARNING — no bot token for slot $_SLOT. Telegram channel will not work." >&2
    echo "  Run setup-claude-channels.sh or add slot $_SLOT to slot-tokens.json." >&2
  fi
}
```

Replace this entire block with:

```bash
  flock -u "$_lock_fd"

  if [ -z "$_SLOT_TOKEN" ]; then
    echo "bridge: WARNING — no bot token for slot $_SLOT. Telegram channel will not work." >&2
    echo "  Run setup-claude-channels.sh or add slot $_SLOT to slot-tokens.json." >&2
  fi

  # Wire presence-aware Telegram pages: install per-slot hooks and start the watcher.
  _bridge_install_hooks "$_SLOT"
  _bridge_watcher_start
}
```

- [ ] **Step 2: Verify the integration runs without breaking slot allocation**

Run an end-to-end smoke test by allocating a slot manually (without launching Claude). The simplest check: ensure `_bridge_slot_allocate` still returns 0 and that the hooks are installed.

```bash
source /home/freax/projects/repos/github/freaxnx01/public/config/shell/bridge.sh

# Force slot 99 (won't conflict with real slots since max is 6)
mkdir -p ~/.cache/bridge
[ -f ~/.cache/bridge/slots.json ] || echo '{"slots":{}}' > ~/.cache/bridge/slots.json
rm -rf ~/.claude-s99

_bridge_slot_allocate 99
[ "$?" = "0" ] && echo OK1_ALLOC || echo FAIL1_ALLOC
[ -f ~/.claude-s99/settings.json ] && echo OK2_HOOKS || echo FAIL2_HOOKS

# Watcher started? (May have already self-exited if no other slots — that's fine,
# the goal is verifying the call path doesn't error.)
echo OK3_NO_ERROR_PATH

# Cleanup
rm -rf ~/.claude-s99
[ -f ~/.cache/bridge/watcher.pid ] && kill "$(cat ~/.cache/bridge/watcher.pid)" 2>/dev/null
rm -f ~/.cache/bridge/watcher.pid
```

Expected: `OK1_ALLOC`, `OK2_HOOKS`, `OK3_NO_ERROR_PATH`.

- [ ] **Step 3: Commit**

```bash
cd /home/freax/projects/repos/github/freaxnx01/public/config
git add shell/bridge.sh
git commit -m "feat(bridge): wire hook install + watcher start into slot allocation"
```

---

## Task 10: Wire marker cleanup into `_bridge_slot_free`

**Files:**
- Modify: `shell/bridge.sh` — add cleanup lines inside `_bridge_slot_free`.

- [ ] **Step 1: Add the cleanup**

In `shell/bridge.sh`, find `_bridge_slot_free()` (current line ~742). The current body looks like:

```bash
_bridge_slot_free() {
  local slot="$1"
  exec {_lock_fd}>"$_BRIDGE_SLOTS_LOCK"
  flock "$_lock_fd"
  python3 -c "
import json
f = '$_BRIDGE_SLOTS_FILE'
with open(f) as fh: d = json.load(fh)
d.setdefault('slots', {})['$slot'] = None
with open(f, 'w') as fh: json.dump(d, fh, indent=2)
" 2>/dev/null
  flock -u "$_lock_fd"
}
```

Replace with:

```bash
_bridge_slot_free() {
  local slot="$1"
  exec {_lock_fd}>"$_BRIDGE_SLOTS_LOCK"
  flock "$_lock_fd"
  python3 -c "
import json
f = '$_BRIDGE_SLOTS_FILE'
with open(f) as fh: d = json.load(fh)
d.setdefault('slots', {})['$slot'] = None
with open(f, 'w') as fh: json.dump(d, fh, indent=2)
" 2>/dev/null
  flock -u "$_lock_fd"

  # Clean up presence-page markers for this slot
  rm -f "$_BRIDGE_CACHE/sessions/${slot}.idle-since" \
        "$_BRIDGE_CACHE/sessions/${slot}.limit-paged" 2>/dev/null
}
```

- [ ] **Step 2: Verify cleanup**

```bash
source /home/freax/projects/repos/github/freaxnx01/public/config/shell/bridge.sh

# Pre-create both markers
mkdir -p ~/.cache/bridge/sessions
touch ~/.cache/bridge/sessions/99.idle-since
touch ~/.cache/bridge/sessions/99.limit-paged

# Pre-record slot 99 in slots.json so _bridge_slot_free has something to clear
[ -f ~/.cache/bridge/slots.json ] || echo '{"slots":{}}' > ~/.cache/bridge/slots.json
python3 -c "
import json
with open('$HOME/.cache/bridge/slots.json') as f: d = json.load(f)
d.setdefault('slots',{})['99'] = {'repo':'x','session':'x','pid':1,'started_at':0}
with open('$HOME/.cache/bridge/slots.json','w') as f: json.dump(d, f, indent=2)
"

_bridge_slot_free 99

[ ! -f ~/.cache/bridge/sessions/99.idle-since  ] && echo OK1_IDLE  || echo FAIL1_IDLE
[ ! -f ~/.cache/bridge/sessions/99.limit-paged ] && echo OK2_LIMIT || echo FAIL2_LIMIT
```

Expected: `OK1_IDLE`, `OK2_LIMIT`.

- [ ] **Step 3: Commit**

```bash
cd /home/freax/projects/repos/github/freaxnx01/public/config
git add shell/bridge.sh
git commit -m "feat(bridge): clear presence-page markers on slot free"
```

---

## Task 11: Polish — tab completion, help text, docs

**Files:**
- Modify: `shell/bridge.sh` — extend tab completion (line ~1352) and `--help` text (line ~1163).
- Modify: `shell/BRIDGE.md` — document the new commands and integration.

- [ ] **Step 1: Update tab completion**

In `shell/bridge.sh`, find the `_bridge()` function (line ~1352). Locate this block:

```bash
  # Built-in verb
  [[ "update" == *"$cur"* ]] && COMPREPLY+=("update")
```

Replace with:

```bash
  # Built-in verbs
  for verb in update away back here presence; do
    [[ "$verb" == *"$cur"* ]] && COMPREPLY+=("$verb")
  done
```

- [ ] **Step 2: Update help text**

In `shell/bridge.sh`, find the help heredoc starting at line ~1163. Locate the section:

```
Usage: bridge [options] [repo-name|.|update]
  (no args)             launch current repo if CWD is under $BRIDGE_BASE, else picker
  .                     launch current repo (errors if CWD is not inside a known repo)
  update                git pull the config repo hosting bridge.sh and re-source it
```

Update to:

```
Usage: bridge [options] [repo-name|.|update|away|back|here|presence]
  (no args)             launch current repo if CWD is under $BRIDGE_BASE, else picker
  .                     launch current repo (errors if CWD is not inside a known repo)
  update                git pull the config repo hosting bridge.sh and re-source it
  away                  set presence to "away" (Telegram pages enabled for all slots)
  back                  resume auto-detection (per-slot tmux client check)
  here                  set presence to "here" (Telegram pages disabled for all slots)
  presence              show current presence mode and per-slot effective state
```

- [ ] **Step 3: Update `shell/BRIDGE.md`**

In `shell/BRIDGE.md`, find the "CLI surface" section (around line 41). Add the new commands:

```
bridge away                     # presence: force "away" (enable Telegram pages for all slots)
bridge back                     # presence: resume auto-detection (per-slot tmux client check)
bridge here                     # presence: force "here" (suppress Telegram pages for all slots)
bridge presence                 # show current presence mode + per-slot effective state
```

Then, after the existing "Integration point for slot/telegram" section (around line 119), add a new section:

```markdown
## Presence-aware Telegram pages

bridge proactively pages each slot's Telegram bot when Claude is paused or
hits the 5h usage limit, but only when the user is **away** from the slot's
tmux session. See spec at `docs/superpowers/specs/2026-05-02-bridge-presence-aware-telegram-pages-design.md`.

### Presence model

| `~/.cache/bridge/presence` | Effective state |
|---|---|
| missing or `auto` | per-slot: present iff the slot's tmux session has ≥1 attached client |
| `away` | always away (forced — pages always sent) |
| `here` | always present (forced — pages suppressed) |

### Event sources

- **Notification hook** (per-slot `~/.claude-s<N>/settings.json`): `idle_prompt` (debounced 120s) and `elicitation_dialog` (immediate) trigger a page via `shell/bridge-hooks/notify.sh`. `UserPromptSubmit` fires `shell/bridge-hooks/clear-idle.sh` to cancel a pending idle page.
- **Watcher daemon** (`shell/bridge-watcher.sh`): polls every 30s for the usage-limit phrase in each active slot's tmux pane. Started by `_bridge_slot_allocate`, self-exits when no slots are occupied.

Both event sources gate through `_bridge_should_page` before sending. Pages go to the slot's existing per-slot bot (`@claude_freax_s<N>_bot`); replies route back via the existing `--channels plugin:telegram@...` mechanism.
```

- [ ] **Step 4: Verify tab completion and help**

```bash
# Re-source after edits
source /home/freax/projects/repos/github/freaxnx01/public/config/shell/bridge.sh

# Help text shows new commands
bridge --help 2>&1 | grep -q "  away   " && echo OK1_HELP || echo FAIL1_HELP

# Tab completion includes new verbs (manual: type `bridge aw<TAB>` and confirm "away")
# Or programmatically:
COMP_WORDS=(bridge away)
COMP_CWORD=1
COMPREPLY=()
_bridge
echo "${COMPREPLY[*]}" | grep -q "away" && echo OK2_COMPLETE || echo FAIL2_COMPLETE
```

Expected: `OK1_HELP`, `OK2_COMPLETE`.

- [ ] **Step 5: Final commit**

```bash
cd /home/freax/projects/repos/github/freaxnx01/public/config
git add shell/bridge.sh shell/BRIDGE.md
git commit -m "docs(bridge): document presence sub-commands and integration"
```

---

## Task 12: End-to-end smoke test

This task does not modify any files — it verifies the full path with a real Claude Code session. Do not skip; the integration touches enough moving pieces that one live exercise catches what unit tests miss.

- [ ] **Step 1: Re-source bridge and verify version**

```bash
source ~/.bashrc
bridge -V
```

Expected: `bridge 1.9.0`.

- [ ] **Step 2: Launch a real session, verify hooks are installed**

```bash
# Pick a small repo, launch through bridge
bridge config &
# Wait ~5s for slot allocation
sleep 5
# Inspect the active slot's settings.json
ls ~/.claude-s*/settings.json 2>/dev/null
# Confirm one of them has the new hook entries
for f in ~/.claude-s*/settings.json; do
  grep -l "bridge-hooks/notify.sh" "$f" 2>/dev/null && echo "OK_HOOKS_IN: $f"
done
```

Expected: at least one settings.json contains the hook command path.

- [ ] **Step 3: Verify the watcher is running**

```bash
[ -f ~/.cache/bridge/watcher.pid ] && kill -0 "$(cat ~/.cache/bridge/watcher.pid)" 2>/dev/null \
  && echo OK_WATCHER || echo FAIL_WATCHER
```

Expected: `OK_WATCHER`.

- [ ] **Step 4: Verify the away override end-to-end**

Detach from the launched session (Ctrl-b d in tmux, or Alt-F4 your terminal), wait ~3 minutes for Claude to be considered idle, and check Telegram. You should see an `idle_prompt` page from the slot's bot.

If you stay attached, no page should arrive. If you `bridge here`, no page should arrive even when detached.

If the page does NOT arrive when expected:
- Check `~/.cache/bridge/hooks.log` for the most recent `notification_type` line — confirm `idle_prompt` is firing.
- Check `_bridge_should_page <slot>` returns 0 (page) when manually invoked while detached.
- Check the `_bridge_telegram_page <slot> "test"` helper sends successfully (Task 4, Step 3).

- [ ] **Step 5: Push to remote**

Once the live test passes, push the branch:

```bash
cd /home/freax/projects/repos/github/freaxnx01/public/config
git push origin main
```

(The plan was committed task-by-task on `main` per existing project workflow. If you prefer a feature branch + PR, branch off before Task 1 and PR at this point instead.)

---

## Self-review notes

**Spec coverage check** (against `2026-05-02-bridge-presence-aware-telegram-pages-design.md`):

| Spec section | Plan task(s) |
|---|---|
| Presence model + CLI surface | Tasks 1, 3 |
| Generator A (Notification hook + UserPromptSubmit hook) | Tasks 5, 6, 7 |
| Generator B (usage-limit watcher) | Task 8 |
| Page format | Task 7 (`build_page_text`), Task 8 (inline snippet builder) |
| Gate (`_bridge_should_page`) | Task 2 |
| File layout (all paths) | Tasks 1, 5, 6, 7, 8 |
| Hook installation (idempotent merge) | Task 5 |
| Integration into existing flows | Tasks 9, 10 |
| Help text + tab completion | Task 11 |
| Coexistence with Remote Control | Documented in spec, no code needed |

**Open spec questions** (deliberately non-blocking — surfaced in code, not spec'd-around):

1. **Exact usage-limit string.** `LIMIT_PATTERNS` array in `bridge-watcher.sh` (Task 8) starts with two best-guess patterns and logs every poll. Tune by inspecting `~/.cache/bridge/watcher.log` after the first real limit hit.
2. **`idle_prompt` payload schema.** `notify.sh` (Task 7) logs the raw payload to `hooks.log` so the field name (`notification_type` vs `type`) and any duration field can be confirmed on first fire. The script tries both keys defensively.
