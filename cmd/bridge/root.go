package main

import "github.com/spf13/cobra"

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

var rootCmd = &cobra.Command{
	Use:     "bridge",
	Short:   "Repo picker + agent launcher (Go core)",
	Version: versionString(),
}

func versionString() string {
	return "bridge " + version + " (commit " + commit + ", built " + date + ")"
}
