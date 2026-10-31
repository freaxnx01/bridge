package launch

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/freaxnx01/bridge/internal/core"
)

func env(kv map[string]string) func(string) string {
	return func(k string) string { return kv[k] }
}

func reposDeps(repos ...core.Repo) Deps {
	return Deps{
		Repos:  func() ([]core.Repo, error) { return repos, nil },
		Getenv: env(nil),
	}
}

func TestFindReposByKeyword_NameHitsWin(t *testing.T) {
	repos := []core.Repo{{Name: "bridge"}, {Name: "other", Topics: []string{"bridge"}}}
	got := FindReposByKeyword(repos, "brid")
	if len(got) != 1 || got[0].Name != "bridge" {
		t.Errorf("got %+v, want only the name hit", got)
	}
}

func TestDefaultAgent_AppendsArgsWithoutMutatingRegistry(t *testing.T) {
	spec, ok := DefaultAgent(env(map[string]string{"BRIDGE_DEFAULT_AGENT": "claude", "BRIDGE_DEFAULT_AGENT_ARGS": "--a  --b"}))
	if !ok || spec.Name != "claude" || !reflect.DeepEqual(spec.Args, []string{"--a", "--b"}) {
		t.Fatalf("spec = %+v, ok = %v", spec, ok)
	}
	again, _ := DefaultAgent(env(map[string]string{"BRIDGE_DEFAULT_AGENT": "claude"}))
	if len(again.Args) != 0 {
		t.Errorf("registry spec mutated: %+v", again)
	}
	if _, ok := DefaultAgent(env(nil)); ok {
		t.Error("unset BRIDGE_DEFAULT_AGENT must report ok=false")
	}
}

func TestWithClaudeName(t *testing.T) {
	spec, _ := DefaultAgent(env(map[string]string{"BRIDGE_DEFAULT_AGENT": "claude", "BRIDGE_DEFAULT_AGENT_ARGS": "--x"}))
	got := WithClaudeName(spec, "bridge [wt]")
	if !reflect.DeepEqual(got.Args, []string{"-n", "bridge [wt]", "--x"}) {
		t.Errorf("args = %v", got.Args)
	}
	code, _ := DefaultAgent(env(map[string]string{"BRIDGE_DEFAULT_AGENT": "code"}))
	if got := WithClaudeName(code, "x"); !reflect.DeepEqual(got, code) {
		t.Errorf("non-claude spec changed: %+v", got)
	}
}

func TestResolveTarget_UnknownAndAmbiguous(t *testing.T) {
	d := reposDeps(core.Repo{Name: "alpha", Path: "/a"}, core.Repo{Name: "alpine", Path: "/b"})
	_, err := ResolveTarget(context.Background(), d, "zzz", "", "claude")
	var le RepoLookupError
	if !errors.As(err, &le) || !strings.Contains(err.Error(), `unknown repo "zzz"`) {
		t.Errorf("err = %v, want unknown-repo RepoLookupError", err)
	}
	_, err = ResolveTarget(context.Background(), d, "alp", "", "claude")
	if !errors.As(err, &le) || !strings.Contains(err.Error(), "ambiguous (2 matches): alpha, alpine") {
		t.Errorf("err = %v, want ambiguous RepoLookupError", err)
	}
}

func TestResolveTarget_UnknownAgent(t *testing.T) {
	d := reposDeps(core.Repo{Name: "alpha", Path: t.TempDir()})
	_, err := ResolveTarget(context.Background(), d, "alpha", "", "nope")
	var ae AgentLookupError
	if !errors.As(err, &ae) || !strings.HasPrefix(err.Error(), "bridge: ") {
		t.Errorf("err = %v, want AgentLookupError with bridge: prefix", err)
	}
}

func TestTargetFor_UsesGivenRepoAndDefaultAgent(t *testing.T) {
	d := Deps{Getenv: env(map[string]string{"BRIDGE_DEFAULT_AGENT": "claude"})}
	repo := core.Repo{Name: "bridge", Path: t.TempDir(), Forge: "forgejo"}
	tg, err := TargetFor(context.Background(), d, repo, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if tg.Repo.Forge != "forgejo" || tg.Slot != "bridge" || !tg.HasAgent || tg.AgentName != "claude" || tg.WorkDir != repo.Path {
		t.Errorf("target = %+v", tg)
	}
}

func TestTargetFor_NoAgent(t *testing.T) {
	tg, err := TargetFor(context.Background(), Deps{Getenv: env(nil)}, core.Repo{Name: "x", Path: t.TempDir()}, "", "")
	if err != nil || tg.HasAgent {
		t.Errorf("target = %+v, err = %v; want HasAgent=false", tg, err)
	}
}

func TestFinalize_LabelSlotAndMRU(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", t.TempDir())
	cache := t.TempDir()
	d := Deps{CacheRoot: cache, Getenv: env(map[string]string{"BRIDGE_DEFAULT_AGENT": "claude"})}
	repo := core.Repo{Name: "bridge", Path: t.TempDir()}
	tg, err := TargetFor(context.Background(), d, repo, "", "")
	if err != nil {
		t.Fatal(err)
	}
	tg.Label = "#7 [fix]"
	Finalize(context.Background(), d, &tg, true)
	if !reflect.DeepEqual(tg.Spec.Args, []string{"-n", "#7 [fix]"}) {
		t.Errorf("args = %v, want the -n label", tg.Spec.Args)
	}
	slots, err := core.LoadSlots(filepath.Join(cache, "slots.json"))
	if err != nil || len(slots) != 1 || slots[0].ID != "bridge" || slots[0].Agent != "claude" {
		t.Errorf("slots = %+v, %v", slots, err)
	}
	mru, _ := core.LoadMRU(filepath.Join(cache, "mru"))
	if len(mru) != 1 || mru[0] != repo.Path {
		t.Errorf("mru = %v, want [%s]", mru, repo.Path)
	}
}

func TestSessionLive(t *testing.T) {
	d := Deps{Live: func() ([]core.Session, error) { return []core.Session{{SlotID: "a"}}, nil }}
	if !SessionLive(d, "a") || SessionLive(d, "b") || SessionLive(Deps{}, "a") {
		t.Error("SessionLive mismatch")
	}
}
