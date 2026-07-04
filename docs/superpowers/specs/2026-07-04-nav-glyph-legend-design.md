# nav status-glyph legend (#157)

## Problem

The `bridge nav` TUI conveys state through compact glyphs — session dots, git
dirty/ahead/behind markers, a remote-only row prefix, an issue-count tag, the
selection caret. Their meaning lives only in the render code (`internal/nav/view.go`);
a new user cannot tell what `●3` or `⤳` or a leading `↓ ` means without reading the
source. Issue #157 asks for an in-TUI legend that maps each glyph to a short
description, reachable without leaving the picker/dashboard, and verified against the
code that emits the glyphs.

## Goal (success criterion)

From the picker or dashboard, pressing `?` opens a legend that documents every status
glyph those screens render, each shown in its real style beside a short meaning, and
`?`/`esc` dismisses it — all without leaving the TUI.

## Decisions (settled during brainstorming)

- **Surface: a `?` toggle overlay.** Standard TUI convention, zero permanent screen
  cost. `?` is currently unbound on every screen.
- **Content: one unified legend, grouped by category** (Session / Git status / Rows &
  selection / Header). Reachable identically from picker and dashboard; a few entries
  won't apply to the current screen, but grouping makes that obvious.
- **Render: full-screen replace** while open (mirrors the existing `viewRepoModal`
  early-return idiom, `view.go:71-73`), using the existing `panel()` box helper.
- **Single source: a `legendEntries` table.** The overlay renders from it; it is the
  one place to maintain.
- **Sync guarantee is right-sized, not a refactor** (see "Staying in sync").

## Audited glyph inventory (the content to document)

Verified against the emitting code (branch `main` at implementation time). Each entry
renders the glyph in the named style.

**Session dots** (picker sessions `view.go:81-83`; dashboard rows `view.go:207-213`)
- `●` (U+25CF) `stOk` — session **attached**
- `○` (U+25CB) `stMuted` — session **detached**
- `·` (U+00B7) `stMuted` — **no session** (dashboard rows)

**Git status** (`dirtyView` `view.go:246-272`)
- `●N` `stBad` — N modified/changed files
- `↑N` `stWarn` — N commits **ahead** of upstream
- `↓N` `stAccent` — N commits **behind** upstream
- `✓ clean` `stOk` — nothing modified/diverged
- `⤳ no upstream` `stMuted` — branch has no upstream tracking ref
- `?` `stMuted` — dirty-state **load error** (`loadErr`, `view.go:251`)
- spinner — dirty-state **loading** (`loadPending`, `view.go:249`)

**Rows & selection**
- `↓ ` prefix (`data.go:92`) — **remote-only** repo (not cloned; clone on select)
- `●N` (`repoIssueTag` `view.go:616`) `stWarn` — open-issue count on a repo row
- `▸ ` (`stAccent` in `stSel`) — **selected** row (picker list/sessions, create row)
- `+ Create new worktree…` (`view.go:235-239`) — dashboard action row

**Header** (`viewDash` `view.go:150-159`)
- `●N open` `stWarn` — repo open-issue count
- `✎ <names>` `stAccent` — present note files (e.g. `✎ ideas.md · TODO.md`)

## Architecture

Additive, confined to `internal/nav`. Reuses centralized styles (`view.go:20-27`) and
`panel()`/`hintLine()` helpers.

### 1. State (`internal/nav/model.go` / `types.go`)

- `showLegend bool` field on `Model`.

### 2. Toggle (`internal/nav/update.go`)

Handle `?` in the top-level `KeyMsg` routing, **before** per-screen and filter-input
routing, so it works even while the picker filter is focused (repo labels never
contain `?`, so a global intercept costs nothing):

```go
// legend overlay: ? toggles it on picker/dashboard; while open, ?/esc/q close and
// all other keys are swallowed.
if k := msg.String(); k == "?" && (m.screen == screenPicker || m.screen == screenDash) {
	m.showLegend = !m.showLegend
	return m, nil
}
if m.showLegend {
	switch msg.String() {
	case "esc", "q", "?":
		m.showLegend = false
	}
	return m, nil
}
```

(Exact insertion point: in the `tea.KeyMsg` branch of `Update`, before it dispatches to
`updatePicker`/`updateDash`. The `?`-toggle and the swallow-block both sit there.)

### 3. Legend data (`internal/nav/view.go`, near the styles block)

A package-level table is the single source:

```go
type legendEntry struct {
	glyph   string
	style   lipgloss.Style
	meaning string
	group   string
}

// legendEntries documents every status glyph the picker/dashboard render. It is the
// single source for viewLegend and is guarded by TestLegend_CoversAuditedGlyphs.
// When you add or change a rendered status glyph, update this table.
var legendEntries = []legendEntry{
	{"●", stOk, "session attached", "Session"},
	// … one entry per audited glyph above …
}
```

### 4. Render (`internal/nav/view.go`)

- `viewLegend() string` builds a `panel(m.width, "Legend", body)`; body iterates
  `legendEntries`, grouping by `group`, rendering `entry.style.Render(entry.glyph)`
  followed by the meaning. Ends with a muted `? / esc to close` line.
- In `View()` (`view.go:57-68`), early-return before the screen switch:
  `if m.showLegend { return m.viewLegend() }`.

### 5. Footer hint (`internal/nav/view.go`)

Append `· ? legend` to the picker hint (`view.go:146`) and dashboard hint
(`view.go:192`).

## Staying in sync (honest scope)

Glyph runes are **not** centralized today — they are inline string literals across
~20 render sites. Making the legend derive automatically from the render code would
require a glyph-registry refactor touching every site: **out of scope** (YAGNI, not a
surgical change). Instead:

1. `legendEntries` is the single source the overlay renders from.
2. **Completeness test** `TestLegend_CoversAuditedGlyphs`: asserts `legendEntries`
   covers exactly the audited glyph set (a fixed expected list encoded in the test),
   and that every entry has a non-empty glyph and meaning. Adding/removing an entry
   without updating the expected set fails the test — a deliberate maintenance gate.
3. **No-phantom check:** for each entry whose glyph is a distinctive status rune
   (`●○·↑↓✓⤳✎▸`), assert the rune appears in the `internal/nav/view.go` source, so the
   legend can't document a glyph the UI no longer emits. (Skipped for `?`/`+`/`↓ `
   which are ambiguous as bare substrings.)
4. A one-line comment at the primary glyph render sites (`dirtyView`, the session-dot
   switch, `repoIssueTag`) points to `legendEntries` as the maintenance anchor.

## Sequencing note

The `★` base-checkout glyph (#182, PR #184) is **not on `main`** yet. This legend
documents the glyphs present on `main` at implementation time. When #182 merges, add a
`{"★", stAccent, "base checkout (main)", "Rows"}` entry and extend the expected set. If
#182 has already merged when this is implemented, include it from the start. (#183 and
#155 add no new glyphs.)

## Testing

- **Golden** of `viewLegend` at a fixed width (`testdata/legend.golden`) — proves
  grouping, glyphs, and the close hint; no ANSI.
- **Toggle unit tests:** `?` on the picker sets `showLegend` and `View()` returns the
  legend; `?` again clears it; `esc` clears it; a non-close key is swallowed while
  open; same from the dashboard screen.
- **Filter-focus test:** `?` toggles the legend even when the picker filter is focused
  (not captured as filter text).
- **Completeness + no-phantom tests** as above.
- **Byte-identical when closed:** existing picker/dashboard goldens unchanged (the
  legend adds only the `· ? legend` hint text; regenerate and confirm only that hint
  differs, or update those goldens in the same commit and note it).
- Gates: `gofmt -l .` empty, `go vet ./...` clean, `golangci-lint run` clean,
  `go test -race ./...` green.

## Acceptance Criteria

- [ ] Every status glyph rendered on the picker/dashboard (the audited inventory above)
      has a documented meaning in the legend.
- [ ] The legend is reachable via `?` from the picker and the dashboard, and dismissed
      with `?`/`esc` without leaving the TUI.
- [ ] Each glyph is rendered in its real `st*` style; meanings match the emitting code.
- [ ] `? legend` appears in the footer hint on both screens.
- [ ] `TestLegend_CoversAuditedGlyphs` guards the table against the audited set; a
      golden covers the rendered overlay.
- [ ] Existing goldens remain correct (only the added footer hint changes).
- [ ] `gofmt -l .` empty, `go vet ./...` clean, `golangci-lint run` clean,
      `go test -race ./...` green.

## Non-goals

- A glyph-registry refactor (centralizing every rune) — the table + guard test is the
  right-sized sync mechanism.
- Covering the **overview** screen's glyphs (`◐`/`⚖`/`•`/`⚠`).
- A configurable, themeable, or paginated legend.
- Documenting dynamic git-porcelain XY codes (e.g. `??` untracked) that appear only as
  live data in the Git-status detail panel, not as designed markers.
