package bot

import (
	"github.com/freaxnx01/bridge/internal/core"
	"github.com/freaxnx01/bridge/internal/forge"
	"github.com/freaxnx01/bridge/internal/navdata"
)

// Event is an input to Update: a Telegram message or tap, or the result of an
// Action the runner executed.
type Event interface{ isEvent() }

// Text is a text message (a /command, a filter or a name).
type Text struct {
	UserID, ChatID int64
	Text           string
}

// Tap is an inline-button tap on message MessageID.
type Tap struct {
	UserID, ChatID int64
	MessageID      int
	Data           string
}

// MessageSent reports the id of the message the runner sent for NewMessage.
type MessageSent struct{ MessageID int }

// ReposLoaded is LoadRepos' result. Err is shown, the lists are still used.
// Refresh echoes LoadRepos.Refresh: only a refresh's result ends the refresh.
type ReposLoaded struct {
	Local, Remote []navdata.RepoEntry
	MRU           []string
	Refresh       bool
	Err           error
}

// DashLoaded is LoadDash's result.
type DashLoaded struct {
	Repo core.Repo
	Rows []navdata.DashRow
}

// StatusLoaded is LoadStatus' result, keyed by worktree path.
type StatusLoaded struct {
	Repo   core.Repo
	Status map[string]RowStatus
}

// FetchDone is Fetch's result.
type FetchDone struct {
	Repo core.Repo
	Err  error
}

// IssuesLoaded is LoadIssues' result.
type IssuesLoaded struct {
	Repo   core.Repo
	Issues []forge.Issue
}

// Started is Start's result.
type Started struct {
	Slot           string
	AlreadyRunning bool
	Exited         bool // the session died before printing a link
	RCURL          string
	Err            error
}

// Stopped is Stop's result.
type Stopped struct {
	Slot string
	Err  error
}

// Cloned is Clone's result.
type Cloned struct {
	Repo core.Repo
	Err  error
}

// RepoCreated is CreateRepo's result.
type RepoCreated struct {
	Repo core.Repo
	Err  error
}

func (Text) isEvent()         {}
func (Tap) isEvent()          {}
func (MessageSent) isEvent()  {}
func (ReposLoaded) isEvent()  {}
func (DashLoaded) isEvent()   {}
func (StatusLoaded) isEvent() {}
func (FetchDone) isEvent()    {}
func (IssuesLoaded) isEvent() {}
func (Started) isEvent()      {}
func (Stopped) isEvent()      {}
func (Cloned) isEvent()       {}
func (RepoCreated) isEvent()  {}

// Action is an output of Update for the runner to execute.
type Action interface{ isAction() }

// NewMessage: send Render(state) as a new message (then feed MessageSent).
type NewMessage struct{}

// Toast: the text to answer the tap's callback query with.
type Toast struct{ Text string }

// Drop: ignore the update (sender not allowlisted); log one line.
type Drop struct{ UserID, ChatID int64 }

// Reply: send Text as a new plain message — the "/nav" hint when the chat has
// no bot message to show a notice on (e.g. after a restart).
type Reply struct{ Text string }

// SendLink: send Text as a separate new message (an RC link → phone notification).
type SendLink struct{ Text string }

// LoadRepos: reload local repos, the remote cache and the MRU; Refresh also
// re-queries the forges (slow).
type LoadRepos struct{ Refresh bool }

// LoadDash: load Repo's dashboard rows.
type LoadDash struct{ Repo core.Repo }

// LoadStatus: read git status for each path.
type LoadStatus struct {
	Repo  core.Repo
	Paths []string
}

// Fetch: git fetch Repo (fresh ahead/behind).
type Fetch struct{ Repo core.Repo }

// LoadIssues: load Repo's open issues (cached).
type LoadIssues struct{ Repo core.Repo }

// Start: start or open a session (Create: make the worktree first). Label
// overrides the session name ("#123 [title]").
type Start struct {
	Repo     core.Repo
	Worktree string
	Label    string
	Create   bool
}

// Stop: kill Slot's tmux session.
type Stop struct{ Slot string }

// Clone: clone a remote-only repo.
type Clone struct{ Ref forge.RepoRef }

// CreateRepo: create, then clone, a new repo.
type CreateRepo struct {
	Name    string
	Forge   string
	Private bool
}

func (NewMessage) isAction() {}
func (Toast) isAction()      {}
func (Drop) isAction()       {}
func (SendLink) isAction()   {}
func (Reply) isAction()      {}
func (LoadRepos) isAction()  {}
func (LoadDash) isAction()   {}
func (LoadStatus) isAction() {}
func (Fetch) isAction()      {}
func (LoadIssues) isAction() {}
func (Start) isAction()      {}
func (Stop) isAction()       {}
func (Clone) isAction()      {}
func (CreateRepo) isAction() {}
