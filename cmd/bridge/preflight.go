package main

import (
	"io"

	"github.com/spf13/cobra"

	"github.com/freaxnx01/bridge/internal/shellbridge"
)

var preflightCmd = &cobra.Command{
	Use:                "__preflight [user-args...]",
	Short:              "internal: emit a shell directive for the shim",
	Hidden:             true,
	DisableFlagParsing: true,
	RunE:               runPreflight,
}

func init() {
	rootCmd.AddCommand(preflightCmd)
}

func runPreflight(cmd *cobra.Command, args []string) error {
	return dispatchPreflight(cmd.OutOrStdout(), args)
}

// dispatchPreflight inspects the user-typed args and decides what directive
// (if any) the parent shell must perform. For verbs handled entirely inside
// the Go binary (list, slots, status, …), it returns noop. For verbs that
// must change the shell (open, sessions attach, the no-arg picker, a bare
// positional repo name), it emits the corresponding directive.
//
// Phase 2 grows this function task by task. This task only knows noop.
func dispatchPreflight(out io.Writer, args []string) error {
	return shellbridge.EmitNoop(out)
}
