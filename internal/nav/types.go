// Package nav implements `bridge nav`: a two-screen interactive navigator
// (repo picker -> per-repo dashboard of tmux sessions + worktrees). It is a
// single Bubble Tea program that survives attach/detach via tea.ExecProcess.
package nav

import (
	"github.com/freaxnx01/bridge/internal/core"
	"github.com/freaxnx01/bridge/internal/forge"
)

type screen int

const (
	screenPicker screen = iota
	screenDash
)

type focus int

const (
	focusFilter focus = iota
	focusList
)

type loadState int

const (
	loadPending loadState = iota
	loadOK
	loadErr
)

// repoRow is one picker row. Remote rows carry a forge.RepoRef for clone.
type repoRow struct {
	label  string // display label, e.g. github/public/bridge
	repo   core.Repo
	remote *forge.RepoRef // non-nil => clone-on-select
}

// sessionRow is one global active-session row on the picker.
type sessionRow struct {
	slotID       string
	repoLabel    string
	worktree     string
	agent        string
	state        string // attached | detached
	lastAccessed string
}

// dashRow is one Screen-2 row: a worktree, optionally with a live session.
type dashRow struct {
	worktree     string // basename
	branch       string
	path         string
	agent        string // "" when no live session
	slotID       string // "" when no live session
	state        string // attached | detached | ""
	lastAccessed string // "" when no live session
	hasSession   bool
	dirty        dirtyInfo
	dirtyState   loadState
}

// dirtyInfo is the async git status for a worktree.
type dirtyInfo struct {
	modified int
	ahead    int
	clean    bool
}

// newWorktreeModal is the inline create-worktree input state.
type newWorktreeModal struct {
	name string
	err  string
}

// Config is everything nav needs, injected by cmd/bridge so internal/nav
// stays free of cmd-layer code (e.g. cloneRemoteRepo).
type Config struct {
	ReposRoots   []string
	RemoteCache  string // path to remote.list
	SlotsPath    string // path to slots.json
	DefaultAgent string // BRIDGE_DEFAULT_AGENT ("" => no auto-launch agent; nav uses claude)
	AgentArgs    []string
	Clone        func(ref forge.RepoRef) (core.Repo, error)
	Once         bool
}

// --- messages ---

type reposMsg struct{ rows []repoRow }
type sessionsMsg struct{ rows []sessionRow }
type remoteMsg struct{ rows []repoRow }
type remoteErrMsg struct{ err error }
type dashRowsMsg struct{ rows []dashRow }
type dirtyMsg struct {
	path string
	info dirtyInfo
	err  error
}
type cloneDoneMsg struct {
	repo core.Repo
	err  error
}
type wtCreatedMsg struct {
	row dashRow
	err error
}
type execDoneMsg struct{ err error }
