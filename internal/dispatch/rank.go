package dispatch

import (
	"fmt"
	"path"
	"time"
)

// dueLayout renders a milestone due date and an issue's age in the one format
// an operator reads back against the forge.
const dueLayout = "2006-01-02"

// Ladder is one candidate's reading on every ordering rung, in rung order. It
// is what lets a reader check a verdict without re-deriving the ladder.
type Ladder struct {
	RepoPattern  string    `json:"repo_pattern,omitempty"` // matched repo_priority pattern, "" if none
	Milestone    string    `json:"milestone,omitempty"`
	MilestoneDue time.Time `json:"milestone_due,omitzero"`
	Type         string    `json:"type"` // "bug" | "feat" | "other"
	Size         string    `json:"size"` // "s" | "m" | "l"
	Created      time.Time `json:"created"`
}

// Rank is where one candidate landed and why it landed there rather than one
// place later.
type Rank struct {
	Position int    `json:"position"`
	Reason   string `json:"reason,omitempty"` // deciding rung vs the next candidate; "" for the last
	Ladder   Ladder `json:"ladder"`
}

// Explain annotates an already-ordered candidate list with each candidate's
// position, ladder reading, and the first rung on which it differs from the
// next candidate — the rung that decided the pair.
//
// It reuses the rank functions Order sorts by, so a reason can never name a
// rung Order did not actually compare. The returned slice has the same length
// and order as ordered.
func Explain(ordered []Candidate, repoPriority []string) []Rank {
	out := make([]Rank, 0, len(ordered))
	for i, c := range ordered {
		r := Rank{Position: i + 1, Ladder: ladderOf(c, repoPriority)}
		if i+1 < len(ordered) {
			r.Reason = decidingRung(c, ordered[i+1], repoPriority)
		}
		out = append(out, r)
	}
	return out
}

// decidingRung names the first rung a and b differ on, empty when they tie on
// every one — then the order between them is the stable sort's, not a decision.
func decidingRung(a, b Candidate, repoPriority []string) string {
	if repoPriorityRank(a.Repo, repoPriority) != repoPriorityRank(b.Repo, repoPriority) {
		return fmt.Sprintf("repo priority: %s vs %s",
			patternLabel(a.Repo, repoPriority), patternLabel(b.Repo, repoPriority))
	}
	if compareDue(a.MilestoneDue, b.MilestoneDue) != 0 {
		return fmt.Sprintf("milestone due: %s vs %s", dueLabel(a.MilestoneDue), dueLabel(b.MilestoneDue))
	}
	if typeRank(a.Issue.Labels) != typeRank(b.Issue.Labels) {
		return fmt.Sprintf("type: %s vs %s", typeLabel(a.Issue.Labels), typeLabel(b.Issue.Labels))
	}
	if sizeRank(a.Issue.Labels) != sizeRank(b.Issue.Labels) {
		return fmt.Sprintf("size: %s vs %s", sizeLabel(a.Issue.Labels), sizeLabel(b.Issue.Labels))
	}
	if a.Issue.Created.Compare(b.Issue.Created) != 0 {
		return fmt.Sprintf("age: %s vs %s",
			a.Issue.Created.Format(dueLayout), b.Issue.Created.Format(dueLayout))
	}
	return ""
}

func ladderOf(c Candidate, repoPriority []string) Ladder {
	return Ladder{
		RepoPattern:  matchedPattern(c.Repo, repoPriority),
		Milestone:    c.Milestone,
		MilestoneDue: c.MilestoneDue,
		Type:         typeLabel(c.Issue.Labels),
		Size:         sizeLabel(c.Issue.Labels),
		Created:      c.Issue.Created,
	}
}

// matchedPattern returns the repo_priority pattern repo matched, empty when it
// matched none — which is also what an empty list gives every repo.
func matchedPattern(repo string, patterns []string) string {
	for _, p := range patterns {
		if ok, err := path.Match(p, repo); err == nil && ok {
			return p
		}
	}
	return ""
}

// patternLabel is matchedPattern for a reason line, where "matched nothing" is
// itself the answer and has to be readable.
func patternLabel(repo string, patterns []string) string {
	if p := matchedPattern(repo, patterns); p != "" {
		return p
	}
	return "none"
}

func dueLabel(t time.Time) string {
	if t.IsZero() {
		return "none"
	}
	return t.Format(dueLayout)
}

func typeLabel(labels []string) string {
	switch typeRank(labels) {
	case 0:
		return "bug"
	case 1:
		return "feat"
	default:
		return "other"
	}
}

func sizeLabel(labels []string) string {
	switch sizeRank(labels) {
	case 0:
		return "s"
	case 2:
		return "l"
	default:
		return "m"
	}
}
