package main

import (
	"os"
	"strings"
	"testing"

	"github.com/freaxnx01/bridge/internal/core"
)

// In the local-only picker, two clones of the same repo name from different
// owners base-render identically (github/public/ai-instructions); the owner is
// injected so they're distinguishable. Uniquely-named repos keep the clean
// label. Each line is "<label>\t<path>".
func TestLocalPickerRows_SameNameDifferentOwners_OwnerQualified(t *testing.T) {
	repos := []core.Repo{
		{Forge: "github", Owner: "freaxnx01", Name: "ai-instructions", Visibility: "public", Path: "/r/freaxnx01/ai-instructions"},
		{Forge: "github", Owner: "anim-bossinfo-ch", Name: "ai-instructions", Visibility: "public", Path: "/r/anim/ai-instructions"},
		{Forge: "github", Owner: "freaxnx01", Name: "bridge", Visibility: "public", Path: "/r/freaxnx01/bridge"},
	}
	got := localPickerRows(repos)

	want := []string{
		"github/public/freaxnx01/ai-instructions\t/r/freaxnx01/ai-instructions",
		"github/public/anim-bossinfo-ch/ai-instructions\t/r/anim/ai-instructions",
		"github/public/bridge\t/r/freaxnx01/bridge", // unique name: clean label
	}
	for _, w := range want {
		if !strings.Contains(got, w+"\n") {
			t.Errorf("localPickerRows missing line %q; got:\n%s", w, got)
		}
	}
}

func TestPickerFixtureCD(t *testing.T) {
	root := writeFakeRepos(t)
	cache := t.TempDir()
	cmd := bridgeCmd("__preflight")
	cmd.Env = append(envWithout("BRIDGE_DEFAULT_AGENT", "BRIDGE_DEFAULT_AGENT_ARGS"),
		"BRIDGE_REPOS_ROOT="+root,
		"XDG_CACHE_HOME="+cache,
		"BRIDGE_PICKER_FIXTURE=bridge",
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

func TestPickerFixtureCancel(t *testing.T) {
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
	if strings.TrimSpace(string(out)) != "cancel" {
		t.Errorf("got %q", out)
	}
}
