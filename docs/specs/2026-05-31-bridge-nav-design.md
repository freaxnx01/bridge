# `bridge nav` — Design Spec (v1)

Status: **approved design, pending plan** · Date: 2026-05-31

A new subcommand, `bridge nav`: a two-screen interactive terminal navigator that
picks a repo, then drops you on a per-repo launchpad to resume an existing agent
session/worktree or create a new one — and **returns to that launchpad when you
detach**, instead of exiting.

Companion artifacts (UI workflow phases 1–2):

- Wireframe: [`docs/design/nav/wireframe.md`](../design/nav/wireframe.md)
- Flow & state map: [`docs/design/nav/flow.md`](../design/nav/flow.md)

## Goal / success criterion

Running `bridge nav` in a terminal lets the user, **without leaving the program
across attach/detach cycles**:

1. pick a repo (local immediately; remote rows load asynchronously and are
   clone-on-select);
2. see that repo's active tmux sessions and worktrees, with a live-loaded
   git-dirty indicator;
3. attach to a session, launch the default agent in a session-less worktree, or
   create a new worktree (which is then launched + attached);
4. on detach, land back on the dashboard with refreshed state.

`bridge`, `bridge tui`, and every other existing command are **untouched**.

## Non-goals (v1)

- The richer btop dashboard panels — Branches, Recent commits, full Git status,
  Open issues, forge statusbar (**Layer 2**, a separate later cycle). The v1
  layout only reserves footer space for them.
- Worktree/session **deletion** (and therefore any confirmation dialog).
- Windows/PowerShell interactive parity for `nav`. tmux/`tea.ExecProcess` attach
  is Unix-only; the Windows build degrades gracefully (see *Cross-platform*).
- Changing the slot registry, MRU, or any on-disk schema.

## Architecture

A new package **`internal/nav`** holding one Bubble Tea program. The cobra
command `cmd/bridge/nav.go` wires `RunE` → `nav.Run(...)`, passing the same
roots/paths the `tui` command already threads (`reposRoot()`, `cacheRoot()`).

### Model

```
type screen int   // screenPicker, screenDash

type Model struct {
    screen        screen
    width, height int
    spinner       spinner.Model   // bubbles/spinner, shared

    // picker state
    filter        textinput.Model
    pickerFocus   focus           // focusFilter | focusList
    sessions      []sessionRow    // global active sessions (top section)
    localRepos    []repoRow
    remoteRepos   []repoRow
    remoteState   loadState       // loading | ok | err
    pickerSel     int

    // dash state
    repo          core.Repo
    dashRows      []dashRow       // session+worktree, sorted by last-accessed desc
    dirty         map[string]dirtyInfo  // keyed by worktree path
    dashSel       int
    modal         *newWorktreeModal     // nil unless open

    status        string
    err           error
}
```

`Update(msg, model) -> (model, cmd)` stays a pure function of `(model, msg)` so
it is unit-testable without a terminal (Go-stack TUI rule). No I/O in `View`.

### Async data flow (`tea.Cmd` → `tea.Msg`)

| Cmd | Reused call (untouched) | Msg |
|---|---|---|
| `loadLocalRepos` | `core.DiscoverRepos` | `reposMsg` |
| `loadSessions` | `core.LiveSessions` + `core.LoadSlots` | `sessionsMsg` |
| `loadRemote` (async) | `forge.ReadRepoCache` | `remoteMsg` / `remoteErrMsg` |
| `loadDashRows` | `core` sessions/slots + `git worktree list --porcelain` | `dashRowsMsg` |
| `gitDirty` (async) | `git -C <wt> status --porcelain` + `rev-list --count` | `dirtyMsg` |
| `createWorktree` | `worktree.Resolve(runner, repoPath, name)` (creates `worktree-<name>` under `.worktrees/<name>`; returns `created` bool) | `wtCreatedMsg` / `wtErrMsg` |

Local repos + sessions render on first frame; remote rows and git-dirty stream
in behind the shared spinner. `remoteErrMsg` keeps cached rows and shows a warn
notice — it never blocks the picker.

### Attach / launch / return — the persistent-dashboard mechanism

Attaching a session, launching the default agent in a worktree, and the final
step of create-worktree all build argv via `internal/launcher` and run it with
**`tea.ExecProcess(cmd, func(err) tea.Msg { return execDoneMsg{err} })`**. Bubble
Tea releases the terminal to `tmux attach`/launch; on detach the *same* program
resumes and `execDoneMsg` triggers `loadDashRows` + `gitDirty` again — the
dashboard returns "like we left it", refreshed.

Launcher selection mirrors the existing open path: `LaunchArgv` normally, the
nested `switch-client` variant when `$TMUX` is set; `AttachArgv` for an existing
session.

### Last-accessed

Sessions are ordered **descending by last activity**, read from tmux
`#{session_activity}` (a new field added to the `LiveSessions` format string;
purely additive — see *Touch list*). Rendered compact, ≤2 units: `1d 2h`,
`3h 12m`, `4m`.

### Create-worktree

`n` (or selecting the `+` row) opens an inline modal with a `textinput`. On
`⏎`: `worktree.Resolve(runner, repoPath, name)` (the existing exec-backed git
`Runner`) creates `worktree-<name>` at `<repo>/.worktrees/<name>`, then the
launch+attach path runs. Name collision
(existing worktree/branch) surfaces as `wtErrMsg` shown inline under the input;
the input stays focused for a retry. `esc` closes the modal.

### Remote clone-on-select

Selecting a `↓ remote` row calls the existing `cloneRemoteRepo(ref)` (progress
streams to the terminal), then transitions to Screen 2 with the freshly cloned
`core.Repo`. Clone failure shows a notice and stays on Screen 1.

## Navigation & keys

| Screen | Key | Action |
|---|---|---|
| Picker | type | edit filter |
| Picker | ↓ (from filter) | move focus into the list |
| Picker | ↑ / ↓ (in list) | move selection |
| Picker | ⏎ on session row | attach (detach → back to picker) |
| Picker | ⏎ on local repo | → dashboard |
| Picker | ⏎ on remote row | clone → dashboard |
| Picker | r | refresh remote |
| Picker | q | quit |
| Dash | ↑ / ↓ | move selection |
| Dash | ⏎ on session | attach (detach → refresh dash) |
| Dash | ⏎ on session-less worktree | launch default agent (detach → refresh) |
| Dash | n / ⏎ on `+` row | open new-worktree modal |
| Dash | esc | back to picker |
| Dash | q | quit |

## Error / empty / loading states

- **Remote loading:** `Repos` panel title shows the spinner + `loading remote…`;
  local rows already interactive.
- **Remote error:** title shows `remote unavailable (cached rows shown)` (warn);
  cached rows remain selectable.
- **No local repos:** picker body shows a hint; remote rows still arrive.
- **No sessions (picker):** the `Active sessions` panel is hidden.
- **git-dirty pending:** dashboard rows render; dirty column shows the spinner
  until `dirtyMsg`.
- **Worktree-create collision:** inline error under the modal input; retry.
- **Non-TTY / SSH-child:** `bridge nav` detects no usable interactive TTY (same
  detection the launch path already uses) and prints a one-line notice on stderr
  + exits non-interactively rather than starting the program.

## Cross-platform

- Unix: full behaviour via tmux + `tea.ExecProcess`.
- Windows: `bridge nav` builds, but interactive attach/return relies on tmux. The
  command prints a "not supported on this platform" notice and exits cleanly. No
  Windows CI; this path is asserted by a build-tag-guarded stub + manual note in
  the PR (per `AGENT-NOTES.md` cross-shell rule).

## Touch list (files)

New:

- `internal/nav/` — `nav.go` (model/Update/View/Run), `data.go` (Cmds), plus
  `*_test.go` (table-driven `Update` tests; `--once` smoke render like `tui`).
- `cmd/bridge/nav.go` — cobra wiring + `--once` flag.
- `cmd/bridge/nav_test.go`.

Modified (additive only):

- `internal/core/session.go` — add `#{session_activity}` to the `LiveSessions`
  format string and a `LastActivity time.Time` field on `Session`; extend
  `ParseTmuxList` (now 4 fields). Existing callers ignore the new field. Update
  `core/session_test.go` fixtures to the 4-field shape.
- `README.md` CLI surface + `CHANGELOG.md` `[Unreleased]`.

Untouched: `internal/tui`, bare `bridge`, every other command, all on-disk
schemas, the shim/`__preflight` protocol.

## Testing (TDD, per Go stack)

- `Update` is pure → table-driven tests drive key/msg sequences and assert state
  transitions (filter→list focus, screen switch, modal open/collision, sort
  order) with **no terminal** and hand-rolled fakes for the data Cmds.
- `ParseTmuxList` gets a 4-field case incl. `session_activity` and the
  last-accessed formatter (`1d 2h`, `3h 12m`, `4m`).
- `nav --once` renders one fixed-size frame to stdout (CI smoke path, mirrors
  `tui --once`), exercised in e2e.
- Gates: `gofmt -l .` empty · `go vet ./...` · `golangci-lint run` ·
  `go test -race ./...`.

## Open follow-ups (not this spec)

- Layer-2 btop panels (own brainstorm cycle).
- Windows interactive parity (if ever — likely Windows Terminal tabs, not tmux).
- Worktree/session deletion + confirmation dialog.
