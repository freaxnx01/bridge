package dispatch

import (
	"testing"
	"time"

	"github.com/freaxnx01/bridge/internal/forge"
)

func day(s string) time.Time {
	v, _ := time.Parse("2006-01-02", s)
	return v
}

func TestOrderLadder(t *testing.T) {
	cs := []Candidate{
		{Repo: "a", Issue: forge.Issue{Number: 1, Labels: []string{"chore"}, Created: day("2026-01-01")}},
		{Repo: "b", Issue: forge.Issue{Number: 2, Labels: []string{"bug"}, Created: day("2026-05-01")}},
		{Repo: "c", Issue: forge.Issue{Number: 3, Labels: []string{"feat"}, Created: day("2026-02-01")}},
		{Repo: "d", Issue: forge.Issue{Number: 4, Labels: []string{"bug"}, Created: day("2026-06-01")},
			MilestoneDue: day("2026-08-01")},
	}
	got := Order(cs)
	want := []int{4, 2, 3, 1} // milestone first, then bug, feat, chore
	for i, n := range want {
		if got[i].Issue.Number != n {
			t.Fatalf("position %d: got #%d, want #%d (full order %v)", i, got[i].Issue.Number, n, numbers(got))
		}
	}
}

func TestOrderSizeThenAge(t *testing.T) {
	cs := []Candidate{
		{Repo: "a", Issue: forge.Issue{Number: 1, Labels: []string{"feat", "size:l"}, Created: day("2026-01-01")}},
		{Repo: "b", Issue: forge.Issue{Number: 2, Labels: []string{"feat", "size:s"}, Created: day("2026-06-01")}},
		{Repo: "c", Issue: forge.Issue{Number: 3, Labels: []string{"feat"}, Created: day("2026-03-01")}},
		{Repo: "d", Issue: forge.Issue{Number: 4, Labels: []string{"feat"}, Created: day("2026-02-01")}},
	}
	got := Order(cs)
	// size:s, then the two unlabelled (= m) oldest-first, then size:l
	want := []int{2, 4, 3, 1}
	for i, n := range want {
		if got[i].Issue.Number != n {
			t.Fatalf("position %d: got #%d, want #%d (full order %v)", i, got[i].Issue.Number, n, numbers(got))
		}
	}
}

func TestOrderDoesNotMutateInput(t *testing.T) {
	cs := []Candidate{
		{Repo: "a", Issue: forge.Issue{Number: 1, Labels: []string{"chore"}}},
		{Repo: "b", Issue: forge.Issue{Number: 2, Labels: []string{"bug"}}},
	}
	Order(cs)
	if cs[0].Issue.Number != 1 {
		t.Errorf("input was mutated: %v", numbers(cs))
	}
}

func numbers(cs []Candidate) []int {
	out := make([]int, len(cs))
	for i, c := range cs {
		out[i] = c.Issue.Number
	}
	return out
}
