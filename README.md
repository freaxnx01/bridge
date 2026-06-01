# bridge

A repo picker and agent-session launcher. Walks `~/projects/repos/`, presents an fzf picker, then opens the selected repo in a tmux-wrapped agent session (Claude Code, Copilot, opencode, or VS Code) or just `cd`'s into it.

`bridge` is a Go binary at `~/.local/bin/bridge` wrapped by a tiny shell-function shim. The legacy `bridge.sh` and friends were deleted in v2.1.0 (Phase 4, [#35](https://github.com/freaxnx01/bridge/issues/35)); the Go binary is the only implementation.

> **Installing or updating?** See [`go-migrate.md`](go-migrate.md). It covers fresh install, updating an already-installed host, and migrating from a pre-v2.1 bash-bridge host.

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
bridge -r                       # picker (no network) — local + cached remote rows (↓ marker = clone-on-select)
bridge --refresh                # picker + refresh remote cache (bounded by 5s)
bridge -a / --attach            # session picker → tmux attach
bridge -D <name>                # delete a local repo
bridge <name> -w <wt>           # open the <wt> worktree (resolves existing via git, else creates it)
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
bridge tui                      # Bubbletea dashboard (repos / cached issues / live sessions; Enter to act)
bridge nav                      # interactive navigator: pick a repo → dashboard of its sessions & worktrees
bridge --dashboard              # alias for `bridge tui` (legacy spelling)
```

Legacy flag spellings (`-r`, `--refresh`, `-D`, `-a`/`--attach`, `--status`, `--dashboard`, `away`/`back`/`auto`) are silently rewritten to the modern subcommand form so bash-bridge muscle memory keeps working. See `cmd/bridge/legacy.go`.

`bridge nav` is a two-screen interactive navigator: a repo picker (local repos plus async remote rows you can clone on select) and a per-repo dashboard of tmux sessions and worktrees with async git-dirty status. On the dashboard, the highlighted worktree's branches, recent commits, and git status show in read-only panels alongside the list (on terminals ≥90 columns; narrower terminals show the list only). Attaching or launching a session goes through tmux and returns you to the dashboard on detach. It is Unix/tmux-only — on Windows or a non-TTY stream it prints a notice and exits.

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

## Carryovers from bash bridge

Most bash-era behavior was ported during cutover; the remaining subsystems were never ported and the bash scripts implementing them were deleted in Phase 4 (#35). They will return as Go features only if reimplemented from scratch:

- **Startup sync** (`git fetch && git pull --ff-only` before each launch).
- **Session-exit autosync** (commit + push uncommitted changes when a bridge session closes).
- **Presence-aware Telegram pages** (idle / elicitation / usage-limit notifications). Replaced operationally by Remote Control for live-session interaction. Telegram bootstrap via the slot-0 admin bot survives through `bridge-bot/` (Python).
- **`--install-admin-commands`** (installed claude slash commands like `/status`, `/issues`).

`bridge-bot/` (the Python Telegram spawner) is unaffected and remains in the tree.

## Editing

- Go code lives in `cmd/bridge/` (CLI) and `internal/` (libraries).
- Install locally: `make install-go`. Build only: `make build-go`.
- See [`CLAUDE.md`](CLAUDE.md) for commit conventions (Conventional Commits; tag releases as `vX.Y.Z`).
- Design docs: `docs/specs/2026-05-25-bridge-core-redesign-design.md`. Plans: `docs/plans/2026-05-26-bridge-core-redesign-plan-{a,b,b1,c}.md`.

## Testing

| Layer | Command | What it covers |
|---|---|---|
| Unit | `go test ./...` | Pure Go logic across all `internal/` and `cmd/bridge` packages. |
| Shim (bash) | `make test-shim` (requires `bats`) | The `bridge()` shell function's directive protocol: `cd:`, `exec:`, `noop`, `cancel`. |
| E2E | `go test -tags=e2e ./e2e/...` | The built binary against a fixture repos-root. Asserts stdout/exit-code/shim-directive contract per subcommand (`list`, `open`, `sessions`, `completion`, `sync`). |
| Cross-platform | GitHub Actions matrix | All of the above on `ubuntu-latest`, `macos-latest`, `windows-latest`. See `.github/workflows/go.yml`. |

Run a single test: `go test ./cmd/bridge -run TestXxx`. Verbose: add `-v`. To benchmark a change in the e2e binary build cost, e2e tests share one built binary per `go test` invocation via `sync.Once`.

## Setting up on a new machine

```bash
git clone https://github.com/freaxnx01/bridge ~/projects/repos/github/freaxnx01/public/bridge
cd ~/projects/repos/github/freaxnx01/public/bridge
make install        # binary + shim + meta-augmenter
bridge init --agent=claude --agent-args="--remote-control --dangerously-skip-permissions"
exec bash -l        # pick up the new lines
bridge doctor       # verify
```

`bridge init` writes to `~/.bashrc`:

- The shim source line so `bridge` becomes a shell function (needed for `open`/`sessions attach` to actually `cd`/`exec`).
- `source <(bridge completion bash)` for tab-completion.
- The meta-augmenter source line so `bridge nextgen<TAB>` expands to `ArchiveRestApiNextGen` (basename substring, plus description/topic keywords from `repo-meta.json`). Cobra's primary completion can't do this — its `compgen` filter drops non-prefix-matching suggestions.
- With `--agent`: `export BRIDGE_DEFAULT_AGENT=<name>` so `bridge <repo>` auto-launches the agent in tmux.
- With `--agent-args`: `export BRIDGE_DEFAULT_AGENT_ARGS="..."` — flags appended to the agent's argv at launch.
- With `--alias=br,brg`: a `complete -F __start_bridge <name>` line per alias (guarded by `declare -F`), so `br <repo><TAB>` inherits bridge's completion. Cobra registers completion under `bridge` only, so wrappers/aliases need this explicit binding.

Run `bridge init` again any time; it's idempotent. Source lines and alias lines are append-if-missing; export lines (`--agent`, `--agent-args`) are replace-in-place so changing your default agent is one command. Use `--dry-run` to preview, `--shell powershell` to print the Windows recipe instead. Skip flags you don't need — `bridge init` alone wires only the shim + completion sources.

`bridge doctor` checks: binary on PATH, shim files installed, rc lines present, `bash-completion` package available, shim loaded in current shell, repos root walkable, `repo-meta.json` cache, registered alias completions, and `BRIDGE_DEFAULT_AGENT` / `BRIDGE_DEFAULT_AGENT_ARGS`. Any `FAIL` exits non-zero.

## Auto-launching an agent on `bridge <repo>`

By default, `bridge <repo>` just `cd`s into the repo. Set two env vars (manually, or via `bridge init --agent=... --agent-args=...`) to restore the bash bridge's behavior of auto-launching the agent in tmux with your preferred flags:

```bash
export BRIDGE_DEFAULT_AGENT=claude
export BRIDGE_DEFAULT_AGENT_ARGS="--remote-control --dangerously-skip-permissions"
```

Both apply to `bridge <repo>`, `bridge open <repo>`, and the interactive picker. Explicit `--agent X` on the command line overrides `BRIDGE_DEFAULT_AGENT`; the `*_ARGS` env var is only appended when the agent comes from the env-var default (so launching `--agent code` doesn't get Claude's flags). Unset both and you're back to cd-only.

Known agents: `claude`, `copilot`, `opencode`, `code`. See `internal/agents/agents.go`.

### `exec` vs child launch (and the SSH "fall-through")

To launch the session, the bash shim normally `exec`s tmux — the terminal *becomes* the session, which is what you want locally. Over SSH that backfires: the session is your SSH entry shell, so exiting or detaching tmux tears down the connection and drops you back where you `ssh`'d from. The shim therefore runs the launch as a **child** when it detects an SSH session (`SSH_CONNECTION` is set), returning you to the remote shell afterward. Overrides:

| Env var | Effect |
|---|---|
| `BRIDGE_NO_EXEC=1` | Always run as a child (return to caller), even locally. |
| `BRIDGE_FORCE_EXEC=1` | Always `exec` (replace the shell), even over SSH. |

`BRIDGE_NO_EXEC` wins if both are set. PowerShell always runs the launch as a child (no `exec` exists), so these are bash-only.

### kitty / "missing or unsuitable terminal: xterm-kitty"

If the host lacks kitty's terminfo entry (common on Chromebook/Crostini or
fresh SSH targets), tmux would abort with `missing or unsuitable terminal:
xterm-kitty`. bridge auto-detects an unresolvable `$TERM` (via `infocmp`) and
launches tmux with `TERM=xterm-256color`, printing a one-line notice. To keep
full kitty terminfo, install it on the host
(`infocmp -x xterm-kitty | tic -x -`) or set `term xterm-256color` in
`kitty.conf`. To disable the fallback and see the raw error, export
`BRIDGE_NO_TERM_FALLBACK=1`.

## Windows

Cross-compile via `GOOS=windows GOARCH=amd64 go build ./cmd/bridge`. Install the `.exe` on PATH as `bridge.exe`, dot-source `shims/bridge-shim.ps1` from `$PROFILE`. Launcher uses Windows Terminal (`wt.exe new-tab`). No Windows CI; the binary builds clean but the runtime path is exercised manually.

Tab completion: run `bridge.exe init --shell powershell` and paste the printed lines into `$PROFILE` (or run it directly on the Windows host to have it write `$PROFILE` itself). The PowerShell side currently wires the cobra-generated completion only — no meta-augmenter yet.

## Known limitations

- Picker is local-only; remote-only entries can't be selected for clone yet ([#54](https://github.com/freaxnx01/bridge/issues/54)).
- No slot-count limit or displacement in the Go binary ([#56](https://github.com/freaxnx01/bridge/issues/56)).
- `last_used` is not populated in `open --json` output (MRU file has no per-entry timestamps).
- GitHub API `per_page=100`: owners with 100+ repos in a single visibility will be truncated.
- Forgejo owner is hardcoded to `freax` in path-inference.
