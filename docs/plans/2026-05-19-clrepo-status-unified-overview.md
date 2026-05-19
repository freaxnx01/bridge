# clrepo `--status` — unified session overview — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Widen `clrepo --status` to surface non-slot tmux sessions (`--no-channel`, `--code`, `--opencode`) alongside slot sessions, and merge Remote Control URLs into the same view via a footer. `--status-rc` becomes a deprecated alias.

**Architecture:** All edits land in `clrepo.sh`. (1) Extend `_clrepo_tmux_session_defaults` to tag each clrepo-created tmux session with `@clrepo-*` user-options at creation time (no new persistent state file). (2) Rewrite `_clrepo_slot_status` to enumerate the union of `slots.json` rows + tmux-tagged sessions, dedup by tmux session name, render a single table, and emit a URLs footer for rows with an active `bridgeSessionId`. (3) Collapse `_clrepo_slot_status_rc` to a thin deprecation wrapper. (4) Bump `_CLREPO_VERSION` (minor) and add a CHANGELOG entry per `CLAUDE.md` convention.

**Tech Stack:** Bash 5, `python3` (already used by `_clrepo_slot_status`), `tmux`. All present today.

**Spec:** `docs/specs/2026-05-18-clrepo-status-unified-overview-design.md` (issue [#1](https://github.com/freaxnx01/clrepo/issues/1))

---

## File Structure

- **Modify:** `clrepo.sh` (sole code file touched)
  - Line 25: bump `_CLREPO_VERSION` from `1.27.0` → `1.28.0` (new feature → minor).
  - Lines 1745–1749: extend `_clrepo_tmux_session_defaults` body + signature.
  - Lines 1854, 1885, 1907, 1950: update the four call sites to pass new args.
  - Lines 1271–1313: full rewrite of `_clrepo_slot_status`.
  - Lines 1320–1370: collapse `_clrepo_slot_status_rc` to alias.
  - Line 2151–2152: drop the dedicated `--status-rc` help line.
  - Line 2370: keep `--status-rc` in tab-completion `flags=`; no change required.
- **Modify:** `CHANGELOG.md` (add `[1.28.0] - 2026-05-19` entry).
- **No new files, no test files.** This repo has no shell-test harness — verification is the manual matrix in Task 7.

## Note on TDD in this plan

The repo has no shell-test harness (no `bats`, no `shunit2`), matching the precedent set by `docs/plans/2026-05-04-clrepo-session-picker.md`. Adding one for this change would dwarf the change itself — explicit YAGNI.

Verification is the 8-row manual matrix from the spec (Task 7 below), each scenario with a precise expected output. Behavior-preserving sub-steps (e.g. `bash -n` parse check) are inlined into the tasks that change code.

---

### Task 1: Extend `_clrepo_tmux_session_defaults` to tag sessions

**Files:**
- Modify: `clrepo.sh:1745-1749` (`_clrepo_tmux_session_defaults` body + signature)

Replace the existing 4-line function body so that, in addition to setting `mouse on` / `history-limit 50000`, it writes `@clrepo-*` user-options derived from new positional args.

- [ ] **Step 1: Replace the function**

Find the current definition at `clrepo.sh:1745`:

```bash
_clrepo_tmux_session_defaults() {
  local session="$1"
  tmux set-option -t "$session" mouse on >/dev/null 2>&1
  tmux set-option -t "$session" history-limit 50000 >/dev/null 2>&1
}
```

Replace with:

```bash
# Apply clrepo's tmux session defaults so wheel-scroll works and the
# scrollback is deep enough to review long agent output. Scoped to the
# session (not server-global) to avoid touching the user's other tmux
# sessions. Hold Shift while dragging to bypass tmux's mouse capture and
# fall back to the terminal emulator's native selection/clipboard.
#
# Also tags the session with @clrepo-* user-options so `clrepo --status`
# can enumerate non-slot tmux sessions (--no-channel, --code, --opencode)
# without a sidecar registry file. The tags are scoped per-session and
# never collide with non-clrepo tmux sessions.
#
# Args:
#   $1 session    tmux session name
#   $2 repo       repo basename
#   $3 worktree   worktree name or empty
#   $4 kind       one of: slot, no-channel, code, copilot, opencode
#   $5 slot       slot number for kind=slot; empty otherwise
_clrepo_tmux_session_defaults() {
  local session="$1" repo="${2:-}" worktree="${3:-}" kind="${4:-}" slot="${5:-}"
  tmux set-option -t "$session" mouse on >/dev/null 2>&1
  tmux set-option -t "$session" history-limit 50000 >/dev/null 2>&1
  # Tags for --status discovery. @clrepo-pid is read once from the pane
  # right after creation so synthetic (non-slot) rows can resolve RC.
  local pid
  pid=$(tmux display-message -t "$session" -p '#{pane_pid}' 2>/dev/null || echo "")
  tmux set-option -t "$session" '@clrepo-repo'     "$repo"     >/dev/null 2>&1
  tmux set-option -t "$session" '@clrepo-worktree' "$worktree" >/dev/null 2>&1
  tmux set-option -t "$session" '@clrepo-kind'     "$kind"     >/dev/null 2>&1
  tmux set-option -t "$session" '@clrepo-slot'     "$slot"     >/dev/null 2>&1
  tmux set-option -t "$session" '@clrepo-pid'      "$pid"      >/dev/null 2>&1
}
```

Notes for the implementer:

- Positional args 2–5 default to empty so any old call that still passes only `"$session"` won't break syntactically — the tags would just be blank, which the enumerator treats as "skip" (Task 3, dedup step).
- `tmux set-option ... '@clrepo-...'` is the standard mechanism for user-defined options in tmux ≥ 3.0. The repo already relies on `set-option` / `set-hook` elsewhere.
- The `>/dev/null 2>&1` redirects match the existing two option-writes and keep the launch path quiet.
- `pid` capture uses the same `tmux display-message ... '#{pane_pid}'` mechanism the slot path already uses at `clrepo.sh:1962`. Same value, same semantics.

- [ ] **Step 2: Parse check**

```bash
bash -n clrepo.sh && echo "syntax OK"
```

Expected: `syntax OK`.

- [ ] **Step 3: Do NOT commit yet** — call sites still pass only one arg. Move on to Task 2 so the next commit is self-contained.

---

### Task 2: Update the four `_clrepo_tmux_session_defaults` call sites

**Files:**
- Modify: `clrepo.sh:1854` (copilot path)
- Modify: `clrepo.sh:1885` (opencode path)
- Modify: `clrepo.sh:1907` (`--no-channel` path)
- Modify: `clrepo.sh:1950` (slot path)

Pass `repo`, `worktree`, `kind`, and (slot-only) `slot` to the helper. The values are already in scope at each call site.

- [ ] **Step 1: Update copilot call site**

Find at `clrepo.sh:1854`:

```bash
        _clrepo_tmux_session_defaults "$session"
```

Replace with:

```bash
        _clrepo_tmux_session_defaults "$session" "$repo" "$worktree" copilot ""
```

- [ ] **Step 2: Update opencode call site**

Find at `clrepo.sh:1885`:

```bash
        _clrepo_tmux_session_defaults "$session"
```

Replace with:

```bash
        _clrepo_tmux_session_defaults "$session" "$repo" "$worktree" opencode ""
```

- [ ] **Step 3: Update `--no-channel` call site**

Find at `clrepo.sh:1907`:

```bash
        _clrepo_tmux_session_defaults "$session"
```

Replace with:

```bash
        _clrepo_tmux_session_defaults "$session" "$repo" "$worktree" no-channel ""
```

- [ ] **Step 4: Update slot call site**

Find at `clrepo.sh:1950`:

```bash
    _clrepo_tmux_session_defaults "$session"
```

Replace with:

```bash
    _clrepo_tmux_session_defaults "$session" "$repo" "$worktree" slot "$_SLOT"
```

Notes:

- `$_SLOT` is the slot number set by `_clrepo_slot_allocate` earlier in the same code path (`clrepo.sh:1918`).
- There is no `code` kind in this repo — the editor variable uses `copilot` for the VS Code-style path. The kind list in the spec (`slot, no-channel, code, copilot, opencode`) included `code` defensively; the actual code path tagged here is `copilot`. The `--status` enumerator (Task 3) accepts any kind string, so this doesn't constrain the output.

- [ ] **Step 5: Parse check**

```bash
bash -n clrepo.sh && echo "syntax OK"
```

Expected: `syntax OK`.

- [ ] **Step 6: Smoke-test tagging on a real session**

Source the script in your interactive shell and start a `--no-channel` session you can kill immediately. Inspect the user-options.

```bash
. clrepo.sh
# Launch a test session in the background-friendliest way for this check:
# (you'll see the tmux session list and read its options)
SSH_CONNECTION=fake clrepo --no-channel clrepo &
sleep 2
tmux ls
tmux show-options -t clrepo -v '@clrepo-repo' 2>/dev/null
tmux show-options -t clrepo -v '@clrepo-kind' 2>/dev/null
tmux show-options -t clrepo -v '@clrepo-pid'  2>/dev/null
tmux kill-session -t clrepo
```

Expected:
- `tmux ls` shows a `clrepo` session.
- `@clrepo-repo` prints `clrepo`.
- `@clrepo-kind` prints `no-channel`.
- `@clrepo-pid` prints a non-empty integer pid.

If any of those print empty, recheck the call site edit (most likely a quoting / arg-count mistake).

- [ ] **Step 7: Stage the changes from Task 1 + Task 2 — do not commit yet**

```bash
git add clrepo.sh
git status
```

Wait until Task 3 lands so `--status` can also use the new tags. We'll commit Task 1+2+3 together as one unit ("tagging + enumeration") to keep the history bisectable.

---

### Task 3: Rewrite `_clrepo_slot_status` for the unified table + footer

**Files:**
- Modify: `clrepo.sh:1271-1313` (`_clrepo_slot_status` function)

Replace the whole function. The new body enumerates two sources, dedups, sorts, prints a unified table, then optionally prints a "Remote Control URLs:" footer.

- [ ] **Step 1: Snapshot current `--status` and `--status-rc` output for cross-check**

Before changing anything, capture the existing outputs so you can confirm Task 7's verification matches.

```bash
clrepo --status    > /tmp/clrepo-status-before.txt    2>&1
clrepo --status-rc > /tmp/clrepo-status-rc-before.txt 2>&1
echo "status exit: $?"
wc -l /tmp/clrepo-status-before.txt /tmp/clrepo-status-rc-before.txt
```

These are not byte-identical targets (the new format differs by design — new columns, dropped token column, merged footer). They're a sanity baseline for comparing the rows your current sessions occupy.

- [ ] **Step 2: Replace `_clrepo_slot_status`**

Find at `clrepo.sh:1271`:

```bash
# Print slot status table.
_clrepo_slot_status() {
  _clrepo_slots_init
  _clrepo_reconcile_slots

  python3 -c "
import json, time
with open('$_CLREPO_SLOTS_FILE') as f: d = json.load(f)
tokens = {}
try:
    with open('$_CLREPO_SLOT_TOKENS') as f: tokens = json.load(f)
except: pass
slots = d.get('slots', {})
MAX = $_CLREPO_MAX_SLOTS
# Slot 0 is the admin/bot 0 row — manually managed (BotFather + optional
# Claude session in ~/.claude-s0). Include it unconditionally so the user
# can see whether it's active. Slots 1..MAX are clrepo-allocated.
keys = set(slots.keys()) | set(tokens.keys()) | {str(n) for n in range(0, MAX + 1)}
# Drop non-numeric / out-of-range keys defensively, in case stale entries
# slipped past reconcile (live records aren't pruned there).
keys = {k for k in keys if k.isdigit() and 0 <= int(k) <= MAX}
now = int(time.time())
print(f\"{'SLOT':<5} {'REPO':<30} {'WORKTREE':<15} {'STARTED':<20} {'PID':<8} {'BOT'}\")
print('-' * 95)
for n in sorted(keys, key=int):
    v = slots.get(n)
    pb = tokens.get(n, '')
    # Slots 1..N follow @claude_freax_sN_bot convention; slot 0 is the
    # admin bot (BotFather-named, opaque here) so we just label it.
    bot = '(admin bot)' if int(n) == 0 else f'@claude_freax_s{n}_bot'
    has_token = '✓' if pb else '—'
    if v:
        repo = v.get('repo', '—')
        wt = v.get('worktree') or '—'
        pid = v.get('pid', '—')
        sa = v.get('started_at', 0)
        age = now - sa
        h, m = divmod(age // 60, 60)
        started = f'{h}h{m:02d}m ago' if sa else '—'
        print(f's{n:<4} {repo:<30} {wt:<15} {started:<20} {pid:<8} {bot} {has_token}')
    else:
        print(f's{n:<4} {\"—\":<30} {\"—\":<15} {\"—\":<20} {\"—\":<8} {bot} {has_token}')
" 2>/dev/null
}
```

Replace with:

```bash
# Print unified session-status table.
#
# Row sources:
#   1. slots.json — all slot rows 0..MAX (occupied or not).
#   2. tmux-tagged sessions — every `tmux list-sessions` entry with
#      @clrepo-repo set. Dedup: if its session name matches a slot row's
#      .session, the slot row wins (richer metadata).
#
# Output: one table + optional "Remote Control URLs:" footer for rows
# with an active bridgeSessionId. RC lookup mirrors the old --status-rc
# logic: slot rows read ~/.claude-s<N>/sessions/<pid>.json; synthetic
# no-channel rows read ~/.claude/sessions/<pid>.json. Other kinds get —.
_clrepo_slot_status() {
  _clrepo_slots_init
  _clrepo_reconcile_slots

  # Enumerate tmux-tagged sessions. Tab-separated for parse safety.
  # Format fields: name, created, repo, worktree, kind, slot, pid.
  local tmux_rows
  tmux_rows=$(tmux list-sessions -F \
    '#{session_name}	#{session_created}	#{@clrepo-repo}	#{@clrepo-worktree}	#{@clrepo-kind}	#{@clrepo-slot}	#{@clrepo-pid}' \
    2>/dev/null)

  python3 -c "
import json, os, time

slots_file = '$_CLREPO_SLOTS_FILE'
MAX = $_CLREPO_MAX_SLOTS
tmux_rows_raw = '''$tmux_rows'''

with open(slots_file) as f: d = json.load(f)
slots = d.get('slots', {})

now = int(time.time())

def bridge_for(cfg_dir, pid):
    if not pid: return ''
    sess_dir = os.path.join(os.path.expanduser(cfg_dir), 'sessions')
    if not os.path.isdir(sess_dir): return ''
    p = os.path.join(sess_dir, f'{pid}.json')
    if not os.path.isfile(p): return ''
    try:
        with open(p) as fh: sd = json.load(fh)
        return sd.get('bridgeSessionId') or ''
    except Exception:
        return ''

def fmt_age(sa):
    if not sa: return '—'
    age = now - int(sa)
    h, m = divmod(age // 60, 60)
    return f'{h}h{m:02d}m ago'

# --- Source 1: slot rows ---
rows = []      # list of dicts in display order
slot_sessions = set()  # tmux session names already covered by a slot row
slot_keys = {str(n) for n in range(0, MAX + 1)}
for n in sorted(slot_keys, key=int):
    v = slots.get(n)
    if v:
        sess = v.get('session') or ''
        if sess: slot_sessions.add(sess)
        repo = v.get('repo', '')
        wt = v.get('worktree') or ''
        repo_disp = f'{repo} [{wt}]' if wt else repo
        pid = v.get('pid', 0)
        bot = '(admin bot)' if int(n) == 0 else f'@claude_freax_s{n}_bot'
        cfg = f'~/.claude-s{n}'
        bridge = bridge_for(cfg, pid)
        rows.append({
            'slot':    f's{n}',
            'kind':    'slot',
            'repo':    repo_disp or '—',
            'started': fmt_age(v.get('started_at', 0)),
            'pid':     str(pid) if pid else '—',
            'tmux':    sess or '—',
            'bot':     bot,
            'bridge':  bridge,
            'label':   f's{n}',
        })
    else:
        bot = '(admin bot)' if int(n) == 0 else f'@claude_freax_s{n}_bot'
        rows.append({
            'slot': f's{n}', 'kind': 'slot', 'repo': '—',
            'started': '—', 'pid': '—', 'tmux': '—', 'bot': bot,
            'bridge': '', 'label': f's{n}',
        })

# --- Source 2: tmux-tagged rows (synthetic, non-slot) ---
synth = []
for line in tmux_rows_raw.strip().split('\n'):
    if not line: continue
    parts = line.split('\t')
    if len(parts) < 7: continue
    name, created, repo, wt, kind, slot, pid = parts[:7]
    if not repo: continue                  # untagged tmux session, skip
    if name in slot_sessions: continue      # dedup: slot row already has it
    repo_disp = f'{repo} [{wt}]' if wt else repo
    if kind == 'no-channel':
        bridge = bridge_for('~/.claude', pid)
    else:
        bridge = ''  # code/opencode have no Claude session file
    try: created_i = int(created)
    except ValueError: created_i = 0
    synth.append({
        'slot':    '—',
        'kind':    kind or '—',
        'repo':    repo_disp,
        'started': fmt_age(created_i),
        'pid':     pid or '—',
        'tmux':    name,
        'bot':     '—',
        'bridge':  bridge,
        'label':   repo_disp,
        'created': created_i,
    })

# Sort synthetic rows newest first, then append after slot rows.
synth.sort(key=lambda r: -r.get('created', 0))
rows.extend(synth)

# --- Render table ---
hdr = f\"{'SLOT':<5} {'KIND':<11} {'REPO':<28} {'STARTED':<13} {'PID':<8} {'TMUX':<20} {'BOT':<28} {'RC'}\"
print(hdr)
print('-' * len(hdr))
for r in rows:
    rc = '✓' if r['bridge'] else '—'
    print(f\"{r['slot']:<5} {r['kind']:<11} {r['repo']:<28} {r['started']:<13} {r['pid']:<8} {r['tmux']:<20} {r['bot']:<28} {rc}\")

# --- Render URL footer (only if at least one bridge is active) ---
rc_rows = [r for r in rows if r['bridge']]
if rc_rows:
    print()
    print('Remote Control URLs:')
    for r in rc_rows:
        url = f\"https://claude.ai/code/{r['bridge']}\"
        print(f\"  {r['label']:<12} {url}\")
" 2>/dev/null
}
```

Notes for the implementer:

- **`tmux_rows_raw = '''$tmux_rows'''`** — Bash interpolates `$tmux_rows` into the python source. The `'''` triple-quote tolerates the embedded newlines and tab-separated fields without further escaping. If `tmux_rows` happens to contain a literal `'''` (it won't — tmux session names don't permit triple-quotes, and clrepo's `_clrepo_tmux_session_name` filters to `[A-Za-z0-9_-]`), parsing would fail; this is an accepted limit.
- **Empty tmux output** — when no tmux server is running, `tmux list-sessions` exits non-zero and `tmux_rows` ends up empty; the synthetic loop short-circuits cleanly via `if not line: continue`.
- **RC for slot 0** — the existing `_clrepo_slot_status_rc` reads `~/.claude-s0/sessions/<pid>.json` for slot 0. We keep the same convention here.
- **Dropped token column** — intentional per spec non-goals; token availability is `--doctor`'s job.
- **Column widths** — chosen to fit a 95-col header (matches the previous `print('-' * 95)`). The `len(hdr)` divider auto-adjusts if widths are tweaked later.

- [ ] **Step 3: Parse check**

```bash
bash -n clrepo.sh && echo "syntax OK"
```

Expected: `syntax OK`.

- [ ] **Step 4: Re-source and run `--status`**

```bash
. clrepo.sh
clrepo --status
```

Expected: unified table with the new columns. Slots you had occupied before still appear with their info. If you have a `--no-channel` session tagged by Task 2's smoke test (already killed), it won't appear — pre-tagged sessions only.

- [ ] **Step 5: Leave changes uncommitted — commit lands in Task 6**

Per `CLAUDE.md`: any change to `clrepo.sh` must ride with a `_CLREPO_VERSION` bump *and* a matching `CHANGELOG.md` entry in the same commit. The single commit for Tasks 1–6 happens at the end of Task 6.

```bash
git status
```

Expected: `clrepo.sh` shown as modified, not yet staged. Move on to Tasks 4–6 without committing.

---

### Task 4: Collapse `_clrepo_slot_status_rc` to deprecation alias

**Files:**
- Modify: `clrepo.sh:1320-1370` (`_clrepo_slot_status_rc` function)

- [ ] **Step 1: Replace the function**

Find at `clrepo.sh:1320`:

```bash
# Print Remote Control status table. For each occupied slot, look up the
# Claude session record under $CLAUDE_CONFIG_DIR/sessions/<pid>.json and
# extract `bridgeSessionId` — the RC session id rendered as
# https://claude.ai/code/<bridgeSessionId>. Empty bridge id means RC is
# inactive for that session (e.g. launched with --no-rc).
_clrepo_slot_status_rc() {
  _clrepo_slots_init
  _clrepo_reconcile_slots

  python3 -c "
import json, os, time
with open('$_CLREPO_SLOTS_FILE') as f: d = json.load(f)
slots = d.get('slots', {})
MAX = $_CLREPO_MAX_SLOTS
keys = set(slots.keys()) | {str(n) for n in range(0, MAX + 1)}
keys = {k for k in keys if k.isdigit() and 0 <= int(k) <= MAX}
now = int(time.time())

def bridge_for(slot, pid):
    # CLAUDE_CONFIG_DIR for slot 0 is ~/.claude-s0 (admin) by convention.
    cfg = os.path.expanduser(f'~/.claude-s{slot}')
    sess_dir = os.path.join(cfg, 'sessions')
    if not pid or not os.path.isdir(sess_dir): return ''
    p = os.path.join(sess_dir, f'{pid}.json')
    if not os.path.isfile(p): return ''
    try:
        with open(p) as fh: sd = json.load(fh)
        return sd.get('bridgeSessionId') or ''
    except Exception:
        return ''

print(f\"{'SLOT':<5} {'REPO':<30} {'STARTED':<14} {'RC':<10} {'URL'}\")
print('-' * 110)
for n in sorted(keys, key=int):
    v = slots.get(n)
    if not v:
        print(f's{n:<4} {\"—\":<30} {\"—\":<14} {\"—\":<10} —')
        continue
    repo = v.get('repo', '—')
    wt   = v.get('worktree') or ''
    if wt: repo = f'{repo} [{wt}]'
    pid  = v.get('pid', 0)
    sa   = v.get('started_at', 0)
    age  = now - sa if sa else 0
    h, m = divmod(age // 60, 60)
    started = f'{h}h{m:02d}m ago' if sa else '—'
    bridge = bridge_for(n, pid)
    if bridge:
        url = f'https://claude.ai/code/{bridge}'
        rc = 'active'
    else:
        url = '—'
        rc = 'inactive'
    print(f's{n:<4} {repo:<30} {started:<14} {rc:<10} {url}')
" 2>/dev/null
}
```

Replace with:

```bash
# Deprecated. RC info is now merged into `clrepo --status`'s footer.
# Kept for one release as an alias so muscle memory / scripts don't break;
# scheduled for removal a minor release after 1.28.x.
_clrepo_slot_status_rc() {
  echo "clrepo: --status-rc is deprecated; use --status (RC URLs now shown in the footer)" >&2
  _clrepo_slot_status
}
```

- [ ] **Step 2: Parse check**

```bash
bash -n clrepo.sh && echo "syntax OK"
```

Expected: `syntax OK`.

- [ ] **Step 3: Smoke-test the alias**

```bash
. clrepo.sh
clrepo --status-rc 2>/tmp/clrepo-deprec.txt
cat /tmp/clrepo-deprec.txt
```

Expected:
- stdout: the same unified table as `clrepo --status`.
- `/tmp/clrepo-deprec.txt` contains: `clrepo: --status-rc is deprecated; use --status (RC URLs now shown in the footer)`.

```bash
rm /tmp/clrepo-deprec.txt
```

---

### Task 5: Update help text

**Files:**
- Modify: `clrepo.sh:2151-2152` (help heredoc — slot management group)

- [ ] **Step 1: Drop the dedicated `--status-rc` help line**

Find at `clrepo.sh:2151`:

```
  --status              show slot status table
  --status-rc           show Remote Control URL per occupied slot
```

Replace with:

```
  --status              show session status table (slot + non-slot tmux + RC URLs)
```

The old `--status-rc` line is removed. Tab-completion (`clrepo.sh:2370`) still lists `--status-rc`, which is the intentional behavior per the spec: typing `--status-rc<TAB>` still completes, then the alias prints the deprecation notice.

- [ ] **Step 2: Parse check**

```bash
bash -n clrepo.sh && echo "syntax OK"
```

Expected: `syntax OK`.

- [ ] **Step 3: Verify help text**

```bash
. clrepo.sh
clrepo --help 2>&1 | grep -E '\-\-status'
```

Expected: exactly one line:

```
  --status              show session status table (slot + non-slot tmux + RC URLs)
```

If two lines appear, the old `--status-rc` line wasn't fully removed — revisit Step 1.

---

### Task 6: Version bump + CHANGELOG entry

**Files:**
- Modify: `clrepo.sh:25` (`_CLREPO_VERSION`)
- Modify: `CHANGELOG.md` (prepend new release block)

Per `CLAUDE.md`: version bump and CHANGELOG entry must land in the same commit as the `clrepo.sh` changes from Tasks 1–5.

- [ ] **Step 1: Bump version**

Find at `clrepo.sh:25`:

```bash
_CLREPO_VERSION="1.27.0"
```

Replace with:

```bash
_CLREPO_VERSION="1.28.0"
```

Minor bump per semver: new user-visible feature, no breaking change for `--status` callers (output format changes but no scripts in this repo parse it), `--status-rc` still works.

- [ ] **Step 2: Prepend the 1.28.0 entry**

`CHANGELOG.md` uses the exact format `## [VERSION] - YYYY-MM-DD` with one blank line before each `### Section` heading and bullets prefixed with `- ` (see `## [1.27.0] - 2026-05-18` for the most recent reference). Insert this block **directly after the file header paragraph** (line 7) and before the existing `## [1.27.0] - 2026-05-18` block:

```markdown
## [1.28.0] - 2026-05-19

### Added

- `clrepo --status` now lists every clrepo-managed Claude session on the
  host: slot sessions, `--no-channel` tmux sessions, and `--code` /
  `--opencode` tmux sessions. Discovery uses `@clrepo-*` tmux
  user-options set at session creation; no new persistent state file.
- `clrepo --status` now merges Remote Control URLs into a footer block
  when at least one session has an active `bridgeSessionId`.

### Changed

- `clrepo --status` output format: new `KIND`, `TMUX`, and `RC` columns;
  the bot-token availability column moved out (it's surfaced by
  `clrepo --doctor`).

### Deprecated

- `clrepo --status-rc` — RC info is now part of `clrepo --status`. The
  flag still works and prints a deprecation notice; removal is planned
  for a follow-up minor release.
```

- [ ] **Step 3: Parse check (again, just in case)**

```bash
bash -n clrepo.sh && echo "syntax OK"
```

- [ ] **Step 4: Stage and commit Tasks 1–6 together**

```bash
git add clrepo.sh CHANGELOG.md
git diff --cached --stat
```

Expected: two files listed. `clrepo.sh` should show diffs near lines 25, 1271, 1320, 1745, 1854, 1885, 1907, 1950, and 2151.

```bash
git commit -m "$(cat <<'EOF'
feat(clrepo): unified --status overview surfaces tmux + RC sessions (bump to 1.28.0)

`clrepo --status` now lists every clrepo-managed Claude session on the
host — slot, --no-channel, --code, --opencode — and merges Remote
Control URLs into a footer. Discovery uses tmux user-options
(@clrepo-repo, @clrepo-kind, @clrepo-pid, etc.) set by
_clrepo_tmux_session_defaults at session creation; no new persistent
state. `--status-rc` becomes a deprecated alias.

Refs #1.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

If the pre-commit hook fails, fix the underlying issue and create a NEW commit (never `--amend` after a hook failure per repo policy).

- [ ] **Step 5: Confirm working tree clean**

```bash
git status
```

Expected: `nothing to commit, working tree clean`.

---

### Task 7: Manual verification matrix

Run each scenario interactively. Don't skip rows — every one of these is the safety net for "no test suite."

- [ ] **Scenario 1 — Coverage**

Goal: confirm the table surfaces slot sessions and `--no-channel` tmux sessions, and ignores hand-rolled tmux sessions running `claude`.

Setup:
1. Start a slot session: `clrepo <some-repo>` over SSH (so it lands in tmux). Detach.
2. Start a `--no-channel` session: `clrepo --no-channel <other-repo>` over SSH. Detach.
3. Start a hand-rolled tmux session that bypasses clrepo: `tmux new-session -d -s manual-test claude`.

Run:
```bash
clrepo --status
```

Expected:
- One row for the slot session (`KIND=slot`, has a slot id like `s1`).
- One row for the `--no-channel` session (`KIND=no-channel`, `SLOT=—`).
- The hand-rolled `manual-test` session is **not** in the table (no `@clrepo-*` tags).

Cleanup: `tmux kill-session -t manual-test`.

- [ ] **Scenario 2 — RC merge**

Goal: confirm the RC column and footer reflect actual bridge state.

Setup:
1. Pick one of the sessions from Scenario 1 that was launched with `--remote-control` (the default for slot sessions). Confirm the RC column shows `✓`.
2. Confirm the other session (e.g. the `--no-channel` one, if started without `--rc`) shows `—`.

Run:
```bash
clrepo --status | tail -20
```

Expected:
- RC column reads `✓` for the active row and `—` for the other.
- Footer "Remote Control URLs:" lists only the active session's URL, with the slot label or the repo label as appropriate.

- [ ] **Scenario 3 — Worktree rendering**

Goal: confirm the `[wt]` suffix renders correctly in both the table and the footer.

Setup: from the same repo as one of the previous sessions, exit the existing session, then start a worktree-bound session: `clrepo <repo> -w feat`. Detach.

Run:
```bash
clrepo --status
```

Expected:
- Row's `REPO` column shows `<repo> [feat]`.
- If RC is active for this row, footer label also reads `<repo> [feat]` (synthetic rows) or `s<N>` (slot rows).

- [ ] **Scenario 4 — Out-of-band cleanup**

Goal: confirm killing a tmux session externally doesn't leave orphan rows.

Setup: identify the tmux session name of one of the existing rows (the table's `TMUX` column).

Run:
```bash
tmux kill-session -t <session-name>
clrepo --status
```

Expected:
- The killed session's row no longer appears.
- For slot sessions: `_clrepo_reconcile_slots` flips that slot back to `—` (existing behavior — re-verifies after this change).
- For synthetic rows: the row is simply absent from `tmux list-sessions` so it drops out.

- [ ] **Scenario 5 — Empty state**

Goal: confirm graceful "nothing running" output.

Setup: kill or detach all clrepo sessions.

Run:
```bash
clrepo --status
echo "exit: $?"
```

Expected:
- Table shows all slots 0..MAX with `—` placeholders.
- No "Remote Control URLs:" footer.
- `exit: 0`.

- [ ] **Scenario 6 — `--status-rc` alias**

Goal: confirm the alias prints the deprecation notice and then the unified table.

Run:
```bash
clrepo --status-rc 2>/tmp/dep.txt
cat /tmp/dep.txt
rm /tmp/dep.txt
```

Expected:
- stdout: identical to `clrepo --status`.
- stderr: one line: `clrepo: --status-rc is deprecated; use --status (RC URLs now shown in the footer)`.

- [ ] **Scenario 7 — Foreground-mode slot**

Goal: confirm a slot session not running under tmux still shows up correctly.

Setup: from a non-SSH terminal (or with `SSH_CONNECTION` unset), run `clrepo <repo>`. This launches `claude` in the foreground; no tmux session is created. Suspend it (Ctrl-Z) or open a second shell.

Run (from another shell):
```bash
clrepo --status
```

Expected:
- The slot row appears with `TMUX=—`.
- If RC is active, the RC column shows `✓` and the footer carries the URL. RC lookup still works because slot rows resolve their config dir from the slot number, not from a tmux tag.

Cleanup: bring the suspended `claude` back with `fg` and exit it (`Ctrl-D` or `/exit`).

- [ ] **Scenario 8 — Old-version compatibility**

Goal: document — not enforce — that sessions launched by pre-1.28 clrepo are invisible to the new table.

Setup (synthetic): manually create a tmux session without setting `@clrepo-*` tags: `tmux new-session -d -s old-style claude`. (This simulates a session started by an older clrepo where the tagging code didn't exist yet.)

Run:
```bash
clrepo --status
```

Expected:
- `old-style` is **not** in the table.
- This matches the spec's documented non-goal: pre-tagged sessions are invisible. Users must kill and relaunch them to get them tracked.

Cleanup: `tmux kill-session -t old-style`.

---

### Task 8: Push (optional)

Only push when verification (Task 7) is fully green and the user has confirmed they want it on the remote.

- [ ] **Step 1: Confirm with user**

Ask: "Verification matrix green — push 1.28.0 to origin/main?"

If yes:

```bash
direnv exec . git push
```

(Per memory: this repo uses direnv-scoped `GH_TOKEN`; the Bash tool is non-interactive so `direnv` doesn't auto-load.)

Expected: clean push, no force, no errors. The pre-push hook (if any) succeeds.

---

## Self-Review Notes (for the engineer running this)

If you find a step that references a line number that no longer matches because an earlier task already shifted things, trust the *named* anchor (function name, comment, distinctive string) over the line number. The plan was written against `clrepo.sh` at commit `582e15f` (post-spec, pre-implementation).
