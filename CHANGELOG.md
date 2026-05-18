# Changelog

All notable changes to clrepo are documented here.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [1.27.0] - 2026-05-18

### Changed

- Enable `mouse on` and `history-limit 50000` on every tmux session
  clrepo creates (claude, copilot, opencode). Mouse wheel now scrolls
  scrollback directly, and the buffer is deep enough to review long
  agent runs. Options are scoped per-session, so the user's other tmux
  sessions and `~/.tmux.conf` are untouched. README documents the
  Shift-drag escape for native-clipboard text selection.

## [1.26.2] - 2026-05-18

### Fixed

- Disable `expand_aliases` while sourcing `clrepo.sh` so an interactive
  `alias clrepo='clrepo --no-channel'` defined after the source line in
  `~/.bashrc` no longer clobbers the `clrepo()` function definition on
  re-source. Extends the protection that already existed inside
  `_clrepo_update` to the initial sourcing path.

## [1.26.1] - 2026-05-16

### Fixed

- Replace stale hook entries on install instead of appending duplicates.

## [1.26.0] - 2026-05-15

### Changed

- Pass `--dangerously-skip-permissions` in the `--no-channel` branch.

## [1.25.1] - 2026-05-14

### Changed

- Adjust paths and self-update URL for standalone repo layout.

## [1.25.0] - 2026-05-07

### Added

- Warn on unpushed commits when exiting the coding agent.

## [1.24.0] - 2026-05-06

### Added

- OpenCode support via `-o`/`--opencode`.

## [1.23.0] - 2026-05-05

### Added

- `--doctor` diagnostics (#5).
- `--worktree-status` / `--ws` (#7).
- `--issues` overview across GitHub + Forgejo forges (#8).
- Admin slash commands for slot 0 (#10).
- Admin bot (#0) title management (#19).
- Session label restore after `/clear` (#20).

### Fixed

- Resolve accumulated merge conflict markers in `clrepo.sh`.

## [1.17.0] - 2026-05-04

### Added

- `--status-rc` command (#9).

## [1.16.1] - 2026-05-04

### Fixed

- Include slot 0 (admin) in `--status` table (#17).

## [1.16.0] - 2026-05-04

### Added

- `-a`/`--attach` session picker (#18).

## [1.15.2] - 2026-05-04

### Changed

- Extract `_clrepo_attach_pick` helper.

## [1.15.1] - 2026-05-04

### Changed

- Extract `_clrepo_reconcile_slots` helper.

## [1.15.0] - 2026-05-04

### Added

- Pre-launch slot credential sanity check.

## [1.14.0] - 2026-05-04

### Changed

- Include worktree in claude display name.

## [1.13.5] - 2026-05-04

### Fixed

- Prune stale out-of-range slot keys from `--status`.

## [1.13.4] - 2026-05-04

### Fixed

- Write worktree path (not main repo) to last cache (#12).

## [1.13.3] - 2026-05-03

### Fixed

- Surface claude startup errors on tmux launch (#11).

## [1.13.2] - 2026-05-03

### Fixed

- Honor `CLREPO_CACHE` env var.

## [1.13.1] - 2026-05-03

### Added

- Local-first update check (#6).

## [1.13.0] - 2026-05-03

### Added

- `setup-claude-channels.sh` plus one-time setup hint.

## [1.12.0] - 2026-05-03

### Added

- Self-init `slots.json` on first use.

## [1.11.1] - 2026-05-03

### Fixed

- Show all configured slots in `--status` (#4).

## [1.11.0] - 2026-05-03

### Changed

- **BREAKING:** Enable `--remote-control` by default.

## [1.10.0] - 2026-05-03

### Added

- `--remote-control` switch for Claude Remote Control.
- `away`/`back`/`here`/`presence` sub-commands.
- Presence gate function and per-slot liveness probe.
- Usage-limit watcher daemon.
- Hook install + watcher start wired into slot allocation.
- `_clrepo_telegram_page` helper and presence-page marker cleanup.

### Fixed

- Stop autosync from leaking `set -u` into the user's shell.
- Start watcher after slot record to avoid a race.
- Create cache dir before opening the hooks lock.
- Silence stderr when the presence file is missing.

## [1.9.0] - 2026-05-02

### Added

- Presence file helpers (groundwork for `away`/`back`/`here`).

## [1.8.3] - 2026-05-02

### Fixed

- Warn when falling into legacy (no-channel) mode.

## [1.8.2] - 2026-04-29

### Fixed

- Use tmux session as the slot liveness signal.

## [1.8.1] - 2026-04-29

### Added

- Opt-in autosync (commit & push on session close).

### Fixed

- Disable `expand_aliases` during update re-source.

## [1.8.0] - 2026-04-29

### Added

- `clrepo update` sub-command with stale-version hint.
- `--no-sync` flag and tab completion.
- `_clrepo_sync` safe ff-pull on launch.
- `_clrepo_warn` helper for yellow stderr warnings.

### Changed

- Extract `_clrepo_tmux_session_name` helper; reused in copilot launch.

## [1.7.1] - 2026-04-28

### Fixed

- Make `-r`/`--refresh` always show the picker.

## [1.7.0] - 2026-04-28

### Added

- Background-warm `repo-meta.json` so tab keyword search works without `-r`.

## [1.6.0] - 2026-04-27

### Added

- `-p`/`--copilot` flag to launch `copilot --yolo`.

## [1.5.0] - 2026-04-27

### Added

- Tab completion via topics/description metadata.

## [1.4.0] - 2026-04-27

### Changed

- Discover forge targets from owner-level `.envrc`.

## [1.3.2] - 2026-04-21

### Fixed

- Print path and remote URL after session ends instead of before.

## [1.3.1] - 2026-04-21

### Fixed

- Keep repo path and remote URL visible despite Claude's screen takeover.

## [1.3.0] - 2026-04-21

### Added

- Print local path and remote URL on repo selection.

## [1.2.2] - 2026-04-21

### Fixed

- Use substring matching for positional arg and tab completion.

## [1.2.1] - 2026-04-21

### Added

- `-c` as short form for `--code`.

## [1.2.0] - 2026-04-21

### Added

- `--code` flag to open repo in VS Code.

## [1.1.1] - 2026-04-21

### Fixed

- Sort remote repos alphabetically per forge.

## [1.1.0] - 2026-04-21

### Added

- ADO project filter via `~/.config/clrepo/ado-projects`.

## [1.0.0] - 2026-04-21

### Added

- Introduce semantic versioning. Baseline includes the repo picker,
  `-r`/`--remote` uncloned discovery, Ctrl-N create, Ctrl-D delete,
  `-w`/`--worktree`, SSH-persistence via tmux, multi-slot Telegram
  channels, and auto-cleanup on tmux session exit.
