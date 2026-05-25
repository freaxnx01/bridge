package main

import (
	"encoding/json"
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/freaxnx01/bridge/internal/core"
	"github.com/freaxnx01/bridge/internal/store"
)

var statusJSON bool

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Slim composed summary",
	RunE:  runStatus,
}

func init() {
	statusCmd.Flags().BoolVar(&statusJSON, "json", false, "machine-readable output")
	rootCmd.AddCommand(statusCmd)
}

type statusOut struct {
	Sessions int    `json:"sessions"`
	Presence string `json:"presence"`
	Sync     struct {
		Unpushed int `json:"unpushed"`
	} `json:"sync"`
	Version string `json:"version"`
}

func runStatus(cmd *cobra.Command, args []string) error {
	var st statusOut
	st.Version = versionString()

	sessions, _ := loadSessions()
	st.Sessions = len(sessions)

	pr, _ := core.LoadPresence(filepath.Join(cacheRoot(), "presence.json"))
	st.Presence = pr.Mode

	if b, err := store.ReadFile(filepath.Join(cacheRoot(), "sync.json")); err == nil && len(b) > 0 {
		var sy SyncState
		if err := json.Unmarshal(b, &sy); err == nil {
			st.Sync.Unpushed = len(sy.Unpushed)
		}
	}

	if statusJSON {
		return emitJSON(cmd.OutOrStdout(), st)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "sessions:  %d\n", st.Sessions)
	fmt.Fprintf(cmd.OutOrStdout(), "presence:  %s\n", st.Presence)
	fmt.Fprintf(cmd.OutOrStdout(), "unpushed:  %d\n", st.Sync.Unpushed)
	fmt.Fprintf(cmd.OutOrStdout(), "version:   %s\n", st.Version)
	return nil
}
