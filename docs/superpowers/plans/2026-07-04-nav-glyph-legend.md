# nav status-glyph legend (#157) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** From the picker or dashboard, pressing `?` opens a legend that
documents every status glyph those screens render (session dots, git
dirty/ahead/behind markers, the remote-only prefix, the issue-count tag, the
selection caret, the header chips), each shown in its real style beside a short
meaning; `?`/`esc` dismisses it — all without leaving the TUI.

**Architecture:** Additive, confined to `internal/nav`. A `showLegend bool` on
`Model` toggled by a global `?` intercept in `Update`'s `tea.KeyMsg` branch
(before picker/dash key routing, so it works even while the picker filter is
focused); a package-level `legendEntries` table as the single source of
content; a `viewLegend()` renderer using the existing `panel()` helper, wired
in as an early return in `View()`; and a `· ? legend` hint appended to the
picker and dashboard footers. Spec:
`docs/superpowers/specs/2026-07-04-nav-glyph-legend-design.md`.

**Tech Stack:** Go, Bubble Tea (charmbracelet), lipgloss; stdlib testing
(table-driven, golden files — NO testify/mockery).

## Global Constraints

- `gofmt -l .` empty; `go vet ./...` clean; `golangci-lint run` clean;
  `go test -race ./...` green.
- No new dependencies. Change confined to `internal/nav`.
- Legend covers exactly the audited glyph set in the spec; each glyph rendered
  in its real `st*` style (the two entries without a meaningful `st*` style —
  the loading spinner and the plain `+` create-row — are called out explicitly
  and use the closest honest representation; see Task 2).
- `?` intercepted globally (works while picker filter focused); `?`/`esc`
  close; while open other keys swallowed.
- Existing goldens change ONLY by the added `· ? legend` footer hint (in
  practice: none of the three current golden files reach the picker/dashboard
  hint line at all — see Task 3 — so no golden file needs an update).

---

## File Structure

- **Modify** `internal/nav/model.go` — add `showLegend bool` to `Model`.
- **Modify** `internal/nav/update.go` — `?` toggle + swallow block in the
  `tea.KeyMsg` branch of `Update`.
- **Modify** `internal/nav/update_test.go` — toggle/swallow/filter-focus tests.
- **Modify** `internal/nav/view.go` — `legendEntry` type, `legendEntries`
  table, `viewLegend()`, `View()` early return, footer hint text on
  `viewPicker`/`viewDash`.
- **Modify** `internal/nav/view_test.go` — golden, completeness, no-phantom,
  footer-hint tests.
- **Create** `internal/nav/testdata/legend.golden` — golden render of the
  legend overlay (generated via `-update`, not hand-written).

No other files change. `filterRepos`, `visibleRepos`, and every other
call site are untouched.

---

## Task 1: `showLegend` state + global `?` toggle & swallow

**Files:**
- Modify: `internal/nav/model.go` (`Model` struct, ~line 12-19)
- Modify: `internal/nav/update.go` (`Update`'s `tea.KeyMsg` branch, ~line
  185-198)
- Test: `internal/nav/update_test.go`

**Interfaces:**
- Produces: `Model.showLegend bool` (new field, zero value `false`).
- Consumes: nothing new — reads `m.screen` (`screenPicker`/`screenDash`,
  `internal/nav/types.go:16-20`) and `msg.String()` (`tea.KeyMsg`).

- [ ] **Step 1: Write the failing tests**

Verify current `Model` (`internal/nav/model.go:12-50`) has no `showLegend`
field, and current `Update` (`internal/nav/update.go:185-198`) has no `?`
handling, so these fail to compile / fail on assertion:

```go
func TestUpdate_QuestionMark_TogglesLegendFromPicker(t *testing.T) {
	m := initialModel(Config{})
	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("?")})
	got := out.(Model)
	if !got.showLegend {
		t.Fatalf("? on picker should open the legend")
	}
}

func TestUpdate_QuestionMark_TogglesLegendOffAgain(t *testing.T) {
	m := initialModel(Config{})
	m.showLegend = true
	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("?")})
	got := out.(Model)
	if got.showLegend {
		t.Fatalf("? while legend open should close it")
	}
}

func TestUpdate_Esc_ClosesLegend(t *testing.T) {
	m := initialModel(Config{})
	m.showLegend = true
	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	got := out.(Model)
	if got.showLegend {
		t.Fatalf("esc while legend open should close it")
	}
}

func TestUpdate_LegendOpen_SwallowsOtherKeys(t *testing.T) {
	m := initialModel(Config{})
	m.showLegend = true
	m.pickerSel = 2
	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	got := out.(Model)
	if !got.showLegend {
		t.Errorf("a non-close key must not close the legend")
	}
	if got.pickerSel != 2 {
		t.Errorf("pickerSel = %d, want unchanged 2 (key should be swallowed)", got.pickerSel)
	}
}

func TestUpdate_QuestionMark_WorksWhileFilterFocused(t *testing.T) {
	m := initialModel(Config{}) // pickerFocus defaults to focusFilter, filter.Focused()
	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("?")})
	got := out.(Model)
	if !got.showLegend {
		t.Fatalf("? should open the legend even while the filter input is focused")
	}
	if got.filter.Value() != "" {
		t.Errorf("? must not be captured as filter text, got filter value %q", got.filter.Value())
	}
}

func TestUpdate_QuestionMark_TogglesLegendFromDash(t *testing.T) {
	m := initialModel(Config{})
	m.screen = screenDash
	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("?")})
	got := out.(Model)
	if !got.showLegend {
		t.Fatalf("? on the dashboard should open the legend")
	}
}

func TestUpdate_QuestionMark_IgnoredOnOverview(t *testing.T) {
	m := initialModel(Config{})
	m.screen = screenOverview
	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("?")})
	got := out.(Model)
	if got.showLegend {
		t.Errorf("? is not wired on the overview screen (out of scope) and must not open the legend")
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/nav/ -run 'TestUpdate_QuestionMark|TestUpdate_Esc_ClosesLegend|TestUpdate_LegendOpen_SwallowsOtherKeys' -v`
Expected: build failure (`m.showLegend`/`got.showLegend` undefined field) —
confirms the field doesn't exist yet.

- [ ] **Step 3: Add the field and the toggle/swallow block**

In `internal/nav/model.go`, add the field next to the other screen-state
fields:

```go
	screen      screen
	pickerFocus focus
	showLegend  bool // ? toggles the status-glyph legend overlay (picker/dash only)
```

In `internal/nav/update.go`, replace the `tea.KeyMsg` branch
(`internal/nav/update.go:185-198`):

```go
	case tea.KeyMsg:
		if m.cfg.DebugKeys != "" {
			logKey(m.cfg.DebugKeys, msg)
		}
		if m.screen == screenOverview {
			if msg.String() == "q" || msg.String() == "ctrl+c" {
				return m, tea.Quit
			}
			return m.updateOverviewKeys(msg)
		}
		if m.screen == screenPicker {
			return m.updatePicker(msg)
		}
		return m.updateDash(msg)
	}
```

with:

```go
	case tea.KeyMsg:
		if m.cfg.DebugKeys != "" {
			logKey(m.cfg.DebugKeys, msg)
		}
		if m.screen == screenOverview {
			if msg.String() == "q" || msg.String() == "ctrl+c" {
				return m, tea.Quit
			}
			return m.updateOverviewKeys(msg)
		}
		// Legend overlay: ? toggles it on picker/dashboard, ahead of the
		// picker/dash key routing below so it works even while the picker
		// filter input is focused (repo labels never contain "?", so a global
		// intercept costs nothing). While open, ?/esc/q close it and every
		// other key is swallowed so nothing leaks through to the underlying
		// screen.
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
		if m.screen == screenPicker {
			return m.updatePicker(msg)
		}
		return m.updateDash(msg)
	}
```

(`msg.String()` on a `tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("?")}`
returns `"?"`; on `tea.KeyMsg{Type: tea.KeyEsc}` it returns `"esc"` — both
already used elsewhere in this switch, e.g. the `screenOverview` branch
above.)

- [ ] **Step 4: Run tests**

Run: `go test ./internal/nav/ -run 'TestUpdate_QuestionMark|TestUpdate_Esc_ClosesLegend|TestUpdate_LegendOpen_SwallowsOtherKeys' -v`
Expected: PASS (all 7 new tests). Then the full package:
`go test ./internal/nav/`
Expected: green (no existing key-handling test observes `?`, so nothing else
is affected).

- [ ] **Step 5: Commit**

```bash
gofmt -l internal/nav/ ; go vet ./internal/nav/
git add internal/nav/model.go internal/nav/update.go internal/nav/update_test.go
git commit -m "feat(nav): add showLegend state and global ? toggle

Add Model.showLegend and intercept ? in Update's tea.KeyMsg branch, before
picker/dash key routing, so the legend opens even while the picker filter
input is focused. esc/q/? close it; every other key is swallowed while open.
Part of #157.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 2: `legendEntries` table + `viewLegend()` render

**Files:**
- Modify: `internal/nav/view.go` (styles block ~line 10-28; `View()` ~line
  57-68)
- Test: `internal/nav/view_test.go`
- Create: `internal/nav/testdata/legend.golden` (generated, not hand-written)

**Interfaces:**
- Produces: `type legendEntry struct { glyph string; style lipgloss.Style;
  meaning string; group string }`; `var legendEntries []legendEntry`;
  `func (m Model) viewLegend() string`.
- Consumes: `panel(w int, title, body string) string`
  (`internal/nav/view.go:34-42`); existing package styles `stOk`, `stMuted`,
  `stWarn`, `stBad`, `stAccent`, `stText` (`internal/nav/view.go:20-27`).

- [ ] **Step 1: Write the failing tests**

Append to `internal/nav/view_test.go`:

```go
func TestLegend_CoversAuditedGlyphs(t *testing.T) {
	type want struct{ glyph, meaning, group string }
	expected := []want{
		{"●", "session attached", "Session"},
		{"○", "session detached", "Session"},
		{"·", "no session (dashboard row)", "Session"},
		{"●N", "N modified/changed files", "Git status"},
		{"↑N", "N commits ahead of upstream", "Git status"},
		{"↓N", "N commits behind upstream", "Git status"},
		{"✓ clean", "nothing modified/diverged", "Git status"},
		{"⤳ no upstream", "branch has no upstream tracking ref", "Git status"},
		{"?", "dirty-state load error", "Git status"},
		{"⠋", "dirty-state loading (spinner)", "Git status"},
		{"↓ ", "remote-only repo (not cloned; clone on select)", "Rows & selection"},
		{"●N", "open-issue count on a repo row", "Rows & selection"},
		{"▸ ", "selected row (picker list/sessions, create row)", "Rows & selection"},
		{"+", "dashboard action row: create a new worktree", "Rows & selection"},
		{"●N open", "repo open-issue count", "Header"},
		{"✎ <names>", "present note files, e.g. ✎ ideas.md · TODO.md", "Header"},
	}
	if len(legendEntries) != len(expected) {
		t.Fatalf("legendEntries has %d entries, want %d — the legend must document exactly the audited glyph set", len(legendEntries), len(expected))
	}
	for i, e := range legendEntries {
		w := expected[i]
		if e.glyph == "" || e.meaning == "" {
			t.Errorf("entry %d: empty glyph or meaning: %+v", i, e)
		}
		if e.glyph != w.glyph || e.meaning != w.meaning || e.group != w.group {
			t.Errorf("entry %d = {%q,%q,%q}, want {%q,%q,%q}", i, e.glyph, e.meaning, e.group, w.glyph, w.meaning, w.group)
		}
	}
}

func TestLegend_NoPhantomGlyphs(t *testing.T) {
	src, err := os.ReadFile("view.go")
	if err != nil {
		t.Fatal(err)
	}
	// Ambiguous as bare substrings — legitimately absent from view.go (the
	// remote-only "↓ " prefix is emitted in data.go) or too generic to prove
	// anything by mere presence.
	skip := map[string]bool{"?": true, "+": true, "↓ ": true}
	distinctive := []string{"●", "○", "·", "↑", "↓", "✓", "⤳", "✎", "▸"}
	for _, e := range legendEntries {
		if skip[e.glyph] {
			continue
		}
		for _, r := range distinctive {
			if strings.Contains(e.glyph, r) && !strings.Contains(string(src), r) {
				t.Errorf("legend entry %q (%s) uses rune %q, not found anywhere in view.go", e.glyph, e.meaning, r)
			}
		}
	}
}

func TestViewLegend_Golden(t *testing.T) {
	m := initialModel(Config{})
	m.width, m.height = 100, 40
	assertGolden(t, "legend", m.viewLegend())
}

func TestView_ShowLegend_ReturnsLegendOverEitherScreen(t *testing.T) {
	m := initialModel(Config{})
	m.width, m.height = 100, 40
	m.showLegend = true
	for _, scr := range []screen{screenPicker, screenDash} {
		m.screen = scr
		out := m.View()
		if !strings.Contains(out, "session attached") {
			t.Errorf("screen %d: View() with showLegend=true should render the legend, got:\n%s", scr, out)
		}
	}
}
```

Add `"os"` to the `view_test.go` import block (it currently imports `"fmt"`,
`"strings"`, `"testing"`, `lipgloss`, `internal/core` — verify and add `"os"`
alongside them).

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/nav/ -run 'TestLegend_|TestViewLegend_|TestView_ShowLegend_' -v`
Expected: FAIL — `legendEntries` and `viewLegend` undefined (compile error).

- [ ] **Step 3: Add the table and the renderer**

In `internal/nav/view.go`, immediately after the styles block
(`internal/nav/view.go:10-28`):

```go
// legendEntry is one row of the ? status-glyph legend: a glyph rendered in
// its real style, a short meaning, and the group it's shown under.
type legendEntry struct {
	glyph   string
	style   lipgloss.Style
	meaning string
	group   string
}

// legendEntries documents every status glyph the picker/dashboard render. It
// is the single source viewLegend renders from, and is guarded by
// TestLegend_CoversAuditedGlyphs: adding, removing, or changing an entry here
// without updating that test's expected set fails the build. When you add or
// change a rendered status glyph, update this table (and the primary
// render site's comment pointing back here).
var legendEntries = []legendEntry{
	{"●", stOk, "session attached", "Session"},
	{"○", stMuted, "session detached", "Session"},
	{"·", stMuted, "no session (dashboard row)", "Session"},

	{"●N", stBad, "N modified/changed files", "Git status"},
	{"↑N", stWarn, "N commits ahead of upstream", "Git status"},
	{"↓N", stAccent, "N commits behind upstream", "Git status"},
	{"✓ clean", stOk, "nothing modified/diverged", "Git status"},
	{"⤳ no upstream", stMuted, "branch has no upstream tracking ref", "Git status"},
	{"?", stMuted, "dirty-state load error", "Git status"},
	// The spinner is a live widget (spinner.Model), not a static st*-styled
	// glyph; "⠋" (its first bubbles/spinner.Dot frame) stands in for it here.
	{"⠋", stMuted, "dirty-state loading (spinner)", "Git status"},

	{"↓ ", stMuted, "remote-only repo (not cloned; clone on select)", "Rows & selection"},
	{"●N", stWarn, "open-issue count on a repo row", "Rows & selection"},
	{"▸ ", stAccent, "selected row (picker list/sessions, create row)", "Rows & selection"},
	// The create-row action text renders unstyled (view.go:235), so its
	// legend entry uses the zero-value style — the honest representation.
	{"+", lipgloss.NewStyle(), "dashboard action row: create a new worktree", "Rows & selection"},

	{"●N open", stWarn, "repo open-issue count", "Header"},
	{"✎ <names>", stAccent, "present note files, e.g. ✎ ideas.md · TODO.md", "Header"},
}

// legendGroups is the fixed display order of legend sections.
var legendGroups = []string{"Session", "Git status", "Rows & selection", "Header"}

// viewLegend renders the ? overlay: legendEntries grouped by category, each
// glyph in its real style, followed by a close hint. Replaces the whole
// screen while open (View()'s early return), mirroring the viewRepoModal
// idiom (view.go:71-73).
func (m Model) viewLegend() string {
	var b strings.Builder
	for gi, group := range legendGroups {
		if gi > 0 {
			b.WriteString("\n")
		}
		b.WriteString(stTitle.Render(group) + "\n")
		for _, e := range legendEntries {
			if e.group != group {
				continue
			}
			b.WriteString(fmt.Sprintf("  %-14s %s\n", e.style.Render(e.glyph), e.meaning))
		}
	}
	b.WriteString("\n" + stMuted.Render("? / esc to close"))
	return panel(m.width, "Legend", strings.TrimRight(b.String(), "\n"))
}
```

In `View()` (`internal/nav/view.go:57-68`), add the early return before the
screen switch:

```go
func (m Model) View() string {
	if m.width == 0 || m.height == 0 {
		return "initialising…"
	}
	if m.showLegend {
		return m.viewLegend()
	}
	if m.screen == screenPicker {
		return m.viewPicker()
	}
	if m.screen == screenOverview {
		return m.viewOverview()
	}
	return m.viewDash()
}
```

- [ ] **Step 4: Run tests, generate the golden**

Run: `go test ./internal/nav/ -run 'TestLegend_|TestViewLegend_|TestView_ShowLegend_' -v`
Expected: `TestLegend_CoversAuditedGlyphs`, `TestLegend_NoPhantomGlyphs`, and
`TestView_ShowLegend_ReturnsLegendOverEitherScreen` PASS;
`TestViewLegend_Golden` FAILS (missing golden file), with the failure message
naming `testdata/legend.golden` and the `-update` command to create it.

Run: `go test ./internal/nav/ -run TestViewLegend_Golden -update`
Then: `go test ./internal/nav/ -run TestViewLegend_Golden -v`
Expected: PASS. Inspect `internal/nav/testdata/legend.golden` by hand — it
must show all four group headers, one line per entry in group order, and end
with `? / esc to close`.

Then the full package: `go test ./internal/nav/`
Expected: green.

- [ ] **Step 5: Commit**

```bash
gofmt -l internal/nav/ ; go vet ./internal/nav/
git add internal/nav/view.go internal/nav/view_test.go internal/nav/testdata/legend.golden
git commit -m "feat(nav): render the ? status-glyph legend overlay

Add the legendEntries table (single source, guarded by
TestLegend_CoversAuditedGlyphs + a no-phantom-glyph check against view.go's
source) and viewLegend(), wired into View() as an early return. Documents
every audited session/git-status/row/header glyph in its real style.
Part of #157.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 3: footer hint on picker and dashboard

**Files:**
- Modify: `internal/nav/view.go` (`viewPicker` hint ~line 146; `viewDash`
  hint ~line 192)
- Test: `internal/nav/view_test.go`

**Interfaces:**
- No signature changes. `hintLine(left string) string`
  (`internal/nav/view.go:621-632`) keeps its existing signature; only the
  `left` string passed in changes.

- [ ] **Step 1: Write the failing tests**

Append to `internal/nav/view_test.go`:

```go
func TestView_Picker_HintMentionsLegend(t *testing.T) {
	m := initialModel(Config{})
	m.width, m.height = 100, 30
	if out := m.View(); !strings.Contains(out, "? legend") {
		t.Errorf("picker hint should mention ? legend:\n%s", out)
	}
}

func TestView_Dash_HintMentionsLegend(t *testing.T) {
	m := initialModel(Config{})
	m.width, m.height = 100, 30
	m.screen = screenDash
	m.repo = core.Repo{Name: "bridge"}
	if out := m.View(); !strings.Contains(out, "? legend") {
		t.Errorf("dashboard hint should mention ? legend:\n%s", out)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/nav/ -run 'TestView_Picker_HintMentionsLegend|TestView_Dash_HintMentionsLegend' -v`
Expected: FAIL — neither hint string currently contains `? legend`.

- [ ] **Step 3: Append the hint**

In `internal/nav/view.go:146` (`viewPicker`), change:

```go
	sections = append(sections, m.hintLine("↑↓ move · g/G first/last · ⏎ open/attach · / filter · r refresh · ctrl+n new · tab panes · q quit"))
```

to:

```go
	sections = append(sections, m.hintLine("↑↓ move · g/G first/last · ⏎ open/attach · / filter · r refresh · ctrl+n new · tab panes · ? legend · q quit"))
```

In `internal/nav/view.go:192` (`viewDash`), change:

```go
	hint := m.hintLine("↑↓ move · tab panes · ⏎ attach/launch · n new worktree · esc back · q quit")
```

to:

```go
	hint := m.hintLine("↑↓ move · tab panes · ⏎ attach/launch · n new worktree · ? legend · esc back · q quit")
```

- [ ] **Step 4: Run tests, confirm existing goldens are untouched**

Run: `go test ./internal/nav/ -run 'TestView_Picker_HintMentionsLegend|TestView_Dash_HintMentionsLegend' -v`
Expected: PASS.

Run: `go test ./internal/nav/ -update && git status --short internal/nav/testdata/`
Expected: no diff. (Verified while writing this plan: none of the three
existing golden files — `ctrln_repo_modal_forge.golden`,
`picker_to_overview.golden`, `overview_with_roadmap.golden` — reach the
picker or dashboard hint line; `picker_to_overview` and
`overview_with_roadmap` capture the *overview* screen's own hint text
(`↑↓ move · tab pane · ⏎ show link/path · esc back · q quit`, unrelated and
untouched), and `ctrln_repo_modal_forge` captures `viewRepoModal`, which never
reaches `hintLine`. If this check surfaces any diff, stop and investigate —
it means a golden reaches picker/dash hint text that wasn't accounted for
here; do not regenerate blindly.)

Then the full package: `go test ./internal/nav/`
Expected: green.

- [ ] **Step 5: Commit**

```bash
gofmt -l internal/nav/ ; go vet ./internal/nav/
git add internal/nav/view.go internal/nav/view_test.go
git commit -m "feat(nav): surface '? legend' in the picker/dashboard hint

Appends '? legend' to both footer hints so the overlay is discoverable.
Confirmed no existing golden reaches this hint text (all three capture the
overview screen or the repo-create modal), so no golden changes. Closes #157.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 4: full verification

**Files:** none.

- [ ] **Step 1: Gates**

Run:
```bash
gofmt -l . | grep -v '.worktrees/'   # empty
go vet ./...                          # clean
go test -race ./...                   # all ok
```

- [ ] **Step 2: Lint (best-effort)**

Run: `golangci-lint run ./internal/nav/...` (if installed). Else note it;
`go vet` is the gate.

- [ ] **Step 3: Golden stability**

Run: `go test ./internal/nav/ -update && git status --short internal/nav/testdata/`
Expected: no diff (Task 3 already established this; this is the final
confirmation after all three tasks land together).

- [ ] **Step 4: Manual smoke (best-effort)**

Run:
```bash
just build
bridge nav
# From the picker: press ? -> legend opens, shows Session/Git status/
#   Rows & selection/Header groups; press ? again -> closes back to the
#   picker. Press / to focus the filter, then ? -> legend still opens.
# Enter a repo to reach the dashboard: press ? -> legend opens; press esc
#   -> closes back to the dashboard (not out to the picker).
```

- [ ] **Step 5: Report**

Report Steps 1-3 output + the Step 4 smoke result. No success claims without
output.

---

## Notes for the implementer

- **Insertion point is load-bearing.** The `?`/swallow block in `Update` must
  sit after the `screenOverview` branch (the legend is out of scope there per
  the spec's non-goals) but before the `screenPicker`/`updateDash` dispatch —
  that ordering is what lets `?` reach the toggle even while
  `m.pickerFocus == focusFilter` and the filter `textinput` would otherwise
  consume the rune.
- **`legendEntries` order is the render order and the test order.** Both
  `TestLegend_CoversAuditedGlyphs` and `viewLegend`'s per-group filtering
  depend on the table being grouped in `legendGroups` order internally (Task 2
  already lists entries grouped); don't reorder without updating the test's
  `expected` slice in lockstep.
- **The `★` base-checkout glyph (#182) is not on this branch** (lives on
  unmerged `feature/182-nav-base-checkout-row`) — confirmed by `grep -rn "★"
  internal/nav/*.go` returning nothing here. Do not add a legend entry for it;
  if #182 merges first, add `{"★", stAccent, "base checkout (main)", "Rows &
  selection"}` and extend both the `legendEntries` table and
  `TestLegend_CoversAuditedGlyphs`'s expected set together, in one commit.
- **Overview screen is explicitly out of scope** (per spec Non-goals): its own
  glyphs (`◐`/`⚖`/`•`/`⚠`) and its own hint line are untouched; `?` is not
  wired there (Task 1's `TestUpdate_QuestionMark_IgnoredOnOverview` guards
  this).
- If you hit a blocker, find the fix and note it inline here.
