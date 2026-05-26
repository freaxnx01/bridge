package main

import (
	"log/slog"
	"os"

	"github.com/spf13/cobra"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

var rootCmd = &cobra.Command{
	Use:   "bridge",
	Short: "Repo picker + agent launcher (Go core)",
	Long: `bridge is a Go-native rewrite of the bash bridge tool.
Plan A (this binary) ships read-only commands alongside the existing bash binary;
interactive commands (open, rm, presence write, sync now, watch) ship in Plan B.

Cache lives at ~/.cache/bridge/ (overridable via XDG_CACHE_HOME).
Repo discovery walks ~/projects/repos/ (overridable via BRIDGE_REPOS_ROOT).`,
	Version: versionString(),
}

func versionString() string {
	return "bridge " + version + " (commit " + commit + ", built " + date + ")"
}

var verboseCount int

func init() {
	rootCmd.PersistentFlags().CountVarP(&verboseCount, "verbose", "v", "increase log verbosity (-v info, -vv debug)")
	rootCmd.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		slog.SetDefault(slog.New(installLogger(os.Stderr, verboseCount, "")))
		return nil
	}
}
