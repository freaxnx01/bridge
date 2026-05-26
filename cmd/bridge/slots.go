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

var slotsPruneCmd = &cobra.Command{
	Use:   "prune",
	Short: "Drop slot entries whose tmux session is no longer live",
	RunE:  runSlotsPrune,
}

func init() {
	slotsCmd.Flags().BoolVar(&slotsJSON, "json", false, "machine-readable output")
	slotsCmd.AddCommand(slotsPruneCmd)
	rootCmd.AddCommand(slotsCmd)
}

func runSlots(cmd *cobra.Command, args []string) error {
	cmd.SilenceUsage = true
	slots, err := core.LoadSlots(filepath.Join(cacheRoot(), "slots.json"))
	if err != nil {
		return err
	}
	// Cross-reference against live sessions so we can mark live entries with
	// '*'. Errors from sessions lookup are non-fatal; absent tmux just means
	// nothing is live and every slot is reported as stale (' ').
	sessions, _ := loadSessions()
	live := make(map[string]bool, len(sessions))
	for _, s := range sessions {
		live[s.SlotID] = true
	}

	if slotsJSON {
		// Augment each slot with a "live" bool. Done via an anonymous wrapper to
		// avoid changing the on-disk Slot shape.
		type augmented struct {
			core.Slot
			Live bool `json:"live"`
		}
		out := make([]augmented, len(slots))
		for i, s := range slots {
			out[i] = augmented{Slot: s, Live: live[s.ID]}
		}
		return emitJSON(cmd.OutOrStdout(), out)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "%-2s %-20s %-20s %-10s %s\n", " ", "id", "repo", "agent", "created")
	for _, s := range slots {
		marker := " "
		if live[s.ID] {
			marker = "*"
		}
		wt := s.Worktree
		if wt == "" {
			wt = "-"
		}
		fmt.Fprintf(cmd.OutOrStdout(), "%-2s %-20s %-20s %-10s %s (wt=%s)\n",
			marker, s.ID, s.Repo, s.Agent, s.Created.Format("2006-01-02 15:04"), wt)
	}
	return nil
}

func runSlotsPrune(cmd *cobra.Command, args []string) error {
	cmd.SilenceUsage = true
	path := filepath.Join(cacheRoot(), "slots.json")
	slots, err := core.LoadSlots(path)
	if err != nil {
		return err
	}
	sessions, err := loadSessions()
	if err != nil {
		// If tmux is unavailable, refuse to prune — otherwise we'd wipe the
		// registry on every machine where tmux isn't running.
		return fmt.Errorf("prune: cannot enumerate live sessions: %w", err)
	}
	kept, dropped := core.PruneSlots(slots, sessions)
	if len(dropped) == 0 {
		fmt.Fprintln(cmd.OutOrStdout(), "no stale entries")
		return nil
	}
	if err := core.WriteSlots(path, kept); err != nil {
		return fmt.Errorf("prune: write: %w", err)
	}
	for _, d := range dropped {
		fmt.Fprintf(cmd.OutOrStdout(), "dropped %s (%s)\n", d.ID, d.Repo)
	}
	return nil
}
