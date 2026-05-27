//go:build e2e

package e2e

import (
	"testing"
)

// TestE2ECompleteOpenPrefix exercises cobra's hidden `__complete` entry
// point — the same one bash + powershell completion scripts invoke under
// the hood. Returning suggestions here means tab-completion will work in
// any shell cobra emits a completer for.
func TestE2ECompleteOpenPrefix(t *testing.T) {
	root := fixtureRoot(t)
	cache := t.TempDir()
	out, _, code := run(t, root, cache, "__complete", "open", "bri")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	contains(t, out, "bridge", "__complete stdout")
}

func TestE2ECompleteRootMixesVerbsAndRepos(t *testing.T) {
	root := fixtureRoot(t)
	cache := t.TempDir()
	out, _, code := run(t, root, cache, "__complete", "")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	// A subcommand and a repo basename should both be reachable for the
	// root-level completer.
	contains(t, out, "list", "__complete root stdout (verb)")
	contains(t, out, "bridge", "__complete root stdout (repo)")
}

func TestE2ECompletionScriptsEmit(t *testing.T) {
	root := fixtureRoot(t)
	cache := t.TempDir()
	for _, shell := range []string{"bash", "zsh", "fish", "powershell"} {
		out, _, code := run(t, root, cache, "completion", shell)
		if code != 0 {
			t.Errorf("completion %s exit %d", shell, code)
			continue
		}
		if len(out) < 200 {
			t.Errorf("completion %s output suspiciously short (%d bytes)", shell, len(out))
		}
	}
}
