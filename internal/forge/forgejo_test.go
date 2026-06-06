package forge

import (
	"context"
	"encoding/json"
	"errors"
	"io"
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

func TestForgejoCreateRepo(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/api/v1/user/repos" {
			t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "token T" {
			t.Fatalf("missing token auth: %q", r.Header.Get("Authorization"))
		}
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &gotBody)
		w.WriteHeader(201)
		_, _ = w.Write([]byte(`{"name":"foo","private":true,"default_branch":"main",
			"html_url":"https://git/h/foo","ssh_url":"ssh://git@git/h/foo.git",
			"owner":{"login":"freax"}}`))
	}))
	defer srv.Close()

	c := NewForgejoClient("T", srv.URL)
	ref, err := c.CreateRepo(context.Background(), "foo", true)
	if err != nil {
		t.Fatal(err)
	}
	if gotBody["private"] != true || gotBody["auto_init"] != true {
		t.Fatalf("body = %v", gotBody)
	}
	if ref.Name != "foo" || ref.Owner != "freax" || ref.Visibility != "private" {
		t.Fatalf("ref = %+v", ref)
	}
	if ref.SSHURL == "" {
		t.Fatal("missing ssh_url")
	}
}

func TestForgejoCreateRepoConflict(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
	}))
	defer srv.Close()
	_, err := NewForgejoClient("T", srv.URL).CreateRepo(context.Background(), "foo", true)
	if !errors.Is(err, ErrRepoExists) {
		t.Fatalf("want ErrRepoExists, got %v", err)
	}
}
