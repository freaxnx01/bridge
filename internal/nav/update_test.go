package nav

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/freaxnx01/bridge/internal/core"
	"github.com/freaxnx01/bridge/internal/forge"
)

func TestUpdate_ReposMsg_PopulatesLocal(t *testing.T) {
	m := initialModel(Config{})
	out, _ := m.Update(reposMsg{rows: []repoRow{{label: "a"}, {label: "b"}}})
	got := out.(Model)
	if len(got.localRepos) != 2 {
		t.Fatalf("localRepos = %d, want 2", len(got.localRepos))
	}
}

func TestUpdate_RemoteErrMsg_SetsErrStateKeepsCache(t *testing.T) {
	m := initialModel(Config{})
	m.remoteRepos = []repoRow{{label: "cached"}}
	out, _ := m.Update(remoteErrMsg{err: errFake})
	got := out.(Model)
	if got.remoteState != loadErr {
		t.Errorf("remoteState = %d, want loadErr", got.remoteState)
	}
	if len(got.remoteRepos) != 1 {
		t.Errorf("cached remote rows should survive an error")
	}
}

func TestUpdate_DownFromFilter_MovesFocusToList(t *testing.T) {
	m := initialModel(Config{})
	m.localRepos = []repoRow{{label: "a"}, {label: "b"}}
	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	got := out.(Model)
	if got.pickerFocus != focusList {
		t.Errorf("pickerFocus = %d, want focusList after Down from filter", got.pickerFocus)
	}
}

func TestUpdate_DirtyMsg_FillsRowByPath(t *testing.T) {
	m := initialModel(Config{})
	m.screen = screenDash
	m.dashRows = []dashRow{{worktree: "x", path: "/r/x", dirtyState: loadPending}}
	out, _ := m.Update(dirtyMsg{path: "/r/x", info: dirtyInfo{modified: 3}})
	got := out.(Model)
	if got.dashRows[0].dirtyState != loadOK || got.dashRows[0].dirty.modified != 3 {
		t.Errorf("dirty not applied: %+v", got.dashRows[0])
	}
}

var errFake = fakeErr("boom")

type fakeErr string

func (e fakeErr) Error() string { return string(e) }

func TestUpdatePicker_EnterLocalRepo_EntersDash(t *testing.T) {
	m := initialModel(Config{})
	m.pickerFocus = focusList
	m.localRepos = []repoRow{{label: "github/public/bridge", repo: core.Repo{Name: "bridge", Path: "/r"}}}
	out, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got := out.(Model)
	if got.screen != screenDash {
		t.Fatalf("screen = %d, want screenDash", got.screen)
	}
	if got.repo.Name != "bridge" {
		t.Errorf("repo = %q, want bridge", got.repo.Name)
	}
	if cmd == nil {
		t.Errorf("entering dash should return a loadDashRows Cmd")
	}
}

func TestUpdateDash_N_OpensModal(t *testing.T) {
	m := initialModel(Config{})
	m.screen = screenDash
	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	got := out.(Model)
	if got.modal == nil {
		t.Fatalf("pressing n should open the new-worktree modal")
	}
}

func TestUpdateDash_EscFromDash_ReturnsToPicker(t *testing.T) {
	m := initialModel(Config{})
	m.screen = screenDash
	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	got := out.(Model)
	if got.screen != screenPicker {
		t.Errorf("esc on dash should return to picker, got screen %d", got.screen)
	}
}

func TestUpdateModal_Backspace_IsRuneSafe(t *testing.T) {
	m := initialModel(Config{})
	m.screen = screenDash
	m.modal = &newWorktreeModal{name: "café"}
	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	got := out.(Model)
	if got.modal.name != "caf" {
		t.Errorf("name = %q, want %q (one rune removed, valid UTF-8)", got.modal.name, "caf")
	}
}

func TestUpdatePicker_UpAtFirst_ReturnsToFilter(t *testing.T) {
	m := initialModel(Config{})
	m.pickerFocus = focusList
	m.localRepos = []repoRow{{label: "a"}, {label: "b"}}
	m.pickerSel = 0
	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyUp})
	if got := out.(Model); got.pickerFocus != focusFilter {
		t.Errorf("up at first entry should return to filter, focus=%d", got.pickerFocus)
	}
}

func TestUpdatePicker_HomeEnd(t *testing.T) {
	m := initialModel(Config{})
	m.pickerFocus = focusList
	for i := 0; i < 10; i++ {
		m.localRepos = append(m.localRepos, repoRow{label: "r"})
	}
	m.pickerSel = 3
	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnd})
	if got := out.(Model); got.pickerSel != 9 {
		t.Errorf("End -> pickerSel=%d, want 9", got.pickerSel)
	}
	m.pickerSel = 5
	out, _ = m.Update(tea.KeyMsg{Type: tea.KeyHome})
	if got := out.(Model); got.pickerSel != 0 {
		t.Errorf("Home -> pickerSel=%d, want 0", got.pickerSel)
	}
}

func TestUpdatePicker_PgDownClampsToLast(t *testing.T) {
	m := initialModel(Config{})
	m.width, m.height = 80, 20 // listPage() == 10
	m.pickerFocus = focusList
	for i := 0; i < 50; i++ {
		m.localRepos = append(m.localRepos, repoRow{label: "r"})
	}
	m.pickerSel = 0
	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	got := out.(Model)
	if got.pickerSel != 10 {
		t.Fatalf("PgDown from 0 -> %d, want 10", got.pickerSel)
	}
	got.pickerSel = 45
	out, _ = got.Update(tea.KeyMsg{Type: tea.KeyPgDown})
	if g2 := out.(Model); g2.pickerSel != 49 {
		t.Errorf("PgDown near end -> %d, want 49 (clamped)", g2.pickerSel)
	}
}

func TestUpdateDash_EndJumpsToCreateRow(t *testing.T) {
	m := initialModel(Config{})
	m.screen = screenDash
	m.dashRows = []dashRow{{worktree: "a"}, {worktree: "b"}}
	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnd})
	if got := out.(Model); got.dashSel != len(m.dashRows) {
		t.Errorf("End -> dashSel=%d, want %d (create row)", got.dashSel, len(m.dashRows))
	}
}

func TestUpdatePicker_EndFromFilter_EntersListAtLast(t *testing.T) {
	m := initialModel(Config{}) // starts focused on the filter
	for i := 0; i < 10; i++ {
		m.localRepos = append(m.localRepos, repoRow{label: "r"})
	}
	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnd})
	got := out.(Model)
	if got.pickerFocus != focusList {
		t.Fatalf("End from filter should enter the list, focus=%d", got.pickerFocus)
	}
	if got.pickerSel != 9 {
		t.Errorf("End from filter -> pickerSel=%d, want 9", got.pickerSel)
	}
}

func TestUpdatePicker_HomeFromFilter_EntersListAtFirst(t *testing.T) {
	m := initialModel(Config{})
	for i := 0; i < 5; i++ {
		m.localRepos = append(m.localRepos, repoRow{label: "r"})
	}
	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyHome})
	got := out.(Model)
	if got.pickerFocus != focusList || got.pickerSel != 0 {
		t.Errorf("Home from filter -> focus=%d sel=%d, want focusList,0", got.pickerFocus, got.pickerSel)
	}
}

func TestLaunchArgvFor_NameArgsInjected(t *testing.T) {
	m := initialModel(Config{
		DefaultAgent: "claude",
		NameArgs: func(agent string, repo core.Repo, wt string) []string {
			return []string{"-n", repo.Name + " [" + wt + "]"}
		},
	})
	m.repo = core.Repo{Name: "bridge", Path: "/r"}
	row := dashRow{worktree: "wt1", path: "/r/.worktrees/wt1"}
	argv, err := m.launchArgvFor(row)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(argv, " "), "-n bridge [wt1]") {
		t.Errorf("expected injected claude name args, got %v", argv)
	}
}

func TestUpdatePicker_UpFromFilter_EntersSessions(t *testing.T) {
	m := initialModel(Config{}) // focusFilter
	m.sessions = []sessionRow{{slotID: "a"}, {slotID: "b"}}
	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyUp})
	got := out.(Model)
	if got.pickerFocus != focusSessions {
		t.Fatalf("Up from filter should enter sessions, focus=%d", got.pickerFocus)
	}
	if got.sessionSel != 1 {
		t.Errorf("sessionSel=%d, want 1 (last)", got.sessionSel)
	}
}

func TestUpdatePicker_UpFromFilter_NoSessions_StaysInFilter(t *testing.T) {
	m := initialModel(Config{})
	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyUp})
	if got := out.(Model); got.pickerFocus != focusFilter {
		t.Errorf("Up with no sessions should stay in filter, focus=%d", got.pickerFocus)
	}
}

func TestUpdatePicker_TabCyclesFocus(t *testing.T) {
	m := initialModel(Config{})
	m.sessions = []sessionRow{{slotID: "a"}}
	m.localRepos = []repoRow{{label: "x"}}
	steps := []focus{focusList, focusSessions, focusFilter}
	cur := m
	for i, want := range steps {
		out, _ := cur.Update(tea.KeyMsg{Type: tea.KeyTab})
		cur = out.(Model)
		if cur.pickerFocus != want {
			t.Fatalf("tab #%d focus=%d, want %d", i+1, cur.pickerFocus, want)
		}
	}
}

func TestUpdatePicker_gG_Aliases(t *testing.T) {
	m := initialModel(Config{})
	m.pickerFocus = focusList
	for i := 0; i < 8; i++ {
		m.localRepos = append(m.localRepos, repoRow{label: "r"})
	}
	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("G")})
	if got := out.(Model); got.pickerSel != 7 {
		t.Errorf("G -> pickerSel=%d, want 7", got.pickerSel)
	}
	m.pickerSel = 5
	out, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("g")})
	if got := out.(Model); got.pickerSel != 0 {
		t.Errorf("g -> pickerSel=%d, want 0", got.pickerSel)
	}
}

func TestUpdatePicker_SessionsEnter_ReturnsAttachCmd(t *testing.T) {
	m := initialModel(Config{})
	m.pickerFocus = focusSessions
	m.sessions = []sessionRow{{slotID: "bridge-wt-fix"}}
	m.sessionSel = 0
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Errorf("Enter on a session should return an attach command")
	}
}

func TestUpdatePicker_EnterFilter_SingleMatchOpens(t *testing.T) {
	m := initialModel(Config{}) // focused on filter
	m.localRepos = []repoRow{
		{label: "github/public/bridge", repo: core.Repo{Name: "bridge", Path: "/r"}},
		{label: "github/public/dgraph", repo: core.Repo{Name: "dgraph", Path: "/d"}},
	}
	m.filter.SetValue("dgra")
	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got := out.(Model)
	if got.screen != screenDash || got.repo.Name != "dgraph" {
		t.Fatalf("Enter on a single-match filter should open it; screen=%d repo=%q", got.screen, got.repo.Name)
	}
}

func TestUpdatePicker_EnterFilter_MultiMatchGoesToList(t *testing.T) {
	m := initialModel(Config{})
	m.localRepos = []repoRow{{label: "alpha", repo: core.Repo{Name: "alpha"}}, {label: "alphabet", repo: core.Repo{Name: "alphabet"}}}
	m.filter.SetValue("alph")
	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got := out.(Model)
	if got.pickerFocus != focusList || got.screen != screenPicker {
		t.Errorf("Enter on multi-match should focus the list; focus=%d screen=%d", got.pickerFocus, got.screen)
	}
}

func TestUpdatePicker_ShiftTabCyclesBack(t *testing.T) {
	m := initialModel(Config{})
	m.sessions = []sessionRow{{slotID: "a"}}
	steps := []focus{focusSessions, focusList, focusFilter} // filter -> sessions -> list -> filter
	cur := m
	for i, want := range steps {
		out, _ := cur.Update(tea.KeyMsg{Type: tea.KeyShiftTab})
		cur = out.(Model)
		if cur.pickerFocus != want {
			t.Fatalf("shift+tab #%d focus=%d want %d", i+1, cur.pickerFocus, want)
		}
	}
}

func TestLogKey_AppendsToFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "k.log")
	logKey(p, tea.KeyMsg{Type: tea.KeyHome})
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "home") {
		t.Errorf("log=%q, want it to contain the key string", b)
	}
}

func TestUpdateDash_gG_Aliases(t *testing.T) {
	m := initialModel(Config{})
	m.screen = screenDash
	m.dashRows = []dashRow{{worktree: "a"}, {worktree: "b"}, {worktree: "c"}}
	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("G")})
	if got := out.(Model); got.dashSel != len(m.dashRows) { // create row
		t.Errorf("G -> dashSel=%d, want %d", got.dashSel, len(m.dashRows))
	}
	m.dashSel = 2
	out, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("g")})
	if got := out.(Model); got.dashSel != 0 {
		t.Errorf("g -> dashSel=%d, want 0", got.dashSel)
	}
}

func TestUpdateDash_MoveSelection_FiresDetailLoadAndSeedsPending(t *testing.T) {
	m := initialModel(Config{})
	m.screen = screenDash
	m.dashRows = []dashRow{
		{worktree: "fix-x", path: "/r/.worktrees/fix-x"},
		{worktree: "docs", path: "/r/.worktrees/docs"},
	}
	m.dashSel = 0
	out, cmd := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	got := out.(Model)
	if got.dashSel != 1 {
		t.Fatalf("dashSel = %d, want 1", got.dashSel)
	}
	d, ok := got.details["/r/.worktrees/docs"]
	if !ok {
		t.Fatalf("expected a pending cache entry for the newly selected worktree")
	}
	if d.branchesState != loadPending || d.commitsState != loadPending || d.statusState != loadPending {
		t.Errorf("new cache entry should be all loadPending, got %+v", d)
	}
	if cmd == nil {
		t.Errorf("moving to an uncached worktree should return a load Cmd")
	}
}

func TestUpdateDash_MoveToCachedWorktree_NoRefire(t *testing.T) {
	m := initialModel(Config{})
	m.screen = screenDash
	m.dashRows = []dashRow{
		{worktree: "fix-x", path: "/r/.worktrees/fix-x"},
		{worktree: "docs", path: "/r/.worktrees/docs"},
	}
	m.dashSel = 0
	m.details["/r/.worktrees/docs"] = &worktreeDetails{branchesState: loadOK}
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if cmd != nil {
		t.Errorf("moving to a cached worktree should not refire a load Cmd")
	}
}

func TestUpdateDash_CreateRowSelected_NoLoad(t *testing.T) {
	m := initialModel(Config{})
	m.screen = screenDash
	m.dashRows = []dashRow{{worktree: "fix-x", path: "/r/.worktrees/fix-x"}}
	m.dashSel = 0
	// Down wraps from the single worktree row onto the "+ create" row (index 1).
	out, cmd := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	got := out.(Model)
	if got.dashSel != 1 {
		t.Fatalf("dashSel = %d, want 1 (the create row)", got.dashSel)
	}
	if cmd != nil {
		t.Errorf("the create row has no worktree, so no load Cmd should fire")
	}
}

func TestUpdate_BranchesMsg_FillsCache(t *testing.T) {
	m := initialModel(Config{})
	m.details["/r/x"] = &worktreeDetails{}
	out, _ := m.Update(branchesMsg{path: "/r/x", branches: []branchInfo{{name: "main"}}})
	got := out.(Model)
	d := got.details["/r/x"]
	if d.branchesState != loadOK || len(d.branches) != 1 {
		t.Errorf("branchesMsg not applied: %+v", d)
	}
}

func TestUpdate_StatusMsgErr_SetsErrState(t *testing.T) {
	m := initialModel(Config{})
	m.details["/r/x"] = &worktreeDetails{}
	out, _ := m.Update(statusMsg{path: "/r/x", err: errFake})
	got := out.(Model)
	if got.details["/r/x"].statusState != loadErr {
		t.Errorf("statusMsg error should set loadErr, got %+v", got.details["/r/x"])
	}
}

func TestUpdate_CommitsMsg_EvictedPath_Ignored(t *testing.T) {
	m := initialModel(Config{})
	// No entry for "/gone" — a late msg for an evicted worktree must be a no-op.
	out, _ := m.Update(commitsMsg{path: "/gone", commits: []commitInfo{{sha: "a"}}})
	got := out.(Model)
	if _, ok := got.details["/gone"]; ok {
		t.Errorf("a msg for an evicted path must not create a cache entry")
	}
}

func TestUpdate_DashRowsMsg_ClearsCacheAndLoadsSelection(t *testing.T) {
	m := initialModel(Config{})
	m.screen = screenDash
	m.details["/stale"] = &worktreeDetails{branchesState: loadOK}
	out, cmd := m.Update(dashRowsMsg{rows: []dashRow{{worktree: "fix-x", path: "/r/fix-x"}}})
	got := out.(Model)
	if _, ok := got.details["/stale"]; ok {
		t.Errorf("dashRowsMsg should clear the stale cache")
	}
	if _, ok := got.details["/r/fix-x"]; !ok {
		t.Errorf("dashRowsMsg should seed a load for the current selection")
	}
	if cmd == nil {
		t.Errorf("dashRowsMsg should return Cmds (dirty + detail load)")
	}
}

func TestUpdate_FetchDoneMsg_Success_ReloadsDirty(t *testing.T) {
	m := initialModel(Config{})
	m.screen = screenDash
	m.dashRows = []dashRow{{worktree: "fix-x", path: "/r/fix-x"}}
	_, cmd := m.Update(fetchDoneMsg{})
	if cmd == nil {
		t.Errorf("a successful fetch should trigger a dirty reload Cmd")
	}
}

func TestUpdate_FetchDoneMsg_Error_KeepsLastKnown(t *testing.T) {
	m := initialModel(Config{})
	m.screen = screenDash
	m.dashRows = []dashRow{{worktree: "fix-x", path: "/r/fix-x"}}
	_, cmd := m.Update(fetchDoneMsg{err: errFake})
	if cmd != nil {
		t.Errorf("a failed fetch should be a no-op (keep last-known), got a Cmd")
	}
}

func TestUpdate_IssueCountMsg_UpdatesLocalAndRemote(t *testing.T) {
	m := initialModel(Config{})
	m.localRepos = []repoRow{{
		label: "github/public/bridge",
		repo:  core.Repo{Forge: "github", Owner: "freaxnx01", Name: "bridge"},
	}}
	m.remoteRepos = []repoRow{{
		label:  "↓ github/public/other",
		remote: &forge.RepoRef{Forge: "github", Owner: "freaxnx01", Name: "other"},
	}}

	out, _ := m.Update(issueCountMsg{key: "github/freaxnx01/bridge", count: 5})
	got := out.(Model)
	if got.localRepos[0].issueCount != 5 {
		t.Errorf("local issueCount = %d, want 5", got.localRepos[0].issueCount)
	}
	if got.localRepos[0].issueState != loadOK {
		t.Errorf("local issueState = %d, want loadOK", got.localRepos[0].issueState)
	}

	out2, _ := got.Update(issueCountMsg{key: "github/freaxnx01/other", count: 3})
	got2 := out2.(Model)
	if got2.remoteRepos[0].issueCount != 3 {
		t.Errorf("remote issueCount = %d, want 3", got2.remoteRepos[0].issueCount)
	}
	if got2.remoteRepos[0].issueState != loadOK {
		t.Errorf("remote issueState = %d, want loadOK", got2.remoteRepos[0].issueState)
	}
}

func TestUpdate_IssueCountMsg_UnknownKey_IsNoop(t *testing.T) {
	m := initialModel(Config{})
	m.localRepos = []repoRow{{label: "a", repo: core.Repo{Forge: "github", Owner: "x", Name: "y"}}}
	out, _ := m.Update(issueCountMsg{key: "github/other/repo", count: 9})
	got := out.(Model)
	if got.localRepos[0].issueCount != 0 {
		t.Errorf("unknown key should not modify any row, issueCount=%d", got.localRepos[0].issueCount)
	}
}

func TestLoadIssueCountCmd_CacheHit_ReturnsCount(t *testing.T) {
	dir := t.TempDir()
	cacheFile := filepath.Join(dir, "github_owner_myrepo.json")
	if err := forge.WriteIssueCache(cacheFile, forge.IssueCache{
		UpdatedAt: time.Now(),
		Issues:    []forge.Issue{{Number: 1}, {Number: 2}},
	}); err != nil {
		t.Fatal(err)
	}
	cmd := loadIssueCountCmd(Config{IssueCacheDir: dir}, "github/owner/myrepo", "github", "owner", "myrepo")
	msg, ok := cmd().(issueCountMsg)
	if !ok {
		t.Fatalf("expected issueCountMsg, got %T", cmd())
	}
	if msg.count != 2 {
		t.Errorf("count = %d, want 2", msg.count)
	}
	if msg.key != "github/owner/myrepo" {
		t.Errorf("key = %q, want github/owner/myrepo", msg.key)
	}
}

func TestLoadIssueCountCmd_NoConfigNoop_ReturnsZero(t *testing.T) {
	cmd := loadIssueCountCmd(Config{}, "github/x/y", "github", "x", "y")
	msg, ok := cmd().(issueCountMsg)
	if !ok {
		t.Fatalf("expected issueCountMsg, got %T", cmd())
	}
	if msg.count != 0 {
		t.Errorf("count = %d, want 0 (no cache, no FetchIssues)", msg.count)
	}
}

func TestIssueCountCmds_NoCfg_ReturnsNil(t *testing.T) {
	m := initialModel(Config{})
	m.localRepos = []repoRow{{
		label: "github/public/bridge",
		repo:  core.Repo{Forge: "github", Owner: "freaxnx01", Name: "bridge"},
	}}
	if cmd := m.issueCountCmds(m.localRepos); cmd != nil {
		t.Errorf("issueCountCmds without cache/fetch config should return nil")
	}
}
