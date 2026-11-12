package cockpit

import (
	"fmt"
	"slices"
	"time"

	"github.com/freaxnx01/bridge/internal/dispatch"
	"github.com/freaxnx01/bridge/internal/forge"
)

// Inbox card kinds, mapped from dispatch.Action.
const (
	CardHuman  = "human"
	CardRetry  = "retry"
	CardWait   = "wait"
	CardParked = "parked"
)

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
	Repo   string              `json:"repo"` // "owner/name"
	Number int                 `json:"number"`
	Title  string              `json:"title"`
	URL    string              `json:"url"`
	Kind   string              `json:"kind"`
	Reason string              `json:"reason"` // Action.Reason verbatim
	Since  time.Time           `json:"since"`  // Run.Posted, else the issue's Updated
	Run    *dispatch.RunRecord `json:"run,omitempty"`
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
		ib.Errors = withCommentErrs(ib.Errors, rd)
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
		return InboxCard{}, false
	}
	c := InboxCard{
		Repo: repoName(rd), Number: i.Number, Title: i.Title, URL: i.URL,
		Kind: kind, Reason: action.Reason, Since: i.Updated,
	}
	if s.HasRun {
		c.Run, c.Since = &s.Run, s.Run.Posted
	}
	return c, true
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

// urgency is a card's sort group: review first, then stopped runs, then
// retries, then the cards bridge knows nothing about.
func urgency(c InboxCard) int {
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

// isInboxCandidate reports whether an issue's labels can produce anything but
// ActionNone.
func isInboxCandidate(labels []string) bool { return hasAny(labels, inboxLabels...) }
