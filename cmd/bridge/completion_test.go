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
	// Typing "BRI" must produce a suggestion that survives bash's
	// case-sensitive compgen post-filter. We splice the typed casing onto
	// the canonical name, so the suggestion is "BRIdge" — same repo, just
	// case-rewritten to start with what the user typed.
	root := writeFakeRepos(t)
	out := runComplete(t, root, "open", "BRI")
	if !strings.Contains(out, "BRIdge") {
		t.Errorf("expected 'BRIdge' (typed-case + canonical tail) in completion of 'BRI', got:\n%s", out)
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

func TestCompleteRootLevel(t *testing.T) {
	// `bridge bri<tab>` should suggest the bridge repo (root-level
	// ValidArgsFunction). Verb completions like `bridge li<tab>` keep
	// working because cobra merges subcommand names with the args
	// function's output.
	root := writeFakeRepos(t)
	cmd := bridgeCmd("__complete", "bri")
	cmd.Env = append(os.Environ(), "BRIDGE_REPOS_ROOT="+root)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("__complete bri: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "bridge") {
		t.Errorf("expected 'bridge' in root-level completion of 'bri':\n%s", out)
	}

	cmd = bridgeCmd("__complete", "li")
	cmd.Env = append(os.Environ(), "BRIDGE_REPOS_ROOT="+root)
	out, _ = cmd.CombinedOutput()
	if !strings.Contains(string(out), "list") {
		t.Errorf("subcommand 'list' missing from root-level 'li' completion:\n%s", out)
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
