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

// Span is a range of the local day. From is inclusive, To exclusive; From > To
// wraps past midnight; From == To covers the whole day. It is split out of
// Window because a lane's windows say only *when*, never anything about the
// budget rung — that stays global.
type Span struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// Window is a schedule span plus the rung flag. Embedding Span keeps the JSON
// shape and every existing `w.From` call site unchanged.
type Window struct {
	Span
	BudgetRung bool `json:"budget_rung"`
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
	Lanes        []Lane   `json:"lanes,omitempty"`
}

// LaneLimits are a lane's overrides of the top-level limits. A zero field
// means "inherit", so a lane states only what it changes.
type LaneLimits struct {
	PerRepo   int            `json:"per_repo,omitempty"`
	Overrides map[string]int `json:"overrides,omitempty"`
	// MaxDispatches bounds one window occurrence of this lane. With a 24h
	// window that is a calendar day; with the default night window it is one
	// night. Zero means unbounded by this rung.
	MaxDispatches int `json:"max_dispatches,omitempty"`
}

// Lane is one autonomy lane: which repos it claims, when it acts, what bounds
// it, and which labels a dispatch in it applies.
type Lane struct {
	Name  string   `json:"name"`
	Repos []string `json:"repos"`
	// Autonomous says this lane's PRs are reviewed and merged by the pipeline.
	// Three things follow from it: the lane is exempt from global_open_prs, its
	// repos are gated on agent.yml opting into ai-merge, and its default labels
	// carry the ai-merge gate label.
	Autonomous bool `json:"autonomous,omitempty"`
	// DryRun runs the lane for real through every decision and applies nothing.
	DryRun  bool       `json:"dry_run,omitempty"`
	Windows []Span     `json:"windows,omitempty"`
	Labels  []string   `json:"labels,omitempty"`
	Limits  LaneLimits `json:"limits,omitempty"`
}

// State is the only local mutable state the dispatcher keeps. Everything else
// lives in the forge as labels so it survives a cache wipe.
type State struct {
	Paused            bool      `json:"paused"`
	LastTick          time.Time `json:"last_tick,omitempty"`
	DispatchedTonight int       `json:"dispatched_tonight"`
	NightStartedAt    time.Time `json:"night_started_at,omitempty"`
}
