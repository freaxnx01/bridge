package main

import (
	"errors"
	"strings"
	"testing"
)

func TestResolveLaunchTargetMainCheckout(t *testing.T) {
	root := writeFakeRepos(t)
	t.Setenv("BRIDGE_REPOS_ROOT", root)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("BRIDGE_DEFAULT_AGENT", "")
	tg, err := resolveLaunchTarget("bridge", "", "claude")
	if err != nil {
		t.Fatal(err)
	}
	if tg.Slot != "bridge" || tg.AgentName != "claude" || !tg.HasAgent || tg.Spec.Bin == "" {
		t.Errorf("unexpected target: %+v", tg)
	}
	if tg.WorkDir != tg.Repo.Path {
		t.Errorf("workdir %q != repo path %q", tg.WorkDir, tg.Repo.Path)
	}
}

func TestResolveLaunchTargetNoAgent(t *testing.T) {
	root := writeFakeRepos(t)
	t.Setenv("BRIDGE_REPOS_ROOT", root)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("BRIDGE_DEFAULT_AGENT", "")
	tg, err := resolveLaunchTarget("bridge", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if tg.HasAgent {
		t.Errorf("want HasAgent=false, got %+v", tg)
	}
}

func TestResolveLaunchTargetUnknownRepo(t *testing.T) {
	root := writeFakeRepos(t)
	t.Setenv("BRIDGE_REPOS_ROOT", root)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	_, err := resolveLaunchTarget("nope", "", "claude")
	var le errRepoLookup
	if !errors.As(err, &le) {
		t.Fatalf("want errRepoLookup, got %v", err)
	}
}

func TestResolveLaunchTargetUnknownAgent(t *testing.T) {
	root := writeFakeRepos(t)
	t.Setenv("BRIDGE_REPOS_ROOT", root)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	_, err := resolveLaunchTarget("bridge", "", "no-such-agent")
	var ae errAgentLookup
	if !errors.As(err, &ae) {
		t.Fatalf("want errAgentLookup, got %v", err)
	}
	if !strings.HasPrefix(err.Error(), "bridge: ") {
		t.Errorf("message %q lacks bridge: prefix", err.Error())
	}
}
