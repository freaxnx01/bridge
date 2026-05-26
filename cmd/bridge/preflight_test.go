package main

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestPreflightNoArgs(t *testing.T) {
	root := writeFakeRepos(t)
	cache := t.TempDir()
	cmd := bridgeCmd("__preflight")
	cmd.Env = append(os.Environ(),
		"BRIDGE_REPOS_ROOT="+root,
		"XDG_CACHE_HOME="+cache,
		"BRIDGE_PICKER_FIXTURE_CANCEL=1",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	s := strings.TrimSpace(string(out))
	if s != "noop" {
		t.Errorf("got %q, want noop", s)
	}
}

func TestPreflightUnknownVerb(t *testing.T) {
	out, err := bridgeCmd("__preflight", "list").CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	if strings.TrimSpace(string(out)) != "noop" {
		t.Errorf("got %q", out)
	}
}

func TestPreflightIsHidden(t *testing.T) {
	out, err := bridgeCmd("--help").CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	if strings.Contains(string(out), "__preflight") {
		t.Errorf("__preflight should be hidden from --help, got:\n%s", out)
	}
}

func TestPreflightOpenEmitsCD(t *testing.T) {
	root := writeFakeRepos(t)
	cache := t.TempDir()
	cmd := bridgeCmd("__preflight", "open", "bridge")
	cmd.Env = append(os.Environ(),
		"BRIDGE_REPOS_ROOT="+root,
		"XDG_CACHE_HOME="+cache,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	s := strings.TrimSpace(string(out))
	if !strings.HasPrefix(s, "cd:") || !strings.HasSuffix(s, "/bridge") {
		t.Errorf("got %q", s)
	}
}

func TestPreflightOpenWithAgentEmitsExec(t *testing.T) {
	root := writeFakeRepos(t)
	cache := t.TempDir()
	cmd := bridgeCmd("__preflight", "open", "bridge", "--agent", "claude")
	cmd.Env = append(os.Environ(),
		"BRIDGE_REPOS_ROOT="+root,
		"XDG_CACHE_HOME="+cache,
	)
	out, _ := cmd.CombinedOutput()
	s := strings.TrimSpace(string(out))
	if !strings.HasPrefix(s, "exec:tmux new-session -A -s ") {
		t.Errorf("got %q", s)
	}
	if !strings.Contains(s, " claude") {
		t.Errorf("expected agent in argv: %q", s)
	}
}

func TestPreflightOpenUnknownRepoExits2(t *testing.T) {
	root := writeFakeRepos(t)
	cache := t.TempDir()
	cmd := bridgeCmd("__preflight", "open", "nope")
	cmd.Env = append(os.Environ(),
		"BRIDGE_REPOS_ROOT="+root,
		"XDG_CACHE_HOME="+cache,
	)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatal("expected non-zero exit")
	}
	// go run wraps exit codes; accept either direct exit code 2 OR stderr containing "exit status 2".
	if ee, ok := err.(*exec.ExitError); ok {
		if ee.ExitCode() != 2 && !strings.Contains(string(out), "exit status 2") {
			t.Errorf("expected exit 2, got exit %d / output %s", ee.ExitCode(), out)
		}
	}
}
