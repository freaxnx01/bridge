package main

import (
	"os"
	"strings"
)

// knownVerbs lists subcommands cobra owns. Anything not in this set,
// not starting with "-", and present as the first arg is rewritten to
// `open <arg>` so muscle-memory `bridge bridge` opens the bridge repo.
var knownVerbs = map[string]bool{
	"list": true, "slots": true, "sessions": true, "presence": true,
	"sync": true, "status": true, "issues": true, "open": true,
	"rm": true, "watch": true, "tui": true, "__preflight": true,
	"version": true, "help": true, "completion": true,
	"__complete": true, "__completeNoDesc": true, "__complete-meta": true,
}

// rewritePositional runs in main() before rootCmd.Execute().
// If os.Args has the form `bridge <positional-name> [flags]` where
// positional-name is not a known verb, it becomes `bridge open <positional-name> [flags]`.
func rewritePositional() {
	if len(os.Args) < 2 {
		return
	}
	first := os.Args[1]
	if knownVerbs[first] {
		return
	}
	if strings.HasPrefix(first, "-") {
		return
	}
	rest := os.Args[2:]
	os.Args = append([]string{os.Args[0], "open", first}, rest...)
}
