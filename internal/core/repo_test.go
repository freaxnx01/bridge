package core

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"
)

func setupFakeRepos(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	layout := []string{
		"github/freaxnx01/public/bridge",
		"github/freaxnx01/private/secret-thing",
		"github/otheruser/public/lib",
		"gitlab/freaxnx01/some-gl-repo",
		"git-forgejo/forgejo-repo",
		"ado/bossinfo-repo",
		"ado/_archive", // must be skipped
	}
	for _, p := range layout {
		full := filepath.Join(root, p)
		if err := os.MkdirAll(full, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	envrcs := []string{
		"github/freaxnx01/public/.envrc",
		"github/freaxnx01/private/.envrc",
		"github/otheruser/public/.envrc",
		"gitlab/freaxnx01/.envrc",
		"git-forgejo/.envrc",
	}
	for _, p := range envrcs {
		if err := os.WriteFile(filepath.Join(root, p), []byte("export TOKEN=x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestDiscoverRepos(t *testing.T) {
	root := setupFakeRepos(t)
	repos, err := DiscoverRepos(root)
	if err != nil {
		t.Fatal(err)
	}
	sort.Slice(repos, func(i, j int) bool { return repos[i].Path < repos[j].Path })

	if len(repos) != 6 {
		t.Fatalf("want 6 repos, got %d: %+v", len(repos), repos)
	}

	want := []struct {
		name, forge, owner, vis string
	}{
		{"forgejo-repo", "forgejo", "freax", ""},
		{"some-gl-repo", "gitlab", "freaxnx01", ""},
		{"lib", "github", "otheruser", "public"},
		{"secret-thing", "github", "freaxnx01", "private"},
		{"bridge", "github", "freaxnx01", "public"},
		{"bossinfo-repo", "ado", "", ""},
	}
	sort.Slice(want, func(i, j int) bool { return want[i].name < want[j].name })
	sort.Slice(repos, func(i, j int) bool { return repos[i].Name < repos[j].Name })

	for i, w := range want {
		if repos[i].Name != w.name || repos[i].Forge != w.forge ||
			repos[i].Owner != w.owner || repos[i].Visibility != w.vis {
			t.Errorf("[%d] got %+v, want %+v", i, repos[i], w)
		}
	}
}

func TestRepoTypeZeroLastUsed(t *testing.T) {
	var r Repo
	if !r.LastUsed.Equal(time.Time{}) {
		t.Errorf("zero LastUsed expected")
	}
}
