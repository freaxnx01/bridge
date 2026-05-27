package main

import (
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/freaxnx01/bridge/internal/tui"
)

var tuiOnce bool

var tuiCmd = &cobra.Command{
	Use:   "tui",
	Short: "Dashboard TUI (Bubbletea — repos + cached issues live; sessions are still fixtures, see #71)",
	RunE: func(cmd *cobra.Command, args []string) error {
		return tui.Run(reposRoot(), filepath.Join(cacheRoot(), "issues.json"), tuiOnce)
	},
}

func init() {
	tuiCmd.Flags().BoolVar(&tuiOnce, "once", false, "render one frame to stdout and exit (smoke test, no TTY)")
	rootCmd.AddCommand(tuiCmd)
}
