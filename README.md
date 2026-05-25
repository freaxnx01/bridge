# bridge — Architecture & Editing Guide

## What it is

A bash function that picks a repo under `~/projects/repos/` via fzf and launches an agent session (Claude Code, Copilot, opencode, or VS Code). Sourced from `~/.bashrc`.

## File locations

| What | Path |
|---|---|
| Source file | `~/projects/repos/github/freaxnx01/public/bridge/bridge.sh` |
| Repo | `github.com/freaxnx01/bridge` (public, branch `main`) |
| bashrc source line | `~/.bashrc` (search for `bridge:`) |
| MRU + remote cache + metadata cache | `~/.cache/bridge/` (`mru`, `remote.list`, `repo-meta.json`) |
| Forgejo `.envrc` | `~/projects/repos/git-forgejo/.envrc` (not in a git repo, local-only artifact) |

## Repo tree layout

```
~/projects/repos/
├── github/<owner>/(public|private)/<repo>/   ← each public/private dir has .envrc loading GH_TOKEN from Passbolt
├── gitlab/<owner>/<repo>/                    ← .envrc at gitlab/<owner>/ loads GITLAB_TOKEN
└── git-forgejo/<repo>/                       ← .envrc at git-forgejo/ loads FORGEJO_TOKEN
```

## Discovery model

Every `.envrc` under `~/projects/repos/` whose path matches the layout is a **forge target**: a credential source + clone destination. Target metadata (forge type, owner, visibility) is inferred from the path pattern — no sidecar config files.

| Path pattern | Forge | Owner | Visibility |
|---|---|---|---|
| `github/<owner>/public` | github | `<owner>` | public |
| `github/<owner>/private` | github | `<owner>` | private |
| `gitlab/<owner>` | gitlab | `<owner>` | — |
| `git-forgejo` | forgejo | `freax` (hardcoded) | — |

Adding a new forge target = create the directory + `.envrc` that loads the right token.

## CLI surface

```
bridge                          # fzf picker (local, fast, MRU on top)
bridge <name>                   # case-insensitive basename lookup; on miss, falls back to topic/description search
bridge -r                       # picker + streaming remote listings from all forges
bridge --refresh                # force-refresh remote cache, then pick
bridge -D <name>                # non-interactive delete (local repos only)
bridge <name> -w <worktree>     # pass --worktree to claude
bridge <name> --remote-control  # pass --remote-control to claude (alias --rc)
bridge --help                   # usage
bridge away                     # presence: force "away" (enable Telegram pages for all slots)
bridge back                     # presence: resume auto-detection (per-slot tmux client check)
bridge here                     # presence: force "here" (suppress Telegram pages for all slots)
bridge presence                 # show current presence mode + per-slot effective state
```

## Picker keybindings

| Key | Action |
|---|---|
| Enter | Launch (clone first if `↓` remote entry) |
| Ctrl-N | Create new remote repo (fzf query = seed name) → forge picker → name prompt → API POST → clone → launch |
| Ctrl-D | Delete highlighted repo (prompts L/R/B/cancel, type-to-confirm for remote) |
| Ctrl-R | Refresh remote cache (only in `-r` mode) |

## Function map

| Function | Purpose |
|---|---|
| `bridge()` | Main entry: flag parsing, picker, dispatch |
| `_bridge_launch()` | **Single launch point.** cd, update MRU, tmux wrap if SSH, then `claude`. The slot/telegram wrapper replaces this body. |
| `_bridge_targets()` | Walk `.envrc` tree → emit TSV of forge targets |
| `_bridge_fetch_target()` | One forge API call in a direnv-activated subshell |
| `_bridge_remote_list()` | Union of all targets, cached with 10min TTL, streams via `tee`; also persists `repo-meta.json` (description + topics per repo) |
| `_bridge_meta_search()` | Case-insensitive keyword match against cached topics + description; emits `<type>\t<path>\t<snippet>` TSV |
| `_bridge_clone_url()` | Infer clone URL from rel path (HTTPS for GitHub/GitLab, SSH for Forgejo) |
| `_bridge_git_clone_in()` | `git clone` with direnv-loaded creds; injects HTTPS credential helper for GitHub |
| `_bridge_clone_remote()` | Resolve URL + clone + invalidate cache |
| `_bridge_create_new()` | Forge picker → name prompt → API POST → clone → launch |
| `_bridge_delete()` | Dirty check → L/R/B prompt → type-to-confirm → API DELETE + rm -rf |
| `_bridge()` | Tab completion (case-insensitive basenames + flags) |

## Keyword lookup (metadata fallback)

If `bridge <name>` doesn't match any local repo basename, it falls back to searching **topics and description** across the cached forge metadata (`~/.cache/bridge/repo-meta.json`).

- **Populated by** any run that reaches `_bridge_remote_list` (i.e. `bridge -r` or `bridge --refresh`). Cache TTL applies; re-fetched together with `remote.list`.
- **Match order:** topic hits rank above description hits. Within each group, sorted by repo basename.
- **1 hit** → auto-launch. If the hit is an uncloned remote repo, clones first (same flow as picker).
- **2+ hits** → fzf picker annotated with `<path>  [topic: <match>]` or `<path>  [desc: …snippet…]`.
- **0 hits** → `bridge: no such repo: <name>` (exit 1), same as before.

To make a repo keyword-reachable, tag it on the forge:

```bash
gh repo edit <owner>/<repo> --add-topic <keyword>      # GitHub
```

GitLab and Forgejo also expose topics in their repo settings; topics and description are pulled from whichever API the `.envrc` target hits.

## Credential flow

All forge API calls (list, create, delete, clone-auth) use **per-dir PATs loaded via direnv**:

1. Subshell `cd` into the target's `.envrc` directory
2. `eval "$(direnv export bash)"` — this runs the `.envrc` which loads the token from Passbolt
3. Use the exported `GH_TOKEN` / `GITLAB_TOKEN` / `FORGEJO_TOKEN` for curl/git

GitHub clone uses an inline `credential.helper` injected via `git -c` (no SSH key for GitHub).
Forgejo clone uses SSH (port 222); bridge runs plain `git clone` and relies on your `~/.ssh/config`. Typical host stanza:

```
Host git.home.freaxnx01.ch
  Port 222
  User git
  IdentityFile ~/.ssh/id_ed25519_forgejo
  IdentitiesOnly yes
```

GitLab clone uses HTTPS via the `GIT_CONFIG_*` credential helper wired in its `.envrc`.

GitHub delete requires `delete_repo` scope on the per-dir PAT (edit at https://github.com/settings/tokens, same token string).

## SSH persistence (tmux)

When `$SSH_CONNECTION` is set, `_bridge_launch` wraps claude in:
```bash
tmux new-session -A -s "<repo>[-<worktree>]" claude -n <repo> [--worktree <wt>]
```
`-A` = attach-or-create. Disconnecting the SSH client detaches tmux; re-running the same `bridge` command reattaches.

Session name = repo basename (+ `-<worktree>` if specified), sanitized to `[A-Za-z0-9_-]`.

### Scrolling inside the tmux session

bridge applies two session-scoped tmux options on session create
(`_bridge_tmux_session_defaults`, applied to claude, copilot, and opencode
launches alike):

- `mouse on` — mouse wheel scrolls scrollback; click selects panes.
- `history-limit 50000` — deep enough to review long agent runs.

Both are set with `tmux set-option -t <session>`, so they only affect
bridge's sessions — your other tmux sessions and `~/.tmux.conf` are
untouched. The keyboard fallback (`Ctrl-b [` to enter copy mode, then
PgUp/PgDn/arrows, `q` to exit) works regardless.

**Selecting text with the mouse:** with `mouse on`, click-drag goes through
tmux and lands in tmux's paste buffer, not the system clipboard. Hold
**Shift** while dragging to bypass tmux and use the terminal emulator's
native selection (works in most terminals — iTerm2, GNOME Terminal,
Alacritty, Kitty, Windows Terminal).

## Integration point for slot/telegram

The slot/telegram spec (separate document) replaces `_bridge_launch()` to add:
- Slot allocation (N parallel sessions with pid tracking)
- Per-slot `CLAUDE_CONFIG_DIR` (`~/.claude-sN`)
- Telegram bot naming + banner message + pin
- Exit hooks for cleanup

Everything upstream of `_bridge_launch` (picker, clone, create, delete, MRU, worktree parsing) is untouched. The worktree name arrives as `$2` of `_bridge_launch`.

## Bootstrap and channel wiring

Slot tracking is the **default mode** — no setup required. `_bridge_slots_init` creates `~/.cache/bridge/slots.json` on first launch; allocation, status, reconciliation, and all other slot machinery just work. Opt out per-launch with `--no-channel`.

Telegram pages are **opt-in**. `setup-claude-channels.sh` is the interactive scaffold:

```bash
~/projects/repos/github/freaxnx01/public/bridge/setup-claude-channels.sh
```

It prompts for the Telegram owner user_id and per-slot Passbolt resource IDs, validates each id against Passbolt, and writes the result. Idempotent — re-run anytime to add slots, rotate tokens, or update the owner.

### Three-file split under `~/.cache/bridge/`

| File | Lifecycle | Bootstrapped by |
|---|---|---|
| `slots.json` | ephemeral runtime state — slot → repo/worktree/pid/session map | bridge (auto, on first launch) |
| `slot-tokens.json` | long-lived secrets — slot → Passbolt resource id for the bot token | `setup-claude-channels.sh` |
| `owner.json` | one-time identity — `{telegram_user_id}` for paging | `setup-claude-channels.sh` |

The split is intentional: runtime state churns every launch and is safe to delete; secrets need rotation tooling; identity is set once. Merging into a single `channels.json` would muddy those concerns.

When `slot-tokens.json` is absent, bridge prints a one-time discoverability hint (gated by a `.channels-hinted` sentinel) pointing at the setup script, then stays silent for the host's lifetime.

## bridge-bot — Telegram wrapper for spawning

`bridge-bot/` ships a standalone Telegram bot that wraps `bridge` on the host.
DM it `/new` to get a paginated picker of local (and optionally remote) repos;
tap a row to launch a fresh Claude session in detached tmux. Independent of
bot0/admin and the per-slot Telegram bots; no Claude in the command loop.

Setup is part of `setup-claude-channels.sh`. See
[`bridge-bot/README.md`](bridge-bot/README.md) and the design at
[`docs/specs/2026-05-24-bridge-telegram-bot-design.md`](docs/specs/2026-05-24-bridge-telegram-bot-design.md).

## Presence-aware Telegram pages

bridge proactively pages each slot's Telegram bot when Claude is paused or
hits the 5h usage limit, but only when the user is **away** from the slot's
tmux session. See spec at `docs/specs/2026-05-02-bridge-presence-aware-telegram-pages-design.md`.

### Presence model

| `~/.cache/bridge/presence` | Effective state |
|---|---|
| missing or `auto` | per-slot: present iff the slot's tmux session has ≥1 attached client |
| `away` | always away (forced — pages always sent) |
| `here` | always present (forced — pages suppressed) |

### Event sources

- **Notification hook** (per-slot `~/.claude-s<N>/settings.json`): `idle_prompt` (debounced 120s) and `elicitation_dialog` (immediate) trigger a page via `bridge-hooks/notify.sh`. `UserPromptSubmit` fires `bridge-hooks/clear-idle.sh` to cancel a pending idle page.
- **Watcher daemon** (`bridge-watcher.sh`): polls every 30s for the usage-limit phrase in each active slot's tmux pane. Started by `_bridge_slot_allocate`, self-exits when no slots are occupied.

Both event sources gate through `_bridge_should_page` before sending. Pages go to the slot's existing per-slot bot (`@claude_freax_s<N>_bot`); replies route back via the existing `--channels plugin:telegram@...` mechanism.

## Use cases: Telegram pages vs. Remote Control

Two independent channels keep you in the loop when away from the terminal. Both are on by default; opt out with `--no-channel` (Telegram + slot tracking) or `--no-remote-control` / `--no-rc`.

| | Telegram pages | Remote Control |
|---|---|---|
| Direction | push (Claude → you) | pull (you → Claude) |
| Triggers | idle prompt (debounced 120s), elicitation dialog, 5h usage limit | manual — open claude.ai/code or mobile app |
| Granularity | discrete pages with short replies | full live-session steering (read + write) |
| Auth | per-slot bot token (Passbolt-backed) | claude.ai OAuth |
| Context cost | zero — pages are out-of-band | counts against the live session's usage quota |
| Presence-aware | yes — gated on `~/.cache/bridge/presence` | no — always available while session is alive |

### Telegram — when to use

- **Long-running tasks.** Kick off, walk away, get pinged when Claude pauses or hits the 5h limit.
- **Many parallel slots.** Each slot has its own bot, so the page identifies *which* session needs attention.
- **One-line replies suffice.** "yes", "go ahead", "use option 2" — no need to open a full client.

### Remote Control — when to use

- **Steer from anywhere.** Continue an active session from claude.ai/code or the mobile app without an SSH terminal.
- **Spectate long runs.** Watch context unfold in the browser without holding the tmux attach.
- **Device hand-off.** Start at desk, continue on laptop or phone — same live session.

### Combining them — meaningful?

Yes. Treat them as complementary tiers, not redundant:

1. **Telegram** pages you when something needs attention (push, low-friction, free).
2. If a one-line reply suffices, answer in Telegram — done.
3. If you need full context — read scrollback, send a multi-line steer, kick off a follow-up — open **Remote Control** on the same session.

Both target the same underlying Claude session, so a Telegram reply and a Remote Control prompt land in the same conversation. Presence governs Telegram alone; Remote Control is always reachable while the session is alive.

### Slash commands via remote inputs

Both remote channels relay user text into the same input stream Claude Code reads from the TUI, so most slash commands work — with two notable carve-outs.

**Telegram bot (channel plugin)**
- The `--channels plugin:telegram@claude-plugins-official` flag relays inbound Telegram DMs as `<channel source="telegram">` events into the session. The leading `/` is preserved, so `/help`, `/clear`, `/compact`, plugin commands, and project commands (e.g. bridge's `/status`) all execute as slash commands.
- No documented escape to send a literal `/foo` as plain text — if you need that, currently you'd have to drop the leading `/`.

**Remote Control (`/remote-control`, default in bridge)**
- Most text-producing commands work the same as in the TUI: `/compact`, `/clear`, `/context`, `/usage`, `/exit`, `/extra-usage`, `/recap`, `/reload-plugins`.
- Local-only (blocked over RC because they need a terminal UI): `/mcp`, `/plugin`, `/resume` (interactive pickers), and any command that only applies to the local CLI (e.g. `/remote-control` itself).
- Auth-sensitive commands like `/login` require a browser flow; running them remotely doesn't help.

**Implication for the slot 0 admin flow**

The custom slash commands installed by `bridge --install-admin-commands` (`/status`, `/issues`, `/worktree-status`, …) are project-level commands stored under `~/.claude-s0/commands/`. Both Telegram and RC see them as regular slash commands and will execute them, so the admin can drive bridge from a phone via either channel.

References:
- https://code.claude.com/docs/en/remote-control.md (Limitations section)
- https://code.claude.com/docs/en/channels.md (Telegram setup)

### Compatibility: Telegram + Remote Control on the same slot

Both subsystems run inside the same `claude` process and share that slot's `CLAUDE_CONFIG_DIR` (`~/.claude-s<N>`) and `TELEGRAM_BOT_TOKEN` env. They don't conflict at the launcher level: `--channels plugin:telegram@…` and `--remote-control` are independent flags wired together at line 1367 of `bridge.sh`. Per-slot isolation holds (each slot's bot has its own token; RC sessions are scoped to the OAuth user).

The lifecycle paths under `_bridge_telegram_setup` / `_bridge_telegram_cleanup` (`bridge.sh:822, 867`) fire identically under both tmux and foreground launches; reattaching a tmux'd RC session keeps the bot alive (no duplicate registrations, no dropped pages) because setup runs only on session create.

## Startup sync and recovery

`bridge <name>` runs a fast-forward sync on the current branch before launching the agent:

```
timeout ${BRIDGE_SYNC_TIMEOUT:-20}s git fetch
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

For the non-trivial cases (`fetch` failed, `no upstream`, `dirty`, `diverged`) bridge now:

- Writes the actual fetch stderr to `~/.cache/bridge/sync.log` (auto-rotated at 400 lines).
- Renders a structured note explaining the skip and suggested next commands.
- Prints a yellow banner with the note's reason line right before the agent starts.
- Writes the full note to `<repo>/.bridge/sync-status.md` (gitignored via `.bridge/.gitignore`, written on first use).
- For Claude launches: passes the note via `claude --append-system-prompt`, so the agent knows the branch isn't current before the first user prompt.

`BRIDGE_SYNC_TIMEOUT` (seconds, default `20`) controls the fetch timeout. Bump it on slow links; lower it if you'd rather fail fast.

## Session-exit autosync

When a bridge session closes, `bridge-autosync.sh` (sourced from `bridge.sh` and re-invoked by the tmux `session-closed` hook) commits any uncommitted changes and pushes them to the upstream branch.

**Default: ON for feature branches.** To disable per-repo, add to the repo's `.envrc`:

```bash
export BRIDGE_AUTOSYNC=0
```

`main` and `master` are protected. Autosync skips them with a warning unless you opt in explicitly:

```bash
export BRIDGE_AUTOSYNC_ALLOW_MAIN=1
```

Caveats:

- Autosync runs `git add -A` — anything not in `.gitignore` will be committed and pushed. Mind your `.gitignore` for `.env`, build artifacts, etc.
- Push failures (no access, branch protection, etc.) emit a yellow warning and (if a slot token is set) a Telegram notification, but never block session exit.
- The auto-commit message is `chore(autosync): wip from bridge session (<timestamp>)`. Squash or amend before opening a PR.

## Config variables

| Variable | Default | Purpose |
|---|---|---|
| `_BRIDGE_BASE` | `/home/freax/projects/repos` | Root of the repo tree |
| `_BRIDGE_CACHE` | `~/.cache/bridge` | MRU, remote cache, (future: slots) |
| `_BRIDGE_REMOTE_TTL` | `600` (10 min) | Remote listing cache lifetime in seconds |
| `BRIDGE_SYNC_TIMEOUT` | `20` | Seconds before `git fetch` is killed at startup |
| `BRIDGE_AUTOSYNC` | `1` (on) | Commit-and-push uncommitted changes on session close; set to `0` per-repo to opt out |
| `BRIDGE_AUTOSYNC_ALLOW_MAIN` | `0` (off) | Allow autosync to push from `main`/`master` (off by default for safety) |

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

## Known limitations

- GitHub API `per_page=100`: owners with 100+ repos in a single visibility will be truncated. Fix: add pagination.
- Forgejo owner is hardcoded to `freax` in the path-inference table.
- `bridge -D <name>` only matches local repos. Use picker Ctrl-D for uncloned remote-only repos.
- No parallel forge API calls: each target is fetched sequentially. With 5 targets, worst case is ~2.5s (cache miss).
