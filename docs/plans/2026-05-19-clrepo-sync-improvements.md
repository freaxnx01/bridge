# clrepo sync improvements — implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `clrepo` startup sync diagnosable (capture fetch stderr, longer timeout, inject a context note into the launched agent), and flip end-of-session autosync from opt-in to opt-out for feature branches.

**Architecture:** Three coordinated edits in the shell layer of `clrepo`. `_clrepo_sync` gains a timeout knob, stderr capture, and a structured "sync note" emitter. `_clrepo_launch` consumes the note: prints a banner, writes a marker file, and (for Claude) appends it via `--append-system-prompt`. `clrepo-autosync.sh` flips its default. All knobs are env vars; no new CLI flags.

**Tech Stack:** Bash, Claude Code CLI (`--append-system-prompt`), tmux, direnv.

**Spec:** `docs/specs/2026-05-19-clrepo-sync-improvements-design.md`

---

## Conventions and commit policy

The clrepo repo enforces (via `CLAUDE.md`) that **every commit changing `clrepo.sh` must bump `_CLREPO_VERSION` and add a matching `CHANGELOG.md` entry in the same commit**. Multiple tiny commits each bumping the version would be noisy, so this plan groups `clrepo.sh` edits into **one bundled commit** at the end (task 7), with version `1.30.0`. Files outside `clrepo.sh` (the autosync script, README, docs) get their own commits.

Tasks 2-6 below modify `clrepo.sh` and **stay uncommitted** until task 7. Task 1 modifies only `clrepo-autosync.sh` and commits independently. Task 8 modifies only `README.md` and commits independently.

This project has **no automated test framework**. Each task ships with concrete manual verification commands. Where a "test" step appears below, it means a manual smoke check, not a unit test.

## File map

- `clrepo-autosync.sh` — single-line default flip (task 1).
- `clrepo.sh` — fetch instrumentation, sync-note builder, banner helper, marker-file write, Claude system-prompt injection, version bump (tasks 2-7).
- `CHANGELOG.md` — `1.30.0` entry (task 7).
- `README.md` — new sections for autosync default + startup sync note (task 8).

Runtime artifacts (created at runtime, not source-controlled):

- `~/.cache/clrepo/sync.log` — appended on fetch failure, auto-rotated.
- `<repo>/.clrepo/sync-status.md` — rendered note, gitignored.
- `<repo>/.clrepo/.gitignore` — contains `*`, gitignored.

---

### Task 1: Flip autosync default from opt-in to opt-out

**Files:**
- Modify: `clrepo-autosync.sh:58`

- [ ] **Step 1: Read the current line to confirm position**

```bash
sed -n '58p' clrepo-autosync.sh
```

Expected output:
```
  [ "${CLREPO_AUTOSYNC:-0}" = 1 ] || return 0
```

- [ ] **Step 2: Change the default from 0 to 1**

Edit `clrepo-autosync.sh`, replace this exact line:

```bash
  [ "${CLREPO_AUTOSYNC:-0}" = 1 ] || return 0
```

with:

```bash
  [ "${CLREPO_AUTOSYNC:-1}" = 1 ] || return 0
```

No other lines change. `main`/`master` protection (the `case "$branch"` block immediately below) stays as-is — it already requires `CLREPO_AUTOSYNC_ALLOW_MAIN=1`.

- [ ] **Step 3: Lint with shellcheck**

```bash
shellcheck -x clrepo-autosync.sh
```

Expected: no new findings. (Pre-existing findings, if any, are out of scope.)

- [ ] **Step 4: Smoke-test the flip in a sandbox clone**

Don't pollute the real repo with a smoke test. Use a sandbox so commits/pushes here are throwaway:

```bash
SANDBOX=/tmp/clrepo-autosync-smoke-$$
git clone --quiet ~/projects/repos/github/freaxnx01/public/clrepo "$SANDBOX"
cd "$SANDBOX"
# The sandbox's "origin" points at the local repo, so push works.

# Test on a feature branch (default-on path)
git checkout -b feature-test -q
echo touched > .smoke-touch
CLREPO_AUTOSYNC_UNSET=1   # for clarity; we just don't export it
unset CLREPO_AUTOSYNC
bash -c '
  source '"$OLDPWD"'/clrepo-autosync.sh
  _clrepo_autosync "$PWD" "" 2>&1
'
git log -1 --oneline
```

Expected: stderr line `clrepo: autosync pushed 1 file(s) to feature-test in clrepo-autosync-smoke-...`; the last commit is `chore(autosync): wip from clrepo session (...)`. With the **old** default this would have silently no-op'd.

- [ ] **Step 5: Smoke-test main protection still works**

Still inside the sandbox:

```bash
git checkout main -q
echo touched > .smoke-main-touch
bash -c '
  source '"$OLDPWD"'/clrepo-autosync.sh
  _clrepo_autosync "$PWD" "" 2>&1
'
git log -1 --oneline   # should NOT be an autosync commit
```

Expected: stderr line `clrepo: autosync skip (...): branch 'main' is protected`; no new commit; `.smoke-main-touch` still untracked.

Cleanup the sandbox:

```bash
cd "$OLDPWD"
rm -rf "$SANDBOX"
```

- [ ] **Step 6: Commit**

```bash
git add clrepo-autosync.sh
direnv exec . git commit -m "$(cat <<'EOF'
feat(autosync): default to on; opt out with CLREPO_AUTOSYNC=0

main/master protection unchanged (still requires
CLREPO_AUTOSYNC_ALLOW_MAIN=1).
EOF
)"
```

(Per the project's git push memo: this repo uses direnv-scoped GH_TOKEN; the `direnv exec .` prefix loads the env for the commit hook chain. Push happens later or by the user.)

---

### Task 2: Add fetch timeout knob, stderr capture, and log rotation

**Files:**
- Modify: `clrepo.sh:1997-1998` (the `timeout 10 git fetch` block inside `_clrepo_sync`).

- [ ] **Step 1: Read the existing block to confirm position**

```bash
sed -n '1995,2003p' clrepo.sh
```

Expected output includes:
```
  timeout 10 git fetch --quiet 2>/dev/null || {
    _clrepo_warn "fetch failed or timed out, skipping sync"; return 0; }
```

- [ ] **Step 2: Replace the fetch block**

Edit `clrepo.sh`. Replace this exact pair of lines:

```bash
  timeout 10 git fetch --quiet 2>/dev/null || {
    _clrepo_warn "fetch failed or timed out, skipping sync"; return 0; }
```

with:

```bash
  local log="$_CLREPO_CACHE/sync.log"
  mkdir -p "$_CLREPO_CACHE"
  if [ -f "$log" ] && [ "$(wc -l < "$log")" -gt 400 ]; then
    tail -n 200 "$log" > "$log.tmp" && mv "$log.tmp" "$log"
  fi

  local fetch_err fetch_rc
  fetch_err=$(timeout "${CLREPO_SYNC_TIMEOUT:-20}" git fetch 2>&1)
  fetch_rc=$?
  if [ "$fetch_rc" -ne 0 ]; then
    printf '[%s] %s on %s (rc=%d): %s\n' \
      "$(date -Iseconds)" "$repo" "$branch" "$fetch_rc" \
      "$(printf '%s' "$fetch_err" | tr '\n' ' ' | head -c 500)" >> "$log"
    _clrepo_warn "fetch failed (rc=$fetch_rc), see $log"
    return 0
  fi
```

(`_clrepo_sync_set_note` is not called yet — that wiring lands in task 3.)

- [ ] **Step 3: Lint**

```bash
shellcheck -x clrepo.sh 2>&1 | head -40
```

Expected: no new findings on the edited block. Pre-existing findings (other parts of the file) are out of scope.

- [ ] **Step 4: Smoke-test the timeout path**

In this repo:

```bash
rm -f ~/.cache/clrepo/sync.log
cd ~/projects/repos/github/freaxnx01/public/clrepo

# Save the real origin URL first.
ORIG_URL=$(git remote get-url origin)

# Point origin at a non-routable address; force a 1s timeout; source and run.
git remote set-url origin https://10.255.255.1/nothing.git
bash -c '
  export CLREPO_SYNC_TIMEOUT=1
  source ./clrepo.sh
  _clrepo_sync clrepo "" 2>&1 || true
'

# Restore the real origin URL.
git remote set-url origin "$ORIG_URL"
git remote -v   # confirm the restore worked
```

Expected stderr line: `clrepo: fetch failed (rc=124), see ~/.cache/clrepo/sync.log` (or `rc=128` if git errors before the timeout — both prove the new code path is firing). Then:

```bash
cat ~/.cache/clrepo/sync.log
```

Expected: one line with timestamp, repo name, branch, rc, and the (truncated) git stderr.

- [ ] **Step 5: Verify the success path still works**

```bash
bash -c '
  source ./clrepo.sh
  _clrepo_sync clrepo "" 2>&1
'
```

Expected: silent (already up to date) OR a "pulled ..." line. **No** `fetch failed` warning.

- [ ] **Step 6: Stage but do NOT commit yet**

```bash
git add clrepo.sh
git status --short
```

Expected: `M  clrepo.sh` (staged but uncommitted — bundled with later tasks).

---

### Task 3: Add `_clrepo_sync_set_note` and wire it into every note-worthy skip path

**Files:**
- Modify: `clrepo.sh:1974` (insert helper above `_clrepo_sync`).
- Modify: `clrepo.sh:1977-2017` (call helper from skip paths in `_clrepo_sync`).

- [ ] **Step 1: Add the helper immediately above `_clrepo_sync`**

Find this line in `clrepo.sh`:

```bash
# Fast-forward sync of the current branch with its upstream before launch.
```

Insert immediately **before** it:

```bash
# Render the sync skip note into _CLREPO_SYNC_NOTE for downstream
# consumption by _clrepo_launch (banner + marker file + agent injection).
# Args: $1 = kind (fetch|no-upstream|dirty|diverged), $2.. = kind-specific.
# Side effect: sets the global var _CLREPO_SYNC_NOTE. Empty kind clears it.
_clrepo_sync_set_note() {
  local kind="${1:-}"
  local branch_v="${branch:-?}"
  local upstream_v="${upstream:-?}"
  local details="" suggested=""

  case "$kind" in
    fetch)
      local err="${2:-}" rc="${3:-?}"
      if [ "$rc" = "124" ]; then
        details="git fetch timed out after ${CLREPO_SYNC_TIMEOUT:-20}s"
      else
        details=$(printf '%s' "$err" | head -n 5)
      fi
      suggested='  - direnv exec . git fetch
  - if auth-related: verify GH_TOKEN/GITLAB_TOKEN/ADO PAT in .envrc
  - then: git pull --ff-only'
      _CLREPO_SYNC_NOTE="clrepo: startup sync was skipped — fetch failed.
Branch: $branch_v  Upstream: $upstream_v
$details
Suggested:
$suggested
Before making changes, please bring the branch in sync."
      ;;
    no-upstream)
      _CLREPO_SYNC_NOTE="clrepo: startup sync was skipped — no upstream.
Branch: $branch_v  Upstream: (none)
Branch $branch_v has no upstream configured.
Suggested:
  - when ready to share: git push -u origin $branch_v
Before making changes, please bring the branch in sync."
      ;;
    dirty)
      local porcelain
      porcelain=$(git status --porcelain 2>/dev/null | head -5)
      _CLREPO_SYNC_NOTE="clrepo: startup sync was skipped — dirty working tree.
Branch: $branch_v  Upstream: $upstream_v
Uncommitted changes (first 5):
$porcelain
Suggested:
  - git status
  - commit or stash before continuing
Before making changes, please bring the branch in sync."
      ;;
    diverged)
      local stats ahead behind
      stats=$(git rev-list --left-right --count HEAD...@{u} 2>/dev/null)
      ahead=$(printf '%s' "$stats" | awk '{print $1}')
      behind=$(printf '%s' "$stats" | awk '{print $2}')
      _CLREPO_SYNC_NOTE="clrepo: startup sync was skipped — diverged from upstream.
Branch: $branch_v  Upstream: $upstream_v
Local ahead by ${ahead:-?}, behind by ${behind:-?}.
Suggested:
  - git log --oneline @{u}..HEAD     # inspect local commits
  - git pull --rebase                # integrate (user judgment)
Before making changes, please bring the branch in sync."
      ;;
    "")
      _CLREPO_SYNC_NOTE=""
      ;;
    *)
      _CLREPO_SYNC_NOTE=""
      ;;
  esac
}
```

- [ ] **Step 2: Clear the note at the top of `_clrepo_sync`**

In `_clrepo_sync` (around line 1977), find:

```bash
_clrepo_sync() {
  local repo="$1" worktree="${2:-}"
  [ "${_CLREPO_NO_SYNC:-0}" = 1 ] && return 0
```

Replace with:

```bash
_clrepo_sync() {
  local repo="$1" worktree="${2:-}"
  _CLREPO_SYNC_NOTE=""
  [ "${_CLREPO_NO_SYNC:-0}" = 1 ] && return 0
```

- [ ] **Step 3: Wire the `no-upstream` skip**

Find this exact line:

```bash
  upstream=$(git rev-parse --abbrev-ref --symbolic-full-name '@{u}' 2>/dev/null) || {
    _clrepo_warn "no upstream for $branch, skipping sync"; return 0; }
```

Replace with:

```bash
  upstream=$(git rev-parse --abbrev-ref --symbolic-full-name '@{u}' 2>/dev/null) || {
    _clrepo_sync_set_note no-upstream
    _clrepo_warn "no upstream for $branch, skipping sync"; return 0; }
```

- [ ] **Step 4: Wire the `dirty` skip**

Find this exact block:

```bash
  if ! git diff --quiet || ! git diff --cached --quiet; then
    _clrepo_warn "dirty working tree, skipping sync"; return 0
  fi
```

Replace with:

```bash
  if ! git diff --quiet || ! git diff --cached --quiet; then
    _clrepo_sync_set_note dirty
    _clrepo_warn "dirty working tree, skipping sync"; return 0
  fi
```

- [ ] **Step 5: Wire the `fetch` skip**

In task 2's edit, the failure branch reads:

```bash
  if [ "$fetch_rc" -ne 0 ]; then
    printf '[%s] %s on %s (rc=%d): %s\n' \
      "$(date -Iseconds)" "$repo" "$branch" "$fetch_rc" \
      "$(printf '%s' "$fetch_err" | tr '\n' ' ' | head -c 500)" >> "$log"
    _clrepo_warn "fetch failed (rc=$fetch_rc), see $log"
    return 0
  fi
```

Insert `_clrepo_sync_set_note fetch "$fetch_err" "$fetch_rc"` immediately before `_clrepo_warn`:

```bash
  if [ "$fetch_rc" -ne 0 ]; then
    printf '[%s] %s on %s (rc=%d): %s\n' \
      "$(date -Iseconds)" "$repo" "$branch" "$fetch_rc" \
      "$(printf '%s' "$fetch_err" | tr '\n' ' ' | head -c 500)" >> "$log"
    _clrepo_sync_set_note fetch "$fetch_err" "$fetch_rc"
    _clrepo_warn "fetch failed (rc=$fetch_rc), see $log"
    return 0
  fi
```

- [ ] **Step 6: Wire the `diverged` skip**

Find this exact line:

```bash
    _clrepo_warn "$branch diverged from $upstream, skipping sync"
```

Replace with:

```bash
    _clrepo_sync_set_note diverged
    _clrepo_warn "$branch diverged from $upstream, skipping sync"
```

(Note: `detached HEAD`, `--no-sync`, tmux-reattach, and `ff-only merge failed unexpectedly` all stay note-free per the spec.)

- [ ] **Step 7: Lint**

```bash
shellcheck -x clrepo.sh 2>&1 | head -40
```

Expected: no new findings.

- [ ] **Step 8: Smoke-test each skip kind**

```bash
cd ~/projects/repos/github/freaxnx01/public/clrepo

# fetch (timeout)
bash -c '
  source ./clrepo.sh
  export CLREPO_SYNC_TIMEOUT=1
  git remote set-url origin https://10.255.255.1/nothing.git
  _clrepo_sync clrepo "" 2>/dev/null
  echo "---NOTE---"; echo "$_CLREPO_SYNC_NOTE"
  git remote set-url origin git@github.com:freaxnx01/clrepo.git
'
```

Expected: prints a multi-line note starting with `clrepo: startup sync was skipped — fetch failed.`

```bash
# no-upstream
bash -c '
  source ./clrepo.sh
  git checkout -b sync-note-test-$$ >/dev/null 2>&1
  _clrepo_sync clrepo "" 2>/dev/null
  echo "---NOTE---"; echo "$_CLREPO_SYNC_NOTE"
  git checkout - >/dev/null 2>&1
  git branch -D sync-note-test-$$ >/dev/null 2>&1
'
```

Expected: note starting with `clrepo: startup sync was skipped — no upstream.` containing `git push -u origin sync-note-test-...`.

```bash
# dirty
bash -c '
  source ./clrepo.sh
  echo wip > .wip-touch.tmp
  _clrepo_sync clrepo "" 2>/dev/null
  echo "---NOTE---"; echo "$_CLREPO_SYNC_NOTE"
  rm -f .wip-touch.tmp
'
```

Expected: note starting with `clrepo: startup sync was skipped — dirty working tree.` listing `.wip-touch.tmp`.

(Divergence is awkward to set up reliably from a one-liner; defer the diverged check to task 9's full smoke pass.)

- [ ] **Step 9: Stage but do NOT commit yet**

```bash
git add clrepo.sh
```

---

### Task 4: Add `_clrepo_sync_banner` and write `.clrepo/sync-status.md` in `_clrepo_launch`

**Files:**
- Modify: `clrepo.sh` (insert helper near other helpers; insert call after `_clrepo_sync` in `_clrepo_launch`).

- [ ] **Step 1: Add the banner helper near `_clrepo_warn`**

Find `_clrepo_warn`:

```bash
_clrepo_warn() {
  printf '\033[33mclrepo: %s\033[0m\n' "$*" >&2
}
```

Add immediately **after** it:

```bash
# Pretty-print a yellow bordered block summarising _CLREPO_SYNC_NOTE.
# Called right before agent launch when the note is non-empty.
_clrepo_sync_banner() {
  [ -z "${_CLREPO_SYNC_NOTE:-}" ] && return 0
  local reason_line suggested_line
  reason_line=$(printf '%s' "$_CLREPO_SYNC_NOTE" | sed -n '1p')
  suggested_line=$(printf '%s' "$_CLREPO_SYNC_NOTE" \
    | awk '/^Suggested:/{flag=1;next} flag&&NF{print; exit}')
  printf '\033[33m\n' >&2
  printf '┌─ clrepo: startup sync was skipped ─────────────────────────────\n' >&2
  printf '│ %s\n' "${reason_line#clrepo: startup sync was skipped — }" >&2
  [ -n "$suggested_line" ] && printf '│ Suggested:%s\n' "${suggested_line#  -}" >&2
  printf '│ Full note: .clrepo/sync-status.md\n' >&2
  printf '└────────────────────────────────────────────────────────────────\n' >&2
  printf '\033[0m\n' >&2
}

# Write _CLREPO_SYNC_NOTE to .clrepo/sync-status.md in the current repo.
# Creates .clrepo/.gitignore on first write so artifacts never get committed.
_clrepo_sync_write_marker() {
  [ -z "${_CLREPO_SYNC_NOTE:-}" ] && return 0
  mkdir -p .clrepo 2>/dev/null || return 0
  [ -f .clrepo/.gitignore ] || printf '*\n' > .clrepo/.gitignore
  {
    printf '<!-- written by clrepo at %s -->\n\n' "$(date -Iseconds)"
    printf '%s\n' "$_CLREPO_SYNC_NOTE"
  } > .clrepo/sync-status.md 2>/dev/null || true
}
```

- [ ] **Step 2: Call them from `_clrepo_launch`**

Find this line in `_clrepo_launch` (around line 2026):

```bash
  _clrepo_sync "$(basename "$sel")" "$worktree"
```

Replace with:

```bash
  _clrepo_sync "$(basename "$sel")" "$worktree"
  if [ -n "${_CLREPO_SYNC_NOTE:-}" ]; then
    _clrepo_sync_banner
    _clrepo_sync_write_marker
  fi
```

- [ ] **Step 3: Lint**

```bash
shellcheck -x clrepo.sh 2>&1 | head -40
```

Expected: no new findings.

- [ ] **Step 4: Smoke-test banner + marker file**

```bash
cd ~/projects/repos/github/freaxnx01/public/clrepo
rm -rf .clrepo

bash -c '
  source ./clrepo.sh
  echo wip > .wip-touch.tmp
  _clrepo_sync clrepo "" 2>/dev/null
  _clrepo_sync_banner
  _clrepo_sync_write_marker
  rm -f .wip-touch.tmp
'
ls -la .clrepo/
cat .clrepo/sync-status.md
git status --short .clrepo
```

Expected:
- Banner printed to stderr in yellow with the "dirty working tree" reason.
- `.clrepo/.gitignore` exists containing `*`.
- `.clrepo/sync-status.md` exists containing the rendered note.
- `git status .clrepo` shows nothing (gitignore working).

Cleanup:
```bash
rm -rf .clrepo
```

- [ ] **Step 5: Stage but do NOT commit yet**

```bash
git add clrepo.sh
```

---

### Task 5: Inject sync note into Claude via `--append-system-prompt`

**Files:**
- Modify: `clrepo.sh:2122-2124` (no-channel branch in `_clrepo_launch`).
- Modify: `clrepo.sh:2143-2145` (slot branch in `_clrepo_launch`).

- [ ] **Step 1: Modify the no-channel `claude_args` block**

Find this block (line 2122-2124, inside the `if [ "${_CLREPO_NO_CHANNEL:-0}" = 1 ]` branch):

```bash
    local -a claude_args=(-n "$display_name" --dangerously-skip-permissions)
    [ -n "$worktree" ] && claude_args+=(--worktree "$worktree")
    [ "$remote_control" = 1 ] && claude_args+=(--remote-control)
```

Replace with:

```bash
    local -a claude_args=(-n "$display_name" --dangerously-skip-permissions)
    [ -n "$worktree" ] && claude_args+=(--worktree "$worktree")
    [ "$remote_control" = 1 ] && claude_args+=(--remote-control)
    [ -n "${_CLREPO_SYNC_NOTE:-}" ] && claude_args+=(--append-system-prompt "$_CLREPO_SYNC_NOTE")
```

- [ ] **Step 2: Modify the slot `claude_args` block**

Find this block (around line 2143-2145, immediately after the `_clrepo_slot_allocate` call):

```bash
  local -a claude_args=(-n "$display_name" --dangerously-skip-permissions --channels plugin:telegram@claude-plugins-official)
  [ -n "$worktree" ] && claude_args+=(--worktree "$worktree")
  [ "$remote_control" = 1 ] && claude_args+=(--remote-control)
```

Replace with:

```bash
  local -a claude_args=(-n "$display_name" --dangerously-skip-permissions --channels plugin:telegram@claude-plugins-official)
  [ -n "$worktree" ] && claude_args+=(--worktree "$worktree")
  [ "$remote_control" = 1 ] && claude_args+=(--remote-control)
  [ -n "${_CLREPO_SYNC_NOTE:-}" ] && claude_args+=(--append-system-prompt "$_CLREPO_SYNC_NOTE")
```

- [ ] **Step 3: Verify `--append-system-prompt` is supported**

```bash
claude --help 2>&1 | grep append-system-prompt
```

Expected: a line documenting the flag. If absent, stop and reconsider the design.

- [ ] **Step 4: Lint**

```bash
shellcheck -x clrepo.sh 2>&1 | head -40
```

Expected: no new findings.

- [ ] **Step 5: Dry-run check that the args array is shaped correctly**

```bash
bash -c '
  _CLREPO_SYNC_NOTE="hello from a test"
  declare -a claude_args=(-n test --dangerously-skip-permissions)
  [ -n "${_CLREPO_SYNC_NOTE:-}" ] && claude_args+=(--append-system-prompt "$_CLREPO_SYNC_NOTE")
  printf "ARG: %s\n" "${claude_args[@]}"
'
```

Expected output:
```
ARG: -n
ARG: test
ARG: --dangerously-skip-permissions
ARG: --append-system-prompt
ARG: hello from a test
```

(The variable substitution is just for the dry-run; the real wiring lives in `_clrepo_launch`.)

- [ ] **Step 6: Stage but do NOT commit yet**

```bash
git add clrepo.sh
```

---

### Task 6: Best-effort sync-note injection note for non-Claude agents

**Files:**
- Modify: `clrepo.sh` (copilot and opencode launch blocks).

This task is intentionally minimal — opencode and copilot have no clean system-prompt injection flag, so the spec settles on banner + marker file (already covered by task 4). We just verify the order of operations: `_clrepo_sync_banner` and `_clrepo_sync_write_marker` happen **before** the copilot / opencode invocations, since the call site sits at the top of `_clrepo_launch` (in task 4). Nothing else needs to change.

- [ ] **Step 1: Re-verify the call site is above all branches**

```bash
grep -n "_clrepo_sync\b\|_clrepo_sync_banner\|claude\|copilot\|opencode\|code \." clrepo.sh \
  | grep -v "_clrepo_sync()" \
  | head -20
```

Expected: the `_clrepo_sync_banner` line (from task 4) appears **before** every `claude`, `copilot`, `opencode`, and `code .` invocation line in `_clrepo_launch`. If not, fix the placement.

- [ ] **Step 2: Smoke-test that the banner fires for the copilot path**

This is a "shape" test only — we don't actually launch copilot. We verify that the path through `_clrepo_launch` for `editor=copilot` would hit the banner. Since the banner/marker calls are right after `_clrepo_sync` and before the `if [ "$editor" = "copilot" ]` branch, no further wiring is needed.

(No code changes in this task — it's a verification task.)

---

### Task 7: Version bump, CHANGELOG entry, bundled clrepo.sh commit

**Files:**
- Modify: `clrepo.sh:25` (`_CLREPO_VERSION`).
- Modify: `CHANGELOG.md` (add `[1.30.0]` block at top).

- [ ] **Step 1: Bump `_CLREPO_VERSION`**

Find:

```bash
_CLREPO_VERSION="1.29.0"
```

Replace with:

```bash
_CLREPO_VERSION="1.30.0"
```

- [ ] **Step 2: Add a CHANGELOG entry**

Insert at the top of `CHANGELOG.md`, immediately below the introductory paragraph and **above** the existing `## [1.29.0]` block:

```markdown
## [1.30.0] - 2026-05-19

### Added

- `_clrepo_sync` now captures `git fetch` stderr to
  `~/.cache/clrepo/sync.log` (auto-rotated at 400 lines) whenever the
  fetch fails, so opaque "fetch failed" messages can finally be
  diagnosed (timeout vs. DNS vs. auth, etc.).
- `CLREPO_SYNC_TIMEOUT` env var (default `20`s, up from a hardcoded
  `10`s) tunes the fetch timeout for slow links.
- When startup sync skips for a non-trivial reason (fetch failure, no
  upstream, dirty tree, or divergence), clrepo now writes a structured
  note to `<repo>/.clrepo/sync-status.md` (auto-gitignored via
  `.clrepo/.gitignore`), prints a yellow banner to stderr, and — for
  Claude launches — passes the note via `claude --append-system-prompt`
  so the agent knows the branch state is off before the first prompt.

### Changed

- `CLREPO_AUTOSYNC` now defaults to **on** for feature branches. To opt
  out, set `export CLREPO_AUTOSYNC=0` in your shell env or the repo's
  `.envrc`. `main`/`master` protection is unchanged: pushes from those
  branches still require `CLREPO_AUTOSYNC_ALLOW_MAIN=1`.

### Fixed

- The "fetch failed or timed out" warning was discarding the actual
  error. The new log file + `rc=<N>` distinction in the stderr message
  surface timeouts (`rc=124`), DNS errors, auth errors, etc.
```

- [ ] **Step 3: Lint**

```bash
shellcheck -x clrepo.sh 2>&1 | head -40
```

Expected: no new findings.

- [ ] **Step 4: Confirm the staged diff is the whole story**

```bash
git diff --cached --stat clrepo.sh clrepo-autosync.sh CHANGELOG.md
```

Expected: `clrepo.sh` has the full set of edits from tasks 2-5 + 7 staged. `CHANGELOG.md` shows the new block.

Stage CHANGELOG.md and any remaining unstaged hunks:

```bash
git add clrepo.sh CHANGELOG.md
```

- [ ] **Step 5: Commit**

```bash
direnv exec . git commit -m "$(cat <<'EOF'
feat(clrepo): diagnosable startup sync + agent context note (bump to 1.30.0)

- _clrepo_sync captures git fetch stderr to ~/.cache/clrepo/sync.log
  (rotated at 400 lines) and distinguishes timeout (rc=124) from other
  fetch errors via rc in the stderr line.
- CLREPO_SYNC_TIMEOUT env var (default 20s) replaces the hardcoded 10s.
- On non-trivial sync skip (fetch fail / no upstream / dirty / diverged),
  _clrepo_sync sets _CLREPO_SYNC_NOTE; _clrepo_launch prints a yellow
  banner, writes <repo>/.clrepo/sync-status.md (gitignored), and for
  Claude launches passes the note via --append-system-prompt so the
  agent knows the branch isn't current before the first user prompt.
EOF
)"
```

- [ ] **Step 6: Verify commit**

```bash
git log -1 --stat
```

Expected: one commit, `clrepo.sh` and `CHANGELOG.md` modified, message as above.

---

### Task 8: README — document autosync default and startup sync note

**Files:**
- Modify: `README.md` (append two new sections + one row in Config variables).

- [ ] **Step 1: Add two new sections before `## Config variables`**

Insert the two sections below immediately **before** the existing `## Config variables` header — so behavior docs sit above the variable reference:

```markdown
## Startup sync and recovery

`clrepo <name>` runs a fast-forward sync on the current branch before launching the agent:

```
timeout ${CLREPO_SYNC_TIMEOUT:-20}s git fetch
# then: ff-only merge if local is strictly behind upstream
```

The sync is silently skipped (with a one-line yellow warning) when any of the following hold:

- `--no-sync` was passed.
- The session is a tmux reattach (the agent is already running).
- HEAD is detached.
- The branch has no upstream.
- The working tree is dirty.
- `git fetch` failed or timed out.
- The branch has diverged from its upstream.

For the non-trivial cases (`fetch` failed, `no upstream`, `dirty`, `diverged`) clrepo now:

- Writes the actual fetch stderr to `~/.cache/clrepo/sync.log` (auto-rotated at 400 lines).
- Renders a structured note explaining the skip and suggested next commands.
- Prints a yellow banner with the note's reason line right before the agent starts.
- Writes the full note to `<repo>/.clrepo/sync-status.md` (gitignored via `.clrepo/.gitignore`, written on first use).
- For Claude launches: passes the note via `claude --append-system-prompt`, so the agent knows the branch isn't current before the first user prompt.

`CLREPO_SYNC_TIMEOUT` (seconds, default `20`) controls the fetch timeout. Bump it on slow links; lower it if you'd rather fail fast.

## Session-exit autosync

When a clrepo session closes, `clrepo-autosync.sh` (sourced from `clrepo.sh` and re-invoked by the tmux `session-closed` hook) commits any uncommitted changes and pushes them to the upstream branch.

**Default: ON for feature branches.** To disable per-repo, add to the repo's `.envrc`:

```bash
export CLREPO_AUTOSYNC=0
```

`main` and `master` are protected. Autosync skips them with a warning unless you opt in explicitly:

```bash
export CLREPO_AUTOSYNC_ALLOW_MAIN=1
```

Caveats:

- Autosync runs `git add -A` — anything not in `.gitignore` will be committed and pushed. Mind your `.gitignore` for `.env`, build artifacts, etc.
- Push failures (no access, branch protection, etc.) emit a yellow warning and (if a slot token is set) a Telegram notification, but never block session exit.
- The auto-commit message is `chore(autosync): wip from clrepo session (<timestamp>)`. Squash or amend before opening a PR.
```

- [ ] **Step 2: Add three rows to the `Config variables` table**

Find the table:

```markdown
| Variable | Default | Purpose |
|---|---|---|
| `_CLREPO_BASE` | `/home/freax/projects/repos` | Root of the repo tree |
| `_CLREPO_CACHE` | `~/.cache/clrepo` | MRU, remote cache, (future: slots) |
| `_CLREPO_REMOTE_TTL` | `600` (10 min) | Remote listing cache lifetime in seconds |
```

Append:

```markdown
| `CLREPO_SYNC_TIMEOUT` | `20` | Seconds before `git fetch` is killed at startup |
| `CLREPO_AUTOSYNC` | `1` (on) | Commit-and-push uncommitted changes on session close; set to `0` per-repo to opt out |
| `CLREPO_AUTOSYNC_ALLOW_MAIN` | `0` (off) | Allow autosync to push from `main`/`master` (off by default for safety) |
```

- [ ] **Step 3: Verify the new sections exist and are in the right order**

```bash
grep -nE '^## (Startup sync and recovery|Session-exit autosync|Config variables|Known limitations)$' README.md
```

Expected: four lines, in this order — `Startup sync and recovery`, `Session-exit autosync`, `Config variables`, `Known limitations`. The two new H2s must appear **before** the existing `Config variables` section so they sit alongside other behavioral docs rather than below the variable reference.

- [ ] **Step 4: Commit**

```bash
git add README.md
direnv exec . git commit -m "docs: document startup sync recovery and default-on autosync"
```

- [ ] **Step 5: Verify**

```bash
git log --oneline -3
```

Expected:
```
<hash> docs: document startup sync recovery and default-on autosync
<hash> feat(clrepo): diagnosable startup sync + agent context note (bump to 1.30.0)
<hash> feat(autosync): default to on; opt out with CLREPO_AUTOSYNC=0
```

---

### Task 9: Full smoke-test pass against the spec's test matrix

**Files:** None. Verification only.

These are the tests from section "Testing" of `docs/specs/2026-05-19-clrepo-sync-improvements-design.md`. Run them sequentially. If anything diverges from the expected behavior, **stop** and either fix the implementation or update the spec (do not silently accept divergence).

- [ ] **Step 1: Up-to-date branch — silent**

```bash
cd ~/projects/repos/github/freaxnx01/public/clrepo
git checkout main
git pull --ff-only
bash -c 'source ./clrepo.sh; _clrepo_sync clrepo "" 2>&1; echo "NOTE=[$_CLREPO_SYNC_NOTE]"'
```

Expected: no warning lines; `NOTE=[]`.

- [ ] **Step 2: Forced fetch timeout**

```bash
git remote get-url origin > /tmp/clrepo-orig-url
git remote set-url origin https://10.255.255.1/nothing.git
bash -c '
  source ./clrepo.sh
  export CLREPO_SYNC_TIMEOUT=1
  _clrepo_sync clrepo "" 2>&1
  echo "---NOTE---"; echo "$_CLREPO_SYNC_NOTE"
'
cat ~/.cache/clrepo/sync.log | tail -3
git remote set-url origin "$(cat /tmp/clrepo-orig-url)"
```

Expected: stderr shows `fetch failed (rc=124), see ...`; note contains `git fetch timed out after 1s`; log line present.

- [ ] **Step 3: Real fetch error (DNS/auth)**

```bash
git remote set-url origin https://github.com/freaxnx01/this-repo-does-not-exist-xyz.git
bash -c '
  source ./clrepo.sh
  _clrepo_sync clrepo "" 2>&1
  echo "---NOTE---"; echo "$_CLREPO_SYNC_NOTE"
'
git remote set-url origin "$(cat /tmp/clrepo-orig-url)"
```

Expected: note contains first lines of the git error (e.g. `remote: Repository not found.` or `fatal: ...`).

- [ ] **Step 4: Behind upstream**

```bash
# Move HEAD back one commit, then run sync.
local_head=$(git rev-parse HEAD)
git reset --hard HEAD~1
bash -c 'source ./clrepo.sh; _clrepo_sync clrepo "" 2>&1; echo "NOTE=[$_CLREPO_SYNC_NOTE]"'
git reset --hard "$local_head"
```

Expected: `clrepo: pulled <sha>..<sha> on main`; `NOTE=[]`.

- [ ] **Step 5: Diverged (in a sandbox clone)**

Divergence is awkward to reproduce in the live repo without rewriting history. Use a sandbox clone instead:

```bash
SANDBOX=/tmp/clrepo-diverge-test-$$
git clone "$(git remote get-url origin)" "$SANDBOX"
cd "$SANDBOX"

# Local commit:
echo local > localfile && git add localfile && git commit -m "local commit" -q

# Upstream commit (rewrite origin to have a different new commit):
git checkout -b fake-upstream HEAD~1 -q
echo upstream > upstreamfile && git add upstreamfile && git commit -m "upstream commit" -q
git update-ref refs/remotes/origin/main fake-upstream
git checkout main -q
git branch -D fake-upstream -q

# Sanity-check: HEAD and origin/main now diverge.
git rev-list --left-right --count HEAD...@{u}
# Expect: 1<TAB>1 (local ahead 1, behind 1)

bash -c '
  source '"$OLDPWD"'/clrepo.sh
  _clrepo_sync clrepo-sandbox "" 2>&1
  echo "---NOTE---"; echo "$_CLREPO_SYNC_NOTE"
'

cd "$OLDPWD"
rm -rf "$SANDBOX"
```

Expected: warning `... diverged from origin/main`; note contains `Local ahead by 1, behind by 1.`

- [ ] **Step 6: Dirty tree**

```bash
echo wip > .wip-touch.tmp
bash -c 'source ./clrepo.sh; _clrepo_sync clrepo "" 2>&1; echo "---NOTE---"; echo "$_CLREPO_SYNC_NOTE"'
rm -f .wip-touch.tmp
```

Expected: warning `dirty working tree`; note lists `.wip-touch.tmp`.

- [ ] **Step 7: `--no-sync`**

```bash
bash -c '
  source ./clrepo.sh
  export _CLREPO_NO_SYNC=1
  _clrepo_sync clrepo "" 2>&1
  echo "NOTE=[$_CLREPO_SYNC_NOTE]"
'
```

Expected: no warning, no fetch attempt, `NOTE=[]`.

- [ ] **Step 8: Detached HEAD**

```bash
git checkout HEAD~1 -q
bash -c 'source ./clrepo.sh; _clrepo_sync clrepo "" 2>&1; echo "NOTE=[$_CLREPO_SYNC_NOTE]"'
git checkout - -q
```

Expected: warning `detached HEAD`; `NOTE=[]` (detached HEAD is note-free per spec).

- [ ] **Step 9: No upstream**

```bash
git checkout -b no-upstream-test-$$ -q
bash -c 'source ./clrepo.sh; _clrepo_sync clrepo "" 2>&1; echo "---NOTE---"; echo "$_CLREPO_SYNC_NOTE"'
git checkout - -q
git branch -D no-upstream-test-$$ -q
```

Expected: warning `no upstream for ...`; note contains `git push -u origin no-upstream-test-...`.

- [ ] **Step 10: Tmux reattach (skip if not in SSH context)**

```bash
[ -n "$SSH_CONNECTION" ] && echo "SSH session; reattach path will fire" || echo "skipping: not SSH"
```

If in SSH: launch `clrepo clrepo` once, detach, re-run — second run should be silent (no banner, no marker write, no fetch).

- [ ] **Steps 11–13: Autosync tests (use the sandbox pattern from task 1)**

Run all three in one sandbox to keep the real repo clean:

```bash
SANDBOX=/tmp/clrepo-autosync-final-$$
git clone --quiet ~/projects/repos/github/freaxnx01/public/clrepo "$SANDBOX"
cd "$SANDBOX"

# (11) Feature branch — autosync commits and pushes by default
git checkout -b auto-default -q
echo wip > .a.tmp
unset CLREPO_AUTOSYNC
bash -c 'source '"$OLDPWD"'/clrepo-autosync.sh; _clrepo_autosync "$PWD" "" 2>&1'
git log -1 --oneline   # expect: chore(autosync): wip from clrepo session ...

# (12) Main branch — autosync skips with warning
git checkout main -q
echo wip > .b.tmp
bash -c 'source '"$OLDPWD"'/clrepo-autosync.sh; _clrepo_autosync "$PWD" "" 2>&1'
git log -1 --oneline   # expect: NOT an autosync commit
git status --short .b.tmp   # expect: ?? .b.tmp

# (13) Feature branch with opt-out
git checkout -b auto-optout -q
echo wip > .c.tmp
CLREPO_AUTOSYNC=0 bash -c 'source '"$OLDPWD"'/clrepo-autosync.sh; _clrepo_autosync "$PWD" "" 2>&1'
git log -1 --oneline   # expect: NOT an autosync commit
git status --short .c.tmp   # expect: ?? .c.tmp

cd "$OLDPWD"
rm -rf "$SANDBOX"
```

Expected:
- (11) `chore(autosync): wip from clrepo session (...)` commit appears; stderr shows the pushed-N-files line.
- (12) Stderr shows `branch 'main' is protected`; no autosync commit; `.b.tmp` still untracked.
- (13) Stderr silent (opt-out returns 0); no autosync commit; `.c.tmp` still untracked.

- [ ] **Step 14: Marker file is gitignored**

After triggering any banner-emitting skip kind:

```bash
git status --short .clrepo
```

Expected: nothing — `.clrepo/` is ignored. If anything appears, the `.clrepo/.gitignore` write isn't firing.

- [ ] **Step 15: Lint pass**

```bash
shellcheck -x clrepo.sh clrepo-autosync.sh 2>&1 | head -40
```

Expected: no new findings versus pre-change baseline.

- [ ] **Step 16: End-to-end with Claude (manual)**

Run `clrepo clrepo` from a state with a non-trivial sync skip (easiest: leave an uncommitted file in the repo). Confirm:

- Yellow banner appears before Claude starts.
- `.clrepo/sync-status.md` is written.
- Inside Claude, ask "Why was startup sync skipped?". The agent should reference the note's reason.

If all 16 steps pass, the implementation is complete. Push the three commits when ready:

```bash
direnv exec . git push
```
