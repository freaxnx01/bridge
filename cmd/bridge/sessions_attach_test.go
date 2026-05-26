package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestPreflightSessionsAttachEmitsExec(t *testing.T) {
	dir := t.TempDir()
	fixture := filepath.Join(dir, "tmux.txt")
	_ = os.WriteFile(fixture, []byte("bridge-main|0|1716000000\n"), 0o644)

	cmd := bridgeCmd("__preflight", "sessions", "attach", "bridge-main")
	cmd.Env = append(os.Environ(), "BRIDGE_TMUX_FIXTURE="+fixture)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	s := strings.TrimSpace(string(out))
	if !strings.HasPrefix(s, "exec:tmux attach-session -t bridge-main") {
		t.Errorf("got %q", s)
	}
}

func TestSessionsAttachUnknownExits2(t *testing.T) {
	dir := t.TempDir()
	fixture := filepath.Join(dir, "tmux.txt")
	_ = os.WriteFile(fixture, []byte(""), 0o644)
	cmd := bridgeCmd("__preflight", "sessions", "attach", "bogus")
	cmd.Env = append(os.Environ(), "BRIDGE_TMUX_FIXTURE="+fixture)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatal("expected non-zero exit")
	}
	if ee, ok := err.(*exec.ExitError); ok {
		if ee.ExitCode() != 2 && !strings.Contains(string(out), "exit status 2") {
			t.Errorf("expected exit 2, got %d / out: %s", ee.ExitCode(), out)
		}
	}
}
