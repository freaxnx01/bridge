package nav

import (
	"testing"
	"time"
)

func TestHumanLastAccessed_TwoUnitsMax(t *testing.T) {
	tests := []struct {
		name string
		d    time.Duration
		want string
	}{
		{"seconds", 30 * time.Second, "0m"},
		{"minutes", 4 * time.Minute, "4m"},
		{"hours-minutes", 3*time.Hour + 12*time.Minute, "3h 12m"},
		{"days-hours", 26*time.Hour + 20*time.Minute, "1d 2h"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := humanLastAccessed(tt.d); got != tt.want {
				t.Errorf("humanLastAccessed(%v) = %q, want %q", tt.d, got, tt.want)
			}
		})
	}
}

func TestFilterRepos_CaseInsensitiveSubstring(t *testing.T) {
	rows := []repoRow{
		{label: "github/public/bridge"},
		{label: "github/public/ai-instructions"},
		{label: "gitlab/acme/infra-tools"},
	}
	got := filterRepos(rows, "INFRA")
	if len(got) != 1 || got[0].label != "gitlab/acme/infra-tools" {
		t.Fatalf("filterRepos = %+v, want only infra-tools", got)
	}
	if len(filterRepos(rows, "")) != 3 {
		t.Errorf("empty filter should return all rows")
	}
}
