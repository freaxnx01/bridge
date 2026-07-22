# MCP Mutating Tools (Tier 1) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add four MCP mutating tools — `close_issue`, `update_issue`, `add_labels`, `comment_issue` — plus the guardrail infrastructure they and future destructive tools depend on: `Deps.AllowDestructive`, the `internal/audit` package, and audit-aware `list_git_forges` reporting.

**Architecture:** Follows the existing `internal/mcp` capability-interface pattern exactly: each tool asserts a narrow one-method interface (`issueCloser`, `issueUpdater`, `labelAdder`, `issueCommenter`) on whatever `Deps.ClientFor` returns, mirrors the `create_issue`/`create_repo` draft-by-default shape, and is registered only when `!Deps.ReadOnly`. `internal/forge`'s `GithubClient`/`ForgejoClient` each gain a `patch` helper (mirroring the existing `post`) and four new methods hitting GitHub/Gitea's issue PATCH/POST endpoints. A new `internal/audit` package (no MCP dependency) is injected into `Deps.Audit`; every tier-1 handler logs exactly one entry per call that reaches the forge (success or forge error) — see Spec Reconciliation below for why drafts and pre-flight errors log nothing.

**Tech Stack:** Go (stdlib `net/http`, `encoding/json`, `log/slog`, `testing`), `github.com/modelcontextprotocol/go-sdk/mcp`. No new dependencies.

**Spec:** `docs/superpowers/specs/2026-07-22-mcp-mutating-tools-design.md`

**Scope (per the spec's Rollout section):** tier 1 tools only (`close_issue`, `update_issue`, `add_labels`, `comment_issue`) + guardrail infra (`AllowDestructive`, `internal/audit`) + confirming the `updated_at` regression test is green. `delete_issue` is out of scope (dropped by the spec — neither forge can delete an issue). Tier 2 (`list_milestones`/`list_prs`/`search_issues`) and tiers 3–4 (`archive_repo`/`delete_repo`) are **not** implemented by this plan; only their capability-interface *stubs* are declared so `Capabilities()` has a complete switch.

## Global Constraints

- Use Test-Driven Development for every task: write a failing test first, watch it fail, implement minimally to pass, verify green.
- **No new Go modules.** No `testify`, `mockery`, `gomock`, or a new logging library — hand-rolled fakes, stdlib `log/slog`.
- **No changes to `internal/oauth`, `internal/overview`, `internal/remote`, or `internal/web`.** Out of scope.
- **`delete_issue` is not implemented.** Do not add it — the spec's Step 0 confirmed neither forge supports it.
- Never discard an error with `_ =` outside `defer resp.Body.Close()`/`defer f.Close()`-style best-effort cleanup already established in the codebase.
- Errors return up to the command layer (`cmd/bridge/mcp.go`) — no `os.Exit` or stderr writes below `main`.
- Error wrap messages are lower-case with no trailing punctuation, wrapped with `%w`.
- Inspect wrapped errors with `errors.Is`/`errors.As`, never string-matching on `err.Error()`.
- No package-level mutable global state (no mutable `var` maps or singletons).
- No `//nolint` suppressions.
- Every task ends green on: `gofmt -l .` (empty output), `go vet ./...`, `golangci-lint run`, `go test -race ./...`.
- `golangci-lint` is pinned to v2.1.6 (`go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.1.6` if absent).
- Run all commands from the repo root. The local `go` binary is 1.22.2 while `go.mod` requires 1.25, so the toolchain resolves only inside the repo.

## Spec Reconciliation (read before Task 1)

The spec's Guardrails section says *"Drafts and refusals are also logged"*, but its Testing section — the concrete, literal test list — says item 1 is *"`confirm=false` → draft output, zero fake calls, **zero audit entries**."* These two statements conflict for tier 1. This plan follows the **Testing section literally**, since that is what the tasks below are graded against:

- `confirm=false` (draft) → **no** audit entry, for any of the four tools.
- `confirm=true`, forge not configured / client lacks the capability → **no** audit entry (pre-flight validation, not an attempted mutation).
- `confirm=true`, capability call returns an error → **one** audit entry, `Outcome: "error"`.
- `confirm=true`, capability call succeeds → **one** audit entry, `Outcome: "success"`.

`Deps.AllowDestructive` is wired end-to-end (flag, env var, `Deps` field, `list_git_forges` reporting) in this plan, but **no tier-1 handler gates on it** — the spec's own Testing section scopes the `AllowDestructive=false → refusal` behavior to "tier 3/4 only, specced not implemented." Tier-1 tools are reversible (a closed issue reopens, a label removes) and were never described as gated by `AllowDestructive` in their tool-contract tables.

The spec's Bug fix section says close/update/label/comment response structs must "declare state, closed_at/updated_at explicitly" to avoid the anonymous-struct field-drop bug fixed in `313b0d2`. This plan applies that at the **forge-client parsing layer**: the `raw` struct each new `GithubClient`/`ForgejoClient` method decodes JSON into names `State`/`UpdatedAt` explicitly (same pattern as the existing `CreateIssue`). The MCP-level output types then carry `*forge.Issue`/`*forge.Comment` pointers, exactly like `create_issue`/`create_repo` already do — no separate top-level `state`/`closed_at` JSON keys are needed since `forge.Issue.State` and `forge.Issue.Updated` already carry that data with explicit field names.

**The `updated_at` regression test already exists** on `main` (added in `313b0d2`, prior to this issue): `TestGithubCreateIssue`/`TestGithubCreateRepo` (`internal/forge/github_test.go`) and `TestForgejoCreateIssue`/`TestForgejoCreateRepo` (`internal/forge/forgejo_test.go`) each assert `.Updated`/`.UpdatedAt` is non-zero. **No new test is added for this** — Task 6's final verification step runs the full suite and confirms these four are still green, which is the entirety of what "the `updated_at` regression test" requires from this plan.

## File Structure

| File | Responsibility | Task |
|---|---|---|
| `internal/audit/audit.go` | `Entry`, `Logger`, `Open`, `Log` | 1 |
| `internal/audit/audit_test.go` | JSON-line + append-across-opens tests | 1 |
| `internal/forge/client.go` | `Comment` type, `Issue.State` field | 2 |
| `internal/forge/github.go` | `patch` helper, `CloseIssue`/`UpdateIssue`/`AddLabels`/`CommentIssue` | 2 |
| `internal/forge/github_test.go` | tests for the four new GitHub methods | 2 |
| `internal/forge/forgejo.go` | `patch` helper, `CloseIssue`/`UpdateIssue`/`AddLabels`/`CommentIssue` | 3 |
| `internal/forge/forgejo_test.go` | tests for the four new Forgejo methods | 3 |
| `internal/mcp/tools.go` | 4 new capability interfaces + 2 stub interfaces, `Capabilities()`, `Deps.AllowDestructive`/`Audit`, `auditLog` helper | 4 |
| `internal/mcp/tools_read.go` | `isWriteTool` extension, `listGitForgesOutput.AllowDestructive` | 4 |
| `internal/mcp/tools_test.go` | `fakeIssues` gains 4 methods, `TestCapabilities_*` updated | 4 |
| `internal/mcp/tools_read_test.go` | `list_git_forges` capability-count + `allow_destructive` tests updated | 4 |
| `internal/mcp/tools_write.go` | `close_issue`/`update_issue`/`add_labels`/`comment_issue` handlers + I/O types | 5 |
| `internal/mcp/tools_write_test.go` | tests for the four new handlers | 5 |
| `internal/mcp/server.go` | registers the four new tools under `!ReadOnly` | 5 |
| `cmd/bridge/mcp.go` | `--allow-destructive` flag, `BRIDGE_MCP_ALLOW_DESTRUCTIVE` env, audit log path resolution + wiring | 6 |

---

### Task 1: `internal/audit` package

**Files:**
- Create: `internal/audit/audit.go`
- Test: `internal/audit/audit_test.go`

**Interfaces:**
- Produces: `audit.Entry{Time time.Time, Forge, Owner, Repo, Tool string, Confirm bool, Outcome string}`; `audit.Logger`; `audit.Open(path string) (*Logger, error)`; `(*Logger).Log(e Entry)`.

- [ ] **Step 1: Write the failing tests**

```go
// internal/audit/audit_test.go
package audit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLogger_LogWritesOneValidJSONLinePerCall(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")

	l, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	l.Log(Entry{
		Time:    time.Date(2026, 7, 22, 10, 0, 0, 0, time.UTC),
		Forge:   "github",
		Owner:   "freaxnx01",
		Repo:    "bridge",
		Tool:    "close_issue",
		Confirm: true,
		Outcome: "success",
	})

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) != 1 {
		t.Fatalf("want 1 line, got %d: %q", len(lines), string(data))
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &got); err != nil {
		t.Fatalf("line is not valid JSON: %v", err)
	}
	if got["forge"] != "github" || got["tool"] != "close_issue" || got["outcome"] != "success" || got["confirm"] != true {
		t.Errorf("entry: %+v", got)
	}
}

func TestLogger_AppendsAcrossMultipleOpensOfSamePath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")

	first, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	first.Log(Entry{Tool: "close_issue", Outcome: "success"})

	second, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	second.Log(Entry{Tool: "update_issue", Outcome: "error"})

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("want 2 lines across two opens, got %d: %q", len(lines), string(data))
	}
}

func TestOpen_CreatesParentDirectory(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "audit.jsonl")

	if _, err := Open(path); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("want audit file created, got %v", err)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/audit/... -v`
Expected: FAIL — `undefined: Open`, `undefined: Entry`, `undefined: Logger` (package `audit` doesn't exist yet).

- [ ] **Step 3: Write the implementation**

```go
// internal/audit/audit.go
package audit

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"
)

// Entry is one audited call to a mutating MCP tool.
type Entry struct {
	Time    time.Time
	Forge   string
	Owner   string
	Repo    string
	Tool    string
	Confirm bool
	Outcome string // "success" | "error" | "refused" | "refused_name_mismatch"
}

// Logger appends one JSON object per line to an audit log file.
type Logger struct {
	slog *slog.Logger
}

// Open opens (creating if absent) the audit log at path in append mode,
// creating any missing parent directories. Callers may call Open on the same
// path more than once (e.g. across process restarts); each returned Logger
// appends independently.
func Open(path string) (*Logger, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create audit log directory for %s: %w", path, err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open audit log %s: %w", path, err)
	}
	return &Logger{slog: slog.New(slog.NewJSONHandler(f, nil))}, nil
}

// Log appends e as one JSON line. A zero e.Time is stamped with time.Now().
func (l *Logger) Log(e Entry) {
	if e.Time.IsZero() {
		e.Time = time.Now()
	}
	l.slog.Info("audit",
		"time", e.Time,
		"forge", e.Forge,
		"owner", e.Owner,
		"repo", e.Repo,
		"tool", e.Tool,
		"confirm", e.Confirm,
		"outcome", e.Outcome,
	)
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/audit/... -v`
Expected: PASS (all three tests).

- [ ] **Step 5: Gate and commit**

Run: `gofmt -l internal/audit/ && go vet ./internal/audit/... && golangci-lint run ./internal/audit/...`
Expected: no output from `gofmt -l`, no errors.

```bash
git add internal/audit/audit.go internal/audit/audit_test.go
git commit -m "feat(mcp): add internal/audit package

Refs #211"
```

---

### Task 2: `internal/forge` — GitHub tier-1 mutating methods

**Files:**
- Modify: `internal/forge/client.go` (add `Comment` type, `Issue.State` field)
- Modify: `internal/forge/github.go` (add `patch` helper + 4 methods)
- Test: `internal/forge/github_test.go`

**Interfaces:**
- Consumes: `GithubClient.post`/`.get` (existing, `internal/forge/github.go`).
- Produces: `forge.Comment{ID int, Body string, Created time.Time}`; `forge.Issue.State string`; `(*GithubClient).CloseIssue(ctx, owner, repo string, number int, stateReason string) (Issue, error)`; `(*GithubClient).UpdateIssue(ctx, owner, repo string, number int, title, body *string) (Issue, error)`; `(*GithubClient).AddLabels(ctx, owner, repo string, number int, labels []string) ([]string, error)`; `(*GithubClient).CommentIssue(ctx, owner, repo string, number int, body string) (Comment, error)`.

- [ ] **Step 1: Add the shared types**

In `internal/forge/client.go`, add a `State` field to `Issue` and a new `Comment` type:

```go
type Issue struct {
	Forge   string    `json:"forge"`
	Repo    string    `json:"repo"`
	Number  int       `json:"number"`
	Title   string    `json:"title"`
	URL     string    `json:"url"`
	State   string    `json:"state,omitempty"`
	Labels  []string  `json:"labels,omitempty"`
	Updated time.Time `json:"updated,omitempty"`
}

// Comment is a single issue comment.
type Comment struct {
	ID      int       `json:"id"`
	Body    string    `json:"body"`
	Created time.Time `json:"created,omitempty"`
}
```

(`State` is inserted as a new field on the existing `Issue` struct; `Comment` is a new type appended after it.)

- [ ] **Step 2: Write the failing tests**

Append to `internal/forge/github_test.go`:

```go
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
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `go test ./internal/forge/... -run TestGithubCloseIssue -v`
Expected: FAIL — `c.CloseIssue undefined (type *GithubClient has no field or method CloseIssue)`.

- [ ] **Step 4: Implement the `patch` helper and four methods**

In `internal/forge/github.go`, add after `post`:

```go
func (c *GithubClient) patch(ctx context.Context, path string, body, out any) error {
	buf, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, "PATCH", c.baseURL+path, bytes.NewReader(buf))
	if err != nil {
		return err
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }() // best-effort close
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("github %s: %s: %s", path, resp.Status, string(b))
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
```

Add after `CreateIssue`:

```go
// CloseIssue closes owner/repo#number. stateReason, when non-empty, is one of
// GitHub's completed/not_planned/duplicate values; empty omits the field.
func (c *GithubClient) CloseIssue(ctx context.Context, owner, repo string, number int, stateReason string) (Issue, error) {
	req := map[string]any{"state": "closed"}
	if stateReason != "" {
		req["state_reason"] = stateReason
	}
	var raw struct {
		Number    int       `json:"number"`
		Title     string    `json:"title"`
		State     string    `json:"state"`
		HTMLURL   string    `json:"html_url"`
		UpdatedAt time.Time `json:"updated_at"`
	}
	path := fmt.Sprintf("/repos/%s/%s/issues/%d", owner, repo, number)
	if err := c.patch(ctx, path, req, &raw); err != nil {
		return Issue{}, err
	}
	return Issue{
		Forge: "github", Repo: owner + "/" + repo,
		Number: raw.Number, Title: raw.Title, State: raw.State,
		URL: raw.HTMLURL, Updated: raw.UpdatedAt,
	}, nil
}

// UpdateIssue updates owner/repo#number's title and/or body. A nil pointer
// leaves that field unchanged; at least one is expected to be non-nil by the
// caller (the MCP handler enforces this at the boundary).
func (c *GithubClient) UpdateIssue(ctx context.Context, owner, repo string, number int, title, body *string) (Issue, error) {
	req := map[string]any{}
	if title != nil {
		req["title"] = *title
	}
	if body != nil {
		req["body"] = *body
	}
	var raw struct {
		Number    int       `json:"number"`
		Title     string    `json:"title"`
		State     string    `json:"state"`
		HTMLURL   string    `json:"html_url"`
		UpdatedAt time.Time `json:"updated_at"`
	}
	path := fmt.Sprintf("/repos/%s/%s/issues/%d", owner, repo, number)
	if err := c.patch(ctx, path, req, &raw); err != nil {
		return Issue{}, err
	}
	return Issue{
		Forge: "github", Repo: owner + "/" + repo,
		Number: raw.Number, Title: raw.Title, State: raw.State,
		URL: raw.HTMLURL, Updated: raw.UpdatedAt,
	}, nil
}

// AddLabels adds labels to owner/repo#number and returns the issue's full
// label set after the call.
func (c *GithubClient) AddLabels(ctx context.Context, owner, repo string, number int, labels []string) ([]string, error) {
	req := map[string]any{"labels": labels}
	var raw []struct {
		Name string `json:"name"`
	}
	path := fmt.Sprintf("/repos/%s/%s/issues/%d/labels", owner, repo, number)
	if err := c.post(ctx, path, req, &raw); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(raw))
	for _, l := range raw {
		out = append(out, l.Name)
	}
	return out, nil
}

// CommentIssue posts a comment on owner/repo#number.
func (c *GithubClient) CommentIssue(ctx context.Context, owner, repo string, number int, body string) (Comment, error) {
	req := map[string]any{"body": body}
	var raw struct {
		ID        int       `json:"id"`
		Body      string    `json:"body"`
		CreatedAt time.Time `json:"created_at"`
	}
	path := fmt.Sprintf("/repos/%s/%s/issues/%d/comments", owner, repo, number)
	if err := c.post(ctx, path, req, &raw); err != nil {
		return Comment{}, err
	}
	return Comment{ID: raw.ID, Body: raw.Body, Created: raw.CreatedAt}, nil
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/forge/... -run 'TestGithubCloseIssue|TestGithubUpdateIssue|TestGithubAddLabels|TestGithubCommentIssue' -v`
Expected: PASS (5 tests).

- [ ] **Step 6: Run the full forge package suite**

Run: `go test -race ./internal/forge/...`
Expected: PASS, including the pre-existing `TestGithubCreateIssue`/`TestGithubCreateRepo`.

- [ ] **Step 7: Gate and commit**

Run: `gofmt -l internal/forge/ && go vet ./internal/forge/... && golangci-lint run ./internal/forge/...`

```bash
git add internal/forge/client.go internal/forge/github.go internal/forge/github_test.go
git commit -m "feat(forge): add GitHub CloseIssue/UpdateIssue/AddLabels/CommentIssue

Refs #211"
```

---

### Task 3: `internal/forge` — Forgejo tier-1 mutating methods

**Files:**
- Modify: `internal/forge/forgejo.go` (add `patch` helper + 4 methods)
- Test: `internal/forge/forgejo_test.go`

**Interfaces:**
- Consumes: `forge.Comment`, `forge.Issue.State` (Task 2). `ForgejoClient.post` (existing).
- Produces: `(*ForgejoClient).CloseIssue`/`.UpdateIssue`/`.AddLabels`/`.CommentIssue` — identical signatures to Task 2's `GithubClient` methods, so both types satisfy the same `internal/mcp` capability interfaces (Task 4).

- [ ] **Step 1: Write the failing tests**

Append to `internal/forge/forgejo_test.go`:

```go
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
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/forge/... -run TestForgejoCloseIssue -v`
Expected: FAIL — `c.CloseIssue undefined (type *ForgejoClient has no field or method CloseIssue)`.

- [ ] **Step 3: Implement the `patch` helper and four methods**

In `internal/forge/forgejo.go`, add after `post`:

```go
func (c *ForgejoClient) patch(ctx context.Context, path string, body, out any) error {
	buf, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, "PATCH", c.baseURL+path, bytes.NewReader(buf))
	if err != nil {
		return err
	}
	if c.token != "" {
		req.Header.Set("Authorization", "token "+c.token)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }() // best-effort close
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("forgejo %s: %s: %s", path, resp.Status, string(b))
	}
	return json.NewDecoder(resp.Body).Decode(out)
}
```

Add after `CreateIssue`:

```go
// CloseIssue closes owner/repo#number. stateReason is accepted for interface
// parity with GithubClient but Forgejo/Gitea has no equivalent field, so it
// is never sent.
func (c *ForgejoClient) CloseIssue(ctx context.Context, owner, repo string, number int, _ string) (Issue, error) {
	req := map[string]any{"state": "closed"}
	var raw struct {
		Number    int       `json:"number"`
		Title     string    `json:"title"`
		State     string    `json:"state"`
		HTMLURL   string    `json:"html_url"`
		UpdatedAt time.Time `json:"updated_at"`
	}
	path := fmt.Sprintf("/api/v1/repos/%s/%s/issues/%d", owner, repo, number)
	if err := c.patch(ctx, path, req, &raw); err != nil {
		return Issue{}, err
	}
	return Issue{
		Forge: "forgejo", Repo: owner + "/" + repo,
		Number: raw.Number, Title: raw.Title, State: raw.State,
		URL: raw.HTMLURL, Updated: raw.UpdatedAt,
	}, nil
}

// UpdateIssue updates owner/repo#number's title and/or body. A nil pointer
// leaves that field unchanged.
func (c *ForgejoClient) UpdateIssue(ctx context.Context, owner, repo string, number int, title, body *string) (Issue, error) {
	req := map[string]any{}
	if title != nil {
		req["title"] = *title
	}
	if body != nil {
		req["body"] = *body
	}
	var raw struct {
		Number    int       `json:"number"`
		Title     string    `json:"title"`
		State     string    `json:"state"`
		HTMLURL   string    `json:"html_url"`
		UpdatedAt time.Time `json:"updated_at"`
	}
	path := fmt.Sprintf("/api/v1/repos/%s/%s/issues/%d", owner, repo, number)
	if err := c.patch(ctx, path, req, &raw); err != nil {
		return Issue{}, err
	}
	return Issue{
		Forge: "forgejo", Repo: owner + "/" + repo,
		Number: raw.Number, Title: raw.Title, State: raw.State,
		URL: raw.HTMLURL, Updated: raw.UpdatedAt,
	}, nil
}

// AddLabels adds labels to owner/repo#number and returns the issue's full
// label set after the call.
func (c *ForgejoClient) AddLabels(ctx context.Context, owner, repo string, number int, labels []string) ([]string, error) {
	req := map[string]any{"labels": labels}
	var raw []struct {
		Name string `json:"name"`
	}
	path := fmt.Sprintf("/api/v1/repos/%s/%s/issues/%d/labels", owner, repo, number)
	if err := c.post(ctx, path, req, &raw); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(raw))
	for _, l := range raw {
		out = append(out, l.Name)
	}
	return out, nil
}

// CommentIssue posts a comment on owner/repo#number.
func (c *ForgejoClient) CommentIssue(ctx context.Context, owner, repo string, number int, body string) (Comment, error) {
	req := map[string]any{"body": body}
	var raw struct {
		ID        int       `json:"id"`
		Body      string    `json:"body"`
		CreatedAt time.Time `json:"created_at"`
	}
	path := fmt.Sprintf("/api/v1/repos/%s/%s/issues/%d/comments", owner, repo, number)
	if err := c.post(ctx, path, req, &raw); err != nil {
		return Comment{}, err
	}
	return Comment{ID: raw.ID, Body: raw.Body, Created: raw.CreatedAt}, nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/forge/... -run 'TestForgejoCloseIssue|TestForgejoUpdateIssue|TestForgejoAddLabels|TestForgejoCommentIssue' -v`
Expected: PASS (5 tests).

- [ ] **Step 5: Run the full forge package suite**

Run: `go test -race ./internal/forge/...`
Expected: PASS, including the pre-existing `TestForgejoCreateIssue`/`TestForgejoCreateRepo`.

- [ ] **Step 6: Gate and commit**

Run: `gofmt -l internal/forge/ && go vet ./internal/forge/... && golangci-lint run ./internal/forge/...`

```bash
git add internal/forge/forgejo.go internal/forge/forgejo_test.go
git commit -m "feat(forge): add Forgejo CloseIssue/UpdateIssue/AddLabels/CommentIssue

Refs #211"
```

---

### Task 4: `internal/mcp` guardrail infra — capability interfaces, `Deps`, `Capabilities()`, `list_git_forges`

**Files:**
- Modify: `internal/mcp/tools.go`
- Modify: `internal/mcp/tools_read.go`
- Modify: `internal/mcp/tools_test.go`
- Modify: `internal/mcp/tools_read_test.go`

**Interfaces:**
- Consumes: `forge.Comment`, `forge.Issue.State` (Task 2/3); `audit.Entry`, `audit.Logger`, `audit.Open` (Task 1).
- Produces: `issueCloser`, `issueUpdater`, `labelAdder`, `issueCommenter`, `repoArchiver`, `repoDeleter` interfaces; `Deps.AllowDestructive bool`, `Deps.Audit *audit.Logger`; `(Deps).auditLog(e audit.Entry)`; `listGitForgesOutput.AllowDestructive bool`. Task 5's handlers consume all of these.

- [ ] **Step 1: Write the failing tests**

In `internal/mcp/tools_test.go`, extend `fakeIssues` with the four new capability methods (it already carries `forgeName`):

```go
type fakeIssues struct {
	forgeName     string
	createCalled  *int
	closeCalled   *int
	closeErr      error
	updateCalled  *int
	updateErr     error
	labelsCalled  *int
	labelsErr     error
	commentCalled *int
	commentErr    error
}

func (f *fakeIssues) CreateIssue(_ context.Context, owner, repo, title, _ string) (forge.Issue, error) {
	if f.createCalled != nil {
		*f.createCalled++
	}
	return forge.Issue{Forge: f.forgeName, Repo: owner + "/" + repo, Number: 42, Title: title}, nil
}

func (f *fakeIssues) CloseIssue(_ context.Context, owner, repo string, number int, _ string) (forge.Issue, error) {
	if f.closeCalled != nil {
		*f.closeCalled++
	}
	if f.closeErr != nil {
		return forge.Issue{}, f.closeErr
	}
	return forge.Issue{Forge: f.forgeName, Repo: owner + "/" + repo, Number: number, State: "closed"}, nil
}

func (f *fakeIssues) UpdateIssue(_ context.Context, owner, repo string, number int, title, _ *string) (forge.Issue, error) {
	if f.updateCalled != nil {
		*f.updateCalled++
	}
	if f.updateErr != nil {
		return forge.Issue{}, f.updateErr
	}
	is := forge.Issue{Forge: f.forgeName, Repo: owner + "/" + repo, Number: number}
	if title != nil {
		is.Title = *title
	}
	return is, nil
}

func (f *fakeIssues) AddLabels(_ context.Context, _, _ string, _ int, labels []string) ([]string, error) {
	if f.labelsCalled != nil {
		*f.labelsCalled++
	}
	if f.labelsErr != nil {
		return nil, f.labelsErr
	}
	return labels, nil
}

func (f *fakeIssues) CommentIssue(_ context.Context, _, _ string, _ int, body string) (forge.Comment, error) {
	if f.commentCalled != nil {
		*f.commentCalled++
	}
	if f.commentErr != nil {
		return forge.Comment{}, f.commentErr
	}
	return forge.Comment{ID: 7, Body: body}, nil
}
```

Update `TestCapabilities_ReportsToolNamesPerCapability`'s "fully capable client reports every tool" case:

```go
		{
			name:   "fully capable client reports every tool",
			client: newFakeFull("github"),
			want: []string{
				"list_repos", "list_issues", "read_file", "create_issue", "create_repo",
				"close_issue", "update_issue", "add_labels", "comment_issue",
			},
		},
```

In `internal/mcp/tools_read_test.go`, update the two exact-count assertions:

```go
	if len(out.Forges[0].Capabilities) != 3 {
		t.Errorf("want the 3 read tools, got %v", out.Forges[0].Capabilities)
	}
```
stays `3` (unaffected — the new tools are all write tools, filtered out same as `create_issue`/`create_repo` already are). The `!= 5` case becomes `!= 9`:

```go
	if len(out.Forges[0].Capabilities) != 9 {
		t.Errorf("want all 9 tools, got %v", out.Forges[0].Capabilities)
	}
```

Add a new test for `AllowDestructive` reporting, appended to `tools_read_test.go`:

```go
func TestHandleListGitForges_ReportsAllowDestructive(t *testing.T) {
	d := Deps{AllowDestructive: true, ClientFor: func(string, string) ForgeReader { return nil }}

	_, out, err := d.handleListGitForges(context.Background(), nil, listGitForgesInput{})
	if err != nil {
		t.Fatal(err)
	}
	if !out.AllowDestructive {
		t.Error("allow_destructive must reflect Deps.AllowDestructive")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/mcp/... -run 'TestCapabilities_|TestHandleListGitForges' -v`
Expected: FAIL — compile errors (`fakeIssues` methods don't satisfy interfaces that don't exist yet; `Deps.AllowDestructive`/`out.AllowDestructive` undefined; capability counts wrong).

- [ ] **Step 3: Implement**

In `internal/mcp/tools.go`, add the import and the four capability interfaces + two stubs after `repoCreator`:

```go
import (
	"context"
	"fmt"

	"github.com/freaxnx01/bridge/internal/audit"
	"github.com/freaxnx01/bridge/internal/forge"
	"github.com/freaxnx01/bridge/internal/overview"
)
```

```go
// issueCloser is asserted by close_issue.
type issueCloser interface {
	CloseIssue(ctx context.Context, owner, repo string, number int, stateReason string) (forge.Issue, error)
}

// issueUpdater is asserted by update_issue.
type issueUpdater interface {
	UpdateIssue(ctx context.Context, owner, repo string, number int, title, body *string) (forge.Issue, error)
}

// labelAdder is asserted by add_labels.
type labelAdder interface {
	AddLabels(ctx context.Context, owner, repo string, number int, labels []string) ([]string, error)
}

// issueCommenter is asserted by comment_issue.
type issueCommenter interface {
	CommentIssue(ctx context.Context, owner, repo string, number int, body string) (forge.Comment, error)
}

// repoArchiver and repoDeleter are tier-3/4 capability stubs: declared so
// Capabilities' switch is complete before those tiers are implemented, but no
// concrete client satisfies them yet.
type repoArchiver interface {
	ArchiveRepo(ctx context.Context, owner, repo string) (forge.RepoRef, error)
}

type repoDeleter interface {
	DeleteRepo(ctx context.Context, owner, repo string) error
}
```

Extend `Capabilities`:

```go
func Capabilities(r ForgeReader) []string {
	if r == nil {
		return nil
	}
	capabilities := []string{"list_repos", "list_issues"}
	if _, ok := r.(fileReader); ok {
		capabilities = append(capabilities, "read_file")
	}
	if _, ok := r.(issueCreator); ok {
		capabilities = append(capabilities, "create_issue")
	}
	if _, ok := r.(repoCreator); ok {
		capabilities = append(capabilities, "create_repo")
	}
	if _, ok := r.(issueCloser); ok {
		capabilities = append(capabilities, "close_issue")
	}
	if _, ok := r.(issueUpdater); ok {
		capabilities = append(capabilities, "update_issue")
	}
	if _, ok := r.(labelAdder); ok {
		capabilities = append(capabilities, "add_labels")
	}
	if _, ok := r.(issueCommenter); ok {
		capabilities = append(capabilities, "comment_issue")
	}
	if _, ok := r.(repoArchiver); ok {
		capabilities = append(capabilities, "archive_repo")
	}
	if _, ok := r.(repoDeleter); ok {
		capabilities = append(capabilities, "delete_repo")
	}
	return capabilities
}
```

Extend `Deps` and add `auditLog`:

```go
type Deps struct {
	ReadOnly         bool
	AllowDestructive bool
	DefaultOwners    []Target
	ClientFor        func(forgeName, owner string) ForgeReader
	BuildOverview    func(ctx context.Context) (overview.Snapshot, error)
	Audit            *audit.Logger
}

// auditLog appends e to Deps.Audit. A no-op when Audit is nil (tests, or a
// caller that hasn't wired one up) so handlers never need a nil check.
func (d Deps) auditLog(e audit.Entry) {
	if d.Audit == nil {
		return
	}
	d.Audit.Log(e)
}
```

In `internal/mcp/tools_read.go`, extend `isWriteTool`:

```go
func isWriteTool(name string) bool {
	switch name {
	case "create_issue", "create_repo", "close_issue", "update_issue", "add_labels", "comment_issue":
		return true
	default:
		return false
	}
}
```

Add `AllowDestructive` to `listGitForgesOutput` and set it in the handler:

```go
type listGitForgesOutput struct {
	Forges           []forgeStatus `json:"forges"`
	ReadOnly         bool          `json:"read_only"`
	AllowDestructive bool          `json:"allow_destructive"`
}
```

```go
	return nil, listGitForgesOutput{Forges: forges, ReadOnly: d.ReadOnly, AllowDestructive: d.AllowDestructive}, nil
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/mcp/... -run 'TestCapabilities_|TestHandleListGitForges' -v`
Expected: PASS.

- [ ] **Step 5: Run the full mcp package suite**

Run: `go test -race ./internal/mcp/...`
Expected: PASS (existing `create_issue`/`create_repo` tests unaffected — no signature changes to anything they use).

- [ ] **Step 6: Gate and commit**

Run: `gofmt -l internal/mcp/ && go vet ./internal/mcp/... && golangci-lint run ./internal/mcp/...`

```bash
git add internal/mcp/tools.go internal/mcp/tools_read.go internal/mcp/tools_test.go internal/mcp/tools_read_test.go
git commit -m "feat(mcp): add tier-1 capability interfaces, AllowDestructive, audit hook

Refs #211"
```

---

### Task 5: `internal/mcp` tier-1 handlers — `close_issue`, `update_issue`, `add_labels`, `comment_issue`

**Files:**
- Modify: `internal/mcp/tools_write.go`
- Modify: `internal/mcp/server.go`
- Modify: `internal/mcp/tools_write_test.go`

**Interfaces:**
- Consumes: `issueCloser`/`issueUpdater`/`labelAdder`/`issueCommenter`, `Deps.auditLog` (Task 4); `audit.Open` (Task 1); `fakeIssues` (Task 4).
- Produces: `Deps.handleCloseIssue`, `Deps.handleUpdateIssue`, `Deps.handleAddLabels`, `Deps.handleCommentIssue` — registered as MCP tools `close_issue`, `update_issue`, `add_labels`, `comment_issue`.

- [ ] **Step 1: Write the failing tests**

Add to `internal/mcp/tools_write_test.go` (add `"os"`, `"path/filepath"`, and `"github.com/freaxnx01/bridge/internal/audit"` to its imports):

```go
func TestHandleCloseIssue_DraftDoesNotCloseOrLogAudit(t *testing.T) {
	calls := 0
	gh := newFakeFull("github")
	gh.closeCalled = &calls
	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
	logger, err := audit.Open(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	d := depsWith(map[string]*fakeFull{"github": gh}, nil)
	d.Audit = logger

	_, out, err := d.handleCloseIssue(context.Background(), nil, closeIssueInput{
		Forge: "github", Owner: "o", Repo: "r", IssueNumber: 142, Confirm: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !out.Draft {
		t.Fatalf("Confirm=false must return a draft: %+v", out)
	}
	if calls != 0 {
		t.Fatalf("draft must not call CloseIssue, got %d calls", calls)
	}
	data, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != 0 {
		t.Errorf("draft must not log an audit entry, got %q", string(data))
	}
}

func TestHandleCloseIssue_ConfirmClosesAndLogsSuccess(t *testing.T) {
	calls := 0
	gh := newFakeFull("github")
	gh.closeCalled = &calls
	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
	logger, err := audit.Open(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	d := depsWith(map[string]*fakeFull{"github": gh}, nil)
	d.Audit = logger

	_, out, err := d.handleCloseIssue(context.Background(), nil, closeIssueInput{
		Forge: "github", Owner: "o", Repo: "r", IssueNumber: 142, StateReason: "completed", Confirm: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Draft {
		t.Fatalf("Confirm=true must not be a draft: %+v", out)
	}
	if out.Issue == nil || out.Issue.State != "closed" {
		t.Fatalf("want closed issue in response: %+v", out.Issue)
	}
	if calls != 1 {
		t.Fatalf("want exactly 1 CloseIssue call, got %d", calls)
	}
	data, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"tool":"close_issue"`) || !strings.Contains(string(data), `"outcome":"success"`) {
		t.Errorf("want a success audit entry, got %q", string(data))
	}
}

func TestHandleCloseIssue_ForgeErrorLogsErrorOutcome(t *testing.T) {
	gh := newFakeFull("github")
	gh.closeErr = errors.New("boom")
	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
	logger, err := audit.Open(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	d := depsWith(map[string]*fakeFull{"github": gh}, nil)
	d.Audit = logger

	_, _, err = d.handleCloseIssue(context.Background(), nil, closeIssueInput{
		Forge: "github", Owner: "o", Repo: "r", IssueNumber: 142, Confirm: true,
	})
	if err == nil {
		t.Fatal("want an error when CloseIssue fails")
	}
	data, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"outcome":"error"`) {
		t.Errorf("want an error audit entry, got %q", string(data))
	}
}

func TestHandleCloseIssue_TierOneClientReportsUnsupportedNotUnconfigured(t *testing.T) {
	d := Deps{ClientFor: func(string, string) ForgeReader { return &fakeReader{name: "gitlab"} }}

	_, _, err := d.handleCloseIssue(context.Background(), nil,
		closeIssueInput{Forge: "gitlab", Owner: "o", Repo: "r", IssueNumber: 1, Confirm: true})
	if err == nil {
		t.Fatal("want an error for a client without CloseIssue, got nil")
	}
	if strings.Contains(err.Error(), "not configured") {
		t.Fatalf("a resolved but incapable client must not be reported as unconfigured: %v", err)
	}
	if !strings.Contains(err.Error(), "does not support") {
		t.Fatalf("want a does-not-support error, got %v", err)
	}
}

func TestHandleCloseIssue_UnconfiguredForgeErrors(t *testing.T) {
	d := depsWith(map[string]*fakeFull{}, nil)

	_, _, err := d.handleCloseIssue(context.Background(), nil,
		closeIssueInput{Forge: "gitlab", Owner: "o", Repo: "r", IssueNumber: 1, Confirm: true})
	if err == nil {
		t.Fatal("want an error for an unconfigured forge, got nil")
	}
	if !strings.Contains(err.Error(), "not configured") {
		t.Errorf("want a not-configured error, got %v", err)
	}
}

func TestHandleUpdateIssue_RequiresTitleOrBody(t *testing.T) {
	d := depsWith(map[string]*fakeFull{}, nil)

	_, _, err := d.handleUpdateIssue(context.Background(), nil,
		updateIssueInput{Forge: "github", Owner: "o", Repo: "r", IssueNumber: 1, Confirm: true})
	if err == nil {
		t.Fatal("want an error when both title and body are empty")
	}
	if !strings.Contains(err.Error(), "at least one of title or body") {
		t.Errorf("want a title-or-body-required error, got %v", err)
	}
}

func TestHandleUpdateIssue_DraftDoesNotUpdate(t *testing.T) {
	calls := 0
	gh := newFakeFull("github")
	gh.updateCalled = &calls
	d := depsWith(map[string]*fakeFull{"github": gh}, nil)

	_, out, err := d.handleUpdateIssue(context.Background(), nil, updateIssueInput{
		Forge: "github", Owner: "o", Repo: "r", IssueNumber: 142, Title: "t", Confirm: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !out.Draft {
		t.Fatalf("Confirm=false must return a draft: %+v", out)
	}
	if calls != 0 {
		t.Fatalf("draft must not call UpdateIssue, got %d calls", calls)
	}
}

func TestHandleUpdateIssue_ConfirmUpdates(t *testing.T) {
	calls := 0
	gh := newFakeFull("github")
	gh.updateCalled = &calls
	d := depsWith(map[string]*fakeFull{"github": gh}, nil)

	_, out, err := d.handleUpdateIssue(context.Background(), nil, updateIssueInput{
		Forge: "github", Owner: "o", Repo: "r", IssueNumber: 142, Title: "new title", Confirm: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Draft {
		t.Fatalf("Confirm=true must not be a draft: %+v", out)
	}
	if out.Issue == nil || out.Issue.Title != "new title" {
		t.Fatalf("want updated title in response: %+v", out.Issue)
	}
	if calls != 1 {
		t.Fatalf("want exactly 1 UpdateIssue call, got %d", calls)
	}
}

func TestHandleUpdateIssue_TierOneClientReportsUnsupportedNotUnconfigured(t *testing.T) {
	d := Deps{ClientFor: func(string, string) ForgeReader { return &fakeReader{name: "gitlab"} }}

	_, _, err := d.handleUpdateIssue(context.Background(), nil,
		updateIssueInput{Forge: "gitlab", Owner: "o", Repo: "r", IssueNumber: 1, Title: "t", Confirm: true})
	if err == nil {
		t.Fatal("want an error for a client without UpdateIssue, got nil")
	}
	if !strings.Contains(err.Error(), "does not support") {
		t.Fatalf("want a does-not-support error, got %v", err)
	}
}

func TestHandleAddLabels_RequiresNonEmptyLabels(t *testing.T) {
	d := depsWith(map[string]*fakeFull{}, nil)

	_, _, err := d.handleAddLabels(context.Background(), nil,
		addLabelsInput{Forge: "github", Owner: "o", Repo: "r", IssueNumber: 1, Confirm: true})
	if err == nil {
		t.Fatal("want an error when labels is empty")
	}
	if !strings.Contains(err.Error(), "labels must not be empty") {
		t.Errorf("want a labels-required error, got %v", err)
	}
}

func TestHandleAddLabels_DraftDoesNotAdd(t *testing.T) {
	calls := 0
	gh := newFakeFull("github")
	gh.labelsCalled = &calls
	d := depsWith(map[string]*fakeFull{"github": gh}, nil)

	_, out, err := d.handleAddLabels(context.Background(), nil, addLabelsInput{
		Forge: "github", Owner: "o", Repo: "r", IssueNumber: 142, Labels: []string{"bug"}, Confirm: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !out.Draft {
		t.Fatalf("Confirm=false must return a draft: %+v", out)
	}
	if calls != 0 {
		t.Fatalf("draft must not call AddLabels, got %d calls", calls)
	}
}

func TestHandleAddLabels_ConfirmAdds(t *testing.T) {
	calls := 0
	gh := newFakeFull("github")
	gh.labelsCalled = &calls
	d := depsWith(map[string]*fakeFull{"github": gh}, nil)

	_, out, err := d.handleAddLabels(context.Background(), nil, addLabelsInput{
		Forge: "github", Owner: "o", Repo: "r", IssueNumber: 142, Labels: []string{"bug", "p1"}, Confirm: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Draft {
		t.Fatalf("Confirm=true must not be a draft: %+v", out)
	}
	if len(out.Labels) != 2 {
		t.Fatalf("want the returned label set: %+v", out.Labels)
	}
	if calls != 1 {
		t.Fatalf("want exactly 1 AddLabels call, got %d", calls)
	}
}

func TestHandleAddLabels_TierOneClientReportsUnsupportedNotUnconfigured(t *testing.T) {
	d := Deps{ClientFor: func(string, string) ForgeReader { return &fakeReader{name: "gitlab"} }}

	_, _, err := d.handleAddLabels(context.Background(), nil,
		addLabelsInput{Forge: "gitlab", Owner: "o", Repo: "r", IssueNumber: 1, Labels: []string{"bug"}, Confirm: true})
	if err == nil {
		t.Fatal("want an error for a client without AddLabels, got nil")
	}
	if !strings.Contains(err.Error(), "does not support") {
		t.Fatalf("want a does-not-support error, got %v", err)
	}
}

func TestHandleCommentIssue_DraftDoesNotComment(t *testing.T) {
	calls := 0
	gh := newFakeFull("github")
	gh.commentCalled = &calls
	d := depsWith(map[string]*fakeFull{"github": gh}, nil)

	_, out, err := d.handleCommentIssue(context.Background(), nil, commentIssueInput{
		Forge: "github", Owner: "o", Repo: "r", IssueNumber: 142, Body: "lgtm", Confirm: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !out.Draft {
		t.Fatalf("Confirm=false must return a draft: %+v", out)
	}
	if calls != 0 {
		t.Fatalf("draft must not call CommentIssue, got %d calls", calls)
	}
}

func TestHandleCommentIssue_ConfirmComments(t *testing.T) {
	calls := 0
	gh := newFakeFull("github")
	gh.commentCalled = &calls
	d := depsWith(map[string]*fakeFull{"github": gh}, nil)

	_, out, err := d.handleCommentIssue(context.Background(), nil, commentIssueInput{
		Forge: "github", Owner: "o", Repo: "r", IssueNumber: 142, Body: "lgtm", Confirm: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Draft {
		t.Fatalf("Confirm=true must not be a draft: %+v", out)
	}
	if out.Comment == nil || out.Comment.Body != "lgtm" {
		t.Fatalf("want the posted comment in response: %+v", out.Comment)
	}
	if calls != 1 {
		t.Fatalf("want exactly 1 CommentIssue call, got %d", calls)
	}
}

func TestHandleCommentIssue_TierOneClientReportsUnsupportedNotUnconfigured(t *testing.T) {
	d := Deps{ClientFor: func(string, string) ForgeReader { return &fakeReader{name: "gitlab"} }}

	_, _, err := d.handleCommentIssue(context.Background(), nil,
		commentIssueInput{Forge: "gitlab", Owner: "o", Repo: "r", IssueNumber: 1, Body: "lgtm", Confirm: true})
	if err == nil {
		t.Fatal("want an error for a client without CommentIssue, got nil")
	}
	if !strings.Contains(err.Error(), "does not support") {
		t.Fatalf("want a does-not-support error, got %v", err)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/mcp/... -run 'TestHandleCloseIssue|TestHandleUpdateIssue|TestHandleAddLabels|TestHandleCommentIssue' -v`
Expected: FAIL — `d.handleCloseIssue undefined`, `closeIssueInput undefined`, etc.

- [ ] **Step 3: Implement the handlers**

Append to `internal/mcp/tools_write.go`:

```go
type closeIssueInput struct {
	Forge       string `json:"forge" jsonschema:"forge hosting the repo: github or forgejo"`
	Owner       string `json:"owner" jsonschema:"repository owner"`
	Repo        string `json:"repo" jsonschema:"repository name"`
	IssueNumber int    `json:"issue_number" jsonschema:"issue number to close"`
	StateReason string `json:"state_reason,omitempty" jsonschema:"GitHub only: completed, not_planned, or duplicate; ignored on Forgejo"`
	Confirm     bool   `json:"confirm,omitempty" jsonschema:"when false, returns a draft without closing; set true to close"`
}

type closeIssueOutput struct {
	Draft       bool         `json:"draft"`
	Forge       string       `json:"forge"`
	Owner       string       `json:"owner"`
	Repo        string       `json:"repo"`
	IssueNumber int          `json:"issue_number"`
	Issue       *forge.Issue `json:"issue,omitempty"`
}

func (d Deps) handleCloseIssue(ctx context.Context, _ *mcp.CallToolRequest, in closeIssueInput) (*mcp.CallToolResult, closeIssueOutput, error) {
	draft := closeIssueOutput{
		Draft: true,
		Forge: in.Forge, Owner: in.Owner, Repo: in.Repo, IssueNumber: in.IssueNumber,
	}
	if !in.Confirm {
		return nil, draft, nil
	}
	client := d.ClientFor(in.Forge, in.Owner)
	if client == nil {
		return nil, closeIssueOutput{}, fmt.Errorf("forge %q not configured", in.Forge)
	}
	closer, ok := client.(issueCloser)
	if !ok {
		return nil, closeIssueOutput{}, fmt.Errorf("forge %q does not support closing issues", in.Forge)
	}
	issue, err := closer.CloseIssue(ctx, in.Owner, in.Repo, in.IssueNumber, in.StateReason)
	if err != nil {
		d.auditLog(audit.Entry{Forge: in.Forge, Owner: in.Owner, Repo: in.Repo, Tool: "close_issue", Confirm: true, Outcome: "error"})
		return nil, closeIssueOutput{}, fmt.Errorf("close issue %s/%s#%d: %w", in.Owner, in.Repo, in.IssueNumber, err)
	}
	d.auditLog(audit.Entry{Forge: in.Forge, Owner: in.Owner, Repo: in.Repo, Tool: "close_issue", Confirm: true, Outcome: "success"})
	return nil, closeIssueOutput{
		Draft: false,
		Forge: in.Forge, Owner: in.Owner, Repo: in.Repo, IssueNumber: in.IssueNumber,
		Issue: &issue,
	}, nil
}

type updateIssueInput struct {
	Forge       string `json:"forge" jsonschema:"forge hosting the repo: github or forgejo"`
	Owner       string `json:"owner" jsonschema:"repository owner"`
	Repo        string `json:"repo" jsonschema:"repository name"`
	IssueNumber int    `json:"issue_number" jsonschema:"issue number to update"`
	Title       string `json:"title,omitempty" jsonschema:"new title; at least one of title/body is required"`
	Body        string `json:"body,omitempty" jsonschema:"new body (markdown); at least one of title/body is required"`
	Confirm     bool   `json:"confirm,omitempty" jsonschema:"when false, returns a draft without updating; set true to update"`
}

type updateIssueOutput struct {
	Draft       bool         `json:"draft"`
	Forge       string       `json:"forge"`
	Owner       string       `json:"owner"`
	Repo        string       `json:"repo"`
	IssueNumber int          `json:"issue_number"`
	Title       string       `json:"title,omitempty"`
	Body        string       `json:"body,omitempty"`
	Issue       *forge.Issue `json:"issue,omitempty"`
}

func (d Deps) handleUpdateIssue(ctx context.Context, _ *mcp.CallToolRequest, in updateIssueInput) (*mcp.CallToolResult, updateIssueOutput, error) {
	if in.Title == "" && in.Body == "" {
		return nil, updateIssueOutput{}, fmt.Errorf("update issue %s/%s#%d: at least one of title or body is required", in.Owner, in.Repo, in.IssueNumber)
	}
	draft := updateIssueOutput{
		Draft: true,
		Forge: in.Forge, Owner: in.Owner, Repo: in.Repo, IssueNumber: in.IssueNumber,
		Title: in.Title, Body: in.Body,
	}
	if !in.Confirm {
		return nil, draft, nil
	}
	client := d.ClientFor(in.Forge, in.Owner)
	if client == nil {
		return nil, updateIssueOutput{}, fmt.Errorf("forge %q not configured", in.Forge)
	}
	updater, ok := client.(issueUpdater)
	if !ok {
		return nil, updateIssueOutput{}, fmt.Errorf("forge %q does not support updating issues", in.Forge)
	}
	var titlePtr, bodyPtr *string
	if in.Title != "" {
		titlePtr = &in.Title
	}
	if in.Body != "" {
		bodyPtr = &in.Body
	}
	issue, err := updater.UpdateIssue(ctx, in.Owner, in.Repo, in.IssueNumber, titlePtr, bodyPtr)
	if err != nil {
		d.auditLog(audit.Entry{Forge: in.Forge, Owner: in.Owner, Repo: in.Repo, Tool: "update_issue", Confirm: true, Outcome: "error"})
		return nil, updateIssueOutput{}, fmt.Errorf("update issue %s/%s#%d: %w", in.Owner, in.Repo, in.IssueNumber, err)
	}
	d.auditLog(audit.Entry{Forge: in.Forge, Owner: in.Owner, Repo: in.Repo, Tool: "update_issue", Confirm: true, Outcome: "success"})
	return nil, updateIssueOutput{
		Draft: false,
		Forge: in.Forge, Owner: in.Owner, Repo: in.Repo, IssueNumber: in.IssueNumber,
		Title: in.Title, Body: in.Body,
		Issue: &issue,
	}, nil
}

type addLabelsInput struct {
	Forge       string   `json:"forge" jsonschema:"forge hosting the repo: github or forgejo"`
	Owner       string   `json:"owner" jsonschema:"repository owner"`
	Repo        string   `json:"repo" jsonschema:"repository name"`
	IssueNumber int      `json:"issue_number" jsonschema:"issue number to label"`
	Labels      []string `json:"labels" jsonschema:"labels to add (non-empty)"`
	Confirm     bool     `json:"confirm,omitempty" jsonschema:"when false, returns a draft without adding labels; set true to add"`
}

type addLabelsOutput struct {
	Draft       bool     `json:"draft"`
	Forge       string   `json:"forge"`
	Owner       string   `json:"owner"`
	Repo        string   `json:"repo"`
	IssueNumber int      `json:"issue_number"`
	Labels      []string `json:"labels,omitempty"`
}

func (d Deps) handleAddLabels(ctx context.Context, _ *mcp.CallToolRequest, in addLabelsInput) (*mcp.CallToolResult, addLabelsOutput, error) {
	if len(in.Labels) == 0 {
		return nil, addLabelsOutput{}, fmt.Errorf("add labels %s/%s#%d: labels must not be empty", in.Owner, in.Repo, in.IssueNumber)
	}
	draft := addLabelsOutput{
		Draft: true,
		Forge: in.Forge, Owner: in.Owner, Repo: in.Repo, IssueNumber: in.IssueNumber,
		Labels: in.Labels,
	}
	if !in.Confirm {
		return nil, draft, nil
	}
	client := d.ClientFor(in.Forge, in.Owner)
	if client == nil {
		return nil, addLabelsOutput{}, fmt.Errorf("forge %q not configured", in.Forge)
	}
	adder, ok := client.(labelAdder)
	if !ok {
		return nil, addLabelsOutput{}, fmt.Errorf("forge %q does not support adding labels", in.Forge)
	}
	labels, err := adder.AddLabels(ctx, in.Owner, in.Repo, in.IssueNumber, in.Labels)
	if err != nil {
		d.auditLog(audit.Entry{Forge: in.Forge, Owner: in.Owner, Repo: in.Repo, Tool: "add_labels", Confirm: true, Outcome: "error"})
		return nil, addLabelsOutput{}, fmt.Errorf("add labels %s/%s#%d: %w", in.Owner, in.Repo, in.IssueNumber, err)
	}
	d.auditLog(audit.Entry{Forge: in.Forge, Owner: in.Owner, Repo: in.Repo, Tool: "add_labels", Confirm: true, Outcome: "success"})
	return nil, addLabelsOutput{
		Draft: false,
		Forge: in.Forge, Owner: in.Owner, Repo: in.Repo, IssueNumber: in.IssueNumber,
		Labels: labels,
	}, nil
}

type commentIssueInput struct {
	Forge       string `json:"forge" jsonschema:"forge hosting the repo: github or forgejo"`
	Owner       string `json:"owner" jsonschema:"repository owner"`
	Repo        string `json:"repo" jsonschema:"repository name"`
	IssueNumber int    `json:"issue_number" jsonschema:"issue number to comment on"`
	Body        string `json:"body" jsonschema:"comment body (markdown)"`
	Confirm     bool   `json:"confirm,omitempty" jsonschema:"when false, returns a draft without commenting; set true to comment"`
}

type commentIssueOutput struct {
	Draft       bool           `json:"draft"`
	Forge       string         `json:"forge"`
	Owner       string         `json:"owner"`
	Repo        string         `json:"repo"`
	IssueNumber int            `json:"issue_number"`
	Body        string         `json:"body,omitempty"`
	Comment     *forge.Comment `json:"comment,omitempty"`
}

func (d Deps) handleCommentIssue(ctx context.Context, _ *mcp.CallToolRequest, in commentIssueInput) (*mcp.CallToolResult, commentIssueOutput, error) {
	draft := commentIssueOutput{
		Draft: true,
		Forge: in.Forge, Owner: in.Owner, Repo: in.Repo, IssueNumber: in.IssueNumber,
		Body: in.Body,
	}
	if !in.Confirm {
		return nil, draft, nil
	}
	client := d.ClientFor(in.Forge, in.Owner)
	if client == nil {
		return nil, commentIssueOutput{}, fmt.Errorf("forge %q not configured", in.Forge)
	}
	commenter, ok := client.(issueCommenter)
	if !ok {
		return nil, commentIssueOutput{}, fmt.Errorf("forge %q does not support commenting on issues", in.Forge)
	}
	comment, err := commenter.CommentIssue(ctx, in.Owner, in.Repo, in.IssueNumber, in.Body)
	if err != nil {
		d.auditLog(audit.Entry{Forge: in.Forge, Owner: in.Owner, Repo: in.Repo, Tool: "comment_issue", Confirm: true, Outcome: "error"})
		return nil, commentIssueOutput{}, fmt.Errorf("comment issue %s/%s#%d: %w", in.Owner, in.Repo, in.IssueNumber, err)
	}
	d.auditLog(audit.Entry{Forge: in.Forge, Owner: in.Owner, Repo: in.Repo, Tool: "comment_issue", Confirm: true, Outcome: "success"})
	return nil, commentIssueOutput{
		Draft: false,
		Forge: in.Forge, Owner: in.Owner, Repo: in.Repo, IssueNumber: in.IssueNumber,
		Body: in.Body, Comment: &comment,
	}, nil
}
```

Add the import to `internal/mcp/tools_write.go`'s import block:

```go
import (
	"context"
	"errors"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/freaxnx01/bridge/internal/audit"
	"github.com/freaxnx01/bridge/internal/forge"
)
```

Register the four tools in `internal/mcp/server.go`, inside the existing `if !deps.ReadOnly` block, after `create_repo`:

```go
		mcp.AddTool(srv, &mcp.Tool{
			Name:        "close_issue",
			Description: "Close an issue. Without confirm=true this returns a draft and closes nothing.",
		}, deps.handleCloseIssue)

		mcp.AddTool(srv, &mcp.Tool{
			Name:        "update_issue",
			Description: "Update an issue's title and/or body. Without confirm=true this returns a draft and updates nothing.",
		}, deps.handleUpdateIssue)

		mcp.AddTool(srv, &mcp.Tool{
			Name:        "add_labels",
			Description: "Add labels to an issue. Without confirm=true this returns a draft and adds nothing.",
		}, deps.handleAddLabels)

		mcp.AddTool(srv, &mcp.Tool{
			Name:        "comment_issue",
			Description: "Post a comment on an issue. Without confirm=true this returns a draft and posts nothing.",
		}, deps.handleCommentIssue)
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/mcp/... -run 'TestHandleCloseIssue|TestHandleUpdateIssue|TestHandleAddLabels|TestHandleCommentIssue' -v`
Expected: PASS (all new tests).

- [ ] **Step 5: Run the full mcp package suite**

Run: `go test -race ./internal/mcp/...`
Expected: PASS.

- [ ] **Step 6: Gate and commit**

Run: `gofmt -l internal/mcp/ && go vet ./internal/mcp/... && golangci-lint run ./internal/mcp/...`

```bash
git add internal/mcp/tools_write.go internal/mcp/tools_write_test.go internal/mcp/server.go
git commit -m "feat(mcp): add close_issue, update_issue, add_labels, comment_issue tools

Refs #211"
```

---

### Task 6: Wire `--allow-destructive` + audit log path in `cmd/bridge/mcp.go`; final verification

**Files:**
- Modify: `cmd/bridge/mcp.go`

**Interfaces:**
- Consumes: `audit.Open` (Task 1); `imcp.Deps.AllowDestructive`, `imcp.Deps.Audit` (Task 4).
- Produces: `--allow-destructive` CLI flag, `BRIDGE_MCP_ALLOW_DESTRUCTIVE` env var, `BRIDGE_AUDIT_LOG_PATH` env var (with `$XDG_STATE_HOME/bridge/audit.jsonl` / `~/.local/state/bridge/audit.jsonl` fallback).

This task has no new unit test of its own — `auditLogPath`'s precedence mirrors the already-tested-by-pattern `mcpStateDir`, and `runMCPServe` is an integration entry point exercised by the manual smoke check in Step 3. Its correctness is verified by the full-suite gate in Step 4.

- [ ] **Step 1: Add the flag and audit-path resolver**

In `cmd/bridge/mcp.go`, add `mcpAllowDestructive` to the `var` block and register the flag:

```go
var (
	mcpPort             int
	mcpHost             string
	mcpReadOnly         bool
	mcpAllowDestructive bool
	mcpNoAuth           bool
	mcpAuthMode         string
)
```

```go
	serveCmd.Flags().BoolVar(&mcpReadOnly, "read-only", false, "disable write tools (create_issue is not registered)")
	serveCmd.Flags().BoolVar(&mcpAllowDestructive, "allow-destructive", false, "allow destructive tools to execute when confirmed (reserved for future archive_repo/delete_repo; tier-1 tools are unaffected)")
	serveCmd.Flags().BoolVar(&mcpNoAuth, "no-auth", false, "skip bearer check (localhost dev only)")
```

Add the resolver after `mcpStateDir`:

```go
// auditLogPath resolves the audit log path: BRIDGE_AUDIT_LOG_PATH wins,
// then $XDG_STATE_HOME/bridge/audit.jsonl, else ~/.local/state/bridge/audit.jsonl.
func auditLogPath() (string, error) {
	if p := os.Getenv("BRIDGE_AUDIT_LOG_PATH"); p != "" {
		return p, nil
	}
	if xdg := os.Getenv("XDG_STATE_HOME"); xdg != "" {
		return filepath.Join(xdg, "bridge", "audit.jsonl"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory for default audit log path: %w", err)
	}
	return filepath.Join(home, ".local", "state", "bridge", "audit.jsonl"), nil
}
```

- [ ] **Step 2: Wire the audit logger and `AllowDestructive` into `runMCPServe`**

Add the import:

```go
	"github.com/freaxnx01/bridge/internal/audit"
```

Replace the body of `runMCPServe` from `roots := reposRoots()` through the `var (handler ...)` block with:

```go
	roots := reposRoots()

	logPath, err := auditLogPath()
	if err != nil {
		return err
	}
	auditLogger, err := audit.Open(logPath)
	if err != nil {
		return fmt.Errorf("open audit log: %w", err)
	}

	deps := imcp.Deps{
		ReadOnly:         mcpReadOnly || os.Getenv("BRIDGE_MCP_READONLY") == "1",
		AllowDestructive: mcpAllowDestructive || os.Getenv("BRIDGE_MCP_ALLOW_DESTRUCTIVE") == "1",
		DefaultOwners:    parseOwners(os.Getenv("BRIDGE_MCP_OWNERS")),
		ClientFor:        newCachingClientResolver(clientForMCP(roots)),
		BuildOverview:    buildOverviewSnapshot,
		Audit:            auditLogger,
	}

	srv := imcp.NewServer(deps)

	var (
		handler http.Handler
		cleanup = func() error { return nil }
	)
```

(`err` is now declared once via `:=` above and reused with `=` in the `switch mcpAuthMode` block that follows unchanged — do not re-declare it there.)

Update the startup log line:

```go
	slog.Info("Bridge MCP", "addr", "http://"+addr, "read_only", deps.ReadOnly, "allow_destructive", deps.AllowDestructive, "auth", !mcpNoAuth, "auth_mode", mcpAuthMode)
```

- [ ] **Step 3: Build and smoke-check**

Run: `go build ./...`
Expected: builds cleanly.

Run: `go run ./cmd/bridge mcp serve --no-auth --allow-destructive &` then `curl -s http://127.0.0.1:7788/ -o /dev/null -w '%{http_code}\n'` (any HTTP response, even 4xx/406 from the MCP transport, proves the server started with the new flag parsed); then stop the background process. Confirm no startup error about the audit log path (check `~/.local/state/bridge/audit.jsonl` or `$XDG_STATE_HOME/bridge/audit.jsonl` was created).

- [ ] **Step 4: Full-suite gate (covers all six tasks, including the pre-existing `updated_at` regression tests)**

Run, in order, from the repo root:

```bash
gofmt -l .
go vet ./...
golangci-lint run
go test -race ./...
just test
```

Expected: `gofmt -l .` empty; `go vet`, `golangci-lint run` clean; `go test -race ./...` PASS including `TestGithubCreateIssue`, `TestGithubCreateRepo`, `TestForgejoCreateIssue`, `TestForgejoCreateRepo` (the pre-existing `updated_at` regression tests, confirmed still green); `just test` PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/bridge/mcp.go
git commit -m "feat(mcp): wire --allow-destructive flag and audit log path

Refs #211"
```

---

## Self-Review Notes

- **Spec coverage:** Step 0 finding (no `delete_issue`) — reflected in Scope/Global Constraints, nothing implements it. Guardrails (`AllowDestructive`, per-call audit entries, `list_git_forges` reporting) — Task 4/5/6. Audit log (`Entry`, `Logger`, `Open`, path resolution) — Task 1 + Task 6. All four tier-1 tool contracts — Task 2/3 (forge layer) + Task 5 (MCP layer). Bug-fix regression test — confirmed pre-existing and gated in Task 6 Step 4; reconciled in "Spec Reconciliation." Tier 2/3/4 — explicitly out of scope except the `repoArchiver`/`repoDeleter` stub interfaces (Task 4).
- **Placeholder scan:** none — every step above has literal code or literal shell commands.
- **Type consistency:** `issueCloser.CloseIssue`/`issueUpdater.UpdateIssue`/`labelAdder.AddLabels`/`issueCommenter.CommentIssue` signatures in Task 4 match `GithubClient`/`ForgejoClient` method signatures in Task 2/3 and the handler call sites in Task 5 exactly (verified: `ctx, owner, repo string, number int, ...`). `forge.Comment`/`forge.Issue.State` (Task 2) are consumed unchanged by Task 3, 4, 5. `audit.Entry`/`audit.Logger`/`audit.Open` (Task 1) match `Deps.auditLog` (Task 4) and every handler's `d.auditLog(audit.Entry{...})` call (Task 5).
