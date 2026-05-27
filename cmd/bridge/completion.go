package main

import (
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/freaxnx01/bridge/internal/core"
)

// completeRepoName is a cobra ValidArgsFunction for subcommands that take a
// single repo name (open, rm). Returns local repo basenames matching the
// typed prefix (case-insensitive). MVP slice of #65 — meta-keyword fallback
// is not yet wired up.
func completeRepoName(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) >= 1 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	repos, err := core.DiscoverRepos(reposRoot())
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}
	needle := strings.ToLower(toComplete)
	out := make([]string, 0, len(repos))
	for _, r := range repos {
		if needle == "" || strings.HasPrefix(strings.ToLower(r.Name), needle) {
			out = append(out, r.Name)
		}
	}
	sort.Strings(out)
	return out, cobra.ShellCompDirectiveNoFileComp
}
