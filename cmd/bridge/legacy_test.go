package main

import (
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestRewriteLegacyDashR(t *testing.T) {
	got := rewriteLegacyArgs([]string{"bridge", "-r"})
	want := []string{"bridge", "list", "-r"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestRewriteLegacyDashRWithRefresh(t *testing.T) {
	got := rewriteLegacyArgs([]string{"bridge", "-r", "--refresh"})
	want := []string{"bridge", "list", "-r", "--refresh"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestRewriteLegacyStandaloneRefresh(t *testing.T) {
	got := rewriteLegacyArgs([]string{"bridge", "--refresh"})
	want := []string{"bridge", "list", "--refresh"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestRewriteLegacyDashD(t *testing.T) {
	got := rewriteLegacyArgs([]string{"bridge", "-D", "old-repo"})
	want := []string{"bridge", "rm", "old-repo", "--yes"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestRewriteLegacyDashDNoName(t *testing.T) {
	// No name supplied — pass through; cobra will surface the error.
	in := []string{"bridge", "-D"}
	got := rewriteLegacyArgs(in)
	if !reflect.DeepEqual(got, in) {
		t.Errorf("got %v, want unchanged %v", got, in)
	}
}

func TestRewriteLegacyAway(t *testing.T) {
	for _, mode := range []string{"away", "back", "auto"} {
		got := rewriteLegacyArgs([]string{"bridge", mode})
		want := []string{"bridge", "presence", mode}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s: got %v, want %v", mode, got, want)
		}
	}
}

func TestRewriteLegacyLeavesKnownVerbsAlone(t *testing.T) {
	in := []string{"bridge", "list", "--refresh"}
	got := rewriteLegacyArgs(in)
	if !reflect.DeepEqual(got, in) {
		t.Errorf("known verb mutated: got %v", got)
	}
}

func TestRewriteLegacyLeavesPreflightAlone(t *testing.T) {
	in := []string{"bridge", "__preflight", "-r"}
	got := rewriteLegacyArgs(in)
	if !reflect.DeepEqual(got, in) {
		t.Errorf("preflight rewritten outer layer: got %v", got)
	}
}

func TestRewriteLegacyLeavesAwayAsArgToPresence(t *testing.T) {
	// If the user already typed `bridge presence away`, do nothing.
	in := []string{"bridge", "presence", "away"}
	got := rewriteLegacyArgs(in)
	if !reflect.DeepEqual(got, in) {
		t.Errorf("got %v want unchanged", got)
	}
}

func TestRewriteLegacyShortArgs(t *testing.T) {
	// Just program name — no rewrite, no panic.
	in := []string{"bridge"}
	got := rewriteLegacyArgs(in)
	if !reflect.DeepEqual(got, in) {
		t.Errorf("got %v want unchanged", got)
	}
}

func TestRewriteLegacyPreflightDashR(t *testing.T) {
	got := rewriteLegacyPreflight([]string{"-r"})
	want := []string{"list", "-r"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
}

func TestRewriteLegacyPreflightAway(t *testing.T) {
	got := rewriteLegacyPreflight([]string{"away"})
	want := []string{"presence", "away"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
}

func TestRewriteLegacyPreflightDashD(t *testing.T) {
	got := rewriteLegacyPreflight([]string{"-D", "foo"})
	want := []string{"rm", "foo", "--yes"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
}

func TestLegacyDashRListsLocal(t *testing.T) {
	root := writeFakeRepos(t)
	cache := t.TempDir()
	cmd := bridgeCmd("-r", "--json")
	cmd.Env = append(envWithout("TMUX"),
		"BRIDGE_REPOS_ROOT="+root,
		"XDG_CACHE_HOME="+cache,
		"GH_TOKEN=", "GITLAB_TOKEN=", "FORGEJO_TOKEN=",
		"BRIDGE_GITHUB_API=", "BRIDGE_GITLAB_API=", "BRIDGE_FORGEJO_API=",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), `"local"`) {
		t.Errorf("expected -r to take list -r shape (with `local` key), got: %s", out)
	}
}

// silence the import warning if no test currently uses os
var _ = os.Stdout
