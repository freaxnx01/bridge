package forge

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestForgejoListRepos(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/users/freax/repos" {
			t.Errorf("path %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "token tok" {
			t.Errorf("auth %q", r.Header.Get("Authorization"))
		}
		w.Write([]byte(`[{"name":"fj","default_branch":"main","description":"d","private":false,"html_url":"u","ssh_url":"s","updated_at":"2026-05-01T00:00:00Z"}]`))
	}))
	defer srv.Close()
	c := NewForgejoClient("tok", srv.URL)
	repos, err := c.ListRepos(context.Background(), "freax")
	if err != nil {
		t.Fatal(err)
	}
	if len(repos) != 1 || repos[0].Forge != "forgejo" || repos[0].Visibility != "public" {
		t.Errorf("%+v", repos)
	}
}

func TestForgejoListIssues(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/repos/freax/fj/issues" {
			t.Errorf("path %s", r.URL.Path)
		}
		w.Write([]byte(`[{"number":5,"title":"t","html_url":"u","labels":[{"name":"x"}],"updated_at":"2026-05-02T00:00:00Z","pull_request":null}]`))
	}))
	defer srv.Close()
	c := NewForgejoClient("tok", srv.URL)
	issues, err := c.ListOpenIssues(context.Background(), "freax", "fj")
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 1 || issues[0].Number != 5 || issues[0].Labels[0] != "x" {
		t.Errorf("%+v", issues)
	}
}
