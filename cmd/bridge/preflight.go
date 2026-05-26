package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/freaxnx01/bridge/internal/agents"
	"github.com/freaxnx01/bridge/internal/core"
	"github.com/freaxnx01/bridge/internal/launcher"
	"github.com/freaxnx01/bridge/internal/shellbridge"
	"github.com/freaxnx01/bridge/internal/store"
)

var preflightCmd = &cobra.Command{
	Use:                "__preflight [user-args...]",
	Short:              "internal: emit a shell directive for the shim",
	Hidden:             true,
	DisableFlagParsing: true,
	RunE:               runPreflight,
}

func init() {
	rootCmd.AddCommand(preflightCmd)
}

func runPreflight(cmd *cobra.Command, args []string) error {
	return dispatchPreflight(cmd.OutOrStdout(), args)
}

func dispatchPreflight(out io.Writer, args []string) error {
	if len(args) == 0 {
		return preflightPicker(out)
	}
	head := args[0]
	if head == "open" {
		return preflightOpen(out, args[1:])
	}
	if !knownVerbs[head] && !strings.HasPrefix(head, "-") {
		return preflightOpen(out, args)
	}
	return shellbridge.EmitNoop(out)
}

func preflightPicker(out io.Writer) error {
	repos, err := core.DiscoverRepos(reposRoot())
	if err != nil {
		return err
	}
	r, ok, err := pickRepo(repos)
	if err != nil {
		return err
	}
	if !ok {
		return shellbridge.EmitNoop(out)
	}
	_ = store.MRUTouch(filepath.Join(cacheRoot(), "mru"), r.Path)
	if agent := os.Getenv("BRIDGE_DEFAULT_AGENT"); agent != "" {
		spec, err := agents.Resolve(agent)
		if err == nil {
			argv, err := launcher.New().LaunchArgv(slotIDFor(r, ""), r.Path, spec)
			if err == nil {
				return shellbridge.EmitExec(out, argv)
			}
		}
	}
	return shellbridge.EmitCD(out, r.Path)
}

func preflightOpen(out io.Writer, args []string) error {
	var name, agentName string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--agent":
			if i+1 < len(args) {
				agentName = args[i+1]
				i++
			}
		case "-w", "--worktree":
			if i+1 < len(args) {
				i++ // skip value
			}
		case "--rc", "--remote-control", "--json":
			// ignore in preflight
		default:
			if name == "" && !strings.HasPrefix(args[i], "-") {
				name = args[i]
			}
		}
	}
	if name == "" {
		return shellbridge.EmitNoop(out)
	}
	repos, err := core.DiscoverRepos(reposRoot())
	if err != nil {
		return err
	}
	repo, ok := findRepoByName(repos, name)
	if !ok {
		matches := findReposByKeyword(repos, name)
		if len(matches) == 1 {
			repo = matches[0]
		} else {
			fmt.Fprintf(os.Stderr, "bridge: unknown repo %q\n", name)
			os.Exit(2)
		}
	}
	_ = store.MRUTouch(filepath.Join(cacheRoot(), "mru"), repo.Path)

	if agentName == "" {
		return shellbridge.EmitCD(out, repo.Path)
	}
	spec, err := agents.Resolve(agentName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "bridge: %v\n", err)
		os.Exit(2)
	}
	slot := slotIDFor(repo, "")
	l := launcher.New()
	argv, err := l.LaunchArgv(slot, repo.Path, spec)
	if err != nil {
		return err
	}
	return shellbridge.EmitExec(out, argv)
}

// slotIDFor produces a deterministic tmux session name.
func slotIDFor(repo core.Repo, worktree string) string {
	id := repo.Name
	if worktree != "" {
		id += "-wt-" + worktree
	}
	return id
}
