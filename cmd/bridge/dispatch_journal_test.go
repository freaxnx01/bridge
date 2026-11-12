package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/freaxnx01/bridge/internal/dispatch"
	"github.com/freaxnx01/bridge/internal/forge"
	"github.com/freaxnx01/bridge/internal/usage"
)

func TestTickTriggerMode_Table(t *testing.T) {
	tests := []struct {
		name              string
		auto, dryRun      bool
		trigger, wantMode string
	}{
		// The observation-week drop-in: the timer runs --auto --dry-run, and
		// that must still read as a timer slot, not as a manual tick.
		{"auto dry-run", true, true, dispatch.TriggerTimer, dispatch.ModeDryRun},
		{"auto live", true, false, dispatch.TriggerTimer, dispatch.ModeLive},
		{"manual dry-run", false, true, dispatch.TriggerManual, dispatch.ModeDryRun},
		{"manual live", false, false, dispatch.TriggerManual, dispatch.ModeLive},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tickTrigger(tc.auto); got != tc.trigger {
				t.Errorf("tickTrigger(%t) = %q, want %q", tc.auto, got, tc.trigger)
			}
			if got := tickMode(tc.dryRun); got != tc.wantMode {
				t.Errorf("tickMode(%t) = %q, want %q", tc.dryRun, got, tc.wantMode)
			}
		})
	}
}

func TestLanesInWindow_ListsOnlyActingLanes(t *testing.T) {
	lanes := []dispatch.Lane{
		{Name: "auto", Repos: []string{"*"}, Windows: []dispatch.Span{{From: "00:00", To: "00:00"}}},
		{Name: "night", Repos: []string{"*"}, Windows: []dispatch.Span{{From: "22:00", To: "23:00"}}},
	}
	now := time.Date(2026, 11, 11, 13, 0, 0, 0, time.Local)

	allDay := dispatch.Config{
		Schedule: dispatch.Schedule{Windows: []dispatch.Window{{Span: dispatch.Span{From: "00:00", To: "00:00"}}}},
		Lanes:    lanes,
	}
	if got := lanesInWindow(allDay, now); !equalStrings(got, []string{"default", "auto"}) {
		t.Errorf("all-day schedule: got %v, want [default auto]", got)
	}

	narrow := dispatch.Config{
		Schedule: dispatch.Schedule{Windows: []dispatch.Window{{Span: dispatch.Span{From: "03:00", To: "03:01"}}}},
		Lanes:    lanes,
	}
	if got := lanesInWindow(narrow, now); !equalStrings(got, []string{"auto"}) {
		t.Errorf("narrow schedule: got %v, want [auto]", got)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestJournalDecisions_Classification(t *testing.T) {
	base := func() dispatch.Candidate {
		return dispatch.Candidate{
			Owner: "freaxnx01", Repo: "bridge",
			Issue: forge.Issue{Number: 41, Title: "t"},
			Lane:  dispatch.Lane{Name: "default"},
		}
	}

	tests := []struct {
		name            string
		candidate       func() dispatch.Candidate
		dispatchIt      bool
		reason          string
		tickDryRun      bool
		want            string
		wantKind        string
		wantRetryReason string
		wantLaneReason  string
	}{
		{
			name: "skipped by a cap", candidate: base, dispatchIt: false,
			reason: "global cap 3/3", want: dispatch.DecisionSkipped, wantKind: dispatch.KindDispatch,
		},
		{
			name: "tick dry-run over a live lane", candidate: base, dispatchIt: true,
			tickDryRun: true, want: dispatch.DecisionWouldDispatch, wantKind: dispatch.KindDispatch,
		},
		{
			name: "live tick over a dry-run lane",
			candidate: func() dispatch.Candidate {
				c := base()
				c.Lane.DryRun = true
				return c
			},
			dispatchIt: true, want: dispatch.DecisionWouldDispatch, wantKind: dispatch.KindDispatch,
		},
		{
			name: "live tick, live lane, not yet booked", candidate: base, dispatchIt: true,
			want: dispatch.DecisionNotApplied, wantKind: dispatch.KindDispatch,
		},
		{
			name: "lane downgrade is carried",
			candidate: func() dispatch.Candidate {
				c := base()
				c.LaneReason = "auto→hitl: x"
				return c
			},
			dispatchIt: true, tickDryRun: true,
			want: dispatch.DecisionWouldDispatch, wantKind: dispatch.KindDispatch, wantLaneReason: "auto→hitl: x",
		},
		{
			name: "retry candidate",
			candidate: func() dispatch.Candidate {
				c := base()
				c.Retry, c.RetryReason = true, "rate_limit, retries exhausted"
				return c
			},
			dispatchIt: true, tickDryRun: true,
			want: dispatch.DecisionWouldDispatch, wantKind: dispatch.KindRetry,
			wantRetryReason: "rate_limit, retries exhausted",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ds := []dispatch.Decision{{Candidate: tc.candidate(), Dispatch: tc.dispatchIt, Reason: tc.reason}}

			got := journalDecisions(ds, tc.tickDryRun)

			if len(got) != 1 {
				t.Fatalf("got %d decisions, want 1", len(got))
			}
			j := got[0]
			if j.Decision != tc.want {
				t.Errorf("decision = %q, want %q", j.Decision, tc.want)
			}
			if j.Kind != tc.wantKind {
				t.Errorf("kind = %q, want %q", j.Kind, tc.wantKind)
			}
			if j.RetryReason != tc.wantRetryReason {
				t.Errorf("retry_reason = %q, want %q", j.RetryReason, tc.wantRetryReason)
			}
			if j.LaneReason != tc.wantLaneReason {
				t.Errorf("lane_reason = %q, want %q", j.LaneReason, tc.wantLaneReason)
			}
			if j.Reason != tc.reason {
				t.Errorf("reason = %q, want %q", j.Reason, tc.reason)
			}
			if j.Owner != "freaxnx01" || j.Repo != "bridge" || j.Issue != 41 || j.Title != "t" || j.Lane != "default" {
				t.Errorf("candidate fields not carried: %+v", j)
			}
		})
	}
}

func TestMarkBooked_FlipsOnlyBookedLive(t *testing.T) {
	js := []dispatch.JournalDecision{
		{Repo: "bridge", Issue: 41, Decision: dispatch.DecisionNotApplied},
		{Repo: "bridge", Issue: 42, Decision: dispatch.DecisionNotApplied},
		{Repo: "bridge", Issue: 43, Decision: dispatch.DecisionWouldDispatch},
	}

	markBooked(js, []usage.Run{{Repo: "bridge", Issue: 41}, {Repo: "bridge", Issue: 43}})

	want := []string{dispatch.DecisionDispatched, dispatch.DecisionNotApplied, dispatch.DecisionWouldDispatch}
	for i, w := range want {
		if js[i].Decision != w {
			t.Errorf("#%d: decision = %q, want %q", js[i].Issue, js[i].Decision, w)
		}
	}
}

func TestFinishRecord_Outcome(t *testing.T) {
	oneDecision := []dispatch.JournalDecision{{Repo: "bridge", Issue: 41}}

	tests := []struct {
		name      string
		preset    string
		decisions []dispatch.JournalDecision
		tickErr   error
		want      string
		wantErr   string
	}{
		{name: "nothing decided", want: dispatch.OutcomeNoCandidates},
		{name: "decided", decisions: oneDecision, want: dispatch.OutcomeDecided},
		{
			name: "error keeps the decisions", decisions: oneDecision, tickErr: errors.New("boom"),
			want: dispatch.OutcomeError, wantErr: "boom",
		},
		{name: "paused is kept", preset: dispatch.OutcomePaused, want: dispatch.OutcomePaused},
		{name: "outside window is kept", preset: dispatch.OutcomeOutsideWindow, want: dispatch.OutcomeOutsideWindow},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := dispatch.JournalRecord{Outcome: tc.preset, Decisions: tc.decisions}

			finishRecord(&rec, tc.tickErr)

			if rec.Outcome != tc.want {
				t.Errorf("outcome = %q, want %q", rec.Outcome, tc.want)
			}
			if rec.Error != tc.wantErr {
				t.Errorf("error = %q, want %q", rec.Error, tc.wantErr)
			}
			if len(rec.Decisions) != len(tc.decisions) {
				t.Errorf("decisions dropped: %+v", rec.Decisions)
			}
		})
	}
}

// A journal that cannot be written must not take the tick down with it, so
// recordTick has no error to return in the first place.
func TestRecordTick_UnwritableDir_NoPanicNoReturn(t *testing.T) {
	blocked := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(blocked, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	recordTick(blocked, newTickRecord(time.Now(), true, true, false), nil)

	got, err := dispatch.ReadJournal(blocked, time.Time{})
	if err == nil && len(got) != 0 {
		t.Errorf("a file is not a journal dir, got %+v", got)
	}
}

func TestNewTickRecord_CarriesTriggerModeRetryOnly(t *testing.T) {
	at := time.Date(2026, 11, 11, 2, 0, 0, 0, time.UTC)

	rec := newTickRecord(at, true, true, true)

	if !rec.At.Equal(at) || rec.Trigger != dispatch.TriggerTimer || rec.Mode != dispatch.ModeDryRun || !rec.RetryOnly {
		t.Errorf("got %+v", rec)
	}
}

func TestDispatchJournalDir_UnderTheCacheRoot(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)

	if got, want := dispatchJournalDir(), filepath.Join(cache, "bridge", "dispatch-journal"); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestRepoNames_OwnerSlashName(t *testing.T) {
	got := repoNames([]repoInput{{Owner: "freaxnx01", Name: "bridge"}, {Owner: "freaxnx01", Name: "quotes"}})
	if !equalStrings(got, []string{"freaxnx01/bridge", "freaxnx01/quotes"}) {
		t.Errorf("got %v", got)
	}
}

func TestJournalBudget_CarriesReadingAndKnown(t *testing.T) {
	got := journalBudget(dispatch.BudgetState{UsedUSD: 4.5, LimitUSD: 9.6, Unknown: true})
	if got == nil || got.UsedUSD != 4.5 || got.LimitUSD != 9.6 || got.Known {
		t.Errorf("got %+v", got)
	}
	if known := journalBudget(dispatch.BudgetState{}); !known.Known {
		t.Errorf("a measured reading is known, got %+v", known)
	}
}
