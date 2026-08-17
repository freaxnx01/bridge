package forge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
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

func TestGithubListProjectV2Items_PaginatesAndMaps(t *testing.T) {
	page := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/graphql" {
			t.Errorf("path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		page++
		if page == 1 {
			w.Write([]byte(`{"data":{"user":{"projectV2":{"items":{
              "pageInfo":{"hasNextPage":true,"endCursor":"C1"},
              "nodes":[
                {"content":{"__typename":"Issue","title":"an issue","url":"https://x/1","repository":{"nameWithOwner":"freaxnx01/bridge"}},
                 "fieldValues":{"nodes":[{"__typename":"ProjectV2ItemFieldSingleSelectValue","name":"In Progress","field":{"name":"Status"}}]}},
                {"content":{"__typename":"DraftIssue","title":"a draft idea"},
                 "fieldValues":{"nodes":[{"__typename":"ProjectV2ItemFieldSingleSelectValue","name":"Todo","field":{"name":"Status"}}]}}
              ]}}}}}`))
			return
		}
		w.Write([]byte(`{"data":{"user":{"projectV2":{"items":{
          "pageInfo":{"hasNextPage":false,"endCursor":"C2"},
          "nodes":[
            {"content":{"__typename":"PullRequest","title":"a pr","url":"https://x/2","repository":{"nameWithOwner":"freaxnx01/agent-os"}},
             "fieldValues":{"nodes":[]}}
          ]}}}}}`))
	}))
	defer srv.Close()

	c := NewGithubClient("token", srv.URL)
	items, err := c.ListProjectV2Items(context.Background(), "freaxnx01", 5)
	if err != nil {
		t.Fatal(err)
	}
	if page != 2 {
		t.Errorf("expected 2 pages fetched, got %d", page)
	}
	if len(items) != 3 {
		t.Fatalf("got %d items, want 3", len(items))
	}
	if items[0].Type != "Issue" || items[0].Repo != "freaxnx01/bridge" || items[0].URL != "https://x/1" || items[0].Status != "In Progress" {
		t.Errorf("item[0]: %+v", items[0])
	}
	if items[1].Type != "DraftIssue" || items[1].Title != "a draft idea" || items[1].Status != "Todo" || items[1].Repo != "" {
		t.Errorf("item[1]: %+v", items[1])
	}
	if items[2].Type != "PullRequest" || items[2].Repo != "freaxnx01/agent-os" || items[2].Status != "" {
		t.Errorf("item[2]: %+v", items[2])
	}
}

func TestGithubGraphQL_SurfacesErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"errors":[{"message":"Your token has not been granted the required scopes"}]}`))
	}))
	defer srv.Close()
	c := NewGithubClient("token", srv.URL)
	_, err := c.ListProjectV2Items(context.Background(), "freaxnx01", 5)
	if err == nil {
		t.Fatal("expected error from graphql errors array")
	}
	if !strings.Contains(err.Error(), "scopes") {
		t.Errorf("error should surface the graphql message, got: %v", err)
	}
}

func TestGithubGetFile_FoundAndAbsent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/repos/freaxnx01/bridge/contents/ideas.md" {
			w.Header().Set("Content-Type", "application/json")
			// base64 of "# Ideas\n\n- one\n" (with a newline in the b64, as GitHub returns)
			w.Write([]byte(`{"sha":"abc123","html_url":"https://x/ideas.md","content":"IyBJZGVhcwoKLSBvbmUK\n"}`))
			return
		}
		w.WriteHeader(404)
		w.Write([]byte(`{"message":"Not Found"}`))
	}))
	defer srv.Close()
	c := NewGithubClient("token", srv.URL)

	content, sha, found, err := c.GetFile(context.Background(), "freaxnx01", "bridge", "ideas.md")
	if err != nil || !found {
		t.Fatalf("GetFile: found=%v err=%v", found, err)
	}
	if sha != "abc123" || string(content) != "# Ideas\n\n- one\n" {
		t.Errorf("got sha=%q content=%q", sha, string(content))
	}

	_, _, found, err = c.GetFile(context.Background(), "freaxnx01", "bridge", "missing.md")
	if err != nil || found {
		t.Errorf("absent file: found=%v err=%v (want found=false, nil err)", found, err)
	}
}

func TestGithubGetFile_EscapesPathAgainstQueryInjection(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawQuery != "" {
			t.Errorf("path leaked into query string: %q", r.URL.RawQuery)
		}
		if want := "/repos/freaxnx01/bridge/contents/a/file.md?ref=evil"; r.URL.Path != want {
			t.Errorf("path = %q, want %q", r.URL.Path, want)
		}
		w.WriteHeader(404)
	}))
	defer srv.Close()
	c := NewGithubClient("token", srv.URL)

	_, _, found, err := c.GetFile(context.Background(), "freaxnx01", "bridge", "a/file.md?ref=evil")
	if err != nil || found {
		t.Errorf("found=%v err=%v", found, err)
	}
}

func TestGithubSearchCode_ScopesQueryToRepo(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/search/code":
			gotQuery = r.URL.Query().Get("q")
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"incomplete_results":false,"items":[
				{"path":"foo.go","repository":{"full_name":"o/r"}}
			]}`))
		case r.URL.Path == "/repos/o/r/contents/foo.go":
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"sha":"s1","content":"cGFja2FnZSBmb28KCmZ1bmMgeCgpIHt9Cg=="}`)) // "package foo\n\nfunc x() {}\n"
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer srv.Close()
	c := NewGithubClient("token", srv.URL)

	matches, incomplete, err := c.SearchCode(context.Background(), "o", "r", "func x")
	if err != nil {
		t.Fatal(err)
	}
	if gotQuery != "func x repo:o/r" {
		t.Errorf("query = %q, want %q", gotQuery, "func x repo:o/r")
	}
	if incomplete {
		t.Error("want incomplete=false")
	}
	if len(matches) != 1 || matches[0].Line != 3 || matches[0].Repo != "o/r" || matches[0].Path != "foo.go" {
		t.Fatalf("unexpected matches: %+v", matches)
	}
}

func TestGithubSearchCode_ScopesQueryToOwnerWithoutRepo(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query().Get("q")
		w.Write([]byte(`{"incomplete_results":false,"items":[]}`))
	}))
	defer srv.Close()
	c := NewGithubClient("token", srv.URL)

	_, _, err := c.SearchCode(context.Background(), "o", "", "func x")
	if err != nil {
		t.Fatal(err)
	}
	if gotQuery != "func x user:o" {
		t.Errorf("query = %q, want %q", gotQuery, "func x user:o")
	}
}

func TestGithubSearchCode_IncompleteResultsSurfaced(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"incomplete_results":true,"items":[]}`))
	}))
	defer srv.Close()
	c := NewGithubClient("token", srv.URL)

	_, incomplete, err := c.SearchCode(context.Background(), "o", "", "x")
	if err != nil {
		t.Fatal(err)
	}
	if !incomplete {
		t.Error("want incomplete=true to be surfaced, not silently dropped")
	}
}

func TestGithubSearchCode_RateLimitIsDistinctError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"message":"You have exceeded a secondary rate limit. Please wait a few minutes before you try again."}`))
	}))
	defer srv.Close()
	c := NewGithubClient("token", srv.URL)

	_, _, err := c.SearchCode(context.Background(), "o", "", "x")
	if !errors.Is(err, ErrSearchRateLimited) {
		t.Fatalf("want ErrSearchRateLimited, got %v", err)
	}
}

func TestGithubSearchCode_OtherForbiddenIsNotRateLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"message":"Must have push access to view repository issue votes."}`))
	}))
	defer srv.Close()
	c := NewGithubClient("token", srv.URL)

	_, _, err := c.SearchCode(context.Background(), "o", "", "x")
	if err == nil {
		t.Fatal("want an error")
	}
	if errors.Is(err, ErrSearchRateLimited) {
		t.Fatal("a non-rate-limit 403 must not be reported as rate limited")
	}
}

func TestGithubListTree_ShallowRoot(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/o/r/contents" {
			t.Errorf("path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[
			{"path":"README.md","type":"file","size":10,"sha":"a1"},
			{"path":"internal","type":"dir","size":0,"sha":"a2"}
		]`))
	}))
	defer srv.Close()
	c := NewGithubClient("token", srv.URL)

	entries, truncated, err := c.ListTree(context.Background(), "o", "r", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if truncated {
		t.Error("shallow listing must never report truncated")
	}
	if len(entries) != 2 || entries[0].Path != "README.md" || entries[0].Type != "file" || entries[1].Type != "dir" {
		t.Fatalf("unexpected entries: %+v", entries)
	}
}

func TestGithubListTree_ShallowSubdirectory(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/o/r/contents/internal" {
			t.Errorf("path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[{"path":"internal/mcp","type":"dir","size":0,"sha":"b1"}]`))
	}))
	defer srv.Close()
	c := NewGithubClient("token", srv.URL)

	entries, _, err := c.ListTree(context.Background(), "o", "r", "internal", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Path != "internal/mcp" {
		t.Fatalf("unexpected entries: %+v", entries)
	}
}

func TestGithubListTree_EmptyRepoReturnsEmptyListNotError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"message":"This repository is empty."}`))
	}))
	defer srv.Close()
	c := NewGithubClient("token", srv.URL)

	entries, truncated, err := c.ListTree(context.Background(), "o", "r", "", false)
	if err != nil {
		t.Fatalf("empty repo must not error: %v", err)
	}
	if truncated {
		t.Error("empty repo must not report truncated")
	}
	if len(entries) != 0 {
		t.Fatalf("want empty list, got %+v", entries)
	}
}

func TestGithubListTree_MissingPathIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"message":"Not Found"}`))
	}))
	defer srv.Close()
	c := NewGithubClient("token", srv.URL)

	_, _, err := c.ListTree(context.Background(), "o", "r", "nope", false)
	if err == nil {
		t.Fatal("want an error for a genuinely missing path, got nil")
	}
}

func TestGithubListTree_RecursiveResolvesDefaultBranchAndFiltersPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/repos/o/r":
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"default_branch":"main"}`))
		case r.URL.Path == "/repos/o/r/git/trees/main":
			if r.URL.Query().Get("recursive") != "1" {
				t.Errorf("want recursive=1, got %q", r.URL.RawQuery)
			}
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{
				"tree": [
					{"path":"README.md","mode":"100644","type":"blob","sha":"a1","size":10},
					{"path":"internal","mode":"040000","type":"tree","sha":"a2"},
					{"path":"internal/mcp","mode":"040000","type":"tree","sha":"a3"},
					{"path":"internal/mcp/tools.go","mode":"100644","type":"blob","sha":"a4","size":20},
					{"path":"vendor/lib","mode":"160000","type":"commit","sha":"a5"},
					{"path":"link","mode":"120000","type":"blob","sha":"a6","size":5}
				],
				"truncated": true
			}`))
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer srv.Close()
	c := NewGithubClient("token", srv.URL)

	entries, truncated, err := c.ListTree(context.Background(), "o", "r", "internal", true)
	if err != nil {
		t.Fatal(err)
	}
	if !truncated {
		t.Error("want truncated=true to be surfaced, not silently dropped")
	}
	if len(entries) != 3 {
		t.Fatalf("want entries scoped to the internal/ prefix, got %+v", entries)
	}
	byPath := map[string]TreeEntry{}
	for _, e := range entries {
		byPath[e.Path] = e
	}
	if byPath["internal"].Type != "dir" {
		t.Errorf("want internal to be dir, got %+v", byPath["internal"])
	}
	if byPath["internal/mcp/tools.go"].Type != "file" {
		t.Errorf("want internal/mcp/tools.go to be file, got %+v", byPath["internal/mcp/tools.go"])
	}
}

func TestGithubListTree_RecursiveTypeMapping(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/repos/o/r":
			w.Write([]byte(`{"default_branch":"main"}`))
		case r.URL.Path == "/repos/o/r/git/trees/main":
			w.Write([]byte(`{
				"tree": [
					{"path":"file.txt","mode":"100644","type":"blob","sha":"a1","size":1},
					{"path":"dir","mode":"040000","type":"tree","sha":"a2"},
					{"path":"sub","mode":"160000","type":"commit","sha":"a3"},
					{"path":"link","mode":"120000","type":"blob","sha":"a4","size":1}
				],
				"truncated": false
			}`))
		}
	}))
	defer srv.Close()
	c := NewGithubClient("token", srv.URL)

	entries, _, err := c.ListTree(context.Background(), "o", "r", "", true)
	if err != nil {
		t.Fatal(err)
	}
	byPath := map[string]string{}
	for _, e := range entries {
		byPath[e.Path] = e.Type
	}
	want := map[string]string{"file.txt": "file", "dir": "dir", "sub": "submodule", "link": "symlink"}
	for path, wantType := range want {
		if byPath[path] != wantType {
			t.Errorf("%s: got type %q, want %q", path, byPath[path], wantType)
		}
	}
}

func TestGithubListTree_RecursiveEmptyRepoReturnsEmptyListNotError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/repos/o/r":
			w.Write([]byte(`{"default_branch":"main"}`))
		case r.URL.Path == "/repos/o/r/git/trees/main":
			w.WriteHeader(http.StatusConflict)
			w.Write([]byte(`{"message":"Git Repository is empty."}`))
		}
	}))
	defer srv.Close()
	c := NewGithubClient("token", srv.URL)

	entries, truncated, err := c.ListTree(context.Background(), "o", "r", "", true)
	if err != nil {
		t.Fatalf("empty repo must not error: %v", err)
	}
	if truncated || len(entries) != 0 {
		t.Fatalf("want empty, non-truncated list, got entries=%+v truncated=%v", entries, truncated)
	}
}

func TestGithubUpdateRepo_PatchesOnlyGivenFields(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch || r.URL.Path != "/repos/o/r" {
			t.Errorf("method=%s path=%s", r.Method, r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"description":"new desc","visibility":"public","html_url":"https://x/o/r","updated_at":"2026-01-02T03:04:05Z"}`))
	}))
	defer srv.Close()
	c := NewGithubClient("token", srv.URL)

	desc := "new desc"
	repo, err := c.UpdateRepo(context.Background(), "o", "r", &desc, nil, nil)
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

func TestGithubUpdateRepo_ArchivedRoundTrips(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if body["archived"] != true {
			t.Errorf("want archived=true in request body, got %+v", body)
		}
		w.Write([]byte(`{"archived":true,"visibility":"public","html_url":"https://x/o/r"}`))
	}))
	defer srv.Close()
	c := NewGithubClient("token", srv.URL)

	archived := true
	repo, err := c.UpdateRepo(context.Background(), "o", "r", nil, nil, &archived)
	if err != nil {
		t.Fatal(err)
	}
	if !repo.Archived {
		t.Errorf("want archived=true in the result, got %+v", repo)
	}
}

func TestGithubSetTopics_RoundTrips(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || r.URL.Path != "/repos/o/r/topics" {
			t.Errorf("method=%s path=%s", r.Method, r.URL.Path)
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		names, _ := body["names"].([]any)
		if len(names) != 2 {
			t.Errorf("want 2 topics in request, got %+v", body)
		}
		w.Write([]byte(`{"names":["a","b"]}`))
	}))
	defer srv.Close()
	c := NewGithubClient("token", srv.URL)

	topics, err := c.SetTopics(context.Background(), "o", "r", []string{"a", "b"})
	if err != nil {
		t.Fatal(err)
	}
	if len(topics) != 2 || topics[0] != "a" {
		t.Errorf("unexpected topics: %+v", topics)
	}
}

func TestGithubPutFile_CreateAndUpdate(t *testing.T) {
	var gotBodies []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "PUT" {
			t.Errorf("method: %s", r.Method)
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		gotBodies = append(gotBodies, body)
		w.Write([]byte(`{"content":{"html_url":"https://x/created.md"}}`))
	}))
	defer srv.Close()
	c := NewGithubClient("token", srv.URL)

	url, err := c.PutFile(context.Background(), "freaxnx01", "ideas-lab", "ideas/2026-06-16-x.md", []byte("hi"), "capture: x", "")
	if err != nil || url != "https://x/created.md" {
		t.Fatalf("PutFile create: url=%q err=%v", url, err)
	}
	if _, hasSHA := gotBodies[0]["sha"]; hasSHA {
		t.Errorf("create must not send sha: %v", gotBodies[0])
	}
	if gotBodies[0]["content"] != "aGk=" { // base64("hi")
		t.Errorf("content not base64: %v", gotBodies[0]["content"])
	}

	_, err = c.PutFile(context.Background(), "freaxnx01", "bridge", "ideas.md", []byte("x"), "capture: idea", "abc123")
	if err != nil {
		t.Fatalf("PutFile update: %v", err)
	}
	if gotBodies[1]["sha"] != "abc123" {
		t.Errorf("update must send sha, got: %v", gotBodies[1])
	}
}

func TestGithubPutFile_EscapesPathAgainstQueryInjection(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawQuery != "" {
			t.Errorf("path leaked into query string: %q", r.URL.RawQuery)
		}
		if want := "/repos/freaxnx01/bridge/contents/justfile?x.md"; r.URL.Path != want {
			t.Errorf("path = %q, want %q", r.URL.Path, want)
		}
		w.Write([]byte(`{"content":{"html_url":"https://x/y"}}`))
	}))
	defer srv.Close()
	c := NewGithubClient("token", srv.URL)

	_, err := c.PutFile(context.Background(), "freaxnx01", "bridge", "justfile?x.md", []byte("x"), "m", "")
	if err != nil {
		t.Fatalf("PutFile: %v", err)
	}
}

func TestGithubCreateRepo(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/user/repos" {
			t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer T" {
			t.Fatalf("bad auth %q", r.Header.Get("Authorization"))
		}
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &gotBody)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"name":"foo","visibility":"private","default_branch":"main",
			"html_url":"https://gh/freaxnx01/foo","ssh_url":"git@github.com:freaxnx01/foo.git",
			"updated_at":"2026-07-22T10:00:00Z","owner":{"login":"freaxnx01"}}`))
	}))
	defer srv.Close()

	c := NewGithubClient("T", srv.URL)
	ref, err := c.CreateRepo(context.Background(), "foo", true)
	if err != nil {
		t.Fatal(err)
	}
	if gotBody["private"] != true || gotBody["auto_init"] != true {
		t.Fatalf("body = %v", gotBody)
	}
	if ref.Name != "foo" || ref.Owner != "freaxnx01" || ref.Visibility != "private" {
		t.Fatalf("ref = %+v", ref)
	}
	if ref.UpdatedAt.IsZero() {
		t.Fatalf("ref.UpdatedAt is zero, want populated from response")
	}
}

func TestGithubCreateRepoExists(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
	}))
	defer srv.Close()
	_, err := NewGithubClient("T", srv.URL).CreateRepo(context.Background(), "foo", true)
	if !errors.Is(err, ErrRepoExists) {
		t.Fatalf("want ErrRepoExists, got %v", err)
	}
}

func TestGithubCreateIssue(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/repos/freaxnx01/bridge/issues" {
			t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"number":142,"title":"flicker","html_url":"https://github.com/freaxnx01/bridge/issues/142","updated_at":"2026-07-22T10:00:00Z"}`))
	}))
	defer srv.Close()

	c := NewGithubClient("T", srv.URL)
	is, err := c.CreateIssue(context.Background(), "freaxnx01", "bridge", "flicker", "")
	if err != nil {
		t.Fatal(err)
	}
	if gotBody["title"] != "flicker" || gotBody["body"] != "" {
		t.Errorf("body sent: %+v", gotBody)
	}
	if is.Forge != "github" || is.Repo != "freaxnx01/bridge" || is.Number != 142 || is.Title != "flicker" ||
		is.URL != "https://github.com/freaxnx01/bridge/issues/142" {
		t.Errorf("issue: %+v", is)
	}
	if is.Updated.IsZero() {
		t.Fatalf("is.Updated is zero, want populated from response")
	}
}

func TestGithubCloseIssue(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "PATCH" || r.URL.Path != "/repos/freaxnx01/bridge/issues/142" {
			t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"number":142,"title":"flicker","state":"closed","html_url":"https://github.com/freaxnx01/bridge/issues/142","updated_at":"2026-07-22T10:00:00Z"}`))
	}))
	defer srv.Close()

	c := NewGithubClient("T", srv.URL)
	is, err := c.CloseIssue(context.Background(), "freaxnx01", "bridge", 142, "completed")
	if err != nil {
		t.Fatal(err)
	}
	if gotBody["state"] != "closed" || gotBody["state_reason"] != "completed" {
		t.Errorf("body sent: %+v", gotBody)
	}
	if is.State != "closed" || is.Number != 142 {
		t.Errorf("issue: %+v", is)
	}
	if is.Updated.IsZero() {
		t.Fatalf("is.Updated is zero, want populated from response")
	}
}

func TestGithubCloseIssue_OmitsStateReasonWhenEmpty(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"number":142,"state":"closed","updated_at":"2026-07-22T10:00:00Z"}`))
	}))
	defer srv.Close()

	c := NewGithubClient("T", srv.URL)
	if _, err := c.CloseIssue(context.Background(), "freaxnx01", "bridge", 142, ""); err != nil {
		t.Fatal(err)
	}
	if _, ok := gotBody["state_reason"]; ok {
		t.Errorf("state_reason must be omitted when empty, got %+v", gotBody)
	}
}

func TestGithubUpdateIssue(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "PATCH" || r.URL.Path != "/repos/freaxnx01/bridge/issues/142" {
			t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"number":142,"title":"new title","html_url":"https://github.com/freaxnx01/bridge/issues/142","updated_at":"2026-07-22T10:00:00Z"}`))
	}))
	defer srv.Close()

	c := NewGithubClient("T", srv.URL)
	title := "new title"
	is, err := c.UpdateIssue(context.Background(), "freaxnx01", "bridge", 142, &title, nil)
	if err != nil {
		t.Fatal(err)
	}
	if gotBody["title"] != "new title" {
		t.Errorf("body sent: %+v", gotBody)
	}
	if _, ok := gotBody["body"]; ok {
		t.Errorf("body must be omitted when nil, got %+v", gotBody)
	}
	if is.Title != "new title" || is.Number != 142 {
		t.Errorf("issue: %+v", is)
	}
}

func TestGithubAddLabels(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/repos/freaxnx01/bridge/issues/142/labels" {
			t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[{"name":"bug"},{"name":"p1"}]`))
	}))
	defer srv.Close()

	c := NewGithubClient("T", srv.URL)
	labels, err := c.AddLabels(context.Background(), "freaxnx01", "bridge", 142, []string{"bug", "p1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(labels) != 2 || labels[0] != "bug" || labels[1] != "p1" {
		t.Errorf("labels: %+v", labels)
	}
}

func TestGithubCommentIssue(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/repos/freaxnx01/bridge/issues/142/comments" {
			t.Fatalf("unexpected %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&gotBody)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":7,"body":"looks good","created_at":"2026-07-22T10:00:00Z"}`))
	}))
	defer srv.Close()

	c := NewGithubClient("T", srv.URL)
	comment, err := c.CommentIssue(context.Background(), "freaxnx01", "bridge", 142, "looks good")
	if err != nil {
		t.Fatal(err)
	}
	if gotBody["body"] != "looks good" {
		t.Errorf("body sent: %+v", gotBody)
	}
	if comment.ID != 7 || comment.Body != "looks good" {
		t.Errorf("comment: %+v", comment)
	}
	if comment.Created.IsZero() {
		t.Fatalf("comment.Created is zero, want populated from response")
	}
}

func TestGithubListOpenIssuesMilestoneAndCreated(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[
		  {"number":41,"title":"authors filter","html_url":"u1","labels":[{"name":"feat"}],
		   "updated_at":"2026-07-01T00:00:00Z","created_at":"2026-06-01T00:00:00Z",
		   "milestone":{"title":"v2 search","number":3,"due_on":"2026-08-15T00:00:00Z"}},
		  {"number":42,"title":"no milestone","html_url":"u2","labels":[],
		   "updated_at":"2026-07-02T00:00:00Z","created_at":"2026-06-02T00:00:00Z"}
		]`))
	}))
	defer srv.Close()

	c := NewGithubClient("token", srv.URL)
	issues, err := c.ListOpenIssues(context.Background(), "freaxnx01", "quotes")
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 2 {
		t.Fatalf("got %d issues", len(issues))
	}
	if issues[0].Milestone != "v2 search" {
		t.Errorf("milestone: %q", issues[0].Milestone)
	}
	if issues[0].Created.Format("2006-01-02") != "2026-06-01" {
		t.Errorf("created: %v", issues[0].Created)
	}
	// A missing milestone must be the empty string, not a panic.
	if issues[1].Milestone != "" {
		t.Errorf("milestone should be empty, got %q", issues[1].Milestone)
	}
}

func TestGithubListOpenMilestones(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("state") != "open" {
			t.Errorf("state: %q", r.URL.Query().Get("state"))
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[
		  {"number":3,"title":"v2 search","due_on":"2026-08-15T00:00:00Z"},
		  {"number":4,"title":"someday","due_on":null}
		]`))
	}))
	defer srv.Close()

	ms, err := NewGithubClient("token", srv.URL).ListOpenMilestones(context.Background(), "o", "r")
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) != 2 || ms[0].Title != "v2 search" {
		t.Fatalf("got %+v", ms)
	}
	// A null due_on must decode to the zero time, not error.
	if !ms[1].DueOn.IsZero() {
		t.Errorf("due_on should be zero, got %v", ms[1].DueOn)
	}
}

func TestGithubListOpenPullRequests(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[
		  {"number":90,"title":"feat: authors","body":"Closes #41","draft":true}
		]`))
	}))
	defer srv.Close()

	prs, err := NewGithubClient("token", srv.URL).ListOpenPullRequests(context.Background(), "o", "r")
	if err != nil {
		t.Fatal(err)
	}
	if len(prs) != 1 || prs[0].Number != 90 || !prs[0].Draft {
		t.Fatalf("got %+v", prs)
	}
	if prs[0].Body != "Closes #41" {
		t.Errorf("body: %q", prs[0].Body)
	}
}

func TestGithubRemoveLabel(t *testing.T) {
	testCases := []struct {
		name       string
		statusCode int
		label      string
	}{
		{
			name:       "success with complex label",
			statusCode: http.StatusNoContent,
			label:      "🧊 parked",
		},
		{
			name:       "success with colon-separated label",
			statusCode: http.StatusNoContent,
			label:      "failed:infra",
		},
		{
			name:       "404 treated as success",
			statusCode: http.StatusNotFound,
			label:      "attempt:1",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// Verify the request method and path
				if r.Method != "DELETE" {
					t.Errorf("method: %s, want DELETE", r.Method)
				}
				// Verify the Accept header
				if r.Header.Get("Accept") != "application/vnd.github+json" {
					t.Errorf("Accept header: %q", r.Header.Get("Accept"))
				}
				// Verify Bearer token header
				if r.Header.Get("Authorization") != "Bearer token" {
					t.Errorf("Authorization header: %q", r.Header.Get("Authorization"))
				}
				// Extract and verify the escaped label from the path
				// Path format: /repos/owner/repo/issues/42/labels/ESCAPED_LABEL
				pathParts := strings.Split(r.URL.Path, "/")
				if len(pathParts) < 8 {
					t.Errorf("path too short: %s", r.URL.Path)
				}
				escapedLabel := pathParts[len(pathParts)-1]
				// url.QueryUnescape should round-trip to the original label
				unescaped, err := url.QueryUnescape(escapedLabel)
				if err != nil {
					t.Errorf("unescape label: %v", err)
				}
				if unescaped != tc.label {
					t.Errorf("label mismatch: got %q, want %q", unescaped, tc.label)
				}
				w.WriteHeader(tc.statusCode)
			}))
			defer srv.Close()

			err := NewGithubClient("token", srv.URL).RemoveLabel(context.Background(), "owner", "repo", 42, tc.label)
			if err != nil {
				t.Errorf("RemoveLabel: %v", err)
			}
		})
	}
}

func TestGithubGetIssue(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/freaxnx01/bridge/issues/235":
			w.Write([]byte(`{"number":235,"title":"feat(mcp): get_issue","body":"the body","html_url":"u235","state":"open","labels":[{"name":"area:mcp"}],"updated_at":"2026-08-11T00:00:00Z","created_at":"2026-08-01T00:00:00Z"}`))
		case "/repos/freaxnx01/bridge/issues/235/comments":
			w.Write([]byte(`[{"id":1,"body":"first","user":{"login":"alice"},"created_at":"2026-08-02T00:00:00Z"},{"id":2,"body":"second","user":{"login":"bob"},"created_at":"2026-08-03T00:00:00Z"}]`))
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	c := NewGithubClient("token", srv.URL)
	issue, comments, err := c.GetIssue(context.Background(), "freaxnx01", "bridge", 235)
	if err != nil {
		t.Fatal(err)
	}
	if issue.Number != 235 || issue.Body != "the body" || issue.State != "open" || issue.Labels[0] != "area:mcp" {
		t.Errorf("issue: %+v", issue)
	}
	if len(comments) != 2 {
		t.Fatalf("want 2 comments, got %d: %+v", len(comments), comments)
	}
	if comments[0].ID != 1 || comments[0].Body != "first" || comments[0].Author != "alice" {
		t.Errorf("comment[0]: %+v", comments[0])
	}
	if comments[1].Author != "bob" {
		t.Errorf("comment[1]: %+v", comments[1])
	}
}

func TestGithubGetIssue_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"message":"Not Found"}`))
	}))
	defer srv.Close()

	c := NewGithubClient("token", srv.URL)
	_, _, err := c.GetIssue(context.Background(), "freaxnx01", "bridge", 9999)
	if err == nil {
		t.Fatal("want error for a non-existent issue, got nil")
	}
}

// TestGithubGetIssue_PaginatesComments covers a thread with more comments
// than a single API page (githubCommentsPageSize): GetIssue must keep
// paging until the full thread is fetched, not silently stop after page 1.
func TestGithubGetIssue_PaginatesComments(t *testing.T) {
	const totalComments = githubCommentsPageSize + 5

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/freaxnx01/bridge/issues/235":
			w.Write([]byte(`{"number":235,"title":"feat(mcp): get_issue","body":"the body","html_url":"u235","state":"open"}`))
		case "/repos/freaxnx01/bridge/issues/235/comments":
			page, _ := strconv.Atoi(r.URL.Query().Get("page"))
			if r.URL.Query().Get("per_page") != strconv.Itoa(githubCommentsPageSize) {
				t.Errorf("per_page: %q", r.URL.Query().Get("per_page"))
			}
			start := (page - 1) * githubCommentsPageSize
			end := start + githubCommentsPageSize
			if end > totalComments {
				end = totalComments
			}
			var out []map[string]any
			for i := start; i < end; i++ {
				out = append(out, map[string]any{
					"id": i + 1, "body": fmt.Sprintf("comment %d", i+1),
					"user": map[string]string{"login": "alice"}, "created_at": "2026-08-02T00:00:00Z",
				})
			}
			b, _ := json.Marshal(out)
			w.Write(b)
		default:
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	c := NewGithubClient("token", srv.URL)
	_, comments, err := c.GetIssue(context.Background(), "freaxnx01", "bridge", 235)
	if err != nil {
		t.Fatal(err)
	}
	if len(comments) != totalComments {
		t.Fatalf("want %d comments, got %d", totalComments, len(comments))
	}
	if comments[0].ID != 1 || comments[totalComments-1].ID != totalComments {
		t.Errorf("comments out of order: first=%d last=%d", comments[0].ID, comments[totalComments-1].ID)
	}
}
