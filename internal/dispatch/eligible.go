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
	LabelToBeDefined     = "❓ to-be-defined"
	LabelParked          = "parked"
	LabelParkedLegacy    = "🧊 parked"
	LabelAIImplement     = "ai-implement"
	LabelAIReviewAIMerge = "ai-review-ai-merge"
	// LabelRetrying marks a re-dispatch label swap in progress. Left behind, it
	// keeps the issue out of both dispatch passes until a person clears it.
	LabelRetrying = "ai:retrying"
)

func hasLabel(labels []string, want string) bool {
	for _, l := range labels {
		if l == want {
			return true
		}
	}
	return false
}

// IsParked reports whether the issue is parked, in either spelling. The
// agent-workflow pipeline parks with the ASCII label (its ADR-016); bridge wrote
// the emoji one until #367 and still reads it, but only ever writes LabelParked.
func IsParked(labels []string) bool {
	return hasLabel(labels, LabelParked) || hasLabel(labels, LabelParkedLegacy)
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

var planHeadingRE = regexp.MustCompile(`(?im)^#{2,6}\s*implementation plan\s*$`)

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
	// Checked before the label rules on purpose: an issue with no body is unfit
	// for dispatch for a reason that has nothing to do with its labels, and an
	// intake path that forgot to stamp needs-enrichment must still be caught.
	if strings.TrimSpace(i.Body) == "" {
		return false, "empty body"
	}
	if hasLabel(i.Labels, LabelNeedsEnrichment) {
		return false, "needs-enrichment"
	}
	if hasLabel(i.Labels, LabelToBeDefined) {
		return false, "to-be-defined"
	}
	if IsParked(i.Labels) {
		return false, "parked"
	}
	// An interrupted label swap can have removed ai-implement already, so this
	// has to be checked on its own: otherwise the half-swapped issue reads as a
	// fresh candidate and gets dispatched as new work.
	if hasLabel(i.Labels, LabelRetrying) {
		return false, "retry interrupted"
	}
	if hasLabel(i.Labels, LabelAIImplement) {
		return false, "already dispatched"
	}
	if !planHeadingRE.MatchString(i.Body) {
		return false, "no implementation plan"
	}
	if HasOpenPR(prs, i.Number) {
		return false, "open PR"
	}
	if activeMilestone != "" && i.Milestone != activeMilestone {
		return false, "outside active milestone"
	}
	return true, ""
}
