package dispatch

import (
	"fmt"
	"strings"

	"github.com/freaxnx01/bridge/internal/forge"
)

const maxAttempts = 2

// transientBuckets are failures that say nothing about the issue itself, so
// retrying costs nothing but a tick. Everything else counts against the
// attempt budget.
var transientBuckets = map[string]bool{
	"api_auth":   true,
	"rate_limit": true,
	"infra":      true,
}

// FailureBucket returns the bucket from a failed:<bucket> label, or "".
func FailureBucket(labels []string) string {
	for _, l := range labels {
		if strings.HasPrefix(l, failedPrefix) {
			return strings.TrimPrefix(l, failedPrefix)
		}
	}
	return ""
}

func IsTransient(bucket string) bool { return transientBuckets[bucket] }

// Action is the label and comment work a retry tick must perform on one issue.
type Action struct {
	AddLabels    []string
	RemoveLabels []string
	Comment      string
	Retry        bool
}

// NextAction decides what to do with an issue whose run failed.
//
// A failed run often produces no PR at all, so the WIP slot frees and the
// issue stays eligible. Without the attempt budget the dispatcher would
// re-dispatch it forever, burning money silently. That is the hole this closes.
func NextAction(i forge.Issue) Action {
	bucket := FailureBucket(i.Labels)
	if bucket == "" {
		return Action{}
	}

	a := Action{RemoveLabels: []string{failedPrefix + bucket}}

	if IsTransient(bucket) {
		a.Retry = true
		return a
	}

	attempts := Attempts(i.Labels) + 1
	if attempts > 1 {
		a.RemoveLabels = append(a.RemoveLabels, fmt.Sprintf("%s%d", attemptPrefix, attempts-1))
	}
	a.AddLabels = append(a.AddLabels, fmt.Sprintf("%s%d", attemptPrefix, attempts))

	if attempts >= maxAttempts {
		a.AddLabels = append(a.AddLabels, LabelParked)
		a.Comment = fmt.Sprintf(
			"Parked by `bridge dispatch` after %d failed runs (last failure: `%s`). "+
				"Remove the parked label to let it run again.", attempts, bucket)
		return a
	}

	a.Retry = true
	a.Comment = fmt.Sprintf("Run failed (`%s`). Retrying — attempt %d of %d.", bucket, attempts, maxAttempts)
	return a
}
