package main

import (
	"github.com/freaxnx01/bridge/internal/herdr"
	"github.com/freaxnx01/bridge/internal/launcher"
)

// selectBackend resolves the session backend nav launches into.
//
// Precedence, highest first: an explicit BRIDGE_LAUNCHER value, then
// HERDR_ENV=1 autodetection (Herdr sets it in every managed pane), then the
// tmux/Windows-Terminal default. getenv is injected so the choice is testable
// without mutating the process environment.
//
// There is deliberately no fallback from Herdr to tmux: inside Herdr, spawning
// tmux is the very thing this backend exists to avoid, so a Herdr failure
// surfaces as an error in nav rather than as a silent tmux session.
func selectBackend(getenv func(string) string) launcher.Backend {
	switch getenv("BRIDGE_LAUNCHER") {
	case "tmux":
		return launcher.NewBackend()
	case "herdr":
		return herdr.New()
	}
	if getenv("HERDR_ENV") == "1" {
		return herdr.New()
	}
	return launcher.NewBackend()
}
