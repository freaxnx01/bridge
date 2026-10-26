package bot

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/freaxnx01/bridge/internal/core"
	"github.com/freaxnx01/bridge/internal/forge"
	"github.com/freaxnx01/bridge/internal/navdata"
)

const (
	me    int64 = 111
	other int64 = 222
	chat  int64 = -1001 // a group chat
)

var cfg = Config{Allow: map[int64]bool{me: true}}

func local(forgeName, owner, name string) navdata.RepoEntry {
	r := core.Repo{Name: name, Forge: forgeName, Owner: owner, Visibility: "public", Path: "/r/" + forgeName + "/" + name}
	return navdata.RepoEntry{Label: navdata.RepoLabel(r), Repo: r}
}

func remote(forgeName, owner, name string) navdata.RepoEntry {
	return navdata.RemoteEntries([]forge.RepoRef{{Forge: forgeName, Owner: owner, Name: name, Visibility: "private"}})[0]
}

// picker returns a loaded picker state with message 10 on screen.
func picker(local, remote []navdata.RepoEntry) State {
	s, _ := Update(cfg, State{}, Text{UserID: me, ChatID: chat, Text: "/nav"})
	s, _ = Update(cfg, s, MessageSent{MessageID: 10})
	s, _ = Update(cfg, s, ReposLoaded{Local: local, Remote: remote})
	return s
}

// tap taps a button of the current keyboard as user me.
func tap(t *testing.T, s State, verb string, args ...string) (State, []Action) {
	t.Helper()
	return Update(cfg, s, Tap{UserID: me, ChatID: chat, MessageID: s.MessageID, Data: data(s.Epoch, verb, args...)})
}

func text(s State, msg string) (State, []Action) {
	return Update(cfg, s, Text{UserID: me, ChatID: chat, Text: msg})
}

func only[T Action](t *testing.T, acts []Action) T {
	t.Helper()
	if len(acts) != 1 {
		t.Fatalf("actions = %#v, want exactly one %T", acts, *new(T))
	}
	a, ok := acts[0].(T)
	if !ok {
		t.Fatalf("action = %#v, want %T", acts[0], *new(T))
	}
	return a
}

func dash(t *testing.T, rows ...navdata.DashRow) State {
	t.Helper()
	s := picker([]navdata.RepoEntry{local("github", "freaxnx01", "bridge")}, nil)
	s, _ = tap(t, s, "r", "0")
	s, _ = Update(cfg, s, DashLoaded{Repo: s.Repo, Rows: rows})
	return s
}

var (
	baseRow  = navdata.DashRow{IsBase: true, Branch: "main", Path: "/r/github/bridge"}
	liveRow  = navdata.DashRow{Worktree: "reorg", Path: "/r/github/bridge/.worktrees/reorg", HasSession: true, SlotID: "bridge-wt-reorg", State: "detached"}
	plainRow = navdata.DashRow{Worktree: "tg", Path: "/r/github/bridge/.worktrees/tg"}
)

func TestNav_OpensFreshPickerInNewMessage(t *testing.T) {
	s, acts := Update(cfg, State{Epoch: 4, Screen: ScreenDash, Filter: "x"}, Text{UserID: me, ChatID: chat, Text: "/nav@freax_bridge_bot"})
	if !reflect.DeepEqual(acts, []Action{NewMessage{}, LoadRepos{}}) {
		t.Errorf("actions = %#v", acts)
	}
	if s.Screen != ScreenPicker || s.Filter != "" || s.Epoch != 5 || s.MessageID != 0 {
		t.Errorf("state = %+v", s)
	}
}

func TestNotAllowlisted_TextDropped(t *testing.T) {
	s0 := State{Epoch: 2}
	s, acts := Update(cfg, s0, Text{UserID: other, ChatID: chat, Text: "/nav"})
	if !reflect.DeepEqual(acts, []Action{Drop{UserID: other, ChatID: chat}}) || !reflect.DeepEqual(s, s0) {
		t.Errorf("state = %+v, actions = %#v", s, acts)
	}
}

// Review Focus: in a group, another member taps a valid button on the
// allowlisted user's message. Nothing may happen.
func TestNotAllowlisted_TapInGroupOnValidButtonDropped(t *testing.T) {
	s0 := picker([]navdata.RepoEntry{local("github", "freaxnx01", "bridge")}, nil)
	s, acts := Update(cfg, s0, Tap{UserID: other, ChatID: chat, MessageID: s0.MessageID, Data: data(s0.Epoch, "r", "0")})
	if !reflect.DeepEqual(acts, []Action{Drop{UserID: other, ChatID: chat}}) {
		t.Errorf("actions = %#v", acts)
	}
	if !reflect.DeepEqual(s, s0) {
		t.Errorf("state changed: %+v", s)
	}
}

func TestOutdatedTaps(t *testing.T) {
	s := picker([]navdata.RepoEntry{local("github", "o", "a")}, nil)
	cases := map[string]Tap{
		"old epoch":     {UserID: me, MessageID: s.MessageID, Data: data(s.Epoch-1, "r", "0")},
		"old message":   {UserID: me, MessageID: s.MessageID - 1, Data: data(s.Epoch, "r", "0")},
		"index gone":    {UserID: me, MessageID: s.MessageID, Data: data(s.Epoch, "r", "7")},
		"garbage":       {UserID: me, MessageID: s.MessageID, Data: "zzz"},
		"over 64 bytes": {UserID: me, MessageID: s.MessageID, Data: data(s.Epoch, "r", strings.Repeat("9", 70))},
	}
	for name, tp := range cases {
		got, acts := Update(cfg, s, tp)
		if !reflect.DeepEqual(acts, []Action{Toast{Text: outdatedText}}) || !reflect.DeepEqual(got, s) {
			t.Errorf("%s: state changed or actions = %#v", name, acts)
		}
	}
	// After a restart the chat has no state at all.
	if _, acts := Update(cfg, State{}, Tap{UserID: me, MessageID: 10, Data: "3:r:0"}); !reflect.DeepEqual(acts, outdated()) {
		t.Errorf("restart: actions = %#v", acts)
	}
}

func TestPickerPaging(t *testing.T) {
	var many []navdata.RepoEntry
	for i := 0; i < 20; i++ {
		many = append(many, local("github", "o", fmt.Sprintf("repo%02d", i)))
	}
	s := picker(many, nil)
	s, _ = tap(t, s, "pg", "+1")
	s, _ = tap(t, s, "pg", "+1")
	if s.Page != 2 {
		t.Fatalf("page = %d, want 2", s.Page)
	}
	s, _ = tap(t, s, "pg", "+1")
	if s.Page != 2 {
		t.Errorf("page = %d, want clamped at 2", s.Page)
	}
	s, _ = tap(t, s, "pg", "-1")
	if s.Page != 1 {
		t.Errorf("page = %d, want 1", s.Page)
	}
}

func TestFilterPromptAndClear(t *testing.T) {
	s := picker([]navdata.RepoEntry{local("github", "o", "bridge"), local("github", "o", "notes")}, nil)
	s.Page = 1
	s, _ = tap(t, s, "f")
	if s.Prompt != PromptFilter {
		t.Fatalf("prompt = %v", s.Prompt)
	}
	s, acts := text(s, "  BRID ")
	if len(acts) != 0 || s.Filter != "BRID" || s.Prompt != PromptNone || s.Page != 0 {
		t.Fatalf("state = %+v, actions = %#v", s, acts)
	}
	if items := s.PickerItems(); len(items) != 1 || items[0].Entry.Repo.Name != "bridge" {
		t.Errorf("items = %+v", items)
	}
	s, _ = tap(t, s, "fx")
	if s.Filter != "" || len(s.PickerItems()) != 2 {
		t.Errorf("filter not cleared: %+v", s)
	}
}

func TestForgeFilterCycles(t *testing.T) {
	s := picker([]navdata.RepoEntry{local("github", "o", "a"), local("forgejo", "o", "b")}, nil)
	var seen []string
	for i := 0; i < 3; i++ {
		s, _ = tap(t, s, "fg")
		seen = append(seen, s.Forge)
	}
	if !reflect.DeepEqual(seen, []string{"github", "forgejo", ""}) {
		t.Errorf("cycle = %v", seen)
	}
}

func TestMRUFirstThenByName(t *testing.T) {
	s := picker([]navdata.RepoEntry{local("github", "o", "zeta"), local("github", "o", "alpha"), local("forgejo", "o", "mid")},
		[]navdata.RepoEntry{remote("github", "o", "beta")})
	s.MRU = []string{"/r/github/zeta"}
	var got []string
	for _, it := range s.PickerItems() {
		_, _, _, n := navdata.Parts(it.Entry)
		got = append(got, fmt.Sprintf("%s:%v", n, it.Star))
	}
	if want := []string{"zeta:true", "alpha:false", "beta:false", "mid:false"}; !reflect.DeepEqual(got, want) {
		t.Errorf("order = %v, want %v", got, want)
	}
}

// Review Focus: the same repo name on two forges must open the repo whose
// button was tapped, never the other one.
func TestSameNameOnTwoForges_OpensTheTappedOne(t *testing.T) {
	s := picker([]navdata.RepoEntry{local("github", "freaxnx01", "bridge"), local("forgejo", "freaxnx01", "bridge")}, nil)
	items := s.PickerItems()
	for i, it := range items {
		_, acts := tap(t, s, "r", fmt.Sprint(i))
		ld := only[LoadDash](t, acts)
		if ld.Repo.Path != it.Entry.Repo.Path {
			t.Errorf("item %d: opened %s, want %s", i, ld.Repo.Path, it.Entry.Repo.Path)
		}
	}
	if items[0].Entry.Repo.Path == items[1].Entry.Repo.Path {
		t.Fatal("both items point at the same repo")
	}
}

func TestOpenDashboard_LoadsRowsStatusFetchIssues(t *testing.T) {
	s := picker([]navdata.RepoEntry{local("github", "freaxnx01", "bridge")}, nil)
	s, acts := tap(t, s, "r", "0")
	if s.Screen != ScreenDash || only[LoadDash](t, acts).Repo.Name != "bridge" {
		t.Fatalf("state = %+v, actions = %#v", s, acts)
	}
	s, acts = Update(cfg, s, DashLoaded{Repo: s.Repo, Rows: []navdata.DashRow{baseRow, liveRow}})
	want := []Action{
		LoadStatus{Repo: s.Repo, Paths: []string{baseRow.Path, liveRow.Path}},
		Fetch{Repo: s.Repo},
		LoadIssues{Repo: s.Repo},
	}
	if !reflect.DeepEqual(acts, want) {
		t.Errorf("actions = %#v, want %#v", acts, want)
	}
	if _, acts := Update(cfg, s, FetchDone{Repo: s.Repo}); !reflect.DeepEqual(acts, want[:1]) {
		t.Errorf("after fetch: %#v", acts)
	}
	if _, acts := Update(cfg, s, FetchDone{Repo: s.Repo, Err: errors.New("offline")}); len(acts) != 0 {
		t.Errorf("failed fetch: %#v", acts)
	}
	stale := core.Repo{Name: "other", Path: "/elsewhere"}
	if got, acts := Update(cfg, s, DashLoaded{Repo: stale, Rows: nil}); len(acts) != 0 || got.Rows == nil {
		t.Errorf("stale result applied: %+v %#v", got, acts)
	}
}

func TestStart_RowAndResults(t *testing.T) {
	s := dash(t, baseRow, liveRow)
	s, acts := tap(t, s, "w", "0")
	if st := only[Start](t, acts); st.Worktree != "" || st.Repo.Name != "bridge" || st.Create {
		t.Errorf("start = %+v", st)
	}
	if s.Busy == "" {
		t.Fatal("busy not set")
	}
	cases := []struct {
		ev       Started
		notice   string
		wantLink bool
	}{
		{Started{Slot: "bridge", RCURL: "https://claude.ai/code/session_A"}, "launched bridge", true},
		{Started{Slot: "bridge", AlreadyRunning: true, RCURL: "https://claude.ai/code/session_A"}, "already running: bridge", true},
		{Started{Slot: "bridge"}, "session bridge running, no link yet", false},
		{Started{Slot: "bridge", Exited: true}, "⚠ session bridge exited during startup", false},
		{Started{Err: errors.New("bridge: tmux: boom\nmore")}, "⚠ tmux: boom", false},
	}
	for _, c := range cases {
		got, acts := Update(cfg, s, c.ev)
		if got.Busy != "" || got.Notice != c.notice {
			t.Errorf("%+v: busy=%q notice=%q, want notice %q", c.ev, got.Busy, got.Notice, c.notice)
		}
		hasLink := false
		for _, a := range acts {
			if l, ok := a.(SendLink); ok {
				hasLink = strings.HasSuffix(l.Text, c.ev.RCURL)
			}
		}
		if hasLink != c.wantLink {
			t.Errorf("%+v: link sent = %v, want %v (%#v)", c.ev, hasLink, c.wantLink, acts)
		}
		if _, ok := acts[len(acts)-1].(LoadDash); !ok {
			t.Errorf("%+v: dashboard not reloaded: %#v", c.ev, acts)
		}
	}
}

// Review Focus: a flood of taps while an action runs must not start anything
// else — every tap only gets a toast.
func TestFloodOfTapsWhileBusy(t *testing.T) {
	s := dash(t, baseRow, liveRow, plainRow)
	s, _ = tap(t, s, "w", "0")
	busy := s
	for _, verb := range [][]string{{"w", "1"}, {"w", "0"}, {"x", "1"}, {"nw"}, {"bk"}, {"rf"}} {
		got, acts := tap(t, busy, verb[0], verb[1:]...)
		toast := only[Toast](t, acts)
		if !strings.HasPrefix(toast.Text, "⏳ still working") {
			t.Errorf("%v: toast = %q", verb, toast.Text)
		}
		if !reflect.DeepEqual(got, busy) {
			t.Errorf("%v: state changed while busy", verb)
		}
	}
	if _, acts := text(busy, "/nav"); !reflect.DeepEqual(acts, []Action{NewMessage{}, LoadRepos{}}) {
		t.Errorf("/nav while busy: %#v", acts)
	}
}

func TestStopConfirmAndCancel(t *testing.T) {
	s := dash(t, baseRow, liveRow)
	if _, acts := tap(t, s, "x", "0"); !reflect.DeepEqual(acts, outdated()) {
		t.Errorf("stop on a row without session: %#v", acts)
	}
	s, acts := tap(t, s, "x", "1")
	if len(acts) != 0 || s.Confirm == nil || *s.Confirm != (Confirm{ConfirmStop, 1}) {
		t.Fatalf("confirm = %+v, actions = %#v", s.Confirm, acts)
	}
	if _, acts := tap(t, s, "ok", "stop", "0"); !reflect.DeepEqual(acts, outdated()) {
		t.Errorf("mismatched confirm: %#v", acts)
	}
	cancelled, _ := tap(t, s, "no")
	if cancelled.Confirm != nil || cancelled.Busy != "" {
		t.Errorf("cancel: %+v", cancelled)
	}
	s, acts = tap(t, s, "ok", "stop", "1")
	if only[Stop](t, acts).Slot != "bridge-wt-reorg" || s.Busy == "" {
		t.Errorf("stop: %+v %#v", s, acts)
	}
	s, acts = Update(cfg, s, Stopped{Slot: "bridge-wt-reorg"})
	if s.Notice != "stopped bridge-wt-reorg" || only[LoadDash](t, acts).Repo.Name != "bridge" {
		t.Errorf("stopped: %+v %#v", s, acts)
	}
}

func TestNewWorktreeNameValidation(t *testing.T) {
	s := dash(t, baseRow)
	s, _ = tap(t, s, "nw")
	for _, bad := range []string{"../x", "a b", "..", ".", "x/y", "ü", strings.Repeat("a", 41), "$(rm)"} {
		got, acts := text(s, bad)
		if len(acts) != 0 || got.Notice != invalidNameText || got.Prompt != PromptNewWorktree {
			t.Errorf("%q accepted: %+v %#v", bad, got, acts)
		}
	}
	s, acts := text(s, "feat-1.2_x")
	if st := only[Start](t, acts); st.Worktree != "feat-1.2_x" || !st.Create || st.Label != "" {
		t.Errorf("start = %+v", st)
	}
	if s.Prompt != PromptNone || s.Busy == "" {
		t.Errorf("state = %+v", s)
	}
}

// Review Focus: an issue title with emoji and a slash yields a safe worktree
// name and a valid label.
func TestIssueLaunch_EmojiAndSlashTitle(t *testing.T) {
	s := dash(t, baseRow)
	s, _ = Update(cfg, s, IssuesLoaded{Repo: s.Repo, Issues: []forge.Issue{{Number: 7, Title: "🚀 fix/launch: crash"}}})
	s, _ = tap(t, s, "is")
	if s.Screen != ScreenIssues {
		t.Fatalf("screen = %v", s.Screen)
	}
	s, _ = tap(t, s, "i", "0")
	s, acts := tap(t, s, "ok", "issue", "0")
	want := Start{Repo: s.Repo, Worktree: "7-fix-launch-crash", Label: "#7 [🚀 fix/launch: crash]", Create: true}
	if got := only[Start](t, acts); !reflect.DeepEqual(got, want) {
		t.Errorf("start = %+v, want %+v", got, want)
	}
	s, _ = Update(cfg, s, Started{Slot: "bridge-wt-7-fix-launch-crash", RCURL: "https://claude.ai/code/session_B"})
	if s.Screen != ScreenDash {
		t.Errorf("screen after issue start = %v, want dashboard", s.Screen)
	}
}

func TestCloneRemoteRepo(t *testing.T) {
	s := picker(nil, []navdata.RepoEntry{remote("github", "freaxnx01", "obsidian-it")})
	s, acts := tap(t, s, "r", "0")
	if len(acts) != 0 || s.Confirm == nil || s.Confirm.Kind != ConfirmClone {
		t.Fatalf("confirm = %+v %#v", s.Confirm, acts)
	}
	s, acts = tap(t, s, "ok", "clone", "0")
	if only[Clone](t, acts).Ref.Name != "obsidian-it" || s.Busy == "" {
		t.Fatalf("clone: %+v %#v", s, acts)
	}
	repo := core.Repo{Name: "obsidian-it", Path: "/r/github/obsidian-it", Forge: "github", Owner: "freaxnx01"}
	s, acts = Update(cfg, s, Cloned{Repo: repo})
	if s.Screen != ScreenDash || s.Repo.Path != repo.Path || s.Busy != "" {
		t.Errorf("after clone: %+v", s)
	}
	if !reflect.DeepEqual(acts, []Action{LoadDash{Repo: repo}, LoadRepos{}}) {
		t.Errorf("actions = %#v", acts)
	}
	failed, acts := Update(cfg, State{Busy: "⏳ cloning x…"}, Cloned{Err: errors.New("clone: exit status 128")})
	if failed.Busy != "" || failed.Notice != "⚠ clone: exit status 128" || len(acts) != 0 {
		t.Errorf("failed clone: %+v %#v", failed, acts)
	}
}

func TestNewRepoFlow(t *testing.T) {
	s := picker(nil, nil)
	s, _ = tap(t, s, "nr")
	s, acts := text(s, "bad name")
	if len(acts) != 0 || s.Notice != invalidNameText {
		t.Fatalf("invalid name: %+v", s)
	}
	s, _ = text(s, "demo")
	if s.Prompt != PromptNewRepoForge || s.NewRepoName != "demo" {
		t.Fatalf("state = %+v", s)
	}
	s, _ = tap(t, s, "nc", "2")
	s, acts = tap(t, s, "ok", "repo", "2")
	if got := only[CreateRepo](t, acts); got != (CreateRepo{Name: "demo", Forge: "github", Private: true}) {
		t.Errorf("create = %+v", got)
	}
	repo := core.Repo{Name: "demo", Path: "/r/github/demo"}
	s, acts = Update(cfg, s, RepoCreated{Repo: repo})
	if s.Screen != ScreenDash || s.Prompt != PromptNone || s.NewRepoName != "" || !reflect.DeepEqual(acts, []Action{LoadDash{Repo: repo}, LoadRepos{}}) {
		t.Errorf("after create: %+v %#v", s, acts)
	}
}

func TestRefreshRepos(t *testing.T) {
	s := picker(nil, nil)
	s, acts := tap(t, s, "rf")
	if !reflect.DeepEqual(acts, []Action{LoadRepos{Refresh: true}}) || s.Busy != busyRefreshRepos {
		t.Fatalf("refresh: %+v %#v", s, acts)
	}
	s, _ = Update(cfg, s, ReposLoaded{Err: errors.New("github: 401")})
	if s.Busy != "" || s.Notice != "⚠ github: 401" {
		t.Errorf("after refresh: %+v", s)
	}
}

func TestBackToReposKeepsPageAndFilter(t *testing.T) {
	var many []navdata.RepoEntry
	for i := 0; i < 20; i++ {
		many = append(many, local("github", "o", fmt.Sprintf("bridge%02d", i)))
	}
	s := picker(many, nil)
	s, _ = tap(t, s, "f")
	s, _ = text(s, "bridge")
	s, _ = tap(t, s, "pg", "+1")
	s, _ = tap(t, s, "r", "9")
	s, acts := tap(t, s, "bk")
	if s.Screen != ScreenPicker || s.Page != 1 || s.Filter != "bridge" || !reflect.DeepEqual(acts, []Action{LoadRepos{}}) {
		t.Errorf("back: page=%d filter=%q acts=%#v", s.Page, s.Filter, acts)
	}
}

func TestValidName(t *testing.T) {
	for _, ok := range []string{"a", "A-b_c.d", strings.Repeat("x", 40), ".env"} {
		if !ValidName(ok) {
			t.Errorf("ValidName(%q) = false", ok)
		}
	}
	for _, bad := range []string{"", ".", "..", "...", "a/b", "a b", "ü", strings.Repeat("x", 41)} {
		if ValidName(bad) {
			t.Errorf("ValidName(%q) = true", bad)
		}
	}
}
