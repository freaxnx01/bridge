# nav worktree/branch selector visibility under low-contrast themes (#255)

## Problem

A tester reported the worktree/branch selector row in `bridge nav`'s dashboard
is "barely visible" under a Solarized terminal color theme. Issue #255
initially suspected the nav palette's hardcoded `lipgloss.Color` hex constants
(`internal/nav/view.go:11-18`) as the root cause, since they are not
`lipgloss.AdaptiveColor` pairs.

## Root cause (confirmed by code inspection)

Every selectable list in `internal/nav/view.go` pairs its background highlight
(`stSel`, `Background(colSelBg)`) with a colored `▸ ` marker
(`stAccent.Render("▸ ")`) that replaces a two-space gutter reserved on
unselected rows — e.g. the picker's Recent list (`view.go:179-185`), the
sessions panel (`view.go:229-234`), the repo picker list (`view.go:286-292`),
the worktree-create chooser (`view.go:450-454`), and the backlog panes
(`view.go:545-549`).

**`dashListBody`** — the worktree/branch list rendered in the nav dashboard,
i.e. the exact "wt/branch selector" the tester means — is the one exception.
Its selected row (`view.go:396-398`) applies only the background highlight:

```go
if i == m.dashSel && m.dashFocus == dashFocusWorktrees {
    line = stSel.Render(line)
}
```

There is no `▸` marker and no reserved gutter on unselected rows, so this is
the only selector in the app whose selection state depends *entirely* on the
background-color delta between `colSelBg` (`#2a2b3d`) and the terminal's
actual background. Under a theme whose background luminance sits close to
`colSelBg` — Solarized's dark variant included — that delta shrinks and the
highlight all but disappears. Every other list in the file has a colored
marker as a theme-independent fallback; this one doesn't.

The originally-suspected palette (non-adaptive hex colors) is a real gap
too, but it affects every styled element uniformly, not this one selector
specifically — it does not explain why the tester singled out the worktree
list. Broadening the whole palette to `AdaptiveColor` is out of scope here;
tracked separately if it recurs elsewhere.

## Goal (success criterion)

The worktree/branch selector's selected row is visible via the same
theme-independent marker convention already used by every other selectable
list in `internal/nav/view.go`, not solely a background-color contrast.

## Decision

Give `dashListBody` the same two-space-gutter / `▸ ` marker treatment as its
neighbors:

- Unselected rows gain a two-space leading gutter before the existing
  `dot` glyph (both the `compact` and full-width formats).
- The selected row replaces that gutter with `stAccent.Render("▸ ")`, then
  the whole line is wrapped in `stSel` as today.
- The already-marker-carrying "+ Create new worktree…" row (`view.go:402-405`)
  is unaffected — it already follows this convention.

This is a two-character shift of the worktree list's columns, confined to
`dashListBody`. It will change `internal/nav/testdata/dash_base_row_pinned.golden`
(the only golden that renders this list); no other golden touches it (verified
by inspecting which tests call `dashListBody`/`viewDash` with a real worktree
list versus the create-row-only path).

## Non-goals

- Migrating the nav color palette to `lipgloss.AdaptiveColor`. Not the
  confirmed cause of this report; would be a separate, broader change.
- Testing against a live Solarized terminal. The fix targets the structural
  gap (marker-less selection) shared by no other list, verifiable by
  golden-file assertion instead of a specific color profile.
