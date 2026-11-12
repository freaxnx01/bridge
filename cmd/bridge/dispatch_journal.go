package main

import (
	"log/slog"
	"path/filepath"
	"strconv"
	"time"

	"github.com/freaxnx01/bridge/internal/dispatch"
	"github.com/freaxnx01/bridge/internal/usage"
)

// dispatchJournalDir is where every tick's record lands. It sits in the cache
// root beside the ledger and the state file, so a cache wipe takes the journal
// with it — it is observability, never a source of truth.
func dispatchJournalDir() string { return filepath.Join(cacheRoot(), "dispatch-journal") }

// tickTrigger reports what started this tick. --auto is the timer's entry
// point and nothing else sets it, so it alone answers the question — in
// particular an --auto --dry-run observation tick is still a timer slot.
func tickTrigger(auto bool) string {
	if auto {
		return dispatch.TriggerTimer
	}
	return dispatch.TriggerManual
}

// tickMode reports whether this tick could apply anything at all. It is
// independent of the trigger: the observation-week timer is timer + dry-run.
func tickMode(dryRun bool) string {
	if dryRun {
		return dispatch.ModeDryRun
	}
	return dispatch.ModeLive
}

// newTickRecord starts a record at the command's entry, before anything can
// fail. runTick overwrites At with the tick's own resolved now.
func newTickRecord(at time.Time, auto, dryRun, retryOnly bool) dispatch.JournalRecord {
	return dispatch.JournalRecord{
		V:         dispatch.JournalVersion,
		At:        at,
		Trigger:   tickTrigger(auto),
		Mode:      tickMode(dryRun),
		RetryOnly: retryOnly,
	}
}

// lanesInWindow names the lanes acting at now, the implicit default lane
// first. It reads the config alone — no network — so it is answerable on the
// early-exit paths that never fetch.
func lanesInWindow(cfg dispatch.Config, now time.Time) []string {
	var out []string
	if dispatch.DefaultLane().InWindow(cfg.Schedule, now) {
		out = append(out, dispatch.DefaultLane().Name)
	}
	for _, l := range cfg.Lanes {
		if l.InWindow(cfg.Schedule, now) {
			out = append(out, l.Name)
		}
	}
	return out
}

// repoNames renders the fetched repos as owner/name, in fetch order.
func repoNames(repos []repoInput) []string {
	out := make([]string, 0, len(repos))
	for _, r := range repos {
		out = append(out, r.Owner+"/"+r.Name)
	}
	return out
}

// journalBudget records the rung's reading. Unknown is inverted into Known so
// a reader never has to spot a negative to see that the number is unusable.
func journalBudget(b dispatch.BudgetState) *dispatch.JournalBudget {
	return &dispatch.JournalBudget{UsedUSD: b.UsedUSD, LimitUSD: b.LimitUSD, Known: !b.Unknown}
}

// journalDecisions renders the tick's decisions. A live decision starts as
// not_applied and only markBooked promotes it: "dispatched" has to mean the
// run was booked, never merely that the tick intended it.
func journalDecisions(ds []dispatch.Decision, tickDryRun bool) []dispatch.JournalDecision {
	out := make([]dispatch.JournalDecision, 0, len(ds))
	for _, d := range ds {
		c := d.Candidate
		out = append(out, dispatch.JournalDecision{
			Owner:       c.Owner,
			Repo:        c.Repo,
			Issue:       c.Issue.Number,
			Title:       c.Issue.Title,
			Kind:        journalKind(c),
			RetryReason: c.RetryReason,
			Lane:        c.Lane.Name,
			LaneReason:  c.LaneReason,
			Decision:    journalDecisionStatus(d, tickDryRun),
			Reason:      d.Reason,
		})
	}
	return out
}

// journalKind names the pass a candidate came from, in the journal's own
// vocabulary. decisionKind answers the same question for --json output; the
// two are deliberately separate so the stdout contract and the journal
// contract can move apart.
func journalKind(c dispatch.Candidate) string {
	if c.Retry {
		return dispatch.KindRetry
	}
	return dispatch.KindDispatch
}

// journalDecisionStatus classifies one decision as the journal records it. A
// dry-run tick and a dry-run lane are the same thing here — nothing is
// applied — even though the console prints a plain "dispatch" for the former.
func journalDecisionStatus(d dispatch.Decision, tickDryRun bool) string {
	switch {
	case !d.Dispatch:
		return dispatch.DecisionSkipped
	case tickDryRun || d.Candidate.Lane.DryRun:
		return dispatch.DecisionWouldDispatch
	default:
		return dispatch.DecisionNotApplied
	}
}

// markBooked promotes each decision the ledger actually booked. Anything left
// at not_applied is a live dispatch the tick meant to make and did not — the
// tick's own error says why.
func markBooked(js []dispatch.JournalDecision, runs []usage.Run) {
	booked := make(map[string]bool, len(runs))
	for _, r := range runs {
		booked[runKey(r.Repo, r.Issue)] = true
	}
	for i := range js {
		if js[i].Decision == dispatch.DecisionNotApplied && booked[runKey(js[i].Repo, js[i].Issue)] {
			js[i].Decision = dispatch.DecisionDispatched
		}
	}
}

func runKey(repo string, issue int) string {
	return repo + "#" + strconv.Itoa(issue)
}

// finishRecord resolves the tick's outcome. A preset paused/outside_window
// stands: those are answers, not failures, and the digest reads them as
// deliberate quiet rather than as a gap.
func finishRecord(rec *dispatch.JournalRecord, tickErr error) {
	if tickErr != nil {
		rec.Error = tickErr.Error()
		if rec.Outcome == "" {
			rec.Outcome = dispatch.OutcomeError
		}
		return
	}
	if rec.Outcome != "" {
		return
	}
	if len(rec.Decisions) == 0 {
		rec.Outcome = dispatch.OutcomeNoCandidates
		return
	}
	rec.Outcome = dispatch.OutcomeDecided
}

// recordTick finishes rec and appends it. It returns nothing on purpose: the
// journal is observability, and a full disk must not stop the dispatcher.
func recordTick(dir string, rec dispatch.JournalRecord, tickErr error) {
	finishRecord(&rec, tickErr)
	if err := dispatch.AppendJournal(dir, rec); err != nil {
		slog.Warn("dispatch: cannot write the tick journal — the tick itself is unaffected", "error", err)
	}
}
