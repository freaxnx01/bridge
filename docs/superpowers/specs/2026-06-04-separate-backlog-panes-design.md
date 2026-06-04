# Separate backlog panes: Open Issues / Ideas / Todos

**Date:** 2026-06-04
**Area:** `internal/nav` — `bridge nav` dashboard (Screen 2)
**Status:** Approved (design)

## Problem

On the dashboard, the right column is a single contextual pane that shows *one*
of: the selected worktree's details (Branches / Commits / Git status), **Open
issues**, or **Notes**. **Notes** combines `ideas.md` and `TODO.md` into one
scrolling pane. To compare issues against the backlog you must Tab between them —
you can only ever see one at a time.

The user wants **Open Issues**, **Ideas**, and **Todos** to be **separate panes,
all visible at once**.

## Goal (success criterion)

When focus is on any backlog pane, the dashboard shows three distinct, stacked,
independently-scrollable panes — Open Issues, Ideas, Todos — simultaneously.
`ideas.md` and `TODO.md` each get their own pane. The worktree Details view is
preserved.

## Decisions (from brainstorming)

1. **Layout — stacked right column.** Left column stays `Sessions & Worktrees`.
   The right column splits into three stacked panes (Open Issues / Ideas /
   Todos) when in backlog mode. Long issue titles keep the full column width.
2. **Focus model — 4-stop cycle, right column toggles.** Tab / Shift-Tab cycle
   `Worktrees → Open Issues → Ideas → Todos → (wrap)`. While focus is on the
   Worktrees list the right column shows the selected worktree's **Details**
   (unchanged). The moment focus enters any backlog pane, the right column shows
   all three stacked panes, with the focused one highlighted and scrollable.
3. **Empty/missing — always show all three with placeholders.** All three panes
   always render in fixed positions. Empty/missing sources show a muted
   placeholder (`✓ no open issues`, `no ideas.md`, `no TODO.md`). Tab stops on
   every pane regardless of content. Layout never shifts.

## Layout

Wide (≥ `dashTwoColMin` = 90 cols), focus on a backlog pane:

```
┌ Sessions & Worktrees ─┐ ┌ Open issues ─────────────┐
│ ● worktree-tesst-fb   │ │ ▸ #144 CLI repo addr...   │
│ ○ wt/ab12cd           │ │   #131 Analyze: does...   │
│                       │ │   ↓ 15 more               │
│ + Create new worktree…│ ├ Ideas · ideas.md ─────────┤
│                       │ │ # Ideas                   │
│                       │ │ - faster picker           │
│                       │ ├ Todos · TODO.md ──────────┤
│                       │ │ - [ ] wire completion     │
└───────────────────────┘ └───────────────────────────┘
```

Wide, focus on the Worktrees list: right column shows today's `detailColumn`
(Branches / Recent commits / Git status), unchanged.

Narrow (< 90 cols): single column, stacked top-to-bottom — Worktrees, then Open
Issues, Ideas, Todos, each always present (with placeholders when empty).

## Design

All changes are in `internal/nav/`. The existing panel / windowing / stretch
helpers and the existing notes loader (`readRepoNotes`, `noteFileNames =
["ideas.md", "TODO.md"]`) are reused as-is. No new data sources, no config.

### `types.go`

- Replace the `dashFocusNotes` enum value with two values: `dashFocusIdeas` and
  `dashFocusTodos`. Update the `dashFocus` doc comment to describe the four
  stops.

### `model.go`

- Replace `notesScroll int` with two independent offsets: `ideasScroll int` and
  `todosScroll int`. (Open Issues keeps its existing `issueSel`.)
- `notes []noteFile` stays as the loaded set (filled by the unchanged
  `loadNotesCmd` / `readRepoNotes`). Add two helpers that pick the relevant file
  by case-insensitive name, returning a pointer or `nil` when absent:
  - `func (m Model) ideaNote() *noteFile`  → matches `ideas.md`
  - `func (m Model) todoNote() *noteFile`  → matches `TODO.md`

  A small shared helper `noteByName(want string) *noteFile` backs both.

### `view.go`

- Add `backlogColumn(w, minH int) string`: `JoinVertical` of three panels —
  `issuesPanel`, `ideasPanel`, `todosPanel` — splitting the column height into
  ~thirds the way `detailColumn` does (`per := (m.height - 14) / 3`, floored at a
  small minimum), with the last pane absorbing slack so the column bottom-aligns
  with the worktree list.
- Split the combined `notesPanel`/`notesBody` into per-file panels:
  - `ideasPanel(w, minH)` / `todosPanel(w, minH)` render one `noteFile` each.
  - A shared `noteFilePanel(nf *noteFile, scroll int, focused bool, title, missing string, w, minH int)` body renderer windows a single file's lines by its own scroll offset and shows the focused selection markers; when `nf == nil` it renders the muted `missing` placeholder.
- `issuesBody` already renders `✓ no open issues` when empty — keep.
- `viewDash`: in the wide branch, choose the right column by focus —
  `detailColumn` when `dashFocus == dashFocusWorktrees`, otherwise
  `backlogColumn`. In the narrow branch, always append the Open Issues, Ideas,
  and Todos panels (each with placeholders) after the worktree list.
- Header chip (`✎ ideas.md · TODO.md` via `notesNames`) and the `●N open` count
  are unchanged.
- Remove the now-unused combined `notesPanel`/`notesBody`/`noteDisplayLines`
  paths only if nothing else references them (otherwise refactor in place).
  `notesTotalLines` is replaced by a per-file line count used to bound each
  file's scroll.

### `update.go`

- `dashPaneCycle` returns the fixed four stops `[Worktrees, Issues, Ideas,
  Todos]` unconditionally (panes always render, so Tab always stops on each).
- `cycledDashFocus` seeds the landed pane: clamp `issueSel` for Issues,
  `ideasScroll` for Ideas, `todosScroll` for Todos.
- `updateDash` dispatch: `dashFocusIssues → updateDashIssues` (unchanged),
  `dashFocusIdeas → updateDashIdeas`, `dashFocusTodos → updateDashTodos`.
- Replace `updateDashNotes` with two scroll handlers (or one parameterized
  helper) that bound scrolling by the **owning file's** line count
  (`ideaNote()` / `todoNote()` lines), so Ideas and Todos scroll independently.
- The dashboard reset path that currently zeroes `notesScroll` zeroes both
  `ideasScroll` and `todosScroll`.

### Hint line

`tab panes` already communicates the cycle; no copy change required.

## Out of scope (YAGNI)

- No editing of `ideas.md` / `TODO.md` from the TUI (read-only preview, as today).
- No additional note files or configurability (`noteFileNames` stays the fixed two).
- No new key bindings beyond the existing Tab/scroll set.

## Testing (TDD)

Write table-driven tests first (same-package or `nav_test` per existing files),
implement to green, then run the full gate.

1. **`ideaNote` / `todoNote` resolution** — case-insensitive name match
   (`Ideas.md`, `todo.md`); returns `nil` when the file is absent.
2. **`dashPaneCycle`** — always yields the four stops in order; Tab from Todos
   wraps to Worktrees; Shift-Tab from Worktrees wraps to Todos.
3. **Independent scroll** — scrolling Ideas does not change `todosScroll` and
   vice-versa; each clamps to its own file's line count.
4. **Backlog column rendering** — with focus on a backlog pane, output contains
   all three pane titles; an absent `TODO.md` renders the `no TODO.md`
   placeholder, not an omitted pane.
5. **Details preserved** — with `dashFocus == dashFocusWorktrees`, the right
   column renders Details (Branches / commits / status), not the backlog panes.

Gate after implementation:

- `gofmt -l .` empty
- `go vet ./...` clean
- `golangci-lint run` clean
- `go test -race ./...` full suite green
