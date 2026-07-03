# nav base-checkout (main) row (#182)

**Date:** 2026-07-01
**Area:** `internal/nav` (dashboard row list) + `internal/worktree` (primary-branch lookup)
**Status:** Approved (design)

## Problem

`bridge nav` has no way to open a session on the **main checkout** without going
through a worktree. This is deliberate at two levels, and both block the
main-checkout use case:

1. **The dashboard never lists main.** `worktree.List`
   (`internal/worktree/worktree.go:32-49`) explicitly skips the primary working
   tree — "nav lists isolated worktrees, not the main checkout". The per-repo
   dashboard therefore shows only isolated worktrees plus a "+ Create new
   worktree…" row.
2. **Naming a worktree "main" doesn't help.** `worktree.Resolve`
   (`internal/worktree/worktree.go:55-88`) refuses to hand back the repo root and
   instead creates a dedicated `.worktrees/main` on branch `worktree-main`.

Every session-launch path in nav (`launchRow`, `launchIssue`, the `n` modal)
goes through worktree creation/resolution. The only bare-attach path is the
picker's Active-sessions panel, which only re-attaches already-running sessions —
it can't start a fresh session on the repo root.

Issue **#182** asks for a **base-checkout (main) row** pinned first on the
dashboard that opens/attaches a session in the repo root, bypassing worktree
`Resolve`, and that shares the same tmux session as the shell `bridge open <repo>`
(no `-w`).

## Goal (success criterion)

Every per-repo dashboard shows a base-checkout row as its **first** row. Pressing
`enter` on it launches (or attaches) a session in `repo.Path` with the bare
`<repo>` slot id — the same session `bridge open <repo>` uses — without creating
or resolving a worktree.

## Decisions (from the issue)

1. **A pinned base row, not a new keybinding.** The base row is prepended to the
   dashboard worktree list and reached with existing navigation + `enter`. No new
   key is introduced.
2. **Bypass `worktree.Resolve`.** The base row already fits the existing launch
   path: `launchPlan` builds the slot as `core.SlotID(repo.Name, row.worktree)`
   and launches in `row.path`. A base row with `worktree == ""` and
   `path == repo.Path` yields the bare `<repo>` slot and launches in the repo
   root — no `Resolve` call, no new launch path.
3. **Same session as `bridge open <repo>`.** The shell open path registers a slot
   with `Worktree: ""` and id `core.SlotID(repo.Name, "")` (== `<repo>`); see
   `cmd/bridge/preflight.go:275-287`. The base row uses the identical slot id, so
   attaching from either path lands in the same tmux session.
4. **Label from the primary HEAD.** `★ <branch>` (the primary checkout's current
   branch), falling back to `★ <repo-name>` when the primary HEAD is detached.
   This needs the primary's branch, which `worktree.List` drops — so add a small
   `worktree.Primary` counterpart that returns the primary working tree.
5. **Pinned first, unsorted.** The base row is prepended **after** `sortDashRows`,
   so it stays first regardless of session recency.
6. **`worktree.Resolve` and the shell `bridge open` path are unchanged** — this
   is purely additive on the nav dashboard.

## Architecture

```
loadDashRowsCmd (data.go)
  ├── worktree.List(repo.Path)      → isolated worktrees            (unchanged)
  ├── worktree.Primary(repo.Path)   → primary entry (branch or "")  (NEW)
  ├── core.LoadSlots / LiveSessions                                 (unchanged)
  └── buildDashRows(repo, primary.Branch, wts, slots, sessions, now)
          rows := <worktree rows, sorted by sortDashRows>           (unchanged)
          base := baseRow(repo, branch, slots, liveBySlot, now)     (NEW, pinned)
          return append([]dashRow{base}, rows…)

dashboard list (view.go dashListBody)
  ▸ ★ main            claude   3m …     ← base row (index 0, isBase)
    fix-x   worktree-fix-x  …           ← isolated worktrees
    …
  + Create new worktree…                ← unchanged trailing row

enter on the base row → launchRow (unchanged)
    launchPlan: slot = SlotID(repo.Name, "")   == "<repo>"   (bare)
                path = repo.Path
    → attach when a live "<repo>" session exists, else launch in repo root
```

### 1. `internal/worktree` — expose the primary working tree

`List` excludes the primary; add its counterpart so nav can label the base row:

```go
// Primary returns the repo's primary working tree (repoPath itself): its path
// and short branch name ("" when HEAD is detached). Counterpart to List, which
// excludes the primary — nav needs the primary's branch to label the base row.
func Primary(r Runner, repoPath string) (Entry, error)
```

It runs the same `git worktree list --porcelain` and returns the entry whose
cleaned path equals `repoPath`; a non-nil error means `repoPath` is not a usable
git repo (or the primary block is absent).

### 2. `internal/nav` — the base row

- New `dashRow` field (`types.go`):
  ```go
  isBase bool // the pinned base-checkout (main) row; worktree == "", path == repo.Path
  ```
- `buildDashRows` (`format.go`) gains a `baseBranch string` parameter and prepends
  the base row **after** sorting the worktree rows:
  ```go
  func buildDashRows(repo core.Repo, baseBranch string, wts []worktree.Entry,
      slots []core.Slot, sessions []core.Session, now time.Time) []dashRow {
      // … existing worktree-row build + sortDashRows(rows, liveBySlot) …
      base := baseRow(repo, baseBranch, slots, liveBySlot, now)
      return append([]dashRow{base}, rows...)
  }
  ```
- `baseRow` builds the pinned row and, when a live bare-`<repo>` session exists,
  fills the same live-session fields a worktree row carries (dot/agent/state/
  last-accessed):
  ```go
  func baseRow(repo core.Repo, branch string, slots []core.Slot,
      liveBySlot map[string]core.Session, now time.Time) dashRow {
      row := dashRow{isBase: true, branch: branch, path: repo.Path, dirtyState: loadPending}
      id := core.SlotID(repo.Name, "") // == "<repo>"
      sess, live := liveBySlot[id]
      if !live {
          return row
      }
      row.hasSession = true
      row.slotID = id
      row.state = sess.State
      row.lastAccessed = humanLastAccessed(now.Sub(sess.LastActivity))
      for _, sl := range slots {
          if sl.ID == id { // bare-<repo> slot carries the agent
              row.agent = sl.Agent
              break
          }
      }
      return row
  }
  ```
- `loadDashRowsCmd` (`data.go`) calls `worktree.Primary` and passes the branch:
  ```go
  primary, _ := worktree.Primary(worktree.ExecRunner{}, repo.Path)
  return dashRowsMsg{rows: buildDashRows(repo, primary.Branch, wts, slots, live, time.Now())}
  ```

### 3. `internal/nav` — View

`dashListBody` (`view.go`) renders the base row's name as `★ <branch>` (or
`★ <repo>` when detached), computed once and used in both the compact and full
layouts:

```go
name := trunc(r.worktree, 18)
if r.isBase {
    label := r.branch
    if label == "" {
        label = m.repo.Name
    }
    name = trunc("★ "+label, 18)
}
```

Everything else — the live-session dot, agent, last-accessed, and dirty
indicator — flows through the existing per-row rendering unchanged, because the
base row is an ordinary `dashRow`.

### 4. Launch — no change

`launchRow`/`launchPlan` (`update.go:648-702`) already do the right thing for the
base row: `slot = core.SlotID(m.repo.Name, row.worktree)` with `row.worktree ==
""` gives the bare `<repo>` slot, and it launches in `row.path == repo.Path`
without touching `worktree.Resolve`. `NameArgs(agent, repo, "", "")` yields the
same session label as the shell `bridge open <repo>`. Navigation already sizes on
`len(m.dashRows)+1`, so the extra pinned row needs no index changes.

## Edge cases

- **Detached primary HEAD:** `worktree.Primary` returns `Branch == ""`; the label
  falls back to `★ <repo-name>`.
- **No live base session:** the base row shows the muted `·` dot, `—` agent, and
  `(no session)` — identical to a session-less worktree row.
- **Live base session started via the shell:** because the slot id is the bare
  `<repo>`, the row shows it as live (dot/agent/state/last-accessed) and `enter`
  **attaches** rather than launching a duplicate.
- **`worktree.Primary` error (not a git repo):** `loadDashRowsCmd` ignores the
  error (as it already does for `worktree.List`), so `branch == ""` → the row
  still renders as `★ <repo-name>`; launch still targets `repo.Path`.
- **Empty repo / no other worktrees:** the dashboard shows just the base row and
  the "+ Create new worktree…" row.

## Testing

- **`internal/worktree`:** `Primary` returns the repo-root entry with its branch;
  a detached-HEAD porcelain fixture yields `Branch == ""`; a fake `Runner` (as in
  the existing `worktree` tests) drives both.
- **`internal/nav` (`format_test.go`):** update
  `TestBuildDashRows_MatchesSessionsAndSorts` for the new signature + the
  prepended base row at index 0; add a test that the base row is **pinned first**
  even when a worktree has the most-recent session, and a test that a live
  bare-`<repo>` session fills the base row's session fields (dot/agent/
  last-accessed) with the agent read from the matching slot.
- **`internal/nav` launch (`launch_test.go`):** a base row (`isBase: true`,
  `worktree: ""`, `path: repo.Path`) → `launchArgvFor`/`launchPlan` produces the
  bare `<repo>` slot and launches in `repo.Path`, and — when `hasSession` — an
  attach to the `<repo>` slot; assert no `worktree.Resolve` path is taken.
- **`internal/nav` view (`flow_test.go` + golden):** `dashListBody` renders the
  base row first as `★ <branch>` (and `★ <repo>` when detached); a golden flow
  frame shows the pinned base row above the worktrees and the create row.
- Gates: `gofmt -l .` empty · `go vet ./...` clean · `golangci-lint run` clean ·
  `go test -race ./...` green · goldens stable.

## Acceptance Criteria

- [ ] The per-repo dashboard shows a base-checkout row as the **first** row, above all worktrees and the "+ Create new worktree…" row, for every repo.
- [ ] The base row is labelled `★ <branch>` (the primary checkout's current branch), falling back to `★ <repo-name>` when the primary HEAD is detached.
- [ ] Pressing `enter` on the base row launches a session in the repo root (`repo.Path`) **without** creating or resolving a worktree.
- [ ] The base session's slot id is the bare `<repo>` (`SlotID(repo, "")`), so it is the same session as one started via the shell `bridge open <repo>` (no `-w`); attaching from either path lands in the same tmux session.
- [ ] When a live bare-`<repo>` session exists, the base row shows it as a live session (dot, agent, last-accessed) exactly like a worktree row.
- [ ] The base row is pinned first regardless of session recency (not reordered by the last-accessed sort).
- [ ] No new keybinding is introduced; existing navigation, `enter`, and the create-worktree row continue to work with the base row present.
- [ ] `worktree.Resolve` and the shell `bridge open` path are unchanged.
- [ ] `gofmt -l .` empty, `go vet ./...` clean, `golangci-lint run` clean, `go test -race ./...` green.

## Non-goals

- **Changing `worktree.Resolve`** or the `-w main` behaviour — untouched.
- **Changing the shell `bridge open <repo>` path** — the base row reuses its slot
  id; the open path itself is unchanged.
- **A new keybinding** for the base row — reached via existing navigation.
- **Removing / renaming the base session** from nav — out of scope.

## Open questions / follow-ups

- **Base-row dirty indicator on very large repos:** the base row gets the same
  async `gitDirtyCmd(repo.Path)` as any row; if that proves slow on the primary
  checkout, a later change could special-case it — out of scope here.
- **Icon choice (`★`):** matches the "pinned/primary" affordance; a themable
  glyph could come later.
