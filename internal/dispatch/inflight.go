package dispatch

import (
	"slices"

	"github.com/freaxnx01/bridge/internal/forge"
)

// InFlight reports whether a workflow run is going for this issue.
func InFlight(i forge.Issue, runs []forge.WorkflowRun) bool {
	return slices.ContainsFunc(runs, func(r forge.WorkflowRun) bool { return IsIssueRunFor(i, r) })
}

// IsIssueRunFor reports whether the run is in flight for this issue. A run names
// its issue only by display title, which for an `issues` event is the title.
func IsIssueRunFor(i forge.Issue, r forge.WorkflowRun) bool {
	return r.Event == "issues" && (r.Status == "queued" || r.Status == "in_progress") &&
		r.DisplayTitle == i.Title
}
