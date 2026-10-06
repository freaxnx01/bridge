package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/freaxnx01/bridge/internal/agents"
	"github.com/freaxnx01/bridge/internal/core"
	"github.com/freaxnx01/bridge/internal/store"
	worktreepkg "github.com/freaxnx01/bridge/internal/worktree"
)

// launchTarget is everything needed to start (or reattach) an agent session
// for a repo — shared by `bridge <repo>` (preflightOpen, which emits an exec
// directive for the shim) and `bridge launch` (which runs tmux detached).
type launchTarget struct {
	Repo            core.Repo
	Worktree        string
	WorkDir         string
	WorktreeCreated bool
	Slot            string
	AgentName       string
	Spec            agents.AgentSpec
	HasAgent        bool
}

// errRepoLookup marks unknown/ambiguous repo names; callers print it and exit 2.
type errRepoLookup struct{ msg string }

func (e errRepoLookup) Error() string { return e.msg }

func resolveLaunchTarget(name, worktree, agentName string) (launchTarget, error) {
	repos, err := reposWithMeta()
	if err != nil {
		return launchTarget{}, err
	}
	repo, ok := findRepoByName(repos, name)
	if !ok {
		matches := findReposByKeyword(repos, name)
		switch len(matches) {
		case 1:
			repo = matches[0]
		case 0:
			return launchTarget{}, errRepoLookup{fmt.Sprintf("bridge: unknown repo %q", name)}
		default:
			names := make([]string, len(matches))
			for i, m := range matches {
				names[i] = m.Name
			}
			return launchTarget{}, errRepoLookup{fmt.Sprintf("bridge: %q is ambiguous (%d matches): %s",
				name, len(matches), strings.Join(names, ", "))}
		}
	}
	_ = store.MRUTouch(filepath.Join(cacheRoot(), "mru"), repo.Path)

	t := launchTarget{Repo: repo, Worktree: worktree, WorkDir: repo.Path}
	// With -w, consult `git worktree list --porcelain` so an existing worktree
	// is found wherever it lives; otherwise create `<repo>/.worktrees/<wt>`.
	// Non-git repo / git failure falls back to the bare convention path.
	if worktree != "" {
		if dir, created, werr := worktreepkg.Resolve(worktreepkg.ExecRunner{}, repo.Path, worktree); werr == nil {
			t.WorkDir, t.WorktreeCreated = dir, created
			if created {
				fmt.Fprintf(os.Stderr, "bridge: created worktree %s\n", dir)
			}
		} else {
			t.WorkDir = filepath.Join(repo.Path, ".worktrees", worktree)
			fmt.Fprintf(os.Stderr, "bridge: worktree resolve failed (%v); using %s\n", werr, t.WorkDir)
		}
	}

	// Explicit --agent wins; otherwise BRIDGE_DEFAULT_AGENT (+ its args).
	if agentName != "" {
		spec, err := agents.Resolve(agentName)
		if err != nil {
			return launchTarget{}, fmt.Errorf("bridge: %w", err)
		}
		t.Spec, t.AgentName, t.HasAgent = spec, agentName, true
	} else if spec, ok := resolveDefaultAgent(); ok {
		t.Spec, t.AgentName, t.HasAgent = spec, spec.Name, true
	}
	t.Slot = slotIDFor(repo, worktree)
	return t, nil
}

// finalizeLaunch applies the side effects every agent launch shares: the
// claude `-n` display label + relabel hook, the best-effort pre-launch sync
// (skipped for a live slot), and the slot-registry upsert (non-fatal).
func finalizeLaunch(t *launchTarget, noSync bool) {
	t.Spec = withClaudeName(t.Spec, t.Repo, t.Worktree)
	ensureClaudeRelabel(t.Spec, t.Repo, t.Worktree)
	maybePreLaunchSync(t.WorkDir, t.Slot, noSync)
	if err := core.UpsertSlot(filepath.Join(cacheRoot(), "slots.json"), core.Slot{
		ID:       t.Slot,
		Repo:     t.Repo.Name,
		Worktree: t.Worktree,
		Agent:    t.AgentName,
		Created:  time.Now().UTC(),
	}); err != nil {
		fmt.Fprintf(os.Stderr, "warning: slot upsert failed: %v\n", err)
	}
}
