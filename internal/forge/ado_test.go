package forge

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestADOListRepos(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/_apis/git/repositories" {
			t.Errorf("path: %s", r.URL.Path)
		}
		if r.URL.Query().Get("api-version") != "7.1" {
			t.Errorf("api-version: %s", r.URL.Query().Get("api-version"))
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"value":[
			{"name":"Repo1","project":{"name":"ProjA"},"remoteUrl":"https://dev.azure.com/org/ProjA/_git/Repo1","sshUrl":"git@ssh.dev.azure.com:v3/org/ProjA/Repo1","defaultBranch":"refs/heads/main"},
			{"name":"Repo2","project":{"name":"ProjB"},"remoteUrl":"https://dev.azure.com/org/ProjB/_git/Repo2","sshUrl":"","defaultBranch":"refs/heads/develop"}
		],"count":2}`))
	}))
	defer srv.Close()

	c := NewADOClient("mytoken", srv.URL)
	repos, err := c.ListRepos(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if len(repos) != 2 {
		t.Fatalf("got %d repos", len(repos))
	}
	r := repos[0]
	if r.Forge != "ado" || r.Owner != "ProjA" || r.Name != "Repo1" {
		t.Errorf("repo[0]: %+v", r)
	}
	if r.DefaultBranch != "main" {
		t.Errorf("defaultBranch: %q", r.DefaultBranch)
	}
	if r.HTMLURL != "https://dev.azure.com/org/ProjA/_git/Repo1" {
		t.Errorf("HTMLURL: %q", r.HTMLURL)
	}
	if repos[1].Owner != "ProjB" || repos[1].DefaultBranch != "develop" {
		t.Errorf("repo[1]: %+v", repos[1])
	}
}

func TestADOAuthHeader(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		want := "Basic " + base64.StdEncoding.EncodeToString([]byte(":mytoken"))
		if got := r.Header.Get("Authorization"); got != want {
			t.Errorf("auth: got %q, want %q", got, want)
		}
		w.Write([]byte(`{"value":[],"count":0}`))
	}))
	defer srv.Close()

	c := NewADOClient("mytoken", srv.URL)
	_, _ = c.ListRepos(context.Background(), "")
}

func TestADOListIssues(t *testing.T) {
	c := NewADOClient("tok", "https://dev.azure.com/org")
	issues, err := c.ListOpenIssues(context.Background(), "proj", "repo")
	if err != nil || len(issues) != 0 {
		t.Errorf("expected empty, got %v %v", issues, err)
	}
}
