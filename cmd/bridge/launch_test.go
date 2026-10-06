//go:build !windows

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeTmux puts a stub tmux on PATH. It logs every invocation to
// <dir>/tmux.log, lists $FAKE_TMUX_LIVE (pipe format) for list-sessions,
// prints $FAKE_TMUX_PANE for capture-pane, and fails has-session so the
// detached `sh -c` body proceeds to new-session.
func fakeTmux(t *testing.T) (binDir, logPath string) {
	t.Helper()
	binDir = t.TempDir()
	logPath = filepath.Join(binDir, "tmux.log")
	script := `#!/bin/sh
echo "$*" >> "` + logPath + `"
case "$1" in
  list-sessions) [ -n "$FAKE_TMUX_LIVE" ] && printf '%s\n' "$FAKE_TMUX_LIVE" && exit 0; exit 1 ;;
  capture-pane) printf '%s\n' "$FAKE_TMUX_PANE"; exit 0 ;;
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
	cmd := bridgeCmd("launch", "bridge", "--agent", "claude", "--json")
	cmd.Env = launchEnv(t, bin, "FAKE_TMUX_LIVE=bridge|0|1700000000|1700000000")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	var res launchResult
	_ = json.Unmarshal(out, &res)
	if !res.AlreadyRunning {
		t.Errorf("want already_running=true: %s", out)
	}
	log, _ := os.ReadFile(logPath)
	if strings.Contains(string(log), "new-session") {
		t.Errorf("must not create a session when live; log:\n%s", log)
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
	_ = json.Unmarshal(out, &res)
	if res.RCURL != "https://claude.ai/code/session_01ABCxyz" {
		t.Errorf("rc_url = %q", res.RCURL)
	}
}

func TestLaunchRCURLMissingStillSucceeds(t *testing.T) {
	bin, _ := fakeTmux(t)
	cmd := bridgeCmd("launch", "bridge", "--agent", "claude", "--rc", "--rc-wait", "1s", "--json")
	cmd.Env = launchEnv(t, bin, "FAKE_TMUX_PANE=starting…")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("launch must succeed without RC URL: %v\n%s", err, out)
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
	}{
		{"unknown repo", []string{"launch", "nope", "--agent", "claude"}},
		{"no agent", []string{"launch", "bridge"}},
		{"rc non-claude", []string{"launch", "bridge", "--agent", "code", "--rc"}},
	} {
		cmd := bridgeCmd(tc.args...)
		cmd.Env = launchEnv(t, bin)
		out, err := cmd.CombinedOutput()
		if err == nil {
			t.Errorf("%s: want non-zero exit, got success: %s", tc.name, out)
			continue
		}
		if ee, ok := err.(interface{ ExitCode() int }); ok && ee.ExitCode() != 2 {
			t.Errorf("%s: exit %d, want 2: %s", tc.name, ee.ExitCode(), out)
		}
	}
	if log, _ := os.ReadFile(logPath); strings.Contains(string(log), "new-session") {
		t.Errorf("error paths must not launch; log:\n%s", log)
	}
}
