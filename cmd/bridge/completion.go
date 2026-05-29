package main

import (
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

)

// completeRepoName is a cobra ValidArgsFunction for subcommands that take a
// single repo name (open, rm). Returns local repo basenames matching the
// typed prefix (case-insensitive). MVP slice of #65 — meta-keyword fallback
// is not yet wired up.
func completeRepoName(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
	if len(args) >= 1 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	repos, err := discoverAllRoots()
	if err != nil {
		return nil, cobra.ShellCompDirectiveError
	}
	needle := strings.ToLower(toComplete)
	out := make([]string, 0, len(repos))
	for _, r := range repos {
		if needle == "" {
			out = append(out, r.Name)
			continue
		}
		lower := strings.ToLower(r.Name)
		if !strings.HasPrefix(lower, needle) {
			continue
		}
		// Bash's compgen post-filters suggestions case-sensitively against
		// the typed prefix. To get true case-insensitive completion without
		// requiring `completion-ignore-case on` in ~/.inputrc, splice the
		// user's typed casing onto the canonical repo name. The resolver
		// (findRepoByName) is already case-insensitive, so accepting e.g.
		// "BRIdge" still opens the "bridge" repo.
		out = append(out, toComplete+r.Name[len(toComplete):])
	}
	sort.Strings(out)
	return out, cobra.ShellCompDirectiveNoFileComp
}

// completeMetaCmd backs the bash-completion meta-fallback augmenter shim
// (`shims/bridge-completion-meta.sh`). The shim calls
// `bridge __complete-meta <prefix>` when cobra's primary completion comes
// back empty, then sets COMPREPLY directly — bypassing compgen's
// case-sensitive prefix filter that would otherwise drop non-prefix-matching
// meta hits like `nextgen` → `ArchiveRestApiNextGen`.
//
// Returns one repo name per line on stdout. Empty stdout = no meta hits.
// Hidden from --help; this is plumbing, not user surface.
var completeMetaCmd = &cobra.Command{
	Use:                "__complete-meta <prefix>",
	Hidden:             true,
	DisableFlagParsing: true,
	Args:               cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		prefix := args[0]
		if prefix == "" {
			return nil
		}
		repos, err := reposWithMeta()
		if err != nil {
			return nil
		}
		// Skip basename prefix hits — cobra's primary completion already
		// handled those. We want basename substring hits (e.g. `nextgen`
		// → `ArchiveRestApiNextGen`) plus Desc/Topics matches, all of
		// which cobra's compgen filter would drop.
		needle := strings.ToLower(prefix)
		seen := map[string]bool{}
		for _, r := range repos {
			lname := strings.ToLower(r.Name)
			if strings.HasPrefix(lname, needle) {
				continue
			}
			match := strings.Contains(lname, needle) ||
				strings.Contains(strings.ToLower(r.Desc), needle)
			if !match {
				for _, t := range r.Topics {
					if strings.Contains(strings.ToLower(t), needle) {
						match = true
						break
					}
				}
			}
			if match && !seen[r.Name] {
				seen[r.Name] = true
				fmt.Fprintln(cmd.OutOrStdout(), r.Name)
			}
		}
		return nil
	},
}

func init() {
	rootCmd.AddCommand(completeMetaCmd)
}
