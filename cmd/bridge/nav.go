package main

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/freaxnx01/bridge/internal/agents"
	"github.com/freaxnx01/bridge/internal/core"
	"github.com/freaxnx01/bridge/internal/forge"
	"github.com/freaxnx01/bridge/internal/nav"
)

var navOnce bool

var navCmd = &cobra.Command{
	Use:   "nav",
	Short: "Interactive navigator: pick a repo, then manage its sessions & worktrees",
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg := nav.Config{
			ReposRoots:   reposRoots(),
			RemoteCache:  filepath.Join(cacheRoot(), "remote.list"),
			SlotsPath:    filepath.Join(cacheRoot(), "slots.json"),
			DefaultAgent: os.Getenv("BRIDGE_DEFAULT_AGENT"),
			AgentArgs:    strings.Fields(os.Getenv("BRIDGE_DEFAULT_AGENT_ARGS")),
			Version:      version,
			DebugKeys:    navDebugPath(),
			Once:         navOnce,
			NameArgs: func(agent string, repo core.Repo, wt string) []string {
				// Reuse the open path's claude labelling: prepend -n "<repo> [<wt>]"
				// and install the relabel hook so /clear keeps the name.
				spec := withClaudeName(agents.AgentSpec{Name: agent}, repo, wt)
				ensureClaudeRelabel(agents.AgentSpec{Name: agent}, repo, wt)
				return spec.Args
			},
			Clone: func(ref forge.RepoRef) (core.Repo, error) {
				dir, err := cloneRemoteRepo(ref)
				if err != nil {
					return core.Repo{}, err
				}
				return repoFromClonedRef(reposRoot(), ref, dir), nil
			},
		}
		return nav.Run(cfg)
	},
}

func init() {
	navCmd.Flags().BoolVar(&navOnce, "once", false, "render one frame to stdout and exit (smoke test, no TTY)")
	rootCmd.AddCommand(navCmd)
}

// navDebugPath resolves BRIDGE_NAV_DEBUG into a key-log file path. "1"/"true"/
// "yes" map to a default temp file; any other non-empty value is used verbatim.
func navDebugPath() string {
	switch v := os.Getenv("BRIDGE_NAV_DEBUG"); v {
	case "":
		return ""
	case "1", "true", "yes":
		return filepath.Join(os.TempDir(), "bridge-nav-keys.log")
	default:
		return v
	}
}
