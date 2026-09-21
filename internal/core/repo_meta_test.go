package core

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestLoadRepoMetaMissing(t *testing.T) {
	got, err := LoadRepoMeta(filepath.Join(t.TempDir(), "no-such-file.json"))
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %v, want empty", got)
	}
}

func TestLoadRepoMetaToleratesExtraFields(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "repo-meta.json")
	_ = os.WriteFile(p, []byte(`{
		"github/me/public/bridge": {
			"description": "the bridge",
			"topics": ["dev-tools","cli"],
			"fetched_at": 1779776608
		},
		"github/me/public/foo": {
			"description": "",
			"topics": []
		}
	}`), 0o644)
	got, err := LoadRepoMeta(p)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got["github/me/public/bridge"].Description != "the bridge" {
		t.Errorf("bridge desc: %+v", got["github/me/public/bridge"])
	}
	if !reflect.DeepEqual(got["github/me/public/bridge"].Topics, []string{"dev-tools", "cli"}) {
		t.Errorf("bridge topics: %+v", got["github/me/public/bridge"].Topics)
	}
}

func TestMergeRepoMeta(t *testing.T) {
	root := "/home/me/projects/repos"
	repos := []Repo{
		{Name: "bridge", Path: root + "/github/me/public/bridge", Forge: "github", Owner: "me", Visibility: "public"},
		{Name: "foo", Path: root + "/github/me/public/foo", Forge: "github", Owner: "me", Visibility: "public"},
	}
	meta := map[string]RepoMeta{
		"github/me/public/bridge": {Description: "the bridge", Topics: []string{"cli"}, DefaultBranch: "main", RemoteURL: "https://github.com/me/bridge"},
		// foo intentionally absent — should stay sparse
	}
	got := MergeRepoMeta(repos, []string{root}, meta)
	if got[0].Desc != "the bridge" || got[0].DefaultBranch != "main" || got[0].RemoteURL == "" {
		t.Errorf("bridge enrichment failed: %+v", got[0])
	}
	if got[1].Desc != "" || got[1].DefaultBranch != "" {
		t.Errorf("foo should remain sparse: %+v", got[1])
	}
}

func TestMergeRepoMetaPreservesExisting(t *testing.T) {
	root := "/r"
	repos := []Repo{{Name: "bridge", Path: root + "/p", Desc: "existing", Topics: []string{"x"}}}
	meta := map[string]RepoMeta{"p": {Description: "FROM CACHE", Topics: []string{"y"}}}
	got := MergeRepoMeta(repos, []string{root}, meta)
	if got[0].Desc != "existing" || !reflect.DeepEqual(got[0].Topics, []string{"x"}) {
		t.Errorf("merge clobbered existing values: %+v", got[0])
	}
}

func TestRepoMetaKey_MatchesMergeRepoMetaLookup(t *testing.T) {
	roots := []string{"/home/u/repos", "/home/u/other"}
	tests := []struct {
		name string
		path string
		want string
	}{
		{"under first root", "/home/u/repos/github/acme/public/bridge", "github/acme/public/bridge"},
		{"under second root", "/home/u/other/gitlab/acme/thing", "gitlab/acme/thing"},
		{"under no root falls back to the path", "/elsewhere/repo", "/elsewhere/repo"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := RepoMetaKey(roots, tt.path); got != tt.want {
				t.Errorf("RepoMetaKey = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSaveRepoMeta_RoundTripsThroughLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "repo-meta.json")
	in := map[string]RepoMeta{
		"github/acme/public/bridge": {
			Description:   "repo navigator",
			Topics:        []string{"go", "tui"},
			DefaultBranch: "main",
			RemoteURL:     "git@github.com:acme/bridge.git",
			FetchedAt:     1789000000,
		},
	}
	if err := SaveRepoMeta(path, in); err != nil {
		t.Fatalf("SaveRepoMeta: %v", err)
	}
	got, err := LoadRepoMeta(path)
	if err != nil {
		t.Fatalf("LoadRepoMeta: %v", err)
	}
	if !reflect.DeepEqual(got, in) {
		t.Errorf("round-trip mismatch:\n got %+v\nwant %+v", got, in)
	}
}

func TestSaveRepoMeta_WrittenFileFeedsMergeRepoMeta(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(t.TempDir(), "repo-meta.json")
	repoPath := filepath.Join(root, "github", "acme", "public", "bridge")
	if err := SaveRepoMeta(path, map[string]RepoMeta{
		RepoMetaKey([]string{root}, repoPath): {
			Description:   "repo navigator",
			Topics:        []string{"go"},
			DefaultBranch: "main",
			RemoteURL:     "git@github.com:acme/bridge.git",
		},
	}); err != nil {
		t.Fatalf("SaveRepoMeta: %v", err)
	}
	meta, err := LoadRepoMeta(path)
	if err != nil {
		t.Fatalf("LoadRepoMeta: %v", err)
	}
	// The write side and the read side must agree on the key, or the merge
	// silently does nothing — which is the bug this whole change fixes.
	out := MergeRepoMeta([]Repo{{Name: "bridge", Path: repoPath}}, []string{root}, meta)
	if out[0].Desc != "repo navigator" || out[0].DefaultBranch != "main" {
		t.Errorf("merge did not populate from the written file: %+v", out[0])
	}
	if len(out[0].Topics) != 1 || out[0].Topics[0] != "go" {
		t.Errorf("topics not merged: %+v", out[0].Topics)
	}
}
