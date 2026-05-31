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
	"github.com/freaxnx01/bridge/internal/hooks"
	"github.com/freaxnx01/bridge/internal/launcher"
	"github.com/freaxnx01/bridge/internal/shellbridge"
	"github.com/freaxnx01/bridge/internal/store"
	"github.com/freaxnx01/bridge/internal/syncer"
	worktreepkg "github.com/freaxnx01/bridge/internal/worktree"
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

// emitLaunch applies the TERM-fallback to a tmux launch argv (a no-op on
// Windows / when not needed), then emits the exec directive. All tmux launch
// paths go through here so the kitty/terminfo fallback (#104) is uniform.
func emitLaunch(out io.Writer, argv []string) error {
	return shellbridge.EmitExec(out, maybeTermFallback(os.Stderr, argv))
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
	// Clone target uses the primary base (first entry of reposRoots).
	root := reposRoot()
	local, err := discoverAllRoots()
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
	maybePreLaunchSync(repo.Path, slotIDFor(repo, ""), false)
	if spec, ok := resolveDefaultAgent(); ok {
		spec = withClaudeName(spec, repo, "")
		ensureClaudeRelabel(spec, repo, "")
		argv, err := launcher.New().LaunchArgv(slotIDFor(repo, ""), repo.Path, spec)
		if err == nil {
			return emitLaunch(out, argv)
		}
	}
	return shellbridge.EmitCD(out, repo.Path)
}

func preflightPicker(out io.Writer) error {
	repos, err := discoverAllRoots()
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
	maybePreLaunchSync(r.Path, slotIDFor(r, ""), false)
	if spec, ok := resolveDefaultAgent(); ok {
		spec = withClaudeName(spec, r, "")
		ensureClaudeRelabel(spec, r, "")
		argv, err := launcher.New().LaunchArgv(slotIDFor(r, ""), r.Path, spec)
		if err == nil {
			return emitLaunch(out, argv)
		}
	}
	return shellbridge.EmitCD(out, r.Path)
}

// resolveDefaultAgent reads BRIDGE_DEFAULT_AGENT and returns its spec, with
// BRIDGE_DEFAULT_AGENT_ARGS (space-split) appended to Args. Returns ok=false
// when the env var is unset or the agent name is unknown. The args env var
// only applies when the agent comes from the default — an explicit `--agent`
// on the command line uses agents.Resolve directly and doesn't pick up
// default args (so launching `code` doesn't get claude's flags).
func resolveDefaultAgent() (agents.AgentSpec, bool) {
	name := os.Getenv("BRIDGE_DEFAULT_AGENT")
	if name == "" {
		return agents.AgentSpec{}, false
	}
	spec, err := agents.Resolve(name)
	if err != nil {
		return agents.AgentSpec{}, false
	}
	if extra := os.Getenv("BRIDGE_DEFAULT_AGENT_ARGS"); extra != "" {
		spec.Args = append(spec.Args, strings.Fields(extra)...)
	}
	return spec, true
}

func preflightOpen(out io.Writer, args []string) error {
	var name, agentName, worktree string
	var noSync bool
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
		case "--no-sync":
			noSync = true
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

	// Resolve the working directory. With -w/--worktree, consult
	// `git worktree list --porcelain` so an existing worktree is found
	// wherever it lives (`.claude/worktrees/`, `.worktrees/`, a custom path);
	// when none matches, one is created under `<repo>/.worktrees/<wt>`. If the
	// repo isn't a git checkout (or git fails), fall back to the bare
	// `.worktrees/<wt>` convention path.
	workDir := repo.Path
	if worktree != "" {
		if dir, created, werr := worktreepkg.Resolve(worktreepkg.ExecRunner{}, repo.Path, worktree); werr == nil {
			workDir = dir
			if created {
				fmt.Fprintf(os.Stderr, "bridge: created worktree %s\n", dir)
			}
		} else {
			workDir = filepath.Join(repo.Path, ".worktrees", worktree)
			fmt.Fprintf(os.Stderr, "bridge: worktree resolve failed (%v); using %s\n", werr, workDir)
		}
	}

	// Explicit --agent wins. Otherwise fall back to BRIDGE_DEFAULT_AGENT so
	// `bridge <repo>` auto-launches when the user has it configured —
	// matching the picker entry points and the bash bridge's UX.
	var spec agents.AgentSpec
	if agentName != "" {
		spec, err = agents.Resolve(agentName)
		if err != nil {
			fmt.Fprintf(os.Stderr, "bridge: %v\n", err)
			os.Exit(2)
		}
	} else {
		var ok bool
		spec, ok = resolveDefaultAgent()
		if !ok {
			return shellbridge.EmitCD(out, workDir)
		}
		agentName = spec.Name
	}
	spec = withClaudeName(spec, repo, worktree)
	ensureClaudeRelabel(spec, repo, worktree)
	slot := slotIDFor(repo, worktree)
	maybePreLaunchSync(workDir, slot, noSync)
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
	return emitLaunch(out, argv)
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
	return emitLaunch(out, launcher.New().AttachArgv(slot))
}

// slotIDFor produces a deterministic tmux session name.
func slotIDFor(repo core.Repo, worktree string) string {
	return core.SlotID(repo.Name, worktree)
}

// displayName returns the claude session display name for a repo launch:
// "<repo>" normally, "<repo> [<worktree>]" when a worktree is given. Matches
// the bash bridge's label.
func displayName(repo core.Repo, worktree string) string {
	if worktree != "" {
		return repo.Name + " [" + worktree + "]"
	}
	return repo.Name
}

// withClaudeName prepends `-n <displayName>` to a claude spec's args so the
// launched session is named in the picker/terminal title. No-op for non-claude
// agents (only claude has --name). Builds a fresh Args slice so the shared
// registry spec is never mutated.
func withClaudeName(spec agents.AgentSpec, repo core.Repo, worktree string) agents.AgentSpec {
	if spec.Name != "claude" {
		return spec
	}
	spec.Args = append([]string{"-n", displayName(repo, worktree)}, spec.Args...)
	return spec
}

// maybePreLaunchSync does a best-effort `git fetch && git pull --ff-only`
// on dir before launch (issue #90). Honors BRIDGE_NO_SYNC and the per-call
// noSync gate; treats an already-live tmux session for slot as a reattach
// and skips silently. Non-trivial skip reasons surface as a one-line
// stderr banner; success is silent. Never fails the launch.
func maybePreLaunchSync(dir, slot string, noSync bool) {
	if noSync || os.Getenv("BRIDGE_NO_SYNC") != "" {
		return
	}
	if dir == "" {
		return
	}
	if slot != "" && sessionLive(slot) {
		// Reattach: don't touch the working tree under a live session.
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	s := &syncer.Syncer{Runner: syncer.ExecRunner{}}
	res := s.SafePull(ctx, dir)
	if res.Skipped != "" && res.Skipped != syncer.SkipNoUpstream {
		fmt.Fprintf(os.Stderr, "bridge: sync skipped (%s)\n", res.Skipped)
	}
}

func sessionLive(slot string) bool {
	sessions, err := loadSessions()
	if err != nil {
		return false
	}
	for _, s := range sessions {
		if s.SlotID == slot {
			return true
		}
	}
	return false
}

// ensureClaudeRelabel installs the SessionStart[clear] hook for claude
// launches so the display label set via `-n` can be restored via /rename
// after /clear wipes it (#85). Non-claude agents are a no-op. Errors are
// non-fatal: the launch itself is the primary action.
func ensureClaudeRelabel(spec agents.AgentSpec, repo core.Repo, worktree string) {
	if spec.Name != "claude" {
		return
	}
	if err := hooks.EnsureRelabel(hooks.EffectiveConfigDir(), displayName(repo, worktree)); err != nil {
		fmt.Fprintf(os.Stderr, "warning: relabel hook install failed: %v\n", err)
	}
}
