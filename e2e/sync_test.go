//go:build e2e

package e2e

import "testing"

// TestE2ESyncReadOnly asserts the read-only `bridge sync` (no args) runs
// against an empty cache and exits 0. The interactive `sync now` /
// `sync --auto` paths shell out to git and are out of scope here.
func TestE2ESyncReadOnly(t *testing.T) {
	root := fixtureRoot(t)
	cache := t.TempDir()
	_, _, code := run(t, root, cache, "sync")
	if code != 0 {
		t.Errorf("sync exit %d (expected 0 on empty cache)", code)
	}
}

func TestE2ESyncJSON(t *testing.T) {
	root := fixtureRoot(t)
	cache := t.TempDir()
	out, _, code := run(t, root, cache, "sync", "--json")
	if code != 0 {
		t.Fatalf("sync --json exit %d", code)
	}
	if len(out) == 0 {
		t.Errorf("expected JSON output, got empty")
	}
}
