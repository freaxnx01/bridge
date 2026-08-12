# get_issue MCP Tool Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a `get_issue` MCP tool that returns an issue's body and comment thread (with authors, in order, newest-20-capped) for both GitHub and Forgejo.

**Architecture:** Follows the existing capability-interface pattern (`fileReader`, `issueCommenter`, etc.): a new `issueReader` interface with `GetIssue`, implemented by `GithubClient`/`ForgejoClient`, asserted in a new `handleGetIssue` handler, registered as a new MCP tool. `forge.Comment` gains `Author`; `forge.Issue` gains `Body`.

**Tech Stack:** Go, stdlib `net/http`/`net/http/httptest` for client tests, hand-rolled fakes for handler tests (no testify/mocks — see `internal/mcp/tools_test.go`'s existing `fakeFull` composition).

## Global Constraints

- No third-party assertion/mocking libraries — hand-rolled fakes only.
- `gofmt -l .`, `go vet ./...`, `golangci-lint run`, `go test -race ./...` must all be clean after every task.
- Comment cap: newest 20 comments returned, `comments_truncated: true` + `total_comments` set when the full thread exceeds 20. Kept comments stay in chronological order.
- No offset/limit pagination — out of scope for this iteration.
- `get_issue` is a read tool: no `Confirm`/`Draft` gate, no audit logging (matches `read_file`/`list_tree`/`list_issues`, not `comment_issue`).
- Error on an unsupported client: `forge %q does not support reading issues` (matches the wording style of every other capability-gated handler in `internal/mcp/tools_read.go`/`tools_write.go`).

---

### Task 1: `forge.Comment.Author` + `forge.Issue.Body` + `GithubClient.GetIssue`

**Files:**
- Modify: `internal/forge/client.go` (add fields)
- Modify: `internal/forge/github.go` (add `GetIssue`)
- Test: `internal/forge/github_test.go` (add `TestGithubGetIssue`)

**Interfaces:**
- Produces: `forge.Comment.Author string` (json `author`), `forge.Issue.Body string` (json `body,omitempty`), `func (c *GithubClient) GetIssue(ctx context.Context, owner, repo string, number int) (Issue, []Comment, error)`.

- [ ] **Step 1: Write the failing test**

Add to `internal/forge/github_test.go`:

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/forge/... -run TestGithubGetIssue -v`
Expected: FAIL — `c.GetIssue undefined (type *GithubClient has no field or method GetIssue)`.

- [ ] **Step 3: Write minimal implementation**

In `internal/forge/client.go`, update the `Comment` and `Issue` structs:

```go
type Issue struct {
	Forge     string    `json:"forge"`
	Repo      string    `json:"repo"`
	Number    int       `json:"number"`
	Title     string    `json:"title"`
	URL       string    `json:"url"`
	State     string    `json:"state,omitempty"`
	Body      string    `json:"body,omitempty"`
	Labels    []string  `json:"labels,omitempty"`
	Milestone string    `json:"milestone,omitempty"`
	Updated   time.Time `json:"updated,omitempty"`
	Created   time.Time `json:"created,omitempty"`
}
```

```go
// Comment is a single issue comment.
type Comment struct {
	ID      int       `json:"id"`
	Author  string    `json:"author,omitempty"`
	Body    string    `json:"body"`
	Created time.Time `json:"created,omitempty"`
}
```

In `internal/forge/github.go`, add near `ListOpenIssues`:

```go
// GetIssue fetches a single issue's body plus its full comment thread, in
// chronological order. Comment-count truncation is the MCP handler's job,
// not the client's — this returns everything the forge has.
func (c *GithubClient) GetIssue(ctx context.Context, owner, repo string, number int) (Issue, []Comment, error) {
	var raw struct {
		Number  int    `json:"number"`
		Title   string `json:"title"`
		HTMLURL string `json:"html_url"`
		State   string `json:"state"`
		Body    string `json:"body"`
		Labels  []struct {
			Name string `json:"name"`
		} `json:"labels"`
		UpdatedAt time.Time `json:"updated_at"`
		CreatedAt time.Time `json:"created_at"`
	}
	path := fmt.Sprintf("/repos/%s/%s/issues/%d", owner, repo, number)
	if err := c.get(ctx, path, &raw); err != nil {
		return Issue{}, nil, err
	}
	labels := make([]string, 0, len(raw.Labels))
	for _, l := range raw.Labels {
		labels = append(labels, l.Name)
	}
	issue := Issue{
		Forge: "github", Repo: owner + "/" + repo,
		Number: raw.Number, Title: raw.Title, URL: raw.HTMLURL,
		State: raw.State, Body: raw.Body, Labels: labels,
		Updated: raw.UpdatedAt, Created: raw.CreatedAt,
	}

	var rawComments []struct {
		ID   int    `json:"id"`
		Body string `json:"body"`
		User struct {
			Login string `json:"login"`
		} `json:"user"`
		CreatedAt time.Time `json:"created_at"`
	}
	commentsPath := fmt.Sprintf("/repos/%s/%s/issues/%d/comments?per_page=100", owner, repo, number)
	if err := c.get(ctx, commentsPath, &rawComments); err != nil {
		return Issue{}, nil, err
	}
	comments := make([]Comment, 0, len(rawComments))
	for _, rc := range rawComments {
		comments = append(comments, Comment{
			ID: rc.ID, Author: rc.User.Login, Body: rc.Body, Created: rc.CreatedAt,
		})
	}
	return issue, comments, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/forge/... -run TestGithubGetIssue -v`
Expected: PASS

- [ ] **Step 5: Run the full forge package suite**

Run: `go test -race ./internal/forge/...`
Expected: PASS (confirms `Comment`/`Issue` field additions didn't break any existing struct-literal test).

- [ ] **Step 6: Commit**

```bash
git add internal/forge/client.go internal/forge/github.go internal/forge/github_test.go
git commit -m "feat(forge): add Comment.Author, Issue.Body, GithubClient.GetIssue"
```

---

### Task 2: `ForgejoClient.GetIssue`

**Files:**
- Modify: `internal/forge/forgejo.go` (add `GetIssue`)
- Test: `internal/forge/forgejo_test.go` (add `TestForgejoGetIssue`)

**Interfaces:**
- Consumes: `Issue`, `Comment` from Task 1 (`internal/forge/client.go`).
- Produces: `func (c *ForgejoClient) GetIssue(ctx context.Context, owner, repo string, number int) (Issue, []Comment, error)`.

- [ ] **Step 1: Write the failing test**

Add to `internal/forge/forgejo_test.go` (check the file's existing `NewForgejoClient(...)` constructor signature and match it):

```go
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
```

If `NewForgejoClient`'s real signature differs from `NewForgejoClient(token, baseURL string)`, match whatever `TestForgejoListRepos`/similar already use in this file — do not invent a new constructor shape.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/forge/... -run TestForgejoGetIssue -v`
Expected: FAIL — `c.GetIssue undefined`.

- [ ] **Step 3: Write minimal implementation**

In `internal/forge/forgejo.go`, add near `ListOpenIssues`:

```go
// GetIssue fetches a single issue's body plus its full comment thread, in
// chronological order. Comment-count truncation is the MCP handler's job,
// not the client's — this returns everything the forge has.
func (c *ForgejoClient) GetIssue(ctx context.Context, owner, repo string, number int) (Issue, []Comment, error) {
	var raw struct {
		Number  int    `json:"number"`
		Title   string `json:"title"`
		HTMLURL string `json:"html_url"`
		State   string `json:"state"`
		Body    string `json:"body"`
		Labels  []struct {
			Name string `json:"name"`
		} `json:"labels"`
		UpdatedAt time.Time `json:"updated_at"`
		CreatedAt time.Time `json:"created_at"`
	}
	path := fmt.Sprintf("/api/v1/repos/%s/%s/issues/%d", owner, repo, number)
	if err := c.get(ctx, path, &raw); err != nil {
		return Issue{}, nil, err
	}
	labels := make([]string, 0, len(raw.Labels))
	for _, l := range raw.Labels {
		labels = append(labels, l.Name)
	}
	issue := Issue{
		Forge: "forgejo", Repo: owner + "/" + repo,
		Number: raw.Number, Title: raw.Title, URL: raw.HTMLURL,
		State: raw.State, Body: raw.Body, Labels: labels,
		Updated: raw.UpdatedAt, Created: raw.CreatedAt,
	}

	var rawComments []struct {
		ID     int    `json:"id"`
		Body   string `json:"body"`
		Poster struct {
			Login string `json:"login"`
		} `json:"poster"`
		CreatedAt time.Time `json:"created_at"`
	}
	commentsPath := fmt.Sprintf("/api/v1/repos/%s/%s/issues/%d/comments", owner, repo, number)
	if err := c.get(ctx, commentsPath, &rawComments); err != nil {
		return Issue{}, nil, err
	}
	comments := make([]Comment, 0, len(rawComments))
	for _, rc := range rawComments {
		comments = append(comments, Comment{
			ID: rc.ID, Author: rc.Poster.Login, Body: rc.Body, Created: rc.CreatedAt,
		})
	}
	return issue, comments, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/forge/... -run TestForgejoGetIssue -v`
Expected: PASS

- [ ] **Step 5: Run the full forge package suite**

Run: `go test -race ./internal/forge/...`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/forge/forgejo.go internal/forge/forgejo_test.go
git commit -m "feat(forge): add ForgejoClient.GetIssue"
```

---

### Task 3: `issueReader` capability, `handleGetIssue`, truncation, and test fakes

**Files:**
- Modify: `internal/mcp/tools.go` (new `issueReader` interface, `Capabilities()` entry)
- Modify: `internal/mcp/tools_read.go` (new `getIssueInput`/`getIssueOutput`, `handleGetIssue`)
- Modify: `internal/mcp/tools_test.go` (new `fakeIssueReader`, add it to `fakeFull`, extend `TestCapabilities_ReportsToolNamesPerCapability`)
- Test: `internal/mcp/tools_read_test.go` (add `TestHandleGetIssue_*`)

**Interfaces:**
- Consumes: `forge.Issue`, `forge.Comment` (Task 1), `d.ClientFor` / `Deps` (existing).
- Produces: `issueReader` interface (`GetIssue(ctx, owner, repo string, number int) (forge.Issue, []forge.Comment, error)`), `getIssueInput{Forge, Owner, Repo, IssueNumber}`, `getIssueOutput{Issue, Comments, TotalComments, CommentsTruncated}`, `func (d Deps) handleGetIssue(ctx context.Context, _ *mcp.CallToolRequest, in getIssueInput) (*mcp.CallToolResult, getIssueOutput, error)`.

- [ ] **Step 1: Write the failing tests**

In `internal/mcp/tools_test.go`, add a new fake capability struct near `fakeSearcher` (not embedded in the shared `fakeReader`, since it needs its own state — issue/comments/err):

```go
// fakeIssueReader supplies the issueReader capability.
type fakeIssueReader struct {
	issue    forge.Issue
	comments []forge.Comment
	err      error
}

func (f *fakeIssueReader) GetIssue(_ context.Context, _, _ string, _ int) (forge.Issue, []forge.Comment, error) {
	if f.err != nil {
		return forge.Issue{}, nil, f.err
	}
	return f.issue, f.comments, nil
}
```

Add `*fakeIssueReader` to the `fakeFull` struct and its constructor:

```go
type fakeFull struct {
	*fakeReader
	*fakeFiles
	*fakeTree
	*fakeIssues
	*fakeRepos
	*fakeRepoUpdater
	*fakePutFile
	*fakeIssueReader
}

func newFakeFull(name string) *fakeFull {
	files := &fakeFiles{}
	return &fakeFull{
		fakeReader:      &fakeReader{name: name},
		fakeFiles:       files,
		fakeTree:        &fakeTree{},
		fakeIssues:      &fakeIssues{forgeName: name},
		fakeRepos:       &fakeRepos{forgeName: name},
		fakeRepoUpdater: &fakeRepoUpdater{forgeName: name},
		fakePutFile:     &fakePutFile{fakeFiles: files},
		fakeIssueReader: &fakeIssueReader{},
	}
}
```

Extend the `"fully capable client reports every tool"` case in `TestCapabilities_ReportsToolNamesPerCapability`'s `want` slice to include `"get_issue"` (append at the end, after `"comment_issue"`).

In `internal/mcp/tools_read_test.go`, add:

```go
func TestHandleGetIssue_ReturnsIssueAndComments(t *testing.T) {
	gh := newFakeFull("github")
	gh.issue = forge.Issue{Forge: "github", Number: 235, Title: "t", Body: "b"}
	gh.comments = []forge.Comment{
		{ID: 1, Author: "alice", Body: "c1"},
		{ID: 2, Author: "bob", Body: "c2"},
	}
	d := depsWith(map[string]*fakeFull{"github": gh}, nil)

	_, out, err := d.handleGetIssue(context.Background(), nil,
		getIssueInput{Forge: "github", Owner: "o", Repo: "r", IssueNumber: 235})
	if err != nil {
		t.Fatal(err)
	}
	if out.Issue.Number != 235 || out.Issue.Body != "b" {
		t.Errorf("issue: %+v", out.Issue)
	}
	if len(out.Comments) != 2 || out.TotalComments != 2 || out.CommentsTruncated {
		t.Errorf("comments: %+v total=%d truncated=%v", out.Comments, out.TotalComments, out.CommentsTruncated)
	}
}

func TestHandleGetIssue_TruncatesToNewest20(t *testing.T) {
	gh := newFakeFull("github")
	comments := make([]forge.Comment, 25)
	for i := range comments {
		comments[i] = forge.Comment{ID: i, Body: fmt.Sprintf("c%d", i)}
	}
	gh.comments = comments
	d := depsWith(map[string]*fakeFull{"github": gh}, nil)

	_, out, err := d.handleGetIssue(context.Background(), nil,
		getIssueInput{Forge: "github", Owner: "o", Repo: "r", IssueNumber: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Comments) != 20 {
		t.Fatalf("want 20 comments, got %d", len(out.Comments))
	}
	if !out.CommentsTruncated {
		t.Error("want CommentsTruncated=true for a 25-comment thread")
	}
	if out.TotalComments != 25 {
		t.Errorf("want TotalComments=25, got %d", out.TotalComments)
	}
	// Newest 20 kept: comments[5] through comments[24], still in order.
	if out.Comments[0].ID != 5 {
		t.Errorf("want the oldest kept comment to be ID 5, got %d", out.Comments[0].ID)
	}
	if out.Comments[19].ID != 24 {
		t.Errorf("want the newest kept comment to be ID 24, got %d", out.Comments[19].ID)
	}
}

func TestHandleGetIssue_UnconfiguredForgeErrors(t *testing.T) {
	d := depsWith(map[string]*fakeFull{}, nil)
	_, _, err := d.handleGetIssue(context.Background(), nil,
		getIssueInput{Forge: "bogus", Owner: "o", Repo: "r", IssueNumber: 1})
	if err == nil {
		t.Fatal("want error for unknown forge, got nil")
	}
	if !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("want a not-configured error, got %v", err)
	}
}

func TestHandleGetIssue_TierOneClientReportsUnsupportedNotUnconfigured(t *testing.T) {
	d := Deps{ClientFor: func(string, string) ForgeReader { return &fakeReader{name: "gitlab"} }}

	_, _, err := d.handleGetIssue(context.Background(), nil,
		getIssueInput{Forge: "gitlab", Owner: "o", Repo: "r", IssueNumber: 1})

	if err == nil {
		t.Fatal("want an error for a client without GetIssue, got nil")
	}
	if strings.Contains(err.Error(), "not configured") {
		t.Fatalf("a resolved but incapable client must not be reported as unconfigured: %v", err)
	}
	if !strings.Contains(err.Error(), "does not support") {
		t.Fatalf("want a does-not-support error, got %v", err)
	}
}

func TestHandleGetIssue_ClientErrorPropagates(t *testing.T) {
	gh := newFakeFull("github")
	gh.fakeIssueReader.err = errors.New("404 not found")
	d := depsWith(map[string]*fakeFull{"github": gh}, nil)

	_, _, err := d.handleGetIssue(context.Background(), nil,
		getIssueInput{Forge: "github", Owner: "o", Repo: "r", IssueNumber: 9999})
	if err == nil {
		t.Fatal("want error to propagate, got nil")
	}
}
```

Add `"fmt"` to `tools_read_test.go`'s import block if not already present.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/mcp/... -run TestHandleGetIssue -v`
Expected: FAIL — `getIssueInput`/`handleGetIssue` undefined (compile error), same for the `TestCapabilities_...` case once `"get_issue"` is added to its `want` slice (fails on length mismatch until Step 3 lands).

- [ ] **Step 3: Write minimal implementation**

In `internal/mcp/tools.go`, add near `issueCommenter`:

```go
// issueReader is asserted by get_issue.
type issueReader interface {
	GetIssue(ctx context.Context, owner, repo string, number int) (forge.Issue, []forge.Comment, error)
}
```

In `Capabilities()`, add after the `issueCommenter` check:

```go
	if _, ok := r.(issueReader); ok {
		capabilities = append(capabilities, "get_issue")
	}
```

In `internal/mcp/tools_read.go`, add near `handleListIssues`:

```go
// maxIssueComments bounds get_issue's comment payload. When a thread exceeds
// it, the newest maxIssueComments comments are kept (thread order preserved)
// and CommentsTruncated is set — no offset/limit pagination in this
// iteration; see the design doc.
const maxIssueComments = 20

type getIssueInput struct {
	Forge       string `json:"forge" jsonschema:"forge hosting the repo: github or forgejo"`
	Owner       string `json:"owner" jsonschema:"repository owner"`
	Repo        string `json:"repo" jsonschema:"repository name"`
	IssueNumber int    `json:"issue_number" jsonschema:"issue number"`
}

type getIssueOutput struct {
	Issue             forge.Issue     `json:"issue"`
	Comments          []forge.Comment `json:"comments"`
	TotalComments     int             `json:"total_comments"`
	CommentsTruncated bool            `json:"comments_truncated,omitempty"`
}

// handleGetIssue returns an issue's body and comment thread. Comments are
// capped at the newest maxIssueComments; a thread over the cap sets
// CommentsTruncated with TotalComments reporting the true count, in place of
// implementing offset/limit pagination.
func (d Deps) handleGetIssue(ctx context.Context, _ *mcp.CallToolRequest, in getIssueInput) (*mcp.CallToolResult, getIssueOutput, error) {
	client := d.ClientFor(in.Forge, in.Owner)
	if client == nil {
		return nil, getIssueOutput{}, fmt.Errorf("forge %q not configured", in.Forge)
	}
	reader, ok := client.(issueReader)
	if !ok {
		return nil, getIssueOutput{}, fmt.Errorf("forge %q does not support reading issues", in.Forge)
	}
	issue, comments, err := reader.GetIssue(ctx, in.Owner, in.Repo, in.IssueNumber)
	if err != nil {
		return nil, getIssueOutput{}, fmt.Errorf("get issue %s/%s#%d: %w", in.Owner, in.Repo, in.IssueNumber, err)
	}
	out := getIssueOutput{Issue: issue, Comments: comments, TotalComments: len(comments)}
	if len(comments) > maxIssueComments {
		out.Comments = comments[len(comments)-maxIssueComments:]
		out.CommentsTruncated = true
	}
	return nil, out, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/mcp/... -run 'TestHandleGetIssue|TestCapabilities_ReportsToolNamesPerCapability' -v`
Expected: PASS

- [ ] **Step 5: Run the full mcp package suite**

Run: `go test -race ./internal/mcp/...`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/mcp/tools.go internal/mcp/tools_read.go internal/mcp/tools_test.go internal/mcp/tools_read_test.go
git commit -m "feat(mcp): add get_issue handler with newest-20 comment cap"
```

---

### Task 4: Register the `get_issue` tool

**Files:**
- Modify: `internal/mcp/server.go` (register the tool)
- Modify: `internal/mcp/server_test.go` (`TestNewServer_RegistersExpectedToolSet` want list)

**Interfaces:**
- Consumes: `deps.handleGetIssue` (Task 3).

- [ ] **Step 1: Write the failing test**

In `internal/mcp/server_test.go`, update `TestNewServer_RegistersExpectedToolSet`'s `want` slice to insert `"get_issue"` in alphabetical order (the list is sorted, so it goes right after `"cross_forge_status"` and before `"list_git_forges"`):

```go
	want := []string{"add_labels", "close_issue", "comment_issue", "create_issue", "create_repo", "cross_forge_status", "get_issue", "list_git_forges", "list_issues", "list_repos", "list_tree", "put_file", "read_file", "search_code", "update_issue", "update_repo"}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/mcp/... -run TestNewServer_RegistersExpectedToolSet -v`
Expected: FAIL — length mismatch (want 16, got 15) since `get_issue` isn't registered yet.

- [ ] **Step 3: Write minimal implementation**

In `internal/mcp/server.go`, add after the `list_issues` registration:

```go
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "get_issue",
		Description: "Read a single issue's body and comment thread (author, body, created, in order). Comments are capped at the newest 20; comments_truncated + total_comments signal when more exist.",
	}, deps.handleGetIssue)
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/mcp/... -run TestNewServer_RegistersExpectedToolSet -v`
Expected: PASS

- [ ] **Step 5: Run the full repo test suite**

Run: `go test -race ./...`
Expected: PASS — full suite green, including `TestListGitForges_AdvertisesOnlyRegisteredTools` (confirms `get_issue`, now both registered and capability-reported, stays consistent).

- [ ] **Step 6: Run static checks**

```bash
gofmt -l .
go vet ./...
golangci-lint run
```

Expected: no output from `gofmt -l .`; clean from `go vet`/`golangci-lint`.

- [ ] **Step 7: Commit**

```bash
git add internal/mcp/server.go internal/mcp/server_test.go
git commit -m "feat(mcp): register get_issue tool"
```

---

## Post-implementation

After Task 4, `get_issue` is fully wired: forge clients, capability interface, handler, and tool registration all covered by tests. No further tasks — this closes issue #235's acceptance criteria (body + ordered comment thread with authors on both forges, truncation behavior, not-found and unsupported-client error shapes matching the rest of the issue tools).
