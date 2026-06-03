package nav

import (
	"reflect"
	"testing"

	"github.com/freaxnx01/bridge/internal/gitauth"
)

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
