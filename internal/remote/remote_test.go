package remote

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/freaxnx01/bridge/internal/forge"
)

func TestDiscoverRemoteTargets_LayoutVariants(t *testing.T) {
	root := t.TempDir()
	// github owner with .envrc at owner level
	mustMkdirEnvrc(t, filepath.Join(root, "github", "acme"))
	// github owner with .envrc only under visibility subdirs (several of them)
	// must collapse to exactly one target (ownerEnvrcDir picks the first marker)
	mustMkdirEnvrc(t, filepath.Join(root, "github", "globex", "public"))
	mustMkdirEnvrc(t, filepath.Join(root, "github", "globex", "private"))
	// gitlab owner with .envrc at owner level
	mustMkdirEnvrc(t, filepath.Join(root, "gitlab", "initech"))
	// forgejo + ado markers at fixed locations
	mustMkdirEnvrc(t, filepath.Join(root, "git-forgejo"))
	mustMkdirEnvrc(t, filepath.Join(root, "ado"))
	// github owner WITHOUT any .envrc -> no target
	if err := os.MkdirAll(filepath.Join(root, "github", "noenv"), 0o755); err != nil {
		t.Fatal(err)
	}

	got := discoverRemoteTargets(root)

	want := map[string]string{ // forge|owner -> present
		"github|acme":    "",
		"github|globex":  "",
		"gitlab|initech": "",
		"forgejo|freax":  "",
		"ado|":           "",
	}
	if len(got) != len(want) {
		t.Fatalf("discoverRemoteTargets returned %d targets, want %d: %+v", len(got), len(want), got)
	}
	for _, tgt := range got {
		key := tgt.Forge + "|" + tgt.Owner
		if _, ok := want[key]; !ok {
			t.Errorf("unexpected target %q (%+v)", key, tgt)
		}
	}
}

func TestRefresh_NoToken_WritesCacheNoNetwork(t *testing.T) {
	root := t.TempDir()
	// A github owner marker but no GH_TOKEN in scope -> fetchTargetRepos returns
	// (nil, nil), so Refresh writes an empty cache without any network call.
	mustMkdirEnvrc(t, filepath.Join(root, "github", "acme"))
	t.Setenv("GH_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")
	cachePath := filepath.Join(t.TempDir(), "remote.list")

	repos, err := Refresh(context.Background(), []string{root}, cachePath)
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if len(repos) != 0 {
		t.Errorf("repos = %d, want 0 (no token)", len(repos))
	}
	if _, err := forge.ReadRepoCache(cachePath); err != nil {
		t.Errorf("cache not written: %v", err)
	}
}

func TestGitHubToken_ResolvesOwnerScope(t *testing.T) {
	root := t.TempDir()
	mustMkdirEnvrc(t, filepath.Join(root, "github", "freaxnx01"))
	t.Setenv("GH_TOKEN", "tok-abc")

	tok, ok := GitHubToken([]string{root}, "freaxnx01")
	if !ok || tok != "tok-abc" {
		t.Errorf("GitHubToken = %q,%v, want tok-abc,true", tok, ok)
	}

	if _, ok := GitHubToken([]string{root}, "nobody"); ok {
		t.Errorf("unknown owner should not resolve")
	}
}

func TestForgejoToken_ResolvesFromGitForgejoDir(t *testing.T) {
	root := t.TempDir()
	mustMkdirEnvrc(t, filepath.Join(root, "git-forgejo"))
	t.Setenv("FORGEJO_TOKEN", "fj-tok")

	tok, ok := ForgejoToken([]string{root})
	if !ok || tok != "fj-tok" {
		t.Errorf("ForgejoToken = %q,%v, want fj-tok,true", tok, ok)
	}
}

func TestForgejoToken_NoneFound(t *testing.T) {
	root := t.TempDir() // no git-forgejo dir
	t.Setenv("FORGEJO_TOKEN", "")
	if _, ok := ForgejoToken([]string{root}); ok {
		t.Errorf("missing git-forgejo dir should not resolve")
	}
}

func mustMkdirEnvrc(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".envrc"), []byte("# test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}
