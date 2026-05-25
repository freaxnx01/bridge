package main

import (
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"github.com/freaxnx01/bridge/internal/core"
)

var sessionsJSON bool

var sessionsCmd = &cobra.Command{
	Use:   "sessions",
	Short: "Show live agent sessions",
	RunE:  runSessions,
}

func init() {
	sessionsCmd.Flags().BoolVar(&sessionsJSON, "json", false, "machine-readable output")
	rootCmd.AddCommand(sessionsCmd)
}

func runSessions(cmd *cobra.Command, args []string) error {
	sessions, err := loadSessions()
	if err != nil {
		return err
	}
	if sessionsJSON {
		return emitJSON(cmd.OutOrStdout(), sessions)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "%-20s %-10s %s\n", "slot", "state", "age")
	for _, s := range sessions {
		fmt.Fprintf(cmd.OutOrStdout(), "%-20s %-10s %s\n", s.SlotID, s.State, humanDuration(s.Age))
	}
	return nil
}

func loadSessions() ([]core.Session, error) {
	if f := os.Getenv("BRIDGE_TMUX_FIXTURE"); f != "" {
		b, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		now := time.Now().Unix()
		if v := os.Getenv("BRIDGE_NOW"); v != "" {
			if n, err := strconv.ParseInt(v, 10, 64); err == nil {
				now = n
			}
		}
		return core.ParseTmuxList(string(b), now)
	}
	return core.LiveSessions()
}

func humanDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}
