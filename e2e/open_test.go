//go:build e2e

package e2e

import (
	"strings"
	"testing"
)

// TestE2EOpenPreflightEmitsCD asserts the shim directive contract: when
// the user runs `bridge open <name>` via the shim, the binary's
// `__preflight open <name>` emits a `cd:<path>` line. The shim parses
// that and changes the parent shell's CWD.
func TestE2EOpenPreflightEmitsCD(t *testing.T) {
	root := fixtureRoot(t)
	cache := t.TempDir()
	out, _, code := run(t, root, cache, "__preflight", "open", "bridge")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	trimmed := strings.TrimSpace(out)
	if !strings.HasPrefix(trimmed, "cd:") {
		t.Fatalf("expected cd: directive, got %q", trimmed)
	}
	if !strings.HasSuffix(trimmed, "github/freaxnx01/public/bridge") {
		t.Errorf("expected path to fixture bridge repo, got %q", trimmed)
	}
}

func TestE2EOpenUnknownRepoExits2(t *testing.T) {
	root := fixtureRoot(t)
	cache := t.TempDir()
	_, stderr, code := run(t, root, cache, "open", "does-not-exist")
	if code != 2 {
		t.Errorf("expected exit 2 on unknown repo, got %d (stderr=%q)", code, stderr)
	}
}
