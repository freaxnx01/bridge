package dispatch

import (
	"testing"

	"github.com/freaxnx01/bridge/internal/forge"
)

func TestIsIssueRunFor(t *testing.T) {
	issue := forge.Issue{Number: 43, Title: "T"}

	tests := []struct {
		name string
		run  forge.WorkflowRun
		want bool
	}{
		{"issues event in progress for this title", forge.WorkflowRun{
			Event: "issues", Status: "in_progress", DisplayTitle: "T"}, true},
		{"queued counts too", forge.WorkflowRun{
			Event: "issues", Status: "queued", DisplayTitle: "T"}, true},
		{"a finished run is not in flight", forge.WorkflowRun{
			Event: "issues", Status: "completed", DisplayTitle: "T"}, false},
		{"another event names the workflow, not the issue", forge.WorkflowRun{
			Event: "workflow_dispatch", Status: "in_progress", DisplayTitle: "T"}, false},
		{"another issue's run", forge.WorkflowRun{
			Event: "issues", Status: "in_progress", DisplayTitle: "other"}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsIssueRunFor(issue, tc.run); got != tc.want {
				t.Errorf("got %v want %v", got, tc.want)
			}
		})
	}
}

func TestInFlight(t *testing.T) {
	issue := forge.Issue{Number: 43, Title: "T"}
	done := forge.WorkflowRun{Event: "issues", Status: "completed", DisplayTitle: "T"}
	other := forge.WorkflowRun{Event: "issues", Status: "in_progress", DisplayTitle: "other"}
	mine := forge.WorkflowRun{Event: "issues", Status: "in_progress", DisplayTitle: "T"}

	tests := []struct {
		name string
		runs []forge.WorkflowRun
		want bool
	}{
		{"no runs", nil, false},
		{"one matching run among others", []forge.WorkflowRun{done, other, mine}, true},
		{"only other issues' runs", []forge.WorkflowRun{done, other}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := InFlight(issue, tc.runs); got != tc.want {
				t.Errorf("got %v want %v", got, tc.want)
			}
		})
	}
}
