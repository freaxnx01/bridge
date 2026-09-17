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
	if len(c.Schedule.Windows) != 2 || !c.Schedule.Windows[1].BudgetRung {
		t.Errorf("default windows: %+v", c.Schedule.Windows)
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

func TestLoadConfigIgnoresRetiredScheduleKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dispatch.json")
	// A config written before windows existed must still load.
	os.WriteFile(path, []byte(`{"schedule":{"dispatch_at":"22:00","retry_until":"06:00"}}`), 0o600)

	c, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("retired keys must be ignored, not fatal: %v", err)
	}
	if len(c.Schedule.Windows) != 2 {
		t.Errorf("windows must fall back to defaults: %+v", c.Schedule.Windows)
	}
}

func TestLoadConfigWindowsReplaceTheDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dispatch.json")
	os.WriteFile(path, []byte(`{"schedule":{"windows":[{"from":"09:00","to":"10:00","budget_rung":true}]}}`), 0o600)

	c, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Schedule.Windows) != 1 || c.Schedule.Windows[0].From != "09:00" {
		t.Errorf("a configured list must replace the defaults, not merge: %+v", c.Schedule.Windows)
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
