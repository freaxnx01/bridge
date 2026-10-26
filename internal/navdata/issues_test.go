package navdata

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/freaxnx01/bridge/internal/forge"
)

func TestIssuesCached_FreshCacheSkipsFetch(t *testing.T) {
	dir := t.TempDir()
	if err := forge.WriteIssueCache(IssueCacheFile(dir, "github", "o", "r"),
		forge.IssueCache{UpdatedAt: time.Now(), Issues: []forge.Issue{{Number: 7}}}); err != nil {
		t.Fatal(err)
	}
	called := false
	src := IssueSource{CacheDir: dir, Fetch: func(context.Context, string, string, string) ([]forge.Issue, error) {
		called = true
		return nil, nil
	}}
	got := IssuesCached(context.Background(), src, "github", "o", "r")
	if called || len(got) != 1 || got[0].Number != 7 {
		t.Errorf("got %+v (fetch called=%v), want cached #7 without fetch", got, called)
	}
}

func TestIssuesCached_FetchErrorFallsBackToStaleCache(t *testing.T) {
	dir := t.TempDir()
	if err := forge.WriteIssueCache(IssueCacheFile(dir, "github", "o", "r"),
		forge.IssueCache{UpdatedAt: time.Now().Add(-time.Hour), Issues: []forge.Issue{{Number: 3}}}); err != nil {
		t.Fatal(err)
	}
	src := IssueSource{CacheDir: dir, Fetch: func(context.Context, string, string, string) ([]forge.Issue, error) {
		return nil, errors.New("offline")
	}}
	if got := IssuesCached(context.Background(), src, "github", "o", "r"); len(got) != 1 || got[0].Number != 3 {
		t.Errorf("got %+v, want stale #3", got)
	}
}

func TestIssuesCached_FetchRewritesCache(t *testing.T) {
	dir := t.TempDir()
	src := IssueSource{CacheDir: dir, Fetch: func(context.Context, string, string, string) ([]forge.Issue, error) {
		return []forge.Issue{{Number: 9}}, nil
	}}
	_ = IssuesCached(context.Background(), src, "github", "o", "r")
	c, err := forge.ReadIssueCache(IssueCacheFile(dir, "github", "o", "r"))
	if err != nil || len(c.Issues) != 1 || c.Issues[0].Number != 9 {
		t.Errorf("cache = %+v, %v; want #9", c, err)
	}
}
