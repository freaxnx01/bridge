package overview

import (
	"math"
	"strconv"
	"strings"
	"time"
)

const (
	defaultEffort       = 3
	staleValueThreshold = 4
	staleAfter          = 30 * 24 * time.Hour
	urgentWithin        = 3 * 24 * time.Hour
	soonWithin          = 14 * 24 * time.Hour
)

// scoreItem computes the W3 score and stale flag for a structured item.
// score = round(value/effort + urgencyBoost, 2). effort 0 => defaultEffort.
// stale is a flag (not a score term): a high-value item untouched for a while.
func scoreItem(value, effort int, due *time.Time, updated, now time.Time) (float64, bool) {
	if effort <= 0 {
		effort = defaultEffort
	}
	s := float64(value)/float64(effort) + urgencyBoost(due, now)
	s = math.Round(s*100) / 100
	stale := value >= staleValueThreshold && now.Sub(updated) > staleAfter
	return s, stale
}

// urgencyBoost adds weight as a due date approaches (or passes).
func urgencyBoost(due *time.Time, now time.Time) float64 {
	if due == nil {
		return 0
	}
	switch d := due.Sub(now); {
	case d <= urgentWithin: // due within 3 days or overdue
		return 2
	case d <= soonWithin: // due within 14 days
		return 1
	default:
		return 0
	}
}

// weightFromLabels extracts value/effort from "value/N" and "effort/N" labels.
// Missing or malformed labels yield 0 (value 0 => unweighted; effort 0 =>
// default applied during scoring). N must be 1..5.
func weightFromLabels(labels []string) (value, effort int) {
	for _, l := range labels {
		if n, ok := labelNum(l, "value/"); ok {
			value = n
		}
		if n, ok := labelNum(l, "effort/"); ok {
			effort = n
		}
	}
	return value, effort
}

func labelNum(label, prefix string) (int, bool) {
	if !strings.HasPrefix(label, prefix) {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimPrefix(label, prefix))
	if err != nil || n < 1 || n > 5 {
		return 0, false
	}
	return n, true
}
