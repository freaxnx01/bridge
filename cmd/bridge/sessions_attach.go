package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

var sessionsAttachCmd = &cobra.Command{
	Use:   "attach <slot>",
	Short: "Attach to a live session (the shim is responsible for the actual attach via __preflight)",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireShim(); err != nil {
			cmd.SilenceUsage = true
			cmd.SilenceErrors = true
			return err
		}
		if len(args) == 0 {
			fmt.Fprintln(cmd.ErrOrStderr(), "bridge sessions attach: pick a slot (run via the shim for an interactive picker)")
			return nil
		}
		fmt.Fprintln(cmd.OutOrStdout(), args[0])
		return nil
	},
}

func init() {
	sessionsCmd.AddCommand(sessionsAttachCmd)
}
