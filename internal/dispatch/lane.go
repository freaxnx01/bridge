package dispatch

import (
	"fmt"
	"path"
	"time"
)

// spans returns the spans this lane acts in: its own when configured, the
// schedule's otherwise. Inheriting is what keeps a lanes-only config from
// silently changing when the dispatcher runs.
func (l Lane) spans(s Schedule) []Span {
	if len(l.Windows) > 0 {
		return l.Windows
	}
	out := make([]Span, 0, len(s.Windows))
	for _, w := range s.Windows {
		out = append(out, w.Span)
	}
	return out
}

// InWindow reports whether the lane acts at now.
func (l Lane) InWindow(s Schedule, now time.Time) bool {
	for _, sp := range l.spans(s) {
		if sp.Covers(now) {
			return true
		}
	}
	return false
}

// WindowStart returns the start of the lane's current window occurrence — the
// boundary its dispatch counter resets at. Zero when no span covers now.
func (l Lane) WindowStart(s Schedule, now time.Time) time.Time {
	for _, sp := range l.spans(s) {
		if sp.Covers(now) {
			return sp.StartOf(now)
		}
	}
	return time.Time{}
}

// GateState answers, per repo, whether it meets an autonomous lane's
// precondition — agent.yml opting into ai-merge. Measuring it is the caller's
// job; keeping the fetch out here is what lets ResolveLane stay pure.
type GateState map[string]bool

// ResolveLane returns the lane a repo dispatches in, plus a reason when an
// autonomous lane was downgraded. First matching lane wins. An autonomous lane
// whose repo fails the gate falls through to the next matching non-autonomous
// lane — an unmet precondition must never dispatch into unattended merge.
func ResolveLane(lanes []Lane, repo string, gate GateState) (Lane, string) {
	downgradedFrom := ""
	for _, l := range lanes {
		if !matchesRepo(l.Repos, repo) {
			continue
		}
		if l.Autonomous && !gate[repo] {
			if downgradedFrom == "" {
				downgradedFrom = l.Name
			}
			continue
		}
		return l, downgradeReason(downgradedFrom, l.Name)
	}
	d := DefaultLane()
	return d, downgradeReason(downgradedFrom, d.Name)
}

// AnyAutonomousLaneMatches reports whether some autonomous lane claims this
// repo, which is what makes its agent.yml worth fetching.
func AnyAutonomousLaneMatches(lanes []Lane, repo string) bool {
	for _, l := range lanes {
		if l.Autonomous && matchesRepo(l.Repos, repo) {
			return true
		}
	}
	return false
}

// matchesRepo reports whether any pattern matches the bare repo name. A
// malformed pattern simply does not match, mirroring repoPriorityRank — lane
// assignment must not fail the tick on a config typo.
func matchesRepo(patterns []string, repo string) bool {
	for _, p := range patterns {
		if ok, err := path.Match(p, repo); err == nil && ok {
			return true
		}
	}
	return false
}

func downgradeReason(from, to string) string {
	if from == "" {
		return ""
	}
	return fmt.Sprintf("%s→%s: agent.yml lacks %s: true", from, to, LabelAIReviewAIMerge)
}
