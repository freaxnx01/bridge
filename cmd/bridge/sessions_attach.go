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
		fmt.Fprintln(cmd.OutOrStdout(), args[0])
		return nil
	},
}

func init() {
	sessionsCmd.AddCommand(sessionsAttachCmd)
}
