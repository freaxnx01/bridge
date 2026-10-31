//go:build windows

package main

import (
	"errors"

	"github.com/spf13/cobra"
)

// botCmd is a hidden stub: it keeps rewritePositional from turning
// `bridge bot` into `open bot` on Windows.
var botCmd = &cobra.Command{
	Use:                "bot",
	Hidden:             true,
	DisableFlagParsing: true,
	RunE: func(*cobra.Command, []string) error {
		return errors.New("bridge bot: unix-only (needs tmux)")
	},
}

func init() { rootCmd.AddCommand(botCmd) }
