package nav

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/freaxnx01/bridge/internal/core"
	"github.com/freaxnx01/bridge/internal/forge"
	"github.com/freaxnx01/bridge/internal/gitauth"
)

func TestArchivedKeys_MatchesRepoRowKeyIdentity(t *testing.T) {
	refs := []forge.RepoRef{
		{Forge: "github", Owner: "freaxnx01", Name: "bridge"},
		{Forge: "github", Owner: "FreaxNx01", Name: "FlowHub-CAS-AISE", Archived: true},
	}
	keys := archivedKeys(refs)
	if len(keys) != 1 {
		t.Fatalf("want 1 archived key, got %d: %+v", len(keys), keys)
	}
	// The key must collide with the local row's key despite the owner casing,
	// which is the whole point of reusing repoRowKey's formula.
	local := repoRow{repo: core.Repo{Forge: "github", Owner: "freaxnx01", Name: "flowhub-cas-aise"}}
	if !keys[repoRowKey(local)] {
		t.Errorf("archived key must match the local row key, got %+v", keys)
	}
}

func TestArchivedKeys_NoneArchived_ReturnsEmpty(t *testing.T) {
	keys := archivedKeys([]forge.RepoRef{{Forge: "github", Owner: "o", Name: "a"}})
	if len(keys) != 0 {
		t.Errorf("want no keys, got %+v", keys)
	}
}

func TestRemoteRows_ArchivedRefsDropped(t *testing.T) {
	rows := remoteRows([]forge.RepoRef{
		{Forge: "github", Owner: "o", Name: "active"},
		{Forge: "github", Owner: "o", Name: "old", Archived: true},
	})
	// An archived repo must never be offered as a clone-on-select row.
	if len(rows) != 1 {
		t.Fatalf("want 1 remote row, got %d: %+v", len(rows), rows)
	}
	if rows[0].remote == nil || rows[0].remote.Name != "active" {
		t.Errorf("wrong row kept: %+v", rows[0])
	}
}

func TestUpdate_RemoteMsg_StoresArchivedSet(t *testing.T) {
	m := initialModel(Config{})
	out, _ := m.Update(remoteMsg{
		rows:     []repoRow{{remote: &forge.RepoRef{Forge: "github", Owner: "o", Name: "a"}}},
		archived: map[string]bool{"github\x00o\x00old": true},
	})
	if got := out.(Model).archived; len(got) != 1 {
		t.Errorf("remoteMsg must store the archived set, got %+v", got)
	}
}

func TestUpdate_RemoteErrMsg_PartialStoresArchivedSet(t *testing.T) {
	m := initialModel(Config{})
	out, _ := m.Update(remoteErrMsg{
		err:      errors.New("one forge down"),
		rows:     []repoRow{{remote: &forge.RepoRef{Forge: "github", Owner: "o", Name: "a"}}},
		archived: map[string]bool{"github\x00o\x00old": true},
	})
	if got := out.(Model).archived; len(got) != 1 {
		t.Errorf("partial refresh must still store the archived set, got %+v", got)
	}
}

func TestBuildSessionRows_NoMatchingSlot_FallsBackToTmuxSessionName(t *testing.T) {
	now := time.Date(2026, 6, 5, 12, 0, 0, 0, time.UTC)
	live := []core.Session{
		{SlotID: "claude", State: "detached", LastActivity: now},
		{SlotID: "bridge", State: "attached", LastActivity: now},
	}
	slots := []core.Slot{
		{ID: "bridge", Repo: "bridge", Worktree: "main", Agent: "claude"},
	}

	rows := buildSessionRows(live, slots, now)

	want := []sessionRow{
		{slotID: "claude", repoLabel: "claude", state: "detached", lastAccessed: humanLastAccessed(0)},
		{slotID: "bridge", repoLabel: "bridge", worktree: "main", agent: "claude", state: "attached", lastAccessed: humanLastAccessed(0)},
	}
	if !reflect.DeepEqual(rows, want) {
		t.Errorf("rows = %#v, want %#v", rows, want)
	}
}

func envHas(env []string, want string) bool {
	for _, e := range env {
		if e == want {
			return true
		}
	}
	return false
}

func TestBuildFetchCmd_TokenForgeWithDirenv_RoutesThroughDirenvWithHelper(t *testing.T) {
	const path = "/repos/ado/Proj/repo"
	cmd := buildFetchCmd(path, "ado", true)

	want := []string{"direnv", "exec", path, "git", "-c", gitauth.CredentialHelper("ado"), "-C", path, "fetch", "--quiet"}
	if !reflect.DeepEqual(cmd.Args, want) {
		t.Errorf("args = %v, want %v", cmd.Args, want)
	}
	if !envHas(cmd.Env, "GIT_TERMINAL_PROMPT=0") {
		t.Errorf("env missing GIT_TERMINAL_PROMPT=0: %v", cmd.Env)
	}
}

func TestBuildFetchCmd_TokenForgeWithoutDirenv_FallsBackToPlainGit(t *testing.T) {
	const path = "/repos/ado/Proj/repo"
	cmd := buildFetchCmd(path, "ado", false)

	want := []string{"git", "-C", path, "fetch", "--quiet"}
	if !reflect.DeepEqual(cmd.Args, want) {
		t.Errorf("args = %v, want %v", cmd.Args, want)
	}
	if !envHas(cmd.Env, "GIT_TERMINAL_PROMPT=0") {
		t.Errorf("env missing GIT_TERMINAL_PROMPT=0: %v", cmd.Env)
	}
}

func TestBuildFetchCmd_ForgeWithoutHelper_UsesPlainGitEvenWithDirenv(t *testing.T) {
	const path = "/repos/gitlab/owner/repo"
	cmd := buildFetchCmd(path, "gitlab", true)

	want := []string{"git", "-C", path, "fetch", "--quiet"}
	if !reflect.DeepEqual(cmd.Args, want) {
		t.Errorf("args = %v, want %v", cmd.Args, want)
	}
	if !envHas(cmd.Env, "GIT_TERMINAL_PROMPT=0") {
		t.Errorf("env missing GIT_TERMINAL_PROMPT=0: %v", cmd.Env)
	}
}
