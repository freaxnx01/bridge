package remote

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
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

// TestEnvFromDirenv_SymlinkedDir_ResolvesVars guards the real-world bug where a
// repos root is a symlink (e.g. ~/repos -> ~/projects/repos): direnv records its
// approval under the canonical path, but `direnv exec` against the symlink path
// reports "blocked", so without resolving the symlink first bridge falls back to
// the (empty) process env and loses BRIDGE_FORGEJO_API.
func TestEnvFromDirenv_SymlinkedDir_ResolvesVars(t *testing.T) {
	if _, err := exec.LookPath("direnv"); err != nil {
		t.Skip("direnv not installed")
	}
	// realDir is the canonical, direnv-allowed dir.
	realDir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	realDir = filepath.Join(realDir, "git-forgejo")
	if err := os.MkdirAll(realDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(realDir, ".envrc"), []byte("export FOO=bar\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Isolate direnv's allow database so the test never touches the host config.
	xdg := t.TempDir()
	t.Setenv("XDG_DATA_HOME", filepath.Join(xdg, "data"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(xdg, "cfg"))
	if out, err := exec.Command("direnv", "allow", realDir).CombinedOutput(); err != nil {
		t.Fatalf("direnv allow: %v: %s", err, out)
	}
	// linkDir is a symlink pointing at realDir's parent — callers reach the
	// .envrc via the symlink path, exactly as a symlinked repos root would.
	linkParent := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(filepath.Dir(realDir), linkParent); err != nil {
		t.Fatal(err)
	}
	symlinkDir := filepath.Join(linkParent, "git-forgejo")

	// FOO must NOT be in the process env, so the only source is the .envrc.
	t.Setenv("FOO", "")

	got := EnvFromDirenv(symlinkDir, []string{"FOO"})
	if got["FOO"] != "bar" {
		t.Fatalf("EnvFromDirenv via symlink = %q, want \"bar\" (symlink not resolved before direnv exec)", got["FOO"])
	}
}

func TestFetchTargetRepos_Forgejo_ResolvesAPIBaseFromEnvrc(t *testing.T) {
	if _, err := exec.LookPath("direnv"); err != nil {
		t.Skip("direnv not installed")
	}
	// A stand-in for the self-hosted Forgejo. The API base lives only in the
	// .envrc (direnv scope), never in the process env — exactly the homelab
	// layout. Without the fix the client falls back to codeberg.org and never
	// hits this server.
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if r.URL.Path != "/api/v1/users/freax/repos" {
			t.Errorf("request path = %q, want /api/v1/users/freax/repos", r.URL.Path)
		}
		_, _ = w.Write([]byte(`[{"name":"obsidian-me","default_branch":"main"}]`))
	}))
	defer srv.Close()

	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dir = filepath.Join(dir, "git-forgejo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	envrc := "export FORGEJO_TOKEN=fj-tok\nexport BRIDGE_FORGEJO_API=" + srv.URL + "\n"
	if err := os.WriteFile(filepath.Join(dir, ".envrc"), []byte(envrc), 0o644); err != nil {
		t.Fatal(err)
	}
	xdg := t.TempDir()
	t.Setenv("XDG_DATA_HOME", filepath.Join(xdg, "data"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(xdg, "cfg"))
	if out, err := exec.Command("direnv", "allow", dir).CombinedOutput(); err != nil {
		t.Fatalf("direnv allow: %v: %s", err, out)
	}
	// The API base must come only from the .envrc, not the process env.
	t.Setenv("BRIDGE_FORGEJO_API", "")

	repos, err := fetchTargetRepos(context.Background(), remoteTarget{Forge: "forgejo", Owner: "freax", Dir: dir})
	if err != nil {
		t.Fatalf("fetchTargetRepos: %v", err)
	}
	if hits == 0 {
		t.Fatal("server never hit: API base not resolved from .envrc (client used codeberg.org default)")
	}
	if len(repos) != 1 || repos[0].Name != "obsidian-me" {
		t.Fatalf("repos = %+v, want one obsidian-me", repos)
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
