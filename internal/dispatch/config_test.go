package dispatch

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfigMissingFileReturnsDefaults(t *testing.T) {
	c, err := LoadConfig(filepath.Join(t.TempDir(), "nope.json"))
	if err != nil {
		t.Fatalf("missing file must not error: %v", err)
	}
	if c.Limits.GlobalOpenPRs != 3 || c.Limits.PerRepo != 1 || c.Limits.MaxDispatchesPerNight != 5 {
		t.Errorf("defaults: %+v", c.Limits)
	}
	if c.Schedule.DispatchAt != "22:00" || c.Schedule.RetryUntil != "06:00" {
		t.Errorf("schedule: %+v", c.Schedule)
	}
}

func TestLoadConfigPartialFileKeepsDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dispatch.json")
	os.WriteFile(path, []byte(`{"limits":{"global_open_prs":7}}`), 0o600)

	c, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.Limits.GlobalOpenPRs != 7 {
		t.Errorf("override not applied: %d", c.Limits.GlobalOpenPRs)
	}
	// Unset keys must not become zero — a zero per_repo would dispatch nothing.
	if c.Limits.PerRepo != 1 {
		t.Errorf("per_repo should stay default, got %d", c.Limits.PerRepo)
	}
}

func TestLimitForUsesOverride(t *testing.T) {
	c := DefaultConfig()
	c.Limits.Overrides = map[string]int{"quotes": 2}
	if got := c.LimitFor("quotes"); got != 2 {
		t.Errorf("override: %d", got)
	}
	if got := c.LimitFor("bridge"); got != 1 {
		t.Errorf("default: %d", got)
	}
}
