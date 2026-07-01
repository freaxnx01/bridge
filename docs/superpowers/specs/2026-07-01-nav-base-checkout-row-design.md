# Design: base-checkout (main) row in `bridge nav`

**Issue:** #174
**Date:** 2026-07-01
**Status:** Approved

## Problem

`bridge nav`'s per-repo dashboard lists only isolated worktrees plus a
"+ Create new worktree…" row. There is no way to open or attach a session on the
**main checkout** (the repo root / primary working tree) without going through a
worktree. This is deliberate at two levels today:

1. `worktree.List` excludes the primary working tree — *"nav lists isolated
   worktrees, not the main checkout"* (`internal/worktree/worktree.go:32-49`).
2. `worktree.Resolve` refuses to resolve to the repo root, so naming a worktree
   `main` creates a dedicated `.worktrees/main` on branch `worktree-main` instead
   (`internal/worktree/worktree.go:55-88`).

Every session-launch path in nav (`launchRow`, `launchIssue`, the `n` modal) goes
through worktree creation/resolution. The only bare-attach path is the picker's
Active-sessions panel, which merely re-attaches already-running sessions.

## Goal

Every repo dashboard shows a **pinned first row** for the main checkout. Pressing
`enter` on it launches (or attaches) a session in the repo root, with **no
worktree** created or selected.

Success criteria — see Acceptance Criteria at the end.

## Design decisions (settled)

| Decision | Choice | Rationale |
|---|---|---|
| Slot identity | Bare `<repo>` (`worktree=""`) | `core.SlotID(repo, "")` returns the bare `<repo>` — the documented base form (`internal/core/slot.go:24-33`). A base session started in nav and one from the shell `bridge open <repo>` (no `-w`) are then the **same** tmux session. |
| Row label | `★ <branch>` (marker + primary branch) | Reads naturally, adapts to `main`/`master`/other. Fallback `★ <repo.Name>` when the primary HEAD is detached. |
| Visibility | Always shown, **pinned first** | Simple, discoverable, no config surface. Not subject to the last-accessed sort. |

## Approach

### 1. `dashRow` gains an `isBase bool` flag

`internal/nav/types.go`. The base row cannot store its label in `worktree` because
`worktree` doubles as the slot key and must be `""` for the bare slot id. So the
base row is identified by an explicit flag:

- `isBase = true`
- `worktree = ""`
- `path = repo.Path`
- `branch = <primary branch>` (from `worktree.Primary`; `""` if detached)
- `displayLabel = ""`

Everything downstream then works unchanged:

- `launchPlan` (`update.go:567`) computes `slot = core.SlotID(m.repo.Name, "")` =
  bare `<repo>`, and launches in `row.path` = repo root.
- Session naming: `NameArgs` → `displayName(repo, "")` = `<repo>`
  (`cmd/bridge/preflight.go:330`), so the tmux/agent session is named cleanly
  after the repo. No special-casing required.

### 2. `worktree.Primary` — new function

`internal/worktree/worktree.go`. Symmetric with `List`; returns the **primary**
working-tree entry (the one whose cleaned path equals `repoPath`) parsed from
`git worktree list --porcelain`, reusing `parsePorcelain`.

```go
// Primary returns the repo's primary working tree (repoPath itself): its path
// and short branch ("" when detached). List excludes this entry; Primary is how
// nav surfaces the main checkout as a base row.
func Primary(r Runner, repoPath string) (Entry, error)
```

Returns an error only when `repoPath` is not a usable git repo. The primary entry
is expected to exist for any real repo; if porcelain output somehow omits it, the
function returns an entry with `Path: repoPath, Branch: ""` so the base row still
renders (label falls back to the repo name).

### 3. `buildDashRows` — prepend the pinned base row

`internal/nav/format.go`. The signature gains the primary branch, threaded from
`loadDashRowsCmd`:

```go
func buildDashRows(repo core.Repo, baseBranch string, wts []worktree.Entry,
    slots []core.Slot, sessions []core.Session, now time.Time) []dashRow
```

Behaviour:

1. Build worktree rows exactly as today and sort them with `sortDashRows` (no
   change to worktree ordering).
2. Construct the base row (`isBase=true`, `worktree=""`, `path=repo.Path`,
   `branch=baseBranch`, `dirtyState=loadPending`).
3. Match the base row's live session by the **bare slot id**
   `core.SlotID(repo.Name, "")`: find a slot for this repo whose `Worktree == ""`
   and whose `ID` has a live session, and populate
   `hasSession`/`slotID`/`agent`/`state`/`lastAccessed` the same way worktree rows
   are populated. This is a dedicated lookup because the existing `slotByWt` map
   skips `sl.Worktree == ""` slots (`format.go:159`) — that filter stays as-is for
   worktree rows.
4. **Prepend** the base row to the sorted worktree rows so it is always first and
   never reordered by session recency.

### 4. `loadDashRowsCmd` — fetch the primary branch

`internal/nav/data.go:102`. In addition to `worktree.List`, call
`worktree.Primary(worktree.ExecRunner{}, repo.Path)` and pass its `Branch` into
`buildDashRows`. A `Primary` error is non-fatal — pass `""` (the row still renders
with the repo-name fallback).

### 5. `dashListBody` — render the base label

`internal/nav/view.go:196`. The name column is currently `trunc(r.worktree, 18)`.
For `r.isBase`, render `★ <branch>` instead — where `<branch>` is `r.branch`, or
`repo`-name fallback when `r.branch == ""` (detached HEAD). Apply to **both** the
compact and full render paths. Extract a tiny helper so the two paths share the
logic, e.g.:

```go
func (r dashRow) listName(repoName string) string {
    if !r.isBase {
        return r.worktree
    }
    b := r.branch
    if b == "" {
        b = repoName
    }
    return "★ " + b
}
```

No new keybinding: the base row is `dashRows[0]`, so existing navigation, `enter`
(→ `launchRow`), and the `dashSel == len(m.dashRows)` create-row logic
(`update.go:369-377`) all keep working with the base row counted as a normal
selectable row. Selecting the base row shows the standard Details panel
(branches/commits/status of `repo.Path`) with no special wording.

## Out of scope

- No config/flag — the base row is always shown.
- No change to `worktree.Resolve` or the shell `bridge open` path.
- No new keybinding.
- Dirty-status loading for the base row uses the existing per-`path` mechanism
  (the row carries `path = repo.Path`, `dirtyState = loadPending`); no new wiring.

## Testing

Standard-library table-driven tests, hand-rolled `Runner` fake — no new deps.

**`internal/nav/format_test.go` (`buildDashRows`):**

- Base row is always present and is `rows[0]`, with `isBase=true`,
  `worktree==""`, `path==repo.Path`, `branch==baseBranch`.
- Base row picks up a live session when a slot with `Worktree==""` and
  `ID==SlotID(repo,"")` has a live session (`hasSession`, `agent`, `state`,
  `lastAccessed` populated).
- Base row shows no session (`hasSession==false`) when no such slot/session
  exists.
- Base row stays pinned first even when a worktree has a more recent session
  (worktree ordering among themselves is unchanged).

**`internal/worktree/worktree_test.go` (`Primary`):**

- Returns the entry whose path equals `repoPath`, with its short branch.
- Detached primary HEAD → `Branch==""`.
- Path matching is clean-path based (trailing slash / `.` tolerant), consistent
  with `List`.

**`internal/nav/view_test.go` (or existing view test) — label formatting:**

- `isBase` row renders `★ <branch>` in the name column.
- Detached base row (`branch==""`) renders `★ <repo.Name>`.

All must pass under `go test -race ./...`; `gofmt`, `go vet`, `golangci-lint`
clean.

## Acceptance Criteria

- [ ] The per-repo dashboard shows a base-checkout row as the **first** row,
      above all worktrees and the "+ Create new worktree…" row, for every repo.
- [ ] The base row is labelled `★ <branch>` (the primary checkout's current
      branch), falling back to `★ <repo-name>` when the primary HEAD is detached.
- [ ] Pressing `enter` on the base row launches a session in the repo root
      (`repo.Path`) **without** creating or resolving a worktree.
- [ ] The base session's slot id is the bare `<repo>` (`SlotID(repo, "")`), so it
      is the same session as one started via the shell `bridge open <repo>`
      (no `-w`); attaching from either path lands in the same tmux session.
- [ ] When a live bare-`<repo>` session exists, the base row shows it as a live
      session (dot, agent, last-accessed) exactly like a worktree row.
- [ ] The base row is pinned first regardless of session recency (not reordered
      by the last-accessed sort).
- [ ] No new keybinding is introduced; existing navigation, `enter`, and the
      create-worktree row continue to work with the base row present.
- [ ] `worktree.Resolve` and the shell `bridge open` path are unchanged.
- [ ] `gofmt -l .` empty, `go vet ./...` clean, `golangci-lint run` clean,
      `go test -race ./...` green.
