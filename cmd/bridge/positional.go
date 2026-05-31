package main

import (
	"os"
	"strings"
)

// isKnownVerb reports whether name is a real subcommand, so it must NOT be
// rewritten to `open <name>`. Registered cobra commands (and their aliases) are
// the source of truth, so new subcommands never need manual registration here.
// cobra-internal entry points start with "__"; a small residual set covers
// built-ins that aren't discoverable as registered commands (notably the
// `--version` flag surfaced as `version`).
func isKnownVerb(name string) bool {
	if strings.HasPrefix(name, "__") {
		return true
	}
	switch name {
	case "version", "help", "completion":
		return true
	}
	for _, c := range rootCmd.Commands() {
		if c.Name() == name {
			return true
		}
		for _, a := range c.Aliases {
			if a == name {
				return true
			}
		}
	}
	return false
}

// rewritePositional runs in main() before rootCmd.Execute().
// If os.Args has the form `bridge <positional-name> [flags]` where
// positional-name is not a known verb, it becomes `bridge open <positional-name> [flags]`.
func rewritePositional() {
	if len(os.Args) < 2 {
		return
	}
	first := os.Args[1]
	if isKnownVerb(first) {
		return
	}
	if strings.HasPrefix(first, "-") {
		return
	}
	rest := os.Args[2:]
	os.Args = append([]string{os.Args[0], "open", first}, rest...)
}
