//go:build windows

package main

import (
	"errors"

	"github.com/spf13/cobra"
)

// launchCmd is a hidden stub: it keeps rewritePositional from turning
// `bridge launch x` into `open launch x` on Windows.
var launchCmd = &cobra.Command{
	Use:                "launch",
	Hidden:             true,
	DisableFlagParsing: true,
	RunE: func(*cobra.Command, []string) error {
		return errors.New("bridge launch: unix-only (needs tmux)")
	},
}

func init() { rootCmd.AddCommand(launchCmd) }
