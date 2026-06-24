// Package nav implements `bridge nav`: a two-screen interactive navigator
// (repo picker -> per-repo dashboard of tmux sessions + worktrees). It is a
// single Bubble Tea program that survives attach/detach via tea.ExecProcess.
package nav

import (
	"context"

	"github.com/freaxnx01/bridge/internal/core"
	"github.com/freaxnx01/bridge/internal/forge"
	"github.com/freaxnx01/bridge/internal/overview"
)

type screen int

const (
	screenPicker screen = iota
	screenDash
	screenOverview
)

type focus int

const (
	focusFilter focus = iota
	focusList
	focusSessions
)

// dashFocus is which Screen-2 pane has the keyboard: the worktree list (left)
// or one of the three right-column backlog panes (open issues, ideas, todos).
// Tab cycles through all four in order (see Model.dashPaneCycle); when the
// worktree list is focused the right column shows worktree Details instead.
type dashFocus int

const (
	dashFocusWorktrees dashFocus = iota
	dashFocusIssues
	dashFocusIdeas
	dashFocusTodos
)

type loadState int

const (
	loadPending loadState = iota
	loadOK
	loadErr
	loadPartial // some sources loaded, at least one failed; fresh rows still shown
)

// repoRow is one picker row. Remote rows carry a forge.RepoRef for clone.
type repoRow struct {
	label      string // display label, e.g. github/public/bridge
	repo       core.Repo
	remote     *forge.RepoRef // non-nil => clone-on-select
	issueCount int
	issueState loadState
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
	// displayLabel overrides the default "<repo> [<wt>]" session name when set
	// (issue-launched worktrees carry "#123 [<short title>]"); "" => default.
	displayLabel string
}

// issueRow is one open-issue row in the Screen-2 issues pane.
type issueRow struct {
	number int
	title  string
}

// noteFile is one repo-root backlog file (ideas.md / TODO.md) surfaced on the
// dashboard. lines holds the UTF-8 text preview (empty when binary); truncated
// marks a file clipped at notesMaxBytes; binary marks non-text content.
type noteFile struct {
	name      string
	lines     []string
	truncated bool
	binary    bool
}

// dirtyInfo is the async git status for a worktree. ahead/behind come from the
// `git status -sb` upstream header; behind is only accurate after a fetch.
// noUpstream marks a branch with no remote tracking (ahead/behind meaningless).
type dirtyInfo struct {
	modified   int
	ahead      int
	behind     int
	noUpstream bool
	clean      bool
}

// branchInfo is one row of the Branches panel. current marks the selected
// worktree's HEAD ("*" in `git branch`); inWorktree marks a branch checked out
// in some worktree ("+"), the across-worktrees overview signal.
type branchInfo struct {
	name       string
	current    bool
	inWorktree bool
}

// commitInfo is one Recent-commits row: short SHA + subject.
type commitInfo struct {
	sha     string
	subject string
}

// statusFile is one Git-status row: the two-char porcelain XY code + path.
type statusFile struct {
	code string
	path string
}

// worktreeDetails is the lazily-loaded, cached panel data for one worktree,
// keyed by worktree path in Model.details. The zero value has every panel in
// loadPending (loadState's zero value), which is what ensureDetails relies on.
type worktreeDetails struct {
	branches      []branchInfo
	commits       []commitInfo
	status        []statusFile
	branchesState loadState
	commitsState  loadState
	statusState   loadState
}

// newWorktreeModal is the inline create-worktree input state.
type newWorktreeModal struct {
	name string
	err  string
}

type repoModalStep int

const (
	repoModalName repoModalStep = iota
	repoModalForge
)

// newRepoModal is the inline Ctrl+N create-repo state (picker screen).
type newRepoModal struct {
	name     string
	step     repoModalStep
	sel      int // index into repoForgeChoices
	creating bool
	err      string
}

// repoForgeChoices are the forge×visibility options in display order.
var repoForgeChoices = []struct {
	label, forge string
	private      bool
}{
	{"Forgejo · Private", "forgejo", true},
	{"Forgejo · Public", "forgejo", false},
	{"GitHub · Private", "github", true},
	{"GitHub · Public", "github", false},
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
	// CreateRepo creates a repo on the named forge (forgejo|github) at the given
	// visibility, clones it, and returns the local repo. Nil disables Ctrl+N.
	CreateRepo func(name, forgeName string, private bool) (core.Repo, error)
	// NameArgs returns extra leading args that label the launched agent session
	// (e.g. claude's `-n "<repo> [<wt>]"`) and performs any pre-launch setup
	// (the relabel hook). Injected by cmd/bridge so internal/nav stays free of
	// agent-label/hook specifics. Nil => no naming. A non-empty label overrides
	// the default "<repo> [<wt>]" naming (issue launches pass "#123 [<title>]").
	NameArgs func(agent string, repo core.Repo, worktree, label string) []string
	// Version is the vX.Y.Z string shown bottom-right (injected by cmd/bridge).
	Version string
	// Environment is the display label ("Personal" / "Business") shown in the
	// Overview screen title. Empty falls back to "bridge".
	Environment string
	// DebugKeys, when non-empty, is a file path each key press is appended to
	// (set via BRIDGE_NAV_DEBUG) for diagnosing key handling.
	DebugKeys string
	Once      bool
	// FetchIssues fetches open issues for a repo. Nil disables live fetching
	// (cache-only). Injected by cmd/bridge so internal/nav is forge-token-free.
	FetchIssues func(ctx context.Context, forgeName, owner, repo string) ([]forge.Issue, error)
	// FetchRemote re-queries every configured forge and returns the owned
	// repos, also refreshing the on-disk cache. Nil disables live refresh: the
	// r key falls back to re-reading the cache.
	FetchRemote func(ctx context.Context) ([]forge.RepoRef, error)
	// BuildOverview aggregates this environment's cross-repo Snapshot (issues +
	// roadmap + file captures). Nil disables the Overview screen. Injected by
	// cmd/bridge so internal/nav stays forge-token-free.
	BuildOverview func(ctx context.Context) (overview.Snapshot, error)
	// IssueCacheDir is the directory for per-repo issue cache files.
	// Empty disables caching (and, combined with nil FetchIssues, skips all issue loading).
	IssueCacheDir string
}

// --- messages ---

type reposMsg struct{ rows []repoRow }
type sessionsMsg struct{ rows []sessionRow }
type remoteMsg struct{ rows []repoRow }

// remoteErrMsg reports a failed remote refresh. rows carries any partial fresh
// rows that loaded before the failure (e.g. one forge 401'd while others
// succeeded); empty rows means a total failure that keeps the cached rows.
type remoteErrMsg struct {
	err  error
	rows []repoRow
}
type issueCountMsg struct {
	key   string // forge/owner/name
	count int
}
type repoIssuesMsg struct{ issues []forge.Issue }
type overviewMsg struct{ snap overview.Snapshot }
type overviewErrMsg struct{ err error }
type notesMsg struct{ notes []noteFile }
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
type repoCreatedMsg struct {
	repo core.Repo
	err  error
}
type execDoneMsg struct{ err error }
type slotRegisteredMsg struct{}
type fetchDoneMsg struct{ err error }
type branchesMsg struct {
	path     string
	branches []branchInfo
	err      error
}
type commitsMsg struct {
	path    string
	commits []commitInfo
	err     error
}
type statusMsg struct {
	path  string
	files []statusFile
	err   error
}
