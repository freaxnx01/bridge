//go:build e2e

package e2e

import "testing"

func TestE2EListHuman(t *testing.T) {
	root := fixtureRoot(t)
	cache := t.TempDir()
	out, _, code := run(t, root, cache, "list")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	for _, want := range []string{"bridge", "secret", "glrepo"} {
		contains(t, out, want, "list stdout")
	}
}

func TestE2EListJSON(t *testing.T) {
	root := fixtureRoot(t)
	cache := t.TempDir()
	out, _, code := run(t, root, cache, "list", "--json")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	contains(t, out, "\"name\":", "list --json stdout")
	contains(t, out, "\"forge\":", "list --json stdout")
}
