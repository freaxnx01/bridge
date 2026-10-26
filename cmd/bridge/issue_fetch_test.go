package main

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/freaxnx01/bridge/internal/forge"
	"github.com/freaxnx01/bridge/internal/navdata"
)

type stubIssueClient struct{ issues []forge.Issue }

func (stubIssueClient) Name() string { return "stub" }
func (stubIssueClient) ListRepos(context.Context, string) ([]forge.RepoRef, error) {
	return nil, nil
}
func (c stubIssueClient) ListOpenIssues(context.Context, string, string) ([]forge.Issue, error) {
	return c.issues, nil
}

// No client (no token in env or direnv) is an error, not "no issues": an
// empty result would be cached as fresh and hide the issues for the TTL.
func TestIssueFetcher_NoClientIsAnErrorAndNotCached(t *testing.T) {
	fetch := newIssueFetcher(func(string, string) forge.Client { return nil })
	if _, err := fetch(context.Background(), "github", "o", "r"); !errors.Is(err, navdata.ErrNoForgeClient) {
		t.Fatalf("err = %v, want navdata.ErrNoForgeClient", err)
	}
	dir := t.TempDir()
	src := navdata.IssueSource{CacheDir: dir, Fetch: fetch}
	if got := navdata.IssuesCached(context.Background(), src, "github", "o", "r"); len(got) != 0 {
		t.Errorf("issues = %+v, want none", got)
	}
	if _, err := os.Stat(navdata.IssueCacheFile(dir, "github", "o", "r")); !os.IsNotExist(err) {
		t.Errorf("cache written for a no-client result (stat err = %v)", err)
	}
}

func TestIssueFetcher_UsesResolvedClient(t *testing.T) {
	var gotForge, gotOwner string
	fetch := newIssueFetcher(func(f, o string) forge.Client {
		gotForge, gotOwner = f, o
		return stubIssueClient{issues: []forge.Issue{{Number: 5}}}
	})
	is, err := fetch(context.Background(), "forgejo", "freax", "r")
	if err != nil || len(is) != 1 || is[0].Number != 5 || gotForge != "forgejo" || gotOwner != "freax" {
		t.Errorf("issues = %+v, err = %v, resolved %s/%s", is, err, gotForge, gotOwner)
	}
}

// The process env wins; without it the direnv resolver is asked, once per
// forge:owner (it spawns direnv).
func TestIssueClientResolver_EnvFirstThenDirenvMemoized(t *testing.T) {
	env := stubIssueClient{}
	calls := 0
	r := issueClientResolver(
		func(f string) forge.Client {
			if f == "github" {
				return env
			}
			return nil
		},
		func(string, string) forge.Client { calls++; return stubIssueClient{issues: []forge.Issue{{Number: 1}}} },
	)
	if c := r("github", "o"); c == nil || calls != 0 {
		t.Fatalf("github: client %v, direnv calls %d; want the env client, no direnv", c, calls)
	}
	for i := 0; i < 3; i++ {
		if c := r("forgejo", "o"); c == nil {
			t.Fatal("forgejo: no client from direnv")
		}
	}
	if calls != 1 {
		t.Errorf("direnv resolver calls = %d, want 1 (memoized)", calls)
	}
}
