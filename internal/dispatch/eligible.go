package dispatch

import (
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/freaxnx01/bridge/internal/forge"
)

const (
	LabelNeedsEnrichment = "needs-enrichment"
	LabelParked          = "🧊 parked"
	LabelAIImplement     = "ai-implement"
	attemptPrefix        = "attempt:"
	failedPrefix         = "failed:"
)

func hasLabel(labels []string, want string) bool {
	for _, l := range labels {
		if l == want {
			return true
		}
	}
	return false
}

// Attempts reads the attempt:N label. Absent or malformed means zero attempts,
// which is the safe direction: a corrupt label lets the issue run, it does not
// silently strand it.
func Attempts(labels []string) int {
	for _, l := range labels {
		if !strings.HasPrefix(l, attemptPrefix) {
			continue
		}
		n, err := strconv.Atoi(strings.TrimPrefix(l, attemptPrefix))
		if err != nil {
			return 0
		}
		return n
	}
	return 0
}

// ActiveMilestone is the open milestone with the earliest due date. A milestone
// without a due date is never active — setting a due date is how the operator
// marks a milestone active, so an undated one is explicitly "not now".
func ActiveMilestone(ms []forge.Milestone) string {
	active := ""
	var activeDue time.Time
	for _, m := range ms {
		if m.DueOn.IsZero() {
			continue
		}
		if active == "" || m.DueOn.Before(activeDue) {
			active, activeDue = m.Title, m.DueOn
		}
	}
	return active
}

var closesRE = regexp.MustCompile(`(?i)\b(clos(?:e|es|ed)|fix(?:e|es|ed)?|resolv(?:e|es|ed))\s+#(\d+)\b`)

// ClosesIssue reports whether prBody contains a GitHub closing keyword for
// issueNumber. The \b on the number is what stops "#410" matching issue 41.
func ClosesIssue(prBody string, issueNumber int) bool {
	for _, m := range closesRE.FindAllStringSubmatch(prBody, -1) {
		if n, err := strconv.Atoi(m[2]); err == nil && n == issueNumber {
			return true
		}
	}
	return false
}

// HasOpenPR reports whether any open PR closes this issue. Only closing PRs
// count, so a hand-written PR never consumes a dispatch slot.
func HasOpenPR(prs []forge.PullRequest, issueNumber int) bool {
	for _, p := range prs {
		if ClosesIssue(p.Body, issueNumber) {
			return true
		}
	}
	return false
}

// Eligible reports whether an issue may be dispatched, plus the reason it was
// skipped. activeMilestone is "" when the repo has no dated open milestone, in
// which case milestone membership is not checked at all.
func Eligible(i forge.Issue, activeMilestone string, prs []forge.PullRequest) (bool, string) {
	if hasLabel(i.Labels, LabelNeedsEnrichment) {
		return false, "needs-enrichment"
	}
	if hasLabel(i.Labels, LabelParked) {
		return false, "parked"
	}
	if Attempts(i.Labels) >= 2 {
		return false, "attempts exhausted"
	}
	if HasOpenPR(prs, i.Number) {
		return false, "open PR"
	}
	if activeMilestone != "" && i.Milestone != activeMilestone {
		return false, "outside active milestone"
	}
	return true, ""
}
