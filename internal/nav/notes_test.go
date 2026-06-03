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

func TestUpdate_NotesMsg_LoadsAndClearsFocusWhenEmpty(t *testing.T) {
	m := initialModel(Config{})
	m.screen = screenDash
	m.dashFocus = dashFocusNotes
	updated, _ := m.Update(notesMsg{notes: nil})
	m = updated.(Model)
	if m.notesState != loadOK {
		t.Errorf("notesState should be loadOK after notesMsg")
	}
	if m.dashFocus != dashFocusWorktrees {
		t.Errorf("focus should fall back to worktrees when notes are empty")
	}
}

func TestUpdateDash_TabCyclesThroughNotes(t *testing.T) {
	m := initialModel(Config{})
	m.screen = screenDash
	m.repo = core.Repo{Name: "bridge"}
	m.issues = []issueRow{{number: 1, title: "x"}}
	m.notes = []noteFile{{name: "ideas.md", lines: []string{"a"}}}

	tab := tea.KeyMsg{Type: tea.KeyTab}
	step := func(mod Model) Model {
		u, _ := mod.updateDash(tab)
		return u.(Model)
	}
	m = step(m)
	if m.dashFocus != dashFocusIssues {
		t.Fatalf("first tab => issues, got %d", m.dashFocus)
	}
	m = step(m)
	if m.dashFocus != dashFocusNotes {
		t.Fatalf("second tab => notes, got %d", m.dashFocus)
	}
	m = step(m)
	if m.dashFocus != dashFocusWorktrees {
		t.Fatalf("third tab wraps to worktrees, got %d", m.dashFocus)
	}
}

func TestUpdateDashNotes_ScrollClamped(t *testing.T) {
	m := initialModel(Config{})
	m.screen = screenDash
	m.dashFocus = dashFocusNotes
	m.notes = []noteFile{{name: "ideas.md", lines: []string{"l1", "l2", "l3"}}}
	// total display lines = 1 header + 3 body = 4.

	up := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("k")}
	u, _ := m.updateDashNotes(up)
	if got := u.(Model).notesScroll; got != 0 {
		t.Errorf("scroll must not go below 0, got %d", got)
	}

	end := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("G")}
	u, _ = m.updateDashNotes(end)
	if got := u.(Model).notesScroll; got != m.notesTotalLines()-1 {
		t.Errorf("G should clamp to last line %d, got %d", m.notesTotalLines()-1, got)
	}
}

func TestViewDash_Wide_NotesFocus_RendersContent(t *testing.T) {
	m := initialModel(Config{})
	m.width, m.height = 130, 40
	m.screen = screenDash
	m.repo = core.Repo{Name: "bridge"}
	m.dashRows = []dashRow{{worktree: "fix-x", path: "/r/fix-x"}}
	m.notesState = loadOK
	m.notes = []noteFile{{name: "ideas.md", lines: []string{"surface notes on the dashboard"}}}
	m.dashFocus = dashFocusNotes
	out := m.View()
	for _, want := range []string{"Notes", "ideas.md", "surface notes on the dashboard"} {
		if !strings.Contains(out, want) {
			t.Errorf("notes-focused dash missing %q\n%s", want, out)
		}
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
