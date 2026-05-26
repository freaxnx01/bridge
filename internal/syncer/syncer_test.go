package syncer

import (
	"context"
	"errors"
	"testing"

	"github.com/freaxnx01/bridge/internal/core"
)

type fakeRunner struct {
	calls   []string
	fail    map[string]error
	outputs map[string]string // key → stdout; if set, Output uses this
}

func (f *fakeRunner) Run(ctx context.Context, dir, name string, args ...string) error {
	key := dir + ":" + name + " " + joinArgs(args)
	f.calls = append(f.calls, key)
	if f.fail != nil {
		if err, ok := f.fail[key]; ok {
			return err
		}
	}
	return nil
}

func (f *fakeRunner) Output(ctx context.Context, dir, name string, args ...string) (string, error) {
	key := dir + ":" + name + " " + joinArgs(args)
	f.calls = append(f.calls, key)
	if f.fail != nil {
		if err, ok := f.fail[key]; ok {
			return "", err
		}
	}
	if f.outputs != nil {
		if s, ok := f.outputs[key]; ok {
			return s, nil
		}
	}
	return "", nil
}

func joinArgs(a []string) string {
	s := ""
	for i, x := range a {
		if i > 0 {
			s += " "
		}
		s += x
	}
	return s
}

func TestSyncOneRepoSuccess(t *testing.T) {
	r := &fakeRunner{}
	s := &Syncer{Runner: r}
	repos := []core.Repo{{Name: "bridge", Path: "/r/bridge"}}
	res := s.Run(context.Background(), repos)
	if len(res.Failed) != 0 {
		t.Errorf("expected no failures, got %+v", res)
	}
	if len(r.calls) != 2 {
		t.Errorf("expected 2 calls (fetch+pull), got %v", r.calls)
	}
}

func TestUnpushedFlagsAheadRepos(t *testing.T) {
	r := &fakeRunner{outputs: map[string]string{
		"/r/a:git rev-list --count @{u}..HEAD": "3\n",
		"/r/b:git rev-list --count @{u}..HEAD": "0\n",
	}}
	r.fail = map[string]error{
		"/r/c:git rev-list --count @{u}..HEAD": errors.New("no upstream"),
	}
	s := &Syncer{Runner: r}
	repos := []core.Repo{
		{Name: "a", Path: "/r/a"},
		{Name: "b", Path: "/r/b"},
		{Name: "c", Path: "/r/c"},
	}
	got := s.Unpushed(context.Background(), repos)
	if len(got) != 1 || got[0] != "a" {
		t.Errorf("got %v, want [a]", got)
	}
}

func TestSyncFetchFailureStopsRepo(t *testing.T) {
	r := &fakeRunner{fail: map[string]error{
		"/r/bridge:git fetch --all --prune": errors.New("network"),
	}}
	s := &Syncer{Runner: r}
	repos := []core.Repo{{Name: "bridge", Path: "/r/bridge"}}
	res := s.Run(context.Background(), repos)
	if len(res.Failed) != 1 || res.Failed[0].Repo.Name != "bridge" {
		t.Errorf("expected fetch failure recorded, got %+v", res)
	}
	if len(r.calls) != 1 {
		t.Errorf("expected only fetch call, got %v", r.calls)
	}
}
