package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"testing"
)

func TestListRemoteJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[{"name":"bridge","default_branch":"main","html_url":"u"}]`))
	}))
	defer srv.Close()

	root := writeFakeRepos(t)
	cacheDir := t.TempDir()

	cmd := exec.Command("go", "run", ".", "list", "-r", "--refresh", "--json")
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
