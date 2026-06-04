package nav

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/freaxnx01/bridge/internal/core"
)

func TestReadRepoNotes_PresentAbsentAndOrder(t *testing.T) {
	dir := t.TempDir()
	// TODO.md present (canonical case), ideas.md present in a different case.
	if err := os.WriteFile(filepath.Join(dir, "Ideas.md"), []byte("- ship nav\n- write docs\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "TODO.md"), []byte("buy milk\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// An unrelated markdown file must be ignored.
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	notes := readRepoNotes(dir)
	if len(notes) != 2 {
		t.Fatalf("want 2 notes, got %d (%v)", len(notes), notes)
	}
	// noteFileNames order is ideas before TODO; on-disk casing is preserved.
	if notes[0].name != "Ideas.md" || notes[1].name != "TODO.md" {
		t.Errorf("unexpected names/order: %q, %q", notes[0].name, notes[1].name)
	}
	if got := notes[0].lines; len(got) != 2 || got[0] != "- ship nav" {
		t.Errorf("ideas lines = %v", got)
	}
}

func TestReadRepoNotes_NoneAndMissingDir(t *testing.T) {
	dir := t.TempDir()
	if n := readRepoNotes(dir); n != nil {
		t.Errorf("empty repo should yield no notes, got %v", n)
	}
	if n := readRepoNotes(filepath.Join(dir, "does-not-exist")); n != nil {
		t.Errorf("missing dir should yield nil, got %v", n)
	}
}

func TestReadNote_BinaryFlagged(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "TODO.md")
	if err := os.WriteFile(path, []byte("text\x00\xff\xfebinary"), 0o644); err != nil {
		t.Fatal(err)
	}
	nf, ok := readNote("TODO.md", path)
	if !ok || !nf.binary {
		t.Fatalf("expected binary flag, got ok=%v nf=%+v", ok, nf)
	}
	if len(nf.lines) != 0 {
		t.Errorf("binary file should have no text lines, got %v", nf.lines)
	}
}

func TestReadNote_TruncatesLargeFileWithoutMisflaggingUTF8(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ideas.md")
	// Multibyte runes so a byte-cap cut lands mid-rune; the reader must drop the
	// partial tail rather than flag the whole file as binary.
	big := strings.Repeat("é", notesMaxBytes) // 2 bytes each => well over the cap
	if err := os.WriteFile(path, []byte(big), 0o644); err != nil {
		t.Fatal(err)
	}
	nf, ok := readNote("ideas.md", path)
	if !ok {
		t.Fatal("read failed")
	}
	if !nf.truncated {
		t.Error("large file should be flagged truncated")
	}
	if nf.binary {
		t.Error("valid UTF-8 file must not be flagged binary after truncation")
	}
	if len(nf.lines) == 0 {
		t.Error("truncated text file should still have preview lines")
	}
}

func TestReadNote_CRLFNormalised(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "TODO.md")
	if err := os.WriteFile(path, []byte("a\r\nb\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	nf, _ := readNote("TODO.md", path)
	if want := []string{"a", "b"}; len(nf.lines) != 2 || nf.lines[0] != want[0] || nf.lines[1] != want[1] {
		t.Errorf("CRLF should normalise to %v, got %v", want, nf.lines)
	}
}

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

func TestViewDash_HeaderShowsNotesChip(t *testing.T) {
	m := initialModel(Config{})
	m.width, m.height = 130, 40
	m.screen = screenDash
	m.repo = core.Repo{Name: "bridge"}
	m.notes = []noteFile{{name: "ideas.md", lines: []string{"a"}}}
	if !strings.Contains(m.View(), "ideas.md") {
		t.Errorf("dash header should advertise present note files")
	}
}

func TestViewDash_Narrow_StacksNotesPanel(t *testing.T) {
	m := initialModel(Config{})
	m.width, m.height = 80, 30 // below dashTwoColMin
	m.screen = screenDash
	m.repo = core.Repo{Name: "bridge"}
	m.dashRows = []dashRow{{worktree: "fix-x", path: "/r/fix-x"}}
	m.notesState = loadOK
	m.notes = []noteFile{{name: "TODO.md", lines: []string{"buy milk"}}}
	out := m.View()
	if !strings.Contains(out, "buy milk") {
		t.Errorf("narrow dash should stack the notes panel:\n%s", out)
	}
}

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

func TestNoteFilePanel_TruncatedFileShowsHint(t *testing.T) {
	m := initialModel(Config{})
	m.notesState = loadOK

	// A file clipped at notesMaxBytes surfaces a (truncated) hint in the title.
	m.notes = []noteFile{{name: "ideas.md", lines: []string{"x"}, truncated: true}}
	if out := m.ideasPanel(60, 0); !strings.Contains(out, "truncated") {
		t.Errorf("truncated note should show a (truncated) hint in the panel:\n%s", out)
	}

	// A non-truncated file must not show the hint.
	m.notes = []noteFile{{name: "ideas.md", lines: []string{"x"}}}
	if out := m.ideasPanel(60, 0); strings.Contains(out, "truncated") {
		t.Errorf("non-truncated note must not show the hint:\n%s", out)
	}
}
