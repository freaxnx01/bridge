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
