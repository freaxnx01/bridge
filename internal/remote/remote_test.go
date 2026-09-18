package remote

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/freaxnx01/bridge/internal/core"
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
	metaPath := filepath.Join(t.TempDir(), "repo-meta.json")

	repos, err := Refresh(context.Background(), []string{root}, cachePath, metaPath)
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

func TestRefresh_WritesRepoMetaBesideRemoteList(t *testing.T) {
	root := t.TempDir()
	mustMkdirEnvrc(t, filepath.Join(root, "github", "acme"))
	mustMkRepo(t, root, "github/acme/public/bridge")
	t.Setenv("GH_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")
	cachePath := filepath.Join(t.TempDir(), "remote.list")
	metaPath := filepath.Join(t.TempDir(), "repo-meta.json")

	if _, err := Refresh(context.Background(), []string{root}, cachePath, metaPath); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	// No token, so no refs — but the file must still be written, with an entry
	// absent rather than the file missing.
	if _, err := os.Stat(metaPath); err != nil {
		t.Fatalf("repo-meta.json not written: %v", err)
	}
	if _, err := core.LoadRepoMeta(metaPath); err != nil {
		t.Errorf("written repo-meta.json is not loadable: %v", err)
	}
}

func TestRefresh_MetaWriteFailure_DoesNotFailTheRefresh(t *testing.T) {
	root := t.TempDir()
	mustMkdirEnvrc(t, filepath.Join(root, "github", "acme"))
	t.Setenv("GH_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")
	cachePath := filepath.Join(t.TempDir(), "remote.list")
	// A directory where the file should go: every write to it fails.
	metaDir := t.TempDir()
	metaPath := filepath.Join(metaDir, "repo-meta.json")
	if err := os.MkdirAll(metaPath, 0o755); err != nil {
		t.Fatal(err)
	}

	if _, err := Refresh(context.Background(), []string{root}, cachePath, metaPath); err != nil {
		t.Errorf("a failed meta write must not fail Refresh, got %v", err)
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

// mustMkRepo creates a git-looking repo dir under root so DiscoverRepos finds it.
func mustMkRepo(t *testing.T, root, rel string) string {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Join(p, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestBuildRepoMeta_MatchesRefsToClonesCaseInsensitively(t *testing.T) {
	root := t.TempDir()
	mustMkRepo(t, root, "github/acme/public/bridge")
	now := time.Unix(1789000000, 0)

	got, err := buildRepoMeta([]string{root}, []forge.RepoRef{{
		Forge: "github", Owner: "ACME", Name: "Bridge",
		Description: "repo navigator", Topics: []string{"go"},
		DefaultBranch: "main", SSHURL: "git@github.com:acme/bridge.git",
	}}, nil, now)
	if err != nil {
		t.Fatalf("buildRepoMeta: %v", err)
	}

	entry, ok := got["github/acme/public/bridge"]
	if !ok {
		t.Fatalf("no entry for the clone, got keys %+v", got)
	}
	if entry.Description != "repo navigator" || entry.DefaultBranch != "main" {
		t.Errorf("entry not populated from the ref: %+v", entry)
	}
	if entry.RemoteURL != "git@github.com:acme/bridge.git" {
		t.Errorf("remote_url should come from the ref SSH URL: %+v", entry)
	}
	if entry.FetchedAt != now.Unix() {
		t.Errorf("FetchedAt = %d, want %d", entry.FetchedAt, now.Unix())
	}
}

func TestBuildRepoMeta_UnmatchedCloneKeepsExistingEntry(t *testing.T) {
	root := t.TempDir()
	mustMkRepo(t, root, "github/acme/public/bridge")
	existing := map[string]core.RepoMeta{
		"github/acme/public/bridge": {Description: "from a healthier day", FetchedAt: 1},
	}
	// No refs at all — e.g. the token 401'd this round.
	got, err := buildRepoMeta([]string{root}, nil, existing, time.Unix(1789000000, 0))
	if err != nil {
		t.Fatalf("buildRepoMeta: %v", err)
	}

	entry, ok := got["github/acme/public/bridge"]
	if !ok {
		t.Fatalf("a failed forge must not drop the entry, got %+v", got)
	}
	if entry.Description != "from a healthier day" || entry.FetchedAt != 1 {
		t.Errorf("stale entry must be preserved verbatim: %+v", entry)
	}
}

func TestBuildRepoMeta_DropsEntriesForReposNoLongerOnDisk(t *testing.T) {
	root := t.TempDir()
	mustMkRepo(t, root, "github/acme/public/bridge")
	existing := map[string]core.RepoMeta{
		"github/acme/public/bridge":  {Description: "still here"},
		"github/acme/public/deleted": {Description: "clone is gone"},
	}
	got, err := buildRepoMeta([]string{root}, nil, existing, time.Unix(1789000000, 0))
	if err != nil {
		t.Fatalf("buildRepoMeta: %v", err)
	}

	if _, ok := got["github/acme/public/deleted"]; ok {
		t.Errorf("entry for a vanished clone must be dropped: %+v", got)
	}
	if _, ok := got["github/acme/public/bridge"]; !ok {
		t.Errorf("entry for a present clone must survive: %+v", got)
	}
}

// TestBuildRepoMeta_RefWithoutTopicsKeepsCachedOnes guards the forges that do
// not report every field: ADO carries neither description nor topics, GitLab
// and Forgejo carry no topics. Overwriting the entry wholesale would blank the
// cached data those clones depend on for keyword lookups.
func TestBuildRepoMeta_RefWithoutTopicsKeepsCachedOnes(t *testing.T) {
	root := t.TempDir()
	mustMkRepo(t, root, "ado/platform/deploy-tools")
	existing := map[string]core.RepoMeta{
		"ado/platform/deploy-tools": {
			Description: "deployment helpers",
			Topics:      []string{"infra", "nextgen"},
			RemoteURL:   "git@ssh.dev.azure.com:v3/acme/platform/deploy-tools",
		},
	}
	// An ADO-shaped ref: name/branch only, no description, no topics.
	refs := []forge.RepoRef{{
		Forge: "ado", Owner: "platform", Name: "deploy-tools", DefaultBranch: "main",
	}}

	got, err := buildRepoMeta([]string{root}, refs, existing, time.Unix(1789000000, 0))
	if err != nil {
		t.Fatalf("buildRepoMeta: %v", err)
	}

	entry, ok := got["ado/platform/deploy-tools"]
	if !ok {
		t.Fatalf("no entry for the clone, got keys %+v", got)
	}
	if entry.Description != "deployment helpers" {
		t.Errorf("Description = %q, want the cached one kept (ref carries none)", entry.Description)
	}
	if len(entry.Topics) != 2 || entry.Topics[0] != "infra" {
		t.Errorf("Topics = %+v, want the cached ones kept (ref carries none)", entry.Topics)
	}
	if entry.RemoteURL != "git@ssh.dev.azure.com:v3/acme/platform/deploy-tools" {
		t.Errorf("RemoteURL = %q, want the cached one kept (ref carries none)", entry.RemoteURL)
	}
	if entry.DefaultBranch != "main" {
		t.Errorf("DefaultBranch = %q, want the ref's value", entry.DefaultBranch)
	}
	if entry.FetchedAt != 1789000000 {
		t.Errorf("FetchedAt = %d, want the refresh timestamp", entry.FetchedAt)
	}
}

func TestBuildRepoMeta_UnreadableRoot_ReturnsError(t *testing.T) {
	root := mustUnreadableForgeRoot(t)

	if _, err := buildRepoMeta([]string{root}, nil, nil, time.Unix(1789000000, 0)); err == nil {
		t.Fatal("an unreadable root must surface as an error, not as an empty map")
	}
}

// TestRefresh_UnreadableRoot_LeavesExistingMetaIntact is the consequence of the
// error above: a transient read failure must not truncate the cache to {} and
// strip `bridge open <keyword>` of its meta data until the next good refresh.
func TestRefresh_UnreadableRoot_LeavesExistingMetaIntact(t *testing.T) {
	root := mustUnreadableForgeRoot(t)
	t.Setenv("GH_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")
	cachePath := filepath.Join(t.TempDir(), "remote.list")
	metaPath := filepath.Join(t.TempDir(), "repo-meta.json")
	before := map[string]core.RepoMeta{
		"github/acme/public/bridge": {Description: "written by a healthy refresh", Topics: []string{"go"}},
	}
	if err := core.SaveRepoMeta(metaPath, before); err != nil {
		t.Fatal(err)
	}

	if _, err := Refresh(context.Background(), []string{root}, cachePath, metaPath); err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	after, err := core.LoadRepoMeta(metaPath)
	if err != nil {
		t.Fatalf("LoadRepoMeta: %v", err)
	}
	entry, ok := after["github/acme/public/bridge"]
	if !ok {
		t.Fatalf("a root that failed to enumerate must not wipe the cache, got %+v", after)
	}
	if entry.Description != "written by a healthy refresh" || len(entry.Topics) != 1 {
		t.Errorf("entry must survive verbatim, got %+v", entry)
	}
}

// TestRefresh_WithRef_WritesRefDataIntoRepoMeta drives the whole
// Refresh → buildRepoMeta → SaveRepoMeta seam against a stand-in GitHub API,
// proving a fetched ref actually lands in repo-meta.json on disk.
func TestRefresh_WithRef_WritesRefDataIntoRepoMeta(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/user/repos" {
			t.Errorf("request path = %q, want /user/repos", r.URL.Path)
		}
		_, _ = w.Write([]byte(`[{
			"name":"bridge","default_branch":"main","description":"repo navigator",
			"topics":["go","nextgen"],"ssh_url":"git@github.com:acme/bridge.git",
			"owner":{"login":"acme"}
		}]`))
	}))
	defer srv.Close()

	root := t.TempDir()
	mustMkdirEnvrc(t, filepath.Join(root, "github", "acme"))
	mustMkRepo(t, root, "github/acme/public/bridge")
	t.Setenv("GH_TOKEN", "tok-abc")
	t.Setenv("BRIDGE_GITHUB_API", srv.URL)
	cachePath := filepath.Join(t.TempDir(), "remote.list")
	metaPath := filepath.Join(t.TempDir(), "repo-meta.json")

	if _, err := Refresh(context.Background(), []string{root}, cachePath, metaPath); err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	meta, err := core.LoadRepoMeta(metaPath)
	if err != nil {
		t.Fatalf("LoadRepoMeta: %v", err)
	}
	entry, ok := meta["github/acme/public/bridge"]
	if !ok {
		t.Fatalf("the fetched ref did not reach repo-meta.json, got %+v", meta)
	}
	if entry.Description != "repo navigator" || entry.DefaultBranch != "main" {
		t.Errorf("entry not populated from the ref: %+v", entry)
	}
	if len(entry.Topics) != 2 || entry.Topics[1] != "nextgen" {
		t.Errorf("Topics = %+v, want [go nextgen]", entry.Topics)
	}
	if entry.RemoteURL != "git@github.com:acme/bridge.git" {
		t.Errorf("RemoteURL = %q, want the ref's SSH URL", entry.RemoteURL)
	}
	if entry.FetchedAt == 0 {
		t.Error("FetchedAt must be stamped on a fresh entry")
	}
}

// mustUnreadableForgeRoot returns a repos root whose github/ dir cannot be
// read, the shape DiscoverRepos reports as an error (a permission problem, an
// unmounted or NFS-stalled root).
func mustUnreadableForgeRoot(t *testing.T) string {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory permissions")
	}
	root := t.TempDir()
	forgeDir := filepath.Join(root, "github")
	if err := os.MkdirAll(forgeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(forgeDir, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(forgeDir, 0o755) }) // let TempDir cleanup remove it
	return root
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
