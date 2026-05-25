# bridge sync improvements — design

**Date:** 2026-05-19
**Component:** `bridge.sh`, `bridge-autosync.sh`

## Problem

Two recurring pain points around git syncing in bridge sessions:

1. **Startup sync fails opaquely.** `_bridge_sync` runs `timeout 10 git fetch --quiet 2>/dev/null` and on any non-zero exit prints `bridge: fetch failed or timed out, skipping sync`. Both stderr and the distinction between "timeout" and "real fetch error" are discarded. Users see the warning often, but have no way to know whether it's a slow link, a DNS issue, an auth problem, a single slow remote, or something else — so they can't fix it. When the sync silently skips, the agent then begins work on a possibly stale tree, sometimes producing duplicate or conflicting work against upstream.
2. **Local changes can be left behind on session exit.** `bridge-autosync.sh` already commits-and-pushes everything on tmux `session-closed`, but it's opt-in via `BRIDGE_AUTOSYNC=1` in the repo's `.envrc`. Users routinely close sessions and only later notice unpushed commits or uncommitted changes that never made it to the remote.

## Goals

- Make startup sync **diagnosable** when it fails (capture the actual git stderr) and slightly more **tolerant** of slow networks (configurable timeout, default 20s).
- When startup sync skips for a non-trivial reason, **inform the launched agent** so it can help the user resolve the issue rather than blindly editing a stale tree.
- Make end-of-session **commit-and-push the default** for feature branches, while preserving the existing safety gate on `main`/`master`.

## Non-goals

- No automatic stash, rebase, or conflict resolution in bridge itself. Divergence handling stays with the user (or the agent the user prompts).
- No background fetch.
- No size guard, secret-scanning, or path filter on what autosync commits — relying on user `.gitignore` discipline as today.
- No new global config file. All knobs are env vars, set in `.envrc` or shell env.
- No change to the remote-listing cache (`remote.list`, `repo-meta.json`).
- No change to which agents bridge can launch.

## Design overview

Three coordinated changes, all in the bridge shell layer:

1. **`_bridge_sync` becomes more diagnosable** — captures git stderr to a log, takes a configurable timeout, and emits a structured "sync note" describing why the sync skipped.
2. **`_bridge_launch` consumes the sync note** and injects it into the launched agent's context, best-effort per agent kind.
3. **`bridge-autosync.sh` flips its default** from opt-in to opt-out. `main`/`master` continues to require `BRIDGE_AUTOSYNC_ALLOW_MAIN=1`.

The sync note travels in-process via a shell variable (`_BRIDGE_SYNC_NOTE`). Fetch stderr lands in `~/.cache/bridge/sync.log`.

## Part 1 — Startup sync robustness

### Knob

New env var `BRIDGE_SYNC_TIMEOUT` (seconds, default `20`, up from a hardcoded `10`). Read once at the top of `_bridge_sync`. No new CLI flag.

### Fetch with stderr capture

In `_bridge_sync`, replace the existing fetch line:

```bash
timeout 10 git fetch --quiet 2>/dev/null || {
  _bridge_warn "fetch failed or timed out, skipping sync"; return 0; }
```

with:

```bash
local log="$_BRIDGE_CACHE/sync.log"
mkdir -p "$_BRIDGE_CACHE"

local fetch_err fetch_rc
fetch_err=$(timeout "${BRIDGE_SYNC_TIMEOUT:-20}" git fetch 2>&1)
fetch_rc=$?
if [ "$fetch_rc" -ne 0 ]; then
  printf '[%s] %s on %s (rc=%d): %s\n' \
    "$(date -Iseconds)" "$repo" "$branch" "$fetch_rc" \
    "$(printf '%s' "$fetch_err" | tr '\n' ' ' | head -c 500)" >> "$log"
  _bridge_sync_set_note fetch "$fetch_err" "$fetch_rc"
  _bridge_warn "fetch failed (rc=$fetch_rc), see $log"
  return 0
fi
```

Notes:
- `rc=124` from `timeout(1)` distinguishes timeout from other fetch failures; the kind is rendered in the sync note.
- Stderr is normalised to one line and capped at 500 chars in the log to keep entries grep-friendly.

### Log rotation

Lightweight: once per `_bridge_sync` invocation, if the log exceeds 400 lines, truncate to the last 200 lines:

```bash
if [ -f "$log" ] && [ "$(wc -l < "$log")" -gt 400 ]; then
  tail -n 200 "$log" > "$log.tmp" && mv "$log.tmp" "$log"
fi
```

No logrotate dependency.

### Skip-path notes

Every non-trivial skip path in `_bridge_sync` calls `_bridge_sync_set_note <kind> [details...]` immediately before its `return 0`. The following table is authoritative for which kinds emit a note:

| Skip kind                | Note? | Rationale                                                                       |
|--------------------------|-------|---------------------------------------------------------------------------------|
| `--no-sync` opt-out      | no    | explicit user choice                                                            |
| tmux reattach            | no    | re-entry to an existing session                                                 |
| `detached HEAD`          | no    | user is clearly on purpose                                                      |
| `no upstream` for branch | yes   | brand-new branch — hint `git push -u origin <branch>` when ready                |
| `dirty` working tree     | yes   | uncommitted work from prior session — agent should know before editing          |
| `fetch` failed / timeout | yes   | include the actual stderr (truncated) and suggest `direnv exec . git fetch`     |
| `diverged` from upstream | yes   | include divergence stats (ahead/behind counts); the most important note to surface |

## Part 2 — Sync note format and agent injection

### `_bridge_sync_set_note <kind> [details...]`

New helper in `bridge.sh`. Sets `_BRIDGE_SYNC_NOTE` (multi-line shell string) based on `<kind>`. Each rendered note follows the same skeleton:

```
bridge: startup sync was skipped — <reason>.
Branch: <branch>  Upstream: <upstream>
<kind-specific details>
Suggested:
  - <cmd 1>
  - <cmd 2>
Before making changes, please bring the branch in sync.
```

Per-kind details and suggestions:

- **fetch (timeout)** — `rc=124`. Details: `git fetch timed out after ${BRIDGE_SYNC_TIMEOUT}s`. Suggested: `direnv exec . git fetch` (per the project's direnv-scoped token convention), then `git pull --ff-only`.
- **fetch (other failure)** — Details: first 5 lines of `fetch_err` (the captured stderr). Suggested: same as above plus "if auth-related, verify GH_TOKEN/GITLAB_TOKEN/ADO PAT in .envrc".
- **no upstream** — Details: `branch <branch> has no upstream`. Suggested: `git push -u origin <branch>` when ready to share.
- **dirty** — Details: name-status digest from `git status --porcelain | head -5`. Suggested: `git status`, then commit or stash before continuing.
- **diverged** — Details: `local ahead by N, behind by M` computed via `git rev-list --left-right --count HEAD...@{u}`. Suggested: `git log --oneline @{u}..HEAD` to inspect local work, `git pull --rebase` to integrate (user judgment).

### Injection in `_bridge_launch`

After `_bridge_sync "$(basename "$sel")" "$worktree"` returns, the launcher reads `_BRIDGE_SYNC_NOTE`. If empty, do nothing extra and launch as before. If non-empty, take all three actions below:

- Call `_bridge_sync_banner` to print a yellow bordered block to stderr immediately before agent launch (so the user sees the reason even in non-Claude flows).
- Write `.bridge/sync-status.md` in the repo root with the rendered note + an ISO timestamp. Create `.bridge/.gitignore` containing `*` on first write so the marker (and any future bridge-local artifacts) never get committed. The status file is overwritten on each launch.
- Inject into the agent **per agent kind:**
  - **Claude** (the `claude "${claude_args[@]}"` paths): add `claude_args+=(--append-system-prompt "$_BRIDGE_SYNC_NOTE")`. This is the primary injection path.
  - **Opencode / Copilot / `--no-channel` / direct shell**: banner + marker file only. No CLI flag is known to inject system-prompt text cleanly for these agents; the marker file gives them something to read on demand.
  - **VS Code (`-c` / `code`)**: banner + marker file. No agent process to inject into; the marker is visible in the file tree if needed.

### `_bridge_sync_banner`

Helper that prints something like:

```
┌─ bridge: startup sync was skipped ─────────────────────────────
│ Reason: diverged from origin/main (ahead 2, behind 3)
│ Suggested: git log --oneline @{u}..HEAD ; git pull --rebase
│ Full note: .bridge/sync-status.md
└────────────────────────────────────────────────────────────────
```

ANSI yellow, stderr. Width-bounded; no fancy wrapping.

## Part 3 — Default-on autosync at exit

### Behavior change

In `bridge-autosync.sh`, flip:

```bash
[ "${BRIDGE_AUTOSYNC:-0}" = 1 ] || return 0
```

to:

```bash
[ "${BRIDGE_AUTOSYNC:-1}" = 1 ] || return 0
```

That is the only line of code that changes in this file. Everything else (the `git add -A`, the commit, the push, the protected-branch gate, the Telegram notification) stays as-is.

### Effect

- Feature branches: commit-and-push on session-closed by default. Users who don't want it set `export BRIDGE_AUTOSYNC=0` in their shell env or the repo's `.envrc`.
- `main` / `master`: unchanged — still skipped with a warning unless `BRIDGE_AUTOSYNC_ALLOW_MAIN=1`. So "default-on" really means "default-on for feature branches". This is intentional: the existing `_bridge_warn_unpushed` already flags unpushed `main` commits on every pane-died.

### Documentation impact

- `CHANGELOG.md`: one **Changed** entry under the new version describing the flip and the opt-out var. Loud enough that users notice.
- `README.md`: rewrite the autosync section. New summary: "Autosync is on by default for feature branches. To opt out per-repo: `export BRIDGE_AUTOSYNC=0` in `.envrc`. `main`/`master` remain protected behind `BRIDGE_AUTOSYNC_ALLOW_MAIN=1`."
- `bridge --help`: existing autosync mention (if any) updated; otherwise no help-text change is required.

### Known caveats (carried over from today, more visible now)

- `git add -A` stages everything not in `.gitignore`. Repos with sloppy `.gitignore` could leak `.env` files, build artifacts, etc. bridge does not size-guard or path-filter. Mitigation is user discipline.
- Push failures (no push access, remote-side branch protection, etc.) are warn-only — already handled by `_autosync_warn` + the optional Telegram notification. The session still closes.

## File touches

- `bridge.sh`
  - Rewrite of `_bridge_sync` (timeout var, stderr capture, log rotation, calls to `_bridge_sync_set_note`).
  - New `_bridge_sync_set_note` helper (renders `_BRIDGE_SYNC_NOTE`).
  - New `_bridge_sync_banner` helper.
  - In `_bridge_launch`: after the `_bridge_sync` call, banner + marker-file write + per-agent injection (`--append-system-prompt` on the two Claude launch sites).
  - Bump `_BRIDGE_VERSION` to `1.30.0`.
- `bridge-autosync.sh`
  - Single-line default flip.
- `CHANGELOG.md`
  - One entry for `1.30.0` with `Changed` (autosync default flip), `Added` (sync log, sync note, `BRIDGE_SYNC_TIMEOUT`), `Fixed` (fetch failure is now diagnosable).
- `README.md`
  - Autosync section rewritten.
  - One-paragraph addition about the startup sync note and `~/.cache/bridge/sync.log`.

No new files except the runtime artifacts (`.bridge/sync-status.md`, `.bridge/.gitignore`, `~/.cache/bridge/sync.log`).

## Testing

Manual smoke tests in this very repo unless noted otherwise:

1. **Up-to-date branch** — clean tree at tip of `main`, `bridge bridge` → silent launch, no banner, no `.bridge/sync-status.md` written.
2. **Forced timeout** — `BRIDGE_SYNC_TIMEOUT=1` with a slow remote (or temporarily block network) → stderr shows `fetch failed (rc=124), see ~/.cache/bridge/sync.log`; log file has one entry; Claude greets with the fetch-timeout note in its system prompt; banner visible before launch.
3. **Real fetch error** — break the remote URL temporarily → log entry shows the actual git error (e.g. `Could not resolve host` or `403 Forbidden`); note includes first 5 stderr lines.
4. **Behind upstream** — reset to a prior commit, `bridge bridge` → normal ff-only pull line, no banner, no note.
5. **Diverged** — create a local commit while upstream also moved → "diverged" banner; note includes `ahead by N, behind by M`; agent acknowledges divergence.
6. **Dirty tree** — `touch wip`, `bridge bridge` → "dirty" banner; note lists the offending files; agent acknowledges uncommitted state.
7. **`--no-sync`** — `bridge --no-sync bridge` → silent, no log entry, no note, no banner.
8. **Detached HEAD** — `git checkout HEAD~1`, `bridge bridge` → existing detached-HEAD warning, no note, no banner.
9. **No upstream** — `git checkout -b throwaway`, `bridge bridge` → "no upstream" note + banner; agent gets the `git push -u` hint.
10. **Tmux reattach** — start a session, detach, re-run → no banner, no note.
11. **Autosync default-on, feature branch** — on a feature branch, no `BRIDGE_AUTOSYNC` in env, exit session with uncommitted changes → autosync commits and pushes; stderr shows the pushed-N-files line.
12. **Autosync default-on, main** — on `main`, exit with changes → autosync skips with protected-branch warning; existing unpushed-warn still fires for any pre-existing unpushed commits.
13. **Autosync opt-out** — `export BRIDGE_AUTOSYNC=0` in `.envrc`, exit feature-branch session with changes → no commit, no push, only the unpushed-warn if applicable.
14. **Opencode launch with note** — temporarily simulate divergence, `bridge -o <repo>` → banner shown before opencode starts; `.bridge/sync-status.md` present in repo; `.bridge/.gitignore` ensures both stay untracked.
15. **Marker file gitignore** — after test 14, run `git status` → no bridge-related files reported.
16. **Lint** — `bash-language-server` (LSP) and/or `shellcheck` clean over `bridge.sh` and `bridge-autosync.sh`.

No unit-test framework exists for these scripts; verification is by manual smoke + LSP.

## Out of scope (could be added later)

- A `bridge --doctor` extension that reports recent `sync.log` summary statistics (which remotes time out, which fail with auth errors).
- An interactive resolution flow on `diverged` (offer rebase / merge / abort before launching the agent).
- Per-agent richer injection for opencode/copilot if/when their CLIs grow a system-prompt flag.
- Size or path guards on autosync (refuse to stage files larger than X MB, refuse to stage paths matching a denylist).
