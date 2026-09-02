# nav worktree/branch selector marker (#255) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** The worktree/branch selector in the nav dashboard (`dashListBody`)
marks its selected row with the same theme-independent `▸ ` accent marker
every other selectable list in `internal/nav/view.go` already uses, instead of
relying solely on a background-color highlight. Spec:
`docs/superpowers/specs/2026-09-02-nav-worktree-selector-marker-design.md`.

**Architecture:** A two-character formatting change confined to
`dashListBody` (`internal/nav/view.go:363-408`): reserve a two-space gutter on
unselected rows, replace it with `stAccent.Render("▸ ")` on the selected row,
matching the convention already used by `recentBlock`, the sessions panel,
the repo picker list, the worktree-create chooser, and the backlog panes.

**Tech Stack:** Go, Bubble Tea (charmbracelet), lipgloss; stdlib testing
(table-driven, golden files via `-update` — NO testify/mockery).

## Global Constraints

- `gofmt -l .` empty; `go vet ./...` clean; `golangci-lint run` clean;
  `go test -race ./...` green.
- No new dependencies. Change confined to `internal/nav`.
- Only `dashListBody`'s worktree rows change. The "+ Create new worktree…"
  row, and every other list's rendering, are untouched.
- `internal/nav/testdata/dash_base_row_pinned.golden` is expected to change
  (regenerate via `-update`, review the diff by eye before committing — it
  should be exactly a two-column shift, nothing else).

---

## File Structure

- **Modify** `internal/nav/view.go` — `dashListBody`: add the two-space
  gutter / `▸ ` marker to the worktree rows.
- **Modify** `internal/nav/flow_test.go` — extend `TestDashListBody_*` tests
  (or add a new one) asserting the marker appears on the selected row and the
  gutter appears on unselected rows.
- **Modify** `internal/nav/testdata/dash_base_row_pinned.golden` — regenerate.

No other files change.

---

## Task 1: Add the marker/gutter to `dashListBody`

- [ ] Write a failing test in `internal/nav/flow_test.go`:
  `TestDashListBody_SelectedRowUsesAccentMarker` — build a `Model` with at
  least two `dashRows`, `dashSel` pointing at index 1, `dashFocus ==
  dashFocusWorktrees`, call `m.dashListBody(false)`, and assert:
  - the selected row's line contains `▸ ` (as a literal substring — the test
    doesn't need to assert color, just presence/position immediately before
    the row content)
  - the unselected row's line starts with two spaces followed by its `dot`
    glyph (i.e. no marker, gutter present)
  - Run it: confirm it fails against the current implementation (no marker
    exists yet).
- [ ] In `internal/nav/view.go`, update `dashListBody` (`view.go:363-408`):
  - Before the `if compact { ... } else { ... }` block that builds `line`,
    decide the leading gutter: `gutter := "  "` by default.
  - After building `line` as today, replace the selection branch:
    ```go
    if i == m.dashSel && m.dashFocus == dashFocusWorktrees {
        line = stSel.Render(stAccent.Render("▸ ") + line)
    } else {
        line = "  " + line
    }
    ```
    (Adjust exact placement so `compact`/full-width `fmt.Sprintf` output is
    unchanged apart from the new two-character prefix — don't reformat the
    existing column widths.)
- [ ] Run the new test: confirm it passes.
- [ ] Run the full `internal/nav` test suite: expect `TestDashListBody_*` and
  golden-comparison tests touching `dash_base_row_pinned.golden` to fail on
  the now-stale golden.
- [ ] Regenerate: `go test ./internal/nav -update`. Diff
  `dash_base_row_pinned.golden` by eye — confirm the only change is the
  two-character shift on every worktree row (no unrelated content moved).
- [ ] Re-run `go test ./internal/nav`: green.

## Task 2: Full verification

- [ ] `gofmt -l .` — empty.
- [ ] `go vet ./...` — clean.
- [ ] `golangci-lint run` — clean.
- [ ] `go test -race ./...` — full suite green, not just `internal/nav`.
