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

// Two different owners with the same repo name + visibility base-render
// identically (github/public/ai-instructions) and look like the #124 duplicate,
// but they are genuinely different repos — not deduped. To tell them apart the
// owner is injected (github/<vis>/<owner>/<name>). A uniquely-named repo keeps
// its clean owner-less label.
func TestVisibleRepos_SameNameDifferentOwners_DisambiguatesByOwner(t *testing.T) {
	m := initialModel(Config{})
	m.localRepos = []repoRow{
		{label: "github/public/ai-instructions", repo: core.Repo{Forge: "github", Owner: "freaxnx01", Name: "ai-instructions", Visibility: "public"}},
		{label: "github/public/bridge", repo: core.Repo{Forge: "github", Owner: "freaxnx01", Name: "bridge", Visibility: "public"}},
	}
	m.remoteRepos = []repoRow{
		{label: "↓ github/public/ai-instructions", remote: &forge.RepoRef{Forge: "github", Owner: "acme", Name: "ai-instructions", Visibility: "public"}},
	}

	got := m.visibleRepos()

	have := map[string]bool{}
	for _, r := range got {
		have[r.label] = true
	}
	want := []string{
		"github/public/freaxnx01/ai-instructions",
		"↓ github/public/acme/ai-instructions",
		"github/public/bridge", // unique name: clean label, no owner
	}
	for _, w := range want {
		if !have[w] {
			t.Errorf("want a row labelled %q; got labels %v", w, labelsOf(got))
		}
	}
}

func labelsOf(rows []repoRow) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.label
	}
	return out
}
