package main

import (
	"fmt"
	"io"
	"time"

	"github.com/freaxnx01/bridge/internal/dispatch"
	"github.com/freaxnx01/bridge/internal/forge"
)

func renderDecisions(w io.Writer, ds []dispatch.Decision) {
	dispatched, skipped := 0, 0
	for _, d := range ds {
		status := "dispatch"
		if !d.Dispatch {
			status = fmt.Sprintf("SKIP (%s)", d.Reason)
			skipped++
		} else {
			dispatched++
		}
		fmt.Fprintf(w, "  %-12s #%-4d %-28s → %s\n",
			d.Candidate.Repo, d.Candidate.Issue.Number, truncate(d.Candidate.Issue.Title, 28), status)
	}
	fmt.Fprintf(w, "\n%d dispatched, %d skipped\n", dispatched, skipped)
}

// repoInput is one repo's fetched state, kept as a plain struct so
// collectCandidates stays testable without a network.
type repoInput struct {
	Forge      string
	Owner      string
	Name       string
	Issues     []forge.Issue
	Milestones []forge.Milestone
	PRs        []forge.PullRequest
}

// collectCandidates filters each repo's issues to the eligible ones.
// Non-GitHub repos are skipped silently: ai-implement runs on GitHub Actions,
// so there is no pipeline to dispatch to elsewhere.
func collectCandidates(repos []repoInput) []dispatch.Candidate {
	var out []dispatch.Candidate
	for _, r := range repos {
		if r.Forge != "github" {
			continue
		}
		active := dispatch.ActiveMilestone(r.Milestones)
		due := milestoneDue(r.Milestones, active)
		for _, i := range r.Issues {
			if ok, _ := dispatch.Eligible(i, active, r.PRs); !ok {
				continue
			}
			out = append(out, dispatch.Candidate{
				Issue: i, Owner: r.Owner, Repo: r.Name, MilestoneDue: due,
			})
		}
	}
	return out
}

func milestoneDue(ms []forge.Milestone, title string) time.Time {
	for _, m := range ms {
		if m.Title == title {
			return m.DueOn
		}
	}
	return time.Time{}
}
