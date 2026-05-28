package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// doctorHome creates a fake HOME with a .bashrc that has all three managed
// lines, a .local/share/bridge/ with both shim files present, and a repos
// root with one fake repo. Returns the HOME path. Used for the happy-path
// test; failure tests modify the layout selectively.
func doctorHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	// rc with all three managed lines present.
	rc := filepath.Join(home, ".bashrc")
	rcContent := `_f=~/.local/share/bridge/bridge-shim.sh; [ -f "$_f" ] && . "$_f"; unset _f
command -v bridge >/dev/null && source <(bridge completion bash)
[ -f ~/.local/share/bridge/bridge-completion-meta.sh ] && source ~/.local/share/bridge/bridge-completion-meta.sh
`
	if err := os.WriteFile(rc, []byte(rcContent), 0o644); err != nil {
		t.Fatal(err)
	}
	// Shim files installed.
	shimDir := filepath.Join(home, ".local", "share", "bridge")
	if err := os.MkdirAll(shimDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"bridge-shim.sh", "bridge-completion-meta.sh"} {
		if err := os.WriteFile(filepath.Join(shimDir, f), []byte("# fake\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return home
}

func TestDoctorAllGreen(t *testing.T) {
	home := doctorHome(t)
	root := writeFakeRepos(t)
	cmd := bridgeCmd("doctor")
	cmd.Env = append(os.Environ(),
		"HOME="+home,
		"BRIDGE_SHIM_LOADED=1",
		"BRIDGE_REPOS_ROOT="+root,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("doctor (all green) expected exit 0, got err=%v\n%s", err, out)
	}
	s := string(out)
	if strings.Contains(s, "FAIL") {
		t.Errorf("doctor surfaced FAIL on a healthy setup:\n%s", s)
	}
}

func TestDoctorFailsWhenRcMissingCompletion(t *testing.T) {
	home := doctorHome(t)
	root := writeFakeRepos(t)
	// Overwrite rc with shim only — no completion, no augmenter.
	rc := filepath.Join(home, ".bashrc")
	if err := os.WriteFile(rc, []byte(`_f=~/.local/share/bridge/bridge-shim.sh; [ -f "$_f" ] && . "$_f"; unset _f`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := bridgeCmd("doctor")
	cmd.Env = append(os.Environ(),
		"HOME="+home,
		"BRIDGE_SHIM_LOADED=1",
		"BRIDGE_REPOS_ROOT="+root,
	)
	out, _ := cmd.CombinedOutput()
	s := string(out)
	if !strings.Contains(s, "FAIL") {
		t.Errorf("expected at least one FAIL when completion/augmenter source missing:\n%s", s)
	}
	if !strings.Contains(s, "bridge init") {
		t.Errorf("expected remediation to suggest `bridge init`; got:\n%s", s)
	}
}

func TestDoctorWarnsWhenShimNotLoaded(t *testing.T) {
	home := doctorHome(t)
	root := writeFakeRepos(t)
	cmd := bridgeCmd("doctor")
	// Explicitly DON'T set BRIDGE_SHIM_LOADED — and remove it if inherited.
	env := []string{}
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "BRIDGE_SHIM_LOADED=") {
			continue
		}
		env = append(env, kv)
	}
	cmd.Env = append(env, "HOME="+home, "BRIDGE_REPOS_ROOT="+root)
	out, _ := cmd.CombinedOutput()
	s := string(out)
	if !strings.Contains(s, "WARN") {
		t.Errorf("expected WARN when shim not loaded in current shell:\n%s", s)
	}
}
