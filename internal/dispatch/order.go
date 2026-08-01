package dispatch

import (
	"path"
	"slices"
	"strings"
	"time"

	"github.com/freaxnx01/bridge/internal/forge"
)

// Candidate is one issue considered for dispatch, with the repo context the
// ordering ladder needs.
type Candidate struct {
	Issue        forge.Issue
	Owner        string
	Repo         string // bare name, e.g. "quotes"
	MilestoneDue time.Time
}

// typeRank maps an issue's labels to the ladder's third rung.
// Lower sorts first.
func typeRank(labels []string) int {
	for _, l := range labels {
		switch strings.ToLower(l) {
		case "bug", "fix":
			return 0
		}
	}
	for _, l := range labels {
		if strings.ToLower(l) == "feat" {
			return 1
		}
	}
	return 2
}

// sizeRank maps size:s|m|l to the ladder's fourth rung. Unlabelled is "m",
// so an unsized issue never jumps ahead of a deliberate quick win.
func sizeRank(labels []string) int {
	for _, l := range labels {
		switch strings.ToLower(l) {
		case "size:s":
			return 0
		case "size:m":
			return 1
		case "size:l":
			return 2
		}
	}
	return 1
}

// repoPriorityRank maps a repo to the ladder's first rung: the index of the
// first pattern it matches, scanned in list order (path.Match glob syntax, so
// a literal name matches only itself). A repo matching nothing ranks after
// every configured entry; an empty list ranks every repo 0, which makes the
// rung a no-op. A malformed pattern simply does not match — ordering must not
// fail on a config typo.
func repoPriorityRank(repo string, patterns []string) int {
	for i, p := range patterns {
		if ok, err := path.Match(p, repo); err == nil && ok {
			return i
		}
	}
	return len(patterns)
}

// Order sorts candidates by the deterministic ladder: repo priority, then
// milestone due date, then type, then size, then age. It returns a new slice.
func Order(cs []Candidate, repoPriority []string) []Candidate {
	out := slices.Clone(cs)
	slices.SortStableFunc(out, func(a, b Candidate) int {
		if c := repoPriorityRank(a.Repo, repoPriority) - repoPriorityRank(b.Repo, repoPriority); c != 0 {
			return c
		}
		if c := compareDue(a.MilestoneDue, b.MilestoneDue); c != 0 {
			return c
		}
		if c := typeRank(a.Issue.Labels) - typeRank(b.Issue.Labels); c != 0 {
			return c
		}
		if c := sizeRank(a.Issue.Labels) - sizeRank(b.Issue.Labels); c != 0 {
			return c
		}
		return a.Issue.Created.Compare(b.Issue.Created)
	})
	return out
}

// compareDue sorts dated milestones before undated ones — a zero time means
// "no milestone", which must sort last rather than earliest.
func compareDue(a, b time.Time) int {
	switch {
	case a.IsZero() && b.IsZero():
		return 0
	case a.IsZero():
		return 1
	case b.IsZero():
		return -1
	default:
		return a.Compare(b)
	}
}
