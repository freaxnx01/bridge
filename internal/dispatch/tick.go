package dispatch

import (
	"time"

	"github.com/freaxnx01/bridge/internal/forge"
)

// RepoInput is one repo's fetched state, kept as a plain struct so
// CollectCandidates stays testable without a network.
type RepoInput struct {
	Forge      string
	Owner      string
	Name       string
	Issues     []forge.Issue
	Milestones []forge.Milestone
	PRs        []forge.PullRequest
}

// CollectCandidates filters each repo's issues to the eligible ones.
// Non-GitHub repos are skipped silently: ai-implement runs on GitHub Actions,
// so there is no pipeline to dispatch to elsewhere.
func CollectCandidates(repos []RepoInput) []Candidate {
	var out []Candidate
	for _, r := range repos {
		if r.Forge != "github" {
			continue
		}
		active := ActiveMilestone(r.Milestones)
		due := MilestoneDue(r.Milestones, active)
		for _, i := range r.Issues {
			if ok, _ := Eligible(i, active, r.PRs); !ok {
				continue
			}
			out = append(out, Candidate{
				Issue: i, Owner: r.Owner, Repo: r.Name,
				Milestone: active, MilestoneDue: due,
			})
		}
	}
	return out
}

// MilestoneDue returns the due date of the milestone named title, zero when
// the repo has no such milestone or it carries no due date.
func MilestoneDue(ms []forge.Milestone, title string) time.Time {
	for _, m := range ms {
		if m.Title == title {
			return m.DueOn
		}
	}
	return time.Time{}
}

// CountOpenAgentPRs counts open PRs that close one of the repo's own issues.
// Only those are pipeline output, so a hand-written PR never consumes a slot.
//
// An autonomous lane's PRs are left out of the global total entirely: that cap
// is the operator's review capacity, and nobody reviews them. They still count
// per repo, where the bound is conflicting concurrent PRs rather than review.
func CountOpenAgentPRs(repos []RepoInput, autonomous func(repo string) bool) (map[string]int, int) {
	byRepo := make(map[string]int, len(repos))
	total := 0
	for _, r := range repos {
		// Resolved once per repo: the answer cannot change within one repo, and
		// ResolveLane walks every lane's globs.
		skipGlobal := autonomous(r.Name)
		for _, pr := range r.PRs {
			for _, i := range r.Issues {
				if ClosesIssue(pr.Body, i.Number) {
					byRepo[r.Name]++
					if !skipGlobal {
						total++
					}
					break
				}
			}
		}
	}
	return byRepo, total
}

// MergeInOrder puts the decision groups back into the order Order produced.
// ApplyCaps only ever sees the acting candidates, so appending the
// out-of-window skips afterwards would render --dry-run — the review surface —
// out of ladder order.
func MergeInOrder(ordered []Candidate, groups ...[]Decision) []Decision {
	type key struct {
		repo   string
		number int
	}
	byKey := make(map[key]Decision)
	for _, g := range groups {
		for _, d := range g {
			byKey[key{d.Candidate.Repo, d.Candidate.Issue.Number}] = d
		}
	}
	out := make([]Decision, 0, len(byKey))
	for _, c := range ordered {
		if d, ok := byKey[key{c.Repo, c.Issue.Number}]; ok {
			out = append(out, d)
		}
	}
	return out
}

// AssignLanes resolves each candidate's lane in place, before ordering, so the
// cap walk and the label write read the same decision.
func AssignLanes(cs []Candidate, lanes []Lane, gate GateState) {
	for i := range cs {
		cs[i].Lane, cs[i].LaneReason = ResolveLane(lanes, cs[i].Repo, gate)
	}
}

// LaneCounts reads each configured lane's spent counter for the occurrence it
// is currently in. The implicit default lane is bounded by the nightly cap, not
// by a lane ceiling, so it needs no entry here.
func LaneCounts(state State, cfg Config, now time.Time) map[string]int {
	out := make(map[string]int, len(cfg.Lanes))
	for _, l := range cfg.Lanes {
		out[l.Name] = state.DispatchesInLane(l.Name, l.WindowStart(cfg.Schedule, now))
	}
	return out
}

// AnyLaneActs reports whether any lane acts at now. The implicit default lane
// is always considered: a repo no configured lane claims still dispatches in
// it, on the top-level schedule.
func AnyLaneActs(cfg Config, now time.Time) bool {
	if DefaultLane().InWindow(cfg.Schedule, now) {
		return true
	}
	for _, l := range cfg.Lanes {
		if l.InWindow(cfg.Schedule, now) {
			return true
		}
	}
	return false
}
