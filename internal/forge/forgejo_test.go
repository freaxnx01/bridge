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
		w.Write([]byte(`[{"name":"fj","default_branch":"main","description":"d","private":false,"html_url":"u","ssh_url":"s","updated_at":"2026-05-01T00:00:00Z"},{"name":"archived-repo","archived":true,"private":false}]`))
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
	for _, r := range repos {
		if r.Name == "archived-repo" {
			t.Errorf("archived repo should be filtered out: %+v", repos)
		}
	}
}

func TestForgejoListRepos_EscapesOwnerAgainstQueryInjection(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawQuery != "limit=50" {
			t.Errorf("owner leaked into query string: %q", r.URL.RawQuery)
		}
		if want := "/api/v1/users/evil?token=x/repos"; r.URL.Path != want {
			t.Errorf("path = %q, want %q", r.URL.Path, want)
		}
		w.Write([]byte(`[]`))
	}))
	defer srv.Close()
	c := NewForgejoClient("tok", srv.URL)
	if _, err := c.ListRepos(context.Background(), "evil?token=x"); err != nil {
		t.Fatal(err)
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

func TestForgejoCreateIssue(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/api/v1/repos/freax/notes/issues" {
			t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"number":7,"title":"rough idea","html_url":"https://fj.example/freax/notes/issues/7"}`))
	}))
	defer srv.Close()

	c := NewForgejoClient("T", srv.URL)
	is, err := c.CreateIssue(context.Background(), "freax", "notes", "rough idea", "")
	if err != nil {
		t.Fatal(err)
	}
	if gotBody["title"] != "rough idea" {
		t.Errorf("body sent: %+v", gotBody)
	}
	if is.Forge != "forgejo" || is.Repo != "freax/notes" || is.Number != 7 || is.URL != "https://fj.example/freax/notes/issues/7" {
		t.Errorf("issue: %+v", is)
	}
}

func TestForgejoGetFile_FoundAndAbsent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/repos/freax/notes/contents/ideas.md" {
			if r.Header.Get("Authorization") != "token tok" {
				t.Errorf("auth %q", r.Header.Get("Authorization"))
			}
			w.Header().Set("Content-Type", "application/json")
			// base64 of "# Ideas\n\n- one\n"
			w.Write([]byte(`{"sha":"fj123","encoding":"base64","content":"IyBJZGVhcwoKLSBvbmUK"}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"message":"Not Found"}`))
	}))
	defer srv.Close()
	c := NewForgejoClient("tok", srv.URL)

	content, sha, found, err := c.GetFile(context.Background(), "freax", "notes", "ideas.md")
	if err != nil || !found {
		t.Fatalf("GetFile: found=%v err=%v", found, err)
	}
	if sha != "fj123" || string(content) != "# Ideas\n\n- one\n" {
		t.Errorf("got sha=%q content=%q", sha, string(content))
	}

	_, _, found, err = c.GetFile(context.Background(), "freax", "notes", "missing.md")
	if err != nil || found {
		t.Errorf("absent file: found=%v err=%v (want found=false, nil err)", found, err)
	}
}

func TestForgejoGetFile_EscapesPathAgainstQueryInjection(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawQuery != "" {
			t.Errorf("path leaked into query string: %q", r.URL.RawQuery)
		}
		if want := "/api/v1/repos/freax/notes/contents/a/file.md?ref=evil"; r.URL.Path != want {
			t.Errorf("path = %q, want %q", r.URL.Path, want)
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	c := NewForgejoClient("tok", srv.URL)

	_, _, found, err := c.GetFile(context.Background(), "freax", "notes", "a/file.md?ref=evil")
	if err != nil || found {
		t.Errorf("found=%v err=%v", found, err)
	}
}
