package main

import (
	"os"
	"strings"
	"testing"
)

func TestPositionalOpensRepo(t *testing.T) {
	root := writeFakeRepos(t)
	cache := t.TempDir()
	cmd := bridgeCmd("bridge", "--json")
	cmd.Env = append(os.Environ(),
		"BRIDGE_REPOS_ROOT="+root,
		"XDG_CACHE_HOME="+cache,
	)
	var sout stringBuf
	cmd.Stdout = &sout
	if err := cmd.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(sout.String(), `"name": "bridge"`) {
		t.Errorf("expected JSON for bridge, got: %s", sout.String())
	}
}

func TestPreflightPositionalEmitsCD(t *testing.T) {
	root := writeFakeRepos(t)
	cache := t.TempDir()
	cmd := bridgeCmd("__preflight", "bridge")
	cmd.Env = append(envWithout("BRIDGE_DEFAULT_AGENT", "BRIDGE_DEFAULT_AGENT_ARGS"),
		"BRIDGE_REPOS_ROOT="+root,
		"XDG_CACHE_HOME="+cache,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	s := strings.TrimSpace(string(out))
	if !strings.HasPrefix(s, "cd:") {
		t.Errorf("got %q", s)
	}
}

func TestIsKnownVerb_RegisteredAndBuiltins(t *testing.T) {
	for _, v := range []string{
		"list", "slots", "sessions", "presence", "sync", "status", "issues",
		"open", "rm", "watch", "tui", "nav", "init", "doctor",
		"version", "help", "completion", "__complete", "__completeNoDesc",
		"__complete-meta", "__preflight",
	} {
		if !isKnownVerb(v) {
			t.Errorf("isKnownVerb(%q) = false, want true", v)
		}
	}
	for _, v := range []string{"somerepo", "myproject", "bridge"} {
		if isKnownVerb(v) {
			t.Errorf("isKnownVerb(%q) = true, want false (should rewrite to open)", v)
		}
	}
}
