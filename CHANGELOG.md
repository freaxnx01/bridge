# Changelog

All notable changes to bridge are documented here.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Fixed

- `bridge list -r` now lists GitHub repos again — including private ones like `obsidian-it`. Two bugs stacked: remote target discovery looked for the token `.envrc` at `github/<owner>/.envrc`, but GitHub clones nest an extra visibility level (`github/<owner>/<public|private>/<repo>`), so the marker actually lives at `github/<owner>/<visibility>/.envrc` and no GitHub target was ever emitted; and the GitHub client queried `/users/{owner}/repos`, which only returns public repos even with a token. Discovery now accepts either the owner-level or visibility-nested `.envrc` (deduped to one target per owner), and `ListRepos` uses the authenticated-user endpoint `/user/repos?visibility=all&affiliation=owner` so private repos come through.
- `bridge open <name> -w <wt>` now **resolves real git worktrees** instead of blindly using the `<repo>/.worktrees/<wt>` path. It consults `git worktree list --porcelain`, so a worktree created anywhere — e.g. Claude Code's `<repo>/.claude/worktrees/<wt>` — is found and launched in. When no worktree named `<wt>` exists, bridge **creates one** via `git worktree add` (branch `worktree-<wt>`) under `<repo>/.worktrees/<wt>` and prints a one-line notice. Previously bridge pointed `tmux -c` at a nonexistent `.worktrees/<wt>` directory, which tmux silently resolved to `$HOME` — so the agent opened in the home dir and no worktree was made. Non-git directories fall back to the bare `.worktrees/<wt>` convention with a stderr banner. New `internal/worktree` package (closes the `git worktree list --porcelain` follow-up deferred in #36).
- Repo-name tab-completion now suggests the **canonical repo casing** (e.g. `FlowHub-CAS-AISE`) rather than splicing the user's typed casing onto the name (which produced `flowHub-CAS-AISE` for a `flow…` prefix). Surfacing canonical casing for a case-differing prefix requires `completion-ignore-case on` in your readline config — see `docs/setup.md`. Resolution stays case-insensitive, so either way the right repo opens.
- Tab-completing a **case-differing repo prefix** now works out of the box: `bridge open flowhub<tab>` completes to `FlowHub-CAS-AISE` (previously only the case-matching `FlowHub<tab>` did). Such prefixes fall into a gap — cobra's `compgen -W -- "$cur"` post-filter is case-sensitive and drops them, while the meta augmenter (`__complete-meta`) was skipping *all* case-insensitive prefix hits on the assumption cobra had handled them. It now skips only *case-matching* prefixes, so the case-differing ones it alone can recover are surfaced. This makes the `completion-ignore-case on` readline tweak optional rather than required (`docs/setup.md` updated).
- Launching a session over SSH no longer "falls through" to the local machine on exit/detach. The bash shim `exec`s tmux locally (terminal becomes the session) but now runs the launch as a child when an SSH session is detected (`SSH_CONNECTION`), so exiting or detaching returns you to the remote shell. New overrides: `BRIDGE_NO_EXEC=1` forces the child branch anywhere, `BRIDGE_FORCE_EXEC=1` forces `exec` even over SSH (`BRIDGE_NO_EXEC` wins if both set). PowerShell already runs launches as a child, so the knobs are bash-only.
- Launching under a terminal whose `$TERM` has no terminfo entry on the host (e.g. **kitty** → `xterm-kitty` on a Chromebook/Crostini box without kitty's terminfo) no longer fails with tmux's `missing or unsuitable terminal`. bridge now detects the unresolvable `$TERM` (via `infocmp`) and transparently launches tmux with `TERM=xterm-256color`, printing a one-line notice. Set `BRIDGE_NO_TERM_FALLBACK=1` to disable and surface the raw error. Unix-only; the Windows `wt.exe` path is unaffected (#104).

## [2.2.0] - 2026-05-29

### Added

- **`bridge tui` — live dashboard** (#64). The dashboard PoC is promoted to a first-class command; `bridge --dashboard` is an alias (#73). Panels are wired to real state: Open Issues reads the on-disk issue cache (#70), Sessions reflects live tmux + `slots.json` (#71), and Enter / slash-command actions are interactive (#72).
- **Repo-name tab-completion restored** (#65). `bridge open`/`bridge rm` and the bare `bridge <repo>` root complete on repo basenames, case-insensitively (#33dc336). Meta-keywords resolve and complete via a keyword resolver plus a tab-time augmenter shim (`shims/bridge-completion-meta.sh`), with substring-basename matching and cross-machine setup wired through `init`/`doctor` (#76). README documents the PowerShell completion install.
- **Azure DevOps forge support** (#118e628). `bridge list -r` lists ADO repos, triggered by `AZURE_DEVOPS_ORG_URL` + `AZURE_DEVOPS_EXT_PAT`/`ADO_PAT` (loaded via direnv). Clones land at `ado/<project>/<repo>`, matching the old bash layout.
- **Picker overhaul** (#4c8ba38). All fzf pickers render entries as `forge/visibility/name` (github) or `forge/project/name` (ado/gitlab), sorted by that key, with the filter bar moved to the top (`--layout=reverse`).
- **Claude session naming at launch** (#84). Launching a claude session now passes `-n <repo>` (or `<repo> [<worktree>]`), restoring the bash bridge's launch label across all picker and open call sites. Non-claude agents are unaffected.
- **`bridge <repo>` auto-launches `BRIDGE_DEFAULT_AGENT`** (#77) instead of only `cd`'ing, when the export is set.
- **Alias completion setup + checks** (#78). `bridge init --alias=br,brg` writes guarded `complete -F __start_bridge <name>` lines (one per alias), and `bridge doctor` reports which aliased completions are registered.
- `bridge init` now defaults the auto-launch agent to **claude** when `--agent` is omitted and no `BRIDGE_DEFAULT_AGENT` export exists yet, writing `BRIDGE_DEFAULT_AGENT=claude` and `BRIDGE_DEFAULT_AGENT_ARGS="--remote-control --dangerously-skip-permissions"` (#82). A plain `bridge init` now makes `bridge <repo>` open a tmux-wrapped claude session out of the box, matching the bash bridge's default. An existing export is never overwritten, so re-running init to refresh completion leaves a customized agent (e.g. opencode) intact. (bash only; the PowerShell `init` writer still does not emit agent exports.)

### Fixed

- `bridge --refresh` run from a single direnv scope no longer obliterates other forges' cache entries. Remote discovery now walks `.envrc` targets under the repos root and fetches each forge with `direnv exec <dir>`, so every forge keeps the right credentials regardless of the parent shell's direnv state (#eee6e6c).
- Picker now groups **private repos before public** via an explicit visibility rank, reads clone credentials inline via `git -c credential.helper` for ADO/GitHub HTTPS clones (PAT never persists to `.git/config` or argv), and uses `--tiebreak=index` so filtered results stay in pre-sorted order instead of fzf's length-bias scoring (#9f30202).
- Legacy bash-shape `~/.cache/bridge/slots.json` is silently migrated on first Go invocation — the file is renamed to `<path>.legacy-<UTC>.bak` and a fresh registry is used, eliminating the per-call `slot upsert failed` warning. Corrupt JSON recovers the same way (#79).
- `bridge sessions attach` / `bridge open` now fail loudly when the shell shim isn't loaded, instead of silently no-op'ing (#66, #67).
- ESC in the picker no longer dumps a raw text list (#63).
- `bridge init --alias=<name>` no longer skips an alias whose name is a prefix of an already-wired one (e.g. `br` vs an existing `brg`). Detection now matches the whole binding line instead of a bare substring (#81).
- `bridge-hooks` idle markers are written to `~/.cache/bridge` (#34).

### Documentation

- `go-migrate.md` drops the obsolete manual `rm ~/.cache/bridge/slots.json` step now that migration is automatic (#80).
- Post-cutover cross-shell parity rules and a PR template added; `CLAUDE.md` restructured with implementer-role framing.
- Design specs and implementation plans for completion restore (#65) and claude-launch-naming (#84).

### Internal

- Cross-platform CI matrix (ubuntu/macos/windows) plus an e2e test harness (#68); `justfile` build/test recipes. Windows build job greened (test-only fixes for shim env, nil-slice JSON, and `.exe`/build-constraint handling).

## [2.1.0] - 2026-05-26

### Removed

- **Phase 4 cleanup** (#35): deleted the frozen bash implementation.
  - Removed: `bridge.sh`, `bridge-watcher.sh`, `bridge-autosync.sh`, `bridge-unpushed-warn.sh`, `setup-claude-channels.sh`, `bridge-hooks/`, all bash tests under `tests/`.
  - Removed: `internal/core` bash-format read-compat for `slots.json` (the legacy `{"slots":{"<idx>":{...}}}` shape). Hosts that haven't run the Go binary since v2.0.0 should delete `~/.cache/bridge/slots.json` before upgrading.
  - Removed: the `mru_compat_test.go` redundancy (MRU shape was always shared between bash and Go).
  - Makefile pruned to Go targets only (`build-go`, `test-go`, `test-shim`, `install-go`, `install-shim`); shellcheck / bats / smoke targets gone.
  - CLAUDE.md guidance simplified — no more "do not edit frozen bash" rule.

### Notes

- bridge-bot's `config.py` still references `setup-claude-channels.sh` in an error message; the Python bot survives but its setup script doesn't. Tracked separately.

### Added

- `bridge slots prune` — drop slot entries whose tmux session is no longer live (#39). Refuses to run if tmux is unavailable (would otherwise wipe the registry on tmux-less hosts).
- `bridge slots` now marks live entries with `*` so staleness is visible without auto-mutating the file. `--json` output gains a `live` bool per slot.

### Fixed

- `bridge -r` / `bridge --refresh` now invoke the interactive picker instead of dumping text (regression vs bash bridge, #42). Bare `-r` opens the picker against local repos + cached remote refs immediately (no network); `--refresh` additionally warms the remote cache bounded by a 5s deadline. `bridge list -r` retains the text-output shape for scripts.
- Picker surfaces **remote-only** repos as `↓ <name>` rows alongside local ones (#54). Selecting a remote entry shells out to `direnv exec <parent-dir> git clone <url> <target>` so the per-dir forge token (from `.envrc`) is loaded; clone progress streams to stderr. Requires `direnv` on PATH. On clone failure the partial target dir is removed so a retry can succeed.
- `bridge -a` / `bridge --attach` are now legacy-mapped to `bridge sessions attach`, restoring the muscle-memory entry point for the live-session picker (#44).
- `bridge status` now includes a per-slot detail table (slot id, kind, repo[wt], agent, age, state, tmux) joining the slot registry with live tmux sessions. Restores the at-a-glance view bash `--status` provided (#43). The 4-line summary remains at top; pass `--slim` for summary-only. Tmux sessions running outside the slot registry are surfaced as `kind=tmux` rows only when at least one of their panes runs a known agent (`claude` / `copilot` / `opencode` / `code` / `node`); see `core.KnownAgentCommands` (#53).
- `bridge --status` is now legacy-mapped to `bridge status`, restoring the bash flag spelling (#43).
- `--json` output keeps the legacy flat shape (`sessions`, `presence`, `sync`, `version`) for backward compatibility, with a new `rows` field carrying the detail table.
- `bridge sync now` and `bridge sync --auto` now serialize against each other via a `sync.lock` flock around `runSyncNow`. Previously a concurrent overlap could let one writer's `sync.json` content overwrite the other's. Block-and-wait semantics match the bash bridge (#38).
- `bridge open <name> -w <wt>` now actually uses the worktree (#36). The slot name becomes `<repo>-wt-<wt>`, the working directory is `<repo>/.worktrees/<wt>` (bash bridge convention), and the recorded slot carries the worktree field. Without `--agent`, `cd` now lands inside the worktree. Non-default worktree layouts (`git worktree list --porcelain`) remain a follow-up.
- `bridge open <name> --json` now enriches the Repo with cached forge metadata (`desc`, `topics`, `default_branch`, `remote_url`) from `~/.cache/bridge/repo-meta.json`, populated by `bridge list -r [--refresh]` (#37). Missing or unreadable cache leaves fields empty — best-effort, never blocks. Populating `last_used` from MRU is a separate follow-up (MRU has no per-entry timestamps).

### Documentation

- `go-migrate.md` documents how to wire bash tab completion for user wrappers like `brg` (#45). Cobra registers completion under the literal program name only; aliases need an explicit `complete -o default -o nospace -F __start_bridge brg` after sourcing `bridge completion bash`.

## [2.0.0] - 2026-05-26

### Changed

- Complete Go-binary rewrite (`cmd/bridge`) replaces the ~3,600-line `bridge.sh`. All read paths (`list`, `slots`, `sessions`, `presence`, `sync`, `status`, `issues`) ship from Plan A; interactive paths (`open`, `rm`, presence writes, `sync now`, `sync --auto`, `watch`, `sessions attach`) plus tmux/WT launcher and shell shim ship from Plan B/B.1.
- `~/.bashrc` now sources `~/.local/share/bridge/bridge-shim.sh` instead of `bridge.sh`. The shim invokes the Go binary via the `__preflight` directive protocol and acts on `cd:` / `exec:` / `noop` responses.
- `bridge --status` decomposed into slim `bridge status` plus focused `bridge sessions` / `bridge slots` / `bridge presence` / `bridge sync` verbs. Each supports `--json`.
- Legacy flags `-r`, `--refresh`, `-D`, and bare `away|back|auto` are silently forwarded to the new verbs inside the binary. Muscle memory preserved.

### Added

- `bridge issues` — open issues across forges with TTL cache.
- `bridge tui` — reserved verb stub for a future dashboard spec.
- Cross-platform support: Linux + Windows from one codebase. tmux launcher on Linux, Windows Terminal launcher on Windows. Inside-tmux invocations use `tmux switch-client` instead of nesting.
- `--json` shape documented in `docs/cli-json-schema.md`.
- Structured logging (`log/slog` + JSON-lines `bridge.log` with rotation) for long-running `sync --auto` and `watch` daemons.

### Frozen / removed

- `bridge.sh`, `bridge-watcher.sh`, `bridge-autosync.sh`, `bridge-unpushed-warn.sh` remain in the repo for one release cycle but are no longer sourced or run. They will be deleted in a follow-up PR (Phase 4).
- `_BRIDGE_VERSION` retired. Go releases tagged `v2.0.0-go.N`.

### Migration notes

- Cache directory `~/.cache/bridge/` is shared between the old bash bridge and the Go binary. `slots.json` written by bash continues to be readable by Go via a read-compat shim; new entries written by Go use an array-shaped `slots` field.
- Bash-only files (`hooks.log`, `hooks.lock`, `meta-warm.lock`, `.channels-hinted`, `sessions/`, `watcher.pid`) remain on disk; Go does not read or write them. These will be cleaned up at Phase 4.
- Roll back the cutover by editing the `~/.bashrc` source line back to `bridge.sh` (a backup `~/.bashrc.bak-bridge-cutover-<timestamp>` is created when the shim is installed via Plan C).

## [1.41.11] - 2026-05-25

### Fixed

- `bridge --status` no longer spawns a stray `tmux new -s foo 'claude ...'` session before printing the slot table (#33). A comment inside `_bridge_slot_status`'s `python3 -c "..."` heredoc used backticks around an example command — `` `tmux new -s foo 'claude ...'` `` — and since the heredoc is double-quoted, bash treated those backticks as command substitution and actually executed the example on every `--status` call. The command launched a real Claude session in a tmux pane named `foo`, attaching the user's TTY until they exited; only then did the python proceed and print the status table. Replaced the backticks with plain quotes and added a warning comment for future editors.

## [1.41.10] - 2026-05-24

### Fixed

- `--status` now surfaces Claude sessions started outside the `bridge` launcher (#31). Two changes to `_bridge_slot_status`: (1) untagged tmux sessions whose pane command is `claude` are listed as `kind=unmanaged` instead of being silently dropped, using the pane's `current_path` basename as the repo label; (2) bridge lookup for non-slot rows now tries `~/.claude` first and then falls back to scanning every `~/.claude-s*/sessions/<pid>.json`, so RC URLs resolve even when a stray session was launched from a slot-specific home directory. Previously such sessions were invisible in `--status` and never produced a Remote Control URL.

## [1.41.9] - 2026-05-24

### Changed

- Tab completion no longer rescans the full repo tree on every keystroke. Basenames are cached at `$_BRIDGE_CACHE/local-repos.list` (built on first use, refreshed in the background after each completion). Previously each tab took 1-2s on a typical setup as `find` walked into every repo's working tree; now it's effectively instant. The cache converges within one tab press after any clone/delete, so no explicit invalidation is needed.

## [1.41.8] - 2026-05-24

### Fixed

- Tab completion no longer dilutes a clean basename match with description-only metadata hits. E.g. `bridge pipe<tab>` now completes to `claude-pipeline` instead of collapsing to the `claude-` common prefix shared with `claude-action-sandbox` (whose description mentions "claude-pipeline"). The meta-search fallback now only runs when basename matching produced nothing, mirroring the positional-arg path.

## [1.41.7] - 2026-05-21

### Changed

- `--dashboard` and `--issues` (`-i`): issue numbers (`#N`) are now OSC 8 terminal hyperlinks — clicking opens the issue URL directly in the browser. The raw URL second line previously printed below each `--issues` row is removed. Column alignment is preserved by padding the visible text manually (escape bytes are excluded from width calculation).

## [1.41.6] - 2026-05-21

### Changed

- `--dashboard` TITLE column now uses the available terminal width instead of a hardcoded 60-character cap. Fixed columns consume 48 chars; the remainder (minimum 40) goes to TITLE. On a typical 220-column terminal the full title is shown untruncated.

## [1.41.5] - 2026-05-21

### Fixed

- `--attach` now finds `no-channel` (and `--code`/`--opencode`) sessions, not only slot-backed ones. Previously it only read `slots.json`; it now also enumerates every tmux session tagged with `@bridge-repo` (the same source `--status` uses), deduplicating against slot rows. The fzf picker shows `LABEL / REPO / KIND / AGE` so sessions are clearly distinguishable.

## [1.41.4] - 2026-05-21

### Changed

- `--dashboard` now shows one row per open issue instead of one row per repo. Columns: `PLAT` / `VIS` (`pub`/`pri`) / `REPO` / `#` / `TITLE` (truncated to 60 chars). Sorted PLAT ASC → REPO ASC → issue number ASC. The `OPEN` count column is gone — issue count is now implicit from the number of rows per repo.

## [1.41.3] - 2026-05-21

### Fixed

- `--attach` no longer rejects `--no-channel`, `--no-sync`, `--slot`, or `--no-rc`. Those flags govern new session creation and are irrelevant when attaching to an existing session; silently ignoring them lets users who have `--no-channel` in a default alias or environment still reach `--attach`.

## [1.41.2] - 2026-05-21

### Changed

- `--dashboard` output is now a compact table. Repos with zero open issues are hidden. The full repo path is replaced by three short columns — `PLAT` (GH / FJ / ADO), `VIS` (pub / priv), and `REPO` (basename only).

## [1.41.1] - 2026-05-21

### Fixed

- `--dashboard` and `bridge -f` no longer print `[N] Done ...` job-completion lines in interactive shells. Background fan-out jobs were started directly from the interactive shell function; bash job control reported each one on completion. Fix: wrap each fan-out loop + `wait` in a non-interactive subshell so the outer shell never directly tracks the inner jobs. Affects `_bridge_dashboard` and both parallel phases of `_bridge_focus_list`.

## [1.41.0] - 2026-05-20

### Added

- Focus topic — full implementation (#9). Completes all remaining acceptance criteria from the feature issue on top of the MVP (PR #23).
  - **Forgejo support:** `--focus-add` and `--focus-rm` now work on Forgejo repos (`git-forgejo/*`) via `PUT/DELETE /api/v1/repos/freax/<name>/topics/focus`. `bridge -f` fetches focus repos from Forgejo via `/api/v1/repos/search?topic=true&q=focus`.
  - **Open-issue counts:** `-f` output shows `N open · M yours` per repo. Counts fetched in parallel via `gh issue list --repo` (GH) and `/api/v1/repos/.../issues` (FJ). Current user resolved once per run via `gh api user` / `GET /api/v1/user`.
  - **JSON cache:** focus list cached at `~/.cache/bridge/focus.json` with 1-hour TTL (tunable via `BRIDGE_FOCUS_TTL`). Cache written atomically. `--focus-add` and `--focus-rm` invalidate it on success.
  - **`--no-cache`:** bypass the cache for one run; only meaningful with `-f`.
  - **`bridge -f <name>`:** opens any local repo by name (tab-completes only focus repos; resolution is against all local repos).
  - **Tab completion:** `bridge -f <TAB>` completes from the cached focus basenames. Falls back to all-repo completion when cache is absent.
  - **Partial-failure handling:** if Forgejo is unreachable or `FORGEJO_TOKEN` is missing, GH results are still shown and a `[!]` warning appears in the footer. Per-repo count failures show `? open` for that row.
  - **Output format:** two lines per repo — name + count row, then URL. Summary footer with totals.

## [1.40.2] - 2026-05-20

### Fixed

- Tab completion now offers `-i` / `--repo-issues`. The flag was added in 1.35.0 (#11) but never made it into the `_bridge()` completion's flag string, so `bridge --r<TAB>` only offered `--remote` / `--remote-control` / `--refresh`. Pre-existing gap.

## [1.40.1] - 2026-05-20

### Fixed

- `-B/--base` no longer leaks across invocations (#5). The flag was mutating the global `_BRIDGE_BASES` / `_BRIDGE_BASE` via `_bridge_collect_bases_with` with no save/restore, so the override silently persisted across subsequent `bridge` calls in the same shell — directly breaking the "for this invocation only" contract. Fix: `bridge()` now shadows both names with `local` declarations on entry, so bash dynamic scoping confines the override (and any helper that touches the same names) to the function's own scope. Regression test at `tests/test_base_flag_scope.sh`.

## [1.40.0] - 2026-05-20

### Added

- `-B` / `--base <dir>` — per-invocation override for the base dir(s) (#5). Highest precedence (above `BRIDGE_BASE` env var, `$_BRIDGE_CONFIG/base` config file, and the default), accepts a `:`-separated list like `BRIDGE_BASE` itself. Affects every base-touching subcommand (`--status`, `--pick`, picker, `--clone`, `.`-launch, `--doctor`, `--worktree-status`, `--issues`, `--dashboard`, `-i`).
  - Implemented as a pre-pass at the top of `bridge()` that extracts the flag before the main dispatch loop runs — this matters because many flags early-return in the main loop, so processing `-B` there would leave the override too late for those paths.
  - New `_bridge_collect_bases_with <value>` helper resets `_BRIDGE_BASES` and re-runs `_bridge_collect_bases` as if `BRIDGE_BASE` were the flag value. All the existing `~`/`$HOME` expansion, trailing-`/` normalisation, dedupe, and missing-dir warn-and-skip apply uniformly.
  - `bridge --help` updated to show the four-step precedence chain.

## [1.39.0] - 2026-05-20

### Added

- Windows / PowerShell support (#8). `bridge.sh` stays canonical and runs under Git Bash; PowerShell users invoke a thin shim.
  - `bridge.ps1` shim — locates `bash.exe` (via `$env:BRIDGE_BASH`, `git --exec-path`, well-known Git for Windows install paths, then `Get-Command`), sources `bridge.sh`, forwards `@args` faithfully, mirrors `$LASTEXITCODE`.
  - Platform helpers `_bridge_is_windows`, `_bridge_norm_path`, `_bridge_display_path` and `_bridge_display_bases` — no-ops on POSIX hosts.
  - `_bridge_norm_path` is applied per-entry inside `_bridge_collect_bases` so `BRIDGE_BASE='C:\Develop\Repos'`, `'C:/Develop/Repos'`, and `'/c/Develop/Repos'` all resolve to the same POSIX path internally. The same normalization is extended to `_BRIDGE_CACHE` and `_BRIDGE_CONFIG` for symmetry on Windows.
  - User-facing "under any of: …" / "under …" error messages route through `_bridge_display_bases` / `_bridge_display_path` so Windows users see `C:\…` paths in errors.
  - Self-contained Bash test at `tests/test_norm_path.sh` covers POSIX passthrough, `cygpath`-driven Windows conversion, the pure-Bash cygpath-less fallback, and display normalization. 11 assertions, runs offline.
  - README: new "Windows / PowerShell" section with prerequisites, PowerShell setup snippet, and the `cd`-doesn't-survive-back-to-PS caveat.

## [1.38.0] - 2026-05-20

### Added

- Focus topic MVP (#9). New flags scope a repository's `focus` topic on GitHub as the source of truth — no local index file.
  - `-f` / `--focus-list`: enumerate every configured GitHub owner via `_bridge_targets`, run `gh repo list <owner> --topic focus` in parallel under each owner's direnv context, print a `[GH]`-tagged table.
  - `--focus-add <name>` / `--focus-rm <name>`: resolve `<name>` locally, then `gh api -X PUT /repos/:nwo/topics` with the merged or filtered topic list. Idempotent.
  - ADO repos surface a clear unsupported-error pointing at `bridge -c <name>`; Forgejo repos show a deferred-to-#9 message.
  - Smoke test at `tests/test_focus_dedup.sh` covers the (forge, owner) dedup so an owner with both `public/` and `private/` subdirs spawns one job, not two.

### Fixed

- `_bridge_focus_list` dedupes targets on (forge, owner) — matching the sibling `_bridge_issues` helper — so an owner with both visibility prefixes no longer double-fans-out and concurrently overwrites the same tmpfile. Tmpfile naming uses a monotonic counter, eliminating any `/` or ` ` collapse-to-`_` collision risk. Name resolution escapes ERE metacharacters before grep so repo names containing `.`, `+`, etc. don't match unintended rows.

## [1.37.0] - 2026-05-20

### Added

- `--cd`: pure-navigation mode. Resolves a repo through the normal picker / fuzzy-lookup / MRU / `.` path, cd's into it (and into the matching git worktree if `-w NAME` is passed), and returns to the shell prompt — no claude / VS Code / copilot / opencode / slot / Telegram / tmux. Sibling of `-c` / `-p` / `-o` in the editor switch; mutually exclusive with them. MRU and `~/.cache/bridge/last` are still updated. Closes #20.

## [1.36.0] - 2026-05-20

### Added

- Multi-base support (#4). `_BRIDGE_BASE` becomes the first element of a new internal array `_BRIDGE_BASES`; existing code reading `$_BRIDGE_BASE` keeps working unchanged on single-base setups.
  - `BRIDGE_BASE` env var now accepts a `:`-separated list (PATH-style). Empty elements ignored.
  - `$_BRIDGE_CONFIG/base` config file (introduced in 1.33.0) now accepts one absolute path per line; every non-empty, non-`#` line becomes a base.
  - Precedence is whole-list (sources never merged): env > file > `["$HOME/projects/repos"]` default.
  - `~` / `$HOME` expansion, trailing-`/` normalisation, dedupe, and missing-dir warn-and-skip apply uniformly.
  - Discovery (`_bridge_targets`, picker-list, worktree-status, bash tab completion) iterates every base. CWD launch finds the owning base. The `_bridge_base_for_rel` helper resolves a rel path to its owning base for cd-style call sites — used by `_bridge_launch`, `_bridge_fetch_target`, status-row fetch, issues-fetch, and `_bridge_delete` (so repos in non-primary bases delete from the right tree and read the right per-dir credentials).
  - "No targets discovered" / "no repos found" messages now list every configured base.
  - `bridge --help` documents the list semantics.

  Deferred to follow-ups (still tracked on #4): picker/`--status` row labels when multi-base is active (cosmetic — single-base output is unchanged either way); updating `bridge-watcher.sh` / `bridge-autosync.sh` to iterate `_BRIDGE_BASES` (they read `_BRIDGE_BASE` directly today and keep working for the first base).

## [1.35.0] - 2026-05-20

### Added

- `-i` / `--repo-issues [name]`: list open GitHub issues for one repo via `gh issue list`. With no name, resolves from `$PWD` when inside a repo under `$_BRIDGE_BASE`. Thin wrapper — `gh` auto-detects the repo's remote once `cd`'d in, with direnv evaluated first so per-repo tokens load. Closes #6.

## [1.34.0] - 2026-05-20

### Added

- `--dashboard`: cross-repo overview. Fans out `gh issue list` over every local repo under `$_BRIDGE_BASE` and prints a table with open-issue count and top 2 issue titles per repo, sorted by count descending. Repos without a GitHub remote are silently skipped — use `--issues` for the cross-forge overview. Closes #7.

## [1.33.0] - 2026-05-19

### Added

- `$_BRIDGE_CONFIG/base` config file: a single absolute path that overrides the default `$HOME/projects/repos` base dir. Precedence: `BRIDGE_BASE` env var > config file > default. `~` and `$HOME` are expanded so users can write `~/work/repos` literally; lines starting with `#` and blank lines are ignored. The first non-empty, non-comment line wins. Foundation for multi-base support (#4). Closes #3.
- `bridge --help` now documents the base-dir precedence chain (previously undocumented — see #3).

## [1.30.0] - 2026-05-19

### Added

- `_bridge_sync` now captures `git fetch` stderr to
  `~/.cache/bridge/sync.log` (auto-rotated at 400 lines) whenever the
  fetch fails, so opaque "fetch failed" messages can finally be
  diagnosed (timeout vs. DNS vs. auth, etc.).
- `BRIDGE_SYNC_TIMEOUT` env var (default `20`s, up from a hardcoded
  `10`s) tunes the fetch timeout for slow links.
- When startup sync skips for a non-trivial reason (fetch failure, no
  upstream, dirty tree, or divergence), bridge now writes a structured
  note to `<repo>/.bridge/sync-status.md` (auto-gitignored via
  `.bridge/.gitignore`), prints a yellow banner to stderr, and — for
  Claude launches — passes the note via `claude --append-system-prompt`
  so the agent knows the branch state is off before the first prompt.

### Changed

- `BRIDGE_AUTOSYNC` now defaults to **on** for feature branches. To opt
  out, set `export BRIDGE_AUTOSYNC=0` in your shell env or the repo's
  `.envrc`. `main`/`master` protection is unchanged: pushes from those
  branches still require `BRIDGE_AUTOSYNC_ALLOW_MAIN=1`.

### Fixed

- The "fetch failed or timed out" warning was discarding the actual
  error. The new log file + `rc=<N>` distinction in the stderr message
  surface timeouts (`rc=124`), DNS errors, auth errors, etc.

## [1.29.0] - 2026-05-19

### Added

- `bridge --pick` (alias `--connect`) — interactive fzf picker over the
  unified `--status` overview. Selecting a row dispatches by transport:
  tmux-backed rows attach via `tmux attach-session`; RC-only rows print
  the `https://claude.ai/code/<bridgeSessionId>` URL (and copy it to the
  clipboard via `xclip` or `wl-copy` when available). Sessions that have
  neither tmux nor an RC bridge are listed with a ✗ marker; selecting
  one prints "not attachable". Read-only `bridge --status` is unchanged,
  so scripts and status checks are unaffected. Sits alongside
  `bridge --attach` (which remains the zero-arg fast-path for slot-bound
  tmux sessions). Closes #2.

## [1.28.0] - 2026-05-19

### Added

- `bridge --status` now lists every bridge-managed Claude session on the
  host: slot sessions, `--no-channel` tmux sessions, and `--code` /
  `--opencode` tmux sessions. Discovery uses `@bridge-*` tmux
  user-options set at session creation; no new persistent state file.
- `bridge --status` now merges Remote Control URLs into a footer block
  when at least one session has an active `bridgeSessionId`.

### Changed

- `bridge --status` output format: new `KIND`, `TMUX`, and
  `RC` columns. The bot-token availability ✓/— column has been
  removed — it was slot configuration state, not session state, and
  not strictly tied to whether a Claude session is running.

### Deprecated

- `bridge --status-rc` — RC info is now part of `bridge --status`. The
  flag still works and prints a deprecation notice; removal is planned
  for a follow-up minor release.

## [1.27.0] - 2026-05-18

### Changed

- Enable `mouse on` and `history-limit 50000` on every tmux session
  bridge creates (claude, copilot, opencode). Mouse wheel now scrolls
  scrollback directly, and the buffer is deep enough to review long
  agent runs. Options are scoped per-session, so the user's other tmux
  sessions and `~/.tmux.conf` are untouched. README documents the
  Shift-drag escape for native-clipboard text selection.

## [1.26.2] - 2026-05-18

### Fixed

- Disable `expand_aliases` while sourcing `bridge.sh` so an interactive
  `alias bridge='bridge --no-channel'` defined after the source line in
  `~/.bashrc` no longer clobbers the `bridge()` function definition on
  re-source. Extends the protection that already existed inside
  `_bridge_update` to the initial sourcing path.

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

- Resolve accumulated merge conflict markers in `bridge.sh`.

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

- Extract `_bridge_attach_pick` helper.

## [1.15.1] - 2026-05-04

### Changed

- Extract `_bridge_reconcile_slots` helper.

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

- Honor `BRIDGE_CACHE` env var.

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
- `_bridge_telegram_page` helper and presence-page marker cleanup.

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

- `bridge update` sub-command with stale-version hint.
- `--no-sync` flag and tab completion.
- `_bridge_sync` safe ff-pull on launch.
- `_bridge_warn` helper for yellow stderr warnings.

### Changed

- Extract `_bridge_tmux_session_name` helper; reused in copilot launch.

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

- ADO project filter via `~/.config/bridge/ado-projects`.

## [1.0.0] - 2026-04-21

### Added

- Introduce semantic versioning. Baseline includes the repo picker,
  `-r`/`--remote` uncloned discovery, Ctrl-N create, Ctrl-D delete,
  `-w`/`--worktree`, SSH-persistence via tmux, multi-slot Telegram
  channels, and auto-cleanup on tmux session exit.
