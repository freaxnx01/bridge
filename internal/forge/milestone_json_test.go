package forge

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestMilestone_MarshalJSON_DueOn(t *testing.T) {
	tests := []struct {
		name    string
		dueOn   time.Time
		wantKey bool
	}{
		{"zero due date is omitted", time.Time{}, false},
		{"set due date is kept", time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := json.Marshal(Milestone{Number: 3, Title: "v1", DueOn: tc.dueOn})
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.Contains(string(raw), `"due_on"`); got != tc.wantKey {
				t.Fatalf("due_on present = %v, want %v in %s", got, tc.wantKey, raw)
			}
		})
	}
}
