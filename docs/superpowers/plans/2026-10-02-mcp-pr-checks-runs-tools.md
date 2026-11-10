# Read-only PR, checks and workflow-run MCP tools Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add three read-only MCP tools — `list_prs`, `get_pr`, `list_runs` — so an MCP client can verify what a dispatched ai-implement run produced (its PR, the PR's checks, and which actor triggered its Actions runs).

**Architecture:** `GithubClient` (`internal/forge/github.go`) grows six read methods and `forge.PullRequest` grows additive fields. The MCP layer (`internal/mcp`) asserts three small consumer-side capability interfaces (`prLister`, `prReader`, `runLister`), reports them via `Capabilities()`, and registers three handlers. Forgejo satisfies none of the interfaces and gets a warning, not an empty list. Every new handler bounds the whole call with a 30 s context deadline.

**Tech Stack:** Go (stdlib `net/http`, `net/url`, `context`, `time`), `github.com/modelcontextprotocol/go-sdk/mcp`, stdlib `testing` + `net/http/httptest` with hand-rolled fakes.

**Spec:** `docs/superpowers/specs/2026-10-02-mcp-pr-checks-runs-tools-design.md`

## Global Constraints

- Read-only: no merge, approve, re-run or other write endpoint is called.
- GitHub only. Forgejo (`ForgejoClient`) implements none of the new methods.
- `forge.PullRequest` changes are additive; `ListOpenPullRequests` keeps its signature and request URL; dispatch behaviour and dispatch tests unchanged.
- The `closes` filter uses `dispatch.ClosesIssue` (`internal/dispatch/eligible.go:68`) — imported, never copied.
- `const toolTimeout = 30 * time.Second` wraps each new handler's context.
- One page per endpoint: `per_page=100` (PRs, files, check-runs), `list_runs` limit default 20, clamped to `[1, 100]`; `maxPRFiles = 100`.
- Forge lacks the capability → empty result, nil error, warning text exactly `"<forge> does not support <tool>"`.
- Unconfigured forge → error `forge "<name>" not configured`.
- No new Go modules. No testify/mockery. No `//nolint`. `gofmt -l .` empty, `go vet ./...`, `golangci-lint run`, `go test -race ./...` all clean.
- Existing tests are not edited except the two server tool-list assertions in `internal/mcp/server_test.go`, which enumerate registered tools and must list the three new ones (a deliberate expectation change, not a green-making edit).
- Commits follow Conventional Commits and reference `#326`.

## Review Focus

1. `closes=41` must not match a PR whose body says `Closes #410` — covered by `TestHandleListPRs_ClosesFilterUsesDispatchRule` (Task 4).
2. A PR whose check-runs request fails still returns the PR and its files, with a warning — `TestHandleGetPR_ChecksFailureIsPartialResult` (Task 5).
3. A PR with more changed files than one page returns `files_truncated: true` — `TestHandleGetPR_CapsFilesAndFlagsTruncation` (Task 5).
4. A stalled forge call is bounded: the context the forge method receives carries a deadline ≤ 30 s — `..._PassesDeadline` tests in Tasks 4, 5, 6.
5. `list_runs` with `limit: 0` or `limit: 500` sends `per_page=20` / `per_page=100` — `TestHandleListRuns_ClampsLimit` (Task 6).

## File Structure

| File | Change | Responsibility |
|---|---|---|
| `internal/forge/client.go` | Modify | `PullRequest` fields; new `PRFile`, `CheckRun`, `CommitStatus`, `WorkflowRun` types |
| `internal/forge/github.go` | Modify | `ghPull` raw type, `ListPullRequests`, refactored `ListOpenPullRequests` |
| `internal/forge/github_pulls.go` | Create | `GetPullRequest`, `ListPullRequestFiles`, `ListCheckRuns`, `ListCommitStatuses`, `ListWorkflowRuns` (keeps `github.go` from growing further) |
| `internal/forge/github_test.go` | Modify | Extend `ListOpenPullRequests` test (new assertions only), add `ListPullRequests` test |
| `internal/forge/github_pulls_test.go` | Create | httptest tests for the five methods in `github_pulls.go` |
| `internal/mcp/tools.go` | Modify | `prLister`, `prReader`, `runLister`; `Capabilities()` entries |
| `internal/mcp/tools_prs.go` | Create | `toolTimeout`, inputs/outputs and handlers for the three tools |
| `internal/mcp/tools_prs_test.go` | Create | `fakePRs` + handler tests |
| `internal/mcp/tools_test.go` | Modify | New `Capabilities` test function (existing cases untouched) |
| `internal/mcp/server.go` | Modify | Register the three tools (read section) |
| `internal/mcp/server_test.go` | Modify | Add three names to both tool-list expectations |
| `docs/mcp-cheatsheet.md`, `README.md`, `CHANGELOG.md` | Modify | Docs |

Handlers go in a new `tools_prs.go` rather than `tools_read.go` because `tools_read.go` is already ~400 lines; the issue's "registered in tools_read.go" is satisfied in spirit — registration is in `server.go`, as for every other tool.

---

### Task 1: Extend `forge.PullRequest` and add `ListPullRequests`

**Files:**
- Modify: `internal/forge/client.go:147-153`
- Modify: `internal/forge/github.go:1022-1037`
- Test: `internal/forge/github_test.go`

**Interfaces:**
- Produces:
  ```go
  type PullRequest struct {
      Number int; Title, Body string; Draft bool
      State, Author, HeadRef, HeadSHA, BaseRef, URL string
      Created, Updated time.Time
      Merged bool; MergeableState string; ChangedFiles int
  }
  func (c *GithubClient) ListPullRequests(ctx context.Context, owner, repo, state string) ([]PullRequest, error)
  // unexported, used by Task 2:
  type ghPull struct{ ... }; func (p ghPull) toPullRequest() PullRequest
  ```

- [ ] **Step 1: Write the failing tests**

Add to `internal/forge/github_test.go`. Extend `TestGithubListOpenPullRequests` by appending new assertions only (existing lines untouched) — replace its fixture JSON line so the same PR carries the new fields:

```go
// in TestGithubListOpenPullRequests, the fixture becomes:
		w.Write([]byte(`[
		  {"number":90,"title":"feat: authors","body":"Closes #41","draft":true,
		   "state":"open","user":{"login":"claude[bot]"},
		   "head":{"ref":"ai/41","sha":"abc123"},"base":{"ref":"main"},
		   "html_url":"https://github.com/o/r/pull/90",
		   "created_at":"2026-10-01T10:00:00Z","updated_at":"2026-10-01T11:00:00Z","merged_at":null}
		]`))
// appended at the end of the test:
	if prs[0].Author != "claude[bot]" || prs[0].HeadRef != "ai/41" || prs[0].HeadSHA != "abc123" || prs[0].BaseRef != "main" {
		t.Errorf("new fields: %+v", prs[0])
	}
	if prs[0].URL != "https://github.com/o/r/pull/90" || prs[0].State != "open" || prs[0].Merged {
		t.Errorf("url/state/merged: %+v", prs[0])
	}
```

New test:

```go
func TestGithubListPullRequests_PassesStateAndMapsMerged(t *testing.T) {
	var gotPath, gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotQuery = r.URL.Path, r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[
		  {"number":7,"title":"t","state":"closed","merged_at":"2026-09-30T08:00:00Z","user":{"login":"a"}},
		  {"number":8,"title":"u","state":"closed","merged_at":null,"user":{"login":"b"}}
		]`))
	}))
	defer srv.Close()

	prs, err := NewGithubClient("token", srv.URL).ListPullRequests(context.Background(), "o", "r", "closed")
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/repos/o/r/pulls" {
		t.Errorf("path: %s", gotPath)
	}
	if gotQuery != "state=closed&sort=updated&direction=desc&per_page=100" {
		t.Errorf("query: %s", gotQuery)
	}
	if len(prs) != 2 || !prs[0].Merged || prs[1].Merged {
		t.Fatalf("merged mapping: %+v", prs)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/forge -run 'TestGithubListOpenPullRequests|TestGithubListPullRequests' -v`
Expected: FAIL — `prs[0].Author undefined` / `ListPullRequests undefined` (compile error).

- [ ] **Step 3: Implement**

`internal/forge/client.go` — replace the `PullRequest` type:

```go
// PullRequest is a pull request. Body is needed to resolve "Closes #N".
// MergeableState and ChangedFiles are only populated by the single-PR endpoint.
type PullRequest struct {
	Number         int       `json:"number"`
	Title          string    `json:"title"`
	Body           string    `json:"body,omitempty"`
	Draft          bool      `json:"draft"`
	State          string    `json:"state,omitempty"`
	Author         string    `json:"author,omitempty"`
	HeadRef        string    `json:"head_ref,omitempty"`
	HeadSHA        string    `json:"head_sha,omitempty"`
	BaseRef        string    `json:"base_ref,omitempty"`
	URL            string    `json:"url,omitempty"`
	Created        time.Time `json:"created,omitempty"`
	Updated        time.Time `json:"updated,omitempty"`
	Merged         bool      `json:"merged,omitempty"`
	MergeableState string    `json:"mergeable_state,omitempty"`
	ChangedFiles   int       `json:"changed_files,omitempty"`
}
```

`internal/forge/github.go` — replace `ListOpenPullRequests` (lines 1022-1037) with:

```go
// ghPull is GitHub's pull-request JSON, shared by the list and single-PR
// endpoints; the single endpoint additionally fills MergeableState and
// ChangedFiles.
type ghPull struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	Body   string `json:"body"`
	Draft  bool   `json:"draft"`
	State  string `json:"state"`
	User   struct {
		Login string `json:"login"`
	} `json:"user"`
	Head struct {
		Ref string `json:"ref"`
		SHA string `json:"sha"`
	} `json:"head"`
	Base struct {
		Ref string `json:"ref"`
	} `json:"base"`
	HTMLURL        string     `json:"html_url"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
	MergedAt       *time.Time `json:"merged_at"`
	MergeableState string     `json:"mergeable_state"`
	ChangedFiles   int        `json:"changed_files"`
}

func (p ghPull) toPullRequest() PullRequest {
	return PullRequest{
		Number: p.Number, Title: p.Title, Body: p.Body, Draft: p.Draft, State: p.State,
		Author: p.User.Login, HeadRef: p.Head.Ref, HeadSHA: p.Head.SHA, BaseRef: p.Base.Ref,
		URL: p.HTMLURL, Created: p.CreatedAt, Updated: p.UpdatedAt, Merged: p.MergedAt != nil,
		MergeableState: p.MergeableState, ChangedFiles: p.ChangedFiles,
	}
}

func toPullRequests(raw []ghPull) []PullRequest {
	out := make([]PullRequest, 0, len(raw))
	for _, p := range raw {
		out = append(out, p.toPullRequest())
	}
	return out
}

func (c *GithubClient) ListOpenPullRequests(ctx context.Context, owner, repo string) ([]PullRequest, error) {
	var raw []ghPull
	if err := c.get(ctx, "/repos/"+owner+"/"+repo+"/pulls?state=open&per_page=100", &raw); err != nil {
		return nil, err
	}
	return toPullRequests(raw), nil
}

// ListPullRequests returns one page (100) of a repo's pull requests in the
// given state ("open", "closed" or "all"), most recently updated first.
func (c *GithubClient) ListPullRequests(ctx context.Context, owner, repo, state string) ([]PullRequest, error) {
	path := fmt.Sprintf("/repos/%s/%s/pulls?state=%s&sort=updated&direction=desc&per_page=100",
		url.PathEscape(owner), url.PathEscape(repo), url.QueryEscape(state))
	var raw []ghPull
	if err := c.get(ctx, path, &raw); err != nil {
		return nil, err
	}
	return toPullRequests(raw), nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/forge ./internal/dispatch ./cmd/bridge -race`
Expected: PASS (dispatch and `cmd/bridge` unchanged and green).

- [ ] **Step 5: Commit**

```bash
git add internal/forge/client.go internal/forge/github.go internal/forge/github_test.go
git commit -m "feat(forge): add ListPullRequests and richer PullRequest fields (#326)"
```

---

### Task 2: Single-PR, files, check-run, status and workflow-run client methods

**Files:**
- Modify: `internal/forge/client.go` (append types)
- Create: `internal/forge/github_pulls.go`
- Test: `internal/forge/github_pulls_test.go`

**Interfaces:**
- Consumes: `ghPull`, `(ghPull).toPullRequest()`, `(*GithubClient).get` from Task 1 / existing code.
- Produces:
  ```go
  type PRFile struct{ Path string; Additions, Deletions int }
  type CheckRun struct{ Name, Status, Conclusion, App string }
  type CommitStatus struct{ Context, State, Description, URL string }
  type WorkflowRun struct{ Name, Event, Status, Conclusion, Actor, TriggeringActor, HeadBranch, HeadSHA, URL string; Created time.Time }
  func (c *GithubClient) GetPullRequest(ctx context.Context, owner, repo string, number int) (PullRequest, error)
  func (c *GithubClient) ListPullRequestFiles(ctx context.Context, owner, repo string, number int) ([]PRFile, error)
  func (c *GithubClient) ListCheckRuns(ctx context.Context, owner, repo, sha string) ([]CheckRun, error)
  func (c *GithubClient) ListCommitStatuses(ctx context.Context, owner, repo, sha string) ([]CommitStatus, error)
  func (c *GithubClient) ListWorkflowRuns(ctx context.Context, owner, repo, branch, headSHA string, limit int) ([]WorkflowRun, error)
  ```

- [ ] **Step 1: Write the failing tests**

Create `internal/forge/github_pulls_test.go`:

```go
package forge

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// serveJSON answers every request with body and records the last request URI.
func serveJSON(t *testing.T, body string, gotURI *string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*gotURI = r.URL.RequestURI()
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestGithubGetPullRequest_MapsSingleEndpointFields(t *testing.T) {
	var uri string
	srv := serveJSON(t, `{"number":90,"title":"t","body":"Closes #41","state":"open",
	  "head":{"ref":"ai/41","sha":"abc"},"base":{"ref":"main"},"user":{"login":"bot"},
	  "merged_at":null,"mergeable_state":"blocked","changed_files":3}`, &uri)

	pr, err := NewGithubClient("tok", srv.URL).GetPullRequest(context.Background(), "o", "r", 90)
	if err != nil {
		t.Fatal(err)
	}
	if uri != "/repos/o/r/pulls/90" {
		t.Errorf("uri: %s", uri)
	}
	if pr.HeadSHA != "abc" || pr.MergeableState != "blocked" || pr.ChangedFiles != 3 || pr.Body != "Closes #41" {
		t.Errorf("pr: %+v", pr)
	}
}

func TestGithubListPullRequestFiles_MapsFilenameToPath(t *testing.T) {
	var uri string
	srv := serveJSON(t, `[{"filename":"a/b.go","additions":5,"deletions":2,"status":"modified"}]`, &uri)

	files, err := NewGithubClient("tok", srv.URL).ListPullRequestFiles(context.Background(), "o", "r", 90)
	if err != nil {
		t.Fatal(err)
	}
	if uri != "/repos/o/r/pulls/90/files?per_page=100" {
		t.Errorf("uri: %s", uri)
	}
	if len(files) != 1 || files[0] != (PRFile{Path: "a/b.go", Additions: 5, Deletions: 2}) {
		t.Errorf("files: %+v", files)
	}
}

func TestGithubListCheckRuns_MapsAppSlugAndNullConclusion(t *testing.T) {
	var uri string
	srv := serveJSON(t, `{"total_count":2,"check_runs":[
	  {"name":"build","status":"completed","conclusion":"failure","app":{"slug":"github-actions"}},
	  {"name":"lint","status":"in_progress","conclusion":null,"app":{"slug":"github-actions"}}]}`, &uri)

	runs, err := NewGithubClient("tok", srv.URL).ListCheckRuns(context.Background(), "o", "r", "abc")
	if err != nil {
		t.Fatal(err)
	}
	if uri != "/repos/o/r/commits/abc/check-runs?per_page=100" {
		t.Errorf("uri: %s", uri)
	}
	want := []CheckRun{
		{Name: "build", Status: "completed", Conclusion: "failure", App: "github-actions"},
		{Name: "lint", Status: "in_progress", Conclusion: "", App: "github-actions"},
	}
	if len(runs) != 2 || runs[0] != want[0] || runs[1] != want[1] {
		t.Errorf("runs: %+v", runs)
	}
}

func TestGithubListCommitStatuses_UsesCombinedStatus(t *testing.T) {
	var uri string
	srv := serveJSON(t, `{"state":"pending","statuses":[
	  {"context":"ci/legacy","state":"success","description":"ok","target_url":"https://ci/1"}]}`, &uri)

	statuses, err := NewGithubClient("tok", srv.URL).ListCommitStatuses(context.Background(), "o", "r", "abc")
	if err != nil {
		t.Fatal(err)
	}
	if uri != "/repos/o/r/commits/abc/status" {
		t.Errorf("uri: %s", uri)
	}
	want := CommitStatus{Context: "ci/legacy", State: "success", Description: "ok", URL: "https://ci/1"}
	if len(statuses) != 1 || statuses[0] != want {
		t.Errorf("statuses: %+v", statuses)
	}
}

func TestGithubListWorkflowRuns_FiltersAndMapsActors(t *testing.T) {
	var uri string
	srv := serveJSON(t, `{"total_count":1,"workflow_runs":[
	  {"name":"agent","event":"issues","status":"completed","conclusion":"success",
	   "actor":{"login":"freaxnx01"},"triggering_actor":{"login":"github-actions[bot]"},
	   "head_branch":"ai/41","head_sha":"abc","created_at":"2026-10-01T10:00:00Z",
	   "html_url":"https://github.com/o/r/actions/runs/1"}]}`, &uri)

	runs, err := NewGithubClient("tok", srv.URL).ListWorkflowRuns(context.Background(), "o", "r", "ai/41", "abc", 20)
	if err != nil {
		t.Fatal(err)
	}
	if uri != "/repos/o/r/actions/runs?branch=ai%2F41&head_sha=abc&per_page=20" {
		t.Errorf("uri: %s", uri)
	}
	if len(runs) != 1 || runs[0].Actor != "freaxnx01" || runs[0].TriggeringActor != "github-actions[bot]" {
		t.Fatalf("runs: %+v", runs)
	}
	if runs[0].Name != "agent" || runs[0].Event != "issues" || runs[0].HeadBranch != "ai/41" || runs[0].Created.IsZero() {
		t.Errorf("run fields: %+v", runs[0])
	}
}

func TestGithubListWorkflowRuns_OmitsEmptyFilters(t *testing.T) {
	var uri string
	srv := serveJSON(t, `{"total_count":0,"workflow_runs":[]}`, &uri)

	if _, err := NewGithubClient("tok", srv.URL).ListWorkflowRuns(context.Background(), "o", "r", "", "", 5); err != nil {
		t.Fatal(err)
	}
	if uri != "/repos/o/r/actions/runs?per_page=5" {
		t.Errorf("uri: %s", uri)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/forge -run 'TestGithub(GetPullRequest|ListPullRequestFiles|ListCheckRuns|ListCommitStatuses|ListWorkflowRuns)' -v`
Expected: FAIL — compile errors (`GetPullRequest undefined`, `PRFile undefined`, …).

- [ ] **Step 3: Implement**

Append to `internal/forge/client.go`:

```go
// PRFile is one file changed by a pull request.
type PRFile struct {
	Path      string `json:"path"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
}

// CheckRun is a check-run on a commit. Conclusion is empty until the run
// completes. App is the reporting app's slug (e.g. "github-actions").
type CheckRun struct {
	Name       string `json:"name"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion,omitempty"`
	App        string `json:"app,omitempty"`
}

// CommitStatus is the latest legacy commit status for one context.
type CommitStatus struct {
	Context     string `json:"context"`
	State       string `json:"state"`
	Description string `json:"description,omitempty"`
	URL         string `json:"url,omitempty"`
}

// WorkflowRun is one GitHub Actions run. Actor is who the run is attributed
// to; TriggeringActor is who actually caused it (they differ on re-runs and
// bot-triggered events).
type WorkflowRun struct {
	Name            string    `json:"name"`
	Event           string    `json:"event"`
	Status          string    `json:"status"`
	Conclusion      string    `json:"conclusion,omitempty"`
	Actor           string    `json:"actor"`
	TriggeringActor string    `json:"triggering_actor"`
	HeadBranch      string    `json:"head_branch,omitempty"`
	HeadSHA         string    `json:"head_sha"`
	Created         time.Time `json:"created"`
	URL             string    `json:"url"`
}
```

Create `internal/forge/github_pulls.go`:

```go
package forge

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"time"
)

func repoPath(owner, repo string) string {
	return "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(repo)
}

// GetPullRequest returns one pull request, including the fields only the
// single-PR endpoint reports (mergeable_state, changed_files).
func (c *GithubClient) GetPullRequest(ctx context.Context, owner, repo string, number int) (PullRequest, error) {
	var raw ghPull
	if err := c.get(ctx, fmt.Sprintf("%s/pulls/%d", repoPath(owner, repo), number), &raw); err != nil {
		return PullRequest{}, err
	}
	return raw.toPullRequest(), nil
}

// ListPullRequestFiles returns the first page (100) of a pull request's
// changed files.
func (c *GithubClient) ListPullRequestFiles(ctx context.Context, owner, repo string, number int) ([]PRFile, error) {
	var raw []struct {
		Filename  string `json:"filename"`
		Additions int    `json:"additions"`
		Deletions int    `json:"deletions"`
	}
	if err := c.get(ctx, fmt.Sprintf("%s/pulls/%d/files?per_page=100", repoPath(owner, repo), number), &raw); err != nil {
		return nil, err
	}
	out := make([]PRFile, 0, len(raw))
	for _, f := range raw {
		out = append(out, PRFile{Path: f.Filename, Additions: f.Additions, Deletions: f.Deletions})
	}
	return out, nil
}

// ListCheckRuns returns the first page (100) of check-runs on a commit.
func (c *GithubClient) ListCheckRuns(ctx context.Context, owner, repo, sha string) ([]CheckRun, error) {
	var raw struct {
		CheckRuns []struct {
			Name       string `json:"name"`
			Status     string `json:"status"`
			Conclusion string `json:"conclusion"`
			App        struct {
				Slug string `json:"slug"`
			} `json:"app"`
		} `json:"check_runs"`
	}
	path := repoPath(owner, repo) + "/commits/" + url.PathEscape(sha) + "/check-runs?per_page=100"
	if err := c.get(ctx, path, &raw); err != nil {
		return nil, err
	}
	out := make([]CheckRun, 0, len(raw.CheckRuns))
	for _, r := range raw.CheckRuns {
		out = append(out, CheckRun{Name: r.Name, Status: r.Status, Conclusion: r.Conclusion, App: r.App.Slug})
	}
	return out, nil
}

// ListCommitStatuses returns the combined status's per-context entries — the
// latest status for each context on the commit.
func (c *GithubClient) ListCommitStatuses(ctx context.Context, owner, repo, sha string) ([]CommitStatus, error) {
	var raw struct {
		Statuses []struct {
			Context     string `json:"context"`
			State       string `json:"state"`
			Description string `json:"description"`
			TargetURL   string `json:"target_url"`
		} `json:"statuses"`
	}
	if err := c.get(ctx, repoPath(owner, repo)+"/commits/"+url.PathEscape(sha)+"/status", &raw); err != nil {
		return nil, err
	}
	out := make([]CommitStatus, 0, len(raw.Statuses))
	for _, s := range raw.Statuses {
		out = append(out, CommitStatus{Context: s.Context, State: s.State, Description: s.Description, URL: s.TargetURL})
	}
	return out, nil
}

type ghWorkflowRun struct {
	Name       string `json:"name"`
	Event      string `json:"event"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
	Actor      struct {
		Login string `json:"login"`
	} `json:"actor"`
	TriggeringActor struct {
		Login string `json:"login"`
	} `json:"triggering_actor"`
	HeadBranch string    `json:"head_branch"`
	HeadSHA    string    `json:"head_sha"`
	CreatedAt  time.Time `json:"created_at"`
	HTMLURL    string    `json:"html_url"`
}

// ListWorkflowRuns returns up to limit Actions runs, newest first, optionally
// narrowed to a branch and/or head commit. Empty filters are omitted.
func (c *GithubClient) ListWorkflowRuns(ctx context.Context, owner, repo, branch, headSHA string, limit int) ([]WorkflowRun, error) {
	q := url.Values{}
	if branch != "" {
		q.Set("branch", branch)
	}
	if headSHA != "" {
		q.Set("head_sha", headSHA)
	}
	q.Set("per_page", strconv.Itoa(limit))
	var raw struct {
		WorkflowRuns []ghWorkflowRun `json:"workflow_runs"`
	}
	if err := c.get(ctx, repoPath(owner, repo)+"/actions/runs?"+q.Encode(), &raw); err != nil {
		return nil, err
	}
	out := make([]WorkflowRun, 0, len(raw.WorkflowRuns))
	for _, r := range raw.WorkflowRuns {
		out = append(out, WorkflowRun{
			Name: r.Name, Event: r.Event, Status: r.Status, Conclusion: r.Conclusion,
			Actor: r.Actor.Login, TriggeringActor: r.TriggeringActor.Login,
			HeadBranch: r.HeadBranch, HeadSHA: r.HeadSHA, Created: r.CreatedAt, URL: r.HTMLURL,
		})
	}
	return out, nil
}
```

Note: `url.Values.Encode()` sorts keys, giving `branch=…&head_sha=…&per_page=…` — the order the test expects.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/forge -race`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/forge/client.go internal/forge/github_pulls.go internal/forge/github_pulls_test.go
git commit -m "feat(forge): read PRs, files, checks, statuses and workflow runs (#326)"
```

---

### Task 3: Capability interfaces and `Capabilities()` entries

**Files:**
- Modify: `internal/mcp/tools.go` (interfaces after `issueReader`, lines 99-101; `Capabilities()` between the `issueReader` check at line 158 and the `repoArchiver` check at line 161)
- Test: `internal/mcp/tools_test.go` (new test function), `internal/mcp/tools_prs_test.go` (create, fake only)

**Interfaces:**
- Consumes: forge types from Tasks 1–2.
- Produces:
  ```go
  type prLister interface { ListPullRequests(ctx context.Context, owner, repo, state string) ([]forge.PullRequest, error) }
  type prReader interface {
      GetPullRequest(ctx context.Context, owner, repo string, number int) (forge.PullRequest, error)
      ListPullRequestFiles(ctx context.Context, owner, repo string, number int) ([]forge.PRFile, error)
      ListCheckRuns(ctx context.Context, owner, repo, sha string) ([]forge.CheckRun, error)
      ListCommitStatuses(ctx context.Context, owner, repo, sha string) ([]forge.CommitStatus, error)
  }
  type runLister interface { ListWorkflowRuns(ctx context.Context, owner, repo, branch, headSHA string, limit int) ([]forge.WorkflowRun, error) }
  // test fake (tools_prs_test.go), used by Tasks 4–6:
  type fakePRs struct{ *fakeReader; ... }; func newFakePRs() *fakePRs; func depsFor(c ForgeReader) Deps
  ```

- [ ] **Step 1: Write the fake and the failing test**

Create `internal/mcp/tools_prs_test.go`:

```go
package mcp

import (
	"context"
	"time"

	"github.com/freaxnx01/bridge/internal/forge"
)

// fakePRs implements prLister, prReader and runLister on top of a tier-1
// reader. It is deliberately not part of fakeFull, so the existing
// "fully capable client" Capabilities expectation is unaffected.
type fakePRs struct {
	*fakeReader
	prs       []forge.PullRequest
	pr        forge.PullRequest
	files     []forge.PRFile
	checkRuns []forge.CheckRun
	statuses  []forge.CommitStatus
	runs      []forge.WorkflowRun

	prsErr, getErr, filesErr, checksErr, statusesErr, runsErr error

	gotState, gotSHA, gotBranch, gotHeadSHA string
	gotLimit                                int
	deadline                                time.Duration // remaining time on the ctx the last call received; 0 if none
}

func newFakePRs() *fakePRs { return &fakePRs{fakeReader: &fakeReader{name: "github"}} }

func depsFor(c ForgeReader) Deps {
	return Deps{ClientFor: func(string, string) ForgeReader { return c }}
}

func (f *fakePRs) record(ctx context.Context) {
	if dl, ok := ctx.Deadline(); ok {
		f.deadline = time.Until(dl)
	}
}

func (f *fakePRs) ListPullRequests(ctx context.Context, _, _, state string) ([]forge.PullRequest, error) {
	f.record(ctx)
	f.gotState = state
	return f.prs, f.prsErr
}

func (f *fakePRs) GetPullRequest(ctx context.Context, _, _ string, _ int) (forge.PullRequest, error) {
	f.record(ctx)
	return f.pr, f.getErr
}

func (f *fakePRs) ListPullRequestFiles(ctx context.Context, _, _ string, _ int) ([]forge.PRFile, error) {
	f.record(ctx)
	return f.files, f.filesErr
}

func (f *fakePRs) ListCheckRuns(ctx context.Context, _, _, sha string) ([]forge.CheckRun, error) {
	f.record(ctx)
	f.gotSHA = sha
	return f.checkRuns, f.checksErr
}

func (f *fakePRs) ListCommitStatuses(ctx context.Context, _, _, _ string) ([]forge.CommitStatus, error) {
	f.record(ctx)
	return f.statuses, f.statusesErr
}

func (f *fakePRs) ListWorkflowRuns(ctx context.Context, _, _, branch, headSHA string, limit int) ([]forge.WorkflowRun, error) {
	f.record(ctx)
	f.gotBranch, f.gotHeadSHA, f.gotLimit = branch, headSHA, limit
	return f.runs, f.runsErr
}
```

Append to `internal/mcp/tools_test.go`:

```go
func TestCapabilities_ReportsPRAndRunTools(t *testing.T) {
	got := Capabilities(newFakePRs())
	want := []string{"list_repos", "list_issues", "list_prs", "get_pr", "list_runs"}
	if !slices.Equal(got, want) {
		t.Fatalf("Capabilities = %v, want %v", got, want)
	}
}
```

(Add `"slices"` to the imports of `tools_test.go` — it is not imported there today.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/mcp -run TestCapabilities_ReportsPRAndRunTools -v`
Expected: FAIL — `Capabilities = [list_repos list_issues], want [... list_prs get_pr list_runs]`.

- [ ] **Step 3: Implement**

In `internal/mcp/tools.go`, after the `issueReader` interface:

```go
// prLister is asserted by list_prs. GitHub-only: Forgejo does not implement
// it, and list_prs reports that as a warning rather than an empty list.
type prLister interface {
	ListPullRequests(ctx context.Context, owner, repo, state string) ([]forge.PullRequest, error)
}

// prReader is asserted by get_pr: the PR itself, its changed files, and the
// check-runs and commit statuses on its head commit.
type prReader interface {
	GetPullRequest(ctx context.Context, owner, repo string, number int) (forge.PullRequest, error)
	ListPullRequestFiles(ctx context.Context, owner, repo string, number int) ([]forge.PRFile, error)
	ListCheckRuns(ctx context.Context, owner, repo, sha string) ([]forge.CheckRun, error)
	ListCommitStatuses(ctx context.Context, owner, repo, sha string) ([]forge.CommitStatus, error)
}

// runLister is asserted by list_runs.
type runLister interface {
	ListWorkflowRuns(ctx context.Context, owner, repo, branch, headSHA string, limit int) ([]forge.WorkflowRun, error)
}
```

In `Capabilities()`, after the `issueReader` check and before `repoArchiver`:

```go
	if _, ok := r.(prLister); ok {
		capabilities = append(capabilities, "list_prs")
	}
	if _, ok := r.(prReader); ok {
		capabilities = append(capabilities, "get_pr")
	}
	if _, ok := r.(runLister); ok {
		capabilities = append(capabilities, "list_runs")
	}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/mcp -race`
Expected: PASS, including the unchanged `TestCapabilities_ReportsToolNamesPerCapability`.

- [ ] **Step 5: Commit**

```bash
git add internal/mcp/tools.go internal/mcp/tools_test.go internal/mcp/tools_prs_test.go
git commit -m "feat(mcp): capability interfaces for PR and workflow-run tools (#326)"
```

---

### Task 4: `list_prs` tool

**Files:**
- Create: `internal/mcp/tools_prs.go`
- Modify: `internal/mcp/server.go` (after the `get_issue` registration, ~line 41)
- Modify: `internal/mcp/server_test.go:43` and `:94` (tool-list expectations)
- Test: `internal/mcp/tools_prs_test.go`

**Interfaces:**
- Consumes: `prLister`, `fakePRs`, `depsFor` (Task 3); `dispatch.ClosesIssue(prBody string, issueNumber int) bool`; `invalidInput(err) error`.
- Produces (used by Tasks 5–6):
  ```go
  const toolTimeout = 30 * time.Second
  func unsupported(forgeName, tool string) []string
  func requireRepo(tool, owner, repo string) error
  func (d Deps) handleListPRs(ctx context.Context, _ *mcp.CallToolRequest, in listPRsInput) (*mcp.CallToolResult, listPRsOutput, error)
  ```

- [ ] **Step 1: Write the failing tests**

Append to `internal/mcp/tools_prs_test.go` (add imports `errors`, `strings`, `testing`):

```go
func TestHandleListPRs_DefaultsToOpenAndOmitsBody(t *testing.T) {
	c := newFakePRs()
	c.prs = []forge.PullRequest{{Number: 90, Title: "t", Body: "Closes #41", Author: "bot", HeadSHA: "abc"}}

	_, out, err := depsFor(c).handleListPRs(context.Background(), nil, listPRsInput{Forge: "github", Owner: "o", Repo: "r"})
	if err != nil {
		t.Fatal(err)
	}
	if c.gotState != "open" {
		t.Errorf("state: %q, want open", c.gotState)
	}
	if len(out.PRs) != 1 || out.PRs[0].Number != 90 || out.PRs[0].Author != "bot" {
		t.Fatalf("prs: %+v", out.PRs)
	}
	if out.PRs[0].Body != "" {
		t.Errorf("list output must omit body, got %q", out.PRs[0].Body)
	}
}

func TestHandleListPRs_ClosesFilterUsesDispatchRule(t *testing.T) {
	c := newFakePRs()
	c.prs = []forge.PullRequest{
		{Number: 1, Body: "Closes #41"},
		{Number: 2, Body: "Closes #410"},
		{Number: 3, Body: "fixes #41 and more"},
		{Number: 4, Body: "refs #41"},
	}

	_, out, err := depsFor(c).handleListPRs(context.Background(), nil,
		listPRsInput{Forge: "github", Owner: "o", Repo: "r", State: "all", Closes: 41})
	if err != nil {
		t.Fatal(err)
	}
	got := []int{}
	for _, p := range out.PRs {
		got = append(got, p.Number)
	}
	if len(got) != 2 || got[0] != 1 || got[1] != 3 {
		t.Fatalf("closes=41 kept %v, want [1 3]", got)
	}
	if c.gotState != "all" {
		t.Errorf("state: %q", c.gotState)
	}
}

func TestHandleListPRs_RejectsUnknownState(t *testing.T) {
	_, _, err := depsFor(newFakePRs()).handleListPRs(context.Background(), nil,
		listPRsInput{Forge: "github", Owner: "o", Repo: "r", State: "merged"})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("want ErrInvalidInput, got %v", err)
	}
}

func TestHandleListPRs_RequiresOwnerAndRepo(t *testing.T) {
	_, _, err := depsFor(newFakePRs()).handleListPRs(context.Background(), nil, listPRsInput{Forge: "github", Owner: "o"})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("want ErrInvalidInput, got %v", err)
	}
}

func TestHandleListPRs_ForgeWithoutCapabilityWarns(t *testing.T) {
	_, out, err := depsFor(&fakeReader{name: "forgejo"}).handleListPRs(context.Background(), nil,
		listPRsInput{Forge: "forgejo", Owner: "o", Repo: "r"})
	if err != nil {
		t.Fatalf("want no error, got %v", err)
	}
	if len(out.PRs) != 0 || len(out.Warnings) != 1 || out.Warnings[0] != "forgejo does not support list_prs" {
		t.Fatalf("out: %+v", out)
	}
}

func TestHandleListPRs_UnconfiguredForgeErrors(t *testing.T) {
	d := Deps{ClientFor: func(string, string) ForgeReader { return nil }}
	_, _, err := d.handleListPRs(context.Background(), nil, listPRsInput{Forge: "github", Owner: "o", Repo: "r"})
	if err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("want not-configured error, got %v", err)
	}
}

func TestHandleListPRs_ClientErrorPropagates(t *testing.T) {
	c := newFakePRs()
	c.prsErr = errors.New("502 bad gateway")
	_, _, err := depsFor(c).handleListPRs(context.Background(), nil, listPRsInput{Forge: "github", Owner: "o", Repo: "r"})
	if err == nil || !strings.Contains(err.Error(), "502") {
		t.Fatalf("want wrapped client error, got %v", err)
	}
}

func TestHandleListPRs_PassesDeadline(t *testing.T) {
	c := newFakePRs()
	if _, _, err := depsFor(c).handleListPRs(context.Background(), nil, listPRsInput{Forge: "github", Owner: "o", Repo: "r"}); err != nil {
		t.Fatal(err)
	}
	if c.deadline <= 0 || c.deadline > toolTimeout {
		t.Fatalf("forge call ctx deadline remaining %v, want (0, %v]", c.deadline, toolTimeout)
	}
}
```

In `internal/mcp/server_test.go` (`TestNewServer_RegistersExpectedToolSet` line 43, `TestNewServer_ReadOnlyOmitsBothWriteTools` line 94), add `"list_prs"` to both `want` slices in alphabetical order. Each tool is registered by the task that implements its handler, so Task 5 adds `"get_pr"` and Task 6 adds `"list_runs"`:

```go
// line 43:
want := []string{"add_labels", "close_issue", "comment_issue", "create_issue", "create_repo", "cross_forge_status", "get_issue", "list_git_forges", "list_issues", "list_prs", "list_repos", "list_tree", "put_file", "read_file", "search_code", "update_issue", "update_repo"}
// line 94:
want := []string{"cross_forge_status", "get_issue", "list_git_forges", "list_issues", "list_prs", "list_repos", "list_tree", "read_file", "search_code"}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/mcp -run 'TestHandleListPRs|TestNewServer' -v`
Expected: FAIL — `handleListPRs undefined` (compile error).

- [ ] **Step 3: Implement**

Create `internal/mcp/tools_prs.go`:

```go
package mcp

import (
	"context"
	"fmt"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/freaxnx01/bridge/internal/dispatch"
	"github.com/freaxnx01/bridge/internal/forge"
)

// toolTimeout bounds a whole PR/run tool call. The forge client's 15 s
// http.Client timeout bounds one request; get_pr makes four, so without this
// a stalled forge could hold the MCP call for a minute or more.
const toolTimeout = 30 * time.Second

// unsupported is the warning a PR/run tool returns for a forge that lacks the
// capability — the search_code convention, so the caller sees "unsupported"
// rather than a silent empty result.
func unsupported(forgeName, tool string) []string {
	return []string{fmt.Sprintf("%s does not support %s", forgeName, tool)}
}

func requireRepo(tool, owner, repo string) error {
	if owner == "" || repo == "" {
		return invalidInput(fmt.Errorf("%s: owner and repo are required", tool))
	}
	return nil
}

type listPRsInput struct {
	Forge  string `json:"forge" jsonschema:"forge hosting the repo: github (forgejo returns a warning)"`
	Owner  string `json:"owner" jsonschema:"repository owner"`
	Repo   string `json:"repo" jsonschema:"repository name"`
	State  string `json:"state,omitempty" jsonschema:"open (default), closed or all"`
	Closes int    `json:"closes,omitempty" jsonschema:"optional issue number; keep only PRs whose body closes it (Closes/Fixes/Resolves #N)"`
}

type listPRsOutput struct {
	PRs      []forge.PullRequest `json:"prs"`
	Warnings []string            `json:"warnings,omitempty"`
}

func prState(s string) (string, error) {
	switch s {
	case "":
		return "open", nil
	case "open", "closed", "all":
		return s, nil
	default:
		return "", invalidInput(fmt.Errorf("list_prs: state %q must be open, closed or all", s))
	}
}

// handleListPRs lists one repo's pull requests, most recently updated first,
// one page of 100. closes narrows them with dispatch's own closing-keyword
// rule, so "which PR closes #N" agrees with what dispatch counts.
func (d Deps) handleListPRs(ctx context.Context, _ *mcp.CallToolRequest, in listPRsInput) (*mcp.CallToolResult, listPRsOutput, error) {
	state, err := prState(in.State)
	if err != nil {
		return nil, listPRsOutput{}, err
	}
	if err := requireRepo("list_prs", in.Owner, in.Repo); err != nil {
		return nil, listPRsOutput{}, err
	}
	client := d.ClientFor(in.Forge, in.Owner)
	if client == nil {
		return nil, listPRsOutput{}, fmt.Errorf("forge %q not configured", in.Forge)
	}
	lister, ok := client.(prLister)
	if !ok {
		return nil, listPRsOutput{PRs: []forge.PullRequest{}, Warnings: unsupported(in.Forge, "list_prs")}, nil
	}
	ctx, cancel := context.WithTimeout(ctx, toolTimeout)
	defer cancel()
	prs, err := lister.ListPullRequests(ctx, in.Owner, in.Repo, state)
	if err != nil {
		return nil, listPRsOutput{}, fmt.Errorf("list prs %s/%s: %w", in.Owner, in.Repo, err)
	}
	return nil, listPRsOutput{PRs: prSummaries(prs, in.Closes)}, nil
}

// prSummaries keeps the PRs that close issue closes (all of them when closes
// is 0) and drops their bodies — get_pr returns the body.
func prSummaries(prs []forge.PullRequest, closes int) []forge.PullRequest {
	out := make([]forge.PullRequest, 0, len(prs))
	for _, p := range prs {
		if closes > 0 && !dispatch.ClosesIssue(p.Body, closes) {
			continue
		}
		p.Body = ""
		out = append(out, p)
	}
	return out
}
```

In `internal/mcp/server.go`, after the `get_issue` registration:

```go
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "list_prs",
		Description: "List a repo's pull requests (number, title, state, draft, author, head/base branch, head SHA, url, created/updated), most recently updated first, one page of 100. state: open (default), closed or all. closes=N keeps only PRs whose body closes issue N. GitHub-only; a Forgejo target returns a warning.",
	}, deps.handleListPRs)
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/mcp -race`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/mcp/tools_prs.go internal/mcp/tools_prs_test.go internal/mcp/server.go internal/mcp/server_test.go
git commit -m "feat(mcp): add read-only list_prs tool (#326)"
```

---

### Task 5: `get_pr` tool

**Files:**
- Modify: `internal/mcp/tools_prs.go`
- Modify: `internal/mcp/server.go` (after `list_prs`)
- Modify: `internal/mcp/server_test.go:43`, `:94` (add `"get_pr"` between `"get_issue"` and `"list_git_forges"`)
- Test: `internal/mcp/tools_prs_test.go`

**Interfaces:**
- Consumes: `prReader`, `toolTimeout`, `unsupported`, `requireRepo`, `fakePRs`, `depsFor`.
- Produces: `func (d Deps) handleGetPR(ctx context.Context, _ *mcp.CallToolRequest, in getPRInput) (*mcp.CallToolResult, getPROutput, error)`; `const maxPRFiles = 100`.

- [ ] **Step 1: Write the failing tests**

Append to `internal/mcp/tools_prs_test.go` (add import `fmt`):

```go
func TestHandleGetPR_ReturnsPRFilesAndChecksForHeadSHA(t *testing.T) {
	c := newFakePRs()
	c.pr = forge.PullRequest{Number: 90, Body: "Closes #41", HeadSHA: "abc", MergeableState: "blocked", ChangedFiles: 1}
	c.files = []forge.PRFile{{Path: "a.go", Additions: 3, Deletions: 1}}
	c.checkRuns = []forge.CheckRun{{Name: "build", Status: "completed", Conclusion: "failure", App: "github-actions"}}
	c.statuses = []forge.CommitStatus{{Context: "ci/legacy", State: "success"}}

	_, out, err := depsFor(c).handleGetPR(context.Background(), nil, getPRInput{Forge: "github", Owner: "o", Repo: "r", Number: 90})
	if err != nil {
		t.Fatal(err)
	}
	if out.PR == nil || out.PR.Body != "Closes #41" || out.PR.MergeableState != "blocked" {
		t.Fatalf("pr: %+v", out.PR)
	}
	if c.gotSHA != "abc" {
		t.Errorf("checks queried for %q, want head SHA abc", c.gotSHA)
	}
	if len(out.Files) != 1 || out.FilesTruncated {
		t.Errorf("files: %+v truncated=%v", out.Files, out.FilesTruncated)
	}
	if len(out.Checks.CheckRuns) != 1 || out.Checks.CheckRuns[0].Conclusion != "failure" {
		t.Errorf("check runs: %+v", out.Checks.CheckRuns)
	}
	if len(out.Checks.Statuses) != 1 || out.Checks.Statuses[0].State != "success" {
		t.Errorf("statuses: %+v", out.Checks.Statuses)
	}
	if len(out.Warnings) != 0 {
		t.Errorf("warnings: %v", out.Warnings)
	}
}

func TestHandleGetPR_CapsFilesAndFlagsTruncation(t *testing.T) {
	c := newFakePRs()
	c.pr = forge.PullRequest{Number: 1, HeadSHA: "abc", ChangedFiles: 250}
	for i := 0; i < 100; i++ {
		c.files = append(c.files, forge.PRFile{Path: fmt.Sprintf("f%d.go", i)})
	}

	_, out, err := depsFor(c).handleGetPR(context.Background(), nil, getPRInput{Forge: "github", Owner: "o", Repo: "r", Number: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Files) != maxPRFiles || !out.FilesTruncated {
		t.Fatalf("want %d files and truncated, got %d truncated=%v", maxPRFiles, len(out.Files), out.FilesTruncated)
	}
}

func TestHandleGetPR_ChecksFailureIsPartialResult(t *testing.T) {
	c := newFakePRs()
	c.pr = forge.PullRequest{Number: 1, HeadSHA: "abc", ChangedFiles: 1}
	c.files = []forge.PRFile{{Path: "a.go"}}
	c.checksErr = errors.New("403 resource not accessible")

	_, out, err := depsFor(c).handleGetPR(context.Background(), nil, getPRInput{Forge: "github", Owner: "o", Repo: "r", Number: 1})
	if err != nil {
		t.Fatalf("a failed check-runs fetch must not fail the call: %v", err)
	}
	if out.PR == nil || len(out.Files) != 1 {
		t.Fatalf("pr and files must still be returned: %+v", out)
	}
	if len(out.Warnings) != 1 || !strings.Contains(out.Warnings[0], "check-runs") || !strings.Contains(out.Warnings[0], "403") {
		t.Fatalf("warnings: %v", out.Warnings)
	}
	if out.Checks.CheckRuns == nil {
		t.Error("check_runs must be an empty list, not null")
	}
}

func TestHandleGetPR_PRFetchFailureErrors(t *testing.T) {
	c := newFakePRs()
	c.getErr = errors.New("404 not found")
	_, _, err := depsFor(c).handleGetPR(context.Background(), nil, getPRInput{Forge: "github", Owner: "o", Repo: "r", Number: 9})
	if err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("want wrapped 404, got %v", err)
	}
}

func TestHandleGetPR_RequiresPositiveNumber(t *testing.T) {
	_, _, err := depsFor(newFakePRs()).handleGetPR(context.Background(), nil, getPRInput{Forge: "github", Owner: "o", Repo: "r"})
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("want ErrInvalidInput, got %v", err)
	}
}

func TestHandleGetPR_ForgeWithoutCapabilityWarns(t *testing.T) {
	_, out, err := depsFor(&fakeReader{name: "forgejo"}).handleGetPR(context.Background(), nil,
		getPRInput{Forge: "forgejo", Owner: "o", Repo: "r", Number: 1})
	if err != nil {
		t.Fatalf("want no error, got %v", err)
	}
	if out.PR != nil || len(out.Warnings) != 1 || out.Warnings[0] != "forgejo does not support get_pr" {
		t.Fatalf("out: %+v", out)
	}
}

func TestHandleGetPR_PassesDeadline(t *testing.T) {
	c := newFakePRs()
	c.pr = forge.PullRequest{Number: 1, HeadSHA: "abc"}
	if _, _, err := depsFor(c).handleGetPR(context.Background(), nil, getPRInput{Forge: "github", Owner: "o", Repo: "r", Number: 1}); err != nil {
		t.Fatal(err)
	}
	if c.deadline <= 0 || c.deadline > toolTimeout {
		t.Fatalf("forge call ctx deadline remaining %v, want (0, %v]", c.deadline, toolTimeout)
	}
}
```

Update `internal/mcp/server_test.go` lines 43 and 94: insert `"get_pr",` after `"get_issue",`.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/mcp -run TestHandleGetPR -v`
Expected: FAIL — `handleGetPR undefined`.

- [ ] **Step 3: Implement**

Append to `internal/mcp/tools_prs.go`:

```go
// maxPRFiles bounds get_pr's file list to one API page; FilesTruncated
// reports when the PR changed more.
const maxPRFiles = 100

type getPRInput struct {
	Forge  string `json:"forge" jsonschema:"forge hosting the repo: github (forgejo returns a warning)"`
	Owner  string `json:"owner" jsonschema:"repository owner"`
	Repo   string `json:"repo" jsonschema:"repository name"`
	Number int    `json:"number" jsonschema:"pull request number"`
}

type prChecks struct {
	CheckRuns []forge.CheckRun     `json:"check_runs"`
	Statuses  []forge.CommitStatus `json:"statuses"`
}

type getPROutput struct {
	PR             *forge.PullRequest `json:"pr,omitempty"`
	Files          []forge.PRFile     `json:"files"`
	FilesTruncated bool               `json:"files_truncated,omitempty"`
	Checks         prChecks           `json:"checks"`
	Warnings       []string           `json:"warnings,omitempty"`
}

// handleGetPR returns one PR with its changed files and the checks on its
// head commit. Only the PR fetch is fatal: a failed files, check-runs or
// statuses fetch becomes a warning on an otherwise complete result.
func (d Deps) handleGetPR(ctx context.Context, _ *mcp.CallToolRequest, in getPRInput) (*mcp.CallToolResult, getPROutput, error) {
	if err := requireRepo("get_pr", in.Owner, in.Repo); err != nil {
		return nil, getPROutput{}, err
	}
	if in.Number <= 0 {
		return nil, getPROutput{}, invalidInput(fmt.Errorf("get_pr: number must be positive"))
	}
	client := d.ClientFor(in.Forge, in.Owner)
	if client == nil {
		return nil, getPROutput{}, fmt.Errorf("forge %q not configured", in.Forge)
	}
	reader, ok := client.(prReader)
	if !ok {
		return nil, getPROutput{Files: []forge.PRFile{}, Warnings: unsupported(in.Forge, "get_pr")}, nil
	}
	ctx, cancel := context.WithTimeout(ctx, toolTimeout)
	defer cancel()
	pr, err := reader.GetPullRequest(ctx, in.Owner, in.Repo, in.Number)
	if err != nil {
		return nil, getPROutput{}, fmt.Errorf("get pr %s/%s#%d: %w", in.Owner, in.Repo, in.Number, err)
	}
	return nil, prDetails(ctx, reader, in, pr), nil
}

// prDetails fetches files, check-runs and statuses for pr, recording each
// failure as a warning instead of failing the call.
func prDetails(ctx context.Context, reader prReader, in getPRInput, pr forge.PullRequest) getPROutput {
	out := getPROutput{
		PR:     &pr,
		Files:  []forge.PRFile{},
		Checks: prChecks{CheckRuns: []forge.CheckRun{}, Statuses: []forge.CommitStatus{}},
	}
	if files, err := reader.ListPullRequestFiles(ctx, in.Owner, in.Repo, in.Number); err != nil {
		out.Warnings = append(out.Warnings, fmt.Sprintf("files: %v", err))
	} else {
		out.Files, out.FilesTruncated = capFiles(files, pr.ChangedFiles)
	}
	if runs, err := reader.ListCheckRuns(ctx, in.Owner, in.Repo, pr.HeadSHA); err != nil {
		out.Warnings = append(out.Warnings, fmt.Sprintf("check-runs: %v", err))
	} else if runs != nil {
		out.Checks.CheckRuns = runs
	}
	if statuses, err := reader.ListCommitStatuses(ctx, in.Owner, in.Repo, pr.HeadSHA); err != nil {
		out.Warnings = append(out.Warnings, fmt.Sprintf("statuses: %v", err))
	} else if statuses != nil {
		out.Checks.Statuses = statuses
	}
	return out
}

// capFiles keeps at most maxPRFiles and reports whether the PR changed more
// files than are returned.
func capFiles(files []forge.PRFile, changedFiles int) ([]forge.PRFile, bool) {
	if files == nil {
		files = []forge.PRFile{}
	}
	if len(files) > maxPRFiles {
		files = files[:maxPRFiles]
	}
	return files, changedFiles > len(files)
}
```

In `internal/mcp/server.go`, after `list_prs`:

```go
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "get_pr",
		Description: "Read one pull request: body, merged, mergeable_state, head SHA, changed files (path, additions, deletions; capped at 100, files_truncated signals more), and checks on the head SHA — check-runs (name, status, conclusion, app) and commit statuses. A failed files/checks fetch lands in warnings without failing the call. GitHub-only; a Forgejo target returns a warning.",
	}, deps.handleGetPR)
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/mcp -race`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/mcp/tools_prs.go internal/mcp/tools_prs_test.go internal/mcp/server.go internal/mcp/server_test.go
git commit -m "feat(mcp): add read-only get_pr tool with checks for the head SHA (#326)"
```

---

### Task 6: `list_runs` tool

**Files:**
- Modify: `internal/mcp/tools_prs.go`
- Modify: `internal/mcp/server.go` (after `get_pr`)
- Modify: `internal/mcp/server_test.go:43`, `:94` (insert `"list_runs"` after `"list_repos"`)
- Test: `internal/mcp/tools_prs_test.go`

**Interfaces:**
- Consumes: `runLister`, `toolTimeout`, `unsupported`, `requireRepo`, `fakePRs`, `depsFor`.
- Produces: `func (d Deps) handleListRuns(ctx context.Context, _ *mcp.CallToolRequest, in listRunsInput) (*mcp.CallToolResult, listRunsOutput, error)`.

- [ ] **Step 1: Write the failing tests**

Append to `internal/mcp/tools_prs_test.go`:

```go
func TestHandleListRuns_PassesFiltersAndReturnsActors(t *testing.T) {
	c := newFakePRs()
	c.runs = []forge.WorkflowRun{{Name: "agent", Actor: "freaxnx01", TriggeringActor: "github-actions[bot]", HeadSHA: "abc"}}

	_, out, err := depsFor(c).handleListRuns(context.Background(), nil,
		listRunsInput{Forge: "github", Owner: "o", Repo: "r", Branch: "ai/41", HeadSHA: "abc", Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if c.gotBranch != "ai/41" || c.gotHeadSHA != "abc" || c.gotLimit != 5 {
		t.Errorf("filters: branch=%q sha=%q limit=%d", c.gotBranch, c.gotHeadSHA, c.gotLimit)
	}
	if len(out.Runs) != 1 || out.Runs[0].Actor != "freaxnx01" || out.Runs[0].TriggeringActor != "github-actions[bot]" {
		t.Fatalf("runs: %+v", out.Runs)
	}
}

func TestHandleListRuns_ClampsLimit(t *testing.T) {
	tests := []struct {
		name  string
		limit int
		want  int
	}{
		{"zero defaults to 20", 0, 20},
		{"negative defaults to 20", -3, 20},
		{"in range kept", 50, 50},
		{"over 100 clamped", 500, 100},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := newFakePRs()
			if _, _, err := depsFor(c).handleListRuns(context.Background(), nil,
				listRunsInput{Forge: "github", Owner: "o", Repo: "r", Limit: tt.limit}); err != nil {
				t.Fatal(err)
			}
			if c.gotLimit != tt.want {
				t.Errorf("limit %d → %d, want %d", tt.limit, c.gotLimit, tt.want)
			}
		})
	}
}

func TestHandleListRuns_ForgeWithoutCapabilityWarns(t *testing.T) {
	_, out, err := depsFor(&fakeReader{name: "forgejo"}).handleListRuns(context.Background(), nil,
		listRunsInput{Forge: "forgejo", Owner: "o", Repo: "r"})
	if err != nil {
		t.Fatalf("want no error, got %v", err)
	}
	if len(out.Runs) != 0 || len(out.Warnings) != 1 || out.Warnings[0] != "forgejo does not support list_runs" {
		t.Fatalf("out: %+v", out)
	}
}

func TestHandleListRuns_ClientErrorPropagates(t *testing.T) {
	c := newFakePRs()
	c.runsErr = errors.New("timeout")
	_, _, err := depsFor(c).handleListRuns(context.Background(), nil, listRunsInput{Forge: "github", Owner: "o", Repo: "r"})
	if err == nil || !strings.Contains(err.Error(), "timeout") {
		t.Fatalf("want wrapped error, got %v", err)
	}
}

func TestHandleListRuns_PassesDeadline(t *testing.T) {
	c := newFakePRs()
	if _, _, err := depsFor(c).handleListRuns(context.Background(), nil, listRunsInput{Forge: "github", Owner: "o", Repo: "r"}); err != nil {
		t.Fatal(err)
	}
	if c.deadline <= 0 || c.deadline > toolTimeout {
		t.Fatalf("forge call ctx deadline remaining %v, want (0, %v]", c.deadline, toolTimeout)
	}
}
```

Update `internal/mcp/server_test.go` lines 43 and 94: insert `"list_runs",` after `"list_repos",`. Final line 43:

```go
want := []string{"add_labels", "close_issue", "comment_issue", "create_issue", "create_repo", "cross_forge_status", "get_issue", "get_pr", "list_git_forges", "list_issues", "list_prs", "list_repos", "list_runs", "list_tree", "put_file", "read_file", "search_code", "update_issue", "update_repo"}
```

Final line 94:

```go
want := []string{"cross_forge_status", "get_issue", "get_pr", "list_git_forges", "list_issues", "list_prs", "list_repos", "list_runs", "list_tree", "read_file", "search_code"}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/mcp -run TestHandleListRuns -v`
Expected: FAIL — `handleListRuns undefined`.

- [ ] **Step 3: Implement**

Append to `internal/mcp/tools_prs.go`:

```go
const (
	defaultRunLimit = 20
	maxRunLimit     = 100
)

type listRunsInput struct {
	Forge   string `json:"forge" jsonschema:"forge hosting the repo: github (forgejo returns a warning)"`
	Owner   string `json:"owner" jsonschema:"repository owner"`
	Repo    string `json:"repo" jsonschema:"repository name"`
	Branch  string `json:"branch,omitempty" jsonschema:"optional branch filter"`
	HeadSHA string `json:"head_sha,omitempty" jsonschema:"optional head commit filter"`
	Limit   int    `json:"limit,omitempty" jsonschema:"max runs, default 20, at most 100"`
}

type listRunsOutput struct {
	Runs     []forge.WorkflowRun `json:"runs"`
	Warnings []string            `json:"warnings,omitempty"`
}

func runLimit(n int) int {
	if n <= 0 {
		return defaultRunLimit
	}
	return min(n, maxRunLimit)
}

// handleListRuns lists a repo's GitHub Actions runs, newest first, with the
// actor and triggering actor of each — what tells a dispatched run apart from
// a hand-started one.
func (d Deps) handleListRuns(ctx context.Context, _ *mcp.CallToolRequest, in listRunsInput) (*mcp.CallToolResult, listRunsOutput, error) {
	if err := requireRepo("list_runs", in.Owner, in.Repo); err != nil {
		return nil, listRunsOutput{}, err
	}
	client := d.ClientFor(in.Forge, in.Owner)
	if client == nil {
		return nil, listRunsOutput{}, fmt.Errorf("forge %q not configured", in.Forge)
	}
	lister, ok := client.(runLister)
	if !ok {
		return nil, listRunsOutput{Runs: []forge.WorkflowRun{}, Warnings: unsupported(in.Forge, "list_runs")}, nil
	}
	ctx, cancel := context.WithTimeout(ctx, toolTimeout)
	defer cancel()
	runs, err := lister.ListWorkflowRuns(ctx, in.Owner, in.Repo, in.Branch, in.HeadSHA, runLimit(in.Limit))
	if err != nil {
		return nil, listRunsOutput{}, fmt.Errorf("list runs %s/%s: %w", in.Owner, in.Repo, err)
	}
	if runs == nil {
		runs = []forge.WorkflowRun{}
	}
	return nil, listRunsOutput{Runs: runs}, nil
}
```

(`min` is the Go 1.21+ builtin; `go.mod` declares `go 1.25.0`.)

In `internal/mcp/server.go`, after `get_pr`:

```go
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "list_runs",
		Description: "List a repo's GitHub Actions runs, newest first: workflow name, event, status, conclusion, actor, triggering_actor, head branch/SHA, created, url. Optional branch / head_sha filters; limit defaults to 20, max 100. GitHub-only; a Forgejo target returns a warning.",
	}, deps.handleListRuns)
```

Also update the `NewServer` doc comment (`server.go:7-9`), which says "seven cross-forge tools", to not enumerate a count: `// NewServer builds the Bridge MCP server with the cross-forge tools registered.`

- [ ] **Step 4: Run the full verification**

Run:
```bash
gofmt -l .
go vet ./...
golangci-lint run
go test -race ./...
```
Expected: `gofmt` prints nothing; vet and lint clean; all packages PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/mcp/tools_prs.go internal/mcp/tools_prs_test.go internal/mcp/server.go internal/mcp/server_test.go
git commit -m "feat(mcp): add read-only list_runs tool with actor and triggering_actor (#326)"
```

---

### Task 7: Docs

**Files:**
- Modify: `docs/mcp-cheatsheet.md` (tool table, after the `list_issues` row at line 24)
- Modify: `README.md:110`
- Modify: `CHANGELOG.md` (`[Unreleased]` → `### Added`, top of list)

**Interfaces:** none.

- [ ] **Step 1: Cheatsheet rows**

Insert after the `list_issues` row in `docs/mcp-cheatsheet.md`:

```markdown
| `list_prs` | List a repo's pull requests (author, head/base branch, head SHA, draft, state, url) | **GitHub-only, read-only** — a Forgejo target returns a warning, not an empty list. `state` is `open` (default), `closed` or `all`; one page of the 100 most recently updated. `closes: N` keeps PRs whose body closes issue N, using the same rule dispatch uses to decide an issue already has a PR |
| `get_pr` | One PR's body, merged, mergeable_state, head SHA, changed files, and the checks on its head SHA | **GitHub-only, read-only.** Checks are check-runs (name, status, conclusion, app) plus commit statuses. Files are capped at 100 with `files_truncated`. A failed files/check-runs/statuses fetch lands in `warnings` without failing the call |
| `list_runs` | List a repo's GitHub Actions runs with `actor` and `triggering_actor` | **GitHub-only, read-only.** Optional `branch` / `head_sha` filters; `limit` defaults to 20, max 100 |
```

Add one line under the table (before the next section):

```markdown
`list_prs`, `get_pr` and `list_runs` each bound the whole call to 30 s, so a stalled GitHub request fails fast instead of holding the MCP call. Merging, approving and re-running stay human acts — none of them is exposed.
```

- [ ] **Step 2: README**

In `README.md:110`, replace `exposing four cross-forge tools (\`list_repos\`, \`read_file\`, \`create_issue\`, \`cross_forge_status\`) over GitHub + Forgejo. \`--read-only\` omits the write tool by construction.` with:

```markdown
exposing cross-forge tools over GitHub + Forgejo — repos, files, issues, pull requests with their checks, and Actions runs; see the tool table in [`docs/mcp-cheatsheet.md`](docs/mcp-cheatsheet.md). `--read-only` omits the write tools by construction.
```

(keep the rest of the paragraph — `--host`/`--port`/`--no-auth` — unchanged).

- [ ] **Step 3: CHANGELOG**

Add as the first bullet under `## [Unreleased]` → `### Added`:

```markdown
- `bridge mcp serve`: read-only **`list_prs`**, **`get_pr`** and **`list_runs`** tools, so an MCP client can verify a dispatched ai-implement run — its PR, the PR's check-runs and commit statuses on the head SHA, and which actor triggered its Actions runs. `list_prs closes=N` uses dispatch's own closing-keyword rule. GitHub-only; a Forgejo target returns a warning. Each call is bounded to 30 s. (#326)
```

- [ ] **Step 4: Verify**

Run: `go test -race ./... && git diff --stat`
Expected: PASS; diff touches only the three docs files in this task.

- [ ] **Step 5: Commit**

```bash
git add docs/mcp-cheatsheet.md README.md CHANGELOG.md
git commit -m "docs(mcp): document list_prs, get_pr and list_runs (#326)"
```
