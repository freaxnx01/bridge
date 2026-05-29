package main

import (
	"context"
	"log/slog"
	"os"

	"github.com/spf13/cobra"

	"github.com/freaxnx01/bridge/internal/update"
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
Repo discovery walks ~/projects/repos/ (overridable via -B/--base,
BRIDGE_BASE, BRIDGE_REPOS_ROOT, or $XDG_CONFIG_HOME/bridge/base).`,
	Version:           versionString(),
	ValidArgsFunction: completeRepoName,
}

func versionString() string {
	s := "bridge " + version + " (commit " + commit + ", built " + date + ")"
	if hint := update.Hint(version); hint != "" {
		s += "\n" + hint
	}
	return s
}

var verboseCount int

func init() {
	rootCmd.PersistentFlags().CountVarP(&verboseCount, "verbose", "v", "increase log verbosity (-v info, -vv debug)")
	rootCmd.PersistentFlags().StringSliceVarP(&baseFlag, "base", "B", nil, "repo discovery base (repeatable; overrides BRIDGE_BASE / BRIDGE_REPOS_ROOT)")
	rootCmd.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
		slog.SetDefault(slog.New(installLogger(os.Stderr, verboseCount, "")))
		// Best-effort: TTL-gated check against GitHub /releases/latest so
		// `--version` can show a "newer version available" hint next time.
		// MaybeRefresh enforces its own short timeout and is a no-op when
		// the cache is fresh, the build is "dev", or BRIDGE_NO_VERSION_CHECK
		// is set. Cobra handles --version before PreRun, so a stale cache
		// doesn't slow that path.
		update.MaybeRefresh(context.Background(), version, "")
		return nil
	}
}
