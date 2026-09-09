# File Toolset Over REST Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `read_file`, `list_tree`, `search_code` and `put_file` reachable over REST at `POST /api/tools/{name}`, so a non-MCP client (FlowHub) can search, read and write repository files.

**Architecture:** A new `internal/mcp/rest.go` exposes `RESTHandler(deps Deps) http.Handler` that decodes the existing unexported `*Input` structs, calls the existing `Deps.handleX` methods, and encodes the existing `*Output` structs. It lives in `internal/mcp` precisely so the two transports share one implementation and cannot drift. `serve.go` registers it behind the same bearer wrapper `/api/capture/` uses.

**Tech Stack:** Go, `net/http`, `encoding/json`, standard library testing with `httptest`.

**Spec:** `docs/superpowers/specs/2026-09-09-rest-file-tools-design.md`

## Global Constraints

- **No change to the MCP surface or its behaviour.** If an existing test in `internal/mcp` needs editing, something has drifted — stop and re-read the plan.
- **No write guard may be relaxed.** The path allowlist, the `confirm=true` draft gate and the `sha` requirement all live inside `handlePutFile`; calling that handler is what preserves them. Do not reimplement any of them.
- **`put_file` must be refused when `deps.ReadOnly`** — MCP enforces this by not registering the tool (`internal/mcp/server.go:82-93`), so REST has to enforce it explicitly.
- Tool names are the MCP names verbatim: `read_file`, `list_tree`, `search_code`, `put_file`.
- Follow the existing dispatch shape in `internal/api/capture.go:34-43` (trim prefix, switch, unknown → 404).
- Conventional Commits; scope `api`.
- `gofmt` clean; `go vet ./...` clean; the repo's `golangci-lint` config applies.

---

## File Structure

- `internal/mcp/rest.go` — **create.** `RESTHandler`, the dispatch switch, and the four per-tool funcs.
- `internal/mcp/rest_test.go` — **create.** Table-driven tests per tool plus the negative cases.
- `cmd/bridge/serve.go` — **modify** (near `:163-168`). Register `/api/tools/` behind `requireBearer`.
- `cmd/bridge/serve_test.go` — **modify.** Assert the route is bearer-guarded.

---

### Task 1: The REST dispatcher and the three read tools

**Files:**
- Create: `internal/mcp/rest.go`
- Create: `internal/mcp/rest_test.go`

**Interfaces:**
- Consumes: `Deps`, `readFileInput`, `listTreeInput`, `searchCodeInput` and the `Deps.handleReadFile` / `handleListTree` / `handleSearchCode` methods — all already in package `mcp`.
- Produces: `func RESTHandler(deps Deps) http.Handler`.

- [ ] **Step 1: Write the failing test**

```go
package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// restRequest posts body to /api/tools/<tool> and returns the recorder.
func restRequest(t *testing.T, deps Deps, tool, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/api/tools/"+tool, strings.NewReader(body))
	w := httptest.NewRecorder()
	RESTHandler(deps).ServeHTTP(w, r)
	return w
}

func TestRESTHandler_UnknownTool(t *testing.T) {
	w := restRequest(t, Deps{}, "nope", `{}`)
	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d (%s)", w.Code, w.Body.String())
	}
}

func TestRESTHandler_WrongMethod(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/api/tools/read_file", nil)
	w := httptest.NewRecorder()
	RESTHandler(Deps{}).ServeHTTP(w, r)
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("want 405, got %d", w.Code)
	}
}

func TestRESTHandler_MalformedBody(t *testing.T) {
	w := restRequest(t, Deps{}, "read_file", `{not json`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", w.Code)
	}
}

func TestRESTHandler_ReadFile_ReturnsHandlerOutput(t *testing.T) {
	deps := depsWithFakeForge(t) // see note in Step 3
	w := restRequest(t, deps, "read_file",
		`{"forge":"github","owner":"o","repo":"r","path":"README.md"}`)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d (%s)", w.Code, w.Body.String())
	}
	var out readFileOutput
	if err := json.NewDecoder(w.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !out.Found {
		t.Fatalf("want found=true, got %+v", out)
	}
}

var _ = context.Background // keep the import if unused after edits
```

**Note on `depsWithFakeForge`:** `internal/mcp` already has test fakes for its
`ForgeReader` — find them with `grep -rn "func.*fake\|stub" internal/mcp/*_test.go` and
reuse the existing one rather than writing a new fake. If several exist, prefer the one
the `read_file` MCP tests already use, so REST and MCP are exercised against identical
behaviour.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/mcp/ -run TestRESTHandler -v`
Expected: FAIL — build error, `RESTHandler` is undefined.

- [ ] **Step 3: Write the minimal implementation**

`internal/mcp/rest.go`:

```go
package mcp

import (
	"encoding/json"
	"net/http"
	"strings"
)

// RESTHandler serves the file toolset over plain HTTP at POST /api/tools/{name}.
//
// It lives in this package on purpose: the input structs and handler methods are
// unexported, and sharing them is what stops the REST and MCP surfaces drifting.
// There is one implementation of each tool; this is a second transport, not a
// second copy.
func RESTHandler(deps Deps) http.Handler {
	return &restHandler{deps: deps}
}

type restHandler struct{ deps Deps }

func (h *restHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		restError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	switch strings.TrimPrefix(r.URL.Path, "/api/tools/") {
	case "read_file":
		restCall(w, r, h.deps.handleReadFile)
	case "list_tree":
		restCall(w, r, h.deps.handleListTree)
	case "search_code":
		restCall(w, r, h.deps.handleSearchCode)
	default:
		restError(w, http.StatusNotFound, "unknown tool")
	}
}

// restCall decodes In from the body, invokes the shared handler, and encodes Out.
// The *mcp.CallToolResult the handler returns is MCP presentation and is discarded.
func restCall[In any, Out any](
	w http.ResponseWriter,
	r *http.Request,
	handle func(context.Context, *mcp.CallToolRequest, In) (*mcp.CallToolResult, Out, error),
) {
	var in In
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		restError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	_, out, err := handle(r.Context(), nil, in)
	if err != nil {
		restError(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

func restError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
```

**Import note:** `restCall` references `context` and the `mcp` SDK types in its signature.
Add the same import the tool handlers use — check the import block at the top of
`internal/mcp/tools_read.go` and copy it exactly, rather than guessing the module path.

**If generics fight the handler signature:** the four handlers have identical shape, so
one generic helper should fit. If the SDK's callback type does not unify, fall back to
four small non-generic funcs — explicit and boring beats clever here. Do not change the
handler signatures to make a generic work.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/mcp/ -run TestRESTHandler -v`
Expected: PASS

- [ ] **Step 5: Confirm nothing else moved, then commit and push**

```bash
gofmt -l internal/mcp/ && go vet ./internal/mcp/ && go test ./internal/mcp/
git add internal/mcp/rest.go internal/mcp/rest_test.go
git commit -m "feat(api): serve the read tools over REST at /api/tools/ (#278)"
git push
```

`go test ./internal/mcp/` must be green **without editing any existing test**. If an
existing test fails, the shared handlers were changed — revert that and re-read the
Global Constraints.

---

### Task 2: `put_file` over REST, refused on a read-only server

**Files:**
- Modify: `internal/mcp/rest.go`
- Modify: `internal/mcp/rest_test.go`

**Interfaces:**
- Consumes: `putFileInput`, `Deps.handlePutFile`, `Deps.ReadOnly` from Task 1's package.
- Produces: the `put_file` case on the same handler.

MCP withholds this tool by not registering it when `deps.ReadOnly`
(`internal/mcp/server.go:82-93`). REST has no registration step, so the check is explicit.

- [ ] **Step 1: Write the failing test**

```go
func TestRESTHandler_PutFile_RefusedWhenReadOnly(t *testing.T) {
	deps := depsWithFakeForge(t)
	deps.ReadOnly = true

	w := restRequest(t, deps, "put_file",
		`{"forge":"github","owner":"o","repo":"r","path":"docs/x.md","content":"hi","message":"m","confirm":true}`)

	if w.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %d (%s)", w.Code, w.Body.String())
	}
}

func TestRESTHandler_PutFile_WithoutConfirmReturnsDraft(t *testing.T) {
	// The confirm gate lives inside handlePutFile — REST must not pre-empt it,
	// and a draft is a successful response, not an error.
	deps := depsWithFakeForge(t)

	w := restRequest(t, deps, "put_file",
		`{"forge":"github","owner":"o","repo":"r","path":"docs/x.md","content":"hi","message":"m"}`)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d (%s)", w.Code, w.Body.String())
	}
	var out putFileOutput
	if err := json.NewDecoder(w.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !out.Draft {
		t.Fatalf("want draft=true, got %+v", out)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/mcp/ -run TestRESTHandler_PutFile -v`
Expected: FAIL — `put_file` currently falls through to `unknown tool`, so both get 404.

- [ ] **Step 3: Write the minimal implementation**

Add to the switch in `ServeHTTP`, before `default`:

```go
	case "put_file":
		// MCP enforces this by not registering the tool on a read-only server;
		// REST has no registration step, so the check is explicit here.
		if h.deps.ReadOnly {
			restError(w, http.StatusForbidden, "write tools are disabled on this server")
			return
		}
		restCall(w, r, h.deps.handlePutFile)
```

Do not add an allowlist check, a `confirm` check or a `sha` check — all three are inside
`handlePutFile`, and duplicating them here is how the two paths start to disagree.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/mcp/ -v`
Expected: PASS — the new tests and every pre-existing test in the package.

- [ ] **Step 5: Commit and push**

```bash
gofmt -l internal/mcp/ && go vet ./internal/mcp/ && go test ./internal/mcp/
git add internal/mcp/rest.go internal/mcp/rest_test.go
git commit -m "feat(api): serve put_file over REST, refused on a read-only server (#278)"
git push
```

---

### Task 3: Wire the route behind bearer auth

**Files:**
- Modify: `cmd/bridge/serve.go` (near `:163-168`)
- Modify: `cmd/bridge/serve_test.go`

**Interfaces:**
- Consumes: `mcp.RESTHandler` from Tasks 1-2, and the existing `requireBearer` helper.
- Produces: the live `/api/tools/` route.

- [ ] **Step 1: Write the failing test**

Follow the shape of the existing bearer tests in `cmd/bridge/serve_test.go` (they already
cover `/api/capture/issue` — read them first and mirror the setup exactly):

```go
func TestAPIMux_ToolsRequiresBearer(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/tools/read_file", nil)
	// … same harness the existing capture bearer tests use …
	// Expect: 401 without a bearer token.
}

func TestAPIMux_ToolsAcceptsValidBearer(t *testing.T) {
	// Same request with the configured token → NOT 401.
	// Assert only "not 401" — the body depends on forge wiring the harness may not have.
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./cmd/bridge/ -run TestAPIMux_Tools -v`
Expected: FAIL — the route is unregistered, so the mux 404s instead of 401ing.

- [ ] **Step 3: Write the minimal implementation**

In `cmd/bridge/serve.go`, alongside the existing registrations at `:163-168`:

```go
	apiMux.Handle("/api/tools/", requireBearer(apiToken, mcp.RESTHandler(deps)))
```

The `deps` value here must be **the same `mcp.Deps` the MCP server is built from** — if
`serve.go` does not already construct one, find where the MCP server gets its `Deps` and
share that construction rather than building a second one. Two independently-built `Deps`
would be exactly the drift this design exists to prevent.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./cmd/bridge/ -v`
Expected: PASS, including every pre-existing test.

- [ ] **Step 5: Full suite, then commit and push**

```bash
gofmt -l . && go vet ./... && go test ./...
git add cmd/bridge/serve.go cmd/bridge/serve_test.go
git commit -m "feat(api): register /api/tools/ behind bearer auth (#278)"
git push
```

---

### Task 4: Document the surface

**Files:**
- Modify: `docs/mcp-cheatsheet.md` — add a short REST section, or say why it does not belong there
- Modify: `README.md` **only if** it enumerates API routes

**Interfaces:** none.

- [ ] **Step 1: Check where the API surface is currently documented**

```bash
grep -rn "api/capture\|api/repos" docs/*.md README.md | head
```

Add the four routes wherever that lands, with one line each: method, path, and that the
request body is the same JSON the MCP tool takes. Note explicitly that `search_code` is
GitHub-only and that a Forgejo target returns a warning rather than an empty result — a
REST caller has no tool description to read, so the constraint must be written down.

If no such enumeration exists, add a short **REST API** section to `docs/mcp-cheatsheet.md`
rather than creating a new file.

- [ ] **Step 2: Commit and push**

```bash
git add docs/
git commit -m "docs(api): document the REST file toolset (#278)"
git push
```

---

## Self-review

**Spec coverage:** D1 (handler in `internal/mcp`) → Task 1 Step 3. D2 (`/api/tools/{name}`, prefix switch, 404 unknown) → Task 1. D3 (guards; ReadOnly 403; bearer) → Tasks 2 and 3. D4 (Forgejo search warning preserved) → falls out of calling the shared handler; asserted implicitly because no MCP test changes. Error-handling table → Tasks 1-2.

**Placeholder scan:** the two spots that cannot be written blind are called out as instructions rather than guesses — the existing test fake in Task 1, and the `Deps` construction site in Task 3. Both say what to look for and what the wrong answer looks like.

**Type consistency:** `RESTHandler(deps Deps) http.Handler` is produced in Task 1 and consumed in Task 3. `restCall` and `restError` are defined in Task 1 and reused in Task 2. Input and output struct names (`readFileOutput`, `putFileOutput`) match `internal/mcp/tools_read.go:36` and `tools_write.go:443`.

**Known risk carried deliberately:** the generic `restCall` may not unify against the SDK's callback type. Task 1 Step 3 names the fallback (four small non-generic funcs) and forbids the tempting wrong fix (changing the handler signatures).
