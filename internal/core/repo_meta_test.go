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
