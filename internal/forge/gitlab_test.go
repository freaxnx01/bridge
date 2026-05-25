package forge

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGitlabListRepos(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v4/users/freaxnx01/projects" {
			t.Errorf("path %s", r.URL.Path)
		}
		if r.Header.Get("PRIVATE-TOKEN") != "tok" {
			t.Errorf("token %q", r.Header.Get("PRIVATE-TOKEN"))
		}
		w.Write([]byte(`[{"name":"glrepo","default_branch":"main","description":"d","visibility":"public","web_url":"u","ssh_url_to_repo":"s","last_activity_at":"2026-05-01T00:00:00Z"}]`))
	}))
	defer srv.Close()
	c := NewGitlabClient("tok", srv.URL)
	repos, err := c.ListRepos(context.Background(), "freaxnx01")
	if err != nil {
		t.Fatal(err)
	}
	if len(repos) != 1 || repos[0].Forge != "gitlab" || repos[0].Name != "glrepo" {
		t.Errorf("%+v", repos)
	}
}

func TestGitlabListIssues(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawPath != "/api/v4/projects/freaxnx01%2Fglrepo/issues" {
			t.Errorf("path %s", r.URL.RawPath)
		}
		w.Write([]byte(`[{"iid":12,"title":"bug","web_url":"u","labels":["a"],"updated_at":"2026-05-02T00:00:00Z"}]`))
	}))
	defer srv.Close()
	c := NewGitlabClient("tok", srv.URL)
	issues, err := c.ListOpenIssues(context.Background(), "freaxnx01", "glrepo")
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 1 || issues[0].Number != 12 || issues[0].Labels[0] != "a" {
		t.Errorf("%+v", issues)
	}
}
