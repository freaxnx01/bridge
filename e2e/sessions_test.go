//go:build e2e

package e2e

import (
	"testing"
)

// TestE2ESessionsExits asserts the sessions command runs end-to-end on a
// host with no live tmux (the typical CI environment). It should not
// crash; an empty session list is a valid result.
func TestE2ESessionsExits(t *testing.T) {
	root := fixtureRoot(t)
	cache := t.TempDir()
	_, _, code := run(t, root, cache, "sessions")
	if code != 0 {
		t.Errorf("sessions exit %d (expected 0 with empty tmux state)", code)
	}
}

func TestE2ESessionsJSON(t *testing.T) {
	root := fixtureRoot(t)
	cache := t.TempDir()
	out, _, code := run(t, root, cache, "sessions", "--json")
	if code != 0 {
		t.Fatalf("sessions --json exit %d", code)
	}
	// Empty array is the expected baseline; just assert it's valid JSON
	// shape (starts with [ or {).
	if len(out) == 0 || (out[0] != '[' && out[0] != '{') {
		t.Errorf("expected JSON output, got %q", out)
	}
}
