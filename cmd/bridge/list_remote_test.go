package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
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
