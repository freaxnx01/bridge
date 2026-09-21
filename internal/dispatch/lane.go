package dispatch

import "time"

// spans returns the spans this lane acts in: its own when configured, the
// schedule's otherwise. Inheriting is what keeps a lanes-only config from
// silently changing when the dispatcher runs.
func (l Lane) spans(s Schedule) []Span {
	if len(l.Windows) > 0 {
		return l.Windows
	}
	out := make([]Span, 0, len(s.Windows))
	for _, w := range s.Windows {
		out = append(out, w.Span)
	}
	return out
}

// InWindow reports whether the lane acts at now.
func (l Lane) InWindow(s Schedule, now time.Time) bool {
	for _, sp := range l.spans(s) {
		if sp.Covers(now) {
			return true
		}
	}
	return false
}

// WindowStart returns the start of the lane's current window occurrence — the
// boundary its dispatch counter resets at. Zero when no span covers now.
func (l Lane) WindowStart(s Schedule, now time.Time) time.Time {
	for _, sp := range l.spans(s) {
		if sp.Covers(now) {
			return sp.StartOf(now)
		}
	}
	return time.Time{}
}
