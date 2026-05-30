# Bash → Go feature-parity gap analysis

**Date:** 2026-05-28 (obsolete-reclassification added 2026-05-29)
**Status:** Reference / triage input
**Method:** Enumerate the old bash CLI surface (every flag in `bridge.sh`'s arg
parser + every `_bridge_*` function and the deleted helper scripts) and check
each item against the current Go command tree (`cmd/bridge`, `internal/`).
Source of the old bash: `git show ef53cf4^:bridge.sh` (v1.41.11, 3644 lines)
plus the deleted `bridge-watcher.sh`, `bridge-autosync.sh`,
`bridge-unpushed-warn.sh`, `setup-claude-channels.sh`, `bridge-hooks/*`.

> **Completeness caveat.** This is derived mechanically from the bash
> arg-parser + functions vs the cobra tree. It is thorough, but a few
> partial/diverged rows rest on inference, and the ❓ items must be confirmed
> against `internal/forge` and the slot code before acting.

## How to read this

Items are bucketed by status:

- **✅ Ported** — parity or near-parity exists in Go.
- **🟡 Partial / diverged** — same idea, but narrower, renamed, or behaving
  differently.
- **❌ Missing (real gap)** — not in Go and still wanted. These become issues.
- **🚫 Obsolete by design** — intentionally NOT ported; superseded by a newer
  model. **Not gaps.**
- **❓ Verify** — needs a code check before classification is final.

---

## 🚫 Obsolete by design (NOT gaps)

The single biggest pruning of the gap list. **Remote Control superseded the
Telegram multi-slot model.** Telegram is now used only as the **init session
bot** (the initial "session started" announcement), not for ongoing paging or
multi-slot orchestration. Therefore the entire slot/paging subsystem is
intentionally not ported:

| Bash feature | Why obsolete |
|---|---|
| Slot allocation/registry (active): `--slot N`, `--free N`, displace-oldest, per-slot `CLAUDE_CONFIG_DIR=~/.claude-sN`, `_bridge_slot_*`, `_bridge_reconcile_slots` | RC replaces multi-session fan-out; the Go registry stays read-only for visibility only |
| `--no-channel` | No channel system to opt out of |
| Telegram per-slot bots, session banners, close messages, admin aggregate title (`_bridge_telegram_*`, `_bridge_admin_status_update`) | RC superseded; Telegram is init-session-bot only |
| Presence-gated paging (`_bridge_should_page`, `_bridge_telegram_page`) | No ongoing paging |
| Usage-limit watcher daemon (`bridge-watcher.sh`, `_bridge_watcher_start`) | Paged via Telegram; obsolete with the paging model |
| `notify.sh` (idle/elicitation paging), `clear-idle.sh` (cancel debounced page) hooks | Telegram paging hooks; obsolete |
| `--setup-admin`, `--install-admin-commands`, slot-0 admin bot | Admin-bot orchestration belonged to the multi-slot model |
| `--status-rc` | Already a deprecated shim in bash |

Note: the **presence** mode itself (`away`/`back`/`auto`) *is* ported; only its
original purpose (gating Telegram pages) is moot. The **relabel** hook
(`/clear`→`/rename`) is about session *naming*, not paging, so it stays a real
gap (see below), unlike its sibling paging hooks.

---

## ✅ Ported (parity or near-parity)

| Bash | Go |
|---|---|
| `bridge <repo>` pick/launch | `open` + positional rewrite + auto-launch (`BRIDGE_DEFAULT_AGENT`) |
| fzf repo picker, MRU-on-top | preflight picker + `store.MRUTouch` |
| `-r/--remote`, `--refresh` | `list -r` / picker remote-warm |
| `-a/--attach` | `sessions attach` (legacy-mapped) |
| `--status` | `status` (legacy-mapped) |
| `--issues` | `issues` |
| `-w/--worktree` | `open -w` (`.worktrees/<wt>` convention) |
| clone-on-select of a remote repo | implemented (#54, direnv clone) |
| `-V/--version`, `-h/--help` | `version` / cobra help |
| `away`/`back`/`presence` | `presence away\|back\|auto` |
| bash tab completion | cobra `ValidArgsFunction` + meta-augmenter |

---

## 🟡 Partial / diverged

| Bash | Go reality | Note |
|---|---|---|
| `-D/--delete` (local **+ remote** forge delete) | `rm` is **local-only** | remote forge deletion missing → gap |
| `-c/-p/-o` short flags | only `--agent code\|copilot\|opencode` | short-flag ergonomics gap |
| `--remote-control` **on by default** | opt-in via `BRIDGE_DEFAULT_AGENT_ARGS`; no `--no-rc` | default differs |
| `--dashboard` (cross-repo **issue table**) | legacy-mapped to `tui` | different UX; table view missing |
| `--doctor` (**forge** creds: direnv/token/API) | `doctor` is shim/completion wiring | forge-doctor missing → gap |
| autosync | `sync now` / `sync --auto` exist | **session-close** autosync hook not wired |
| `watch` | Go `watch` = fsnotify of repos dir | bash usage-limit watcher is obsolete (above) |
| presence | works; "read-only in Plan A" label is stale | paging-gate purpose moot |

---

## ❌ Missing (real gaps → issues)

| # | Gap | Bash source | Notes |
|---|---|---|---|
| G1 | **Session display name at launch** (`claude -n "<repo>"` / `"<repo> [<wt>]"`) | `_bridge_launch` | spec written: `docs/superpowers/specs/2026-05-28-claude-launch-naming-design.md`; branch `feat/claude-launch-naming` |
| G2 | **`/clear`→`/rename` label restore hook** | `bridge-hooks/relabel.sh`, `_bridge_install_hooks` | needs a Go hook-install path; naming, not paging |
| G3 | **Multi-base repo discovery** (`-B/--base`, `BRIDGE_BASE`, base config file) | `_bridge_collect_bases*` | Go supports a single `BRIDGE_REPOS_ROOT` |
| G4 | **Focus repos** (`-f/--focus-list`, `--focus-add/--focus-rm`, `--no-cache`) | `_bridge_focus_*` | `focus` topic across GitHub+Forgejo |
| G5 | **Forge repo creation** (picker Ctrl-N → create on forge, clone, launch) | `_bridge_create_new` | GitHub/GitLab/Forgejo/ADO |
| G6 | **Remote forge deletion** (extend `rm` to delete remote via forge API) | `_bridge_delete` | with dirty/unpushed confirmation |
| G7 | **Startup ff-pull before launch** + `--no-sync` + skip-note banner/marker | `_bridge_sync`, `_bridge_sync_*` | pre-launch safety pull |
| G8 | **Session-close autosync** (commit+push WIP on exit; protect main) | `bridge-autosync.sh` | + unpushed-commit warning (`bridge-unpushed-warn.sh`) |
| G9 | **`bridge update`** self-update + newer-version hint | `_bridge_update`, `_bridge_check_latest` | Go installs via `make`; at least keep the version-newer hint |
| G10 | **Forge doctor** (diagnose forge targets: direnv export, token, live API) | `_bridge_doctor` | distinct from the existing setup-`doctor` |
| G11 | **Worktree status** (`--worktree-status/--ws`: branch/dirty/ahead/worktrees) | `_bridge_worktree_status` | cross-repo overview |
| G12 | **Single-repo issues** (`-i/--repo-issues [name]`) | `_bridge_issues`/`gh issue list` | minor |
| G13 | **Cross-repo issue dashboard table** (`--dashboard`) | `_bridge_dashboard` | currently mapped to `tui`; restore the table or fold into tui |
| G14 | **RC-URL status picker** (`--pick/--connect`: attach tmux row or copy Remote Control URL) | `_bridge_status_pick` | **more** relevant now that RC is primary |
| G15 | **Legacy flag-spelling ergonomics** (`-c/-p/-o`, explicit `--cd`, `--no-rc`, `here` mode, positional `.`/`update`) | bash arg parser | muscle-memory parity; group as one |

---

## ❓ Verify in code (before acting)

- **Forge breadth.** Bash supported **4 forges** (GitHub / GitLab / Forgejo /
  ADO) across discovery, clone, create, delete, doctor. Confirm whether
  `internal/forge` covers all four or only GitHub + Forgejo; gaps G5/G6/G10
  scale with this.
- **Slot write path.** Confirm no active slot-allocation write path exists
  beyond the read-only registry (expected, given the obsolete reclassification).

---

## Appendix A — full bash CLI flag surface

| Flag(s) | Purpose |
|---|---|
| `-B, --base <dir>` | Override base dir(s) for this invocation (highest precedence; `:`-separated list) |
| `-r, --remote` | Also list uncloned remote repos from discovered forge targets |
| `--refresh` | Force refresh of the remote cache (implies `-r`) |
| `--no-channel` | Legacy mode: no slot allocation, no Telegram |
| `--no-sync` | Skip the upstream fast-forward pull on startup |
| `--slot N` | Force a specific slot (1..N) |
| `-a, --attach` | fzf picker over live sessions; reattach |
| `--pick, --connect` | fzf picker over the `--status` overview; attach tmux row or print/copy RC URL |
| `--status` | Session status table; returns immediately |
| `--status-rc` | RC-focused variant of `--status` |
| `--doctor` | Diagnose forge targets (direnv, tokens, API) |
| `--worktree-status, --ws` | Per-repo git status (branch, dirty, ahead, worktrees) |
| `--issues` | Open issues across GitHub + Forgejo |
| `--dashboard` | Cross-repo open-issue table (top 2 titles/repo) |
| `-i, --repo-issues [name]` | Open GitHub issues for one repo (`$PWD` if no name) |
| `-f, --focus-list [name]` | List focus repos, or open `<name>` |
| `--no-cache` | Bypass the 1-hour cache (only with `-f`) |
| `--focus-add <name>` | Tag a repo with the `focus` topic |
| `--focus-rm <name>` | Remove the `focus` topic |
| `--setup-admin LABEL` | Wire slot 0 (admin) for label-restore hook |
| `--install-admin-commands` | Symlink admin slash commands into `~/.claude-s0/commands/` |
| `--free N` | Force-free slot N |
| `-D, --delete` | Delete a repo (local and/or remote) |
| `-c, --code` | Open in VS Code |
| `-p, --copilot` | Launch `copilot --yolo` |
| `-o, --opencode` | Launch `opencode` |
| `--cd` | Just `cd` into the repo; no agent |
| `--remote-control, --rc` | Pass `--remote-control` to claude (on by default) |
| `--no-remote-control, --no-rc` | Opt out of `--remote-control` for this launch |
| `-w, --worktree NAME` | Pass through to `claude --worktree`; cd into the worktree for `-p/-o/--cd` |
| `-V, --version` | Print version and exit |
| `-h, --help` | Print usage and exit |
| `--` | End-of-options; rest are positional |
| positional verbs | `.`, `update`, `away`, `back`, `here`, `presence`, `<repo-name>` |

## Appendix B — current Go command tree

| Command | Flags | Purpose |
|---|---|---|
| `bridge` (root) | `-v/--verbose` | picker + launcher; bare repo-name rewritten to `open` |
| `list` | `--json`, `-r/--remote`, `--refresh` | list local (+remote with `-r`) repos |
| `open <name>` | `--json`, `--agent`, `-w/--worktree`, `--rc` | resolve repo; emit `cd:`/`exec:` directive |
| `rm <name>` | `--yes` | delete a **local** repo |
| `slots` | `--json` | show slot registry (read-only; `*` = live) |
| `slots prune` | — | drop dead-session slot entries |
| `sessions` | `--json` | live agent (tmux) sessions |
| `sessions attach <slot>` | — | attach to a live session (via shim) |
| `presence [away\|back\|auto]` | `--json` | show/set presence mode |
| `sync [now\|--auto]` | `--json`, `--auto`, `--interval` | show state; `now` one-shot; `--auto` daemon |
| `status` | `--json`, `--slim` | composed summary + per-slot/session table |
| `issues` | `--json`, `--refresh` | open issues across forges (cached) |
| `watch` | `--status`, `--stop`, `--daemonize` | fsnotify watcher of repos root |
| `tui` | `--once` | Bubbletea dashboard |
| `init` | `--shell`, `--dry-run`, `--agent`, `--agent-args`, `--alias` | wire shim + completion + agent exports |
| `doctor` | — | diagnose shim + tab-completion setup |
| `__preflight` | (hidden) | emit shell directive for the shim |
| `__complete-meta <prefix>` | (hidden) | meta-keyword completion fallback |
| `completion [shell]` | (cobra) | completion-script generator |

### Legacy bash-flag mappings (Go)

| Old spelling | Maps to |
|---|---|
| `bridge <repo>` | `open <repo>` |
| `--status` | `status` |
| `--dashboard` | `tui` |
| `-a` / `--attach` | `sessions attach` |
| `-D <name>` | `rm <name> --yes` |
| `away` / `back` / `auto` | `presence …` |
| `-r` / `--refresh` | top-level → `list`; via shim → interactive picker |
