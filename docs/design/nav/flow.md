# `bridge nav` — Flow & State Map (UI Phase 2)

Approved 2026-05-31. Maps the approved [wireframe](wireframe.md) onto a single
Bubble Tea program. For a TUI the "component map" is the model's states + the
`tea.Msg`/`tea.Cmd` graph (per the Go stack overlay).

## Diagram 1a — User journey: Screen 1 (picker)

```mermaid
flowchart TD
  A([bridge nav]) --> TTY{interactive TTY?}
  TTY -- no --> FB[degrade: non-interactive / launch-and-replace]
  TTY -- yes --> S1[Screen 1: picker<br/>local repos + active sessions shown<br/>remote loads async]
  S1 --> K{key}
  K -- type / ↓ into list --> S1
  K -- q --> Q([quit])
  K -- ⏎ session row --> AT[tea.ExecProcess: tmux attach]
  AT -- detach --> S1
  K -- ⏎ local repo --> S2GO[/go to Screen 2/]
  K -- ⏎ remote row --> CL[clone repo<br/>progress streams]
  CL -- ok --> S2GO
  CL -- fail --> ERR1[error notice · stay on S1] --> S1
  RM[[remoteMsg arrives]] -- ok --> S1
  RM -- err --> WARN[warn: cached rows shown] --> S1
```

## Diagram 1b — User journey: Screen 2 (dashboard)

```mermaid
flowchart TD
  B[Screen 2: repo dashboard<br/>sessions+worktrees · git-dirty async] --> K2{key}
  K2 -- esc --> S1B[/back to Screen 1/]
  K2 -- q --> Q2([quit])
  K2 -- ⏎ active session --> AT2[tea.ExecProcess: tmux attach]
  K2 -- ⏎ no-session worktree --> LA[launch default agent<br/>tea.ExecProcess attach]
  AT2 -- detach --> RFR[refresh rows + git-dirty]
  LA -- detach --> RFR
  RFR --> B
  K2 -- n / + row --> NW[New-worktree modal<br/>name input]
  NW -- esc --> B
  NW -- ⏎ --> CR{git worktree add}
  CR -- name collides --> IE[inline error · stay in modal] --> NW
  CR -- ok --> LA
  GD[[dirtyMsg arrives]] --> B
```

## Diagram 2 — State & message map (single `tea.Program`)

```mermaid
flowchart TD
  subgraph M["nav.Model"]
    SCR{{screen: picker · dash}}
    PST[picker state: filter, focus filter/list,<br/>sessions, localRepos, remoteRepos, remoteErr]
    DST[dash state: repo, rows session+worktree,<br/>dirty map, modal name+err]
  end

  INIT([Init]) --> C1[loadLocalRepos] --> Mr>reposMsg] --> PST
  INIT --> C2[loadSessions] --> Ms>sessionsMsg] --> PST
  INIT --> C3[loadRemote · async] --> Mrr>remoteMsg / remoteErrMsg] --> PST

  PST -- select repo / clone ok --> DST
  DST --> C4[loadDashRows] --> Md>dashRowsMsg] --> DST
  DST --> C5[gitDirty · async] --> Md2>dirtyMsg] --> DST
  DST -- attach / launch / create --> EX[tea.ExecProcess argv] --> Me>execDoneMsg] --> C4
```

### Services (packages) each Cmd calls — props down, events up via `Msg`

| Cmd | Calls (reused, untouched) |
|---|---|
| `loadLocalRepos` | `core.DiscoverRepos` |
| `loadSessions` | `core.LiveSessions` + `core.LoadSlots` |
| `loadRemote` | `forge.ReadRepoCache` (then optional fetch) |
| clone (on remote select) | `cloneRemoteRepo` (`forge` + `git`) |
| `loadDashRows` | `core` sessions/slots + `git worktree list` |
| `gitDirty` | `git -C <wt> status --porcelain` / `rev-list` |
| create worktree | `worktree.Resolve` / `git worktree add` |
| attach / launch | `internal/launcher` argv → `tea.ExecProcess` |

## Screen inventory check

- **Clone progress** — transient streaming state on `↓ remote` select (git clone
  output, then → Screen 2); failure returns to Screen 1. Not a separate screen.
- **Non-TTY / SSH-child fallback** — not a screen; `bridge nav` detects no usable
  TTY and prints a notice + degrades.
- **No destructive actions in v1** — no worktree/session deletion, so no
  confirmation dialog yet.

## Scope decision (recorded)

Git information is split into two layers; **only Layer 1 is in this spec**:

- **Layer 1 (v1, this spec):** per-row git-dirty indicator on Screen 2.
- **Layer 2 (deferred, separate cycle):** the btop dashboard panels — Branches ·
  Recent commits · Git status (full) · Open issues · forge statusbar. The v1
  layout reserves footer space for them so they are additive later.
