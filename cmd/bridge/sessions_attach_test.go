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

func TestSessionsAttachWithoutShimFailsLoudly(t *testing.T) {
	// `bridge sessions attach foo` outside the shim used to print the slot
	// name and exit 0 (silent no-op — see #66). It must now error usefully.
	cmd := bridgeCmd("sessions", "attach", "foo")
	cmd.Env = []string{"PATH=" + os.Getenv("PATH")} // no BRIDGE_SHIM_LOADED
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected non-zero exit, got success: %s", out)
	}
	if !strings.Contains(string(out), "shim") {
		t.Errorf("expected error mentioning shim, got: %s", out)
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
