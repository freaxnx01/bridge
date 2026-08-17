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
			"updated_at":"2026-07-22T10:00:00Z","owner":{"login":"freax"}}`))
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
	if ref.UpdatedAt.IsZero() {
		t.Fatalf("ref.UpdatedAt is zero, want populated from response")
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
		_, _ = w.Write([]byte(`{"number":7,"title":"rough idea","html_url":"https://fj.example/freax/notes/issues/7","updated_at":"2026-07-22T10:00:00Z"}`))
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
	if is.Updated.IsZero() {
		t.Fatalf("is.Updated is zero, want populated from response")
	}
}

func TestForgejoUpdateRepo_PatchesOnlyGivenFields(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch || r.URL.Path != "/api/v1/repos/freax/notes" {
			t.Errorf("method=%s path=%s", r.Method, r.URL.Path)
		}
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"description":"new desc","private":false,"html_url":"https://x/freax/notes","updated_at":"2026-01-02T03:04:05Z"}`))
	}))
	defer srv.Close()
	c := NewForgejoClient("tok", srv.URL)

	desc := "new desc"
	repo, err := c.UpdateRepo(context.Background(), "freax", "notes", &desc, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(gotBody) != 1 || gotBody["description"] != "new desc" {
		t.Errorf("want only description in the PATCH body, got %+v", gotBody)
	}
	if repo.Description != "new desc" || repo.Visibility != "public" {
		t.Errorf("unexpected repo: %+v", repo)
	}
	if repo.UpdatedAt.IsZero() {
		t.Error("want a real timestamp, not a Go zero value")
	}
}

func TestForgejoUpdateRepo_ArchivedRoundTrips(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if body["archived"] != true {
			t.Errorf("want archived=true in request body, got %+v", body)
		}
		w.Write([]byte(`{"archived":true,"html_url":"https://x/freax/notes"}`))
	}))
	defer srv.Close()
	c := NewForgejoClient("tok", srv.URL)

	archived := true
	repo, err := c.UpdateRepo(context.Background(), "freax", "notes", nil, nil, &archived)
	if err != nil {
		t.Fatal(err)
	}
	if !repo.Archived {
		t.Errorf("want archived=true in the result, got %+v", repo)
	}
}

func TestForgejoSetTopics_RoundTrips(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || r.URL.Path != "/api/v1/repos/freax/notes/topics" {
			t.Errorf("method=%s path=%s", r.Method, r.URL.Path)
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		topics, _ := body["topics"].([]any)
		if len(topics) != 2 {
			t.Errorf("want 2 topics in request, got %+v", body)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	c := NewForgejoClient("tok", srv.URL)

	topics, err := c.SetTopics(context.Background(), "freax", "notes", []string{"a", "b"})
	if err != nil {
		t.Fatal(err)
	}
	if len(topics) != 2 || topics[0] != "a" {
		t.Errorf("unexpected topics: %+v", topics)
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

func TestForgejoPutFile_CreateAndUpdate(t *testing.T) {
	var gotBodies []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "PUT" {
			t.Errorf("method: %s", r.Method)
		}
		if r.Header.Get("Authorization") != "token tok" {
			t.Errorf("auth %q", r.Header.Get("Authorization"))
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		gotBodies = append(gotBodies, body)
		w.Write([]byte(`{"content":{"html_url":"https://x/created.md"}}`))
	}))
	defer srv.Close()
	c := NewForgejoClient("tok", srv.URL)

	url, err := c.PutFile(context.Background(), "freax", "sandbox", "docs/x.md", []byte("hi"), "docs: add x", "")
	if err != nil || url != "https://x/created.md" {
		t.Fatalf("PutFile create: url=%q err=%v", url, err)
	}
	if _, hasSHA := gotBodies[0]["sha"]; hasSHA {
		t.Errorf("create must not send sha: %v", gotBodies[0])
	}
	if gotBodies[0]["content"] != "aGk=" { // base64("hi")
		t.Errorf("content not base64: %v", gotBodies[0]["content"])
	}

	_, err = c.PutFile(context.Background(), "freax", "sandbox", "docs/x.md", []byte("bye"), "docs: update x", "abc123")
	if err != nil {
		t.Fatalf("PutFile update: %v", err)
	}
	if gotBodies[1]["sha"] != "abc123" {
		t.Errorf("update must send sha, got: %v", gotBodies[1])
	}
}

func TestForgejoPutFile_ErrorResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		w.Write([]byte(`{"message":"sha does not match"}`))
	}))
	defer srv.Close()
	c := NewForgejoClient("tok", srv.URL)

	_, err := c.PutFile(context.Background(), "freax", "sandbox", "docs/x.md", []byte("x"), "msg", "stale-sha")
	if err == nil {
		t.Fatal("want error on 422 response")
	}
}

func TestForgejoListTree_ShallowRoot(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/repos/freax/notes/contents" {
			t.Errorf("path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[{"path":"README.md","type":"file","size":10,"sha":"a1"}]`))
	}))
	defer srv.Close()
	c := NewForgejoClient("tok", srv.URL)

	entries, truncated, err := c.ListTree(context.Background(), "freax", "notes", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if truncated {
		t.Error("shallow listing must never report truncated")
	}
	if len(entries) != 1 || entries[0].Path != "README.md" {
		t.Fatalf("unexpected entries: %+v", entries)
	}
}

func TestForgejoListTree_EmptyRepoReturnsEmptyListNotError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"message":"repository is empty"}`))
	}))
	defer srv.Close()
	c := NewForgejoClient("tok", srv.URL)

	entries, _, err := c.ListTree(context.Background(), "freax", "notes", "", false)
	if err != nil {
		t.Fatalf("empty repo must not error: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("want empty list, got %+v", entries)
	}
}

func TestForgejoListTree_RecursiveResolvesDefaultBranchAndFiltersPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v1/repos/freax/notes":
			w.Write([]byte(`{"default_branch":"main"}`))
		case r.URL.Path == "/api/v1/repos/freax/notes/git/trees/main":
			if r.URL.Query().Get("recursive") != "true" {
				t.Errorf("want recursive=true, got %q", r.URL.RawQuery)
			}
			w.Write([]byte(`{
				"tree": [
					{"path":"README.md","mode":"100644","type":"blob","sha":"a1","size":10},
					{"path":"src","mode":"040000","type":"tree","sha":"a2"},
					{"path":"src/main.go","mode":"100644","type":"blob","sha":"a3","size":20}
				],
				"truncated": false
			}`))
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer srv.Close()
	c := NewForgejoClient("tok", srv.URL)

	entries, truncated, err := c.ListTree(context.Background(), "freax", "notes", "src", true)
	if err != nil {
		t.Fatal(err)
	}
	if truncated {
		t.Error("want truncated=false")
	}
	if len(entries) != 2 {
		t.Fatalf("want entries scoped to the src/ prefix, got %+v", entries)
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

func TestForgejoCloseIssue(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "PATCH" || r.URL.Path != "/api/v1/repos/freaxnx01/bridge/issues/142" {
			t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"number":142,"title":"flicker","state":"closed","html_url":"https://forgejo.example/freaxnx01/bridge/issues/142","updated_at":"2026-07-22T10:00:00Z"}`))
	}))
	defer srv.Close()

	c := NewForgejoClient("T", srv.URL)
	is, err := c.CloseIssue(context.Background(), "freaxnx01", "bridge", 142, "completed")
	if err != nil {
		t.Fatal(err)
	}
	if gotBody["state"] != "closed" {
		t.Errorf("body sent: %+v", gotBody)
	}
	if is.State != "closed" || is.Number != 142 {
		t.Errorf("issue: %+v", is)
	}
	if is.Updated.IsZero() {
		t.Fatalf("is.Updated is zero, want populated from response")
	}
}

func TestForgejoCloseIssue_NeverSendsStateReason(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"number":142,"state":"closed","updated_at":"2026-07-22T10:00:00Z"}`))
	}))
	defer srv.Close()

	c := NewForgejoClient("T", srv.URL)
	if _, err := c.CloseIssue(context.Background(), "freaxnx01", "bridge", 142, "completed"); err != nil {
		t.Fatal(err)
	}
	if _, ok := gotBody["state_reason"]; ok {
		t.Errorf("Forgejo has no state_reason field, got %+v", gotBody)
	}
}

func TestForgejoUpdateIssue(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "PATCH" || r.URL.Path != "/api/v1/repos/freaxnx01/bridge/issues/142" {
			t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"number":142,"title":"new title","html_url":"https://forgejo.example/freaxnx01/bridge/issues/142","updated_at":"2026-07-22T10:00:00Z"}`))
	}))
	defer srv.Close()

	c := NewForgejoClient("T", srv.URL)
	body := "new body"
	is, err := c.UpdateIssue(context.Background(), "freaxnx01", "bridge", 142, nil, &body)
	if err != nil {
		t.Fatal(err)
	}
	if gotBody["body"] != "new body" {
		t.Errorf("body sent: %+v", gotBody)
	}
	if _, ok := gotBody["title"]; ok {
		t.Errorf("title must be omitted when nil, got %+v", gotBody)
	}
	if is.Number != 142 {
		t.Errorf("issue: %+v", is)
	}
}

func TestForgejoAddLabels(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/api/v1/repos/freaxnx01/bridge/issues/142/labels" {
			t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[{"name":"bug"}]`))
	}))
	defer srv.Close()

	c := NewForgejoClient("T", srv.URL)
	labels, err := c.AddLabels(context.Background(), "freaxnx01", "bridge", 142, []string{"bug"})
	if err != nil {
		t.Fatal(err)
	}
	if len(labels) != 1 || labels[0] != "bug" {
		t.Errorf("labels: %+v", labels)
	}
}

func TestForgejoCommentIssue(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/api/v1/repos/freaxnx01/bridge/issues/142/comments" {
			t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":7,"body":"looks good","created_at":"2026-07-22T10:00:00Z"}`))
	}))
	defer srv.Close()

	c := NewForgejoClient("T", srv.URL)
	comment, err := c.CommentIssue(context.Background(), "freaxnx01", "bridge", 142, "looks good")
	if err != nil {
		t.Fatal(err)
	}
	if comment.ID != 7 || comment.Body != "looks good" {
		t.Errorf("comment: %+v", comment)
	}
	if comment.Created.IsZero() {
		t.Fatalf("comment.Created is zero, want populated from response")
	}
}

func TestForgejoGetIssue(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/repos/freax/notes/issues/12":
			w.Write([]byte(`{"number":12,"title":"fix(x)","body":"the body","html_url":"u12","state":"open","labels":[{"name":"bug"}],"updated_at":"2026-08-11T00:00:00Z","created_at":"2026-08-01T00:00:00Z"}`))
		case "/api/v1/repos/freax/notes/issues/12/comments":
			w.Write([]byte(`[{"id":1,"body":"first","poster":{"login":"alice"},"created_at":"2026-08-02T00:00:00Z"},{"id":2,"body":"second","poster":{"login":"bob"},"created_at":"2026-08-03T00:00:00Z"}]`))
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	c := NewForgejoClient("token", srv.URL)
	issue, comments, err := c.GetIssue(context.Background(), "freax", "notes", 12)
	if err != nil {
		t.Fatal(err)
	}
	if issue.Number != 12 || issue.Body != "the body" || issue.State != "open" || issue.Labels[0] != "bug" {
		t.Errorf("issue: %+v", issue)
	}
	if len(comments) != 2 {
		t.Fatalf("want 2 comments, got %d: %+v", len(comments), comments)
	}
	if comments[0].Author != "alice" || comments[1].Author != "bob" {
		t.Errorf("comments: %+v", comments)
	}
}

func TestForgejoGetIssue_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"message":"Not Found"}`))
	}))
	defer srv.Close()

	c := NewForgejoClient("token", srv.URL)
	_, _, err := c.GetIssue(context.Background(), "freax", "notes", 9999)
	if err == nil {
		t.Fatal("want error for a non-existent issue, got nil")
	}
}
