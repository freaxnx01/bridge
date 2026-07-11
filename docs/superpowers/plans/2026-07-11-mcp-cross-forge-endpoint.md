# Bridge cross-forge MCP endpoint Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship a self-hosted, remote (Streamable HTTP) MCP endpoint — `bridge mcp serve` — exposing four cross-forge tools (`list_repos`, `read_file`, `create_issue`, `cross_forge_status`) over GitHub + Forgejo, guarded by a static bearer token.

**Architecture:** A new `internal/mcp` package builds an `*mcp.Server` from an injected `Deps` struct (a `forgeClient` resolver, default owners, and an overview builder) and registers the tools — with the write tool omitted by construction in read-only mode. A new `cmd/bridge/mcp.go` subcommand resolves per-forge tokens via `internal/remote`, wires the server into `mcp.NewStreamableHTTPHandler`, wraps it in `auth.RequireBearerToken`, and runs an `http.Server` with timeouts + graceful shutdown, mirroring `cmd/bridge/serve.go`.

**Tech Stack:** Go 1.25, Cobra, the official MCP Go SDK `github.com/modelcontextprotocol/go-sdk@v1.2.0` (`mcp` + `auth` packages), `golang.org/x/sync/errgroup`, `crypto/subtle`, stdlib `net/http` + `net/http/httptest`, stdlib `testing`.

## Global Constraints

- **Go toolchain:** `go 1.25.0` — do NOT change the `go` line in `go.mod`.
- **One new dependency only:** `github.com/modelcontextprotocol/go-sdk@v1.2.0` (approved in the spec). Run `go mod tidy` after adding; review the `go.sum` diff. Add nothing else.
- **Testing:** stdlib `testing`, table-driven, hand-rolled fakes. NO `testify`/`mockery`/`gomock`.
- **Interfaces at the consumer:** `internal/mcp` defines its own small `forgeClient` interface; `*forge.GithubClient` and `*forge.ForgejoClient` satisfy it structurally.
- **Errors:** return errors (never `panic`/`os.Exit` below `main`); wrap with `%w`, lower-case message, no trailing punctuation. `context.Context` is the first parameter of every I/O function.
- **No package-level mutable globals** in `internal/mcp`; dependencies pass through `Deps`. (The Cobra flag vars in `cmd/bridge/mcp.go` follow the existing `serve.go` pattern.)
- **Write safety by construction:** in read-only mode `create_issue` is never registered. `Confirm` is tool-input *data*, not a Go flag argument.
- **Auth:** constant-time bearer compare via `crypto/subtle.ConstantTimeCompare`. Startup fails fast if `BRIDGE_MCP_TOKEN` is unset unless `--no-auth`.
- **Gates after every task:** `gofmt -l .` empty, `go vet ./...` clean, `golangci-lint run` clean, `go test -race ./...` green, `govulncheck ./...` clean (run govulncheck at least once after the dependency is added).

### Scope decision (assumption stated explicitly)

The spec's tool table lists an optional `ref?` on `read_file`, but the spec's `forgeClient` interface defines `GetFile(ctx, owner, repo, path)` with **no** `ref` — matching the existing `GithubClient.GetFile`. Honoring `ref` would mean widening an existing, in-use signature (scope creep against the surgical-edits guardrail). **Decision for this slice: `read_file` takes no `ref`; content is read from the default branch.** Ref-pinning is deferred. This is noted again in Task 1 and Task 3.

---

### Task 1: Forgejo `GetFile`

Add a `GetFile` method to `ForgejoClient` mirroring `GithubClient.GetFile`, so both clients satisfy the forthcoming `internal/mcp` `forgeClient` interface. Forgejo/Gitea's Contents API (`GET /api/v1/repos/{owner}/{repo}/contents/{path}`) returns `{content: <base64>, encoding, sha}` and 404 when absent — the same shape `GithubClient.GetFile` already handles.

**Files:**
- Modify: `internal/forge/forgejo.go` (add method after `ListRepos`, before `ListOpenIssues`)
- Test: `internal/forge/forgejo_test.go` (add one test)

**Interfaces:**
- Consumes: the existing unexported `ForgejoClient.get` is NOT reused here (it decodes JSON into `out` and can't expose the 404-as-not-found distinction); this method does its own request like `GithubClient.GetFile`.
- Produces: `func (c *ForgejoClient) GetFile(ctx context.Context, owner, repo, path string) (content []byte, sha string, found bool, err error)` — identical signature to `GithubClient.GetFile`.

- [ ] **Step 1: Write the failing test**

Add to `internal/forge/forgejo_test.go`:

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/forge -run TestForgejoGetFile_FoundAndAbsent -v`
Expected: FAIL — `c.GetFile undefined (type *ForgejoClient has no field or method GetFile)`.

- [ ] **Step 3: Write minimal implementation**

Add these imports to `internal/forge/forgejo.go` (it already imports `bytes`, `context`, `encoding/json`, `fmt`, `io`, `net/http`, `time`): add `"encoding/base64"` and `"strings"`.

Add the method after `ListRepos` (around line 147):

```go
// GetFile fetches a file's decoded content and blob sha via the Forgejo/Gitea
// Contents API. found is false (with nil error) when the file does not exist
// (404). Content is read from the repository's default branch.
func (c *ForgejoClient) GetFile(ctx context.Context, owner, repo, path string) (content []byte, sha string, found bool, err error) {
	url := fmt.Sprintf("%s/api/v1/repos/%s/%s/contents/%s", c.baseURL, owner, repo, path)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, "", false, err
	}
	if c.token != "" {
		req.Header.Set("Authorization", "token "+c.token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, "", false, err
	}
	defer func() { _ = resp.Body.Close() }() // best-effort close
	if resp.StatusCode == http.StatusNotFound {
		return nil, "", false, nil
	}
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		return nil, "", false, fmt.Errorf("forgejo get %s: %s: %s", path, resp.Status, string(b))
	}
	var fc struct {
		Content string `json:"content"`
		SHA     string `json:"sha"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&fc); err != nil {
		return nil, "", false, err
	}
	raw, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(fc.Content, "\n", ""))
	if err != nil {
		return nil, "", false, fmt.Errorf("decode %s: %w", path, err)
	}
	return raw, fc.SHA, true, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/forge -run TestForgejoGetFile_FoundAndAbsent -v`
Expected: PASS.

- [ ] **Step 5: Format, vet, full package suite**

Run: `gofmt -w internal/forge/forgejo.go && go vet ./internal/forge && go test -race ./internal/forge`
Expected: no gofmt output, vet clean, all forge tests PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/forge/forgejo.go internal/forge/forgejo_test.go
git commit -m "feat(forge): add Forgejo GetFile mirroring GitHub contents API"
```

---

### Task 2: Add the MCP SDK dependency + static bearer verifier

Introduce the one approved dependency and the smallest unit that imports it: a static-token `auth.TokenVerifier`. The verifier constant-time-compares the presented bearer against a configured token and returns a non-expiring `*auth.TokenInfo` on success, `auth.ErrInvalidToken` otherwise.

**Files:**
- Modify: `go.mod`, `go.sum` (via `go get` + `go mod tidy`)
- Create: `internal/mcp/auth.go`
- Test: `internal/mcp/auth_test.go`

**Interfaces:**
- Consumes: `github.com/modelcontextprotocol/go-sdk/auth` — `auth.TokenVerifier`, `auth.TokenInfo`, `auth.ErrInvalidToken`.
- Produces: `func StaticBearerVerifier(want string) auth.TokenVerifier` (a `func(ctx context.Context, token string) (*auth.TokenInfo, error)`) — consumed by Task 5's HTTP wiring.

- [ ] **Step 1: Add the dependency**

Run:
```bash
go get github.com/modelcontextprotocol/go-sdk@v1.2.0
go mod tidy
```
Expected: `go.mod` gains `github.com/modelcontextprotocol/go-sdk v1.2.0` in the `require` block; `go.sum` updated. Review the `go.sum` diff for unexpected modules.

- [ ] **Step 2: Confirm the `auth.TokenInfo` field names**

The `RequireBearerToken` middleware checks token expiration, so the verifier must return a `TokenInfo` with a future expiration. Confirm the exact field name before writing code:

Run: `go doc github.com/modelcontextprotocol/go-sdk/auth.TokenInfo`
Expected: a struct with an expiration time field (named `Expiration`) and a `Scopes []string` field. **If the field name differs from `Expiration`, use the name reported by `go doc`** in Step 4 and update the code accordingly.

- [ ] **Step 3: Write the failing test**

Create `internal/mcp/auth_test.go`:

```go
package mcp

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
)

func TestStaticBearerVerifier_ValidInvalidMissing(t *testing.T) {
	verify := StaticBearerVerifier("s3cret")
	tests := []struct {
		name    string
		token   string
		wantErr bool
	}{
		{"valid", "s3cret", false},
		{"wrong", "nope", true},
		{"empty", "", true},
		{"prefix-of-valid", "s3cre", true},
		{"superset-of-valid", "s3cretx", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info, err := verify(context.Background(), tt.token)
			if tt.wantErr {
				if !errors.Is(err, auth.ErrInvalidToken) {
					t.Fatalf("token %q: want ErrInvalidToken, got info=%v err=%v", tt.token, info, err)
				}
				return
			}
			if err != nil || info == nil {
				t.Fatalf("token %q: want info, got info=%v err=%v", tt.token, info, err)
			}
			if !info.Expiration.After(time.Now()) {
				t.Errorf("token %q: expiration %v is not in the future", tt.token, info.Expiration)
			}
		})
	}
}
```

- [ ] **Step 4: Run test to verify it fails**

Run: `go test ./internal/mcp -run TestStaticBearerVerifier -v`
Expected: FAIL — `undefined: StaticBearerVerifier`.

- [ ] **Step 5: Write minimal implementation**

Create `internal/mcp/auth.go`:

```go
// Package mcp constructs the Bridge cross-forge MCP server: it registers the
// forge tools on an *mcp.Server built from injected dependencies, and provides
// the static-bearer verifier used to guard the HTTP transport. Transport and
// process concerns live in cmd/bridge.
package mcp

import (
	"context"
	"crypto/subtle"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
)

// StaticBearerVerifier returns an auth.TokenVerifier that accepts exactly one
// bearer token (want), compared in constant time. On a match it returns a
// TokenInfo with a far-future expiration (the RequireBearerToken middleware
// rejects expired tokens); otherwise it returns auth.ErrInvalidToken.
func StaticBearerVerifier(want string) auth.TokenVerifier {
	return func(_ context.Context, token string) (*auth.TokenInfo, error) {
		if subtle.ConstantTimeCompare([]byte(token), []byte(want)) != 1 {
			return nil, auth.ErrInvalidToken
		}
		return &auth.TokenInfo{
			Expiration: time.Now().Add(10 * 365 * 24 * time.Hour),
		}, nil
	}
}
```

(If Step 2 reported a different expiration field name, substitute it here.)

- [ ] **Step 6: Run test to verify it passes**

Run: `go test ./internal/mcp -run TestStaticBearerVerifier -v`
Expected: PASS.

- [ ] **Step 7: Vuln scan + gates**

Run: `gofmt -l internal/mcp && go vet ./internal/mcp && govulncheck ./... && go test -race ./internal/mcp`
Expected: no gofmt output, vet clean, govulncheck reports no vulnerabilities, tests PASS.

- [ ] **Step 8: Commit**

```bash
git add go.mod go.sum internal/mcp/auth.go internal/mcp/auth_test.go
git commit -m "feat(mcp): add MCP Go SDK and static bearer verifier"
```

---

### Task 3: `forgeClient` interface, tool types, and handlers

Define the consumer-side `forgeClient` interface, the typed input/output structs for all four tools, the injected `Deps`, and the handler methods. Handlers are plain typed functions driven directly in tests via a hand-rolled fake — no MCP server or transport needed yet.

**Files:**
- Create: `internal/mcp/tools.go`
- Test: `internal/mcp/tools_test.go`

**Interfaces:**
- Consumes: `internal/forge` (`forge.RepoRef`, `forge.Issue`), `internal/overview` (`overview.Snapshot`), `golang.org/x/sync/errgroup`.
- Produces (consumed by Task 4's `NewServer` and Task 5's wiring):
  - `type Target struct { Forge, Owner string }`
  - `type forgeClient interface { Name() string; ListRepos(ctx, owner) ([]forge.RepoRef, error); GetFile(ctx, owner, repo, path) ([]byte, string, bool, error); CreateIssue(ctx, owner, repo, title, body) (forge.Issue, error) }`
  - `type Deps struct { ReadOnly bool; DefaultOwners []Target; ClientFor func(forge, owner string) forgeClient; BuildOverview func(ctx context.Context) (overview.Snapshot, error) }`
  - Input/output structs: `listReposInput/Output`, `readFileInput/Output`, `createIssueInput/Output`, `crossForgeStatusInput`.
  - Handler methods on `Deps`: `handleListRepos`, `handleReadFile`, `handleCreateIssue`, `handleCrossForgeStatus`, each with signature `func(ctx context.Context, req *mcp.CallToolRequest, in In) (*mcp.CallToolResult, Out, error)`.

- [ ] **Step 1: Write the failing tests**

Create `internal/mcp/tools_test.go`:

```go
package mcp

import (
	"context"
	"errors"
	"testing"

	"github.com/freaxnx01/bridge/internal/forge"
	"github.com/freaxnx01/bridge/internal/overview"
)

// fakeForge is a hand-rolled forgeClient. Each field is the canned result for
// the corresponding method; nil funcs mean "not expected".
type fakeForge struct {
	name         string
	repos        []forge.RepoRef
	file         []byte
	sha          string
	found        bool
	created      forge.Issue
	createCalled *int
}

func (f *fakeForge) Name() string { return f.name }
func (f *fakeForge) ListRepos(_ context.Context, _ string) ([]forge.RepoRef, error) {
	return f.repos, nil
}
func (f *fakeForge) GetFile(_ context.Context, _, _, _ string) ([]byte, string, bool, error) {
	return f.file, f.sha, f.found, nil
}
func (f *fakeForge) CreateIssue(_ context.Context, owner, repo, title, _ string) (forge.Issue, error) {
	if f.createCalled != nil {
		*f.createCalled++
	}
	return forge.Issue{Forge: f.name, Repo: owner + "/" + repo, Number: 42, Title: title}, nil
}

func depsWith(clients map[string]*fakeForge, owners []Target) Deps {
	return Deps{
		DefaultOwners: owners,
		ClientFor: func(forgeName, _ string) forgeClient {
			c, ok := clients[forgeName]
			if !ok {
				return nil
			}
			return c
		},
	}
}

func TestHandleListRepos_AggregatesDefaultOwners(t *testing.T) {
	clients := map[string]*fakeForge{
		"github":  {name: "github", repos: []forge.RepoRef{{Forge: "github", Owner: "freaxnx01", Name: "bridge"}}},
		"forgejo": {name: "forgejo", repos: []forge.RepoRef{{Forge: "forgejo", Owner: "freax", Name: "notes"}}},
	}
	d := depsWith(clients, []Target{{"github", "freaxnx01"}, {"forgejo", "freax"}})
	_, out, err := d.handleListRepos(context.Background(), nil, listReposInput{})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Repos) != 2 {
		t.Fatalf("want 2 repos, got %d: %+v", len(out.Repos), out.Repos)
	}
}

func TestHandleListRepos_ForgeFilterHonoured(t *testing.T) {
	clients := map[string]*fakeForge{
		"github":  {name: "github", repos: []forge.RepoRef{{Forge: "github", Name: "bridge"}}},
		"forgejo": {name: "forgejo", repos: []forge.RepoRef{{Forge: "forgejo", Name: "notes"}}},
	}
	d := depsWith(clients, []Target{{"github", "freaxnx01"}, {"forgejo", "freax"}})
	_, out, err := d.handleListRepos(context.Background(), nil, listReposInput{Forge: "forgejo"})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Repos) != 1 || out.Repos[0].Forge != "forgejo" {
		t.Fatalf("forge filter not honoured: %+v", out.Repos)
	}
}

func TestHandleListRepos_OwnerInputOverridesDefaults(t *testing.T) {
	clients := map[string]*fakeForge{
		"github": {name: "github", repos: []forge.RepoRef{{Forge: "github", Owner: "acme", Name: "widget"}}},
	}
	// Default owners intentionally exclude "acme"; explicit input must still query it.
	d := depsWith(clients, []Target{{"forgejo", "freax"}})
	_, out, err := d.handleListRepos(context.Background(), nil, listReposInput{Forge: "github", Owner: "acme"})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Repos) != 1 || out.Repos[0].Owner != "acme" {
		t.Fatalf("owner override not honoured: %+v", out.Repos)
	}
}

func TestHandleReadFile_FoundAndAbsent(t *testing.T) {
	clients := map[string]*fakeForge{
		"github": {name: "github", file: []byte("hello"), sha: "abc", found: true},
	}
	d := depsWith(clients, nil)

	_, out, err := d.handleReadFile(context.Background(), nil, readFileInput{Forge: "github", Owner: "o", Repo: "r", Path: "f.md"})
	if err != nil {
		t.Fatal(err)
	}
	if !out.Found || out.Content != "hello" || out.SHA != "abc" {
		t.Fatalf("found file: %+v", out)
	}

	clients["github"].found = false
	clients["github"].file = nil
	_, out, err = d.handleReadFile(context.Background(), nil, readFileInput{Forge: "github", Owner: "o", Repo: "r", Path: "missing.md"})
	if err != nil {
		t.Fatal(err)
	}
	if out.Found {
		t.Fatalf("absent file should have Found=false: %+v", out)
	}
}

func TestHandleReadFile_UnknownForge(t *testing.T) {
	d := depsWith(map[string]*fakeForge{}, nil)
	_, _, err := d.handleReadFile(context.Background(), nil, readFileInput{Forge: "bogus", Owner: "o", Repo: "r", Path: "f"})
	if err == nil {
		t.Fatal("want error for unknown forge, got nil")
	}
}

func TestHandleCreateIssue_DraftDoesNotCreate(t *testing.T) {
	calls := 0
	clients := map[string]*fakeForge{"github": {name: "github", createCalled: &calls}}
	d := depsWith(clients, nil)
	_, out, err := d.handleCreateIssue(context.Background(), nil, createIssueInput{
		Forge: "github", Owner: "o", Repo: "r", Title: "t", Body: "b", Confirm: false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !out.Draft {
		t.Fatalf("Confirm=false must return a draft: %+v", out)
	}
	if out.Issue != nil {
		t.Fatalf("draft must carry no created issue: %+v", out.Issue)
	}
	if calls != 0 {
		t.Fatalf("draft must not call CreateIssue, got %d calls", calls)
	}
	if out.Title != "t" || out.Forge != "github" {
		t.Fatalf("draft must echo resolved fields: %+v", out)
	}
}

func TestHandleCreateIssue_ConfirmCreates(t *testing.T) {
	calls := 0
	clients := map[string]*fakeForge{"github": {name: "github", createCalled: &calls}}
	d := depsWith(clients, nil)
	_, out, err := d.handleCreateIssue(context.Background(), nil, createIssueInput{
		Forge: "github", Owner: "o", Repo: "r", Title: "t", Body: "b", Confirm: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Draft {
		t.Fatalf("Confirm=true must not be a draft: %+v", out)
	}
	if out.Issue == nil || out.Issue.Number != 42 {
		t.Fatalf("Confirm=true must return created issue: %+v", out.Issue)
	}
	if calls != 1 {
		t.Fatalf("want exactly 1 CreateIssue call, got %d", calls)
	}
}

func TestHandleCrossForgeStatus_DelegatesToBuild(t *testing.T) {
	want := overview.Snapshot{RoadmapErr: "sentinel"}
	d := Deps{BuildOverview: func(_ context.Context) (overview.Snapshot, error) { return want, nil }}
	_, out, err := d.handleCrossForgeStatus(context.Background(), nil, crossForgeStatusInput{})
	if err != nil {
		t.Fatal(err)
	}
	if out.RoadmapErr != "sentinel" {
		t.Fatalf("cross_forge_status did not delegate to BuildOverview: %+v", out)
	}
}

func TestHandleCrossForgeStatus_PropagatesError(t *testing.T) {
	sentinel := errors.New("boom")
	d := Deps{BuildOverview: func(_ context.Context) (overview.Snapshot, error) { return overview.Snapshot{}, sentinel }}
	_, _, err := d.handleCrossForgeStatus(context.Background(), nil, crossForgeStatusInput{})
	if !errors.Is(err, sentinel) {
		t.Fatalf("want sentinel error, got %v", err)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/mcp -run 'TestHandle' -v`
Expected: FAIL — `undefined: Deps`, `undefined: listReposInput`, etc.

- [ ] **Step 3: Write minimal implementation**

Create `internal/mcp/tools.go`:

```go
package mcp

import (
	"context"
	"fmt"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/sync/errgroup"

	"github.com/freaxnx01/bridge/internal/forge"
	"github.com/freaxnx01/bridge/internal/overview"
)

// Target is a (forge, owner) pair queried by list_repos when no owner is given
// in the tool input.
type Target struct {
	Forge string
	Owner string
}

// forgeClient is the consumer-side interface the MCP tools need. Both
// *forge.GithubClient and *forge.ForgejoClient satisfy it structurally.
type forgeClient interface {
	Name() string
	ListRepos(ctx context.Context, owner string) ([]forge.RepoRef, error)
	GetFile(ctx context.Context, owner, repo, path string) (content []byte, sha string, found bool, err error)
	CreateIssue(ctx context.Context, owner, repo, title, body string) (forge.Issue, error)
}

// Deps are the injected dependencies of the MCP server. ClientFor returns a
// ready per-(forge, owner) client (token baked in) or nil when that forge is
// unconfigured. BuildOverview produces the cross-forge status snapshot.
type Deps struct {
	ReadOnly      bool
	DefaultOwners []Target
	ClientFor     func(forgeName, owner string) forgeClient
	BuildOverview func(ctx context.Context) (overview.Snapshot, error)
}

type listReposInput struct {
	Forge string `json:"forge,omitempty" jsonschema:"optional forge filter: github or forgejo"`
	Owner string `json:"owner,omitempty" jsonschema:"optional owner filter; overrides the configured default owners"`
}

type listReposOutput struct {
	Repos []forge.RepoRef `json:"repos"`
}

type readFileInput struct {
	Forge string `json:"forge" jsonschema:"forge hosting the repo: github or forgejo"`
	Owner string `json:"owner" jsonschema:"repository owner"`
	Repo  string `json:"repo" jsonschema:"repository name"`
	Path  string `json:"path" jsonschema:"file path within the repo (default branch)"`
}

type readFileOutput struct {
	Content string `json:"content"`
	SHA     string `json:"sha"`
	Found   bool   `json:"found"`
}

type createIssueInput struct {
	Forge   string `json:"forge" jsonschema:"forge hosting the repo: github or forgejo"`
	Owner   string `json:"owner" jsonschema:"repository owner"`
	Repo    string `json:"repo" jsonschema:"repository name"`
	Title   string `json:"title" jsonschema:"issue title"`
	Body    string `json:"body,omitempty" jsonschema:"issue body (markdown)"`
	Confirm bool   `json:"confirm,omitempty" jsonschema:"when false, returns a draft without creating; set true to create"`
}

type createIssueOutput struct {
	Draft bool         `json:"draft"`
	Forge string       `json:"forge"`
	Owner string       `json:"owner"`
	Repo  string       `json:"repo"`
	Title string       `json:"title"`
	Body  string       `json:"body,omitempty"`
	Issue *forge.Issue `json:"issue,omitempty"`
}

type crossForgeStatusInput struct{}

// targets returns the (forge, owner) pairs list_repos should query for the
// given input: an explicit owner (with optional forge) overrides the defaults;
// otherwise the configured defaults are used, narrowed by an optional forge.
func (d Deps) targets(in listReposInput) []Target {
	if in.Owner != "" {
		forgeName := in.Forge
		if forgeName == "" {
			forgeName = "github"
		}
		return []Target{{Forge: forgeName, Owner: in.Owner}}
	}
	var out []Target
	for _, t := range d.DefaultOwners {
		if in.Forge != "" && t.Forge != in.Forge {
			continue
		}
		out = append(out, t)
	}
	return out
}

func (d Deps) handleListRepos(ctx context.Context, _ *mcp.CallToolRequest, in listReposInput) (*mcp.CallToolResult, listReposOutput, error) {
	targets := d.targets(in)
	var (
		mu  sync.Mutex
		all []forge.RepoRef
	)
	g, gctx := errgroup.WithContext(ctx)
	for _, t := range targets {
		t := t
		client := d.ClientFor(t.Forge, t.Owner)
		if client == nil {
			continue
		}
		g.Go(func() error {
			repos, err := client.ListRepos(gctx, t.Owner)
			if err != nil {
				return fmt.Errorf("list repos %s/%s: %w", t.Forge, t.Owner, err)
			}
			mu.Lock()
			all = append(all, repos...)
			mu.Unlock()
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, listReposOutput{}, err
	}
	return nil, listReposOutput{Repos: all}, nil
}

func (d Deps) handleReadFile(ctx context.Context, _ *mcp.CallToolRequest, in readFileInput) (*mcp.CallToolResult, readFileOutput, error) {
	client := d.ClientFor(in.Forge, in.Owner)
	if client == nil {
		return nil, readFileOutput{}, fmt.Errorf("forge %q not configured", in.Forge)
	}
	content, sha, found, err := client.GetFile(ctx, in.Owner, in.Repo, in.Path)
	if err != nil {
		return nil, readFileOutput{}, fmt.Errorf("read %s/%s/%s: %w", in.Owner, in.Repo, in.Path, err)
	}
	return nil, readFileOutput{Content: string(content), SHA: sha, Found: found}, nil
}

func (d Deps) handleCreateIssue(ctx context.Context, _ *mcp.CallToolRequest, in createIssueInput) (*mcp.CallToolResult, createIssueOutput, error) {
	draft := createIssueOutput{
		Draft: true,
		Forge: in.Forge, Owner: in.Owner, Repo: in.Repo, Title: in.Title, Body: in.Body,
	}
	if !in.Confirm {
		return nil, draft, nil
	}
	client := d.ClientFor(in.Forge, in.Owner)
	if client == nil {
		return nil, createIssueOutput{}, fmt.Errorf("forge %q not configured", in.Forge)
	}
	issue, err := client.CreateIssue(ctx, in.Owner, in.Repo, in.Title, in.Body)
	if err != nil {
		return nil, createIssueOutput{}, fmt.Errorf("create issue %s/%s: %w", in.Owner, in.Repo, err)
	}
	return nil, createIssueOutput{
		Draft: false,
		Forge: in.Forge, Owner: in.Owner, Repo: in.Repo, Title: in.Title, Body: in.Body,
		Issue: &issue,
	}, nil
}

func (d Deps) handleCrossForgeStatus(ctx context.Context, _ *mcp.CallToolRequest, _ crossForgeStatusInput) (*mcp.CallToolResult, overview.Snapshot, error) {
	snap, err := d.BuildOverview(ctx)
	if err != nil {
		return nil, overview.Snapshot{}, fmt.Errorf("build cross-forge status: %w", err)
	}
	return nil, snap, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/mcp -run 'TestHandle' -v`
Expected: all PASS.

- [ ] **Step 5: Gates**

Run: `gofmt -l internal/mcp && go vet ./internal/mcp && go test -race ./internal/mcp`
Expected: no gofmt output, vet clean, tests PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/mcp/tools.go internal/mcp/tools_test.go
git commit -m "feat(mcp): forge tool handlers with draft/confirm write safety"
```

---

### Task 4: `NewServer` — registration + read-only gating

Build the `*mcp.Server`, register the tools via `mcp.AddTool`, and omit `create_issue` when `Deps.ReadOnly` is set. Verify gating by connecting an in-memory client and listing the advertised tools.

**Files:**
- Create: `internal/mcp/server.go`
- Test: `internal/mcp/server_test.go`

**Interfaces:**
- Consumes: `Deps` and the handler methods from Task 3; `mcp.NewServer`, `mcp.AddTool`, `mcp.Tool`, `mcp.Implementation`.
- Produces (consumed by Task 5): `func NewServer(deps Deps) *mcp.Server`.

- [ ] **Step 1: Write the failing test**

Create `internal/mcp/server_test.go`:

```go
package mcp

import (
	"context"
	"sort"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func advertisedTools(t *testing.T, deps Deps) []string {
	t.Helper()
	ctx := context.Background()
	srv := NewServer(deps)
	ct, st := mcp.NewInMemoryTransports()
	ss, err := srv.Connect(ctx, st, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	defer ss.Close()

	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "v0"}, nil)
	cs, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	defer cs.Close()

	res, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	var names []string
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
	}
	sort.Strings(names)
	return names
}

func TestNewServer_RegistersFourToolsByDefault(t *testing.T) {
	names := advertisedTools(t, Deps{})
	want := []string{"create_issue", "cross_forge_status", "list_repos", "read_file"}
	if len(names) != len(want) {
		t.Fatalf("want %v, got %v", want, names)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("want %v, got %v", want, names)
		}
	}
}

func TestNewServer_ReadOnlyOmitsCreateIssue(t *testing.T) {
	names := advertisedTools(t, Deps{ReadOnly: true})
	for _, n := range names {
		if n == "create_issue" {
			t.Fatalf("read-only server must not advertise create_issue: %v", names)
		}
	}
	if len(names) != 3 {
		t.Fatalf("want 3 tools in read-only mode, got %v", names)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/mcp -run TestNewServer -v`
Expected: FAIL — `undefined: NewServer`.

- [ ] **Step 3: Write minimal implementation**

Create `internal/mcp/server.go`:

```go
package mcp

import (
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// NewServer builds the Bridge MCP server with the four cross-forge tools
// registered. In read-only mode the write tool (create_issue) is not
// registered at all, so there is nothing to bypass.
func NewServer(deps Deps) *mcp.Server {
	srv := mcp.NewServer(&mcp.Implementation{Name: "bridge", Version: "v1"}, nil)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "list_repos",
		Description: "List repositories across the configured GitHub and Forgejo owners (live).",
	}, deps.handleListRepos)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "read_file",
		Description: "Read a file's contents and blob sha from a repo's default branch.",
	}, deps.handleReadFile)

	mcp.AddTool(srv, &mcp.Tool{
		Name:        "cross_forge_status",
		Description: "Return the cross-forge overview snapshot (ranked items, inbox, roadmap).",
	}, deps.handleCrossForgeStatus)

	if !deps.ReadOnly {
		mcp.AddTool(srv, &mcp.Tool{
			Name:        "create_issue",
			Description: "Create an issue. Without confirm=true this returns a draft and creates nothing.",
		}, deps.handleCreateIssue)
	}

	return srv
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/mcp -run TestNewServer -v`
Expected: both PASS.

- [ ] **Step 5: Gates (full package under race)**

Run: `gofmt -l internal/mcp && go vet ./internal/mcp && go test -race ./internal/mcp`
Expected: no gofmt output, vet clean, all `internal/mcp` tests PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/mcp/server.go internal/mcp/server_test.go
git commit -m "feat(mcp): NewServer with read-only write-tool gating"
```

---

### Task 5: `bridge mcp serve` subcommand + HTTP/auth wiring

Add the `bridge mcp serve` subcommand that resolves per-forge tokens via `internal/remote`, builds `Deps`, mounts the server behind `mcp.NewStreamableHTTPHandler` + `auth.RequireBearerToken`, and runs an `http.Server` with timeouts and graceful shutdown — mirroring `cmd/bridge/serve.go`. Two thin, pure helpers (`parseOwners`, `buildMCPHandler`) carry the testable logic; the integration test drives a real MCP client over HTTP with a bearer header.

**Files:**
- Create: `cmd/bridge/mcp.go`
- Test: `cmd/bridge/mcp_test.go`
- Modify: `CHANGELOG.md` (add an `Added` entry under `[Unreleased]`)

**Interfaces:**
- Consumes: `internal/mcp` (`NewServer`, `Deps`, `Target`, `StaticBearerVerifier`), `internal/remote` (`GitHubToken`, `ForgejoToken`), `internal/overview` (`Build`, `Config`), the existing cmd helpers (`reposRoots`, `overviewRepos`, `ideasLabDir`, `fetchAllOpenIssues`, `roadmapFetcher`), `mcp.NewStreamableHTTPHandler`, `auth.RequireBearerToken`.
- Produces: the `bridge mcp serve` command (no exported Go API).

- [ ] **Step 1: Write the failing tests**

Create `cmd/bridge/mcp_test.go`:

```go
package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	imcp "github.com/freaxnx01/bridge/internal/mcp"
)

func TestParseOwners(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []imcp.Target
	}{
		{"empty", "", nil},
		{"single", "github:freaxnx01", []imcp.Target{{Forge: "github", Owner: "freaxnx01"}}},
		{"comma-and-space", "github:freaxnx01, forgejo:freax", []imcp.Target{
			{Forge: "github", Owner: "freaxnx01"}, {Forge: "forgejo", Owner: "freax"},
		}},
		{"skips-malformed", "github:freaxnx01 bogus forgejo:freax", []imcp.Target{
			{Forge: "github", Owner: "freaxnx01"}, {Forge: "forgejo", Owner: "freax"},
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseOwners(tt.in)
			if len(got) != len(tt.want) {
				t.Fatalf("parseOwners(%q) = %+v, want %+v", tt.in, got, tt.want)
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Fatalf("parseOwners(%q) = %+v, want %+v", tt.in, got, tt.want)
				}
			}
		})
	}
}

func TestBuildMCPHandler_FailFastWithoutToken(t *testing.T) {
	srv := imcp.NewServer(imcp.Deps{ReadOnly: true})
	_, err := buildMCPHandler(srv, "", false)
	if err == nil {
		t.Fatal("want error when token empty and auth required, got nil")
	}
}

func TestBuildMCPHandler_NoAuthSkipsToken(t *testing.T) {
	srv := imcp.NewServer(imcp.Deps{ReadOnly: true})
	h, err := buildMCPHandler(srv, "", true)
	if err != nil || h == nil {
		t.Fatalf("no-auth must not require a token: h=%v err=%v", h, err)
	}
}

func TestBuildMCPHandler_RejectsMissingBearer(t *testing.T) {
	srv := imcp.NewServer(imcp.Deps{ReadOnly: true})
	h, err := buildMCPHandler(srv, "s3cret", false)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(h)
	defer ts.Close()

	resp, err := http.Post(ts.URL, "application/json", http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("missing bearer: want 401, got %d", resp.StatusCode)
	}
}

// bearerRoundTripper injects a static Authorization header on every request.
type bearerRoundTripper struct {
	token string
	base  http.RoundTripper
}

func (b bearerRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return b.base.RoundTrip(r)
}

func TestBuildMCPHandler_ValidBearerListsTools(t *testing.T) {
	srv := imcp.NewServer(imcp.Deps{ReadOnly: true})
	h, err := buildMCPHandler(srv, "s3cret", false)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(h)
	defer ts.Close()

	ctx := context.Background()
	transport := &sdkmcp.StreamableClientTransport{
		Endpoint:   ts.URL,
		HTTPClient: &http.Client{Transport: bearerRoundTripper{token: "s3cret", base: http.DefaultTransport}},
	}
	client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "test", Version: "v0"}, nil)
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		t.Fatalf("connect with bearer: %v", err)
	}
	defer session.Close()

	res, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if len(res.Tools) != 3 {
		t.Fatalf("read-only server: want 3 tools over HTTP, got %d", len(res.Tools))
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./cmd/bridge -run 'TestParseOwners|TestBuildMCPHandler' -v`
Expected: FAIL — `undefined: parseOwners`, `undefined: buildMCPHandler`.

- [ ] **Step 3: Write minimal implementation**

Create `cmd/bridge/mcp.go`:

```go
// cmd/bridge/mcp.go
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	sdkauth "github.com/modelcontextprotocol/go-sdk/auth"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/freaxnx01/bridge/internal/forge"
	imcp "github.com/freaxnx01/bridge/internal/mcp"
	"github.com/freaxnx01/bridge/internal/overview"
	"github.com/freaxnx01/bridge/internal/remote"
)

var (
	mcpPort     int
	mcpHost     string
	mcpReadOnly bool
	mcpNoAuth   bool
)

func init() {
	rootCmd.AddCommand(newMCPCmd())
}

func newMCPCmd() *cobra.Command {
	mcpCmd := &cobra.Command{
		Use:   "mcp",
		Short: "Bridge cross-forge MCP endpoint",
	}
	serveCmd := &cobra.Command{
		Use:   "serve",
		Short: "Start the Bridge MCP server (Streamable HTTP)",
		RunE:  runMCPServe,
	}
	serveCmd.Flags().IntVar(&mcpPort, "port", 7788, "port to listen on")
	serveCmd.Flags().StringVar(&mcpHost, "host", "127.0.0.1", "host to bind to")
	serveCmd.Flags().BoolVar(&mcpReadOnly, "read-only", false, "disable write tools (create_issue is not registered)")
	serveCmd.Flags().BoolVar(&mcpNoAuth, "no-auth", false, "skip bearer check (localhost dev only)")
	mcpCmd.AddCommand(serveCmd)
	return mcpCmd
}

// parseOwners parses a BRIDGE_MCP_OWNERS value: comma- and/or space-separated
// "forge:owner" entries. Malformed entries (no colon, empty parts) are skipped.
func parseOwners(s string) []imcp.Target {
	fields := strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' || r == '\n' })
	var out []imcp.Target
	for _, f := range fields {
		forgeName, owner, ok := strings.Cut(f, ":")
		if !ok || forgeName == "" || owner == "" {
			continue
		}
		out = append(out, imcp.Target{Forge: forgeName, Owner: owner})
	}
	return out
}

// buildMCPHandler mounts srv on a Streamable HTTP handler and, unless noAuth is
// set, wraps it in bearer-token middleware. It fails fast when a token is
// required but empty.
func buildMCPHandler(srv *sdkmcp.Server, token string, noAuth bool) (http.Handler, error) {
	streamable := sdkmcp.NewStreamableHTTPHandler(func(*http.Request) *sdkmcp.Server { return srv }, nil)
	if noAuth {
		return streamable, nil
	}
	if token == "" {
		return nil, fmt.Errorf("BRIDGE_MCP_TOKEN is required (or pass --no-auth for localhost dev)")
	}
	middleware := sdkauth.RequireBearerToken(imcp.StaticBearerVerifier(token), nil)
	return middleware(streamable), nil
}

// mcpClientFor returns a ready per-(forge, owner) forge client with its token
// resolved from the owner's direnv scope, mirroring the capture wiring in
// serve.go. It returns nil when the forge is unconfigured or the token is
// missing (that target is then skipped by list_repos / errors in read_file).
func mcpClientFor(forgeName, owner string) imcp.ForgeClientForTest {
	return nil // replaced below — see note
}

func runMCPServe(cmd *cobra.Command, _ []string) error {
	roots := reposRoots()

	deps := imcp.Deps{
		ReadOnly:      mcpReadOnly || os.Getenv("BRIDGE_MCP_READONLY") == "1",
		DefaultOwners: parseOwners(os.Getenv("BRIDGE_MCP_OWNERS")),
		ClientFor:     clientForMCP(roots),
		BuildOverview: buildOverviewSnapshot,
	}

	srv := imcp.NewServer(deps)
	handler, err := buildMCPHandler(srv, os.Getenv("BRIDGE_MCP_TOKEN"), mcpNoAuth)
	if err != nil {
		return err
	}

	addr := fmt.Sprintf("%s:%d", mcpHost, mcpPort)
	httpSrv := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      120 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	slog.Info("Bridge MCP", "addr", "http://"+addr, "read_only", deps.ReadOnly, "auth", !mcpNoAuth)

	ctx, cancel := context.WithCancel(cmd.Context())
	defer cancel()
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-quit
		cancel()
		shutCtx, shutCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutCancel()
		httpSrv.Shutdown(shutCtx) //nolint:errcheck // shutdown errors are not actionable at process exit
	}()

	if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

// clientForMCP builds the Deps.ClientFor resolver: per-owner GitHub tokens and
// the single Forgejo token come from their direnv scopes via internal/remote.
func clientForMCP(roots []string) func(forgeName, owner string) forge.Client {
	return func(forgeName, owner string) forge.Client {
		switch forgeName {
		case "github":
			tok, ok := remote.GitHubToken(roots, owner)
			if !ok {
				return nil
			}
			return forge.NewGithubClient(tok, os.Getenv("BRIDGE_GITHUB_API"))
		case "forgejo":
			tok, ok := remote.ForgejoToken(roots)
			if !ok {
				return nil
			}
			return forge.NewForgejoClient(tok, os.Getenv("BRIDGE_FORGEJO_API"))
		}
		return nil
	}
}

// buildOverviewSnapshot mirrors serve.go's overview handler wiring.
func buildOverviewSnapshot(ctx context.Context) (overview.Snapshot, error) {
	repos := overviewRepos()
	return overview.Build(ctx, overview.Config{
		Environment:  os.Getenv("BRIDGE_ENV"),
		Repos:        repos,
		IdeasLabDir:  ideasLabDir(),
		FetchIssues:  func(c context.Context) ([]overview.Issue, error) { return fetchAllOpenIssues(c, repos) },
		FetchRoadmap: roadmapFetcher(),
	})
}
```

**IMPORTANT — reconcile the `ClientFor` type before compiling.** `Deps.ClientFor` is typed `func(forgeName, owner string) forgeClient` where `forgeClient` is an *unexported* interface in `internal/mcp`. `clientForMCP` returns `forge.Client` values (`*forge.GithubClient` / `*forge.ForgejoClient`), which structurally satisfy `forgeClient` — but Go will not let `cmd/bridge` name the unexported type, and a `func(...) forge.Client` is not assignable to a `func(...) forgeClient` field. Resolve this by exporting the interface: in `internal/mcp/tools.go` rename `forgeClient` → **`ForgeClient`** (exported) and change the `Deps.ClientFor` field type to `func(forgeName, owner string) ForgeClient`. Then in `cmd/bridge/mcp.go` set `ClientFor` to a closure that adapts the concrete client to `imcp.ForgeClient`:

```go
ClientFor: func(forgeName, owner string) imcp.ForgeClient {
	c := clientForMCP(roots)(forgeName, owner)
	if c == nil {
		return nil // typed-nil pitfall: return untyped nil, not a nil *forge.GithubClient
	}
	// c is a forge.Client; both concrete forge clients also implement imcp.ForgeClient.
	fc, ok := c.(imcp.ForgeClient)
	if !ok {
		return nil
	}
	return fc
},
```

Delete the placeholder `mcpClientFor`/`ForgeClientForTest` stub shown above — it exists only to flag this reconciliation. Update Task 3's `forgeClient` references (interface name, `Deps.ClientFor` type, and the fake in `tools_test.go` return type) to the exported `ForgeClient` accordingly, and rerun `go test ./internal/mcp`.

- [ ] **Step 4: Apply the `ForgeClient` export**

In `internal/mcp/tools.go`: rename the interface `forgeClient` → `ForgeClient` (add a doc comment starting "ForgeClient"), and change `Deps.ClientFor` to `func(forgeName, owner string) ForgeClient`. In `internal/mcp/tools_test.go`: change `ClientFor`'s return type in `depsWith` to `ForgeClient`. Run `go test ./internal/mcp` — still green.

- [ ] **Step 5: Finalize `cmd/bridge/mcp.go`**

Remove the `mcpClientFor` placeholder and the `ForgeClientForTest` reference; wire `Deps.ClientFor` with the adapter closure from Step 3's note. Ensure imports are tidy (`goimports`).

- [ ] **Step 6: Run the cmd tests to verify they pass**

Run: `go test ./cmd/bridge -run 'TestParseOwners|TestBuildMCPHandler' -v`
Expected: all PASS (including the HTTP round-trip listing 3 tools with a valid bearer, and 401 without one).

- [ ] **Step 7: Full gates**

Run:
```bash
gofmt -l . && go vet ./... && golangci-lint run && go test -race ./... && govulncheck ./...
```
Expected: no gofmt output; vet clean; lint clean; the FULL suite passes under `-race`; govulncheck reports no vulnerabilities.

- [ ] **Step 8: Update the changelog**

Add under `## [Unreleased]` in `CHANGELOG.md` a new `### Added` section (above the existing `### Changed`):

```markdown
### Added

- **`bridge mcp serve`** — a self-hosted, remote (Streamable HTTP) MCP endpoint exposing four cross-forge tools (`list_repos`, `read_file`, `create_issue`, `cross_forge_status`) over GitHub + Forgejo, guarded by a static `BRIDGE_MCP_TOKEN` bearer. `--read-only` omits the write tool by construction; `create_issue` returns a draft unless called with `confirm: true`. (#195)
```

- [ ] **Step 9: Manually verify the binary (non-TTY smoke)**

Run:
```bash
go build ./cmd/bridge
BRIDGE_MCP_TOKEN=devtoken ./bridge mcp serve --read-only --port 7799 &
sleep 1
# missing bearer -> 401
curl -s -o /dev/null -w '%{http_code}\n' -X POST http://127.0.0.1:7799/
# valid bearer, initialize -> not 401 (200/202/SSE)
curl -s -o /dev/null -w '%{http_code}\n' -X POST http://127.0.0.1:7799/ \
  -H 'Authorization: Bearer devtoken' -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  --data '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"clientInfo":{"name":"c","version":"v0"},"protocolVersion":"2025-06-18","capabilities":{}}}'
kill %1
rm -f ./bridge
```
Expected: first curl prints `401`; second prints a non-401 status. Report the actual codes in the task summary.

- [ ] **Step 10: Commit**

```bash
git add cmd/bridge/mcp.go cmd/bridge/mcp_test.go internal/mcp/tools.go internal/mcp/tools_test.go CHANGELOG.md
git commit -m "feat(mcp): bridge mcp serve subcommand with bearer-guarded HTTP transport

Closes #195"
```

---

## Self-Review

**Spec coverage:**
- MCP protocol layer (official Go SDK v1.2.0) → Task 2 (dependency) + Task 4 (`mcp.NewServer`/`AddTool`).
- `list_repos` live `ListRepos` with concurrent fan-out (errgroup) → Task 3 `handleListRepos`.
- Forgejo `read_file` via new `GetFile` → Task 1 + Task 3 `handleReadFile`.
- `bridge mcp serve` subcommand with its own listener → Task 5.
- Static bearer (`BRIDGE_MCP_TOKEN`), fail-fast, constant-time compare → Task 2 verifier + Task 5 `buildMCPHandler`.
- Four tools with typed input/output → Task 3 structs + Task 4 registration.
- `cross_forge_status` reuses `overview.Build` → Task 3 `handleCrossForgeStatus` + Task 5 `buildOverviewSnapshot`.
- Write safety: read-only omits the write tool by construction → Task 4 gating; draft/confirm as input data → Task 3 `handleCreateIssue`.
- `BRIDGE_MCP_OWNERS` parsing → Task 5 `parseOwners`.
- Flags `--port 7788 / --host / --read-only / --no-auth`, env precedence → Task 5.
- Timeouts + SIGINT/SIGTERM graceful shutdown mirroring `serve.go` → Task 5.
- Testing: fake-driven tool tests, Forgejo `httptest`, bearer table test, `tools/list` integration (in-memory in Task 4, HTTP in Task 5) → covered.
- **Gap noted, not implemented (by decision):** `read_file` `ref?` is dropped this slice (see Scope decision). Traefik/Authentik edge reachability and the OAuth↔bearer reconciliation are explicitly out of scope in the spec.

**Placeholder scan:** The one intentional "placeholder-flag" is the `mcpClientFor`/`ForgeClientForTest` stub in Task 5 Step 3, which exists solely to make the exported-interface reconciliation impossible to miss; Steps 4–5 delete it and wire the real adapter. No other TBD/TODO/"add error handling" placeholders remain; every code step carries complete code.

**Type consistency:** `GetFile` signature is identical in Task 1 (Forgejo), the existing GitHub client, and the Task 3 interface. `Deps`, `Target`, and the tool input/output struct names are used consistently across Tasks 3–5. The interface is `forgeClient` in Tasks 3–4 and deliberately promoted to exported `ForgeClient` in Task 5 Steps 3–4 (with the corresponding test/field updates called out). `StaticBearerVerifier`, `NewServer`, `parseOwners`, `buildMCPHandler` names match across their definitions and call sites.

---

## Execution Handoff

Per the pickup handoff and the user's standing preference (`subagent-driven-default`), implementation will use **superpowers:subagent-driven-development** — a fresh subagent per task with two-stage review between tasks. That happens after this plan is committed, pushed, and issue #195 is updated.
