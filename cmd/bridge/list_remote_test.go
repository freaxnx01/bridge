package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestListRemoteADO(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"value":[{"name":"MyRepo","project":{"name":"MyProject"},"remoteUrl":"https://dev.azure.com/org/MyProject/_git/MyRepo","sshUrl":"","defaultBranch":"refs/heads/main"}],"count":1}`))
	}))
	defer srv.Close()

	root := writeFakeRepos(t)
	cacheDir := t.TempDir()

	cmd := bridgeCmd("list", "-r", "--refresh", "--json")
	cmd.Env = append(os.Environ(),
		"BRIDGE_REPOS_ROOT="+root,
		"XDG_CACHE_HOME="+cacheDir,
		"BRIDGE_GITHUB_API=",
		"GH_TOKEN=",
		"BRIDGE_GITLAB_API=",
		"BRIDGE_FORGEJO_API=",
		"GITLAB_TOKEN=",
		"FORGEJO_TOKEN=",
		"BRIDGE_ADO_API="+srv.URL,
		"AZURE_DEVOPS_EXT_PAT=tok",
		"ADO_PAT=",
	)
	var sout stringBuf
	cmd.Stdout = &sout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	var out struct {
		Remote []map[string]any `json:"remote"`
	}
	if err := json.Unmarshal([]byte(sout.String()), &out); err != nil {
		t.Fatalf("json: %v in %s", err, sout.String())
	}
	if len(out.Remote) == 0 {
		t.Fatal("expected ADO repos in remote, got none")
	}
	found := false
	for _, r := range out.Remote {
		if r["forge"] == "ado" && r["name"] == "MyRepo" {
			found = true
		}
	}
	if !found {
		t.Errorf("ADO repo not in output: %s", sout.String())
	}
}

func TestListRemoteADOText(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"value":[{"name":"MyRepo","project":{"name":"MyProject"},"remoteUrl":"https://dev.azure.com/org/MyProject/_git/MyRepo","sshUrl":"","defaultBranch":"refs/heads/main"}],"count":1}`))
	}))
	defer srv.Close()

	root := writeFakeRepos(t)
	cacheDir := t.TempDir()

	cmd := bridgeCmd("list", "-r", "--refresh")
	cmd.Env = append(os.Environ(),
		"BRIDGE_REPOS_ROOT="+root,
		"XDG_CACHE_HOME="+cacheDir,
		"BRIDGE_GITHUB_API=",
		"GH_TOKEN=",
		"BRIDGE_GITLAB_API=",
		"BRIDGE_FORGEJO_API=",
		"GITLAB_TOKEN=",
		"FORGEJO_TOKEN=",
		"BRIDGE_ADO_API="+srv.URL,
		"AZURE_DEVOPS_EXT_PAT=tok",
		"ADO_PAT=",
	)
	var sout stringBuf
	cmd.Stdout = &sout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(sout.String(), "ado") || !strings.Contains(sout.String(), "MyRepo") {
		t.Errorf("expected ado/MyRepo in text output:\n%s", sout.String())
	}
}

func TestDiscoverRemoteTargets(t *testing.T) {
	root := t.TempDir()
	mustWrite := func(rel string) {
		t.Helper()
		full := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(""), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite("github/freaxnx01/.envrc")      // owner-level layout
	mustWrite("github/orgB/.envrc")           // owner-level layout
	mustWrite("github/nested/public/.envrc")  // visibility-nested layout
	mustWrite("github/nested/private/.envrc") // second visibility dir, same owner
	mustWrite("github/noenvrc/.gitkeep")      // owner dir without .envrc → skipped
	mustWrite("gitlab/freaxnx01/.envrc")
	mustWrite("git-forgejo/.envrc")
	mustWrite("ado/.envrc")

	targets := discoverRemoteTargets(root)
	got := map[string]bool{}
	count := map[string]int{}
	for _, t := range targets {
		key := t.Forge + "|" + t.Owner
		got[key] = true
		count[key]++
	}
	want := []string{"github|freaxnx01", "github|orgB", "github|nested", "gitlab|freaxnx01", "forgejo|freax", "ado|"}
	for _, w := range want {
		if !got[w] {
			t.Errorf("missing target %q in %v", w, got)
		}
	}
	if got["github|noenvrc"] {
		t.Errorf("owner dir without .envrc should be skipped")
	}
	// public + private under the same owner must collapse to one target.
	if count["github|nested"] != 1 {
		t.Errorf("nested owner should yield exactly one target, got %d", count["github|nested"])
	}
}

func TestListRemoteJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[{"name":"bridge","default_branch":"main","html_url":"u"}]`))
	}))
	defer srv.Close()

	root := writeFakeRepos(t)
	cacheDir := t.TempDir()

	cmd := bridgeCmd("list", "-r", "--refresh", "--json")
	cmd.Env = append(os.Environ(),
		"BRIDGE_REPOS_ROOT="+root,
		"XDG_CACHE_HOME="+cacheDir,
		"BRIDGE_GITHUB_API="+srv.URL,
		"GH_TOKEN=tok",
		"BRIDGE_GITLAB_API=",
		"BRIDGE_FORGEJO_API=",
		"GITLAB_TOKEN=",
		"FORGEJO_TOKEN=",
	)
	var sout stringBuf
	cmd.Stdout = &sout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	var out struct {
		Local  []map[string]any `json:"local"`
		Remote []map[string]any `json:"remote"`
	}
	if err := json.Unmarshal([]byte(sout.String()), &out); err != nil {
		t.Fatalf("json: %v in %s", err, sout.String())
	}
	if len(out.Local) == 0 {
		t.Errorf("expected local repos")
	}
	if len(out.Remote) == 0 {
		t.Errorf("expected remote repos")
	}
}
