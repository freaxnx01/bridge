# nav: the dashboard detail column never outgrows the frame (#364)

Date: 2026-11-09
Status: approved
Issue: [#364](https://github.com/freaxnx01/bridge/issues/364)
Follows: [#256](https://github.com/freaxnx01/bridge/issues/256) / #258, `2026-09-02-nav-dashboard-height-stability-design.md`

## Problem

On `bridge nav`'s two-column dashboard, moving the cursor through the worktree
list still makes the screen jump. Selecting a worktree with long commit
subjects or several git-status entries renders a frame **taller than the
pane**. The terminal scrolls, and the header box (`bridge nav · <repo>` and its
top border) disappears off the top. Selecting a worktree with sparse data
brings it back.

#258 fixed the *outer* frame budget: `viewDash()` derives the shared height `h`
from `m.height` minus the header and hint (`internal/nav/view.go:371-378`). Its
spec deliberately left the *inner* panel budgets alone ("`per := (m.height-14)/3`
… stay as they are"). That leftover is what still breaks.

## Reproduction (measured 2026-11-09 against `main` @ `2ec1d06`)

A throwaway test rendered `m.View()` for one worktree and compared
`lipgloss.Height` with `m.height`:

| Size | Detail data | Frame | Over |
|---|---|---|---|
| 150×38 | 20 short commits, clean | 38 | 0 |
| 150×38 | 20 **long** commits (≈95 chars), clean | 42 | **+4** |
| 150×38 | 20 long commits, 2 status files | 43 | **+5** |
| 150×38 | 20 short commits, **50** status files | 42 | **+4** |
| 120×30 | 20 long commits, 2 status files | 34 | **+4** |
| 200×50 | 20 short commits, 50 status files | 51 | **+1** |

The backlog column (issues/ideas/todos focus) was measured with 50 entries in
each pane and fits exactly at both sizes. It already truncates lines
(`view.go:573`, `:672`) and windows by `minH`, so it is out of scope.

## Root cause

Two independent defects in `detailColumn` (`internal/nav/view.go:520-536`) and
its three body renderers:

1. **Lines soft-wrap.** `branchesBody` (`:703`), `commitsBody` (`:733`) and
   `statusBody` (`:755`) emit each item unclipped. `panel` fixes the content
   width (`Width(w-2)` with `Padding(0,1)`, so `w-4` usable columns), and
   lipgloss **wraps** any longer line onto a second row. `windowList` counts
   items, not rows, so each wrapped item costs one row the budget never
   reserved.

2. **The row budget ignores the column height.** The item cap is
   `per := (m.height - 14) / 3`, the same for all three panels. The column's
   real height is `minH = m.height - 6` (header panel 5 + hint 1). Three full
   panels take `3·per + 3·4` rows (4 = border top/bottom + title + blank
   line), which is `minH + 4`. Git status is `stretchPanel`ed to
   `statusH = minH - branches - commits`, but `stretchPanel` (`:682-688`) only
   ever **grows** a panel to `minH`. When its natural height is larger, it
   returns the natural render and the column overflows.

## Design

All changes stay inside `internal/nav/view.go`.

### 1. Budget rows from `minH`, give Git status the remainder

```go
// detailPanelChrome is the rows a detail panel spends outside its body:
// rounded border top and bottom, the title line, and the blank line under it.
const detailPanelChrome = 4
```

In `detailColumn`:

- `per := max(1, (minH-3*detailPanelChrome)/3)` caps Branches and Recent
  commits.
- Git status gets the rows that remain: `statusMax := max(1, statusH-detailPanelChrome)`,
  where `statusH = minH - height(branches) - height(commits)`, as today.

Branches and commits each take at most `per + 4` rows, so
`statusH ≥ per + 4` and Git status can always show `statusMax ≥ per` rows.
When branches or commits have few items, Git status inherits their unused
rows. The column then sums to exactly `minH`.

### 2. Clip every line to the panel's inner width

The bodies take the inner width (`w - 4`, the same figure the notes pane uses
at `:672`). Each line keeps its prefix and truncates only the variable text,
**before** styling:

- branches: 2-column marker (`* `, `+ `, `  `) + `trunc(name, inner-2)`
- commits: `sha + " " + trunc(subject, inner - width(sha) - 1)`
- status: `code + " " + trunc(path, inner - width(code) - 1)`

A clipped line ends in `…`. Nothing wraps, so one item is one row.

### 3. Make `trunc` rune- and width-safe

`trunc` (`:506-514`) returns `s[:n-1] + "…"`. That slices **bytes**: a
subject containing `—` or `é` can be cut mid-rune, producing invalid UTF-8,
and a wide rune can overrun the width. Commit subjects routinely contain `—`
(see the issue's screenshots). The fix walks runes and accumulates
`lipgloss.Width` until the next rune would exceed `n-1`, then appends `…`.
Every call site passes plain, unstyled text (checked: `view.go`, `agents.go`,
`overview.go`, `issues.go`), so per-rune widths are exact. ASCII output is
byte-for-byte unchanged.

### Invariant

For a two-column dashboard (`m.width ≥ dashTwoColMin`) and
`m.height ≥ 24`, the rendered dashboard is **exactly `m.height` rows** for
every selected worktree, whatever its branch, commit or status data.

Below roughly 21 rows `per` floors at 1 and `windowList` still spends a row
on its `… +N more` marker, so a very short terminal can still overflow. That
degradation is accepted and not tested.

## Non-goals

- The backlog column (measured; it fits).
- The narrow single-column layout (`m.width < dashTwoColMin`), which has no
  detail column.
- Wrapping commit subjects to show more text. Truncation was chosen
  (2026-11-09) to match the issues and notes panes.
- The picker's own hint-line width budget (#282 review).

## Acceptance criteria

- With the two-column dashboard and the worktree list focused,
  `lipgloss.Height(m.View()) == m.height` for every height in 24–60 and
  widths 100, 120 and 150, with each of these detail sets: long commit
  subjects, 50 status files, long branch names, long status paths, and all
  of them at once.
- The header panel stays the first thing rendered: line 0 of the view is its
  top border and line 1 contains `bridge nav · <repo>`.
- A commit subject wider than the panel renders on one row ending in `…`.
- `trunc` never splits a rune: its result is valid UTF-8, and its display
  width is at most `n` for ASCII, accented (`é`), dash (`—`) and wide (CJK)
  input.
- `trunc` on ASCII input returns exactly what it returned before.
- Existing `TestView_Dash_FrameHeightStable_AcrossWorktreeSelection` and
  `TestViewDash_StatusNotice_DoesNotChangeFrameHeight` still pass.
- `gofmt`, `go vet`, `golangci-lint` clean; `go test -race ./...` green,
  goldens regenerated only where the detail panels' item counts change.

## Testing

- `trunc`: table-driven test in `internal/nav/view_test.go` over ASCII,
  multi-byte and wide input. The multi-byte case fails today (invalid UTF-8).
- Frame invariant: table-driven test in `view_test.go` over the sizes and
  detail sets above, asserting the exact height and the header lines. It fails
  today by +1 to +5 rows, matching the reproduction table.
- Truncation: one test with a single long subject, asserting the commit's row
  contains the sha and ends in `…` and that the frame height holds.
- Goldens: `go test ./internal/nav -update`. Read the diff: only Recent
  commits / Branches / Git status item counts and `… +N more` markers may
  change.
