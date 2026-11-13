package dispatchrun

import (
	"cmp"
	"slices"
	"time"

	"github.com/freaxnx01/bridge/internal/dispatch"
)

// IssueSummary is one issue's week: how many timer ticks looked at it, how
// many would have picked it, and the reason that came up most often.
type IssueSummary struct {
	Repo           string `json:"repo"`
	Issue          int    `json:"issue"`
	Title          string `json:"title"`
	EvaluatedTicks int    `json:"evaluatedTicks"`
	PickedTicks    int    `json:"pickedTicks"`
	TopReason      string `json:"topReason"`
}

// TickCandidate is one row of a tick's timeline chip: enough to filter the
// timeline by issue without loading every tick's detail.
type TickCandidate struct {
	Repo     string `json:"repo"`
	Issue    int    `json:"issue"`
	Dispatch bool   `json:"dispatch"`
}

// TickHeader is one tick as the timeline shows it.
type TickHeader struct {
	At           time.Time       `json:"at"`
	Mode         string          `json:"mode"`
	Trigger      string          `json:"trigger"`
	Outcome      string          `json:"outcome"`
	PickedCount  int             `json:"pickedCount"`
	SkippedCount int             `json:"skippedCount"`
	Candidates   []TickCandidate `json:"candidates"`
}

// History is the Dispatcher area's payload: the per-issue summary and the
// tick timeline it drills into.
type History struct {
	Summary []IssueSummary `json:"summary"`
	Ticks   []TickHeader   `json:"ticks"`
}

// Summarize folds a run of journal records into the history view. Only timer
// ticks feed the per-issue summary — a hand-run `dispatch now` is the
// operator's own tick and would double-count the night it was run — while the
// timeline shows every tick, decisions or not.
func Summarize(ticks []dispatch.JournalRecord) History {
	acc := map[issueRef]*issueAccumulator{}
	headers := make([]TickHeader, 0, len(ticks))

	for _, rec := range ticks {
		headers = append(headers, headerOf(rec))
		if rec.Trigger != dispatch.TriggerTimer {
			continue
		}
		for _, d := range rec.Decisions {
			ref := issueRef{d.Repo, d.Issue}
			a, ok := acc[ref]
			if !ok {
				a = &issueAccumulator{reasons: map[string]reasonCount{}}
				acc[ref] = a
			}
			a.add(rec.At, d)
		}
	}
	return History{Summary: summaryRows(acc), Ticks: headers}
}

type issueRef struct {
	repo  string
	issue int
}

// reasonCount is how often a reason was seen and when it was last seen, which
// is what breaks a tie towards the present.
type reasonCount struct {
	count int
	last  time.Time
}

// issueAccumulator folds one issue's decisions. Picked and skipped reasons are
// kept apart: once an issue has ever been picked, why it got picked is the
// interesting answer, and the skips are noise around it.
type issueAccumulator struct {
	title     string
	evaluated int
	picked    bool
	pickCount int
	reasons   map[string]reasonCount
}

func (a *issueAccumulator) add(at time.Time, d dispatch.JournalDecision) {
	a.evaluated++
	if d.Title != "" {
		a.title = d.Title
	}
	pick := isPick(d.Decision)
	if pick {
		a.pickCount++
		if !a.picked {
			// The first pick invalidates every skip reason collected so far.
			a.picked, a.reasons = true, map[string]reasonCount{}
		}
	}
	if pick != a.picked {
		return
	}
	reason := d.Reason
	if pick {
		reason = d.RankReason
	}
	if reason == "" {
		return
	}
	c := a.reasons[reason]
	a.reasons[reason] = reasonCount{count: c.count + 1, last: laterOf(c.last, at)}
}

// topReason is the most frequent reason, the most recent one winning a tie.
func (a *issueAccumulator) topReason() string {
	best, bestCount, bestAt := "", 0, time.Time{}
	for reason, c := range a.reasons {
		if c.count > bestCount || (c.count == bestCount && c.last.After(bestAt)) {
			best, bestCount, bestAt = reason, c.count, c.last
		}
	}
	return best
}

func summaryRows(acc map[issueRef]*issueAccumulator) []IssueSummary {
	out := make([]IssueSummary, 0, len(acc))
	for ref, a := range acc {
		out = append(out, IssueSummary{
			Repo: ref.repo, Issue: ref.issue, Title: a.title,
			EvaluatedTicks: a.evaluated, PickedTicks: a.pickCount, TopReason: a.topReason(),
		})
	}
	slices.SortFunc(out, func(x, y IssueSummary) int {
		return cmp.Or(
			cmp.Compare(y.PickedTicks, x.PickedTicks),
			cmp.Compare(y.EvaluatedTicks, x.EvaluatedTicks),
			cmp.Compare(x.Repo, y.Repo),
			cmp.Compare(x.Issue, y.Issue),
		)
	})
	return out
}

func headerOf(rec dispatch.JournalRecord) TickHeader {
	h := TickHeader{
		At: rec.At, Mode: rec.Mode, Trigger: rec.Trigger, Outcome: rec.Outcome,
		Candidates: make([]TickCandidate, 0, len(rec.Decisions)),
	}
	for _, d := range rec.Decisions {
		pick := isPick(d.Decision)
		if pick {
			h.PickedCount++
		} else {
			h.SkippedCount++
		}
		h.Candidates = append(h.Candidates, TickCandidate{Repo: d.Repo, Issue: d.Issue, Dispatch: pick})
	}
	return h
}

// isPick reports whether a recorded decision is one the dispatcher chose. A
// dry-run lane's would_dispatch counts: it is the pick the live lane would
// have made, which is the whole point of an observation week.
func isPick(decision string) bool {
	return decision == dispatch.DecisionDispatched ||
		decision == dispatch.DecisionWouldDispatch ||
		decision == dispatch.DecisionNotApplied
}

func laterOf(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}
