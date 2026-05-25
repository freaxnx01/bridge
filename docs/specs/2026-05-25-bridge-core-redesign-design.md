# bridge core redesign — design

**Date:** 2026-05-25
**Status:** approved (brainstorming complete; plan pending)
**Scope:** Core only. TUI/dashboard and web UI are separate, later specs.

## Goals

- Replace the ~3,600-line `bridge.sh` with a single Go binary that owns repo discovery, slot/session bookkeeping, presence, sync, and issue fetching.
- Restructure the CLI surface into discoverable subcommands while preserving the two muscle-memory hot paths (`bridge` and `bridge <name>`).
- Decompose `bridge --status`, which has become overloaded with too many concerns and too noisy output.
- Lay the foundation for a future Bubbletea TUI and (later) a local web UI by exposing the domain through a stable Go package (`internal/core`) — without committing to a daemon or RPC contract yet.
- Achieve Linux + Windows parity from one codebase.

## Non-goals

- TUI / dashboard implementation (separate spec).
- Web UI / long-running daemon / IPC contract (separate spec).
- Telegram bot port (separate spec).
- New features beyond parity, `--status` decomposition, and a new `bridge issues` read command.

## Architecture

### Binary + shell shim

A single Go binary `bridge`, cross-compiled for Linux and Windows. Stateless: each invocation reads and writes `~/.cache/bridge/` and exits.

Because a binary cannot change the parent shell's working directory, a small shell shim stays sourced from `~/.bashrc`:

```sh
bridge() {
    local directive
    directive=$(command bridge __preflight "$@") || return $?
    case "$directive" in
        cd:*)   cd "${directive#cd:}" ;;
        exec:*) exec ${directive#exec:} ;;
        noop)   : ;;
    esac
}
```

The shim is ≤20 lines. The Go binary decides what the parent shell must do via a directive printed on its preflight channel: `cd <path>`, `exec <cmd…>`, or `noop`. PowerShell gets the analogous `bridge.ps1`.

### Package layout

```
cmd/bridge/                  # cobra root + subcommand wiring; thin
internal/core/               # domain types + business logic
  repo.go                    #   discovery (walk ~/projects/repos/ for .envrc)
  slot.go                    #   slot registry + lifecycle
  session.go                 #   tmux/WT session inspection
  presence.go                #   away/back state
  mru.go                     #   most-recently-used ordering
internal/store/              # file-backed persistence; atomic writes; schema versioning; flock
internal/forge/              # github / gitlab / forgejo clients
  client.go                  #   interface
  github.go gitlab.go forgejo.go
  cache.go                   #   TTL'd repo + issue cache
internal/launcher/           # tmux on Linux, Windows Terminal on Windows
internal/agents/             # claude / copilot / opencode / code spawn
internal/shellbridge/        # __preflight directive protocol
```

`internal/core` is the API surface. The CLI in `cmd/bridge` is a thin presentation layer. Future TUI/web clients import `internal/core` directly; no duplication.

### Cross-platform

`internal/launcher` is the only OS-conditional package. Linux uses tmux; Windows uses Windows Terminal (`wt.exe`). One interface, two implementations. No `runtime.GOOS` checks scattered elsewhere.

## Data model

Five domain types in `internal/core`. Small, serializable, JSON-friendly so they can later be the contract for TUI/web clients.

```go
type Repo struct {
    Name          string
    Path          string
    Forge         string    // "github" | "gitlab" | "forgejo"
    Owner         string
    Visibility    string    // "public" | "private" | ""
    Topics        []string
    Desc          string
    DefaultBranch string    // forge-API-derived, cached
    RemoteURL     string    // from `git config --get remote.origin.url`, cached
    LastUsed     time.Time  // from MRU
}

type Slot struct {
    ID       string         // stable; used as tmux session name
    Repo     string         // Repo.Name
    Worktree string         // "" if not a worktree
    Agent    string         // "claude" | "copilot" | "opencode" | "code"
    Created  time.Time
}

type Session struct {
    SlotID   string
    State    string         // "attached" | "detached" | "code" | "dead"
    Age      time.Duration
    PID      int
    TmuxName string         // or WTTabID on Windows
}

type Presence struct {
    Mode      string                   // "auto" | "away" | "back"
    Overrides map[string]string        // slot ID → forced state
    UpdatedAt time.Time
}

type Issue struct {
    Forge   string
    Repo    string                     // "owner/name"
    Number  int
    Title   string
    URL     string
    Labels  []string
    Updated time.Time
}
```

### Persistence

State lives under `~/.cache/bridge/`:

```
mru                 newline-delimited paths (backward-compatible with bash format)
repo-meta.json      cached forge-derived repo metadata (Topics, Desc, DefaultBranch, RemoteURL)
remote.list         remote repo listings cache (unchanged)
issues.json         cached issues, forge-keyed, per-repo TTL
slots.json          slot registry
presence.json       presence mode + per-slot overrides
schema-version      integer; bumped on incompatible changes
bridge.log          JSON-lines log; rotated at 10 MB, keep 3
```

Each domain type owns exactly one file. Migrations are explicit functions (no magic). All writes use tmp-file + `os.Rename`. All reads tolerate partial/missing files (treat as empty). Concurrent writers (e.g. `watch` + interactive `open`) coordinate via `flock(2)` on Linux and `LockFileEx` on Windows, hidden behind `internal/store`.

### Discovery vs. cache split

`Repo.Name/Path/Forge/Owner/Visibility` are derived from the filesystem (`.envrc` layout under `~/projects/repos/`) on every invocation — cheap walk, no caching. `Topics/Desc/DefaultBranch/RemoteURL` come from the forge API or local git config and are cached in `repo-meta.json`. `LastUsed` comes from `mru`. This separation prevents stale caches from lying about basics like which repos exist.

### Issue cache

Forge-keyed, per-repo TTL (default 10 minutes). `bridge issues --refresh` forces refetch. `bridge list` does not fetch issues unless asked (`--with-issues`). Designed so a future TUI can poll `bridge issues --json` cheaply.

## CLI surface

Hot path stays positional; everything else becomes a verb.

```
bridge                          picker (MRU on top, fzf TUI)
bridge <name>                   open repo (case-insensitive; keyword fallback)
bridge open <name>              explicit form (scriptable; bypasses fuzzy)
bridge list                     local repos
bridge list -r                  + remote listings (streaming)
bridge list --refresh           force remote cache refresh
bridge rm <name>                delete local repo

bridge sessions                 live agent sessions
bridge sessions attach <name>   attach to a session
bridge slots                    slot registry + state
bridge presence                 read presence
bridge presence away|back       set presence
bridge sync                     sync state summary
bridge sync now                 force sync now
bridge sync --auto              long-running auto-sync (replaces bridge-autosync.sh)
bridge status                   slim composed summary (~10 lines)

bridge issues                   open issues across forges
bridge watch                    long-running watcher (replaces bridge-watcher.sh)

bridge tui                      reserved for the dashboard spec

bridge <any> --json             machine-readable output (read commands only)
bridge --help / <verb> --help
bridge --version
```

Open-by-name flags (apply to `bridge <name>` and `bridge open`):

```
-w, --worktree <name>           pass-through to claude
    --remote-control, --rc      pass-through
    --agent <claude|copilot|opencode|code>
```

### Removed / renamed

| Old (bash) | New (Go) |
|---|---|
| `bridge -r` | `bridge list -r` |
| `bridge --refresh` | `bridge list --refresh` |
| `bridge -D <name>` | `bridge rm <name>` |
| `bridge away` | `bridge presence away` |
| `bridge --status` | split into `bridge status` (slim) + `sessions` / `slots` / `presence` / `sync` |

### `--status` decomposition

The single biggest UX pain point. `bridge --status` today mixes sessions, slots, presence, sync state, version, hook activity, and unpushed warnings into one dense block. The new shape:

- `bridge status` — slim summary (~10 lines): N sessions, presence mode, sync state, unpushed count. Composes the focused verbs.
- `bridge sessions` — live agent sessions, columns: slot, state, age, repo, worktree.
- `bridge slots` — registry view (includes dead slots), columns: id, repo, agent, created.
- `bridge presence` — current mode + per-slot overrides.
- `bridge sync` — autosync state, queue, last run, unpushed branches.

Each supports `--json`.

## Adjacent processes

| Today | Plan |
|---|---|
| `bridge-watcher.sh` | Ported to `bridge watch`. Foreground by default; `--daemonize` writes PID to `~/.cache/bridge/watch.pid`. |
| `bridge-autosync.sh` | Ported to `bridge sync --auto` (long-running). `bridge sync now` is the one-shot. |
| `bridge-unpushed-warn.sh` | Folded into `bridge sync` (it's a sync-state query). Standalone script removed in Phase 4. |
| `bridge-bot/` (Telegram) | Out of scope. Bot reads `bridge presence --json` for the data it needs. Own spec later. |
| `bridge-hooks/` | Unchanged. User hook scripts that call `bridge` subcommands. |
| `bridge-admin-commands` | Unchanged. |

### Long-running processes & the stateless model

"Stateless" applies to *invocations* — each `bridge list` is a fresh process. Long-running processes (`watch`, `sync --auto`) are allowed; they persist their work through the same `~/.cache/bridge/` files everyone else reads. **Filesystem-as-bus** — no sockets, no IPC, no JSON-RPC. `flock` keeps writers honest.

### Process control

`bridge watch --daemonize` writes a PID file and detaches. `bridge watch --status` reports running/stopped. `bridge watch --stop` kills. Same shape for `sync --auto`. No systemd unit shipped.

## Error handling

| Category | Example | Policy |
|---|---|---|
| User input | unknown repo, bad flag | Exit 2, error to stderr, no stack trace, `did you mean X?` |
| Filesystem (read) | `mru` missing/corrupt | Treat as empty, log at `-v`, continue |
| Filesystem (write) | `~/.cache/bridge/` unwritable | Exit 1, clear error pointing at path |
| Forge API | GitHub 503, token missing | Degrade: return cached data, mark stale in JSON, never block hot path |
| Subprocess | tmux/wt.exe missing | Exit 1 with install hint; detect early in launcher |
| Cache schema mismatch | older bash format | Read-compat shim during cutover; on failure, rebuild from FS and warn once |

**Hot path is never blocked by network.** `bridge`, `bridge <name>`, `bridge list` (without `-r`) work fully offline. Only `list -r`, `issues`, and explicit refresh commands hit the network.

### Exit codes

```
0   success
1   internal / filesystem / subprocess failure
2   user input error
3   network/forge error with no cache fallback
```

### Logging

`log/slog`-based. Defaults to silent. `-v` / `-vv` to stderr (human text). File log at `~/.cache/bridge/bridge.log` (JSON lines, rotated at 10 MB, keep 3) — used by long-running processes by default; foreground mirrors to stderr.

### `--json` contract

Every read command supports `--json`. Errors in `--json` mode emit a single line on stderr:

```json
{"error": "repo not found", "code": 2}
```

stdout stays parseable. The JSON shape per command will be documented as a small schema doc alongside this spec when the implementation lands.

### Atomicity invariants (`internal/store`)

- Writes: tmp file in same directory + `os.Rename`.
- Reads: tolerate partial file (treat as missing, never crash).
- Concurrency: `flock(2)` (Linux) / `LockFileEx` (Windows), abstracted by `internal/store`.
- Schema bump: backup `<file>.bak-<version>` before migration.

## Testing

- **Unit tests** (`go test`): `internal/core`, `internal/store`, `internal/forge` (httptest mock servers), `internal/shellbridge` directive parsing. Table-driven.
- **CLI integration tests**: golden-file approach. Each subcommand has `testdata/` with input state + expected stdout/stderr/exit. Binary invoked via `exec.Command`. Refresh with `go test ./... -update`.
- **Shell-shim tests**: bats (existing `tests/` directory). Verify directive protocol actually moves the parent shell.
- **Launcher tests**: Linux launcher in CI with a real tmux. Windows launcher manual (no WT in CI). Build tags isolate.
- **Soft-cutover regression**: existing bats suite keeps running against the bash shim during overlap. New Go tests run in parallel. CI gates on both.

Out of CI scope: TUI rendering, real forge API calls, full tmux/WT lifecycle.

## Migration & rollout

### Phase 0 — repo prep

- `go.mod` at repo root (separate from `prototypes/dashboard-tui/go.mod`).
- Create `cmd/bridge/` and `internal/` skeleton.
- CI: Go build + test jobs alongside existing bats jobs.
- `bridge.sh` untouched.

### Phase 1 — Go binary ships, bash is primary

- Implement `internal/core`, `internal/store` with read-compat for existing `~/.cache/bridge/` files.
- Implement read-only subcommands: `list`, `sessions`, `slots`, `presence`, `sync`, `status`, `issues`.
- Ship binary as `bridge-go` (or similar) for A/B testing against bash.
- No shell-shim changes yet.

### Phase 2 — interactive paths + launcher

- Implement `open`, `rm`, `presence away|back`, `sync now`, `sync --auto`, `watch`.
- Implement `internal/launcher` (Linux tmux first; Windows WT next).
- Implement the directive protocol; ship the new ≤20-line shim as `bridge-shim.sh` (and `bridge-shim.ps1` for Windows).

### Phase 3 — cutover

- User flips `~/.bashrc` source line from old `bridge.sh` to new `bridge-shim.sh`.
- Shim invokes the Go binary as `bridge`.
- Legacy flags (`-r`, `-D`, `--refresh`, `away` positional) are **silently forwarded** to new verbs inside the binary. No deprecation hints.
- Old `bridge.sh`, `bridge-watcher.sh`, `bridge-autosync.sh`, `bridge-unpushed-warn.sh` remain in the repo but are no longer sourced/run.

### Phase 4 — cleanup (later, separate PR)

- Delete old bash scripts.
- Remove read-compat code paths if cache formats changed.
- Tag `v2.0.0`.

### Versioning

- Go binary reports `bridge version` (Git SHA + build date).
- `_BRIDGE_VERSION` in `bridge.sh` sunsets when `bridge.sh` does. The CLAUDE.md rule about bumping it on edits applies until Phase 3 cutover, then is retired.
- Tags follow semver from `v2.0.0` on the Go side. First Go release tagged `v2.0.0-go.0` during Phase 2.

## Open items (for the implementation plan, not this spec)

- Exact column layouts and human-readable formatting for each `--status` successor verb.
- `--json` schema doc per command.
- Bubbletea TUI is a separate spec; this design only reserves `bridge tui`.
- Telegram bot port is a separate spec.

## References

- PoC: `prototypes/dashboard-tui/main.go` (commit `864d248`) — shapes the future TUI's data needs (Repos / Issues / Sessions / command palette).
- Prior specs: `docs/specs/2026-05-19-bridge-status-unified-overview-design.md` — the work this redesign supersedes.
- `ideas.md` — backlog items (dashboard TUI, Windows variant, directory mode, branches vs. worktrees) feeding future specs.
