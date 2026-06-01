# `bridge nav` — Layer-2 Dashboard Panels — Design Spec (v1)

Status: **approved design, pending plan** · Date: 2026-06-01

Layer 2 of `bridge nav`: enrich the Screen-2 dashboard with three **read-only**
detail panels bound to the highlighted worktree — **Branches**, **Recent
commits**, and **Git status** — laid out master-detail beside the existing
Sessions & Worktrees list.

This is the follow-up cycle the v1 nav spec deferred (see
[`2026-05-31-bridge-nav-design.md`](2026-05-31-bridge-nav-design.md) — *Non-goals*
and *Open follow-ups*). The current dashboard already reserves footer space for
these sections.

Companion v1 artifacts: spec above · wireframe
[`docs/design/nav/wireframe.md`](../design/nav/wireframe.md).

## Goal / success criterion

On the per-repo dashboard, as the user moves the cursor through the worktree
list, three panels on the right reflect the **highlighted worktree**:

1. **Recent commits** — that worktree's HEAD-branch log;
2. **Git status** — that checkout's changed-file list (complements, does not
   replace, the existing per-row `●N ↑N` dirty count);
3. **Branches** — the repo's branches with an **across-worktrees overview**:
   which branch each worktree occupies (`+`) and the selected worktree's current
   branch (`*`).

Panels are **read-only** (focus never leaves the worktree list), load lazily
behind a spinner, and are cached per worktree. On a narrow terminal the
dashboard degrades to today's exact list-only view.

`bridge`, `bridge tui`, the nav **picker** screen, and the nav launch/attach/
create-worktree flows are **untouched**.

## Non-goals (this cycle)

- **Forge panels** — Open issues and forge statusbar (need async `gh`/forge API
  with auth + rate-limit + cache handling). They remain deferred; the footer
  shrinks to `(later: Open issues · forge statusbar)`.
- **Panel focus / scroll** — panels are not focusable; overflow is truncated
  with `… +N more`, not scrolled.
- **Panel actions** — no branch checkout, commit show, or file open from a panel.
- **Picker-screen changes**, slot/MRU/on-disk schema changes, Windows
  interactive parity.

## Architecture

All changes are additive to **`internal/nav`**. `Update` stays a pure function
of `(model, msg)`; no I/O in `View`.

### Binding model

- **Contextual** (follow the highlighted worktree's checkout/branch): Recent
  commits, Git status.
- **Repo overview** (one read, rendered against the selected worktree for the
  `*` marker): Branches — the panel that carries the cross-worktree overview.

The selected worktree is `m.dashRows[m.dashSel]` when `m.dashSel <
len(m.dashRows)`; on the trailing `+ Create new worktree…` row there is no
selected worktree (`selectedWorktreePath()` returns `""`).

### Model additions

```go
type Model struct {
    // … existing fields unchanged …
    details map[string]*worktreeDetails // keyed by worktree path
}

// worktreeDetails is the lazily-loaded, cached panel data for one worktree.
type worktreeDetails struct {
    branches      []branchInfo
    commits       []commitInfo
    status        []statusFile
    branchesState loadState
    commitsState  loadState
    statusState   loadState
}
```

New row/value types (in `types.go`):

```go
// branchInfo is one row of the Branches panel. current marks the selected
// worktree's HEAD ("*"); inWorktree marks a branch checked out in some worktree
// ("+"), the across-worktrees overview signal.
type branchInfo struct {
    name       string
    current    bool
    inWorktree bool
}

// commitInfo is one Recent-commits row: short SHA + subject.
type commitInfo struct {
    sha     string
    subject string
}

// statusFile is one Git-status row: the two-char porcelain XY code + path.
type statusFile struct {
    code string
    path string
}
```

New messages (each carries the worktree `path` so a stale reply for an evicted
path is ignored):

```go
type branchesMsg struct { path string; branches []branchInfo; err error }
type commitsMsg  struct { path string; commits  []commitInfo;  err error }
type statusMsg   struct { path string; files    []statusFile;  err error }
```

### Async data flow (lazy load + per-worktree cache)

| Cmd | Underlying call | Parser (pure, tested) | Msg |
|---|---|---|---|
| `gitBranchesCmd(path)` | `git -C <path> branch --sort=-committerdate` | `parseBranches` | `branchesMsg` |
| `gitCommitsCmd(path)` | `git -C <path> log --format=%h%x00%s -n <N>` | `parseCommits` | `commitsMsg` |
| `gitStatusCmd(path)` | `git -C <path> status --porcelain` | `parseStatusFiles` | `statusMsg` |

- `parseBranches` reads the leading marker `git branch` emits per line: `*
  `=current, `+ `=checked out in another worktree, `  `=plain. Sorted as the
  command returns (most-recent committerdate first); current floats to the top
  in the view.
- `N` (commit count) is sized to the panel's body height at render time but the
  Cmd uses a safe fixed cap (e.g. `-n 20`); the view truncates to fit and
  appends `… +M more`.
- `parseStatusFiles` splits porcelain `XY <path>` lines (handles `??` untracked
  and `R  old -> new` rename forms — render the new path).

**Trigger:** whenever `dashSel` changes to a worktree row whose path is **not**
in `m.details`, a reducer helper `ensureDetailsCmd()` creates the cache entry
with all three states `loadPending` and returns
`tea.Batch(gitBranchesCmd, gitCommitsCmd, gitStatusCmd)`. A path already present
(any state) does not refire — cache hit.

**Fill:** `branchesMsg`/`commitsMsg`/`statusMsg` write their slice into
`m.details[path]` and flip that panel's state to `loadOK` (or `loadErr` on
`err`). If `path` is no longer in the map (evicted), the msg is dropped.

**Invalidation:** `m.details` is cleared whenever `loadDashRowsCmd` is issued —
i.e. on dashboard entry (`enterDash`) and on detach-return refresh
(`execDoneMsg` → `loadDashRowsCmd`). When the resulting `dashRowsMsg` lands, the
reducer re-issues `ensureDetailsCmd()` for the then-current selection so the
visible worktree's panels reload fresh.

### Reducer wiring (`update.go`)

- `updateDash` navigation cases (`up/k`, `down/j`, `g`, `G`, `PgUp`, `PgDn`)
  already mutate `dashSel`; they funnel through a single tail that returns
  `m, m.ensureDetailsCmd()` instead of `m, nil`, so any selection change kicks a
  (deduped) load. Launch/modal/`esc`/`q` paths are unchanged.
- `dashRowsMsg`: clear+rebuild rows (existing), then return `ensureDetailsCmd()`
  for the current selection (in addition to the existing dirty Cmds).
- New top-level `case branchesMsg/commitsMsg/statusMsg`: fill cache as above.

### View (`view.go`)

`viewDash` becomes master-detail:

- Compute usable width. **If width < ~90 cols** (threshold constant), render the
  **current single-column dashboard unchanged** (list-only fallback — no
  regression on small terminals).
- Otherwise split into two columns with `lipgloss.JoinHorizontal(lipgloss.Top,
  left, right)`:
  - **Left:** the existing Sessions & Worktrees panel at a bounded width
    (≈ the larger of 40% and a min column width).
  - **Right:** `lipgloss.JoinVertical` of three `panel()`s — Branches, Recent
    commits, Git status — for `selectedWorktreePath()`, height divided among
    them.
- Each panel body dispatches on its `loadState`: `loadPending`→spinner,
  `loadErr`→muted `unavailable`, `loadOK`→rendered rows truncated to the body
  height with `… +N more`.
- On the `+ Create new worktree…` row (`selectedWorktreePath() == ""`), the
  right column shows a single muted hint (`select a worktree to see details`).
- Footer changes from
  `(later: Branches · Recent commits · Git status · Open issues · forge statusbar)`
  to `(later: Open issues · forge statusbar)`.

## Error / empty / loading states

- **Loading:** each panel shows the shared spinner until its msg lands.
- **Error** (`git` failed): panel body shows muted `unavailable`; the other
  panels and the list are unaffected.
- **Empty:** Branches `(only this worktree)`, Recent commits `(no commits)`,
  Git status `✓ clean`.
- **Narrow terminal:** right column omitted; dashboard identical to today.
- **No selected worktree** (`+` row): right column shows the select-a-worktree
  hint.

## Navigation & keys

No new keys. The worktree list keeps focus and all existing bindings
(`↑↓`/`j k`/`g G`/`PgUp PgDn`/`n`/`⏎`/`esc`/`q`). Cursor movement is the only
trigger for panel refresh.

## Touch list (files)

Modified (additive only), all under `internal/nav`:

- `types.go` — `branchInfo`, `commitInfo`, `statusFile`, `worktreeDetails`,
  `branchesMsg`, `commitsMsg`, `statusMsg`; `details` field on `Model`.
- `format.go` (+ `format_test.go`) — `parseBranches`, `parseCommits`,
  `parseStatusFiles`.
- `data.go` — `gitBranchesCmd`, `gitCommitsCmd`, `gitStatusCmd`.
- `model.go` — initialise `details` (map) in `initialModel`.
- `update.go` (+ `update_test.go`) — `ensureDetailsCmd`, `selectedWorktreePath`,
  navigation tail wiring, cache clear on `loadDashRowsCmd`, three new msg cases.
- `view.go` (+ `view_test.go`) — master-detail `viewDash`, per-panel renderers,
  narrow fallback, footer text.

Untouched: nav picker, launch/attach/modal flow, `internal/tui`, bare `bridge`,
`cmd/bridge/nav.go` (no flag/config change), every on-disk schema.

Docs: `README.md` (nav paragraph — mention the detail panels) and
`CHANGELOG.md` `[Unreleased] → Added`.

## Testing (TDD, per Go stack)

- **Pure parsers** (`format_test.go`, table-driven): `parseBranches`
  (`*` current / `+` in-worktree / plain, ordering), `parseCommits` (NUL split,
  sha + subject, empty), `parseStatusFiles` (`M`, `??`, rename `->`).
- **Reducer** (`update_test.go`): cursor move onto a fresh path returns a
  non-nil (batched) Cmd and seeds a `loadPending` cache entry; a second move
  back is a cache hit (no entry recreation); `branchesMsg/commitsMsg/statusMsg`
  fill the cache and flip state; a msg for an evicted path is dropped;
  `dashRowsMsg`/detach clears `details`.
- **View** (`view_test.go`): wide frame contains `Branches`, `Recent commits`,
  `Git status`; a narrow frame (e.g. width 80) contains none of them and matches
  the list-only shape.
- `nav --once` still renders a frame.
- Gates: `gofmt -l .` empty · `go vet ./...` · `golangci-lint run` ·
  `go test -race ./...`.

## Open follow-ups (not this spec)

- Forge panels: Open issues + forge statusbar (own cycle — async `gh`/forge API,
  cache, auth/rate-limit states).
- Panel focus + scroll for long lists.
- Panel actions (checkout branch, show commit, open file) + their confirm/error
  states.
