package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestIssuesFetchAndCache(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Write([]byte(`[{"number":1,"title":"t","html_url":"u","updated_at":"2026-05-01T00:00:00Z"}]`))
	}))
	defer srv.Close()

	root := writeFakeRepos(t)
	cache := t.TempDir()

	common := append(os.Environ(),
		"BRIDGE_REPOS_ROOT="+root,
		"XDG_CACHE_HOME="+cache,
		"BRIDGE_GITHUB_API="+srv.URL,
		"GH_TOKEN=tok",
		"GITLAB_TOKEN=",
		"FORGEJO_TOKEN=",
	)

	cmd := bridgeCmd("issues", "--json", "--refresh")
	cmd.Env = common
	var sout stringBuf
	cmd.Stdout = &sout
	if err := cmd.Run(); err != nil {
		t.Fatalf("run1: %v", err)
	}
	var issues []map[string]any
	if err := json.Unmarshal([]byte(sout.String()), &issues); err != nil {
		t.Fatalf("json: %v in %s", err, sout.String())
	}
	if len(issues) == 0 {
		t.Errorf("expected issues, got %v", issues)
	}
	if calls == 0 {
		t.Errorf("expected network calls")
	}

	callsBefore := calls
	cmd2 := bridgeCmd("issues", "--json")
	cmd2.Env = common
	var sout2 stringBuf
	cmd2.Stdout = &sout2
	if err := cmd2.Run(); err != nil {
		t.Fatalf("run2: %v", err)
	}
	if calls != callsBefore {
		t.Errorf("expected no additional network calls; before=%d after=%d", callsBefore, calls)
	}
}
