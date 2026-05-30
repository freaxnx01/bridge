package forge

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGithubListRepos(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Must hit the authenticated-user endpoint with visibility=all so
		// private repos come through — /users/{owner}/repos would hide them.
		if r.URL.Path != "/user/repos" {
			t.Errorf("path: %s", r.URL.Path)
		}
		if r.URL.Query().Get("visibility") != "all" {
			t.Errorf("visibility: %q", r.URL.Query().Get("visibility"))
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[
          {"name":"bridge","default_branch":"main","description":"d","topics":["x"],"visibility":"public","owner":{"login":"freaxnx01"},"html_url":"https://github.com/freaxnx01/bridge","ssh_url":"git@github.com:freaxnx01/bridge.git","updated_at":"2026-05-01T00:00:00Z"},
          {"name":"obsidian-it","default_branch":"main","visibility":"private","owner":{"login":"freaxnx01"},"html_url":"https://github.com/freaxnx01/obsidian-it","updated_at":"2026-05-02T00:00:00Z"}
        ]`))
	}))
	defer srv.Close()

	c := NewGithubClient("token", srv.URL)
	repos, err := c.ListRepos(context.Background(), "freaxnx01")
	if err != nil {
		t.Fatal(err)
	}
	if len(repos) != 2 {
		t.Fatalf("got %d", len(repos))
	}
	if repos[0].Forge != "github" || repos[0].Owner != "freaxnx01" || repos[0].Name != "bridge" {
		t.Errorf("repo[0]: %+v", repos[0])
	}
	// The private repo must be present — this is the obsidian-it regression.
	if repos[1].Name != "obsidian-it" || repos[1].Visibility != "private" {
		t.Errorf("repo[1]: %+v", repos[1])
	}
}

func TestGithubListIssues(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/freaxnx01/bridge/issues" {
			t.Errorf("path: %s", r.URL.Path)
		}
		if r.URL.Query().Get("state") != "open" {
			t.Errorf("state: %s", r.URL.Query().Get("state"))
		}
		w.Write([]byte(`[
          {"number":30,"title":"feat(dashboard)","html_url":"u30","labels":[{"name":"area:tui"}],"updated_at":"2026-05-01T00:00:00Z","pull_request":null},
          {"number":31,"title":"is a PR","html_url":"u31","pull_request":{"url":"x"},"updated_at":"2026-05-02T00:00:00Z"}
        ]`))
	}))
	defer srv.Close()

	c := NewGithubClient("token", srv.URL)
	issues, err := c.ListOpenIssues(context.Background(), "freaxnx01", "bridge")
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 1 {
		t.Fatalf("got %d", len(issues))
	}
	if issues[0].Number != 30 || issues[0].Repo != "freaxnx01/bridge" || issues[0].Labels[0] != "area:tui" {
		t.Errorf("got %+v", issues[0])
	}
}

func TestGithubAuthHeader(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer tok" {
			t.Errorf("auth: %q", got)
		}
		w.Write([]byte(`[]`))
	}))
	defer srv.Close()
	c := NewGithubClient("tok", srv.URL)
	_, _ = c.ListRepos(context.Background(), "x")
}
