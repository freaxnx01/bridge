package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/freaxnx01/bridge/internal/core"
	"github.com/freaxnx01/bridge/internal/store"
)

var (
	openJSON     bool
	openAgent    string
	openWorktree string
	openRC       bool
)

var openCmd = &cobra.Command{
	Use:   "open <name>",
	Short: "Open a repo (creates/attaches an agent session in Phase 2)",
	Args:  cobra.ExactArgs(1),
	RunE:  runOpen,
}

func init() {
	openCmd.Flags().BoolVar(&openJSON, "json", false, "machine-readable output")
	openCmd.Flags().StringVar(&openAgent, "agent", "", "agent to launch (claude|copilot|opencode|code); empty = no auto-launch")
	openCmd.Flags().StringVarP(&openWorktree, "worktree", "w", "", "pass-through worktree name")
	openCmd.Flags().BoolVar(&openRC, "rc", false, "pass-through --remote-control")
	rootCmd.AddCommand(openCmd)
}

func runOpen(cmd *cobra.Command, args []string) error {
	name := args[0]
	repos, err := core.DiscoverRepos(reposRoot())
	if err != nil {
		return fmt.Errorf("discover: %w", err)
	}
	repo, ok := findRepoByName(repos, name)
	if !ok {
		cmd.SilenceUsage = true
		cmd.SilenceErrors = true
		fmt.Fprintf(cmd.ErrOrStderr(), "bridge: unknown repo %q\n", name)
		os.Exit(2)
	}
	mruPath := filepath.Join(cacheRoot(), "mru")
	if err := store.MRUTouch(mruPath, repo.Path); err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: MRU touch failed: %v\n", err)
	}
	if openJSON {
		return emitJSON(cmd.OutOrStdout(), repo)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "%s\n", repo.Path)
	return nil
}

// findRepoByName returns the first repo whose Name equals name (case-insensitive).
func findRepoByName(repos []core.Repo, name string) (core.Repo, bool) {
	needle := strings.ToLower(name)
	for _, r := range repos {
		if strings.ToLower(r.Name) == needle {
			return r, true
		}
	}
	return core.Repo{}, false
}
