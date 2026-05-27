package main

import (
	"errors"
	"os"
)

// requireShim returns an error if the bridge shell shim isn't loaded in the
// caller's shell. Verbs that emit `cd:` / `exec:` directives via __preflight
// (e.g. `open`, `sessions attach`) need the shim to consume those directives,
// otherwise the user gets a silent no-op. The shim exports BRIDGE_SHIM_LOADED=1
// (see shims/bridge-shim.sh / .ps1) so the binary can detect this case.
func requireShim() error {
	if os.Getenv("BRIDGE_SHIM_LOADED") != "" {
		return nil
	}
	return errors.New(
		"this command needs the bridge shell shim to actually attach/cd.\n" +
			"Install it once with `make install` and start a new shell, or\n" +
			"see go-migrate.md for the source line to add to ~/.bashrc / $PROFILE.")
}
