package nav

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/freaxnx01/bridge/internal/core"
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
