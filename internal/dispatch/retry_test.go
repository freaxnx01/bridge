package dispatch

import "testing"

func TestRetryCandidate(t *testing.T) {
	tests := []struct {
		name       string
		labels     []string
		wantOK     bool
		wantReason string
	}{
		{"a failed dispatched issue", []string{"ai-implement", "ai:failed"}, true, ""},
		{"extra labels do not matter", []string{"ai-implement", "ai:failed", "feat"}, true, ""},
		{"dispatched but not failed", []string{"ai-implement"}, false, ""},
		{"failed but no longer dispatched", []string{"ai:failed"}, false, ""},
		{"no labels at all", nil, false, ""},
		{"parked", []string{"ai-implement", "ai:failed", "parked"}, false, "parked"},
		{"parked, legacy spelling", []string{"ai-implement", "ai:failed", "🧊 parked"}, false, "parked"},
		{"paused", []string{"ai-implement", "ai:failed", "ai:paused"}, false, "paused"},
		{"an interrupted swap", []string{"ai-implement", "ai:failed", "ai:retrying"}, false, "retry interrupted"},
		{"a swap interrupted after step 2", []string{"ai:retrying"}, false, "retry interrupted"},
		{"retrying beats parked", []string{"ai:retrying", "parked"}, false, "retry interrupted"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ok, reason := RetryCandidate(tc.labels)
			if ok != tc.wantOK || reason != tc.wantReason {
				t.Errorf("got (%v, %q) want (%v, %q)", ok, reason, tc.wantOK, tc.wantReason)
			}
		})
	}
}
