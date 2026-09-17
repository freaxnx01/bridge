package dispatch

import (
	"strconv"
	"strings"
	"time"
)

// InWindow returns the first configured window covering now. A malformed
// entry is skipped rather than fatal, and no match means dispatch does not act
// — the safe direction for a schedule.
func (s Schedule) InWindow(now time.Time) (Window, bool) {
	cur := now.Hour()*60 + now.Minute()
	for _, w := range s.Windows {
		from, ok := parseHHMM(w.From)
		if !ok {
			continue
		}
		to, ok := parseHHMM(w.To)
		if !ok {
			continue
		}
		if covers(from, to, cur) {
			return w, true
		}
	}
	return Window{}, false
}

// covers reports whether minute-of-day cur falls in [from, to), wrapping past
// midnight when from > to. from == to covers the whole day.
func covers(from, to, cur int) bool {
	switch {
	case from == to:
		return true
	case from < to:
		return cur >= from && cur < to
	default:
		return cur >= from || cur < to
	}
}

func parseHHMM(s string) (int, bool) {
	h, m, found := strings.Cut(s, ":")
	if !found {
		return 0, false
	}
	hh, err := strconv.Atoi(h)
	if err != nil || hh < 0 || hh > 23 {
		return 0, false
	}
	mm, err := strconv.Atoi(m)
	if err != nil || mm < 0 || mm > 59 {
		return 0, false
	}
	return hh*60 + mm, true
}
