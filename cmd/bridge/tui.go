package main

import (
	"errors"

	"github.com/spf13/cobra"
)

var tuiCmd = &cobra.Command{
	Use:   "tui",
	Short: "Dashboard TUI (reserved; not implemented yet)",
	RunE: func(cmd *cobra.Command, args []string) error {
		return errors.New("bridge tui: not implemented yet — see the dashboard spec")
	},
}

func init() {
	rootCmd.AddCommand(tuiCmd)
}
