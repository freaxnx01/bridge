package main

import (
	"os"
	"path/filepath"
	"runtime"
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
	// doctor resolves the binary via exec.LookPath("bridge"), which on Windows
	// only matches an executable extension (bridge.exe). Stage the file under
	// the platform-correct name so the lookup succeeds.
	staged := "bridge"
	if runtime.GOOS == "windows" {
		staged += ".exe"
	}
	target := filepath.Join(pathDir, staged)
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
	stripped := map[string]bool{
		"HOME=": true, "PATH=": true,
		"BRIDGE_SHIM_LOADED=":        true,
		"BRIDGE_REPOS_ROOT=":         true,
		"BRIDGE_DEFAULT_AGENT=":      true,
		"BRIDGE_DEFAULT_AGENT_ARGS=": true,
	}
	for _, kv := range os.Environ() {
		skip := false
		for prefix := range stripped {
			if strings.HasPrefix(kv, prefix) {
				skip = true
				break
			}
		}
		if !skip {
			env = append(env, kv)
		}
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

func TestDoctorReportsRegisteredAliasCompletions(t *testing.T) {
	home, pathDir := doctorHome(t)
	root := writeFakeRepos(t)
	// Append `complete -F __start_bridge` lines for br and brg.
	rc := filepath.Join(home, ".bashrc")
	existing, _ := os.ReadFile(rc)
	aliasLines := `
declare -F __start_bridge >/dev/null && \
    complete -o default -o nospace -F __start_bridge br
declare -F __start_bridge >/dev/null && \
    complete -o default -o nospace -F __start_bridge brg
`
	if err := os.WriteFile(rc, append(existing, []byte(aliasLines)...), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := bridgeCmd("doctor")
	cmd.Env = append(doctorEnv(home, pathDir, root), "BRIDGE_SHIM_LOADED=1")
	out, _ := cmd.CombinedOutput()
	s := string(out)
	if !strings.Contains(s, "alias completions") {
		t.Errorf("expected 'alias completions' check line:\n%s", s)
	}
	for _, name := range []string{"br", "brg"} {
		if !strings.Contains(s, name) {
			t.Errorf("expected alias %q in output:\n%s", name, s)
		}
	}
}

func TestDoctorReportsNoAliasCompletionsWhenAbsent(t *testing.T) {
	home, pathDir := doctorHome(t)
	root := writeFakeRepos(t)
	cmd := bridgeCmd("doctor")
	cmd.Env = append(doctorEnv(home, pathDir, root), "BRIDGE_SHIM_LOADED=1")
	out, _ := cmd.CombinedOutput()
	s := string(out)
	if !strings.Contains(s, "alias completions") {
		t.Errorf("expected 'alias completions' check line:\n%s", s)
	}
	if !strings.Contains(s, "(none") {
		t.Errorf("expected '(none ...)' hint when no aliases registered:\n%s", s)
	}
}

func TestDoctorReportsDefaultAgentPass(t *testing.T) {
	home, pathDir := doctorHome(t)
	root := writeFakeRepos(t)
	cmd := bridgeCmd("doctor")
	cmd.Env = append(doctorEnv(home, pathDir, root),
		"BRIDGE_SHIM_LOADED=1",
		"BRIDGE_DEFAULT_AGENT=claude",
		"BRIDGE_DEFAULT_AGENT_ARGS=--remote-control",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("doctor: %v\n%s", err, out)
	}
	s := string(out)
	if !strings.Contains(s, "BRIDGE_DEFAULT_AGENT") {
		t.Errorf("expected BRIDGE_DEFAULT_AGENT line in output:\n%s", s)
	}
	if !strings.Contains(s, "claude") {
		t.Errorf("expected agent name 'claude' in output:\n%s", s)
	}
	if !strings.Contains(s, "--remote-control") {
		t.Errorf("expected args echoed in output:\n%s", s)
	}
}

func TestDoctorReportsDefaultAgentWarnWhenUnset(t *testing.T) {
	home, pathDir := doctorHome(t)
	root := writeFakeRepos(t)
	cmd := bridgeCmd("doctor")
	cmd.Env = append(doctorEnv(home, pathDir, root), "BRIDGE_SHIM_LOADED=1")
	out, _ := cmd.CombinedOutput()
	s := string(out)
	// Find the BRIDGE_DEFAULT_AGENT line and confirm WARN status.
	if !strings.Contains(s, "WARN") || !strings.Contains(s, "BRIDGE_DEFAULT_AGENT") {
		t.Errorf("expected WARN on BRIDGE_DEFAULT_AGENT when unset:\n%s", s)
	}
	if !strings.Contains(s, "bridge init") {
		t.Errorf("expected remediation suggesting `bridge init`:\n%s", s)
	}
}

func TestDoctorFailsOnUnknownAgent(t *testing.T) {
	home, pathDir := doctorHome(t)
	root := writeFakeRepos(t)
	cmd := bridgeCmd("doctor")
	cmd.Env = append(doctorEnv(home, pathDir, root),
		"BRIDGE_SHIM_LOADED=1",
		"BRIDGE_DEFAULT_AGENT=bogus-agent",
	)
	out, _ := cmd.CombinedOutput()
	s := string(out)
	if !strings.Contains(s, "FAIL") {
		t.Errorf("expected FAIL on unknown agent name:\n%s", s)
	}
	if !strings.Contains(s, "bogus-agent") {
		t.Errorf("expected unknown agent name in output:\n%s", s)
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
