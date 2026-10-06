//go:build !windows

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// fakeTmux puts a stub tmux on PATH. It logs every invocation to
// <dir>/tmux.log, lists $FAKE_TMUX_LIVE (pipe format) for list-sessions,
// prints $FAKE_TMUX_PANE for capture-pane (or fails if $FAKE_TMUX_CAPTURE_FAIL), and fails has-session so the
// detached `sh -c` body proceeds to new-session.
func fakeTmux(t *testing.T) (binDir, logPath string) {
	t.Helper()
	binDir = t.TempDir()
	logPath = filepath.Join(binDir, "tmux.log")
	script := `#!/bin/sh
echo "$*" >> "` + logPath + `"
case "$1" in
  list-sessions) [ -n "$FAKE_TMUX_LIVE" ] && printf '%s\n' "$FAKE_TMUX_LIVE" && exit 0; exit 1 ;;
  capture-pane) [ -n "$FAKE_TMUX_CAPTURE_FAIL" ] && exit 1; printf '%s\n' "$FAKE_TMUX_PANE"; exit 0 ;;
  has-session) exit 1 ;;
esac
exit 0
`
	if err := os.WriteFile(filepath.Join(binDir, "tmux"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return binDir, logPath
}

func launchEnv(t *testing.T, binDir string, extra ...string) []string {
	root := writeFakeRepos(t)
	env := append(envWithout("TMUX", "BRIDGE_DEFAULT_AGENT", "BRIDGE_DEFAULT_AGENT_ARGS", "PATH"),
		"PATH="+binDir+":"+os.Getenv("PATH"),
		"BRIDGE_REPOS_ROOT="+root,
		"XDG_CACHE_HOME="+t.TempDir(),
		"BRIDGE_NO_SYNC=1",
		"CLAUDE_CONFIG_DIR="+t.TempDir(),
		"BRIDGE_CACHE="+t.TempDir(),
	)
	return append(env, extra...)
}

func TestLaunchStartsDetachedSessionJSON(t *testing.T) {
	bin, logPath := fakeTmux(t)
	cmd := bridgeCmd("launch", "bridge", "--agent", "claude", "--json")
	cmd.Env = launchEnv(t, bin)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	var res launchResult
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatalf("json: %v\n%s", err, out)
	}
	if res.Slot != "bridge" || res.Agent != "claude" || res.AlreadyRunning {
		t.Errorf("unexpected result: %+v", res)
	}
	log, _ := os.ReadFile(logPath)
	if !strings.Contains(string(log), "new-session -d -s bridge") {
		t.Errorf("tmux new-session -d not called; log:\n%s", log)
	}
	if strings.Contains(string(log), "attach-session") || strings.Contains(string(log), "switch-client") {
		t.Errorf("launch must not attach; log:\n%s", log)
	}
}

func TestLaunchAlreadyRunningDoesNotStartAgain(t *testing.T) {
	bin, logPath := fakeTmux(t)
	gitLog := filepath.Join(bin, "git.log")
	gitScript := "#!/bin/sh\necho \"$*\" >> \"" + gitLog + "\"\nexit 0\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(gitScript), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := bridgeCmd("launch", "bridge", "--agent", "claude", "--json")
	// Sync enabled on purpose: a live slot must still never be pulled.
	cmd.Env = slices.DeleteFunc(launchEnv(t, bin, "FAKE_TMUX_LIVE=bridge|0|1700000000|1700000000"),
		func(e string) bool { return strings.HasPrefix(e, "BRIDGE_NO_SYNC=") })
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	var res launchResult
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatalf("json: %v\n%s", err, out)
	}
	if !res.AlreadyRunning {
		t.Errorf("want already_running=true: %s", out)
	}
	log, _ := os.ReadFile(logPath)
	if strings.Contains(string(log), "new-session") {
		t.Errorf("must not create a session when live; log:\n%s", log)
	}
	if g, _ := os.ReadFile(gitLog); strings.Contains(string(g), "pull") || strings.Contains(string(g), "fetch") {
		t.Errorf("must not sync a live slot; git log:\n%s", g)
	}
}

func TestLaunchRCScrapesURL(t *testing.T) {
	bin, _ := fakeTmux(t)
	cmd := bridgeCmd("launch", "bridge", "--agent", "claude", "--rc", "--rc-wait", "2s", "--json")
	cmd.Env = launchEnv(t, bin, "FAKE_TMUX_PANE=  Remote Control: https://claude.ai/code/session_01ABCxyz  ")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	var res launchResult
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatalf("json: %v\n%s", err, out)
	}
	if res.RCURL != "https://claude.ai/code/session_01ABCxyz" {
		t.Errorf("rc_url = %q", res.RCURL)
	}
}

func TestLaunchRCURLMissingStillSucceeds(t *testing.T) {
	bin, _ := fakeTmux(t)
	cmd := bridgeCmd("launch", "bridge", "--agent", "claude", "--rc", "--rc-wait", "1s", "--json")
	cmd.Env = launchEnv(t, bin, "FAKE_TMUX_LIVE=bridge|0|1700000000|1700000000", "FAKE_TMUX_PANE=starting…")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("launch must succeed without RC URL: %v\n%s", err, out)
	}
	if !strings.Contains(stderr.String(), "no Remote Control URL") {
		t.Errorf("stderr note missing: %q", stderr.String())
	}
	if strings.Contains(string(out), "rc_url") {
		t.Errorf("rc_url should be omitted: %s", out)
	}
}

func TestLaunchErrorsExit2(t *testing.T) {
	bin, logPath := fakeTmux(t)
	for _, tc := range []struct {
		name string
		args []string
		hint string
	}{
		{"unknown repo", []string{"launch", "nope", "--agent", "claude"}, "unknown repo"},
		{"ambiguous repo", []string{"launch", "e", "--agent", "claude"}, "ambiguous"},
		{"no agent", []string{"launch", "bridge"}, "--agent"},
		{"rc non-claude", []string{"launch", "bridge", "--agent", "code", "--rc"}, "--rc"},
	} {
		cmd := bridgeCmd(tc.args...)
		cmd.Env = launchEnv(t, bin)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if err == nil {
			t.Errorf("%s: want non-zero exit, got success: %s", tc.name, out)
			continue
		}
		var ee *exec.ExitError
		if !errors.As(err, &ee) {
			t.Errorf("%s: error is not an exit error: %v", tc.name, err)
			continue
		}
		if ee.ExitCode() != 2 {
			t.Errorf("%s: exit %d, want 2: %s", tc.name, ee.ExitCode(), stderr.String())
		}
		if !strings.Contains(stderr.String(), tc.hint) {
			t.Errorf("%s: stderr %q lacks %q", tc.name, stderr.String(), tc.hint)
		}
	}
	if log, _ := os.ReadFile(logPath); strings.Contains(string(log), "new-session") {
		t.Errorf("error paths must not launch; log:\n%s", log)
	}
}

func TestLaunchRCSessionDiesEarly(t *testing.T) {
	bin, _ := fakeTmux(t)
	cmd := bridgeCmd("launch", "bridge", "--agent", "claude", "--rc", "--rc-wait", "5s", "--json")
	cmd.Env = launchEnv(t, bin, "FAKE_TMUX_CAPTURE_FAIL=1")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	start := time.Now()
	out, err := cmd.Output()
	elapsed := time.Since(start)
	var ee *exec.ExitError
	if !errors.As(err, &ee) || ee.ExitCode() != 1 {
		t.Fatalf("want exit 1, got %v\n%s", err, out)
	}
	if !strings.Contains(stderr.String(), "exited") {
		t.Errorf("stderr lacks 'exited': %q", stderr.String())
	}
	if elapsed > 3*time.Second {
		t.Errorf("took %s; must return early when the session is gone", elapsed)
	}
}

func TestLaunchRCLastMatchWins(t *testing.T) {
	bin, _ := fakeTmux(t)
	cmd := bridgeCmd("launch", "bridge", "--agent", "claude", "--rc", "--rc-wait", "2s", "--json")
	cmd.Env = launchEnv(t, bin, "FAKE_TMUX_PANE=old https://claude.ai/code/session_OLD1\nnew https://claude.ai/code/session_NEW2")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	var res launchResult
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatalf("json: %v\n%s", err, out)
	}
	if res.RCURL != "https://claude.ai/code/session_NEW2" {
		t.Errorf("rc_url = %q, want last match", res.RCURL)
	}
}

func TestLaunchRCIgnoresNonSessionURL(t *testing.T) {
	bin, _ := fakeTmux(t)
	cmd := bridgeCmd("launch", "bridge", "--agent", "claude", "--rc", "--rc-wait", "1s", "--json")
	cmd.Env = launchEnv(t, bin, "FAKE_TMUX_LIVE=bridge|0|1700000000|1700000000", "FAKE_TMUX_PANE=see https://claude.ai/code/artifact/abc")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	if strings.Contains(string(out), "rc_url") {
		t.Errorf("non-session URL must not be reported: %s", out)
	}
}

func TestLaunchRejectsBeforeSideEffects(t *testing.T) {
	bin, _ := fakeTmux(t)
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"rc non-claude", []string{"launch", "bridge", "-w", "wt1", "--agent", "code", "--rc"}},
		{"no agent", []string{"launch", "bridge", "-w", "wt1"}},
	} {
		cmd := bridgeCmd(tc.args...)
		cmd.Env = launchEnv(t, bin)
		root := ""
		for _, e := range cmd.Env {
			if v, ok := strings.CutPrefix(e, "BRIDGE_REPOS_ROOT="); ok {
				root = v
			}
		}
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		_ = cmd.Run()
		matches, _ := filepath.Glob(filepath.Join(root, "*", "*", "*", "*", ".worktrees", "wt1"))
		if len(matches) != 0 || strings.Contains(stderr.String(), "created worktree") ||
			strings.Contains(stderr.String(), "worktree resolve") {
			t.Errorf("%s: rejected launch touched worktrees: %v %q", tc.name, matches, stderr.String())
		}
	}
}
