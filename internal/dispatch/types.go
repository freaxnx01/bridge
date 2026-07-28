// Package dispatch decides which enriched issues to hand to the agent-workflow
// pipeline. Everything here except Run is a pure function over plain structs:
// no network, no clock, no filesystem. That is what makes it table-testable.
package dispatch

import "time"

type Limits struct {
	GlobalOpenPRs         int            `json:"global_open_prs"`
	PerRepo               int            `json:"per_repo"`
	MaxDispatchesPerNight int            `json:"max_dispatches_per_night"`
	Overrides             map[string]int `json:"overrides,omitempty"`
}

type Schedule struct {
	DispatchAt string `json:"dispatch_at"`
	RetryUntil string `json:"retry_until"`
}

type Config struct {
	Limits   Limits   `json:"limits"`
	Schedule Schedule `json:"schedule"`
}

// State is the only local mutable state the dispatcher keeps. Everything else
// lives in the forge as labels so it survives a cache wipe.
type State struct {
	Paused            bool      `json:"paused"`
	LastTick          time.Time `json:"last_tick,omitempty"`
	DispatchedTonight int       `json:"dispatched_tonight"`
	NightStartedAt    time.Time `json:"night_started_at,omitempty"`
}
