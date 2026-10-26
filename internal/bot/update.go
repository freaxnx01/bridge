package bot

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/freaxnx01/bridge/internal/core"
	"github.com/freaxnx01/bridge/internal/navdata"
)

const (
	outdatedText     = "⚠ outdated — /nav"
	invalidNameText  = "⚠ invalid name — use A-Z a-z 0-9 . _ - (max 40)"
	busyRefreshRepos = "⏳ refreshing repos…"
	noticeRefreshing = "⏳ refreshing…"
	hintNav          = "send /nav to open the picker"
	unknownCommand   = "⚠ unknown command — /nav"
)

// Config is the bot's static configuration.
type Config struct {
	// Allow is the set of Telegram user ids the bot answers. Everyone else —
	// including other members of a group the bot is in — is dropped.
	Allow map[int64]bool
}

// Allowed reports whether userID is allowlisted.
func (c Config) Allowed(userID int64) bool { return c.Allow[userID] }

// Update applies one event to a chat's state and returns the new state plus
// the actions to execute. It is pure.
func Update(cfg Config, s State, ev Event) (State, []Action) {
	switch e := ev.(type) {
	case Text:
		if !cfg.Allowed(e.UserID) {
			return s, []Action{Drop{UserID: e.UserID, ChatID: e.ChatID}}
		}
		return onText(s, e)
	case Tap:
		if !cfg.Allowed(e.UserID) {
			return s, []Action{Drop{UserID: e.UserID, ChatID: e.ChatID}}
		}
		return onTap(s, e)
	case MessageSent:
		s.MessageID = e.MessageID
		return s, nil
	case ReposLoaded:
		return onReposLoaded(s, e), nil
	case DashLoaded:
		return onDashLoaded(s, e)
	case StatusLoaded:
		return onStatusLoaded(s, e), nil
	case FetchDone:
		return onFetchDone(s, e)
	case IssuesLoaded:
		return onIssuesLoaded(s, e), nil
	case Started:
		return onStarted(s, e)
	case Stopped:
		return onStopped(s, e)
	case Cloned:
		return onOpened(s, e.Repo, e.Err)
	case RepoCreated:
		s.Prompt, s.NewRepoName = PromptNone, ""
		return onOpened(s, e.Repo, e.Err)
	}
	return s, nil
}

func outdated() []Action { return []Action{Toast{Text: outdatedText}} }

// command returns the lower-cased /command of text ("/nav@bridge_bot x" →
// "/nav"), or "" when text is not a command.
func command(text string) string {
	if !strings.HasPrefix(text, "/") {
		return ""
	}
	cmd, _, _ := strings.Cut(text, " ")
	cmd, _, _ = strings.Cut(cmd, "@")
	return strings.ToLower(cmd)
}

func onText(s State, e Text) (State, []Action) {
	txt := strings.TrimSpace(e.Text)
	switch command(txt) {
	case "":
	case "/nav", "/start":
		// A fresh picker in a new message; repo lists and a running action survive.
		return State{
			Epoch: s.Epoch + 1, Busy: s.Busy, Fetching: s.Fetching,
			Local: s.Local, Remote: s.Remote, MRU: s.MRU, ReposLoaded: s.ReposLoaded,
		}, []Action{NewMessage{}, LoadRepos{}}
	default:
		return hint(s, unknownCommand)
	}
	switch s.Prompt {
	case PromptFilter:
		s.Filter = navdata.TruncRunes(txt, MaxFilterRunes)
		s.Prompt, s.Page, s.Notice = PromptNone, 0, ""
		s.Epoch++
		return s, nil
	case PromptNewRepoName:
		if !ValidName(txt) {
			s.Notice = invalidNameText
			return s, nil
		}
		s.NewRepoName, s.Prompt, s.Notice = txt, PromptNewRepoForge, ""
		s.Epoch++
		return s, nil
	case PromptNewWorktree:
		if !ValidName(txt) {
			s.Notice = invalidNameText
			return s, nil
		}
		if s.Busy != "" {
			s.Notice = "⏳ still working — try again when done"
			return s, nil
		}
		s.Prompt, s.Notice = PromptNone, ""
		s.Busy = "⏳ creating worktree " + txt + "…"
		s.Epoch++
		return s, []Action{Start{Repo: s.Repo, Worktree: txt, Create: true}}
	}
	return hint(s, hintNav)
}

// hint shows msg as the screen's notice, or — when the chat has no bot
// message yet (e.g. after a restart) — sends it as a new message.
func hint(s State, msg string) (State, []Action) {
	if s.MessageID == 0 {
		return s, []Action{Reply{Text: msg}}
	}
	s.Notice = msg
	return s, nil
}

func onTap(s State, e Tap) (State, []Action) {
	d, ok := parseData(e.Data)
	if !ok || s.MessageID == 0 || e.MessageID != s.MessageID || d.epoch != s.Epoch {
		return s, outdated()
	}
	if d.verb == "nop" {
		return s, nil
	}
	if s.Busy != "" {
		return s, []Action{Toast{Text: "⏳ still working: " + strings.TrimPrefix(s.Busy, "⏳ ")}}
	}
	s.Notice = ""
	switch d.verb {
	case "no":
		s.Confirm, s.Prompt, s.NewRepoName = nil, PromptNone, ""
		s.Epoch++
		return s, nil
	case "ok":
		return onConfirm(s, d)
	}
	switch s.Screen {
	case ScreenDash:
		return onDashTap(s, d)
	case ScreenIssues:
		return onIssuesTap(s, d)
	default:
		return onPickerTap(s, d)
	}
}

func onPickerTap(s State, d tapData) (State, []Action) {
	items := s.PickerItems()
	switch d.verb {
	case "r":
		i, ok := d.intArg(0)
		if !ok || i < 0 || i >= len(items) {
			return s, outdated()
		}
		if items[i].Entry.Remote != nil {
			s.Confirm = &Confirm{Kind: ConfirmClone, Index: i}
			s.Epoch++
			return s, nil
		}
		return openDash(s, items[i].Entry.Repo)
	case "pg":
		delta, ok := d.intArg(0)
		if !ok {
			return s, outdated()
		}
		s.Page = clamp(s.Page+delta, 0, pageCount(len(items), PageSize)-1)
		return s, nil
	case "fg":
		s.Forge = nextForge(s.Forge, navdata.PresentForges(s.Local, s.Remote))
		s.Page = 0
		s.Epoch++
		return s, nil
	case "f":
		s.Prompt = PromptFilter
		s.Epoch++
		return s, nil
	case "fx":
		s.Filter, s.Page = "", 0
		s.Epoch++
		return s, nil
	case "nr":
		s.Prompt, s.NewRepoName = PromptNewRepoName, ""
		s.Epoch++
		return s, nil
	case "nc":
		i, ok := d.intArg(0)
		if !ok || s.Prompt != PromptNewRepoForge || i < 0 || i >= len(navdata.RepoChoices()) {
			return s, outdated()
		}
		s.Confirm = &Confirm{Kind: ConfirmNewRepo, Index: i}
		s.Epoch++
		return s, nil
	case "rf":
		s.Busy = busyRefreshRepos
		return s, []Action{LoadRepos{Refresh: true}}
	}
	return s, outdated()
}

// nextForge cycles All → each present forge → All.
func nextForge(cur string, present []navdata.ForgeOpt) string {
	if len(present) <= 1 {
		return ""
	}
	keys := []string{""}
	for _, o := range present {
		keys = append(keys, o.Key)
	}
	for i, k := range keys {
		if k == cur {
			return keys[(i+1)%len(keys)]
		}
	}
	return ""
}

func openDash(s State, repo core.Repo) (State, []Action) {
	s.Screen = ScreenDash
	s.Repo = repo
	s.Rows, s.Status = nil, nil
	s.Issues, s.IssuesLoaded, s.IssuePage = nil, false, 0
	s.Prompt, s.Confirm = PromptNone, nil
	s.Epoch++
	return s, []Action{LoadDash{Repo: repo}}
}

func onDashTap(s State, d tapData) (State, []Action) {
	switch d.verb {
	case "w":
		i, ok := d.intArg(0)
		if !ok || i < 0 || i >= len(s.Rows) {
			return s, outdated()
		}
		row := s.Rows[i]
		s.Busy = "⏳ starting " + rowName(row) + "…"
		return s, []Action{Start{Repo: s.Repo, Worktree: row.Worktree, Label: row.DisplayLabel}}
	case "x":
		i, ok := d.intArg(0)
		if !ok || i < 0 || i >= len(s.Rows) || !s.Rows[i].HasSession {
			return s, outdated()
		}
		s.Confirm = &Confirm{Kind: ConfirmStop, Index: i}
		s.Epoch++
		return s, nil
	case "nw":
		s.Prompt = PromptNewWorktree
		s.Epoch++
		return s, nil
	case "is":
		s.Screen, s.IssuePage = ScreenIssues, 0
		s.Epoch++
		return s, nil
	case "rf":
		if s.Fetching[s.Repo.Path] {
			// The last refresh's git fetch still runs; don't pile up another.
			return s, []Action{Toast{Text: "⏳ still working: fetching " + s.Repo.Name + "…"}}
		}
		s.Notice = noticeRefreshing
		return s, []Action{LoadDash{Repo: s.Repo}}
	case "bk":
		s.Screen, s.Prompt, s.Confirm = ScreenPicker, PromptNone, nil
		s.Epoch++
		return s, []Action{LoadRepos{}}
	}
	return s, outdated()
}

func onIssuesTap(s State, d tapData) (State, []Action) {
	switch d.verb {
	case "i":
		i, ok := d.intArg(0)
		if !ok || i < 0 || i >= len(s.Issues) {
			return s, outdated()
		}
		s.Confirm = &Confirm{Kind: ConfirmIssue, Index: i}
		s.Epoch++
		return s, nil
	case "ip":
		delta, ok := d.intArg(0)
		if !ok {
			return s, outdated()
		}
		s.IssuePage = clamp(s.IssuePage+delta, 0, pageCount(len(s.Issues), IssuePageSize)-1)
		return s, nil
	case "bk":
		s.Screen = ScreenDash
		s.Epoch++
		return s, nil
	}
	return s, outdated()
}

// onConfirm handles ✅ "ok:<kind>:<index>", which must match the pending Confirm.
func onConfirm(s State, d tapData) (State, []Action) {
	i, ok := d.intArg(1)
	if s.Confirm == nil || len(d.args) != 2 || d.args[0] != string(s.Confirm.Kind) || !ok || i != s.Confirm.Index {
		return s, outdated()
	}
	var act Action
	busy := ""
	switch s.Confirm.Kind {
	case ConfirmClone:
		items := s.PickerItems()
		if i >= len(items) || items[i].Entry.Remote == nil {
			return s, outdated()
		}
		ref := *items[i].Entry.Remote
		act, busy = Clone{Ref: ref}, "⏳ cloning "+ref.Name+"…"
	case ConfirmNewRepo:
		choices := navdata.RepoChoices()
		if i >= len(choices) || s.NewRepoName == "" {
			return s, outdated()
		}
		ch := choices[i]
		act = CreateRepo{Name: s.NewRepoName, Forge: ch.Forge, Private: ch.Private}
		busy = "⏳ creating " + s.NewRepoName + " (" + ch.Label + ")…"
	case ConfirmStop:
		if i >= len(s.Rows) || !s.Rows[i].HasSession {
			return s, outdated()
		}
		slot := s.Rows[i].SlotID
		act, busy = Stop{Slot: slot}, "⏳ stopping "+slot+"…"
	case ConfirmIssue:
		if i >= len(s.Issues) {
			return s, outdated()
		}
		is := s.Issues[i]
		wt, label := navdata.IssueWorktreeName(is.Number, is.Title)
		act = Start{Repo: s.Repo, Worktree: wt, Label: label, Create: true}
		busy = fmt.Sprintf("⏳ starting #%d…", is.Number)
	default:
		return s, outdated()
	}
	s.Confirm, s.Busy = nil, busy
	s.Epoch++
	return s, []Action{act}
}

func onReposLoaded(s State, e ReposLoaded) State {
	if e.Refresh && s.Busy == busyRefreshRepos {
		s.Busy = ""
	}
	var before []PickerItem
	if s.ReposLoaded {
		before = s.PickerItems()
	}
	wasLoaded := s.ReposLoaded
	if e.Err != nil {
		s.Notice = "⚠ " + shortErr(e.Err)
	}
	s.Local, s.Remote, s.MRU, s.ReposLoaded = e.Local, e.Remote, e.MRU, true
	present := false
	for _, o := range navdata.PresentForges(s.Local, s.Remote) {
		present = present || o.Key == s.Forge
	}
	if !present {
		s.Forge = ""
	}
	s.Page = clamp(s.Page, 0, pageCount(len(s.PickerItems()), PageSize)-1)
	if s.Screen == ScreenPicker && (!wasLoaded || !reflect.DeepEqual(before, s.PickerItems())) {
		// Indexes moved: a pending clone confirm may now point elsewhere.
		if s.Confirm != nil && s.Confirm.Kind == ConfirmClone {
			s.Confirm = nil
		}
		s.Epoch++
	}
	return s
}

// current reports whether a result for repo still belongs on screen.
func (s State) current(repo core.Repo) bool {
	return s.Screen != ScreenPicker && repo.Path == s.Repo.Path
}

func rowPaths(rows []navdata.DashRow) []string {
	paths := make([]string, 0, len(rows))
	for _, r := range rows {
		paths = append(paths, r.Path)
	}
	return paths
}

func onDashLoaded(s State, e DashLoaded) (State, []Action) {
	if !s.current(e.Repo) {
		return s, nil
	}
	s.Rows, s.Status = e.Rows, map[string]RowStatus{}
	if s.Notice == noticeRefreshing {
		s.Notice = ""
	}
	if s.Screen == ScreenDash {
		if s.Confirm != nil && s.Confirm.Kind == ConfirmStop {
			s.Confirm = nil
		}
		s.Epoch++
	}
	acts := []Action{LoadStatus{Repo: s.Repo, Paths: rowPaths(e.Rows)}}
	if !s.Fetching[s.Repo.Path] {
		s.Fetching = withFetching(s.Fetching, s.Repo.Path, true)
		acts = append(acts, Fetch{Repo: s.Repo})
	}
	if !s.IssuesLoaded && hasForgeIDs(s.Repo) {
		acts = append(acts, LoadIssues{Repo: s.Repo})
	}
	return s, acts
}

func onStatusLoaded(s State, e StatusLoaded) State {
	if !s.current(e.Repo) {
		return s
	}
	m := make(map[string]RowStatus, len(s.Status)+len(e.Status))
	for k, v := range s.Status {
		m[k] = v
	}
	for k, v := range e.Status {
		m[k] = v
	}
	s.Status = m
	return s
}

// withFetching returns a copy of m with path marked (or unmarked) in flight.
func withFetching(m map[string]bool, path string, on bool) map[string]bool {
	out := make(map[string]bool, len(m)+1)
	for k, v := range m {
		out[k] = v
	}
	if on {
		out[path] = true
	} else {
		delete(out, path)
	}
	return out
}

func onFetchDone(s State, e FetchDone) (State, []Action) {
	if s.Fetching[e.Repo.Path] {
		s.Fetching = withFetching(s.Fetching, e.Repo.Path, false)
	}
	if !s.current(e.Repo) || e.Err != nil || s.Rows == nil {
		return s, nil // offline / failed fetch: keep last-known status
	}
	return s, []Action{LoadStatus{Repo: s.Repo, Paths: rowPaths(s.Rows)}}
}

func onIssuesLoaded(s State, e IssuesLoaded) State {
	if !s.current(e.Repo) {
		return s
	}
	s.Issues, s.IssuesLoaded = e.Issues, true
	s.IssuePage = clamp(s.IssuePage, 0, pageCount(len(s.Issues), IssuePageSize)-1)
	if s.Screen == ScreenIssues {
		if s.Confirm != nil && s.Confirm.Kind == ConfirmIssue {
			s.Confirm = nil
		}
		s.Epoch++
	}
	return s
}

func onStarted(s State, e Started) (State, []Action) {
	s.Busy = ""
	var acts []Action
	switch {
	case e.Err != nil:
		s.Notice = "⚠ " + shortErr(e.Err)
	case e.RCURL != "":
		s.Notice = "launched " + e.Slot
		if e.AlreadyRunning {
			s.Notice = "already running: " + e.Slot
		}
		acts = append(acts, SendLink{Text: "🔗 " + e.Slot + "\n" + e.RCURL})
	case e.Exited:
		s.Notice = "⚠ session " + e.Slot + " exited during startup"
	case e.AlreadyRunning:
		s.Notice = "already running: " + e.Slot + " (no link yet)"
	default:
		s.Notice = "session " + e.Slot + " running, no link yet"
	}
	if s.Screen == ScreenIssues {
		s.Screen = ScreenDash
		s.Epoch++
	}
	if s.Screen == ScreenDash {
		acts = append(acts, LoadDash{Repo: s.Repo})
	}
	return s, acts
}

func onStopped(s State, e Stopped) (State, []Action) {
	s.Busy = ""
	if e.Err != nil {
		s.Notice = "⚠ " + shortErr(e.Err)
	} else {
		s.Notice = "stopped " + e.Slot
	}
	if s.Screen == ScreenDash {
		return s, []Action{LoadDash{Repo: s.Repo}}
	}
	return s, nil
}

// onOpened finishes a clone or a repo creation: open the new repo's
// dashboard and reload the picker lists (it is local now).
func onOpened(s State, repo core.Repo, err error) (State, []Action) {
	s.Busy = ""
	if err != nil {
		s.Notice = "⚠ " + shortErr(err)
		return s, nil
	}
	s, acts := openDash(s, repo)
	s.Notice = "ready: " + repo.Name
	return s, append(acts, LoadRepos{})
}

// shortErr is the first line of err without the "bridge: " prefix, ≤200 runes.
func shortErr(err error) string {
	msg, _, _ := strings.Cut(err.Error(), "\n")
	return navdata.TruncRunes(strings.TrimPrefix(msg, "bridge: "), 200)
}

// rowName is a dashboard row's short name: the base row's branch (or "base"),
// else the worktree basename.
func rowName(r navdata.DashRow) string {
	if r.IsBase {
		if r.Branch != "" {
			return r.Branch
		}
		return "base"
	}
	return r.Worktree
}
