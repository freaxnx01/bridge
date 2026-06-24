package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidRepoName(t *testing.T) {
	ok := []string{"foo", "foo-bar", "foo_bar.baz", "A1"}
	bad := []string{"", "foo bar", "foo/bar", "foo;rm", "..", "foo$x", "-foo", "--public", "-h"}
	for _, n := range ok {
		if !validRepoName(n) {
			t.Errorf("want valid: %q", n)
		}
	}
	for _, n := range bad {
		if validRepoName(n) {
			t.Errorf("want invalid: %q", n)
		}
	}
}

func TestCreateForgejoEndToEnd(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "git-forgejo"), 0o755); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(201)
		_, _ = w.Write([]byte(`{"name":"foo","private":true,"ssh_url":"ssh://x/foo.git",
			"html_url":"https://h/foo","owner":{"login":"freax"}}`))
	}))
	defer srv.Close()

	t.Setenv("BRIDGE_REPOS_ROOT", root)
	t.Setenv("BRIDGE_FORGEJO_API", srv.URL)
	t.Setenv("FORGEJO_TOKEN", "T")

	var gotURL, gotTarget string
	old := cloneFn
	cloneFn = func(sshURL, target string) error { gotURL, gotTarget = sshURL, target; return nil }
	defer func() { cloneFn = old }()

	cmd := newCreateCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"foo", "--forge", "forgejo", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if gotURL != "ssh://x/foo.git" {
		t.Fatalf("clone url = %q", gotURL)
	}
	wantTarget := filepath.Join(root, "git-forgejo", "foo")
	if gotTarget != wantTarget {
		t.Fatalf("clone target = %q want %q", gotTarget, wantTarget)
	}
	if !bytes.Contains(out.Bytes(), []byte(`"forge": "forgejo"`)) {
		t.Fatalf("json out = %s", out.String())
	}
}

// TestCreateForgejo_ApiUrlFromEnvrc verifies that BRIDGE_FORGEJO_API set inside
// the git-forgejo .envrc (alongside FORGEJO_TOKEN) is honored — the historical
// bug was that bridge only read it from the process env, so a self-hosted user
// whose .envrc set the URL still got routed to codeberg.org by default.
func TestCreateForgejo_ApiUrlFromEnvrc(t *testing.T) {
	if _, err := exec.LookPath("direnv"); err != nil {
		t.Skip("direnv not installed")
	}
	root := t.TempDir()
	forgeDir := filepath.Join(root, "git-forgejo")
	if err := os.MkdirAll(forgeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	var hit bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit = true
		w.WriteHeader(201)
		_, _ = w.Write([]byte(`{"name":"foo","private":true,"ssh_url":"ssh://x/foo.git",
			"html_url":"https://h/foo","owner":{"login":"freax"}}`))
	}))
	defer srv.Close()

	envrc := "export FORGEJO_TOKEN=envrc-tok\nexport BRIDGE_FORGEJO_API=" + srv.URL + "\n"
	if err := os.WriteFile(filepath.Join(forgeDir, ".envrc"), []byte(envrc), 0o644); err != nil {
		t.Fatal(err)
	}
	// Isolate direnv's allow database to a tempdir so the test never touches the
	// user's real direnv config.
	xdg := t.TempDir()
	t.Setenv("XDG_DATA_HOME", filepath.Join(xdg, "data"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(xdg, "cfg"))
	if out, err := exec.Command("direnv", "allow", forgeDir).CombinedOutput(); err != nil {
		t.Fatalf("direnv allow: %v: %s", err, out)
	}

	t.Setenv("BRIDGE_REPOS_ROOT", root)
	// Crucially: NO BRIDGE_FORGEJO_API / FORGEJO_TOKEN in the process env — the
	// only source must be the .envrc.
	t.Setenv("BRIDGE_FORGEJO_API", "")
	t.Setenv("FORGEJO_TOKEN", "")

	old := cloneFn
	cloneFn = func(string, string) error { return nil }
	defer func() { cloneFn = old }()

	cmd := newCreateCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetArgs([]string{"foo", "--forge", "forgejo", "--json"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("create: %v", err)
	}
	if !hit {
		t.Fatal("create did not call the .envrc BRIDGE_FORGEJO_API server — bridge fell back to a different URL")
	}
}

// TestCreateForgejo_EmptyApiUrlErrors verifies that when a Forgejo token
// resolves but no BRIDGE_FORGEJO_API is set, create fails fast with a clear
// message instead of silently falling back to the codeberg.org default and
// sending the self-hosted token to the wrong server.
func TestCreateForgejo_EmptyApiUrlErrors(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "git-forgejo"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BRIDGE_REPOS_ROOT", root)
	t.Setenv("BRIDGE_FORGEJO_API", "")
	t.Setenv("FORGEJO_TOKEN", "T")

	old := cloneFn
	cloneFn = func(string, string) error {
		t.Fatal("clone should not run when BRIDGE_FORGEJO_API is empty")
		return nil
	}
	defer func() { cloneFn = old }()

	_, _, err := createAndClone(context.Background(), "foo", "forgejo", true)
	if err == nil {
		t.Fatal("want error when BRIDGE_FORGEJO_API is empty")
	}
	if !strings.Contains(err.Error(), "BRIDGE_FORGEJO_API") {
		t.Fatalf("error should mention BRIDGE_FORGEJO_API, got: %v", err)
	}
}

func TestCreateGithubPublicTargetDir(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "github", "freaxnx01", "public"), 0o755); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(201)
		_, _ = w.Write([]byte(`{"name":"foo","visibility":"public","ssh_url":"git@gh:freaxnx01/foo.git",
			"html_url":"https://h/foo","owner":{"login":"freaxnx01"}}`))
	}))
	defer srv.Close()

	t.Setenv("BRIDGE_REPOS_ROOT", root)
	t.Setenv("BRIDGE_GITHUB_API", srv.URL)
	t.Setenv("GH_TOKEN", "T")

	var gotTarget string
	old := cloneFn
	cloneFn = func(sshURL, target string) error { gotTarget = target; return nil }
	defer func() { cloneFn = old }()

	cmd := newCreateCmd()
	cmd.SetArgs([]string{"foo", "--forge", "github", "--public"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, "github", "freaxnx01", "public", "foo")
	if gotTarget != want {
		t.Fatalf("target = %q want %q", gotTarget, want)
	}
}

func TestCreateRejectsBadForge(t *testing.T) {
	cmd := newCreateCmd()
	cmd.SetArgs([]string{"foo", "--forge", "bitbucket"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("want error for unknown forge")
	}
}

func TestCreateAndClone_GitHub(t *testing.T) {
	// httptest GitHub forge that accepts the create POST.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"name":"proj","visibility":"public","default_branch":"main",
			"html_url":"https://gh/freaxnx01/proj","ssh_url":"git@github.com:freaxnx01/proj.git",
			"owner":{"login":"freaxnx01"}}`))
	}))
	defer srv.Close()
	t.Setenv("BRIDGE_GITHUB_API", srv.URL)
	t.Setenv("GH_TOKEN", "tok")

	// a repos root with a github/<owner>/public dir so githubTargetDir resolves
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "github", githubOwner, "public"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BRIDGE_REPOS_ROOT", root)

	var clonedURL, clonedTarget string
	orig := cloneFn
	cloneFn = func(sshURL, target string) error { clonedURL, clonedTarget = sshURL, target; return nil }
	defer func() { cloneFn = orig }()

	repo, ref, err := createAndClone(context.Background(), "proj", "github", false)
	if err != nil {
		t.Fatal(err)
	}
	if clonedURL != "git@github.com:freaxnx01/proj.git" {
		t.Errorf("cloned ssh = %q", clonedURL)
	}
	if repo.Name != "proj" || repo.Owner != "freaxnx01" || repo.Forge != "github" || repo.Visibility != "public" {
		t.Errorf("repo = %+v", repo)
	}
	if repo.Path != clonedTarget || repo.Path != filepath.Join(root, "github", githubOwner, "public", "proj") {
		t.Errorf("path = %q (clonedTarget %q)", repo.Path, clonedTarget)
	}
	if ref.HTMLURL != "https://gh/freaxnx01/proj" {
		t.Errorf("ref.HTMLURL = %q", ref.HTMLURL)
	}
}

func TestCreateAndClone_InvalidName(t *testing.T) {
	if _, _, err := createAndClone(context.Background(), "bad name", "github", true); err == nil {
		t.Errorf("invalid name should error")
	}
}
