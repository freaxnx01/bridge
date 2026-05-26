# bridge

A repo picker and agent-session launcher. Walks `~/projects/repos/`, presents an fzf picker, then opens the selected repo in a tmux-wrapped agent session (Claude Code, Copilot, opencode, or VS Code) or just `cd`'s into it.

As of `v2.0.0` (2026-05-26), `bridge` is a Go binary at `~/.local/bin/bridge` wrapped by a tiny shell-function shim. The legacy `bridge.sh` is frozen in the tree but no longer sourced; scheduled for deletion in Phase 4 ([#35](https://github.com/freaxnx01/bridge/issues/35)).

> **Installing or migrating?** See [`go-migrate.md`](go-migrate.md). It covers the one-time `~/.bashrc` edit, updating an already-cut-over host, the shim source, and rollback.

## Layout

```
~/projects/repos/
├── github/<owner>/(public|private)/<repo>/
├── gitlab/<owner>/<repo>/
└── git-forgejo/<repo>/
```

Each forge directory has an `.envrc` (loaded by direnv) that exports the appropriate token: `GH_TOKEN`, `GITLAB_TOKEN`, `FORGEJO_TOKEN`. Discovery is purely path-pattern based — no sidecar config.

| Path pattern | Forge | Owner | Visibility |
|---|---|---|---|
| `github/<owner>/public/<repo>` | github | `<owner>` | public |
| `github/<owner>/private/<repo>` | github | `<owner>` | private |
| `gitlab/<owner>/<repo>` | gitlab | `<owner>` | — |
| `git-forgejo/<repo>` | forgejo | `freax` (hardcoded) | — |

## CLI surface

```
bridge                          # fzf picker (local repos, MRU on top)
bridge <name>                   # case-insensitive basename lookup; on miss, keyword search across cached topics/desc
bridge -r                       # picker (no network) — local repos
bridge --refresh                # picker + refresh remote cache (bounded by 5s)
bridge -a / --attach            # session picker → tmux attach
bridge -D <name>                # delete a local repo
bridge <name> -w <wt>           # open repo at <repo>/.worktrees/<wt>
bridge <name> --agent <name>    # launch via tmux + named agent (claude|copilot|opencode|code)
bridge <name> --rc              # pass --remote-control to claude
bridge --version

# Composed read commands (each supports --json):
bridge status [--slim]          # summary + per-slot/session detail table
bridge slots [prune]            # slot registry; live entries marked '*'; prune drops dead entries
bridge sessions [attach <slot>] # live tmux sessions; attach picker without a slot arg
bridge presence [away|back|auto]
bridge sync [now|--auto]
bridge list [-r] [--refresh]    # text dump (scripts); -r adds remote rows
bridge issues                   # open issues across forges (TTL cache)
bridge watch                    # long-running watcher of ~/projects/repos/
```

Legacy flag spellings (`-r`, `--refresh`, `-D`, `-a`/`--attach`, `--status`, `away`/`back`/`auto`) are silently rewritten to the modern subcommand form so bash-bridge muscle memory keeps working. See `cmd/bridge/legacy.go`.

JSON output for every read command is documented in [`docs/cli-json-schema.md`](docs/cli-json-schema.md).

## Cache files

All under `~/.cache/bridge/` (override with `XDG_CACHE_HOME`):

| File | Written by | Purpose |
|---|---|---|
| `mru` | `open` | newline-delimited MRU list (most recent first) |
| `slots.json` | `open`, `slots prune` | slot registry (`{slots: [...]}`) — id, repo, worktree, agent, created |
| `presence.json` | `presence` writes | `{mode: away|back|auto}` |
| `sync.json` | `sync now`, `sync --auto` | last sync run + unpushed list + queue |
| `repo-meta.json` | `list -r [--refresh]` | per-repo topics/description/default-branch/remote URL |
| `remote.list` | `list -r [--refresh]` | cached union of all forge listings |
| `issues.json` | `issues` | open-issue cache (TTL) |
| `bridge.log` | long-running daemons | structured JSON lines (slog), rotated by `lumberjack` |
| `sync.lock` | `runSyncNow` | flock — serializes concurrent `sync now`/`sync --auto` |
| `slots.lock` | slot writes | flock — serializes concurrent `UpsertSlot` |

## Go packages

| Package | Role |
|---|---|
| `cmd/bridge` | All commands + cobra wiring + the `__preflight` directive emitter the shim consumes |
| `internal/core` | Repo discovery, slot/session/presence/MRU/meta read+write, pure parsers |
| `internal/launcher` | tmux/Windows-Terminal argv construction (cross-platform) |
| `internal/forge` | GitHub/GitLab/Forgejo HTTP clients + RepoRef + issue listings |
| `internal/syncer` | `git fetch && git pull --ff-only` across repos + unpushed detection |
| `internal/store` | atomic write, flock primitive, cache path helpers |
| `internal/shellbridge` | the `cd:` / `exec:` / `noop` directive protocol the shim parses |
| `internal/agents` | agent spec (binary + args) for claude/copilot/opencode/code |

## Shim — how `bridge` becomes a `cd`-capable command

A binary can't change its parent shell's working directory. So `~/.local/share/bridge/bridge-shim.sh` defines a `bridge()` shell function that:

1. Runs `command bridge __preflight "$@"` to ask the binary what to do.
2. Reads the directive from stdout:
   - `cd:<path>` → `cd "<path>"`
   - `exec:<sh-quoted argv>` → `eval "exec <argv>"` (e.g. `tmux new-session ...`)
   - `noop` → `command bridge "$@"` (let cobra handle it normally)
3. Returns the binary's exit code.

The shim is ≤20 lines of logic on purpose. All real work lives in the binary.

## Credential flow

Forge API calls run with tokens loaded from each target dir's `.envrc` (direnv → Passbolt). HTTPS clones use an inline `credential.helper` (GitHub). Forgejo clone uses SSH (port 222) via `~/.ssh/config`. GitLab clone uses HTTPS via the `GIT_CONFIG_*` helpers the dir's `.envrc` wires.

## Status of bash-era features in the Go port

Not everything in `bridge.sh` has been ported yet. The cutover prioritized the daily picker/launcher path; specialty subsystems are tracked separately:

| Feature | Status | Tracking |
|---|---|---|
| fzf picker (local) | ✓ shipped | [PR #46](https://github.com/freaxnx01/bridge/pull/46) |
| Session picker for `-a/--attach` | ✓ shipped | [#44](https://github.com/freaxnx01/bridge/issues/44) |
| Worktree wiring (`-w`) | ✓ shipped | [#36](https://github.com/freaxnx01/bridge/issues/36) |
| Per-slot status table (`bridge status`) | ✓ shipped | [#43](https://github.com/freaxnx01/bridge/issues/43) |
| `slots prune` + live marker | ✓ shipped | [#39](https://github.com/freaxnx01/bridge/issues/39) |
| Sync serialization (flock) | ✓ shipped | [#38](https://github.com/freaxnx01/bridge/issues/38) |
| Picker: remote rows + clone-on-select | open | [#54](https://github.com/freaxnx01/bridge/issues/54) |
| Slot-count limit + displacement | open | [#56](https://github.com/freaxnx01/bridge/issues/56) (mechanism), [#32](https://github.com/freaxnx01/bridge/issues/32) (UX) |
| Telegram per-slot pages (presence-aware) | bash-only; scope-down in flight | [#40](https://github.com/freaxnx01/bridge/issues/40), [#41](https://github.com/freaxnx01/bridge/issues/41) |
| Startup sync (`git fetch` before launch) | bash-only | not yet filed |
| Session-exit autosync (`bridge-autosync.sh`) | bash-only | not yet filed |
| `--install-admin-commands` / `setup-claude-channels.sh` | bash-only | not in Go scope |

The bash scripts (`bridge.sh`, `bridge-watcher.sh`, `bridge-autosync.sh`, `bridge-unpushed-warn.sh`) remain in the tree but are **not sourced** and **not executed**. They will be deleted in Phase 4 ([#35](https://github.com/freaxnx01/bridge/issues/35)).

## Editing

- Go changes go in `cmd/bridge/` and `internal/`. Don't edit the frozen bash scripts.
- Tests: `go test ./...`. Per-package: `go test ./cmd/bridge -run TestXxx`.
- Install locally: `make install-go`. Build only: `make build-go`.
- See [`CLAUDE.md`](CLAUDE.md) for commit conventions (Conventional Commits; tag the Go binary as `v2.0.0-go.N`).
- Design docs: `docs/specs/2026-05-25-bridge-core-redesign-design.md`. Plans: `docs/plans/2026-05-26-bridge-core-redesign-plan-{a,b,b1,c}.md`.

## Windows

Cross-compile via `GOOS=windows GOARCH=amd64 go build ./cmd/bridge`. Install the `.exe` on PATH as `bridge.exe`, dot-source `shims/bridge-shim.ps1` from `$PROFILE`. Launcher uses Windows Terminal (`wt.exe new-tab`). No Windows CI; the binary builds clean but the runtime path is exercised manually.

## Known limitations

- Picker is local-only; remote-only entries can't be selected for clone yet ([#54](https://github.com/freaxnx01/bridge/issues/54)).
- No slot-count limit or displacement in the Go binary ([#56](https://github.com/freaxnx01/bridge/issues/56)).
- `last_used` is not populated in `open --json` output (MRU file has no per-entry timestamps).
- GitHub API `per_page=100`: owners with 100+ repos in a single visibility will be truncated.
- Forgejo owner is hardcoded to `freax` in path-inference.
