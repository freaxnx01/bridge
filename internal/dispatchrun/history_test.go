package dispatchrun

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/freaxnx01/bridge/internal/dispatch"
)

func histAt(h int) time.Time { return time.Date(2026, 11, 12, h, 0, 0, 0, time.UTC) }

// tick builds one journal record with the decisions given.
func tick(at time.Time, trigger string, ds ...dispatch.JournalDecision) dispatch.JournalRecord {
	return dispatch.JournalRecord{
		V: dispatch.JournalVersion, At: at, Trigger: trigger, Mode: dispatch.ModeDryRun,
		Outcome: dispatch.OutcomeDecided, Decisions: ds,
	}
}

func picked(issue int, rankReason string) dispatch.JournalDecision {
	return dispatch.JournalDecision{
		Repo: "bridge", Issue: issue, Title: "t", Decision: dispatch.DecisionWouldDispatch,
		Rank: 1, RankReason: rankReason,
	}
}

func skipped(issue int, reason string) dispatch.JournalDecision {
	return dispatch.JournalDecision{
		Repo: "bridge", Issue: issue, Title: "t", Decision: dispatch.DecisionSkipped, Reason: reason,
	}
}

// The per-issue summary answers "what did the timer do with this issue over
// the week?". A hand-run `dispatch now` is the operator's own tick and would
// double-count the night it was run.
func TestSummarize_CountsTimerTicksOnly(t *testing.T) {
	got := Summarize([]dispatch.JournalRecord{
		tick(histAt(1), dispatch.TriggerTimer, picked(1, "type: bug vs feat")),
		tick(histAt(2), dispatch.TriggerManual, picked(1, "type: bug vs feat")),
	})

	if len(got.Summary) != 1 {
		t.Fatalf("summary = %+v", got.Summary)
	}
	if got.Summary[0].PickedTicks != 1 || got.Summary[0].EvaluatedTicks != 1 {
		t.Errorf("got %+v", got.Summary[0])
	}
	if len(got.Ticks) != 2 {
		t.Errorf("every tick gets a header, including manual ones: %+v", got.Ticks)
	}
}

// A dry-run lane's "would dispatch" is the observation week's whole point: it
// is a pick the live lane would have made.
func TestSummarize_WouldDispatch_CountsAsPicked(t *testing.T) {
	got := Summarize([]dispatch.JournalRecord{
		tick(histAt(1), dispatch.TriggerTimer,
			dispatch.JournalDecision{Repo: "bridge", Issue: 1, Decision: dispatch.DecisionWouldDispatch}),
		tick(histAt(2), dispatch.TriggerTimer,
			dispatch.JournalDecision{Repo: "bridge", Issue: 1, Decision: dispatch.DecisionDispatched}),
	})

	if got.Summary[0].PickedTicks != 2 {
		t.Errorf("got %+v", got.Summary[0])
	}
}

func TestSummarize_TopReason_PrefersRankReasonWhenPicked(t *testing.T) {
	got := Summarize([]dispatch.JournalRecord{
		tick(histAt(1), dispatch.TriggerTimer, picked(1, "type: bug vs feat")),
		tick(histAt(2), dispatch.TriggerTimer, picked(1, "type: bug vs feat")),
		tick(histAt(3), dispatch.TriggerTimer, skipped(1, "repo at WIP 1/1")),
	})

	if got.Summary[0].TopReason != "type: bug vs feat" {
		t.Errorf("TopReason = %q", got.Summary[0].TopReason)
	}
}

func TestSummarize_TopReason_FallsBackToSkipReason(t *testing.T) {
	got := Summarize([]dispatch.JournalRecord{
		tick(histAt(1), dispatch.TriggerTimer, skipped(1, "budget-exhausted 418.27/9.60 USD")),
		tick(histAt(2), dispatch.TriggerTimer, skipped(1, "budget-exhausted 418.27/9.60 USD")),
		tick(histAt(3), dispatch.TriggerTimer, skipped(1, "global cap 3/3")),
	})

	if got.Summary[0].TopReason != "budget-exhausted 418.27/9.60 USD" {
		t.Errorf("TopReason = %q", got.Summary[0].TopReason)
	}
}

// Ties go to the most recent tick: "what is it doing now" beats "what did it
// do on Monday" when both happened equally often.
func TestSummarize_TopReason_TiesGoToTheMostRecentTick(t *testing.T) {
	got := Summarize([]dispatch.JournalRecord{
		tick(histAt(1), dispatch.TriggerTimer, skipped(1, "global cap 3/3")),
		tick(histAt(2), dispatch.TriggerTimer, skipped(1, "night cap 5/5")),
	})

	if got.Summary[0].TopReason != "night cap 5/5" {
		t.Errorf("TopReason = %q", got.Summary[0].TopReason)
	}
}

func TestSummarize_Empty_ReturnsEmptySlicesNotNil(t *testing.T) {
	raw, err := json.Marshal(Summarize(nil))
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `{"summary":[],"ticks":[]}` {
		t.Errorf("got %s", raw)
	}
}

func TestSummarize_SummaryOrder_PickedThenEvaluatedThenRepoAndIssue(t *testing.T) {
	got := Summarize([]dispatch.JournalRecord{
		tick(histAt(1), dispatch.TriggerTimer,
			skipped(9, "global cap 3/3"),
			picked(2, ""),
			dispatch.JournalDecision{Repo: "agent-workflow", Issue: 9,
				Decision: dispatch.DecisionSkipped, Reason: "global cap 3/3"},
		),
		tick(histAt(2), dispatch.TriggerTimer, skipped(9, "global cap 3/3")),
	})

	want := []struct {
		repo  string
		issue int
	}{
		{"bridge", 2},         // picked once
		{"bridge", 9},         // never picked, evaluated twice
		{"agent-workflow", 9}, // never picked, evaluated once
	}
	if len(got.Summary) != len(want) {
		t.Fatalf("summary = %+v", got.Summary)
	}
	for i, w := range want {
		if got.Summary[i].Repo != w.repo || got.Summary[i].Issue != w.issue {
			t.Errorf("row %d = %s#%d, want %s#%d",
				i, got.Summary[i].Repo, got.Summary[i].Issue, w.repo, w.issue)
		}
	}
}

func TestSummarize_TickHeader_CountsAndCandidates(t *testing.T) {
	got := Summarize([]dispatch.JournalRecord{
		tick(histAt(1), dispatch.TriggerTimer, picked(1, ""), skipped(2, "repo at WIP 1/1")),
	})

	if len(got.Ticks) != 1 {
		t.Fatalf("ticks = %+v", got.Ticks)
	}
	h := got.Ticks[0]
	if h.PickedCount != 1 || h.SkippedCount != 1 {
		t.Errorf("counts: %+v", h)
	}
	if h.Mode != dispatch.ModeDryRun || h.Trigger != dispatch.TriggerTimer {
		t.Errorf("header: %+v", h)
	}
	want := []TickCandidate{
		{Repo: "bridge", Issue: 1, Dispatch: true},
		{Repo: "bridge", Issue: 2, Dispatch: false},
	}
	if len(h.Candidates) != 2 || h.Candidates[0] != want[0] || h.Candidates[1] != want[1] {
		t.Errorf("candidates = %+v, want %+v", h.Candidates, want)
	}
}

// A paused or out-of-window tick decides nothing, and the timeline has to say
// so rather than leave a gap the operator has to explain.
func TestSummarize_TickWithoutDecisions_StillGetsAHeader(t *testing.T) {
	got := Summarize([]dispatch.JournalRecord{{
		V: dispatch.JournalVersion, At: histAt(1), Trigger: dispatch.TriggerTimer,
		Mode: dispatch.ModeDryRun, Outcome: dispatch.OutcomePaused,
	}})

	if len(got.Ticks) != 1 || got.Ticks[0].Outcome != dispatch.OutcomePaused {
		t.Fatalf("ticks = %+v", got.Ticks)
	}
	if got.Ticks[0].Candidates == nil {
		t.Error("candidates must serialize as [], not null")
	}
	if len(got.Summary) != 0 {
		t.Errorf("a tick with no decisions contributes no summary rows: %+v", got.Summary)
	}
}
