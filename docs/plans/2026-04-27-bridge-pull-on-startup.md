# bridge Pull/Sync on Startup — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fast-forward pull the active branch in `_bridge_launch` immediately after `cd`, with safe skip conditions and a `--no-sync` opt-out.

**Architecture:** Two new shell helpers (`_bridge_warn`, `_bridge_sync`) plus a small refactor that extracts the existing tmux session-name derivation into `_bridge_tmux_session_name` so the new sync step's reattach-skip check uses the exact same name `_bridge_launch` does. All changes live in one file.

**Tech Stack:** Bash, `git`, `coreutils` `timeout`. No new dependencies.

**Repo:** `freaxnx01/config` (the shell-config repo used across claude-dev LXC, WSL2 on Win11, and the company notebook).

**Spec:** `docs/superpowers/specs/2026-04-27-bridge-pull-on-startup-design.md`

**Testing note:** The repo has no test harness. Verification is by running commands and inspecting output. Each task ends with a concrete verification step. The final task runs `bash-language-server` over the modified file.

---

## File Structure

**Modified files:**
- `shell/bridge.sh` — all code changes here:
  - New helper `_bridge_warn` near the top.
  - New helper `_bridge_tmux_session_name` (extracted from `_bridge_launch` inline derivation).
  - Refactor `_bridge_launch` to use `_bridge_tmux_session_name`.
  - New function `_bridge_sync` placed immediately above `_bridge_launch`.
  - `_bridge_launch` body: call `_bridge_sync` right after `cd`.
  - `bridge()` arg parser: add `_BRIDGE_NO_SYNC=0` local; add `--no-sync` case branch; add help-text line.
  - `_bridge` completion: add `--no-sync` to flags list.

**New files:** None.

---

## Task 1: Add `_bridge_warn` helper

**Why:** A single yellow-prefixed stderr printer is reused by every skip path in `_bridge_sync`. Defining it once keeps the call sites readable and lets us tweak the format in one place.

**Files:**
- Modify: `shell/bridge.sh` — insert below the `_BRIDGE_OWNER` config block (after line 34, before `# Emit forge targets:` on line 36).

- [ ] **Step 1: Insert helper**

After the `_BRIDGE_OWNER="$_BRIDGE_CACHE/owner.json"` line and its blank line, add:

```bash
# Yellow-prefixed warning to stderr. Used by _bridge_sync skip paths.
_bridge_warn() {
  printf '\033[33mbridge: %s\033[0m\n' "$*" >&2
}
```

- [ ] **Step 2: Verify it works**

Source the file and call the helper:

```bash
( source shell/bridge.sh && _bridge_warn "test message" ) 2>&1
```

Expected: yellow-coloured `bridge: test message` printed to stderr.

- [ ] **Step 3: Commit**

```bash
git add shell/bridge.sh
git commit -m "bridge: add _bridge_warn helper for yellow stderr warnings"
```

---

## Task 2: Extract `_bridge_tmux_session_name` helper and refactor `_bridge_launch`

**Why:** The new `_bridge_sync` skip-on-reattach check must use the *same* tmux session name as `_bridge_launch`. Inline derivation in two places is a drift hazard; extract once now.

**Files:**
- Modify: `shell/bridge.sh` — add the helper just above `_bridge_launch` (around line 740). Update the two inline derivations inside `_bridge_launch` (currently around lines 759–760 and 779–781).

**Note:** The existing inline derivation appears in the `--no-channel` legacy branch (lines 759–760) and the slot-mode branch (lines 779–781). Both must be updated.

- [ ] **Step 1: Add the helper**

Immediately above the line `_bridge_launch() {`, insert:

```bash
# Derive a stable tmux session name from repo basename + optional worktree.
# Identical for a given (repo, worktree) pair so reattach checks match
# session creates.
_bridge_tmux_session_name() {
  local s="$1"
  [ -n "${2:-}" ] && s="$1-$2"
  printf '%s' "${s//[^A-Za-z0-9_-]/_}"
}
```

- [ ] **Step 2: Replace the legacy-branch inline derivation**

In the `--no-channel` legacy branch of `_bridge_launch`, find this block (currently lines 758–763):

```bash
    if [ -n "${SSH_CONNECTION:-}" ] && command -v tmux >/dev/null; then
      local session="$repo"
      [ -n "$worktree" ] && session="$repo-$worktree"
      session="${session//[^A-Za-z0-9_-]/_}"
      tmux new-session -A -s "$session" claude "${claude_args[@]}"
    else
```

Replace with:

```bash
    if [ -n "${SSH_CONNECTION:-}" ] && command -v tmux >/dev/null; then
      local session
      session=$(_bridge_tmux_session_name "$repo" "$worktree")
      tmux new-session -A -s "$session" claude "${claude_args[@]}"
    else
```

- [ ] **Step 3: Replace the slot-mode branch inline derivation**

In the slot-mode branch of `_bridge_launch`, find this block (currently lines 778–781):

```bash
  if [ -n "${SSH_CONNECTION:-}" ] && command -v tmux >/dev/null; then
    local session="$repo"
    [ -n "$worktree" ] && session="$repo-$worktree"
    session="${session//[^A-Za-z0-9_-]/_}"
```

Replace with:

```bash
  if [ -n "${SSH_CONNECTION:-}" ] && command -v tmux >/dev/null; then
    local session
    session=$(_bridge_tmux_session_name "$repo" "$worktree")
```

- [ ] **Step 4: Verify the helper produces the expected names**

```bash
( source shell/bridge.sh
  _bridge_tmux_session_name config; echo
  _bridge_tmux_session_name config feature/foo; echo
  _bridge_tmux_session_name "weird name!" ""; echo
)
```

Expected output (one per line):
```
config
config-feature_foo
weird_name_
```

- [ ] **Step 5: Verify `bridge` still loads cleanly**

```bash
bash -n shell/bridge.sh && echo OK
```

Expected: `OK`.

- [ ] **Step 6: Commit**

```bash
git add shell/bridge.sh
git commit -m "bridge: extract _bridge_tmux_session_name helper"
```

---

## Task 3: Add `_bridge_sync` function

**Why:** The core of the feature. Runs after `cd` to perform the safe fast-forward pull described in the spec.

**Files:**
- Modify: `shell/bridge.sh` — insert immediately above `_bridge_launch() {` (after the `_bridge_tmux_session_name` helper added in Task 2).

- [ ] **Step 1: Insert `_bridge_sync`**

Immediately above `_bridge_launch() {`, after the `_bridge_tmux_session_name` helper, insert:

```bash
# Fast-forward sync of the current branch with its upstream before launch.
# Args: $1 = repo basename, $2 = optional worktree name.
# Never fails the launch; every error path returns 0 after a stderr line.
_bridge_sync() {
  local repo="$1" worktree="${2:-}"
  [ "${_BRIDGE_NO_SYNC:-0}" = 1 ] && return 0

  # Skip if we're about to reattach an existing tmux session.
  if [ -n "${SSH_CONNECTION:-}" ] && command -v tmux >/dev/null; then
    local session
    session=$(_bridge_tmux_session_name "$repo" "$worktree")
    tmux has-session -t "$session" 2>/dev/null && return 0
  fi

  local branch upstream
  branch=$(git symbolic-ref --quiet --short HEAD) || {
    _bridge_warn "detached HEAD, skipping sync"; return 0; }
  upstream=$(git rev-parse --abbrev-ref --symbolic-full-name '@{u}' 2>/dev/null) || {
    _bridge_warn "no upstream for $branch, skipping sync"; return 0; }
  if ! git diff --quiet || ! git diff --cached --quiet; then
    _bridge_warn "dirty working tree, skipping sync"; return 0
  fi

  timeout 10 git fetch --quiet 2>/dev/null || {
    _bridge_warn "fetch failed or timed out, skipping sync"; return 0; }

  local local_sha upstream_sha base
  local_sha=$(git rev-parse HEAD)
  upstream_sha=$(git rev-parse '@{u}')
  [ "$local_sha" = "$upstream_sha" ] && return 0

  base=$(git merge-base HEAD '@{u}')
  if [ "$base" = "$upstream_sha" ]; then
    return 0  # local is ahead of upstream — fine, nothing to pull
  elif [ "$base" = "$local_sha" ]; then
    git merge --ff-only --quiet '@{u}' || {
      _bridge_warn "ff-only merge failed unexpectedly, skipping sync"; return 0; }
    printf 'bridge: pulled %s..%s on %s\n' \
      "$(git rev-parse --short "$local_sha")" \
      "$(git rev-parse --short "$upstream_sha")" "$branch" >&2
  else
    _bridge_warn "$branch diverged from $upstream, skipping sync"
  fi
}
```

- [ ] **Step 2: Syntax-check the file**

```bash
bash -n shell/bridge.sh && echo OK
```

Expected: `OK`.

- [ ] **Step 3: Smoke-test the function in this very repo (clean tree, up-to-date)**

```bash
( source shell/bridge.sh
  cd /home/freax/projects/repos/github/freaxnx01/public/config
  _bridge_sync config
)
```

Expected: silent (no output) if local matches origin/main. (If local is behind, you'll see a `bridge: pulled …` line; if dirty, a yellow warning. Any of these is acceptable evidence the function ran.)

- [ ] **Step 4: Smoke-test detached HEAD**

```bash
( source shell/bridge.sh
  cd /tmp
  rm -rf bridge-sync-test && git clone --quiet \
    /home/freax/projects/repos/github/freaxnx01/public/config bridge-sync-test
  cd bridge-sync-test
  git checkout --quiet HEAD~1 2>/dev/null || git checkout --quiet HEAD
  _bridge_sync bridge-sync-test
)
```

Expected: yellow `bridge: detached HEAD, skipping sync`.

- [ ] **Step 5: Smoke-test no-upstream**

```bash
( source shell/bridge.sh
  cd /tmp/bridge-sync-test
  git checkout --quiet -b throwaway
  _bridge_sync bridge-sync-test
)
```

Expected: yellow `bridge: no upstream for throwaway, skipping sync`.

- [ ] **Step 6: Smoke-test dirty tree**

```bash
( source shell/bridge.sh
  cd /tmp/bridge-sync-test
  git checkout --quiet main 2>/dev/null || git checkout --quiet master
  echo dirty >> README.md 2>/dev/null || echo dirty > dirty.txt
  git add -N dirty.txt 2>/dev/null || true
  echo dirty2 >> README.md
  _bridge_sync bridge-sync-test
)
```

Expected: yellow `bridge: dirty working tree, skipping sync`.

- [ ] **Step 7: Smoke-test --no-sync env override**

```bash
( source shell/bridge.sh
  cd /tmp/bridge-sync-test
  git checkout --quiet main 2>/dev/null || git checkout --quiet master
  git stash --quiet 2>/dev/null || true
  _BRIDGE_NO_SYNC=1 _bridge_sync bridge-sync-test
)
```

Expected: silent, no fetch.

- [ ] **Step 8: Cleanup**

```bash
rm -rf /tmp/bridge-sync-test
```

- [ ] **Step 9: Commit**

```bash
git add shell/bridge.sh
git commit -m "bridge: add _bridge_sync for safe ff-pull on launch"
```

---

## Task 4: Wire `--no-sync` flag in `bridge()`

**Why:** Surface the opt-out so users can skip sync on a per-invocation basis.

**Files:**
- Modify: `shell/bridge.sh` — `bridge()` arg-parser block (currently around lines 813–857).

- [ ] **Step 1: Add `_BRIDGE_NO_SYNC=0` to the locals line**

Find this line (currently line 814):

```bash
  local with_remote=0 force_refresh=0 mode_delete=0 worktree="" _BRIDGE_NO_CHANNEL=0 _BRIDGE_FORCED_SLOT=""
```

Replace with:

```bash
  local with_remote=0 force_refresh=0 mode_delete=0 worktree="" _BRIDGE_NO_CHANNEL=0 _BRIDGE_FORCED_SLOT="" _BRIDGE_NO_SYNC=0
```

- [ ] **Step 2: Add the case branch**

Find the `--no-channel` case branch (currently line 820):

```bash
      --no-channel)   _BRIDGE_NO_CHANNEL=1; shift ;;
```

Insert immediately after it:

```bash
      --no-sync)      _BRIDGE_NO_SYNC=1; shift ;;
```

- [ ] **Step 3: Add help-text line**

In the heredoc inside the `-h|--help` case branch, find the line:

```bash
  --no-channel            legacy mode, no slot allocation, no Telegram
```

Insert immediately after it:

```bash
  --no-sync               skip the upstream fast-forward pull on startup
```

- [ ] **Step 4: Verify help text**

```bash
( source shell/bridge.sh && bridge --help )
```

Expected: help output now contains the `--no-sync` line.

- [ ] **Step 5: Verify flag parses without error**

```bash
( source shell/bridge.sh && bridge --no-sync --help )
```

Expected: same help output, no error.

- [ ] **Step 6: Commit**

```bash
git add shell/bridge.sh
git commit -m "bridge: add --no-sync flag (parser + help)"
```

---

## Task 5: Call `_bridge_sync` from `_bridge_launch`

**Why:** Wire the new function into the launch path so it actually runs.

**Files:**
- Modify: `shell/bridge.sh` — `_bridge_launch` body (currently around line 744).

- [ ] **Step 1: Insert the call**

Find this block at the top of `_bridge_launch` (currently lines 740–747):

```bash
_bridge_launch() {
  local sel="$1"
  local worktree="${2:-}"
  local mru="$_BRIDGE_CACHE/mru"
  cd "$_BRIDGE_BASE/$sel" || return
  { printf '%s
' "$sel"; grep -vxF "$sel" "$mru" 2>/dev/null; } | head -10 > "$mru.tmp" && mv "$mru.tmp" "$mru"
```

Insert a `_bridge_sync` call between the `cd` and the MRU update:

```bash
_bridge_launch() {
  local sel="$1"
  local worktree="${2:-}"
  local mru="$_BRIDGE_CACHE/mru"
  cd "$_BRIDGE_BASE/$sel" || return
  _bridge_sync "$(basename "$sel")" "$worktree"
  { printf '%s
' "$sel"; grep -vxF "$sel" "$mru" 2>/dev/null; } | head -10 > "$mru.tmp" && mv "$mru.tmp" "$mru"
```

- [ ] **Step 2: Syntax-check**

```bash
bash -n shell/bridge.sh && echo OK
```

Expected: `OK`.

- [ ] **Step 3: End-to-end smoke (dry — don't actually launch claude)**

We can't easily run `_bridge_launch` end-to-end without launching Claude. Instead, source the file and invoke the function up to the point of `claude`, then Ctrl-C. A safer alternative: inspect the call ordering by running just `_bridge_sync` from the same cwd as a real invocation:

```bash
( source shell/bridge.sh
  cd /home/freax/projects/repos/github/freaxnx01/public/config
  _bridge_sync "$(basename "$PWD")"
  echo "[would now: MRU update, slot allocation, claude launch]"
)
```

Expected: silent (or yellow warning if the working tree is dirty), then the bracketed line.

- [ ] **Step 4: Commit**

```bash
git add shell/bridge.sh
git commit -m "bridge: call _bridge_sync from _bridge_launch after cd"
```

---

## Task 6: Update tab completion for `--no-sync`

**Why:** Discoverability. Users tab-complete flags rather than reading `--help`.

**Files:**
- Modify: `shell/bridge.sh` — `_bridge` completion function (currently around lines 976–993).

- [ ] **Step 1: Add `--no-sync` to the flags list**

Find this line (currently around line 980):

```bash
    local flags="-r --remote --refresh -D --delete -w --worktree -h --help"
```

Replace with:

```bash
    local flags="-r --remote --refresh -D --delete -w --worktree --no-sync --no-channel --slot --status --free -h --help"
```

(The original list omitted several flags that already exist in the parser. Adding them all here improves discoverability without changing parser behavior.)

- [ ] **Step 2: Verify completion**

```bash
( source shell/bridge.sh
  COMP_WORDS=(bridge --n)
  COMP_CWORD=1
  _bridge
  printf '%s\n' "${COMPREPLY[@]}"
)
```

Expected output includes `--no-sync`, `--no-channel`.

- [ ] **Step 3: Commit**

```bash
git add shell/bridge.sh
git commit -m "bridge: add --no-sync (and other existing flags) to tab completion"
```

---

## Task 7: Final verification — bash LSP and full smoke run

**Why:** Per the spec's testing section: lint with bash-language-server and run the canonical smoke scenarios in one pass.

**Files:** None modified. Verification only.

- [ ] **Step 1: Run bash-language-server / shellcheck over the file**

Try in this order, take whichever is available:

```bash
# Preferred: via bash-language-server CLI if installed.
command -v bash-language-server >/dev/null && bash-language-server analyze shell/bridge.sh

# Fallback: direct shellcheck (bash-language-server uses it internally).
command -v shellcheck >/dev/null && shellcheck -x shell/bridge.sh
```

Expected: no NEW errors or warnings introduced by Tasks 1–6 vs. the pre-feature `main`. Pre-existing diagnostics from earlier code are out of scope.

If new diagnostics appear, fix them in `shell/bridge.sh` and amend the appropriate task's commit (or add a follow-up commit).

- [ ] **Step 2: Up-to-date scenario**

```bash
( source shell/bridge.sh
  cd /home/freax/projects/repos/github/freaxnx01/public/config
  _bridge_sync "$(basename "$PWD")"
)
```

Expected: silent (assuming local matches origin/main).

- [ ] **Step 3: --no-sync silent scenario**

```bash
( source shell/bridge.sh
  cd /home/freax/projects/repos/github/freaxnx01/public/config
  _BRIDGE_NO_SYNC=1 _bridge_sync "$(basename "$PWD")"
)
```

Expected: silent.

- [ ] **Step 4: Behind scenario**

```bash
mkdir -p /tmp/bridge-final && cd /tmp/bridge-final
rm -rf upstream clone
git init --quiet --bare upstream
git clone --quiet upstream clone
cd clone
git config user.email t@t && git config user.name t
echo a > a && git add a && git commit --quiet -m a
git push --quiet origin master 2>/dev/null || git push --quiet origin main
echo b > b && git add b && git commit --quiet -m b
git push --quiet
git reset --quiet --hard HEAD~1
( source /home/freax/projects/repos/github/freaxnx01/public/config/shell/bridge.sh
  _bridge_sync clone )
cd /tmp && rm -rf bridge-final
```

Expected: `bridge: pulled <sha>..<sha> on <branch>` line on stderr.

- [ ] **Step 5: Diverged scenario**

```bash
mkdir -p /tmp/bridge-final && cd /tmp/bridge-final
rm -rf upstream clone
git init --quiet --bare upstream
git clone --quiet upstream clone
cd clone
git config user.email t@t && git config user.name t
echo a > a && git add a && git commit --quiet -m a
br=$(git symbolic-ref --short HEAD)
git push --quiet -u origin "$br"
# Make upstream advance (simulated via second clone).
cd .. && git clone --quiet upstream other && cd other
git config user.email t@t && git config user.name t
echo c > c && git add c && git commit --quiet -m c
git push --quiet
cd ../clone
# Local diverges
echo b > b && git add b && git commit --quiet -m b
( source /home/freax/projects/repos/github/freaxnx01/public/config/shell/bridge.sh
  _bridge_sync clone )
cd /tmp && rm -rf bridge-final
```

Expected: yellow `bridge: <branch> diverged from origin/<branch>, skipping sync`.

- [ ] **Step 6: Final commit (if any fixups landed)**

```bash
git status
# If there are uncommitted fixups from Step 1:
git add shell/bridge.sh
git commit -m "bridge: address LSP diagnostics from sync feature"
```

If `git status` shows clean, skip the commit.

---

## Self-Review Notes

Spec coverage:
- Sync semantics (Section "Sync semantics") → Task 3.
- Skip conditions table → Task 3 (every row covered: `--no-sync`, tmux reattach, detached HEAD, no upstream, dirty, fetch fail, diverged).
- New `--no-sync` flag → Tasks 4 + 6.
- Help text update → Task 4.
- Tab completion update → Task 6.
- Integration point (post-`cd`, pre-MRU) → Task 5.
- `_bridge_tmux_session_name` shared helper → Task 2.
- Error handling never fails launch → Task 3 function body (every error path returns 0).
- Manual smoke + LSP testing → Task 7.

No placeholders, all code blocks complete, all paths absolute.
