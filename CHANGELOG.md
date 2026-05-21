# Changelog

All notable changes to clrepo are documented here.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [1.41.3] - 2026-05-21

### Fixed

- `--attach` no longer rejects `--no-channel`, `--no-sync`, `--slot`, or `--no-rc`. Those flags govern new session creation and are irrelevant when attaching to an existing session; silently ignoring them lets users who have `--no-channel` in a default alias or environment still reach `--attach`.

## [1.41.2] - 2026-05-21

### Changed

- `--dashboard` output is now a compact table. Repos with zero open issues are hidden. The full repo path is replaced by three short columns — `PLAT` (GH / FJ / ADO), `VIS` (pub / priv), and `REPO` (basename only).

## [1.41.1] - 2026-05-21

### Fixed

- `--dashboard` and `clrepo -f` no longer print `[N] Done ...` job-completion lines in interactive shells. Background fan-out jobs were started directly from the interactive shell function; bash job control reported each one on completion. Fix: wrap each fan-out loop + `wait` in a non-interactive subshell so the outer shell never directly tracks the inner jobs. Affects `_clrepo_dashboard` and both parallel phases of `_clrepo_focus_list`.

## [1.41.0] - 2026-05-20

### Added

- Focus topic — full implementation (#9). Completes all remaining acceptance criteria from the feature issue on top of the MVP (PR #23).
  - **Forgejo support:** `--focus-add` and `--focus-rm` now work on Forgejo repos (`git-forgejo/*`) via `PUT/DELETE /api/v1/repos/freax/<name>/topics/focus`. `clrepo -f` fetches focus repos from Forgejo via `/api/v1/repos/search?topic=true&q=focus`.
  - **Open-issue counts:** `-f` output shows `N open · M yours` per repo. Counts fetched in parallel via `gh issue list --repo` (GH) and `/api/v1/repos/.../issues` (FJ). Current user resolved once per run via `gh api user` / `GET /api/v1/user`.
  - **JSON cache:** focus list cached at `~/.cache/clrepo/focus.json` with 1-hour TTL (tunable via `CLREPO_FOCUS_TTL`). Cache written atomically. `--focus-add` and `--focus-rm` invalidate it on success.
  - **`--no-cache`:** bypass the cache for one run; only meaningful with `-f`.
  - **`clrepo -f <name>`:** opens any local repo by name (tab-completes only focus repos; resolution is against all local repos).
  - **Tab completion:** `clrepo -f <TAB>` completes from the cached focus basenames. Falls back to all-repo completion when cache is absent.
  - **Partial-failure handling:** if Forgejo is unreachable or `FORGEJO_TOKEN` is missing, GH results are still shown and a `[!]` warning appears in the footer. Per-repo count failures show `? open` for that row.
  - **Output format:** two lines per repo — name + count row, then URL. Summary footer with totals.

## [1.40.2] - 2026-05-20

### Fixed

- Tab completion now offers `-i` / `--repo-issues`. The flag was added in 1.35.0 (#11) but never made it into the `_clrepo()` completion's flag string, so `clrepo --r<TAB>` only offered `--remote` / `--remote-control` / `--refresh`. Pre-existing gap.

## [1.40.1] - 2026-05-20

### Fixed

- `-B/--base` no longer leaks across invocations (#5). The flag was mutating the global `_CLREPO_BASES` / `_CLREPO_BASE` via `_clrepo_collect_bases_with` with no save/restore, so the override silently persisted across subsequent `clrepo` calls in the same shell — directly breaking the "for this invocation only" contract. Fix: `clrepo()` now shadows both names with `local` declarations on entry, so bash dynamic scoping confines the override (and any helper that touches the same names) to the function's own scope. Regression test at `tests/test_base_flag_scope.sh`.

## [1.40.0] - 2026-05-20

### Added

- `-B` / `--base <dir>` — per-invocation override for the base dir(s) (#5). Highest precedence (above `CLREPO_BASE` env var, `$_CLREPO_CONFIG/base` config file, and the default), accepts a `:`-separated list like `CLREPO_BASE` itself. Affects every base-touching subcommand (`--status`, `--pick`, picker, `--clone`, `.`-launch, `--doctor`, `--worktree-status`, `--issues`, `--dashboard`, `-i`).
  - Implemented as a pre-pass at the top of `clrepo()` that extracts the flag before the main dispatch loop runs — this matters because many flags early-return in the main loop, so processing `-B` there would leave the override too late for those paths.
  - New `_clrepo_collect_bases_with <value>` helper resets `_CLREPO_BASES` and re-runs `_clrepo_collect_bases` as if `CLREPO_BASE` were the flag value. All the existing `~`/`$HOME` expansion, trailing-`/` normalisation, dedupe, and missing-dir warn-and-skip apply uniformly.
  - `clrepo --help` updated to show the four-step precedence chain.

## [1.39.0] - 2026-05-20

### Added

- Windows / PowerShell support (#8). `clrepo.sh` stays canonical and runs under Git Bash; PowerShell users invoke a thin shim.
  - `clrepo.ps1` shim — locates `bash.exe` (via `$env:CLREPO_BASH`, `git --exec-path`, well-known Git for Windows install paths, then `Get-Command`), sources `clrepo.sh`, forwards `@args` faithfully, mirrors `$LASTEXITCODE`.
  - Platform helpers `_clrepo_is_windows`, `_clrepo_norm_path`, `_clrepo_display_path` and `_clrepo_display_bases` — no-ops on POSIX hosts.
  - `_clrepo_norm_path` is applied per-entry inside `_clrepo_collect_bases` so `CLREPO_BASE='C:\Develop\Repos'`, `'C:/Develop/Repos'`, and `'/c/Develop/Repos'` all resolve to the same POSIX path internally. The same normalization is extended to `_CLREPO_CACHE` and `_CLREPO_CONFIG` for symmetry on Windows.
  - User-facing "under any of: …" / "under …" error messages route through `_clrepo_display_bases` / `_clrepo_display_path` so Windows users see `C:\…` paths in errors.
  - Self-contained Bash test at `tests/test_norm_path.sh` covers POSIX passthrough, `cygpath`-driven Windows conversion, the pure-Bash cygpath-less fallback, and display normalization. 11 assertions, runs offline.
  - README: new "Windows / PowerShell" section with prerequisites, PowerShell setup snippet, and the `cd`-doesn't-survive-back-to-PS caveat.

## [1.38.0] - 2026-05-20

### Added

- Focus topic MVP (#9). New flags scope a repository's `focus` topic on GitHub as the source of truth — no local index file.
  - `-f` / `--focus-list`: enumerate every configured GitHub owner via `_clrepo_targets`, run `gh repo list <owner> --topic focus` in parallel under each owner's direnv context, print a `[GH]`-tagged table.
  - `--focus-add <name>` / `--focus-rm <name>`: resolve `<name>` locally, then `gh api -X PUT /repos/:nwo/topics` with the merged or filtered topic list. Idempotent.
  - ADO repos surface a clear unsupported-error pointing at `clrepo -c <name>`; Forgejo repos show a deferred-to-#9 message.
  - Smoke test at `tests/test_focus_dedup.sh` covers the (forge, owner) dedup so an owner with both `public/` and `private/` subdirs spawns one job, not two.

### Fixed

- `_clrepo_focus_list` dedupes targets on (forge, owner) — matching the sibling `_clrepo_issues` helper — so an owner with both visibility prefixes no longer double-fans-out and concurrently overwrites the same tmpfile. Tmpfile naming uses a monotonic counter, eliminating any `/` or ` ` collapse-to-`_` collision risk. Name resolution escapes ERE metacharacters before grep so repo names containing `.`, `+`, etc. don't match unintended rows.

## [1.37.0] - 2026-05-20

### Added

- `--cd`: pure-navigation mode. Resolves a repo through the normal picker / fuzzy-lookup / MRU / `.` path, cd's into it (and into the matching git worktree if `-w NAME` is passed), and returns to the shell prompt — no claude / VS Code / copilot / opencode / slot / Telegram / tmux. Sibling of `-c` / `-p` / `-o` in the editor switch; mutually exclusive with them. MRU and `~/.cache/clrepo/last` are still updated. Closes #20.

## [1.36.0] - 2026-05-20

### Added

- Multi-base support (#4). `_CLREPO_BASE` becomes the first element of a new internal array `_CLREPO_BASES`; existing code reading `$_CLREPO_BASE` keeps working unchanged on single-base setups.
  - `CLREPO_BASE` env var now accepts a `:`-separated list (PATH-style). Empty elements ignored.
  - `$_CLREPO_CONFIG/base` config file (introduced in 1.33.0) now accepts one absolute path per line; every non-empty, non-`#` line becomes a base.
  - Precedence is whole-list (sources never merged): env > file > `["$HOME/projects/repos"]` default.
  - `~` / `$HOME` expansion, trailing-`/` normalisation, dedupe, and missing-dir warn-and-skip apply uniformly.
  - Discovery (`_clrepo_targets`, picker-list, worktree-status, bash tab completion) iterates every base. CWD launch finds the owning base. The `_clrepo_base_for_rel` helper resolves a rel path to its owning base for cd-style call sites — used by `_clrepo_launch`, `_clrepo_fetch_target`, status-row fetch, issues-fetch, and `_clrepo_delete` (so repos in non-primary bases delete from the right tree and read the right per-dir credentials).
  - "No targets discovered" / "no repos found" messages now list every configured base.
  - `clrepo --help` documents the list semantics.

  Deferred to follow-ups (still tracked on #4): picker/`--status` row labels when multi-base is active (cosmetic — single-base output is unchanged either way); updating `clrepo-watcher.sh` / `clrepo-autosync.sh` to iterate `_CLREPO_BASES` (they read `_CLREPO_BASE` directly today and keep working for the first base).

## [1.35.0] - 2026-05-20

### Added

- `-i` / `--repo-issues [name]`: list open GitHub issues for one repo via `gh issue list`. With no name, resolves from `$PWD` when inside a repo under `$_CLREPO_BASE`. Thin wrapper — `gh` auto-detects the repo's remote once `cd`'d in, with direnv evaluated first so per-repo tokens load. Closes #6.

## [1.34.0] - 2026-05-20

### Added

- `--dashboard`: cross-repo overview. Fans out `gh issue list` over every local repo under `$_CLREPO_BASE` and prints a table with open-issue count and top 2 issue titles per repo, sorted by count descending. Repos without a GitHub remote are silently skipped — use `--issues` for the cross-forge overview. Closes #7.

## [1.33.0] - 2026-05-19

### Added

- `$_CLREPO_CONFIG/base` config file: a single absolute path that overrides the default `$HOME/projects/repos` base dir. Precedence: `CLREPO_BASE` env var > config file > default. `~` and `$HOME` are expanded so users can write `~/work/repos` literally; lines starting with `#` and blank lines are ignored. The first non-empty, non-comment line wins. Foundation for multi-base support (#4). Closes #3.
- `clrepo --help` now documents the base-dir precedence chain (previously undocumented — see #3).

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

## [1.29.0] - 2026-05-19

### Added

- `clrepo --pick` (alias `--connect`) — interactive fzf picker over the
  unified `--status` overview. Selecting a row dispatches by transport:
  tmux-backed rows attach via `tmux attach-session`; RC-only rows print
  the `https://claude.ai/code/<bridgeSessionId>` URL (and copy it to the
  clipboard via `xclip` or `wl-copy` when available). Sessions that have
  neither tmux nor an RC bridge are listed with a ✗ marker; selecting
  one prints "not attachable". Read-only `clrepo --status` is unchanged,
  so scripts and status checks are unaffected. Sits alongside
  `clrepo --attach` (which remains the zero-arg fast-path for slot-bound
  tmux sessions). Closes #2.

## [1.28.0] - 2026-05-19

### Added

- `clrepo --status` now lists every clrepo-managed Claude session on the
  host: slot sessions, `--no-channel` tmux sessions, and `--code` /
  `--opencode` tmux sessions. Discovery uses `@clrepo-*` tmux
  user-options set at session creation; no new persistent state file.
- `clrepo --status` now merges Remote Control URLs into a footer block
  when at least one session has an active `bridgeSessionId`.

### Changed

- `clrepo --status` output format: new `KIND`, `TMUX`, and
  `RC` columns. The bot-token availability ✓/— column has been
  removed — it was slot configuration state, not session state, and
  not strictly tied to whether a Claude session is running.

### Deprecated

- `clrepo --status-rc` — RC info is now part of `clrepo --status`. The
  flag still works and prints a deprecation notice; removal is planned
  for a follow-up minor release.

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
