package dispatch

import (
	"strings"
	"testing"

	"github.com/freaxnx01/bridge/internal/forge"
)

func TestIsTransient(t *testing.T) {
	for _, b := range []string{"api_auth", "rate_limit", "infra"} {
		if !IsTransient(b) {
			t.Errorf("%s should be transient", b)
		}
	}
	for _, b := range []string{"max_turns", "gate_failed", "no_diff", "", "unknown"} {
		if IsTransient(b) {
			t.Errorf("%s should not be transient", b)
		}
	}
}

func TestNextActionTransientRetriesWithoutIncrementing(t *testing.T) {
	i := forge.Issue{Number: 41, Labels: []string{"failed:rate_limit", "ai-implement"}}
	a := NextAction(i)

	if !a.Retry {
		t.Error("transient failure must retry")
	}
	for _, l := range a.AddLabels {
		if strings.HasPrefix(l, "attempt:") {
			t.Errorf("transient must not increment attempts, got %v", a.AddLabels)
		}
	}
	// The stale failure label must be cleared or the next tick re-reads it.
	if len(a.RemoveLabels) != 1 || a.RemoveLabels[0] != "failed:rate_limit" {
		t.Errorf("RemoveLabels: %v", a.RemoveLabels)
	}
}

func TestNextActionSubstantiveIncrementsToOne(t *testing.T) {
	i := forge.Issue{Number: 41, Labels: []string{"failed:max_turns"}}
	a := NextAction(i)

	if !contains(a.AddLabels, "attempt:1") {
		t.Errorf("AddLabels: %v", a.AddLabels)
	}
	if contains(a.AddLabels, LabelParked) {
		t.Error("must not park on the first substantive failure")
	}
	if !a.Retry {
		t.Error("first substantive failure still retries")
	}
}

func TestNextActionSecondSubstantiveParks(t *testing.T) {
	i := forge.Issue{Number: 41, Labels: []string{"failed:gate_failed", "attempt:1"}}
	a := NextAction(i)

	if !contains(a.AddLabels, "attempt:2") {
		t.Errorf("AddLabels: %v", a.AddLabels)
	}
	if !contains(a.AddLabels, LabelParked) {
		t.Errorf("second substantive failure must park: %v", a.AddLabels)
	}
	if a.Retry {
		t.Error("parked issues must not retry")
	}
	if !strings.Contains(a.Comment, "gate_failed") {
		t.Errorf("comment must name the bucket: %q", a.Comment)
	}
	// The old counter must go, or the issue carries attempt:1 and attempt:2.
	if !contains(a.RemoveLabels, "attempt:1") {
		t.Errorf("RemoveLabels: %v", a.RemoveLabels)
	}
}

func TestNextActionNoFailureIsNoop(t *testing.T) {
	a := NextAction(forge.Issue{Number: 41, Labels: []string{"feat"}})
	if a.Retry || len(a.AddLabels) != 0 || len(a.RemoveLabels) != 0 || a.Comment != "" {
		t.Errorf("expected zero Action, got %+v", a)
	}
}

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}
