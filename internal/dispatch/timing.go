package dispatch

import (
	"fmt"
	"time"
)

// Timing resolves one instant against the configured schedule: which window
// covers it, what the budget rung is guarding, the span to measure usage over,
// and which window occurrence the nightly counter belongs to.
type Timing struct {
	Now             time.Time
	Window          Window
	InWindow        bool
	RungOn          bool
	Guard           time.Time // the instant the rung protects
	MeasureFrom     time.Time
	NightCapApplies bool
	NightStart      time.Time
}

// ResolveTiming is the single place the schedule is interpreted, so a tick and
// a `status` read can never disagree about which bounds are in force.
func ResolveTiming(cfg Config, now time.Time) Timing {
	t := Timing{Now: now}
	t.Window, t.InWindow = cfg.Schedule.InWindow(now)

	guard, on := cfg.Schedule.RungGuard(now, cfg.Budget.WindowHours)
	t.RungOn = on
	t.Guard = guard
	if on {
		// Measure the quota window that ends at the guarded instant. Inside a
		// rung window that is now; in the pre-dawn shoulder it is the coming
		// handover, so only spend still inside the window at that point counts.
		t.MeasureFrom = guard.Add(-time.Duration(cfg.Budget.WindowHours * float64(time.Hour)))
	}

	// The nightly ceiling bounds unattended spend, so it belongs to a window
	// whose rung is off — and its counter resets at that window's own start.
	if t.InWindow && !t.Window.BudgetRung {
		t.NightCapApplies = true
		t.NightStart = t.Window.StartOf(now)
	}
	return t
}

// NightStartForReport resolves the occurrence the nightly counter belongs to
// for display. Inside an unattended window that is the tick's own boundary;
// elsewhere it is the latest unattended window's start, so `status` can still
// report what the last night spent instead of dropping the number.
func NightStartForReport(cfg Config, t Timing, now time.Time) time.Time {
	if !t.NightStart.IsZero() {
		return t.NightStart
	}
	var latest time.Time
	for _, w := range cfg.Schedule.Windows {
		if w.BudgetRung {
			continue
		}
		if s := w.StartOf(now); !s.IsZero() && (latest.IsZero() || s.After(latest)) {
			latest = s
		}
	}
	return latest
}

// RungLabel describes whether the budget rung is policing this moment, and
// which window that decision came from.
func RungLabel(t Timing) string {
	switch {
	case t.RungOn && t.InWindow && t.Window.BudgetRung:
		return fmt.Sprintf("active (window %s-%s)", t.Window.From, t.Window.To)
	case t.RungOn:
		// The shoulder: still in an unguarded window, but close enough to the
		// handover that this spend survives into the operator's quota window.
		return fmt.Sprintf("active (reserving headroom for the %s handover)",
			t.Guard.Format("15:04"))
	case !t.InWindow:
		return "inactive (outside every configured window)"
	default:
		return fmt.Sprintf("inactive (window %s-%s)", t.Window.From, t.Window.To)
	}
}
