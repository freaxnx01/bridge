package main

import (
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/freaxnx01/bridge/internal/core"
)

var slotsJSON bool

var slotsCmd = &cobra.Command{
	Use:   "slots",
	Short: "Show slot registry",
	RunE:  runSlots,
}

func init() {
	slotsCmd.Flags().BoolVar(&slotsJSON, "json", false, "machine-readable output")
	rootCmd.AddCommand(slotsCmd)
}

func runSlots(cmd *cobra.Command, args []string) error {
	// Match the rest of the read commands: don't dump usage on runtime errors.
	cmd.SilenceUsage = true
	slots, err := core.LoadSlots(filepath.Join(cacheRoot(), "slots.json"))
	if err != nil {
		return err
	}
	if slotsJSON {
		return emitJSON(cmd.OutOrStdout(), slots)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "%-20s %-20s %-10s %s\n", "id", "repo", "agent", "created")
	for _, s := range slots {
		wt := s.Worktree
		if wt == "" {
			wt = "-"
		}
		fmt.Fprintf(cmd.OutOrStdout(), "%-20s %-20s %-10s %s (wt=%s)\n", s.ID, s.Repo, s.Agent, s.Created.Format("2006-01-02 15:04"), wt)
	}
	return nil
}
