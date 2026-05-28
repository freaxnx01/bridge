package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// bashShimLine, bashCompLine, bashMetaLine: substrings unique to each of
// the three managed lines. Tests check presence, not exact text, so the
// implementation can tune wording without breaking these assertions.
const (
	bashShimSubstr = "bridge-shim.sh"
	bashCompSubstr = "bridge completion bash"
	bashMetaSubstr = "bridge-completion-meta.sh"
)

func TestInitBashAppendsAllToFreshRc(t *testing.T) {
	home := t.TempDir()
	rc := filepath.Join(home, ".bashrc")
	if err := os.WriteFile(rc, []byte("# pre-existing line\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := bridgeCmd("init", "--shell", "bash")
	cmd.Env = append(os.Environ(), "HOME="+home, "BRIDGE_SHIM_LOADED=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	got, _ := os.ReadFile(rc)
	for _, want := range []string{bashShimSubstr, bashCompSubstr, bashMetaSubstr} {
		if !strings.Contains(string(got), want) {
			t.Errorf("expected %q in .bashrc; got:\n%s", want, got)
		}
	}
	if !strings.Contains(string(got), "# pre-existing line") {
		t.Errorf("pre-existing rc content lost; got:\n%s", got)
	}
}

func TestInitBashIdempotent(t *testing.T) {
	home := t.TempDir()
	rc := filepath.Join(home, ".bashrc")
	initial := `# user content
_f=~/.local/share/bridge/bridge-shim.sh; [ -f "$_f" ] && . "$_f"; unset _f
command -v bridge >/dev/null && source <(bridge completion bash)
[ -f ~/.local/share/bridge/bridge-completion-meta.sh ] && \
    source ~/.local/share/bridge/bridge-completion-meta.sh
`
	if err := os.WriteFile(rc, []byte(initial), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := bridgeCmd("init", "--shell", "bash")
	cmd.Env = append(os.Environ(), "HOME="+home, "BRIDGE_SHIM_LOADED=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	got, _ := os.ReadFile(rc)
	if string(got) != initial {
		t.Errorf("expected idempotent (unchanged); got:\n%s", got)
	}
}

func TestInitBashAddsMissingOnly(t *testing.T) {
	home := t.TempDir()
	rc := filepath.Join(home, ".bashrc")
	initial := `_f=~/.local/share/bridge/bridge-shim.sh; [ -f "$_f" ] && . "$_f"; unset _f
`
	if err := os.WriteFile(rc, []byte(initial), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := bridgeCmd("init", "--shell", "bash")
	cmd.Env = append(os.Environ(), "HOME="+home, "BRIDGE_SHIM_LOADED=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	got, _ := os.ReadFile(rc)
	s := string(got)
	if strings.Count(s, bashShimSubstr) != 1 {
		t.Errorf("expected shim line preserved exactly once; got:\n%s", got)
	}
	if !strings.Contains(s, bashCompSubstr) {
		t.Errorf("expected completion line added; got:\n%s", got)
	}
	if !strings.Contains(s, bashMetaSubstr) {
		t.Errorf("expected augmenter line added; got:\n%s", got)
	}
}

func TestInitBashWritesAgentExport(t *testing.T) {
	home := t.TempDir()
	rc := filepath.Join(home, ".bashrc")
	if err := os.WriteFile(rc, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := bridgeCmd("init", "--shell", "bash", "--agent", "claude")
	cmd.Env = append(os.Environ(), "HOME="+home, "BRIDGE_SHIM_LOADED=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	got, _ := os.ReadFile(rc)
	if !strings.Contains(string(got), `export BRIDGE_DEFAULT_AGENT=claude`) {
		t.Errorf("expected BRIDGE_DEFAULT_AGENT export; got:\n%s", got)
	}
}

func TestInitBashWritesAgentArgsExportQuoted(t *testing.T) {
	home := t.TempDir()
	rc := filepath.Join(home, ".bashrc")
	if err := os.WriteFile(rc, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := bridgeCmd("init", "--shell", "bash",
		"--agent", "claude",
		"--agent-args", "--remote-control --dangerously-skip-permissions")
	cmd.Env = append(os.Environ(), "HOME="+home, "BRIDGE_SHIM_LOADED=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	got, _ := os.ReadFile(rc)
	want := `export BRIDGE_DEFAULT_AGENT_ARGS="--remote-control --dangerously-skip-permissions"`
	if !strings.Contains(string(got), want) {
		t.Errorf("expected quoted args export:\nwant: %s\ngot:\n%s", want, got)
	}
}

func TestInitBashAgentReplacesExisting(t *testing.T) {
	home := t.TempDir()
	rc := filepath.Join(home, ".bashrc")
	initial := "export BRIDGE_DEFAULT_AGENT=opencode\n# trailing user line\n"
	if err := os.WriteFile(rc, []byte(initial), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := bridgeCmd("init", "--shell", "bash", "--agent", "claude")
	cmd.Env = append(os.Environ(), "HOME="+home, "BRIDGE_SHIM_LOADED=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	got, _ := os.ReadFile(rc)
	s := string(got)
	if strings.Contains(s, "opencode") {
		t.Errorf("expected old value 'opencode' to be replaced; got:\n%s", s)
	}
	if !strings.Contains(s, `export BRIDGE_DEFAULT_AGENT=claude`) {
		t.Errorf("expected new value 'claude'; got:\n%s", s)
	}
	if !strings.Contains(s, "# trailing user line") {
		t.Errorf("user content lost; got:\n%s", s)
	}
	// Only one export line should remain (the replaced one).
	if got := strings.Count(s, "export BRIDGE_DEFAULT_AGENT="); got != 1 {
		t.Errorf("expected exactly one BRIDGE_DEFAULT_AGENT export, got %d; content:\n%s", got, s)
	}
}

func TestInitBashAgentIdempotentWhenUnchanged(t *testing.T) {
	home := t.TempDir()
	rc := filepath.Join(home, ".bashrc")
	if err := os.WriteFile(rc, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	// First call writes.
	first := bridgeCmd("init", "--shell", "bash", "--agent", "claude")
	first.Env = append(os.Environ(), "HOME="+home, "BRIDGE_SHIM_LOADED=1")
	if out, err := first.CombinedOutput(); err != nil {
		t.Fatalf("init first: %v\n%s", err, out)
	}
	afterFirst, _ := os.ReadFile(rc)
	// Second call with same flags must produce identical content.
	second := bridgeCmd("init", "--shell", "bash", "--agent", "claude")
	second.Env = append(os.Environ(), "HOME="+home, "BRIDGE_SHIM_LOADED=1")
	if out, err := second.CombinedOutput(); err != nil {
		t.Fatalf("init second: %v\n%s", err, out)
	}
	afterSecond, _ := os.ReadFile(rc)
	if string(afterFirst) != string(afterSecond) {
		t.Errorf("second call mutated content;\nfirst:\n%s\nsecond:\n%s", afterFirst, afterSecond)
	}
}

func TestInitBashAliasAddsCompleteLine(t *testing.T) {
	home := t.TempDir()
	rc := filepath.Join(home, ".bashrc")
	if err := os.WriteFile(rc, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := bridgeCmd("init", "--shell", "bash", "--alias", "br")
	cmd.Env = append(os.Environ(), "HOME="+home, "BRIDGE_SHIM_LOADED=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	got, _ := os.ReadFile(rc)
	s := string(got)
	want := `complete -o default -o nospace -F __start_bridge br`
	if !strings.Contains(s, want) {
		t.Errorf("expected %q in .bashrc; got:\n%s", want, s)
	}
	// Guard must be present so the line no-ops when completion hasn't loaded.
	if !strings.Contains(s, "declare -F __start_bridge >/dev/null") {
		t.Errorf("expected declare -F guard in alias line:\n%s", s)
	}
}

func TestInitBashAliasMultiple(t *testing.T) {
	home := t.TempDir()
	rc := filepath.Join(home, ".bashrc")
	if err := os.WriteFile(rc, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := bridgeCmd("init", "--shell", "bash", "--alias", "br,brg")
	cmd.Env = append(os.Environ(), "HOME="+home, "BRIDGE_SHIM_LOADED=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	got, _ := os.ReadFile(rc)
	s := string(got)
	for _, name := range []string{"br", "brg"} {
		want := `__start_bridge ` + name
		if !strings.Contains(s, want) {
			t.Errorf("expected alias %q complete line; got:\n%s", name, s)
		}
	}
}

func TestInitBashAliasIdempotent(t *testing.T) {
	home := t.TempDir()
	rc := filepath.Join(home, ".bashrc")
	if err := os.WriteFile(rc, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	first := bridgeCmd("init", "--shell", "bash", "--alias", "br")
	first.Env = append(os.Environ(), "HOME="+home, "BRIDGE_SHIM_LOADED=1")
	if out, err := first.CombinedOutput(); err != nil {
		t.Fatalf("init first: %v\n%s", err, out)
	}
	afterFirst, _ := os.ReadFile(rc)
	second := bridgeCmd("init", "--shell", "bash", "--alias", "br")
	second.Env = append(os.Environ(), "HOME="+home, "BRIDGE_SHIM_LOADED=1")
	if out, err := second.CombinedOutput(); err != nil {
		t.Fatalf("init second: %v\n%s", err, out)
	}
	afterSecond, _ := os.ReadFile(rc)
	if string(afterFirst) != string(afterSecond) {
		t.Errorf("second call mutated content;\nfirst:\n%s\nsecond:\n%s", afterFirst, afterSecond)
	}
	if got := strings.Count(string(afterSecond), "__start_bridge br"); got != 1 {
		t.Errorf("expected exactly one br complete line, got %d", got)
	}
}

func TestInitBashAliasAddsMissingOnly(t *testing.T) {
	home := t.TempDir()
	rc := filepath.Join(home, ".bashrc")
	// Pre-existing br line; init --alias=br,brg should add only brg.
	initial := "complete -o default -o nospace -F __start_bridge br\n"
	if err := os.WriteFile(rc, []byte(initial), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := bridgeCmd("init", "--shell", "bash", "--alias", "br,brg")
	cmd.Env = append(os.Environ(), "HOME="+home, "BRIDGE_SHIM_LOADED=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	got, _ := os.ReadFile(rc)
	s := string(got)
	if got := strings.Count(s, "__start_bridge br\n"); got != 1 {
		t.Errorf("br line should still be present exactly once; got %d in:\n%s", got, s)
	}
	if !strings.Contains(s, "__start_bridge brg") {
		t.Errorf("brg should have been added; got:\n%s", s)
	}
}

func TestInitBashDryRunNoFileChange(t *testing.T) {
	home := t.TempDir()
	rc := filepath.Join(home, ".bashrc")
	initial := "# untouched\n"
	if err := os.WriteFile(rc, []byte(initial), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := bridgeCmd("init", "--shell", "bash", "--dry-run")
	cmd.Env = append(os.Environ(), "HOME="+home, "BRIDGE_SHIM_LOADED=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("init --dry-run: %v\n%s", err, out)
	}
	got, _ := os.ReadFile(rc)
	if string(got) != initial {
		t.Errorf("--dry-run modified file; got:\n%s", got)
	}
	// Dry-run output should contain each line it would add.
	for _, want := range []string{bashShimSubstr, bashCompSubstr, bashMetaSubstr} {
		if !strings.Contains(string(out), want) {
			t.Errorf("expected dry-run output to mention %q; got:\n%s", want, out)
		}
	}
}
