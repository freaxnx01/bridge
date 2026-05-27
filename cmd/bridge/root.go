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
	Long: `bridge is a repo picker and agent-session launcher. It walks
~/projects/repos/, presents an fzf picker, then opens the selected repo in
a tmux-wrapped agent session (Claude Code, Copilot, opencode, VS Code) or
cd's into it via the shell shim.

Cache lives at ~/.cache/bridge/ (overridable via XDG_CACHE_HOME).
Repo discovery walks ~/projects/repos/ (overridable via BRIDGE_REPOS_ROOT).`,
	Version:           versionString(),
	ValidArgsFunction: completeRepoName,
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
