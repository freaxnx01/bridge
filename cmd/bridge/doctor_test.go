package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// doctorHome creates a fake HOME with a .bashrc that has all three managed
// lines, a .local/share/bridge/ with both shim files present, and a repos
// root with one fake repo. Returns the HOME path and a PATH-prepend
// directory that contains a `bridge` symlink to the test binary so
// doctor's `exec.LookPath("bridge")` succeeds. Used for the happy-path
// test; failure tests modify the layout selectively.
func doctorHome(t *testing.T) (home, pathDir string) {
	t.Helper()
	home = t.TempDir()
	rc := filepath.Join(home, ".bashrc")
	rcContent := `_f=~/.local/share/bridge/bridge-shim.sh; [ -f "$_f" ] && . "$_f"; unset _f
command -v bridge >/dev/null && source <(bridge completion bash)
[ -f ~/.local/share/bridge/bridge-completion-meta.sh ] && source ~/.local/share/bridge/bridge-completion-meta.sh
`
	if err := os.WriteFile(rc, []byte(rcContent), 0o644); err != nil {
		t.Fatal(err)
	}
	shimDir := filepath.Join(home, ".local", "share", "bridge")
	if err := os.MkdirAll(shimDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"bridge-shim.sh", "bridge-completion-meta.sh"} {
		if err := os.WriteFile(filepath.Join(shimDir, f), []byte("# fake\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	pathDir = filepath.Join(home, "bin")
	if err := os.MkdirAll(pathDir, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(pathDir, "bridge")
	if err := os.Symlink(bridgeBin, target); err != nil {
		// Windows runners without developer mode can't symlink — copy instead.
		data, rerr := os.ReadFile(bridgeBin)
		if rerr != nil {
			t.Fatalf("doctorHome: cannot stage bridge on PATH: %v / %v", err, rerr)
		}
		if werr := os.WriteFile(target, data, 0o755); werr != nil {
			t.Fatalf("doctorHome: cannot stage bridge on PATH: %v", werr)
		}
	}
	return home, pathDir
}

// doctorEnv returns os.Environ() with HOME, BRIDGE_REPOS_ROOT, and PATH set
// for a doctor invocation. PATH is rewritten to put pathDir first so the
// staged `bridge` symlink wins exec.LookPath. Caller appends BRIDGE_SHIM_LOADED
// (or omits it) per test.
func doctorEnv(home, pathDir, reposRoot string) []string {
	env := []string{}
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "HOME=") || strings.HasPrefix(kv, "PATH=") ||
			strings.HasPrefix(kv, "BRIDGE_SHIM_LOADED=") || strings.HasPrefix(kv, "BRIDGE_REPOS_ROOT=") {
			continue
		}
		env = append(env, kv)
	}
	env = append(env,
		"HOME="+home,
		"PATH="+pathDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"BRIDGE_REPOS_ROOT="+reposRoot,
	)
	return env
}

func TestDoctorAllGreen(t *testing.T) {
	home, pathDir := doctorHome(t)
	root := writeFakeRepos(t)
	cmd := bridgeCmd("doctor")
	cmd.Env = append(doctorEnv(home, pathDir, root), "BRIDGE_SHIM_LOADED=1")
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
	home, pathDir := doctorHome(t)
	root := writeFakeRepos(t)
	rc := filepath.Join(home, ".bashrc")
	if err := os.WriteFile(rc, []byte(`_f=~/.local/share/bridge/bridge-shim.sh; [ -f "$_f" ] && . "$_f"; unset _f`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := bridgeCmd("doctor")
	cmd.Env = append(doctorEnv(home, pathDir, root), "BRIDGE_SHIM_LOADED=1")
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
	home, pathDir := doctorHome(t)
	root := writeFakeRepos(t)
	cmd := bridgeCmd("doctor")
	cmd.Env = doctorEnv(home, pathDir, root) // BRIDGE_SHIM_LOADED intentionally absent
	out, _ := cmd.CombinedOutput()
	s := string(out)
	if !strings.Contains(s, "WARN") {
		t.Errorf("expected WARN when shim not loaded in current shell:\n%s", s)
	}
}
