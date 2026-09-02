# Nav dashboard height stability

Date: 2026-09-02
Status: approved
Issue: [#256](https://github.com/freaxnx01/bridge/issues/256)

## Problem

Tester feedback: moving the cursor across the worktree list in `bridge nav`'s
dashboard (the two-column screen, `viewDash()`) makes the whole panel grid
visibly resize frame-to-frame — described as "bouncing" and "unstable." The
top frame in particular should hold a fixed size while the user is just
moving the selection, not resizing the terminal.

## Root cause

`viewDash()` (`internal/nav/view.go:309-358`) renders the two-column body in
two passes:

```go
h := max(lipgloss.Height(panel(leftW, "Sessions & Worktrees", listBody)), lipgloss.Height(rightAt(0)))
left := panelH(leftW, h, "Sessions & Worktrees", listBody)
body = lipgloss.JoinHorizontal(lipgloss.Top, left, rightAt(h))
```

`rightAt(0)` calls `detailColumn(rightW, 0)` when the worktree list has
focus. `detailColumn` (`internal/nav/view.go:493-510`) stacks three panels —
Branches, Recent commits, Git status — sized from the *currently selected*
worktree's actual data (`d := m.details[path]`): its branch count, commit
count, and status-line count. Two worktrees rarely have the same amount of
this data, so `rightAt(0)`'s natural height changes every time the selection
moves. `h` — the shared height both columns get stretched to — is derived
from that natural height, so the whole two-column frame changes size in
lockstep with the selection instead of staying fixed for a given terminal
size.

The left column's own natural height (`dashListBody`) does *not* vary with
selection — it's the full worktree list, unaffected by which row is
highlighted — so the instability is entirely attributable to the right
column's content-dependent probe.

## Goal

Moving the cursor across the worktree list, with the terminal size
unchanged, must not change the rendered height of the dashboard's two-column
frame — regardless of which worktree is selected or how much branch/commit/
status data it has.

## Non-goals

- Changing what's shown inside `detailColumn`'s panels, or how many rows they
  window (`per := (m.height-14)/3` and `windowList`/`windowAround` stay as
  they are).
- The narrow single-column layout (`m.width < dashTwoColMin`) — it doesn't
  use the two-pass stretch and isn't affected.
- `backlogColumn` while it's the visible right column (issues/ideas/todos
  focus) — its content doesn't depend on worktree selection, so it isn't the
  source of this bug. It is still driven through the same fixed `h` after
  this change (see Design), so behavior there is unchanged, not newly
  changed.

## Design

Stop deriving the shared height `h` from either column's *content-dependent*
natural render. Derive it instead from a budget computed purely from
`m.height` and the dashboard's fixed chrome (header line + hint line + one
blank line + the panel's own border/padding) — the same style of budget
`viewPicker` already uses for its own list window (`view.go:278`,
`m.height - used - 9 - recentH - forgeH`).

Concretely, in `viewDash()`:

1. Replace the two-pass probe (`h := max(natural(left), natural(right))`)
   with a single computed budget:

   ```go
   h := m.height - lipgloss.Height(header) - 3 // hint line + blank + fudge for the join
   ```

   accounting for whatever separates `header`, `body`, and `hint` in the
   final `out := header + "\n" + body + "\n" + hint` join, so `header` height
   + `h` + hint height + the two newlines exactly account for `m.height`.
   Clamp to a sane minimum (e.g. `3`) the same way `maxVisible` is clamped
   elsewhere, so a very short terminal degrades instead of going negative.

2. Render both columns stretched to that fixed `h` directly —
   `panelH(leftW, h, ...)` and `rightAt(h)` — with no natural-height probe
   step at all. `detailColumn`/`backlogColumn` already know how to stretch
   their *last* inner panel to absorb slack given a `minH` (`statusH`,
   `todosH`), so passing a `h` that's independent of content works with the
   existing stretch logic unchanged.

3. Leave `detailColumn`'s and `backlogColumn`'s internal per-panel budgets
   (`per := (m.height-14)/3`) untouched — they already bound how many rows
   each inner panel *can* show; this change only fixes what the *outer*
   frame height is driven from.

**Trade-off, made explicit:** if a worktree's branch/commit data would want
more room than the fixed budget gives the detail column, its panels window
(existing overflow markers — `↑ N more` / `↓ N more`) instead of growing the
frame. This is the same trade-off `stretchPanel` already makes for the
*last* panel in a stack; this change just makes the *outer* frame follow the
same rule instead of only the inner ones.

## Testing

`internal/nav` does not use `teatest`; it drives the `Model` directly through
a hand-rolled `session` harness (`internal/nav/navtest_test.go`: `newSession`,
`.key(...)`, `.frame()`) at a fixed `width, height`. Follow that existing
pattern, not `teatest`:

- New test in `internal/nav/view_test.go` (or alongside the existing
  `navtest_test.go` harness tests): build a dashboard `Model` via the harness
  with `m.dashRows` seeded with two worktrees, and `m.details` seeded so one
  worktree's `worktreeDetails` has few branches/commits/status lines and the
  other has many. Select the sparse worktree, capture `lipgloss.Height` of
  the rendered frame; select the other via `.key("down")`; capture again;
  assert the two heights are equal.
- Manual: `bridge nav` dashboard on the `bridge` repo itself (it has
  multiple worktrees with differing branch/commit counts) — arrow up/down
  through the worktree list should show a visually static frame at a fixed
  terminal size.

## Acceptance Criteria

- [ ] Moving ↑/↓ across the worktree list in `bridge nav`'s dashboard, with
      terminal size unchanged, renders the two-column frame at the same
      height on every keystroke, regardless of which worktree is selected.
- [ ] Existing behavior is unchanged when the terminal is resized (layout
      still adapts) and when the narrow single-column layout is active.
- [ ] A harness-driven test (`internal/nav`'s existing `session` pattern)
      asserts the frame height is identical across selections of at least
      two worktrees with differently-sized detail data.
- [ ] `gofmt -l .`, `go vet ./...`, `golangci-lint run`, and
      `go test -race ./...` stay green.
