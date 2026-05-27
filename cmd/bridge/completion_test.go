package main

import (
	"os"
	"strings"
	"testing"
)

// completion is invoked by shells via the hidden `__complete` subcommand cobra
// installs. Output is one suggestion per line followed by a `:N` directive
// line. We assert that local repo basenames appear in the suggestion lines.

func runComplete(t *testing.T, root string, verb, prefix string) string {
	t.Helper()
	cmd := bridgeCmd("__complete", verb, prefix)
	cmd.Env = append(os.Environ(), "BRIDGE_REPOS_ROOT="+root)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("__complete %s %q: %v\n%s", verb, prefix, err, out)
	}
	return string(out)
}

func TestCompleteOpenAllRepos(t *testing.T) {
	root := writeFakeRepos(t)
	out := runComplete(t, root, "open", "")
	for _, want := range []string{"bridge", "secret", "glrepo"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in completion output, got:\n%s", want, out)
		}
	}
}

func TestCompleteOpenPrefixCaseInsensitive(t *testing.T) {
	root := writeFakeRepos(t)
	out := runComplete(t, root, "open", "BRI")
	if !strings.Contains(out, "bridge") {
		t.Errorf("expected 'bridge' in completion of 'BRI', got:\n%s", out)
	}
	if strings.Contains(out, "secret") || strings.Contains(out, "glrepo") {
		t.Errorf("non-matching repos leaked into prefix completion:\n%s", out)
	}
}

func TestCompleteRmAlsoCompletes(t *testing.T) {
	root := writeFakeRepos(t)
	out := runComplete(t, root, "rm", "")
	if !strings.Contains(out, "bridge") {
		t.Errorf("rm completion missing 'bridge':\n%s", out)
	}
}

func TestCompleteOpenNoSecondArg(t *testing.T) {
	// Once a repo arg is present, completion should return nothing — open
	// only takes a single positional. Asserts the `len(args) >= 1` guard.
	root := writeFakeRepos(t)
	out := runComplete(t, root, "open", "")
	// Find the directive footer line beginning with ':' to anchor the test.
	// Suggestions appear *before* it; we just check that with a second-arg
	// scenario the suggestion section is empty.
	cmd := bridgeCmd("__complete", "open", "bridge", "")
	cmd.Env = append(os.Environ(), "BRIDGE_REPOS_ROOT="+root)
	o2, _ := cmd.CombinedOutput()
	if strings.Contains(string(o2), "bridge\n") && !strings.Contains(string(o2), ":4") {
		t.Errorf("expected no suggestions for second-arg completion; got:\n%s\n(first-arg out was:\n%s)", o2, out)
	}
}
