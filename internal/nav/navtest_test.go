package nav

import (
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

var update = flag.Bool("update", false, "update golden files")

// session drives a nav Model through a scripted key sequence for flow tests.
// It is white-box (package nav) so it can build the model via initialModel and
// render via the unexported View pipeline. Update is pure and every effect is
// an explicit tea.Cmd, so a driven sequence reproduces the real runtime flow
// without a tea.Program or a TTY.
type session struct {
	t       *testing.T
	m       Model
	lastCmd tea.Cmd
}

// newSession builds the model at a fixed, layout-deterministic size.
func newSession(t *testing.T, cfg Config) *session {
	t.Helper()
	m := initialModel(cfg)
	m.width, m.height = 120, 40
	return &session{t: t, m: m}
}

// send applies one message via Update and records the returned Cmd.
func (s *session) send(msg tea.Msg) {
	s.t.Helper()
	out, cmd := s.m.Update(msg)
	s.m = out.(Model)
	s.lastCmd = cmd
}

// key sends a key press. Special names map to their tea.KeyType; anything else
// is sent as runes (so "o", "j", "/", and typed text all work).
func (s *session) key(k string) {
	s.t.Helper()
	switch k {
	case "enter":
		s.send(tea.KeyMsg{Type: tea.KeyEnter})
	case "esc":
		s.send(tea.KeyMsg{Type: tea.KeyEsc})
	case "tab":
		s.send(tea.KeyMsg{Type: tea.KeyTab})
	case "up":
		s.send(tea.KeyMsg{Type: tea.KeyUp})
	case "down":
		s.send(tea.KeyMsg{Type: tea.KeyDown})
	case "backspace":
		s.send(tea.KeyMsg{Type: tea.KeyBackspace})
	default:
		s.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)})
	}
}

// resolve runs the last recorded Cmd and feeds its message(s) back through
// Update, flattening tea.BatchMsg. No-op if there is no pending Cmd. Explicit
// by design: tests never resolve self-repeating cmds (e.g. spinner tick), so
// there is no infinite loop.
func (s *session) resolve() {
	s.t.Helper()
	cmd := s.lastCmd
	s.lastCmd = nil
	if cmd == nil {
		return
	}
	for _, msg := range flattenCmd(cmd) {
		s.send(msg)
	}
}

// flattenCmd runs a cmd and returns the resulting message(s), expanding a
// tea.BatchMsg (a []tea.Cmd) one level. tea.Quit's message is dropped (control,
// not state). Nested batches are expanded recursively.
func flattenCmd(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	switch m := msg.(type) {
	case nil:
		return nil
	case tea.BatchMsg:
		var out []tea.Msg
		for _, c := range m {
			out = append(out, flattenCmd(c)...)
		}
		return out
	default:
		// tea.Quit returns an internal quitMsg we can't name; it carries no
		// state, so feeding it to Update is harmless (Update ignores unknown
		// msgs). Keep it simple: return the message as-is.
		return []tea.Msg{msg}
	}
}

// frame returns the current View() with ANSI escapes stripped, so golden
// comparisons are stable across terminals/CI regardless of color profile.
func (s *session) frame() string {
	s.t.Helper()
	return stripANSI(s.m.View())
}

var ansiRE = regexp.MustCompile("\x1b\\[[0-9;]*[a-zA-Z]")

func stripANSI(s string) string { return ansiRE.ReplaceAllString(s, "") }

// assertGolden compares got against internal/nav/testdata/<name>.golden,
// rewriting it when -update is set.
func assertGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name+".golden")
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s: %v (run `go test ./internal/nav -update` to create it)", path, err)
	}
	if got != string(want) {
		t.Errorf("frame mismatch for %s\n--- got ---\n%s\n--- want ---\n%s", name, got, string(want))
	}
}

func TestHarness_PickerSmoke(t *testing.T) {
	s := newSession(t, Config{})
	s.send(reposMsg{rows: []repoRow{{label: "github/public/bridge"}, {label: "github/public/agent-os"}}})
	f := s.frame()
	if !strings.Contains(f, "bridge") {
		t.Errorf("picker frame missing seeded repo:\n%s", f)
	}
}
