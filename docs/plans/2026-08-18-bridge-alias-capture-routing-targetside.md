# Bridge Alias Capture Routing — Target-Side Implementation Plan (bridge repo)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax.

**Goal:** Let the `bridge` REST API accept a repo **alias** (from a `.bridge.yaml` in each repo) as an alternative to `{owner,repo}`/`{target}` when creating an issue or an ideas.md entry, add an issue **body** field, and protect the `/api/capture/*` write endpoints with a `BRIDGE_API_TOKEN` bearer check.

**Architecture:** Alias is read from each repo's local `.bridge.yaml` during the existing filesystem `DiscoverRepos` walk and carried on `core.Repo` (so it flows into `GET /api/repos` for free). A shared `core.ResolveAlias` maps an alias → the one matching repo (forge/owner/name), with typed errors for unknown/ambiguous. The REST layer accepts `alias`/`body`, resolves alias in the `serve.go` capture closures, and maps resolver errors to HTTP status. A new bearer-auth middleware wraps only the capture handler.

**Tech Stack:** Go 1.25, stdlib `net/http`; YAML via `go.yaml.in/yaml/v3` (already transitively in `go.sum`). Tests: stdlib only (no testify), `net/http/httptest`, hand-written fakes.

**Counterpart:** The FlowHub side (`BridgeSkillIntegration`) is already merged (freaxnx01/flowhub#16). It sends `POST /api/capture/issue {alias,title,body}` / `idea {alias,text}` with a `Bearer` token and reads `url` from the response — this plan is the server it calls.

## Global Constraints

- Module `github.com/freaxnx01/bridge`, Go 1.25. `go build ./...`, `go test ./...`, and `go test -tags=e2e ./e2e/...` must pass.
- **`golangci-lint` v2.1.6 clean** and **`govulncheck ./...` clean** (both are CI checks; govulncheck is required).
- JSON struct tags are `snake_case` with `omitempty` on optional fields. Match existing style.
- Tests use the standard library only — `net/http/httptest`, `encoding/json`, hand-written fake structs implementing the consumer interface (see `internal/api/capture_test.go`, `internal/capture/capture_test.go`). No new test deps.
- Branch from `main`, PR back to `main` (bridge repo). Conventional Commits (`feat`/`fix`/`test`/…).
- Alias format: lowercased, matches `^[a-z0-9][a-z0-9-]*$`. Duplicate alias across repos must resolve to **none** (never silently pick one) → ambiguous error.
- Auth convention: when `BRIDGE_API_TOKEN` is **set**, enforce bearer on `/api/capture/*`; when **unset/empty**, auth is disabled (dev) — mirrors how empty tokens degrade elsewhere in the codebase. Read endpoints are never gated.
- Error contract for `/api/capture/*` (per design spec §D): `400` missing fields · `401` bad/absent bearer · `404` unknown alias · `409` ambiguous alias · `5xx` forge failure.

---

## File Structure

| Path | Change |
|---|---|
| `internal/core/repo.go` | +`Alias` field on `Repo`; set it in `DiscoverRepos` (4 append sites) |
| `internal/core/bridgeyaml.go` | **new** — `readBridgeAlias(repoPath string) string` (parse+validate `.bridge.yaml`) |
| `internal/core/alias.go` | **new** — `ResolveAlias`, `ErrAliasNotFound`, `ErrAliasAmbiguous` |
| `internal/capture/capture.go` | `CaptureIssue` gains a `body` param (drop hardcoded `""`) |
| `internal/api/capture.go` | `issueRequest`/`ideaRequest` +`alias`/`body`; relaxed validation; widened `Issue`/`Idea` func-field signatures; resolver-error → HTTP status |
| `cmd/bridge/serve.go` | capture closures resolve alias + pass body; new bearer middleware wraps `captureH` |
| `cmd/bridge/capture.go` | update `CaptureIssue` CLI call site for the new `body` param |
| Tests | `internal/core/bridgeyaml_test.go`, `internal/core/alias_test.go`, `internal/core/repo_test.go` (alias in discovery), `internal/capture/capture_test.go` (body), `internal/api/capture_test.go` (alias/body/status), `internal/api/repos_test.go` (alias in JSON), `cmd/bridge/serve_test.go` (middleware) |

### Dependency order

Task 1 (alias field + parse) → Task 2 (resolver) → Task 3 (issue body) → Task 4 (REST wiring: request structs + serve.go closures + error mapping) → Task 5 (bearer middleware). Each task compiles and tests independently.

---

## Task 1: `.bridge.yaml` → `core.Repo.Alias`

**Files:**
- Create: `internal/core/bridgeyaml.go`
- Modify: `internal/core/repo.go` (struct + 4 discovery append sites)
- Test: `internal/core/bridgeyaml_test.go`, `internal/core/repo_test.go`

**Interfaces:**
- Produces: `Repo.Alias string \`json:"alias,omitempty"\``.
- Produces: `func readBridgeAlias(repoPath string) string` (package `core`, unexported) — returns the validated lowercased alias, or `""` on missing file / read error / malformed YAML / pattern violation. Never panics, never errors out (discovery must be resilient).

- [ ] **Step 1: Write the failing test**

Create `internal/core/bridgeyaml_test.go`:

```go
package core

import (
	"os"
	"path/filepath"
	"testing"
)

func writeBridgeYAML(t *testing.T, dir, contents string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, ".bridge.yaml"), []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestReadBridgeAlias(t *testing.T) {
	cases := []struct {
		name     string
		contents string
		write    bool
		want     string
	}{
		{"valid", "alias: br\n", true, "br"},
		{"valid_with_comment", "# repo alias\nalias: agp\n", true, "agp"},
		{"uppercase_lowercased", "alias: BR\n", true, "br"},
		{"quoted", "alias: \"ainstr\"\n", true, "ainstr"},
		{"hyphen_and_digits", "alias: web-2\n", true, "web-2"},
		{"missing_file", "", false, ""},
		{"empty_alias", "alias: \"\"\n", true, ""},
		{"malformed_yaml", "alias: [not a scalar\n", true, ""},
		{"invalid_leading_hyphen", "alias: -bad\n", true, ""},
		{"invalid_chars", "alias: bad_slug!\n", true, ""},
		{"no_alias_key", "other: value\n", true, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if tc.write {
				writeBridgeYAML(t, dir, tc.contents)
			}
			if got := readBridgeAlias(dir); got != tc.want {
				t.Fatalf("readBridgeAlias() = %q, want %q", got, tc.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/core/ -run TestReadBridgeAlias`
Expected: FAIL — `readBridgeAlias` undefined.

- [ ] **Step 3: Implement `readBridgeAlias`**

Create `internal/core/bridgeyaml.go`:

```go
package core

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"

	yaml "go.yaml.in/yaml/v3"
)

// aliasPattern is the permitted shape of a repo alias: lowercase alphanumerics
// and hyphens, not starting with a hyphen.
var aliasPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// bridgeConfig is the parsed shape of a repo's .bridge.yaml. Only Alias is
// consumed today; the file is intentionally extensible (future: idea-target,
// issue-labels), so unknown keys are ignored by the YAML decoder.
type bridgeConfig struct {
	Alias string `yaml:"alias"`
}

// readBridgeAlias reads and validates the alias from <repoPath>/.bridge.yaml.
// It returns the lowercased alias, or "" when the file is absent, unreadable,
// malformed, or the alias violates aliasPattern. Discovery must never fail on a
// bad .bridge.yaml, so all error paths degrade to "".
func readBridgeAlias(repoPath string) string {
	raw, err := os.ReadFile(filepath.Join(repoPath, ".bridge.yaml"))
	if err != nil {
		return ""
	}
	var cfg bridgeConfig
	if err := yaml.Unmarshal(raw, &cfg); err != nil {
		return ""
	}
	alias := strings.ToLower(strings.TrimSpace(cfg.Alias))
	if !aliasPattern.MatchString(alias) {
		return ""
	}
	return alias
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/core/ -run TestReadBridgeAlias -v`
Expected: PASS (all sub-cases).

- [ ] **Step 5: Add `Alias` to `Repo` and set it in discovery**

In `internal/core/repo.go`, add the field to the `Repo` struct (after `Visibility`):

```go
	Alias         string    `json:"alias,omitempty"`
```

Then, at each of the four `out = append(out, Repo{...})` sites in `DiscoverRepos` (github, gitlab, forgejo, ado), add `Alias: readBridgeAlias(repoPath)` to the struct literal. `repoPath` is already the in-scope local checkout dir at each site.

- [ ] **Step 6: Write the discovery test**

Add to `internal/core/repo_test.go` (create if absent; package `core`):

```go
func TestDiscoverRepos_SetsAliasFromBridgeYAML(t *testing.T) {
	root := t.TempDir()
	// github/<owner>/public/<repo> layout with a .git dir and a .bridge.yaml.
	repoPath := filepath.Join(root, "github", "freaxnx01", "public", "demo")
	if err := os.MkdirAll(filepath.Join(repoPath, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeBridgeYAML(t, repoPath, "alias: demo\n")

	repos, err := DiscoverRepos(root)
	if err != nil {
		t.Fatal(err)
	}
	var found *Repo
	for i := range repos {
		if repos[i].Name == "demo" {
			found = &repos[i]
		}
	}
	if found == nil {
		t.Fatalf("repo 'demo' not discovered in %v", repos)
	}
	if found.Alias != "demo" {
		t.Fatalf("Alias = %q, want %q", found.Alias, "demo")
	}
}
```

(Ensure the test file imports `os`, `path/filepath`, `testing`. Reuse `writeBridgeYAML` from `bridgeyaml_test.go` — same package.)

- [ ] **Step 7: Run the core tests**

Run: `go test ./internal/core/`
Expected: PASS.

- [ ] **Step 8: Tidy modules and verify build**

Run: `go mod tidy && go build ./...`
Expected: `go.yaml.in/yaml/v3` promoted to a direct require; build clean. Then `go test ./...` green.

- [ ] **Step 9: Commit**

```bash
git add internal/core/ go.mod go.sum
git commit -m "feat(core): index .bridge.yaml alias during repo discovery"
```

---

## Task 2: `core.ResolveAlias` + typed errors

**Files:**
- Create: `internal/core/alias.go`
- Test: `internal/core/alias_test.go`

**Interfaces:**
- Consumes: `Repo.Alias` (Task 1).
- Produces: `var ErrAliasNotFound = errors.New("unknown alias")`, `var ErrAliasAmbiguous = errors.New("ambiguous alias")`.
- Produces: `func ResolveAlias(alias string, repos []Repo) (Repo, error)` — case-insensitive match on `Repo.Alias`; `0` matches → `ErrAliasNotFound`; `≥2` → `ErrAliasAmbiguous`; exactly `1` → that `Repo`. Empty/blank `alias` → `ErrAliasNotFound` (never matches repos with empty alias).

- [ ] **Step 1: Write the failing test**

Create `internal/core/alias_test.go`:

```go
package core

import (
	"errors"
	"testing"
)

func TestResolveAlias(t *testing.T) {
	repos := []Repo{
		{Name: "bridge", Forge: "github", Owner: "freaxnx01", Alias: "br"},
		{Name: "agent-pipeline", Forge: "github", Owner: "freaxnx01", Alias: "agp"},
		{Name: "no-alias", Forge: "forgejo", Owner: "freax"},
	}

	t.Run("hit", func(t *testing.T) {
		got, err := ResolveAlias("br", repos)
		if err != nil {
			t.Fatalf("err = %v", err)
		}
		if got.Name != "bridge" || got.Forge != "github" {
			t.Fatalf("got %+v", got)
		}
	})

	t.Run("case_insensitive", func(t *testing.T) {
		got, err := ResolveAlias("AGP", repos)
		if err != nil || got.Name != "agent-pipeline" {
			t.Fatalf("got %+v err %v", got, err)
		}
	})

	t.Run("unknown", func(t *testing.T) {
		_, err := ResolveAlias("nope", repos)
		if !errors.Is(err, ErrAliasNotFound) {
			t.Fatalf("err = %v, want ErrAliasNotFound", err)
		}
	})

	t.Run("empty", func(t *testing.T) {
		_, err := ResolveAlias("", repos)
		if !errors.Is(err, ErrAliasNotFound) {
			t.Fatalf("err = %v, want ErrAliasNotFound", err)
		}
	})

	t.Run("ambiguous", func(t *testing.T) {
		dup := append([]Repo{}, repos...)
		dup = append(dup, Repo{Name: "brdup", Forge: "forgejo", Owner: "freax", Alias: "br"})
		_, err := ResolveAlias("br", dup)
		if !errors.Is(err, ErrAliasAmbiguous) {
			t.Fatalf("err = %v, want ErrAliasAmbiguous", err)
		}
	})
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/core/ -run TestResolveAlias`
Expected: FAIL — `ResolveAlias` undefined.

- [ ] **Step 3: Implement**

Create `internal/core/alias.go`:

```go
package core

import (
	"errors"
	"strings"
)

var (
	// ErrAliasNotFound is returned when no repo carries the requested alias.
	ErrAliasNotFound = errors.New("unknown alias")
	// ErrAliasAmbiguous is returned when more than one repo carries the alias;
	// bridge never silently picks one.
	ErrAliasAmbiguous = errors.New("ambiguous alias")
)

// ResolveAlias maps a repo alias to the single repo that declares it. Matching
// is case-insensitive on Repo.Alias. A blank alias, or an alias no repo carries,
// yields ErrAliasNotFound; two or more carriers yield ErrAliasAmbiguous.
func ResolveAlias(alias string, repos []Repo) (Repo, error) {
	want := strings.ToLower(strings.TrimSpace(alias))
	if want == "" {
		return Repo{}, ErrAliasNotFound
	}
	var match Repo
	found := 0
	for _, r := range repos {
		if r.Alias != "" && strings.EqualFold(r.Alias, want) {
			match = r
			found++
		}
	}
	switch found {
	case 0:
		return Repo{}, ErrAliasNotFound
	case 1:
		return match, nil
	default:
		return Repo{}, ErrAliasAmbiguous
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/core/ -run TestResolveAlias -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/core/alias.go internal/core/alias_test.go
git commit -m "feat(core): resolve a repo by alias with unknown/ambiguous errors"
```

---

## Task 3: Issue `body` through `capture.CaptureIssue`

**Files:**
- Modify: `internal/capture/capture.go` (`CaptureIssue` signature)
- Modify: `cmd/bridge/serve.go`, `cmd/bridge/capture.go` (call sites — compile only; full wiring in Task 4)
- Test: `internal/capture/capture_test.go`

**Interfaces:**
- Produces: `func CaptureIssue(ctx context.Context, w IssueCreator, owner, repo, title, body string) (forge.Issue, error)` — trims title, errors on empty title (unchanged), passes `body` through to `w.CreateIssue(ctx, owner, repo, title, body)`.

- [ ] **Step 1: Write the failing test**

In `internal/capture/capture_test.go`, add a test that asserts the body is forwarded. The existing fake `IssueCreator` must record the body. If the existing fake doesn't capture body, extend it (record the last `body` arg). Add:

```go
func TestCaptureIssue_ForwardsBody(t *testing.T) {
	fake := &fakeIssueCreator{issue: forge.Issue{Number: 7, URL: "https://forge/issues/7"}}
	got, err := CaptureIssue(context.Background(), fake, "freaxnx01", "bridge", "  Login 500  ", "detail body")
	if err != nil {
		t.Fatal(err)
	}
	if fake.gotTitle != "Login 500" {
		t.Fatalf("title = %q", fake.gotTitle)
	}
	if fake.gotBody != "detail body" {
		t.Fatalf("body = %q, want %q", fake.gotBody, "detail body")
	}
	if got.Number != 7 {
		t.Fatalf("issue = %+v", got)
	}
}
```

If a `fakeIssueCreator` doesn't already exist in this package's tests, add:

```go
type fakeIssueCreator struct {
	gotOwner, gotRepo, gotTitle, gotBody string
	issue                                forge.Issue
	err                                  error
}

func (f *fakeIssueCreator) CreateIssue(_ context.Context, owner, repo, title, body string) (forge.Issue, error) {
	f.gotOwner, f.gotRepo, f.gotTitle, f.gotBody = owner, repo, title, body
	return f.issue, f.err
}
```

(Imports: `context`, `testing`, and the `forge` package.)

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/capture/ -run TestCaptureIssue_ForwardsBody`
Expected: FAIL — `CaptureIssue` takes 4 args, not 5.

- [ ] **Step 3: Update `CaptureIssue`**

In `internal/capture/capture.go`, change the signature and the passthrough. Replace:

```go
func CaptureIssue(ctx context.Context, w IssueCreator, owner, repo, title string) (forge.Issue, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return forge.Issue{}, fmt.Errorf("empty issue title")
	}
	return w.CreateIssue(ctx, owner, repo, title, "")
}
```

with:

```go
func CaptureIssue(ctx context.Context, w IssueCreator, owner, repo, title, body string) (forge.Issue, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return forge.Issue{}, fmt.Errorf("empty issue title")
	}
	return w.CreateIssue(ctx, owner, repo, title, body)
}
```

- [ ] **Step 4: Fix the CLI call site (compile)**

In `cmd/bridge/capture.go`, find the `capture.CaptureIssue(...)` call and pass a body. If the CLI has no body concept yet, pass `""` for now (the REST path is what threads a real body): change `capture.CaptureIssue(ctx, creator, tgt.Owner, tgt.Repo, title)` → `capture.CaptureIssue(ctx, creator, tgt.Owner, tgt.Repo, title, "")`. (The `serve.go` call site is updated in Task 4.)

- [ ] **Step 5: Run tests + build**

Run: `go test ./internal/capture/ -run TestCaptureIssue_ForwardsBody -v && go build ./...`
Expected: test PASS; build clean (serve.go still passes 4 args? — no: serve.go must also be fixed here to compile). Update the `serve.go` Issue closure call `capture.CaptureIssue(c, creator, tgt.Owner, tgt.Repo, title)` → append `, ""` for now (Task 4 replaces the `""` with the real body). Re-run `go build ./...` → clean.

- [ ] **Step 6: Commit**

```bash
git add internal/capture/ cmd/bridge/capture.go cmd/bridge/serve.go
git commit -m "feat(capture): thread issue body through CaptureIssue"
```

---

## Task 4: REST layer — `alias`/`body`, validation, resolver-error → status

**Files:**
- Modify: `internal/api/capture.go` (request structs, validation, func-field signatures, error mapping)
- Modify: `cmd/bridge/serve.go` (capture closures: alias resolution + pass body)
- Test: `internal/api/capture_test.go`

**Interfaces:**
- Consumes: `core.ResolveAlias`, `core.ErrAliasNotFound`, `core.ErrAliasAmbiguous` (Task 2); `capture.CaptureIssue(...,body)` (Task 3).
- Produces: `CaptureHandler.Issue func(ctx context.Context, p IssueParams) (forge.Issue, error)` and `CaptureHandler.Idea func(ctx context.Context, p IdeaParams) (string, error)`, where:

```go
type IssueParams struct{ Owner, Repo, Alias, Title, Body string }
type IdeaParams struct{ Target, Alias, Text string }
```

(Define these in `internal/api/capture.go`.)

- [ ] **Step 1: Write the failing tests**

In `internal/api/capture_test.go`, add handler tests. The handler is driven by injected `Issue`/`Idea` funcs (fakes). Cover: alias issue happy path (body forwarded), alias idea happy path, unknown alias → 404, ambiguous alias → 409, missing-everything → 400. Example:

```go
func TestCaptureIssue_AliasAndBody_ForwardedToIssueFunc(t *testing.T) {
	var got IssueParams
	h := &CaptureHandler{
		Issue: func(_ context.Context, p IssueParams) (forge.Issue, error) {
			got = p
			return forge.Issue{Number: 1, URL: "https://forge/issues/1"}, nil
		},
	}
	body := `{"alias":"br","title":"Login 500","body":"the detail"}`
	req := httptest.NewRequest(http.MethodPost, "/api/capture/issue", strings.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if got.Alias != "br" || got.Title != "Login 500" || got.Body != "the detail" {
		t.Fatalf("params = %+v", got)
	}
}

func TestCaptureIssue_UnknownAlias_Returns404(t *testing.T) {
	h := &CaptureHandler{
		Issue: func(_ context.Context, _ IssueParams) (forge.Issue, error) {
			return forge.Issue{}, core.ErrAliasNotFound
		},
	}
	req := httptest.NewRequest(http.MethodPost, "/api/capture/issue", strings.NewReader(`{"alias":"nope","title":"x"}`))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d", w.Code)
	}
}

func TestCaptureIssue_AmbiguousAlias_Returns409(t *testing.T) {
	h := &CaptureHandler{
		Issue: func(_ context.Context, _ IssueParams) (forge.Issue, error) {
			return forge.Issue{}, core.ErrAliasAmbiguous
		},
	}
	req := httptest.NewRequest(http.MethodPost, "/api/capture/issue", strings.NewReader(`{"alias":"br","title":"x"}`))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("status = %d", w.Code)
	}
}

func TestCaptureIssue_NoTarget_Returns400(t *testing.T) {
	h := &CaptureHandler{Issue: func(_ context.Context, _ IssueParams) (forge.Issue, error) {
		t.Fatal("Issue should not be called")
		return forge.Issue{}, nil
	}}
	req := httptest.NewRequest(http.MethodPost, "/api/capture/issue", strings.NewReader(`{"title":"x"}`))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", w.Code)
	}
}

func TestCaptureIdea_AliasHappyPath(t *testing.T) {
	var got IdeaParams
	h := &CaptureHandler{
		Idea: func(_ context.Context, p IdeaParams) (string, error) {
			got = p
			return "https://forge/ideas.md#x", nil
		},
	}
	req := httptest.NewRequest(http.MethodPost, "/api/capture/idea", strings.NewReader(`{"alias":"agp","text":"what if"}`))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK || got.Alias != "agp" || got.Text != "what if" {
		t.Fatalf("code=%d params=%+v", w.Code, got)
	}
}
```

Update any **existing** capture_test.go cases that construct `CaptureHandler{Issue: func(ctx, owner, repo, title)...}` to the new `IssueParams`/`IdeaParams` signatures (accommodation — keep their assertions).

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/api/ -run TestCapture`
Expected: FAIL — new fields/signatures/status mapping absent; existing cases may not compile until updated.

- [ ] **Step 3: Update request structs, validation, signatures, error mapping**

In `internal/api/capture.go`:

1. Add the param structs (near the top):

```go
type IssueParams struct{ Owner, Repo, Alias, Title, Body string }
type IdeaParams struct{ Target, Alias, Text string }
```

2. Change the `CaptureHandler` `Issue`/`Idea` field types to `func(context.Context, IssueParams) (forge.Issue, error)` and `func(context.Context, IdeaParams) (string, error)`.

3. Extend the request structs:

```go
type issueRequest struct {
	Owner string `json:"owner"`
	Repo  string `json:"repo"`
	Alias string `json:"alias"`
	Title string `json:"title"`
	Body  string `json:"body"`
}

type ideaRequest struct {
	Target string `json:"target"`
	Alias  string `json:"alias"`
	Text   string `json:"text"`
}
```

4. Relax validation. Issue: `Title` required; and **either** `Alias` **or** (`Owner` and `Repo`). Idea: `Text` required; and **either** `Alias` **or** `Target`. On violation → `http.StatusBadRequest`.

5. Call the func-field with params: `h.Issue(ctx, IssueParams{Owner: req.Owner, Repo: req.Repo, Alias: req.Alias, Title: req.Title, Body: req.Body})` (and the idea equivalent).

6. Map the returned error to status (replace the current "err → 500" branch):

```go
issue, err := h.Issue(ctx, params)
if err != nil {
	switch {
	case errors.Is(err, core.ErrAliasNotFound):
		http.Error(w, err.Error(), http.StatusNotFound)
	case errors.Is(err, core.ErrAliasAmbiguous):
		http.Error(w, err.Error(), http.StatusConflict)
	default:
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
	return
}
```

(Apply the same mapping in the idea handler. Add imports `errors` and the `core` package.)

- [ ] **Step 4: Wire alias resolution + body in the serve.go closures**

In `cmd/bridge/serve.go`, change the capture closures to the new signatures and resolve alias when present. For the **Issue** closure:

```go
Issue: func(ctx context.Context, p api.IssueParams) (forge.Issue, error) {
	owner, repo, forgeName := p.Owner, p.Repo, ""
	if p.Alias != "" {
		r, err := core.ResolveAlias(p.Alias, repos)
		if err != nil {
			return forge.Issue{}, err // core.ErrAliasNotFound / ErrAliasAmbiguous → mapped by the handler
		}
		owner, repo, forgeName = r.Owner, r.Name, r.Forge
	} else {
		tgt, err := resolveIssueTarget(owner+"/"+repo, repos)
		if err != nil {
			return forge.Issue{}, err
		}
		owner, repo, forgeName = tgt.Owner, tgt.Repo, tgt.Forge
	}
	creator, err := issueCreatorFor(forgeName /* + existing client/token selection */)
	if err != nil {
		return forge.Issue{}, err
	}
	return capture.CaptureIssue(ctx, creator, owner, repo, p.Title, p.Body)
},
```

Adapt to the existing client/token-selection code already in the closure (the `switch tgt.Forge` block that picks `NewGithubClient`/`NewForgejoClient` with the right token) — factor it to take a `forgeName`/owner so both the alias and owner/repo paths reuse it. Do the analogous change for the **Idea** closure (resolve alias → `capture.Target{Owner, Repo}` for the non-ideas-lab case, else existing `resolveCaptureTarget`).

`repos` is the discovered slice already in scope where the closures are built (`discoverAllRoots()` result). If it isn't, capture it once before building the closures: `repos, _ := discoverAllRoots()`.

- [ ] **Step 5: Run tests + build**

Run: `go test ./internal/api/ && go build ./...`
Expected: api tests PASS; whole build clean.

- [ ] **Step 6: Verify `GET /api/repos` still serializes alias**

Add to `internal/api/repos_test.go` a case asserting a repo's `alias` appears in the JSON (extend the existing `fakeRepos()` to include an `Alias` and assert it round-trips). Run `go test ./internal/api/`.

- [ ] **Step 7: Commit**

```bash
git add internal/api/ cmd/bridge/serve.go
git commit -m "feat(api): accept alias + body on capture endpoints, map resolver errors"
```

---

## Task 5: `BRIDGE_API_TOKEN` bearer middleware

**Files:**
- Modify: `cmd/bridge/serve.go` (middleware + read `BRIDGE_API_TOKEN`, wrap `captureH`)
- Test: `cmd/bridge/serve_test.go`

**Interfaces:**
- Produces: `func requireBearer(token string, next http.Handler) http.Handler` (package `main`, in `cmd/bridge`). When `token == ""` → returns `next` unchanged (auth disabled). Otherwise returns a handler that requires `Authorization: Bearer <token>` (constant-time compare); on missing/mismatch → `401` and does not call `next`.

- [ ] **Step 1: Write the failing test**

Create `cmd/bridge/serve_test.go`:

```go
package main

import (
	"crypto/subtle"
	"net/http"
	"net/http/httptest"
	"testing"
)

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
}

func TestRequireBearer_NoTokenConfigured_PassesThrough(t *testing.T) {
	h := requireBearer("", okHandler())
	req := httptest.NewRequest(http.MethodPost, "/api/capture/issue", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (auth disabled)", w.Code)
	}
}

func TestRequireBearer_MissingHeader_401(t *testing.T) {
	h := requireBearer("secret", okHandler())
	req := httptest.NewRequest(http.MethodPost, "/api/capture/issue", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}

func TestRequireBearer_WrongToken_401(t *testing.T) {
	h := requireBearer("secret", okHandler())
	req := httptest.NewRequest(http.MethodPost, "/api/capture/issue", nil)
	req.Header.Set("Authorization", "Bearer nope")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}

func TestRequireBearer_CorrectToken_PassesThrough(t *testing.T) {
	h := requireBearer("secret", okHandler())
	req := httptest.NewRequest(http.MethodPost, "/api/capture/issue", nil)
	req.Header.Set("Authorization", "Bearer secret")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	// keep subtle imported in the test as a sanity anchor for constant-time intent
	_ = subtle.ConstantTimeCompare
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/bridge/ -run TestRequireBearer`
Expected: FAIL — `requireBearer` undefined.

- [ ] **Step 3: Implement the middleware**

In `cmd/bridge/serve.go` add:

```go
// requireBearer gates a handler behind a static bearer token. When token is
// empty, auth is disabled and next is returned unchanged (dev/LAN default). When
// set, requests must carry "Authorization: Bearer <token>" (constant-time
// compared) or receive 401.
func requireBearer(token string, next http.Handler) http.Handler {
	if token == "" {
		return next
	}
	want := []byte("Bearer " + token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := []byte(r.Header.Get("Authorization"))
		if len(got) != len(want) || subtle.ConstantTimeCompare(got, want) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}
```

(Add `crypto/subtle` to `serve.go` imports.)

- [ ] **Step 4: Wrap the capture handler**

In `runServe`, read the token once and wrap only the capture handler. Replace the registration:

```go
apiMux.Handle("/api/capture/", captureH)
```

with:

```go
apiToken := os.Getenv("BRIDGE_API_TOKEN")
apiMux.Handle("/api/capture/", requireBearer(apiToken, captureH))
```

Read endpoints (`/api/repos`, `/api/overview`, …) stay unwrapped.

- [ ] **Step 5: Run tests + full build/test**

Run: `go test ./cmd/bridge/ -run TestRequireBearer -v && go test ./... && go build ./...`
Expected: PASS; whole suite green.

- [ ] **Step 6: Lint + vuln check (CI parity)**

Run: `golangci-lint run ./... ; govulncheck ./...`
Expected: both clean. (If `golangci-lint` isn't installed locally, note it — CI will run it.)

- [ ] **Step 7: Commit**

```bash
git add cmd/bridge/serve.go cmd/bridge/serve_test.go
git commit -m "feat(serve): bearer-protect /api/capture with BRIDGE_API_TOKEN"
```

---

## Self-Review

**Spec coverage (design §B):**
- §B.1 index `.bridge.yaml`, add `Alias`, surface on `GET /api/repos` → Task 1 (+ repos_test in Task 4). ✅
- §B.2 resolve by alias; unknown → 404; ambiguous → 409 → Task 2 (resolver) + Task 4 (mapping). ✅
- §B.3 add `body` to issue creation → Task 3 (+ Task 4 threads it from REST). ✅
- §B.4 `BRIDGE_API_TOKEN` bearer on `/api/capture/*`, read endpoints unchanged → Task 5. ✅
- Error contract 400/401/404/409/5xx → Task 4 (400/404/409/5xx) + Task 5 (401). ✅
- Duplicate alias never silently picked → `ResolveAlias` ambiguous (Task 2). ✅

**Placeholder scan:** No TBD/"handle errors"; each step carries code or an exact edit. The `serve.go` client/token-selection factoring (Task 4 Step 4) references existing code the implementer adapts — flagged explicitly, not hand-waved.

**Type consistency:** `IssueParams`/`IdeaParams`, `ResolveAlias(alias, []Repo)(Repo,error)`, `ErrAliasNotFound`/`ErrAliasAmbiguous`, `requireBearer(token,next)`, `CaptureIssue(...,body)` used identically across tasks.

**Open item for the implementer (Task 4 Step 4):** the exact shape of the existing forge-client/token selection in the `serve.go` closures wasn't fully quoted here; the implementer must read `cmd/bridge/serve.go:83-125` and reuse that selection for both the alias and owner/repo paths (factor a small `issueCreatorFor(forge, ...)` helper rather than duplicating the `switch`).

**Deferred (not in scope, per spec "non-goals"):** `bridge doctor` dup-alias surfacing (the resolver's ambiguous error + an optional discovery-time log line is enough now); per-repo `idea-target`/`issue-labels` fields (the YAML struct is extensible — add fields when needed).
