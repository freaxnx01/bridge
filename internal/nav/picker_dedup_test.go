package nav

import (
	"testing"

	"github.com/freaxnx01/bridge/internal/core"
	"github.com/freaxnx01/bridge/internal/forge"
)

// #124: a repo already cloned locally must not also appear as a remote ("↓")
// row in the picker. Dedup is by forge+owner+name, case-insensitively (local
// owner is derived from the on-disk path, the remote owner from the forge API,
// so casing can differ).
func TestVisibleRepos_RemoteWithLocalClone_IsDeduped(t *testing.T) {
	m := initialModel(Config{})
	m.localRepos = []repoRow{
		{label: "github/public/bridge", repo: core.Repo{Forge: "github", Owner: "freaxnx01", Name: "bridge", Visibility: "public"}},
	}
	m.remoteRepos = []repoRow{
		// same repo as the local clone, owner/name casing differs — must dedup
		{label: "↓ github/public/bridge", remote: &forge.RepoRef{Forge: "github", Owner: "FreaxNx01", Name: "Bridge", Visibility: "public"}},
		// genuinely remote-only — must survive
		{label: "↓ github/public/other", remote: &forge.RepoRef{Forge: "github", Owner: "freaxnx01", Name: "other", Visibility: "public"}},
	}

	got := m.visibleRepos()

	if len(got) != 2 {
		t.Fatalf("visibleRepos returned %d rows, want 2 (local bridge + remote other); the remote clone of bridge should be deduped", len(got))
	}
	for _, r := range got {
		if r.remote != nil && r.remote.Name == "Bridge" {
			t.Errorf("remote bridge (already cloned locally) should be deduped, but it survived")
		}
	}
}
