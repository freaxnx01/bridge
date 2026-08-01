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
	got := Order(cs, nil)
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
	got := Order(cs, nil)
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
	Order(cs, nil)
	if cs[0].Issue.Number != 1 {
		t.Errorf("input was mutated: %v", numbers(cs))
	}
}

func TestRepoPriorityRank(t *testing.T) {
	tests := []struct {
		name     string
		repo     string
		patterns []string
		want     int
	}{
		{"literal match", "agent-workflow", []string{"agent-workflow", "game-*"}, 0},
		{"second literal match", "ai-instructions", []string{"agent-workflow", "ai-instructions"}, 1},
		{"glob match", "game-tetris", []string{"agent-workflow", "game-*", "*"}, 1},
		{"first match wins over later overlapping pattern", "game-tetris", []string{"*", "game-*"}, 0},
		{"no match falls after every entry", "bridge", []string{"agent-workflow", "game-*"}, 2},
		{"empty pattern list ranks every repo 0", "anything", nil, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := repoPriorityRank(tt.repo, tt.patterns)
			if got != tt.want {
				t.Errorf("repoPriorityRank(%q, %v) = %d, want %d", tt.repo, tt.patterns, got, tt.want)
			}
		})
	}
}

func TestOrderRepoPriorityDominatesMilestone(t *testing.T) {
	cs := []Candidate{
		// Earliest due date, but the lower-priority repo.
		{Repo: "game-tetris", Issue: forge.Issue{Number: 1, Labels: []string{"bug"}, Created: day("2026-01-01")},
			MilestoneDue: day("2026-02-01")},
		{Repo: "agent-workflow", Issue: forge.Issue{Number: 2, Labels: []string{"chore"}, Created: day("2026-06-01")}},
	}
	got := Order(cs, []string{"agent-workflow", "*"})
	if got[0].Issue.Number != 2 {
		t.Fatalf("position 0: got #%d, want #2 (repo priority must dominate milestone due date, full order %v)",
			got[0].Issue.Number, numbers(got))
	}
}

func TestOrderRepoPriorityEmptyIsNoOp(t *testing.T) {
	cs := []Candidate{
		{Repo: "a", Issue: forge.Issue{Number: 1, Labels: []string{"chore"}, Created: day("2026-01-01")}},
		{Repo: "b", Issue: forge.Issue{Number: 2, Labels: []string{"bug"}, Created: day("2026-05-01")}},
	}
	for _, patterns := range [][]string{nil, {}} {
		got := Order(cs, patterns)
		if got[0].Issue.Number != 2 {
			t.Fatalf("repoPriority %v must behave like the pre-existing 4-rung ladder: got %v", patterns, numbers(got))
		}
	}
}

func numbers(cs []Candidate) []int {
	out := make([]int, len(cs))
	for i, c := range cs {
		out[i] = c.Issue.Number
	}
	return out
}
