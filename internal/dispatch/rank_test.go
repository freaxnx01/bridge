package dispatch

import (
	"github.com/freaxnx01/bridge/internal/forge"
	"testing"
)

func TestExplain_AdjacentCandidates_NamesFirstDifferingRung(t *testing.T) {
	priority := []string{"agent-workflow", "*"}

	tests := []struct {
		name string
		a, b Candidate
		want string
	}{
		{
			name: "repo priority",
			a:    Candidate{Repo: "agent-workflow", Issue: forge.Issue{Number: 1}},
			b:    Candidate{Repo: "bridge", Issue: forge.Issue{Number: 2}},
			want: "repo priority: agent-workflow vs *",
		},
		{
			name: "milestone due",
			a:    Candidate{Repo: "bridge", MilestoneDue: day("2026-11-20"), Issue: forge.Issue{Number: 1}},
			b:    Candidate{Repo: "bridge", MilestoneDue: day("2026-12-01"), Issue: forge.Issue{Number: 2}},
			want: "milestone due: 2026-11-20 vs 2026-12-01",
		},
		{
			name: "an undated milestone sorts last and renders as none",
			a:    Candidate{Repo: "bridge", MilestoneDue: day("2026-11-20"), Issue: forge.Issue{Number: 1}},
			b:    Candidate{Repo: "bridge", Issue: forge.Issue{Number: 2}},
			want: "milestone due: 2026-11-20 vs none",
		},
		{
			name: "type",
			a:    Candidate{Repo: "bridge", Issue: forge.Issue{Number: 1, Labels: []string{"bug"}}},
			b:    Candidate{Repo: "bridge", Issue: forge.Issue{Number: 2, Labels: []string{"enhancement"}}},
			want: "type: bug vs feat",
		},
		{
			name: "size",
			a:    Candidate{Repo: "bridge", Issue: forge.Issue{Number: 1, Labels: []string{"size:s"}}},
			b:    Candidate{Repo: "bridge", Issue: forge.Issue{Number: 2}},
			want: "size: s vs m",
		},
		{
			name: "age",
			a:    Candidate{Repo: "bridge", Issue: forge.Issue{Number: 1, Created: day("2026-10-01")}},
			b:    Candidate{Repo: "bridge", Issue: forge.Issue{Number: 2, Created: day("2026-10-05")}},
			want: "age: 2026-10-01 vs 2026-10-05",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Explain([]Candidate{tc.a, tc.b}, priority)
			if len(got) != 2 {
				t.Fatalf("got %d ranks", len(got))
			}
			if got[0].Reason != tc.want {
				t.Errorf("Reason = %q, want %q", got[0].Reason, tc.want)
			}
			if got[0].Position != 1 || got[1].Position != 2 {
				t.Errorf("positions: %d, %d", got[0].Position, got[1].Position)
			}
		})
	}
}

func TestExplain_LastCandidate_HasNoReason(t *testing.T) {
	got := Explain([]Candidate{{Repo: "bridge", Issue: forge.Issue{Number: 1}}}, nil)

	if len(got) != 1 {
		t.Fatalf("got %d ranks", len(got))
	}
	if got[0].Position != 1 || got[0].Reason != "" {
		t.Errorf("got %+v", got[0])
	}
}

// Two candidates that tie on every rung have nothing to name: the order
// between them is the stable sort's, not a decision the ladder made.
func TestExplain_TiedCandidates_HaveNoReason(t *testing.T) {
	c := Candidate{Repo: "bridge", Issue: forge.Issue{Number: 1, Created: day("2026-10-01")}}

	got := Explain([]Candidate{c, c}, nil)

	if got[0].Reason != "" {
		t.Errorf("Reason = %q, want empty", got[0].Reason)
	}
}

func TestExplain_Ladder_FilledFromCandidate(t *testing.T) {
	c := Candidate{
		Repo:         "bridge",
		Milestone:    "M1",
		MilestoneDue: day("2026-11-20"),
		Issue: forge.Issue{
			Number: 7, Labels: []string{"bug", "size:l"}, Created: day("2026-10-01"),
		},
	}

	got := Explain([]Candidate{c}, []string{"bridge"})[0].Ladder

	want := Ladder{
		RepoPattern: "bridge", Milestone: "M1", MilestoneDue: day("2026-11-20"),
		Type: "bug", Size: "l", Created: day("2026-10-01"),
	}
	if got != want {
		t.Errorf("got %+v want %+v", got, want)
	}
}

func TestExplain_RepoMatchingNoPattern_HasNoRepoPattern(t *testing.T) {
	c := Candidate{Repo: "bridge", Issue: forge.Issue{Number: 1}}

	got := Explain([]Candidate{c}, []string{"agent-workflow"})[0].Ladder

	if got.RepoPattern != "" {
		t.Errorf("RepoPattern = %q, want empty", got.RepoPattern)
	}
}

// Explain must never disagree with Order about which rung decided: both read
// the same rank functions, so an ordered pair's reason names a real rung.
func TestExplain_OrderedInput_ReasonMatchesTheLadderOrder(t *testing.T) {
	priority := []string{"agent-workflow", "*"}
	cs := []Candidate{
		{Repo: "bridge", Issue: forge.Issue{Number: 2, Labels: []string{"enhancement"}, Created: day("2026-10-05")}},
		{Repo: "agent-workflow", Issue: forge.Issue{Number: 1, Labels: []string{"bug"}, Created: day("2026-10-01")}},
	}

	ordered := Order(cs, priority)
	got := Explain(ordered, priority)

	if ordered[0].Repo != "agent-workflow" {
		t.Fatalf("Order put %s first", ordered[0].Repo)
	}
	if got[0].Reason != "repo priority: agent-workflow vs *" {
		t.Errorf("Reason = %q", got[0].Reason)
	}
}

func TestCollectCandidates_ActiveMilestone_LandsOnTheCandidate(t *testing.T) {
	repos := []RepoInput{{
		Forge: "github", Owner: "o", Name: "quotes",
		Milestones: []forge.Milestone{{Title: "M1", DueOn: day("2026-11-20"), State: "open"}},
		Issues: []forge.Issue{{
			Number: 41, Body: "a real description\n## Implementation Plan\n- step",
			Labels: []string{"feat"}, Milestone: "M1",
		}},
	}}

	got := CollectCandidates(repos)

	if len(got) != 1 {
		t.Fatalf("got %d candidates", len(got))
	}
	if got[0].Milestone != "M1" || !got[0].MilestoneDue.Equal(day("2026-11-20")) {
		t.Errorf("got %+v", got[0])
	}
}
