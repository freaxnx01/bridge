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

// StartOf returns the absolute instant at which w's occurrence covering now
// began. For a window that wraps past midnight this is the previous day's
// boundary when now is on the morning side of it. A malformed From yields the
// zero time, which callers treat as "no usable boundary".
func (w Window) StartOf(now time.Time) time.Time {
	from, ok := parseHHMM(w.From)
	if !ok {
		return time.Time{}
	}
	// Pick the latest boundary instant that is not in the future. A simple
	// "today, else yesterday" test is not enough: on a fall-back day the wall
	// clock repeats, so From names two instants and time.Date resolves to the
	// later one. During the first pass that looks like a future boundary and
	// rolling back a day hands DispatchesSince the previous occurrence's
	// counter — the inherited-counter bug, for one hour a year.
	var best time.Time
	for _, dayOffset := range []int{0, -1} {
		for _, c := range boundaryInstants(now.AddDate(0, 0, dayOffset), from) {
			if c.After(now) {
				continue
			}
			if best.IsZero() || c.After(best) {
				best = c
			}
		}
	}
	if best.IsZero() {
		// Unreachable in practice — yesterday's boundary always precedes now.
		return atMinuteOfDay(now.AddDate(0, 0, -1), from)
	}
	return best
}

// boundaryInstants returns every instant on day's calendar date whose local
// wall clock reads minuteOfDay: one normally, two on a fall-back day.
func boundaryInstants(day time.Time, minuteOfDay int) []time.Time {
	t := atMinuteOfDay(day, minuteOfDay)
	out := []time.Time{t}
	if e, ok := earlierPass(t); ok {
		out = append(out, e)
	}
	return out
}

// earlierPass returns the earlier of the two instants sharing t's wall clock
// when t falls in a repeated hour. The shift is read from the zone offsets
// rather than assumed to be an hour, because some zones move by 30 minutes
// (Australia/Lord_Howe).
func earlierPass(t time.Time) (time.Time, bool) {
	_, off := t.Zone()
	_, offBefore := t.Add(-24 * time.Hour).Zone()
	shift := time.Duration(offBefore-off) * time.Second
	if shift <= 0 {
		return time.Time{}, false
	}
	e := t.Add(-shift)
	if e.Hour() != t.Hour() || e.Minute() != t.Minute() {
		return time.Time{}, false
	}
	return e, true
}

// RungGuard returns the instant the budget rung is protecting and whether the
// rung applies at now.
//
// Inside a budget_rung window the guarded instant is now itself, so the rung
// measures the plain trailing quota window. Outside one it is the next
// budget_rung window's start, and the rung *still* applies once now is within
// windowHours of it: the subscription window is rolling, so spend in the last
// windowHours of the night is still inside the window when the operator starts
// work. Without this, an unattended run at 05:00 could hand over a window that
// is already spent — the exact headroom the rung exists to reserve.
//
// Deeper into the night the rung is off: that spend has aged out of the window
// before the handover, so burning it costs the operator nothing.
func (s Schedule) RungGuard(now time.Time, windowHours float64) (time.Time, bool) {
	if w, ok := s.InWindow(now); ok && w.BudgetRung {
		return now, true
	}
	guard, ok := s.nextRungStart(now)
	if !ok {
		return time.Time{}, false
	}
	shoulder := guard.Add(-time.Duration(windowHours * float64(time.Hour)))
	return guard, !now.Before(shoulder)
}

// nextRungStart returns the earliest upcoming start of a budget_rung window.
func (s Schedule) nextRungStart(now time.Time) (time.Time, bool) {
	var best time.Time
	for _, w := range s.Windows {
		if !w.BudgetRung {
			continue
		}
		from, ok := parseHHMM(w.From)
		if !ok {
			continue
		}
		t := atMinuteOfDay(now, from)
		if !t.After(now) {
			t = t.AddDate(0, 0, 1)
		}
		if best.IsZero() || t.Before(best) {
			best = t
		}
	}
	return best, !best.IsZero()
}

// atMinuteOfDay resolves a wall-clock minute-of-day on now's calendar day.
//
// It must be calendar arithmetic, not midnight plus a duration: a DST day is
// 23 or 25 hours long, so adding 18h to midnight yields 19:00 in spring and
// 17:00 in autumn. Since InWindow compares wall-clock minutes, a duration-based
// boundary makes the two disagree about the same window twice a year — and a
// boundary that resolves to the previous day makes DispatchesSince return the
// previous night's counter, refusing a whole evening with "night cap N/N".
//
// time.Date normalises a nonexistent local time (02:30 on a spring-forward
// day) forward out of the gap, which is the wanted behaviour here.
func atMinuteOfDay(now time.Time, minuteOfDay int) time.Time {
	return time.Date(now.Year(), now.Month(), now.Day(),
		minuteOfDay/60, minuteOfDay%60, 0, 0, now.Location())
}
