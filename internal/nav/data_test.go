package nav

import (
	"reflect"
	"testing"
	"time"

	"github.com/freaxnx01/bridge/internal/core"
	"github.com/freaxnx01/bridge/internal/gitauth"
)

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

	want := append([]string{"direnv", "exec", path, "git"}, gitauth.CredentialArgs("ado")...)
	want = append(want, "-C", path, "fetch", "--quiet")
	if !reflect.DeepEqual(cmd.Args, want) {
		t.Errorf("args = %v, want %v", cmd.Args, want)
	}
	if !envHas(cmd.Env, "GIT_TERMINAL_PROMPT=0") {
		t.Errorf("env missing GIT_TERMINAL_PROMPT=0: %v", cmd.Env)
	}
}

func TestBuildFetchCmd_TokenForgeWithoutDirenv_PlainGitKeepsHelper(t *testing.T) {
	const path = "/repos/git-forgejo/owner/repo"
	cmd := buildFetchCmd(path, "forgejo", false)

	want := append([]string{"git"}, gitauth.CredentialArgs("forgejo")...)
	want = append(want, "-C", path, "fetch", "--quiet")
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
