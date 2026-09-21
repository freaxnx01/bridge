package dispatch

import (
	"os"
	"path/filepath"
	"slices"
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

func TestLoadConfigLanes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dispatch.json")
	os.WriteFile(path, []byte(`{"lanes":[
		{"name":"auto","repos":["game-*"],"autonomous":true,"dry_run":true,
		 "windows":[{"from":"00:00","to":"00:00"}],
		 "limits":{"per_repo":1,"max_dispatches":12}},
		{"name":"hitl","repos":["*"]}]}`), 0o600)

	c, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Lanes) != 2 {
		t.Fatalf("lanes: %+v", c.Lanes)
	}
	auto := c.Lanes[0]
	if auto.Name != "auto" || !auto.Autonomous || !auto.DryRun {
		t.Errorf("auto lane: %+v", auto)
	}
	if len(auto.Windows) != 1 || auto.Windows[0].From != "00:00" {
		t.Errorf("lane windows: %+v", auto.Windows)
	}
	if auto.Limits.MaxDispatches != 12 || auto.Limits.PerRepo != 1 {
		t.Errorf("lane limits: %+v", auto.Limits)
	}
	// Lanes are additive: the top-level limits a lane does not restate stay put.
	if c.Limits.GlobalOpenPRs != 3 {
		t.Errorf("top-level limits must survive a lanes-only config: %+v", c.Limits)
	}
}

func TestDefaultConfigHasNoLanes(t *testing.T) {
	// The zero-config case must keep pre-lane behaviour, which the implicit
	// DefaultLane provides. A default lane list would be a silent policy change.
	if got := DefaultConfig().Lanes; len(got) != 0 {
		t.Errorf("default config must not configure lanes: %+v", got)
	}
}

func TestEffectiveLabels(t *testing.T) {
	tests := []struct {
		name string
		lane Lane
		want []string
	}{
		{"autonomous defaults to both labels", Lane{Autonomous: true},
			[]string{LabelAIImplement, LabelAIReviewAIMerge}},
		{"plain lane defaults to the trigger alone", Lane{},
			[]string{LabelAIImplement}},
		{"an explicit list wins", Lane{Autonomous: true, Labels: []string{"ai-implement", "ai-review-human-merge"}},
			[]string{"ai-implement", "ai-review-human-merge"}},
		{"the implicit default lane applies the trigger alone", DefaultLane(),
			[]string{LabelAIImplement}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.lane.EffectiveLabels()
			if !slices.Equal(got, tc.want) {
				t.Errorf("got %v want %v", got, tc.want)
			}
		})
	}
}
