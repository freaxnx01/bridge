package dispatch

// RetryCandidate reports whether an issue's labels put it into the retry pass,
// plus the reason it was left out. A non-empty reason means "list it as a
// skipped retry"; an empty one means the issue is not a retry issue at all and
// is dropped silently, which is what keeps the pass off every open issue.
//
// Only a dispatched issue with a failed run can reach ActionRetry, so the label
// pair is a pre-filter: it spares every other issue the comment fetch.
func RetryCandidate(labels []string) (bool, string) {
	if hasLabel(labels, LabelRetrying) {
		return false, "retry interrupted"
	}
	if !hasLabel(labels, LabelAIImplement) || !hasLabel(labels, LabelAIFailed) {
		return false, ""
	}
	if IsParked(labels) {
		return false, "parked"
	}
	if hasLabel(labels, LabelAIPaused) {
		return false, "paused"
	}
	return true, ""
}
