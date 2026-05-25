package main

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/freaxnx01/bridge/internal/store"
)

type SyncState struct {
	LastRun  time.Time `json:"last_run,omitempty"`
	Queue    []string  `json:"queue,omitempty"`
	Unpushed []string  `json:"unpushed,omitempty"`
}

var syncJSON bool

var syncCmd = &cobra.Command{
	Use:   "sync [now|--auto]",
	Short: "Show autosync state (read-only in Plan A)",
	Args:  cobra.MaximumNArgs(1),
	RunE:  runSync,
}

func init() {
	syncCmd.Flags().BoolVar(&syncJSON, "json", false, "machine-readable output")
	rootCmd.AddCommand(syncCmd)
}

func runSync(cmd *cobra.Command, args []string) error {
	if len(args) > 0 {
		return fmt.Errorf("`bridge sync %s` is not implemented yet (Plan B); read-only status in Plan A", args[0])
	}
	b, err := store.ReadFile(filepath.Join(cacheRoot(), "sync.json"))
	if err != nil {
		return err
	}
	var s SyncState
	if len(b) > 0 {
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
	}
	if syncJSON {
		return emitJSON(cmd.OutOrStdout(), s)
	}
	if s.LastRun.IsZero() {
		fmt.Fprintln(cmd.OutOrStdout(), "last run: never")
	} else {
		fmt.Fprintf(cmd.OutOrStdout(), "last run: %s\n", s.LastRun.Format(time.RFC3339))
	}
	fmt.Fprintf(cmd.OutOrStdout(), "queue: %d\n", len(s.Queue))
	fmt.Fprintf(cmd.OutOrStdout(), "unpushed: %d\n", len(s.Unpushed))
	for _, r := range s.Unpushed {
		fmt.Fprintf(cmd.OutOrStdout(), "  - %s\n", r)
	}
	return nil
}
