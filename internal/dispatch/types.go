// Package dispatch decides which enriched issues to hand to the agent-workflow
// pipeline. Everything here except Run is a pure function over plain structs:
// no network, no clock, no filesystem. That is what makes it table-testable.
package dispatch

import (
	"time"

	"github.com/freaxnx01/bridge/internal/usage"
)

type Limits struct {
	GlobalOpenPRs         int            `json:"global_open_prs"`
	PerRepo               int            `json:"per_repo"`
	MaxDispatchesPerNight int            `json:"max_dispatches_per_night"`
	Overrides             map[string]int `json:"overrides,omitempty"`
}

// Window is a span of the local day during which dispatch ticks act. From is
// inclusive, To exclusive; From > To wraps past midnight. BudgetRung turns the
// usage-budget rung on for the window.
type Window struct {
	From       string `json:"from"`
	To         string `json:"to"`
	BudgetRung bool   `json:"budget_rung"`
}

// Schedule is the single source of truth for when dispatch acts. The systemd
// timer is a bare hourly heartbeat, so these windows are the only place the
// hours are written down — the previous dispatch_at/retry_until fields were
// read by nothing and duplicated the timer.
type Schedule struct {
	Windows []Window `json:"windows"`
}

// Budget configures the usage-budget rung. Every value is a calibrated proxy:
// no API reports how much of the 5h subscription window is left, so trailing
// consumption is summed in USD-equivalent against WindowBudgetUSD, which is
// pinned empirically against /usage.
type Budget struct {
	WindowHours     float64               `json:"window_hours"`
	WindowBudgetUSD float64               `json:"window_budget_usd"`
	DaytimeCap      float64               `json:"daytime_cap"`
	MeanRunCostUSD  float64               `json:"mean_run_cost_usd"`
	Pricing         map[string]usage.Rate `json:"pricing,omitempty"`
}

type Config struct {
	Limits   Limits   `json:"limits"`
	Schedule Schedule `json:"schedule"`
	Budget   Budget   `json:"budget"`
	// RepoPriority is an ordered list of repo-name patterns (path.Match glob
	// syntax) driving the ordering ladder's first rung. Absent/empty skips
	// the rung entirely, which is what keeps pre-existing configs unchanged.
	RepoPriority []string `json:"repo_priority,omitempty"`
}

// State is the only local mutable state the dispatcher keeps. Everything else
// lives in the forge as labels so it survives a cache wipe.
type State struct {
	Paused            bool      `json:"paused"`
	LastTick          time.Time `json:"last_tick,omitempty"`
	DispatchedTonight int       `json:"dispatched_tonight"`
	NightStartedAt    time.Time `json:"night_started_at,omitempty"`
}
