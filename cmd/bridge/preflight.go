package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

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
	args = rewriteLegacyPreflight(args)
	if len(args) == 0 {
		return preflightPicker(out)
	}
	// Picker entry points: bare `-r` / `--refresh` (no other args) → open the
	// picker, optionally refreshing the remote cache first. Restores the bash
	// bridge's interactive UX for `bridge -r`. The text-output `bridge list -r`
	// remains available as an explicit verb.
	if len(args) == 1 && (args[0] == "-r" || args[0] == "--refresh") {
		return preflightPickerWithRemote(out, args[0] == "--refresh")
	}
	head := args[0]
	if head == "sessions" && len(args) >= 2 && args[1] == "attach" {
		slot := ""
		if len(args) >= 3 {
			slot = args[2]
		} else {
			sessions, _ := loadSessions()
			if len(sessions) == 0 {
				fmt.Fprintln(os.Stderr, "bridge: no live sessions to attach to")
				os.Exit(2)
			}
			slot = pickSession(sessions)
			if slot == "" {
				return shellbridge.EmitCancel(out)
			}
		}
		return preflightSessionsAttach(out, slot)
	}
	if head == "open" {
		return preflightOpen(out, args[1:])
	}
	if !knownVerbs[head] && !strings.HasPrefix(head, "-") {
		return preflightOpen(out, args)
	}
	return shellbridge.EmitNoop(out)
}

// preflightPickerWithRemote runs the combined picker over local repos plus
// any remote refs in the cache. With `--refresh` it warms the remote cache
// first (bounded by 5s so a slow forge can't stall the picker).
//
// Bare `-r` does NOT trigger a network call — it shows whatever the cache
// holds. On selection of a remote-only entry the binary shells out to
// `direnv exec <parent> git clone <url>` to acquire credentials, then emits
// a directive as if the repo were local. See #54.
func preflightPickerWithRemote(out io.Writer, refresh bool) error {
	root := reposRoot()
	local, err := core.DiscoverRepos(root)
	if err != nil {
		return err
	}
	if refresh {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if _, ferr := loadOrFetchRemote(ctx, local, true); ferr != nil {
			fmt.Fprintf(os.Stderr, "warning: remote refresh failed (continuing with cached picker): %v\n", ferr)
		}
		cancel()
	}
	remote := readRemoteCache()

	choice, ok, err := pickRepoOrRemote(local, remote)
	if err != nil {
		return err
	}
	if !ok {
		return shellbridge.EmitCancel(out)
	}

	var repo core.Repo
	switch {
	case choice.Local != nil:
		repo = *choice.Local
	case choice.Remote != nil:
		cloned, cerr := cloneRemoteRepo(*choice.Remote)
		if cerr != nil {
			fmt.Fprintf(os.Stderr, "bridge: %v\n", cerr)
			os.Exit(2)
		}
		repo = repoFromClonedRef(root, *choice.Remote, cloned)
	default:
		return shellbridge.EmitCancel(out)
	}

	_ = store.MRUTouch(filepath.Join(cacheRoot(), "mru"), repo.Path)
	if agent := os.Getenv("BRIDGE_DEFAULT_AGENT"); agent != "" {
		spec, err := agents.Resolve(agent)
		if err == nil {
			argv, err := launcher.New().LaunchArgv(slotIDFor(repo, ""), repo.Path, spec)
			if err == nil {
				return shellbridge.EmitExec(out, argv)
			}
		}
	}
	return shellbridge.EmitCD(out, repo.Path)
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
		return shellbridge.EmitCancel(out)
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
	var name, agentName, worktree string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--agent":
			if i+1 < len(args) {
				agentName = args[i+1]
				i++
			}
		case "-w", "--worktree":
			if i+1 < len(args) {
				worktree = args[i+1]
				i++
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
	repos, err := reposWithMeta()
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

	// Resolve the working directory. With -w/--worktree, use the bash bridge
	// convention `<repo.Path>/.worktrees/<wt>`. A future enhancement could
	// consult `git worktree list --porcelain` for non-default layouts.
	workDir := repo.Path
	if worktree != "" {
		workDir = filepath.Join(repo.Path, ".worktrees", worktree)
	}

	if agentName == "" {
		return shellbridge.EmitCD(out, workDir)
	}
	spec, err := agents.Resolve(agentName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "bridge: %v\n", err)
		os.Exit(2)
	}
	slot := slotIDFor(repo, worktree)
	// Record the slot in the registry. Non-fatal on failure — emitting the
	// exec directive is still the right thing to do.
	if err := core.UpsertSlot(filepath.Join(cacheRoot(), "slots.json"), core.Slot{
		ID:       slot,
		Repo:     repo.Name,
		Worktree: worktree,
		Agent:    agentName,
		Created:  time.Now().UTC(),
	}); err != nil {
		fmt.Fprintf(os.Stderr, "warning: slot upsert failed: %v\n", err)
	}
	l := launcher.New()
	var argv []string
	if os.Getenv("TMUX") != "" {
		// Already inside tmux: nesting `tmux new-session -A` fails, so use the
		// nested launcher that creates-detached-then-switches the current client.
		argv, err = l.LaunchArgvNested(slot, workDir, spec)
	} else {
		argv, err = l.LaunchArgv(slot, workDir, spec)
	}
	if err != nil {
		return err
	}
	return shellbridge.EmitExec(out, argv)
}

func preflightSessionsAttach(out io.Writer, slot string) error {
	sessions, err := loadSessions()
	if err != nil {
		return err
	}
	found := false
	for _, s := range sessions {
		if s.SlotID == slot {
			found = true
			break
		}
	}
	if !found {
		fmt.Fprintf(os.Stderr, "bridge: no session %q\n", slot)
		os.Exit(2)
	}
	return shellbridge.EmitExec(out, launcher.New().AttachArgv(slot))
}

// slotIDFor produces a deterministic tmux session name.
func slotIDFor(repo core.Repo, worktree string) string {
	id := repo.Name
	if worktree != "" {
		id += "-wt-" + worktree
	}
	return id
}
