// Package launch resolves a repo/worktree/agent into a launch Target and
// starts it — the shared core of `bridge <repo>` (preflight), `bridge launch`
// and `bridge bot`. The cmd layer keeps flag parsing, exit codes and printing.
package launch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/freaxnx01/bridge/internal/agents"
	"github.com/freaxnx01/bridge/internal/core"
	"github.com/freaxnx01/bridge/internal/hooks"
	"github.com/freaxnx01/bridge/internal/store"
	"github.com/freaxnx01/bridge/internal/syncer"
	worktreepkg "github.com/freaxnx01/bridge/internal/worktree"
)

// Deps is everything launch needs from the cmd layer. Zero-value fields fall
// back to safe defaults (see the accessor methods).
type Deps struct {
	// Repos lists the discoverable repos (cmd: reposWithMeta).
	Repos func() ([]core.Repo, error)
	// CacheRoot holds "mru" and "slots.json"; "" skips both writes.
	CacheRoot string
	// Live lists live sessions (cmd: loadSessions, which honours
	// BRIDGE_TMUX_FIXTURE). Nil => nothing is live.
	Live func() ([]core.Session, error)
	// Getenv reads BRIDGE_DEFAULT_AGENT(_ARGS) and BRIDGE_NO_SYNC. Nil => os.Getenv.
	Getenv func(string) string
	// Stderr receives the one-line notes bridge has always printed. Nil => discarded.
	Stderr io.Writer
	// Exec runs argv and returns its combined output. Nil => exec.CommandContext.
	Exec func(ctx context.Context, argv []string) ([]byte, error)
	// PollEvery is WaitRCURL's poll interval. 0 => 500ms.
	PollEvery time.Duration
}

func (d Deps) getenv(k string) string {
	if d.Getenv == nil {
		return os.Getenv(k)
	}
	return d.Getenv(k)
}

func (d Deps) stderr() io.Writer {
	if d.Stderr == nil {
		return io.Discard
	}
	return d.Stderr
}

func (d Deps) run(ctx context.Context, argv []string) ([]byte, error) {
	if d.Exec != nil {
		return d.Exec(ctx, argv)
	}
	return exec.CommandContext(ctx, argv[0], argv[1:]...).CombinedOutput()
}

// Target is everything needed to start (or reattach) an agent session.
type Target struct {
	Repo            core.Repo
	Worktree        string
	WorkDir         string
	WorktreeCreated bool
	Slot            string
	AgentName       string
	Spec            agents.AgentSpec
	HasAgent        bool
	// Label overrides the claude display name ("" => DisplayName(Repo, Worktree)).
	// Issue launches set "#123 [<title>]".
	Label string
}

// RepoLookupError marks an unknown or ambiguous repo name (cmd: exit 2).
type RepoLookupError struct{ Msg string }

func (e RepoLookupError) Error() string { return e.Msg }

// AgentLookupError marks an unknown --agent (cmd: exit 2).
type AgentLookupError struct{ Cause error }

func (e AgentLookupError) Error() string { return "bridge: " + e.Cause.Error() }
func (e AgentLookupError) Unwrap() error { return e.Cause }

// RCAgentError marks a Remote Control request for a non-claude agent (cmd: exit 2).
type RCAgentError struct{ Agent string }

func (e RCAgentError) Error() string {
	return fmt.Sprintf("bridge: --rc needs the claude agent (got %s)", e.Agent)
}

// FindRepoByName returns the first repo whose Name equals name (case-insensitive).
func FindRepoByName(repos []core.Repo, name string) (core.Repo, bool) {
	needle := strings.ToLower(name)
	for _, r := range repos {
		if strings.ToLower(r.Name) == needle {
			return r, true
		}
	}
	return core.Repo{}, false
}

// FindReposByKeyword returns repos matching q (case-insensitive substring) in
// Name, else in Desc or a Topic. Name matches win: if any Name contains q,
// only Name matches are returned.
func FindReposByKeyword(repos []core.Repo, q string) []core.Repo {
	needle := strings.ToLower(q)
	var nameHits, metaHits []core.Repo
	for _, r := range repos {
		if strings.Contains(strings.ToLower(r.Name), needle) {
			nameHits = append(nameHits, r)
			continue
		}
		if strings.Contains(strings.ToLower(r.Desc), needle) {
			metaHits = append(metaHits, r)
			continue
		}
		for _, t := range r.Topics {
			if strings.Contains(strings.ToLower(t), needle) {
				metaHits = append(metaHits, r)
				break
			}
		}
	}
	if len(nameHits) > 0 {
		return nameHits
	}
	return metaHits
}

// DefaultAgent resolves BRIDGE_DEFAULT_AGENT, appending the space-split
// BRIDGE_DEFAULT_AGENT_ARGS. ok=false when unset or unknown.
func DefaultAgent(getenv func(string) string) (agents.AgentSpec, bool) {
	name := getenv("BRIDGE_DEFAULT_AGENT")
	if name == "" {
		return agents.AgentSpec{}, false
	}
	spec, err := agents.Resolve(name)
	if err != nil {
		return agents.AgentSpec{}, false
	}
	if extra := getenv("BRIDGE_DEFAULT_AGENT_ARGS"); extra != "" {
		spec.Args = append(append([]string{}, spec.Args...), strings.Fields(extra)...)
	}
	return spec, true
}

// DisplayName is the claude session name: "<repo>" or "<repo> [<worktree>]".
func DisplayName(repo core.Repo, worktree string) string {
	if worktree != "" {
		return repo.Name + " [" + worktree + "]"
	}
	return repo.Name
}

// WithClaudeName prepends `-n <label>` to a claude spec's args (no-op for
// other agents). Builds a fresh Args slice; the registry spec is never mutated.
func WithClaudeName(spec agents.AgentSpec, label string) agents.AgentSpec {
	if spec.Name != "claude" {
		return spec
	}
	spec.Args = append([]string{"-n", label}, spec.Args...)
	return spec
}

// EnsureRelabel installs the SessionStart[clear] hook that restores label
// after /clear (claude only). Failures are a warning on stderr, never fatal.
func EnsureRelabel(spec agents.AgentSpec, label string, stderr io.Writer) {
	if spec.Name != "claude" {
		return
	}
	if err := hooks.EnsureRelabel(hooks.EffectiveConfigDir(), label); err != nil {
		fmt.Fprintf(stderr, "warning: relabel hook install failed: %v\n", err)
	}
}

// ResolveTarget looks name up (exact name first, then keyword) and builds its Target.
func ResolveTarget(ctx context.Context, d Deps, name, worktree, agentName string) (Target, error) {
	if d.Repos == nil {
		return Target{}, errors.New("launch: Deps.Repos is nil")
	}
	repos, err := d.Repos()
	if err != nil {
		return Target{}, fmt.Errorf("launch: list repos: %w", err)
	}
	repo, ok := FindRepoByName(repos, name)
	if !ok {
		matches := FindReposByKeyword(repos, name)
		switch len(matches) {
		case 1:
			repo = matches[0]
		case 0:
			return Target{}, RepoLookupError{fmt.Sprintf("bridge: unknown repo %q", name)}
		default:
			names := make([]string, len(matches))
			for i, m := range matches {
				names[i] = m.Name
			}
			return Target{}, RepoLookupError{fmt.Sprintf("bridge: %q is ambiguous (%d matches): %s",
				name, len(matches), strings.Join(names, ", "))}
		}
	}
	return TargetFor(ctx, d, repo, worktree, agentName)
}

// TargetFor builds the Target for an already-resolved repo (the bot passes the
// exact repo the user tapped, so same-named repos on two forges never mix).
// It touches the MRU and, with a worktree, resolves or creates it; a git
// failure falls back to the bare <repo>/.worktrees/<wt> path. agentName ""
// uses DefaultAgent.
func TargetFor(ctx context.Context, d Deps, repo core.Repo, worktree, agentName string) (Target, error) {
	if err := ctx.Err(); err != nil {
		return Target{}, err
	}
	if d.CacheRoot != "" {
		_ = store.MRUTouch(filepath.Join(d.CacheRoot, "mru"), repo.Path) // best effort, as before
	}
	t := Target{Repo: repo, Worktree: worktree, WorkDir: repo.Path}
	if worktree != "" {
		if dir, created, werr := worktreepkg.Resolve(worktreepkg.ExecRunner{}, repo.Path, worktree); werr == nil {
			t.WorkDir, t.WorktreeCreated = dir, created
			if created {
				fmt.Fprintf(d.stderr(), "bridge: created worktree %s\n", dir)
			}
		} else {
			t.WorkDir = filepath.Join(repo.Path, ".worktrees", worktree)
			fmt.Fprintf(d.stderr(), "bridge: worktree resolve failed (%v); using %s\n", werr, t.WorkDir)
		}
	}
	if agentName != "" {
		spec, err := agents.Resolve(agentName)
		if err != nil {
			return Target{}, AgentLookupError{err}
		}
		t.Spec, t.AgentName, t.HasAgent = spec, agentName, true
	} else if spec, ok := DefaultAgent(d.getenv); ok {
		t.Spec, t.AgentName, t.HasAgent = spec, spec.Name, true
	}
	t.Slot = core.SlotID(repo.Name, worktree)
	return t, nil
}

// Finalize applies the side effects every agent launch shares: the claude
// `-n` label + relabel hook, the best-effort pre-launch sync (skipped for a
// live slot), and the slot-registry upsert (non-fatal).
func Finalize(ctx context.Context, d Deps, t *Target, noSync bool) {
	label := t.Label
	if label == "" {
		label = DisplayName(t.Repo, t.Worktree)
	}
	t.Spec = WithClaudeName(t.Spec, label)
	EnsureRelabel(t.Spec, label, d.stderr())
	PreLaunchSync(ctx, d, t.WorkDir, t.Slot, noSync)
	if d.CacheRoot == "" {
		return
	}
	if err := core.UpsertSlot(filepath.Join(d.CacheRoot, "slots.json"), core.Slot{
		ID:       t.Slot,
		Repo:     t.Repo.Name,
		Worktree: t.Worktree,
		Agent:    t.AgentName,
		Created:  time.Now().UTC(),
	}); err != nil {
		fmt.Fprintf(d.stderr(), "warning: slot upsert failed: %v\n", err)
	}
}

// PreLaunchSync does a best-effort `git fetch && git pull --ff-only` in dir
// (issue #90). Skipped when noSync, BRIDGE_NO_SYNC, an empty dir, or a live
// slot (never touch the tree under a running session). Never fails a launch.
func PreLaunchSync(ctx context.Context, d Deps, dir, slot string, noSync bool) {
	if noSync || d.getenv("BRIDGE_NO_SYNC") != "" || dir == "" {
		return
	}
	if slot != "" && SessionLive(d, slot) {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	s := &syncer.Syncer{Runner: syncer.ExecRunner{}}
	res := s.SafePull(ctx, dir)
	if res.Skipped != "" && res.Skipped != syncer.SkipNoUpstream {
		fmt.Fprintf(d.stderr(), "bridge: sync skipped (%s)\n", res.Skipped)
	}
}

// SessionLive reports whether slot is among d.Live's sessions.
func SessionLive(d Deps, slot string) bool {
	if d.Live == nil {
		return false
	}
	sessions, err := d.Live()
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
