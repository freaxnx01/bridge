//go:build !windows

package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
)

// fallbackTerm is the portable terminfo bridge falls back to when the
// advertised $TERM has no entry on the host.
const fallbackTerm = "xterm-256color"

// termResolver reports whether name has a terminfo entry on this host. It is a
// package var so tests can stub it without spawning infocmp.
var termResolver = infocmpResolves

// infocmpResolves runs `infocmp <name>` and reports success. If infocmp is not
// on PATH we cannot tell, so we report true (resolved) to preserve current
// behavior rather than risk a wrong fallback on a working setup.
func infocmpResolves(name string) bool {
	if _, err := exec.LookPath("infocmp"); err != nil {
		return true
	}
	return exec.Command("infocmp", name).Run() == nil
}

// maybeTermFallback prepends `env TERM=xterm-256color` to a tmux launch argv
// when the current $TERM has no terminfo entry on the host (which would make
// tmux abort with "missing or unsuitable terminal"). Returns argv unchanged
// when there's nothing to fix or the fallback is disabled. A one-line notice
// is written to stderr when the fallback is applied. See #104.
func maybeTermFallback(stderr io.Writer, argv []string) []string {
	if len(argv) == 0 {
		return argv
	}
	term := os.Getenv("TERM")
	if term == "" || term == fallbackTerm {
		return argv
	}
	if os.Getenv("BRIDGE_NO_TERM_FALLBACK") != "" {
		return argv
	}
	if termResolver(term) {
		return argv
	}
	fmt.Fprintf(stderr,
		"bridge: TERM=%q has no terminfo entry on this host; launching tmux with TERM=%s (set BRIDGE_NO_TERM_FALLBACK=1 to disable)\n",
		term, fallbackTerm)
	return append([]string{"env", "TERM=" + fallbackTerm}, argv...)
}
