package dispatch

import (
	"testing"
	"time"

	"github.com/freaxnx01/bridge/internal/forge"
)

func TestAttempts(t *testing.T) {
	cases := []struct {
		labels []string
		want   int
	}{
		{nil, 0},
		{[]string{"feat"}, 0},
		{[]string{"attempt:1"}, 1},
		{[]string{"feat", "attempt:2"}, 2},
		{[]string{"attempt:notanumber"}, 0},
	}
	for _, c := range cases {
		if got := Attempts(c.labels); got != c.want {
			t.Errorf("Attempts(%v) = %d, want %d", c.labels, got, c.want)
		}
	}
}

func TestActiveMilestoneEarliestDueDate(t *testing.T) {
	d := func(s string) time.Time {
		v, _ := time.Parse("2006-01-02", s)
		return v
	}
	ms := []forge.Milestone{
		{Title: "later", DueOn: d("2026-09-01")},
		{Title: "sooner", DueOn: d("2026-08-15")},
		{Title: "someday"}, // no due date — never active
	}
	if got := ActiveMilestone(ms); got != "sooner" {
		t.Errorf("got %q", got)
	}
	if got := ActiveMilestone([]forge.Milestone{{Title: "someday"}}); got != "" {
		t.Errorf("undated milestone must not be active, got %q", got)
	}
	if got := ActiveMilestone(nil); got != "" {
		t.Errorf("got %q", got)
	}
}

func TestClosesIssue(t *testing.T) {
	cases := []struct {
		body string
		want bool
	}{
		{"Closes #41", true},
		{"closes #41", true},
		{"Fixes #41", true},
		{"Resolves #41", true},
		{"body\n\nCloses #41\n", true},
		{"Closes #410", false}, // must not match a longer number
		{"Closes #4", false},
		{"mentions #41 casually", false},
		{"", false},
	}
	for _, c := range cases {
		if got := ClosesIssue(c.body, 41); got != c.want {
			t.Errorf("ClosesIssue(%q, 41) = %v, want %v", c.body, got, c.want)
		}
	}
}

func TestEligible(t *testing.T) {
	base := forge.Issue{Number: 41, Labels: []string{"feat"}, Milestone: "v2 search"}

	cases := []struct {
		name       string
		issue      forge.Issue
		milestone  string
		prs        []forge.PullRequest
		wantOK     bool
		wantReason string
	}{
		{"happy path", base, "v2 search", nil, true, ""},
		{"no active milestone dispatches anyway",
			forge.Issue{Number: 41, Milestone: ""}, "", nil, true, ""},
		{"not enriched",
			forge.Issue{Number: 41, Labels: []string{"needs-enrichment"}}, "", nil,
			false, "needs-enrichment"},
		{"parked",
			forge.Issue{Number: 41, Labels: []string{"🧊 parked"}}, "", nil,
			false, "parked"},
		{"attempt budget spent",
			forge.Issue{Number: 41, Labels: []string{"attempt:2"}}, "", nil,
			false, "attempts exhausted"},
		{"has open PR", base, "v2 search",
			[]forge.PullRequest{{Number: 90, Body: "Closes #41"}},
			false, "open PR"},
		{"already dispatched, no open PR",
			forge.Issue{Number: 41, Labels: []string{"ai-implement"}}, "", nil,
			false, "already dispatched"},
		{"outside active milestone",
			forge.Issue{Number: 41, Milestone: "backlog"}, "v2 search", nil,
			false, "outside active milestone"},
	}
	for _, c := range cases {
		ok, reason := Eligible(c.issue, c.milestone, c.prs)
		if ok != c.wantOK || reason != c.wantReason {
			t.Errorf("%s: got (%v, %q), want (%v, %q)", c.name, ok, reason, c.wantOK, c.wantReason)
		}
	}
}
