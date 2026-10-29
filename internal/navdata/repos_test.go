package navdata

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/freaxnx01/bridge/internal/core"
	"github.com/freaxnx01/bridge/internal/forge"
)

func mkRepos(t *testing.T, rel ...string) string {
	t.Helper()
	root := t.TempDir()
	for _, p := range rel {
		if err := os.MkdirAll(filepath.Join(root, p, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func labels(es []RepoEntry) []string {
	out := make([]string, 0, len(es))
	for _, e := range es {
		out = append(out, e.Label)
	}
	return out
}

func TestLocalRepos_SortedDedupedAcrossRoots(t *testing.T) {
	root := mkRepos(t, "github/freaxnx01/public/zeta", "github/freaxnx01/public/alpha", "git-forgejo/notes")
	got := LocalRepos(context.Background(), []string{root, root})
	want := []string{"forgejo/notes", "github/public/alpha", "github/public/zeta"}
	if !reflect.DeepEqual(labels(got), want) {
		t.Errorf("labels = %v, want %v", labels(got), want)
	}
}

func TestLocalRepos_MissingRootIsSkipped(t *testing.T) {
	root := mkRepos(t, "github/freaxnx01/public/alpha")
	got := LocalRepos(context.Background(), []string{filepath.Join(root, "nope"), root})
	if len(got) != 1 || got[0].Repo.Name != "alpha" {
		t.Errorf("got %+v, want only alpha", got)
	}
}

func TestRemoteEntries_PrefixedAndSorted(t *testing.T) {
	got := RemoteEntries([]forge.RepoRef{
		{Forge: "github", Owner: "o", Name: "zz", Visibility: "private"},
		{Forge: "gitlab", Owner: "o", Name: "aa"},
	})
	want := []string{"↓ github/private/zz", "↓ gitlab/o/aa"}
	if !reflect.DeepEqual(labels(got), want) {
		t.Errorf("labels = %v, want %v", labels(got), want)
	}
	if got[0].Remote == nil || got[0].Remote.Name != "zz" {
		t.Errorf("remote ref not carried: %+v", got[0])
	}
}

func TestRemoteRepos_MissingCacheIsEmpty(t *testing.T) {
	got, err := RemoteRepos(filepath.Join(t.TempDir(), "remote.list"))
	if err != nil || len(got) != 0 {
		t.Errorf("got %v, %v; want empty, nil", got, err)
	}
}

func TestPickerList_DedupsClonedRemoteAndDisambiguatesOwners(t *testing.T) {
	local := []RepoEntry{
		{Label: "github/public/tool", Repo: core.Repo{Name: "tool", Forge: "github", Owner: "freaxnx01", Visibility: "public"}},
	}
	remote := RemoteEntries([]forge.RepoRef{
		{Forge: "github", Owner: "FREAXNX01", Name: "tool", Visibility: "public"}, // already cloned (case differs)
		{Forge: "github", Owner: "acme", Name: "tool", Visibility: "public"},      // same label, other owner
	})
	got := labels(PickerList(local, remote, "", ""))
	want := []string{"github/public/freaxnx01/tool", "↓ github/public/acme/tool"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("labels = %v, want %v", got, want)
	}
}

func TestPickerList_ForgeAndQuery(t *testing.T) {
	local := []RepoEntry{
		{Label: "github/public/bridge", Repo: core.Repo{Name: "bridge", Forge: "github"}},
		{Label: "forgejo/bridge", Repo: core.Repo{Name: "bridge", Forge: "forgejo"}},
		{Label: "forgejo/notes", Repo: core.Repo{Name: "notes", Forge: "forgejo"}},
	}
	got := labels(PickerList(local, nil, "forgejo", "BRI"))
	if !reflect.DeepEqual(got, []string{"forgejo/bridge"}) {
		t.Errorf("labels = %v", got)
	}
}

func TestPresentForges_CanonicalOrder(t *testing.T) {
	local := []RepoEntry{{Repo: core.Repo{Forge: "forgejo"}}, {Repo: core.Repo{Forge: "github"}}}
	got := PresentForges(local, nil)
	want := []ForgeOpt{{"github", "GitHub"}, {"forgejo", "Forgejo"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestRecentPaths_MissingFileIsEmpty(t *testing.T) {
	if got := RecentPaths(filepath.Join(t.TempDir(), "mru")); len(got) != 0 {
		t.Errorf("got %v, want empty", got)
	}
}
