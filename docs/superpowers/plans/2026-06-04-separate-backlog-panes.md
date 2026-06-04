# Separate Backlog Panes (Open Issues / Ideas / Todos) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** On the `bridge nav` dashboard, render Open Issues, Ideas, and Todos as three always-visible stacked panes when a backlog pane is focused, splitting today's combined Notes pane into per-file Ideas and Todos panes, while preserving the worktree Details view.

**Architecture:** Pure refactor of `internal/nav`. The contextual right column gains two modes: **Details** (when the Worktrees list is focused, unchanged) and **Backlog** (when any backlog pane is focused — three stacked panels). The single `notesScroll` + combined `Notes` renderer is replaced by per-file scroll offsets (`ideasScroll`, `todosScroll`) and a per-file panel renderer. Tab becomes a fixed 4-stop cycle. Reuses the existing `panel`/`stretchPanel`/`clampInt` helpers and the unchanged `readRepoNotes` loader.

**Tech Stack:** Go, Bubble Tea (Model-Update-View), Lipgloss. Standard-library `testing`, table-driven, hand-rolled fakes. Race detector on.

**Spec:** `docs/superpowers/specs/2026-06-04-separate-backlog-panes-design.md`

---

## File Structure

All files already exist under `internal/nav/`:

- `notes.go` — note-file loader; **add** the file-name constants and the `noteByName`/`ideaNote`/`todoNote`/`noteLineCount` selection helpers.
- `notes_test.go` — **add** helper tests; **replace** the four tests that assert the old single-Notes behavior.
- `types.go` — `dashFocus` enum: **replace** `dashFocusNotes` with `dashFocusIdeas` + `dashFocusTodos`.
- `model.go` — `Model` struct: **replace** `notesScroll` with `ideasScroll` + `todosScroll`.
- `view.go` — **add** `noteFileBody`, `noteFilePanel`, `ideasPanel`, `todosPanel`, `backlogColumn`; **rewire** `viewDash`; **delete** `notesPanel`, `notesBody`, `noteDisplayLines`.
- `update.go` — fixed `dashPaneCycle`; seed in `cycledDashFocus`; dispatch + `updateDashIdeas`/`updateDashTodos`/`scrollByKey`; **delete** `updateDashNotes`, `notesTotalLines`; drop the empty-pane focus fallbacks; reset both scrolls.
- `update_test.go` — **delete** three tests that assert the now-removed empty-issues focus fallback and the old conditional Tab cycle.
- `CHANGELOG.md` — add an `[Unreleased]` entry.

Task 1 is purely additive and isolated (compiles + lints green on its own). Task 2 is the coupled core: the `dashFocus` enum and the shared `notesState` make the view/update/model changes a single atomic edit — splitting them would leave the package uncompilable mid-way. Its checkbox steps keep it bite-sized (tests first, then file-by-file, then gate).

---

## Task 1: Note-file selection helpers

**Files:**
- Modify: `internal/nav/notes.go`
- Test: `internal/nav/notes_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `internal/nav/notes_test.go`:

```go
func TestNoteByName_CaseInsensitiveAndAbsent(t *testing.T) {
	m := Model{notes: []noteFile{
		{name: "Ideas.md", lines: []string{"a"}},
		{name: "todo.md", lines: []string{"b"}},
	}}
	if nf := m.ideaNote(); nf == nil || nf.name != "Ideas.md" {
		t.Errorf("ideaNote should match Ideas.md case-insensitively, got %+v", nf)
	}
	if nf := m.todoNote(); nf == nil || nf.name != "todo.md" {
		t.Errorf("todoNote should match todo.md case-insensitively, got %+v", nf)
	}
	empty := Model{}
	if empty.ideaNote() != nil || empty.todoNote() != nil {
		t.Errorf("absent files must resolve to nil")
	}
}

func TestNoteLineCount(t *testing.T) {
	if got := noteLineCount(nil); got != 0 {
		t.Errorf("nil => 0, got %d", got)
	}
	if got := noteLineCount(&noteFile{binary: true}); got != 1 {
		t.Errorf("binary => 1, got %d", got)
	}
	if got := noteLineCount(&noteFile{lines: []string{"x", "y"}}); got != 2 {
		t.Errorf("text => len(lines), got %d", got)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/nav -run 'TestNoteByName|TestNoteLineCount' -v`
Expected: compile error — `m.ideaNote undefined` / `noteLineCount undefined`.

- [ ] **Step 3: Add the constants and helpers**

In `internal/nav/notes.go`, replace the `noteFileNames` declaration:

```go
var noteFileNames = []string{"ideas.md", "TODO.md"}
```

with:

```go
// ideasFileName / todoFileName are the two conventional repo-root backlog files
// surfaced as the Ideas and Todos panes. The set is intentionally fixed.
const (
	ideasFileName = "ideas.md"
	todoFileName  = "TODO.md"
)

var noteFileNames = []string{ideasFileName, todoFileName}
```

Then append to `internal/nav/notes.go` (after `readNote`):

```go
// noteByName returns the loaded note file matching want (case-insensitive on the
// file name), or nil when that file is absent for the dashboard repo.
func (m Model) noteByName(want string) *noteFile {
	for i := range m.notes {
		if strings.EqualFold(m.notes[i].name, want) {
			return &m.notes[i]
		}
	}
	return nil
}

// ideaNote returns the loaded ideas.md note, or nil when absent.
func (m Model) ideaNote() *noteFile { return m.noteByName(ideasFileName) }

// todoNote returns the loaded TODO.md note, or nil when absent.
func (m Model) todoNote() *noteFile { return m.noteByName(todoFileName) }

// noteLineCount is the number of scrollable display lines a note pane renders for
// nf: zero when absent, one for the binary marker, else the text line count.
func noteLineCount(nf *noteFile) int {
	if nf == nil {
		return 0
	}
	if nf.binary {
		return 1
	}
	return len(nf.lines)
}
```

(`strings` is already imported in `notes.go`.)

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/nav -run 'TestNoteByName|TestNoteLineCount' -v`
Expected: PASS. Then `go build ./...` — Expected: clean.

- [ ] **Step 5: Commit**

```bash
git add internal/nav/notes.go internal/nav/notes_test.go
git commit -m "refactor(nav): add per-file note selection helpers

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 2: Three always-visible backlog panes with a 4-stop cycle

This task replaces the combined Notes pane and the contextual single right pane with the three-pane backlog column and the fixed Tab cycle. Because the `dashFocus` enum value `dashFocusNotes` and the single `notesScroll`/`notesState` are referenced across `types.go`, `model.go`, `view.go`, `update.go`, and existing tests, the edits land together. Write the new tests first, watch them fail, then apply the edits file-by-file, then gate.

**Files:**
- Modify: `internal/nav/types.go`
- Modify: `internal/nav/model.go`
- Modify: `internal/nav/view.go`
- Modify: `internal/nav/update.go`
- Test: `internal/nav/notes_test.go` (replace 4 tests, add 5)
- Test: `internal/nav/update_test.go` (delete 3 obsolete tests)

### Behavior changes this task makes (so the test replacements are intentional, not test-fudging)

- `dashFocusNotes` → split into `dashFocusIdeas` + `dashFocusTodos`.
- Tab always stops on all four panes (Worktrees, Issues, Ideas, Todos), even when a source is empty — so the old "fall back to Worktrees when the pane is empty" logic in the `repoIssuesMsg`/`notesMsg` handlers is **removed**.
- The combined `Notes` pane (single `notesScroll`) becomes two independently-scrolled panes.

- [ ] **Step 1: Replace the obsolete tests and add the new behavior tests**

In `internal/nav/notes_test.go`, **delete** these four tests entirely (they assert removed behavior/symbols): `TestUpdate_NotesMsg_LoadsAndClearsFocusWhenEmpty`, `TestUpdateDash_TabCyclesThroughNotes`, `TestUpdateDashNotes_ScrollClamped`, `TestViewDash_Wide_NotesFocus_RendersContent`.

In `internal/nav/update_test.go`, **delete** these three tests entirely (they assert behavior this change removes — the empty-issues focus fallback and the old content-conditional Tab cycle): `TestUpdate_RepoIssuesMsg_EmptyDropsIssuesFocus`, `TestUpdateDash_TabTogglesIssuesFocus`, `TestUpdateDash_TabNoIssues_StaysOnWorktrees`. The new `TestUpdateDash_TabCyclesAllFourPanes` (below) supersedes the latter two by asserting Tab stops on all four panes regardless of content.

Keep `TestViewDash_HeaderShowsNotesChip` and `TestViewDash_Narrow_StacksNotesPanel` (they still pass — the header chip and narrow stacking are unchanged for present files).

Add these replacements to `internal/nav/notes_test.go`:

```go
func TestUpdate_NotesMsg_LoadsAndKeepsFocus(t *testing.T) {
	m := initialModel(Config{})
	m.screen = screenDash
	m.dashFocus = dashFocusIdeas
	m.ideasScroll = 5
	m.todosScroll = 7
	updated, _ := m.Update(notesMsg{notes: nil})
	m = updated.(Model)
	if m.notesState != loadOK {
		t.Errorf("notesState should be loadOK after notesMsg")
	}
	// Panes are always shown now, so focus stays put even when empty.
	if m.dashFocus != dashFocusIdeas {
		t.Errorf("focus should stay on Ideas; panes are always present, got %d", m.dashFocus)
	}
	if m.ideasScroll != 0 || m.todosScroll != 0 {
		t.Errorf("notesMsg should reset both scroll offsets, got ideas=%d todos=%d", m.ideasScroll, m.todosScroll)
	}
}

func TestUpdateDash_TabCyclesAllFourPanes(t *testing.T) {
	m := initialModel(Config{})
	m.screen = screenDash
	m.repo = core.Repo{Name: "bridge"}
	// Deliberately leave issues/notes empty: Tab must still stop on every pane.
	tab := tea.KeyMsg{Type: tea.KeyTab}
	step := func(mod Model) Model {
		u, _ := mod.updateDash(tab)
		return u.(Model)
	}
	want := []dashFocus{dashFocusIssues, dashFocusIdeas, dashFocusTodos, dashFocusWorktrees}
	for i, w := range want {
		m = step(m)
		if m.dashFocus != w {
			t.Fatalf("tab %d => %d, want %d", i+1, m.dashFocus, w)
		}
	}
}

func TestUpdateDashScroll_IdeasAndTodosIndependent(t *testing.T) {
	m := initialModel(Config{})
	m.screen = screenDash
	m.notes = []noteFile{
		{name: "ideas.md", lines: []string{"i1", "i2", "i3"}},
		{name: "TODO.md", lines: []string{"t1", "t2", "t3"}},
	}

	down := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")}

	// Scrolling Ideas moves only ideasScroll.
	mi := m
	mi.dashFocus = dashFocusIdeas
	u, _ := mi.updateDashIdeas(down)
	mi = u.(Model)
	if mi.ideasScroll != 1 || mi.todosScroll != 0 {
		t.Errorf("ideas scroll should be independent: ideas=%d todos=%d", mi.ideasScroll, mi.todosScroll)
	}

	// Scrolling Todos moves only todosScroll.
	mt := m
	mt.dashFocus = dashFocusTodos
	u, _ = mt.updateDashTodos(down)
	mt = u.(Model)
	if mt.todosScroll != 1 || mt.ideasScroll != 0 {
		t.Errorf("todos scroll should be independent: ideas=%d todos=%d", mt.ideasScroll, mt.todosScroll)
	}

	// Up past the top clamps at 0.
	up := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("k")}
	u, _ = m.updateDashIdeas(up)
	if got := u.(Model).ideasScroll; got != 0 {
		t.Errorf("ideas scroll must clamp at 0, got %d", got)
	}
}

func TestViewDash_Wide_BacklogShowsThreePanesWithPlaceholders(t *testing.T) {
	m := initialModel(Config{})
	m.width, m.height = 130, 40
	m.screen = screenDash
	m.repo = core.Repo{Name: "bridge"}
	m.dashRows = []dashRow{{worktree: "fix-x", path: "/r/fix-x"}}
	m.issuesState = loadOK
	m.notesState = loadOK
	// Only ideas.md present; TODO.md absent must show a placeholder, not vanish.
	m.notes = []noteFile{{name: "ideas.md", lines: []string{"faster picker"}}}
	m.dashFocus = dashFocusIdeas

	out := m.View()
	for _, want := range []string{"Open issues", "Ideas", "Todos", "faster picker", "no " + todoFileName} {
		if !strings.Contains(out, want) {
			t.Errorf("backlog-focused dash missing %q\n%s", want, out)
		}
	}
}

func TestViewDash_Wide_WorktreeFocus_ShowsDetailsNotBacklog(t *testing.T) {
	m := initialModel(Config{})
	m.width, m.height = 130, 40
	m.screen = screenDash
	m.repo = core.Repo{Name: "bridge"}
	m.dashRows = []dashRow{{worktree: "fix-x", path: "/r/fix-x"}}
	m.dashSel = 0
	m.dashFocus = dashFocusWorktrees
	m.notesState = loadOK
	m.notes = []noteFile{{name: "ideas.md", lines: []string{"faster picker"}}}

	out := m.View()
	if !strings.Contains(out, "Branches") {
		t.Errorf("worktree focus should render Details (Branches) column:\n%s", out)
	}
	if strings.Contains(out, "faster picker") {
		t.Errorf("worktree focus must not render the Ideas body:\n%s", out)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/nav -run 'TestUpdate_NotesMsg_LoadsAndKeepsFocus|TestUpdateDash_TabCyclesAllFourPanes|TestUpdateDashScroll|TestViewDash_Wide_Backlog|TestViewDash_Wide_WorktreeFocus' -v`
Expected: compile error — `dashFocusIdeas undefined`, `m.ideasScroll undefined`, `m.updateDashIdeas undefined`, etc.

- [ ] **Step 3: Split the focus enum (`types.go`)**

In `internal/nav/types.go`, replace the `dashFocus` const block and its doc comment:

```go
// dashFocus is which Screen-2 pane has the keyboard: the worktree list (left)
// or one of the contextual right-column panes (open issues, repo notes). Tab
// cycles through the panes that have content (see Model.dashPaneCycle).
type dashFocus int

const (
	dashFocusWorktrees dashFocus = iota
	dashFocusIssues
	dashFocusNotes
)
```

with:

```go
// dashFocus is which Screen-2 pane has the keyboard: the worktree list (left)
// or one of the three right-column backlog panes (open issues, ideas, todos).
// Tab cycles through all four in order (see Model.dashPaneCycle); when the
// worktree list is focused the right column shows worktree Details instead.
type dashFocus int

const (
	dashFocusWorktrees dashFocus = iota
	dashFocusIssues
	dashFocusIdeas
	dashFocusTodos
)
```

- [ ] **Step 4: Split the scroll state (`model.go`)**

In `internal/nav/model.go`, replace:

```go
	notes       []noteFile
	notesScroll int // top display-line offset of the notes pane
	notesState  loadState
```

with:

```go
	notes       []noteFile
	ideasScroll int // top display-line offset of the Ideas pane
	todosScroll int // top display-line offset of the Todos pane
	notesState  loadState
```

- [ ] **Step 5: Add the per-file panels and the backlog column, and rewire `viewDash` (`view.go`)**

In `internal/nav/view.go`, inside `viewDash`, replace the wide-branch `rightAt` closure:

```go
		rightAt := func(h int) string {
			switch m.dashFocus {
			case dashFocusIssues:
				return m.issuesPanel(rightW, h)
			case dashFocusNotes:
				return m.notesPanel(rightW, h)
			default:
				return m.detailColumn(rightW, h)
			}
		}
```

with:

```go
		// Right column has two modes: the selected worktree's Details when the
		// worktree list is focused, otherwise the three stacked backlog panes.
		rightAt := func(h int) string {
			if m.dashFocus == dashFocusWorktrees {
				return m.detailColumn(rightW, h)
			}
			return m.backlogColumn(rightW, h)
		}
```

In the narrow branch of `viewDash`, replace:

```go
		parts := []string{panel(w, "Sessions & Worktrees", m.dashListBody(false))}
		// Narrow layout has no detail column, so stack the issues pane below the
		// worktree list when the repo has any (or is still loading them).
		if m.issuesState == loadPending || len(m.issues) > 0 {
			parts = append(parts, m.issuesPanel(w, 0))
		}
		if m.notesState == loadPending || len(m.notes) > 0 {
			parts = append(parts, m.notesPanel(w, 0))
		}
		body = strings.Join(parts, "\n")
```

with:

```go
		// Narrow layout has no detail column, so stack the three backlog panes
		// below the worktree list. They are always shown (with placeholders) so
		// the layout is stable and a missing TODO.md is visible.
		parts := []string{
			panel(w, "Sessions & Worktrees", m.dashListBody(false)),
			m.issuesPanel(w, 0),
			m.ideasPanel(w, 0),
			m.todosPanel(w, 0),
		}
		body = strings.Join(parts, "\n")
```

Then **replace** the `notesPanel`, `notesBody`, and `noteDisplayLines` functions (the block from the `// notesPanel renders…` comment through the end of `noteDisplayLines`) with the backlog column + per-file renderer:

```go
// backlogColumn stacks the three backlog panes (Open Issues, Ideas, Todos) as
// the dashboard's right column when a backlog pane is focused. Each pane gets
// roughly a third of the height; the last absorbs the slack so the column
// bottom-aligns with the worktree list. minH <= 0 renders at natural height.
func (m Model) backlogColumn(w, minH int) string {
	per := (m.height - 14) / 3
	if per < 3 {
		per = 3
	}
	issues := m.issuesPanel(w, per)
	ideas := m.ideasPanel(w, per)
	todosH := minH - lipgloss.Height(issues) - lipgloss.Height(ideas)
	todos := m.todosPanel(w, todosH)
	return lipgloss.JoinVertical(lipgloss.Left, issues, ideas, todos)
}

// ideasPanel renders the repo-root ideas.md pane (a single bordered panel),
// scrolled by ideasScroll and stretched to at least minH.
func (m Model) ideasPanel(w, minH int) string {
	return m.noteFilePanel(w, minH, dashFocusIdeas, "Ideas", ideasFileName, m.ideaNote(), m.ideasScroll)
}

// todosPanel renders the repo-root TODO.md pane (a single bordered panel),
// scrolled by todosScroll and stretched to at least minH.
func (m Model) todosPanel(w, minH int) string {
	return m.noteFilePanel(w, minH, dashFocusTodos, "Todos", todoFileName, m.todoNote(), m.todosScroll)
}

// noteFilePanel renders one backlog note file as a bordered, scrollable panel.
// The title carries the on-disk file name when present and a scroll hint when
// focused. wantName names the file for the "no <name>" placeholder when absent.
func (m Model) noteFilePanel(w, minH int, focus dashFocus, label, wantName string, nf *noteFile, scroll int) string {
	title := label
	if nf != nil {
		title += "  " + stMuted.Render("· "+nf.name)
	}
	if m.dashFocus == focus {
		title += "  " + stMuted.Render("(↑↓ scroll)")
	}
	return stretchPanel(w, minH, title, m.noteFileBody(w, minH, wantName, nf, scroll))
}

// noteFileBody renders a single note file's windowed text. It shares the
// loading/error states with the other backlog notes (one notesState load), then
// shows a placeholder when the file is absent, the binary marker, or the
// scrolled text window with overflow markers.
func (m Model) noteFileBody(w, minH int, wantName string, nf *noteFile, scroll int) string {
	if text, ok := m.panelState(m.notesState); !ok {
		return text
	}
	if nf == nil {
		return stMuted.Render("no " + wantName)
	}
	if nf.binary {
		return stMuted.Render("(binary or non-text content)")
	}
	if len(nf.lines) == 0 {
		return stMuted.Render("(empty)")
	}
	// Reserve panel chrome (border + title + blank lines + overflow markers) when
	// budgeting visible rows; fall back to a small window at natural height.
	maxVisible := minH - 6
	if maxVisible < 3 {
		maxVisible = 3
	}
	start := clampInt(scroll, 0, max(0, len(nf.lines)-maxVisible))
	end := min(len(nf.lines), start+maxVisible)
	var b strings.Builder
	if start > 0 {
		b.WriteString(stMuted.Render(fmt.Sprintf("  ↑ %d more", start)) + "\n")
	}
	for i := start; i < end; i++ {
		b.WriteString(stText.Render(trunc(nf.lines[i], w-4)) + "\n")
	}
	if end < len(nf.lines) {
		b.WriteString(stMuted.Render(fmt.Sprintf("  ↓ %d more", len(nf.lines)-end)) + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}
```

Leave `notesNames` (the header chip) untouched. (`fmt`, `strings`, `lipgloss` are already imported in `view.go`.)

- [ ] **Step 6: Rewire the Update layer (`update.go`)**

In `internal/nav/update.go`:

(a) In the `repoIssuesMsg` handler, **delete** the empty-pane focus fallback so Tab can still rest on an empty Issues pane:

```go
		if len(m.issues) == 0 && m.dashFocus == dashFocusIssues {
			m.dashFocus = dashFocusWorktrees
		}
```

(b) Replace the `notesMsg` handler body:

```go
	case notesMsg:
		m.notes = msg.notes
		m.notesState = loadOK
		m.notesScroll = 0
		if len(m.notes) == 0 && m.dashFocus == dashFocusNotes {
			m.dashFocus = dashFocusWorktrees
		}
		return m, nil
```

with:

```go
	case notesMsg:
		m.notes = msg.notes
		m.notesState = loadOK
		m.ideasScroll = 0
		m.todosScroll = 0
		return m, nil
```

(c) In `updateDash`, replace the focus dispatch:

```go
	switch m.dashFocus {
	case dashFocusIssues:
		return m.updateDashIssues(msg)
	case dashFocusNotes:
		return m.updateDashNotes(msg)
	}
	return m.updateDashWorktrees(msg)
```

with:

```go
	switch m.dashFocus {
	case dashFocusIssues:
		return m.updateDashIssues(msg)
	case dashFocusIdeas:
		return m.updateDashIdeas(msg)
	case dashFocusTodos:
		return m.updateDashTodos(msg)
	}
	return m.updateDashWorktrees(msg)
```

(d) Replace `dashPaneCycle` with the fixed four stops:

```go
// dashPaneCycle is the ordered set of panes the dashboard's Tab key rotates
// through: the worktree list, then the open-issues pane (when the repo has open
// issues) and the notes pane (when the repo has note files). Empty panes are
// skipped so Tab never lands on a box with nothing to show.
func (m Model) dashPaneCycle() []dashFocus {
	cycle := []dashFocus{dashFocusWorktrees}
	if len(m.issues) > 0 {
		cycle = append(cycle, dashFocusIssues)
	}
	if len(m.notes) > 0 {
		cycle = append(cycle, dashFocusNotes)
	}
	return cycle
}
```

with:

```go
// dashPaneCycle is the fixed order the dashboard's Tab key rotates through:
// the worktree list, then the three always-visible backlog panes. The backlog
// panes are always shown (with placeholders when empty), so Tab stops on every
// one regardless of content.
func (m Model) dashPaneCycle() []dashFocus {
	return []dashFocus{dashFocusWorktrees, dashFocusIssues, dashFocusIdeas, dashFocusTodos}
}
```

(e) Replace the seeding switch in `cycledDashFocus`:

```go
	m.dashFocus = cycle[idx]
	switch m.dashFocus {
	case dashFocusIssues:
		m.issueSel = clampInt(m.issueSel, 0, len(m.issues)-1)
	case dashFocusNotes:
		m.notesScroll = clampInt(m.notesScroll, 0, max(0, m.notesTotalLines()-1))
	}
	return m
```

with:

```go
	m.dashFocus = cycle[idx]
	switch m.dashFocus {
	case dashFocusIssues:
		m.issueSel = clampInt(m.issueSel, 0, len(m.issues)-1)
	case dashFocusIdeas:
		m.ideasScroll = clampInt(m.ideasScroll, 0, max(0, noteLineCount(m.ideaNote())-1))
	case dashFocusTodos:
		m.todosScroll = clampInt(m.todosScroll, 0, max(0, noteLineCount(m.todoNote())-1))
	}
	return m
```

(f) Replace `updateDashNotes` and `notesTotalLines` (the block from `// updateDashNotes handles scrolling…` through the end of `notesTotalLines`) with the shared scroll helper and the two per-pane handlers:

```go
// updateDashIdeas handles scrolling when the Ideas pane holds focus.
func (m Model) updateDashIdeas(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	m.ideasScroll = scrollByKey(msg, m.ideasScroll, noteLineCount(m.ideaNote()), m.listPage())
	return m, nil
}

// updateDashTodos handles scrolling when the Todos pane holds focus.
func (m Model) updateDashTodos(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	m.todosScroll = scrollByKey(msg, m.todosScroll, noteLineCount(m.todoNote()), m.listPage())
	return m, nil
}

// scrollByKey maps a scroll key to a new top-line offset, clamped to [0, total-1].
// page is the PgUp/PgDown step. total is the owning file's display-line count.
func scrollByKey(msg tea.KeyMsg, scroll, total, page int) int {
	switch msg.String() {
	case "up", "k":
		scroll--
	case "down", "j":
		scroll++
	case "home", "g":
		scroll = 0
	case "end", "G":
		scroll = total
	case "pgup", "ctrl+u":
		scroll -= page
	case "pgdown", "ctrl+d":
		scroll += page
	}
	return clampInt(scroll, 0, max(0, total-1))
}
```

(g) In `enterDash`, replace:

```go
	m.notes = nil
	m.notesScroll = 0
	m.notesState = loadPending
```

with:

```go
	m.notes = nil
	m.ideasScroll = 0
	m.todosScroll = 0
	m.notesState = loadPending
```

- [ ] **Step 7: Run the new tests to verify they pass**

Run: `go test ./internal/nav -run 'TestUpdate_NotesMsg_LoadsAndKeepsFocus|TestUpdateDash_TabCyclesAllFourPanes|TestUpdateDashScroll|TestViewDash_Wide_Backlog|TestViewDash_Wide_WorktreeFocus' -v`
Expected: all PASS.

- [ ] **Step 8: Run the full gate**

Run each, expect clean/green:

```bash
gofmt -l internal/nav
go vet ./...
golangci-lint run
go test -race ./...
```

Expected: `gofmt -l` prints nothing; vet/lint clean; full suite passes (the two kept tests `TestViewDash_HeaderShowsNotesChip` and `TestViewDash_Narrow_StacksNotesPanel` still pass — the header chip and narrow stacking of a present TODO.md are unchanged). If `golangci-lint` reports any symbol as unused, confirm `notesPanel`/`notesBody`/`noteDisplayLines`/`updateDashNotes`/`notesTotalLines` were fully removed in Steps 5–6.

- [ ] **Step 9: Commit**

```bash
git add internal/nav/types.go internal/nav/model.go internal/nav/view.go internal/nav/update.go internal/nav/notes_test.go internal/nav/update_test.go
git commit -m "feat(nav): show Open Issues / Ideas / Todos as three panes

Splits the contextual Notes pane into separate, independently-scrolled
Ideas and Todos panes and shows them stacked with Open Issues whenever a
backlog pane is focused. Tab is now a fixed 4-stop cycle (Worktrees ->
Issues -> Ideas -> Todos); the worktree list keeps its Details column.
Empty/missing sources render a placeholder so the layout stays stable.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 3: Changelog and manual verification

**Files:**
- Modify: `CHANGELOG.md`

- [ ] **Step 1: Add the changelog entry**

In `CHANGELOG.md`, under `## [Unreleased]` → `### Changed` (create the `### Changed` subsection directly after `### Added` if it does not exist), add:

```markdown
- `bridge nav` dashboard: **Open Issues**, **Ideas**, and **Todos** are now three separate, always-visible panes stacked in the right column (previously a single Notes pane combined `ideas.md` and `TODO.md`, and you had to Tab between Issues and Notes to see each). Each scrolls independently; missing sources show a placeholder. Tab cycles a fixed Worktrees → Issues → Ideas → Todos loop, and the worktree list keeps its Branches/Commits/Status detail column.
```

- [ ] **Step 2: Commit**

```bash
git add CHANGELOG.md
git commit -m "docs(changelog): note the split backlog panes

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

- [ ] **Step 3: Manual verification (TUI is interactive — exercise by hand)**

`Update`/`View` are covered by tests, but the visual layout needs a real terminal. Run `bridge nav`, open a repo that has both `ideas.md` and `TODO.md` plus open issues, and confirm:

1. Pressing **Tab** from the worktree list moves through Open Issues → Ideas → Todos and back; the right column shows all three stacked panes once focus leaves the worktree list.
2. Focusing the worktree list again shows the Branches/Commits/Status Details column (not the backlog).
3. Scrolling within **Ideas** does not move **Todos**, and vice-versa.
4. In a repo with no `TODO.md`, the Todos pane still shows with a `no TODO.md` placeholder and Tab still stops on it.
5. Shrink the terminal below 90 columns: the three backlog panes stack below the worktree list, each present.

Note in the PR that items 1–5 were verified manually (the TUI has no automated end-to-end harness).

---

## Notes for the implementer

- **Do not** add dependencies, change `go.mod`, or introduce new helpers beyond those specified — this is a contained refactor of one package.
- The seven deleted tests in Task 2 (four in `notes_test.go`, three in `update_test.go`) assert behavior this change intentionally removes (single Notes pane, empty-pane focus fallback, content-conditional Tab cycle). Replacing them is correct; do **not** instead weaken the new tests to match old code.
- Keep `gofmt`/`go vet`/`golangci-lint`/`go test -race ./...` green at every commit (Task 1 and Task 2 each leave the tree green).
