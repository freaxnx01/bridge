package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/spf13/cobra"

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
	got := repoNames([]dispatch.RepoInput{{Owner: "freaxnx01", Name: "bridge"}, {Owner: "freaxnx01", Name: "quotes"}})
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

// newJournalGithub serves the one repo the journal integration tests fetch:
// a single eligible issue #41, no milestones, no PRs, and a label/comment
// endpoint whose status each test picks. Anything else is a test bug.
func newJournalGithub(t *testing.T, labelStatus, commentStatus int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/repos/freaxnx01/bridge/issues":
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`[
				{"number":41,"title":"eligible issue","body":"a real description\n## Implementation Plan\n- step","html_url":"u41","labels":[{"name":"feat"}],"updated_at":"2026-07-01T00:00:00Z","created_at":"2026-06-01T00:00:00Z"}
			]`))
		case r.Method == "GET" && r.URL.Path == "/repos/freaxnx01/bridge/milestones":
			w.Write([]byte(`[]`))
		case r.Method == "GET" && r.URL.Path == "/repos/freaxnx01/bridge/pulls":
			w.Write([]byte(`[]`))
		case r.Method == "POST" && r.URL.Path == "/repos/freaxnx01/bridge/issues/41/labels":
			w.WriteHeader(labelStatus)
			if labelStatus == http.StatusOK {
				w.Write([]byte(`[{"name":"ai-implement"}]`))
			} else {
				w.Write([]byte(`{"message":"boom"}`))
			}
		case r.Method == "POST" && r.URL.Path == "/repos/freaxnx01/bridge/issues/41/comments":
			w.WriteHeader(commentStatus)
			w.Write([]byte(`{"id":1,"body":"x","created_at":"2026-07-01T00:00:00Z"}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
	}))
}

// journalTickEnv isolates one tick: its own cache (where the journal lands),
// config, transcripts, and the flags runDispatch reads as globals.
func journalTickEnv(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("BRIDGE_CLAUDE_PROJECTS", filepath.Join(t.TempDir(), "absent"))
	t.Cleanup(func() { dispatchAuto, dispatchRetryOnly = false, false })
}

// pointAtFakeGithub wires the one fake repo and its server into the tick.
func pointAtFakeGithub(t *testing.T, srv *httptest.Server) {
	t.Helper()
	t.Setenv("BRIDGE_REPOS_ROOT", writeFakeGithubRepo(t, "freaxnx01", "bridge"))
	t.Setenv("BRIDGE_GITHUB_API", srv.URL)
	t.Setenv("GH_TOKEN", "tok")
}

// onlyRecord asserts the tick wrote exactly one journal record and returns it.
func onlyRecord(t *testing.T) dispatch.JournalRecord {
	t.Helper()
	recs, err := dispatch.ReadJournal(dispatchJournalDir(), time.Time{})
	if err != nil {
		t.Fatalf("ReadJournal: %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("want exactly one record per invocation, got %d: %+v", len(recs), recs)
	}
	return recs[0]
}

func decisionFor(t *testing.T, rec dispatch.JournalRecord, issue int) dispatch.JournalDecision {
	t.Helper()
	for _, d := range rec.Decisions {
		if d.Issue == issue {
			return d
		}
	}
	t.Fatalf("no decision for #%d in %+v", issue, rec.Decisions)
	return dispatch.JournalDecision{}
}

func runJournalTick(t *testing.T) error {
	t.Helper()
	cmd := &cobra.Command{}
	cmd.SetOut(&bytes.Buffer{})
	return runDispatch(cmd, nil)
}

// The observation-week drop-in: the timer runs --auto --dry-run. That has to
// journal as a timer slot in dry-run mode, with a live-lane candidate recorded
// as would_dispatch — never as a dispatch that did not happen.
func TestRunDispatch_AutoDryRun_JournalsTimerDryRunWouldDispatch(t *testing.T) {
	srv := newJournalGithub(t, http.StatusOK, http.StatusCreated)
	defer srv.Close()
	journalTickEnv(t)
	pointAtFakeGithub(t, srv)
	pinUnattendedSchedule(t)
	dispatchAuto = true
	setDispatchFlags(t, false, true)

	if err := runJournalTick(t); err != nil {
		t.Fatalf("runDispatch: %v", err)
	}

	rec := onlyRecord(t)
	if rec.Trigger != dispatch.TriggerTimer || rec.Mode != dispatch.ModeDryRun {
		t.Errorf("trigger/mode = %q/%q, want timer/dry-run", rec.Trigger, rec.Mode)
	}
	if rec.Outcome != dispatch.OutcomeDecided {
		t.Errorf("outcome = %q, want decided", rec.Outcome)
	}
	if !equalStrings(rec.Repos, []string{"freaxnx01/bridge"}) {
		t.Errorf("repos = %v", rec.Repos)
	}
	if len(rec.LanesInWindow) == 0 || rec.LanesInWindow[0] != "default" {
		t.Errorf("lanes_in_window = %v, want the default lane", rec.LanesInWindow)
	}
	if d := decisionFor(t, rec, 41); d.Decision != dispatch.DecisionWouldDispatch || d.Kind != dispatch.KindDispatch {
		t.Errorf("#41 = %q/%q, want would_dispatch/dispatch", d.Decision, d.Kind)
	}
}

func TestRunDispatch_ManualLive_JournalsDispatched(t *testing.T) {
	srv := newJournalGithub(t, http.StatusOK, http.StatusCreated)
	defer srv.Close()
	journalTickEnv(t)
	pointAtFakeGithub(t, srv)
	pinUnattendedSchedule(t)
	setDispatchFlags(t, false, false)

	if err := runJournalTick(t); err != nil {
		t.Fatalf("runDispatch: %v", err)
	}

	rec := onlyRecord(t)
	if rec.Trigger != dispatch.TriggerManual || rec.Mode != dispatch.ModeLive {
		t.Errorf("trigger/mode = %q/%q, want manual/live", rec.Trigger, rec.Mode)
	}
	if rec.Outcome != dispatch.OutcomeDecided {
		t.Errorf("outcome = %q, want decided", rec.Outcome)
	}
	if d := decisionFor(t, rec, 41); d.Decision != dispatch.DecisionDispatched {
		t.Errorf("#41 = %q, want dispatched", d.Decision)
	}
}

// A label that never landed means the pipeline never fired, so the journal
// must not claim a dispatch for it.
func TestRunDispatch_LabelFails_JournalsNotAppliedAndError(t *testing.T) {
	srv := newJournalGithub(t, http.StatusInternalServerError, http.StatusCreated)
	defer srv.Close()
	journalTickEnv(t)
	pointAtFakeGithub(t, srv)
	pinUnattendedSchedule(t)
	setDispatchFlags(t, false, false)

	if err := runJournalTick(t); err == nil {
		t.Fatal("a failing label must surface as an error")
	}

	rec := onlyRecord(t)
	if rec.Outcome != dispatch.OutcomeError || rec.Error == "" {
		t.Errorf("outcome/error = %q/%q, want error with text", rec.Outcome, rec.Error)
	}
	if d := decisionFor(t, rec, 41); d.Decision != dispatch.DecisionNotApplied {
		t.Errorf("#41 = %q, want not_applied", d.Decision)
	}
}

// The label is the pipeline's trigger: once it lands the run is real and the
// ledger books it, so a failing follow-up comment still journals a dispatch.
func TestRunDispatch_CommentFailsAfterLabel_JournalsDispatched(t *testing.T) {
	srv := newJournalGithub(t, http.StatusOK, http.StatusInternalServerError)
	defer srv.Close()
	journalTickEnv(t)
	pointAtFakeGithub(t, srv)
	pinUnattendedSchedule(t)
	setDispatchFlags(t, false, false)

	if err := runJournalTick(t); err == nil {
		t.Fatal("a failing comment must surface as an error")
	}

	rec := onlyRecord(t)
	if rec.Outcome != dispatch.OutcomeError {
		t.Errorf("outcome = %q, want error", rec.Outcome)
	}
	if d := decisionFor(t, rec, 41); d.Decision != dispatch.DecisionDispatched {
		t.Errorf("#41 = %q, want dispatched (the label landed)", d.Decision)
	}
}

func TestRunDispatch_AutoPaused_JournalsPausedWithoutFetch(t *testing.T) {
	journalTickEnv(t)
	if err := dispatch.WriteState(dispatchStatePath(), dispatch.State{Paused: true}); err != nil {
		t.Fatal(err)
	}
	dispatchAuto = true
	setDispatchFlags(t, false, false)

	if err := runJournalTick(t); err != nil {
		t.Fatalf("a paused tick returns cleanly: %v", err)
	}

	rec := onlyRecord(t)
	if rec.Outcome != dispatch.OutcomePaused || rec.Trigger != dispatch.TriggerTimer {
		t.Errorf("outcome/trigger = %q/%q, want paused/timer", rec.Outcome, rec.Trigger)
	}
	if len(rec.Repos) != 0 {
		t.Errorf("a paused tick fetches nothing, got repos %v", rec.Repos)
	}
}

func TestRunDispatch_AutoOutsideWindow_JournalsOutsideWindow(t *testing.T) {
	journalTickEnv(t)
	// One minute wide, three hours ahead: a window that cannot contain now,
	// and cannot coincide with it on a fall-back DST day either.
	future := time.Now().Add(3 * time.Hour)
	body := `{"schedule":{"windows":[{"from":"` + future.Format("15:04") + `","to":"` +
		future.Add(time.Minute).Format("15:04") + `","budget_rung":false}]}}`
	writeDispatchConfig(t, body)
	dispatchAuto = true
	setDispatchFlags(t, false, false)

	if err := runJournalTick(t); err != nil {
		t.Fatalf("out-of-window tick must return cleanly: %v", err)
	}

	rec := onlyRecord(t)
	if rec.Outcome != dispatch.OutcomeOutsideWindow {
		t.Errorf("outcome = %q, want outside_window", rec.Outcome)
	}
	if len(rec.LanesInWindow) != 0 {
		t.Errorf("no lane acts, got %v", rec.LanesInWindow)
	}
}

func TestRunDispatch_ConfigUnreadable_JournalsError(t *testing.T) {
	journalTickEnv(t)
	writeDispatchConfig(t, `{not json`)
	setDispatchFlags(t, false, false)

	if err := runJournalTick(t); err == nil {
		t.Fatal("an unreadable config must surface as an error")
	}

	rec := onlyRecord(t)
	if rec.Outcome != dispatch.OutcomeError || rec.Error == "" {
		t.Errorf("outcome/error = %q/%q, want error with text", rec.Outcome, rec.Error)
	}
}

func TestRunDispatch_RetryOnlyDryRun_JournalsRetryOnly(t *testing.T) {
	srv := newJournalGithub(t, http.StatusOK, http.StatusCreated)
	defer srv.Close()
	journalTickEnv(t)
	pointAtFakeGithub(t, srv)
	pinUnattendedSchedule(t)
	dispatchRetryOnly = true
	setDispatchFlags(t, false, true)

	if err := runJournalTick(t); err != nil {
		t.Fatalf("runDispatch: %v", err)
	}

	rec := onlyRecord(t)
	if !rec.RetryOnly || rec.Mode != dispatch.ModeDryRun {
		t.Errorf("retry_only/mode = %t/%q, want true/dry-run", rec.RetryOnly, rec.Mode)
	}
	for _, d := range rec.Decisions {
		if d.Issue == 41 {
			t.Errorf("--retry-only skips new issues, got %+v", d)
		}
	}
}

// The journal is observability: an unwritable one must leave the tick's error,
// its stdout and its state exactly as they were.
func TestRunDispatch_JournalDirUnwritable_TickUnaffected(t *testing.T) {
	journalTickEnv(t)
	t.Setenv("BRIDGE_BASE", t.TempDir()) // exists but has no repos
	setDispatchFlags(t, true, false)     // --json, live

	if err := os.MkdirAll(cacheRoot(), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dispatchJournalDir(), []byte("not a dir"), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := &cobra.Command{}
	var out bytes.Buffer
	cmd.SetOut(&out)

	if err := runDispatch(cmd, nil); err != nil {
		t.Fatalf("a broken journal must not fail the tick: %v", err)
	}
	var decisions []dispatch.Decision
	if err := json.Unmarshal(out.Bytes(), &decisions); err != nil {
		t.Fatalf("stdout must be unchanged, got %q: %v", out.String(), err)
	}
}

// writeDispatchConfig puts body at the path runDispatch reads its config from.
func writeDispatchConfig(t *testing.T, body string) {
	t.Helper()
	dir := filepath.Dir(dispatchConfigPath())
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dispatchConfigPath(), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}
