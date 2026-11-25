package cockpit

import (
	"fmt"
	"slices"
	"time"

	"github.com/freaxnx01/bridge/internal/dispatch"
	"github.com/freaxnx01/bridge/internal/forge"
)

// Inbox card kinds, mapped from dispatch.Action — plus CardMerge, which is not
// an Action at all: it is the ai:done issue whose agent PR waits for a person
// to merge it, which dispatch has nothing left to say about.
const (
	CardHuman  = "human"
	CardRetry  = "retry"
	CardWait   = "wait"
	CardParked = "parked"
	CardMerge  = "merge"
)

// reasonReadyToMerge is a merge card's reason when nothing blocks the merge.
const reasonReadyToMerge = "ready to merge"

// inboxLabels are the labels that can produce anything but ActionNone. An
// issue carrying none of them is skipped without reading its comments.
var inboxLabels = []string{
	dispatch.LabelAIImplement, dispatch.LabelAIFailed, dispatch.LabelAIPartial,
	dispatch.LabelAIDone, dispatch.LabelAIPaused, dispatch.LabelAIReviewBlocked,
	dispatch.LabelAIReviewFailed, dispatch.LabelAIRunnerBlocked, dispatch.LabelAIChecksBlocked,
	dispatch.LabelParked, dispatch.LabelParkedLegacy,
}

// reviewReasons are the reasons that mean work exists and waits for review —
// the most urgent group of cards.
var reviewReasons = []string{"review blocked", "review failed", "checks blocked", "partial"}

// noInfoReasons are the reasons bridge cannot say anything about the run from,
// so they sort last.
var noInfoReasons = []string{"no run marker", "retry never reported", "re-dispatch never reported"}

// InboxCard is one issue in the inbox, in whichever of the three buckets its
// NextAction put it.
type InboxCard struct {
	Forge  string              `json:"forge"`
	Repo   string              `json:"repo"` // "owner/name"
	Number int                 `json:"number"`
	Title  string              `json:"title"`
	URL    string              `json:"url"`
	Kind   string              `json:"kind"`
	Reason string              `json:"reason"` // Action.Reason verbatim
	Since  time.Time           `json:"since"`  // Run.Posted, else the issue's Updated
	Run    *dispatch.RunRecord `json:"run,omitempty"`
	// Merge is the PR evidence of a CardMerge card, and nil on every other kind.
	Merge *MergeEvidence `json:"merge,omitempty"`
}

// Inbox is the whole payload of the cockpit's start page.
type Inbox struct {
	Cards       []InboxCard       `json:"cards"`   // human + retry, urgency order
	Waiting     []InboxCard       `json:"waiting"` // wait, oldest first
	Parked      []InboxCard       `json:"parked"`  // oldest first
	CollectedAt time.Time         `json:"collectedAt"`
	Errors      map[string]string `json:"errors,omitempty"`
}

// InboxInput is everything BuildInbox needs.
type InboxInput struct {
	Repos []RepoData
	Now   time.Time
}

// IssueRef names the issue an IssueDetail is about.
type IssueRef struct {
	Repo   string   `json:"repo"`
	Number int      `json:"number"`
	Title  string   `json:"title"`
	URL    string   `json:"url"`
	Labels []string `json:"labels"`
}

// IssueDetail is one issue plus its run history, newest run first.
type IssueDetail struct {
	Issue IssueRef             `json:"issue"`
	Runs  []dispatch.RunRecord `json:"runs"`
}

// BuildInbox classifies every inbox candidate into cards, waiting lines and
// parked entries. It fills everything except CollectedAt.
func BuildInbox(in InboxInput) Inbox {
	ib := Inbox{Cards: []InboxCard{}, Waiting: []InboxCard{}, Parked: []InboxCard{}}
	for _, rd := range in.Repos {
		for _, i := range rd.Issues {
			c, ok := classify(rd, i, in.Now)
			switch {
			case !ok:
			case c.Kind == CardParked:
				ib.Parked = append(ib.Parked, c)
			case c.Kind == CardWait:
				ib.Waiting = append(ib.Waiting, c)
			default:
				ib.Cards = append(ib.Cards, c)
			}
		}
		ib.Errors = withMergeErrs(withCommentErrs(ib.Errors, rd), rd)
	}
	slices.SortStableFunc(ib.Cards, func(x, y InboxCard) int {
		if d := urgency(x) - urgency(y); d != 0 {
			return d
		}
		return x.Since.Compare(y.Since)
	})
	sortBySince(ib.Waiting)
	sortBySince(ib.Parked)
	return ib
}

// BuildIssueDetail pairs an issue with every trusted run marker on it, newest
// first.
func BuildIssueDetail(repo string, i forge.Issue, comments []forge.Comment) IssueDetail {
	labels := i.Labels
	if labels == nil {
		labels = []string{}
	}
	runs := dispatch.Runs(comments)
	slices.Reverse(runs)
	return IssueDetail{
		Issue: IssueRef{Repo: repo, Number: i.Number, Title: i.Title, URL: i.URL, Labels: labels},
		Runs:  runs,
	}
}

// classify maps one issue onto a card. It reports false when the issue is not
// an inbox candidate of a wired GitHub repo, when its comments have never been
// read — an unread issue must never read as "no run marker" — or when its
// NextAction is ActionNone.
func classify(rd RepoData, i forge.Issue, now time.Time) (InboxCard, bool) {
	if rd.Forge != "github" || !rd.Wired || !isInboxCandidate(i.Labels) {
		return InboxCard{}, false
	}
	comments, read := rd.Comments[i.Number]
	if !read {
		return InboxCard{}, false
	}
	// The same constructor the dispatcher's retry pass uses, so a guarded retry
	// (rows 15a-15c) reads here exactly as it does there.
	s := dispatch.StateFor(i, comments, rd.Runs, rd.PRs, now)
	action := dispatch.NextAction(s)
	kind, ok := cardKind(action)
	if !ok {
		// Nothing left for the dispatcher means the run succeeded: the only card
		// still owed is the merge of the PR it opened.
		return mergeCard(rd, i)
	}
	c := InboxCard{
		Forge: rd.Forge, Repo: repoName(rd), Number: i.Number, Title: i.Title, URL: i.URL,
		Kind: kind, Reason: action.Reason, Since: i.Updated,
	}
	if s.HasRun {
		c.Run, c.Since = &s.Run, s.Run.Posted
	}
	return c, true
}

// mergeCard is the card of an ai:done issue the collector gathered merge
// evidence for. It reports false for anything else — without evidence there is
// nothing to show and nothing to merge.
func mergeCard(rd RepoData, i forge.Issue) (InboxCard, bool) {
	if !hasAny(i.Labels, dispatch.LabelAIDone) {
		return InboxCard{}, false
	}
	ev, ok := rd.Merge[i.Number]
	if !ok {
		return InboxCard{}, false
	}
	reason := ev.Reason
	if ev.CanMerge {
		reason = reasonReadyToMerge
	}
	return InboxCard{
		Forge: rd.Forge, Repo: repoName(rd), Number: i.Number, Title: i.Title, URL: i.URL,
		Kind: CardMerge, Reason: reason, Since: prUpdated(rd, i, ev), Merge: &ev,
	}, true
}

// prUpdated is the card's age anchor: the PR's own Updated, because a merge
// card is about the PR waiting, not about the issue. It falls back to the
// issue's when the PR is not in the snapshot.
func prUpdated(rd RepoData, i forge.Issue, ev MergeEvidence) time.Time {
	for _, p := range rd.PRs {
		if p.Number == ev.PR && !p.Updated.IsZero() {
			return p.Updated
		}
	}
	return i.Updated
}

// cardKind maps an action onto a bucket. Parked is its own kind: the pipeline
// parks at its attempt cap and a person parks on purpose, and neither is a card.
func cardKind(a dispatch.Action) (string, bool) {
	switch a.Kind {
	case dispatch.ActionHuman:
		if a.Reason == "parked" {
			return CardParked, true
		}
		return CardHuman, true
	case dispatch.ActionRetry:
		return CardRetry, true
	case dispatch.ActionWait:
		return CardWait, true
	}
	return "", false
}

// urgency is a card's sort group: review and merges first, then stopped runs,
// then retries, then the cards bridge knows nothing about.
func urgency(c InboxCard) int {
	if c.Kind == CardMerge {
		return 1
	}
	if c.Kind != CardHuman {
		return 3
	}
	switch {
	case slices.Contains(reviewReasons, c.Reason):
		return 1
	case slices.Contains(noInfoReasons, c.Reason):
		return 4
	}
	return 2
}

func sortBySince(cards []InboxCard) {
	slices.SortStableFunc(cards, func(x, y InboxCard) int { return x.Since.Compare(y.Since) })
}

// withCommentErrs adds one entry per failed comment fetch, keyed like the
// board's source errors.
func withCommentErrs(errs map[string]string, rd RepoData) map[string]string {
	for number, msg := range rd.CommentErrs {
		if errs == nil {
			errs = map[string]string{}
		}
		errs[fmt.Sprintf("comments %s#%d", repoName(rd), number)] = msg
	}
	return errs
}

// withMergeErrs adds one entry per failed merge-evidence read, keyed like the
// comment errors.
func withMergeErrs(errs map[string]string, rd RepoData) map[string]string {
	for number, msg := range rd.MergeErrs {
		if errs == nil {
			errs = map[string]string{}
		}
		errs[fmt.Sprintf("merge %s#%d", repoName(rd), number)] = msg
	}
	return errs
}

// isInboxCandidate reports whether an issue's labels can produce anything but
// ActionNone.
func isInboxCandidate(labels []string) bool { return hasAny(labels, inboxLabels...) }
