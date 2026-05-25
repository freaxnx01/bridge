package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/spf13/cobra"

	"github.com/freaxnx01/bridge/internal/core"
)

var (
	listJSON    bool
	listRemote  bool
	listRefresh bool
)

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List local repos (and optionally remote)",
	RunE:  runList,
}

func init() {
	listCmd.Flags().BoolVar(&listJSON, "json", false, "machine-readable output")
	listCmd.Flags().BoolVarP(&listRemote, "remote", "r", false, "include remote listings")
	listCmd.Flags().BoolVar(&listRefresh, "refresh", false, "force refresh of remote cache")
	rootCmd.AddCommand(listCmd)
}

func reposRoot() string {
	if v := os.Getenv("BRIDGE_REPOS_ROOT"); v != "" {
		return v
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "projects", "repos")
}

func runList(cmd *cobra.Command, args []string) error {
	root := reposRoot()
	repos, err := core.DiscoverRepos(root)
	if err != nil {
		return fmt.Errorf("discover: %w", err)
	}
	sort.Slice(repos, func(i, j int) bool { return repos[i].Path < repos[j].Path })

	if listJSON {
		return emitJSON(cmd.OutOrStdout(), repos)
	}
	for _, r := range repos {
		vis := r.Visibility
		if vis == "" {
			vis = "-"
		}
		fmt.Fprintf(cmd.OutOrStdout(), "%-12s %-12s %-8s %s\n", r.Forge, r.Owner, vis, r.Name)
	}
	return nil
}
