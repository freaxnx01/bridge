package nav

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
)

// ideasFileName / todoFileName are the two conventional repo-root backlog files
// surfaced as the Ideas and Todos panes. The set is intentionally fixed.
const (
	ideasFileName = "ideas.md"
	todoFileName  = "TODO.md"
)

// noteFileNames is the fixed, minimal set of repo-root backlog files surfaced on
// the dashboard (#130). The set is intentionally NOT configurable: keeping it to
// the two conventional names keeps discovery predictable and the default small.
// Matched case-insensitively against the repo-root entries so common spellings
// (TODO.md, todo.md, Ideas.md) all resolve.
var noteFileNames = []string{ideasFileName, todoFileName}

// notesMaxBytes caps how much of a note file is read into the preview. Larger
// files are read up to the cap and flagged truncated, so a huge file scrolls a
// bounded window rather than flooding the view or memory.
const notesMaxBytes = 64 * 1024

// loadNotesCmd reads the repo-root note files asynchronously, off the Update
// loop, mirroring the dirtyMsg disk-I/O pattern (issue #130).
func loadNotesCmd(repoPath string) tea.Cmd {
	return func() tea.Msg {
		return notesMsg{notes: readRepoNotes(repoPath)}
	}
}

// readRepoNotes returns the present note files at the repo root, in
// noteFileNames order. Absent files are simply omitted (no error, no empty
// entry). Matching is case-insensitive over a single directory read.
func readRepoNotes(repoPath string) []noteFile {
	entries, err := os.ReadDir(repoPath)
	if err != nil {
		return nil
	}
	onDisk := make(map[string]string, len(entries)) // lowercased name -> actual name
	for _, e := range entries {
		if !e.IsDir() {
			onDisk[strings.ToLower(e.Name())] = e.Name()
		}
	}
	var out []noteFile
	for _, want := range noteFileNames {
		actual, ok := onDisk[strings.ToLower(want)]
		if !ok {
			continue
		}
		if nf, ok := readNote(actual, filepath.Join(repoPath, actual)); ok {
			out = append(out, nf)
		}
	}
	return out
}

// readNote reads up to notesMaxBytes of path and decodes it as a UTF-8 text
// preview. Binary / non-UTF-8 content is flagged rather than rendered. A read
// error (file vanished between the dir scan and the open) drops the file.
func readNote(name, path string) (noteFile, bool) {
	f, err := os.Open(path)
	if err != nil {
		return noteFile{}, false
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, notesMaxBytes+1))
	if err != nil {
		return noteFile{}, false
	}
	nf := noteFile{name: name}
	if len(data) > notesMaxBytes {
		data = data[:notesMaxBytes]
		nf.truncated = true
		// The byte cap can split a trailing multibyte rune; drop up to 3 partial
		// tail bytes so a valid UTF-8 file isn't misflagged as binary below.
		for i := 0; i < utf8.UTFMax-1 && len(data) > 0 && !utf8.Valid(data); i++ {
			data = data[:len(data)-1]
		}
	}
	if bytes.IndexByte(data, 0) >= 0 || !utf8.Valid(data) {
		nf.binary = true
		return nf, true
	}
	body := strings.ReplaceAll(string(data), "\r\n", "\n")
	nf.lines = strings.Split(strings.TrimRight(body, "\n"), "\n")
	return nf, true
}

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
