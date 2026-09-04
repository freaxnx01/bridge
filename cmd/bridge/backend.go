package main

import (
	"fmt"
	"io"

	"github.com/freaxnx01/bridge/internal/herdr"
	"github.com/freaxnx01/bridge/internal/launcher"
)

// selectBackend resolves the session backend nav launches into.
//
// Precedence, highest first: an explicit BRIDGE_LAUNCHER value, then
// HERDR_ENV=1 autodetection (Herdr sets it in every managed pane), then the
// tmux/Windows-Terminal default. getenv is injected so the choice is testable
// without mutating the process environment, and warn receives a notice when
// BRIDGE_LAUNCHER holds an unrecognized value — a typo there would otherwise
// route the user's explicit choice to autodetection with no diagnostic at all.
//
// There is deliberately no fallback from Herdr to tmux: inside Herdr, spawning
// tmux is the very thing this backend exists to avoid, so a Herdr failure
// surfaces as an error in nav rather than as a silent tmux session.
func selectBackend(getenv func(string) string, warn io.Writer) launcher.Backend {
	switch v := getenv("BRIDGE_LAUNCHER"); v {
	case "":
		// Nothing chosen: fall through to autodetection.
	case "tmux":
		return launcher.NewBackend()
	case "herdr":
		return herdr.New()
	default:
		fmt.Fprintf(warn, "bridge: ignoring BRIDGE_LAUNCHER=%q (expected \"tmux\" or \"herdr\")\n", v)
	}
	if getenv("HERDR_ENV") == "1" {
		return herdr.New()
	}
	return launcher.NewBackend()
}
