package syncer

import (
	"context"
	"errors"
	"testing"

	"github.com/freaxnx01/bridge/internal/core"
)

type fakeRunner struct {
	calls []string
	fail  map[string]error
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
