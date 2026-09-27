# MCP Stateless Transport Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Serve `bridge mcp serve`'s Streamable HTTP endpoint statelessly with JSON responses, so no MCP protocol session lives in server memory.

**Architecture:** One new constructor `newStreamableHandler(srv)` in `cmd/bridge/mcp.go` builds the SDK handler with `StreamableHTTPOptions{Stateless: true, JSONResponse: true}`; both `buildMCPHandler` (static / no-auth) and `buildOAuthHandler` call it instead of their own `NewStreamableHTTPHandler(…, nil)`. `internal/mcp` is not touched.

**Tech Stack:** Go, `github.com/modelcontextprotocol/go-sdk` v1.6.1 (already in `go.mod`), stdlib `net/http` + `httptest`.

**Spec:** `docs/superpowers/specs/2026-09-28-mcp-stateless-transport-design.md`

## Global Constraints

- No new Go modules; no change to the `go` line in `go.mod`.
- Stateless mode is **always on** — no flag, no env var.
- Options are exactly `&sdkmcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true}`; no other field set.
- `WriteTimeout` on the `http.Server` stays `0` (only its comment changes).
- Bearer auth behaviour is unchanged in both auth modes.
- Tests: stdlib `testing` only, table-driven with `t.Run`, no testify. Names follow `TestFunc_State_Expected`.
- Gate after every task: `gofmt -l .` empty, `go vet ./...`, `golangci-lint run`, `go test -race ./...`.

## Review Focus

- **Stale session ID after a restart** — a client re-sending an `Mcp-Session-Id` the server never issued must get 200, not 404 (the whole point). Pinned in Task 1 (`made-up session id` case).
- **Auth bypass via session header** — a request with a session ID but no bearer must still be 401; statelessness must not weaken auth. Pinned in Task 1 (`TestBuildMCPHandler_StatelessStillRequiresBearer`).
- **Client lifecycle traffic** — `notifications/initialized` (202) and `DELETE` on client shutdown (204) must not error, or clients log failures on every connect/disconnect. Pinned in Task 1 table.
- **Client without `MCP-Protocol-Version` header** — older clients omit it; must still get a result (SDK assumes 2025-03-26). Pinned in Task 1 table (the default case sends no version header).
- **Real SDK client end-to-end** — `TestBuildMCPHandler_ValidBearerListsTools` (existing) must stay green unchanged; it is the proof that a spec-compliant client handles 405-on-GET and JSON responses.

---

### Task 1: Stateless JSON transport for both auth modes

**Files:**
- Modify: `cmd/bridge/mcp.go` — `buildMCPHandler` (the `streamable := …` line) and `buildOAuthHandler` (the `streamable := …` line); add `newStreamableHandler` just above `buildMCPHandler`
- Test: `cmd/bridge/mcp_test.go` (append)

**Interfaces:**
- Consumes: existing `buildMCPHandler(srv *sdkmcp.Server, token string, noAuth bool) (http.Handler, error)`, `imcp.NewServer(imcp.Deps) *sdkmcp.Server`.
- Produces: `func newStreamableHandler(srv *sdkmcp.Server) http.Handler` (package `main`, `cmd/bridge`).

- [ ] **Step 1: Write the failing tests**

Append to the end of `cmd/bridge/mcp_test.go`, separated from the last function by a blank line. Add `"io"` and `"strings"` to the existing import block (keep it gofmt-sorted).

```go
// listGitForgesCall is a raw tools/call for list_git_forges, which makes no
// network requests, so it exercises the transport without any forge fakes.
const listGitForgesCall = `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"list_git_forges","arguments":{}}}`

// rawMCPRequest sends one raw HTTP request to the MCP endpoint with the
// headers a Streamable HTTP client sends, overridden by hdr (an empty value
// deletes the header). Returns status, headers and body.
func rawMCPRequest(t *testing.T, url, method, body string, hdr map[string]string) (int, http.Header, string) {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	for k, v := range hdr {
		if v == "" {
			req.Header.Del(k)
			continue
		}
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }() // read fully below; close error is not actionable in a test
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, resp.Header, string(b)
}

func TestBuildMCPHandler_StatelessTransport_ServesWithoutSession(t *testing.T) {
	modes := []struct {
		name   string
		token  string
		noAuth bool
		auth   map[string]string
	}{
		{"no-auth", "", true, nil},
		{"bearer", "s3cret", false, map[string]string{"Authorization": "Bearer s3cret"}},
	}
	cases := []struct {
		name            string
		method          string
		body            string
		hdr             map[string]string
		wantStatus      int
		wantContentType string // "" = don't check
		wantBody        string // substring; "" = don't check
		wantAllow       string // "" = don't check
	}{
		{"tools/call without initialize or session id", http.MethodPost, listGitForgesCall, nil,
			http.StatusOK, "application/json", `"result"`, ""},
		{"made-up session id is not rejected", http.MethodPost, listGitForgesCall,
			map[string]string{"Mcp-Session-Id": "never-issued"},
			http.StatusOK, "application/json", `"result"`, ""},
		{"initialize answers with json", http.MethodPost,
			`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"clientInfo":{"name":"c","version":"v0"},"protocolVersion":"2025-06-18","capabilities":{}}}`,
			map[string]string{"MCP-Protocol-Version": "2025-06-18"},
			http.StatusOK, "application/json", `"protocolVersion"`, ""},
		{"initialized notification is accepted", http.MethodPost,
			`{"jsonrpc":"2.0","method":"notifications/initialized"}`, nil,
			http.StatusAccepted, "", "", ""},
		{"GET offers no stream", http.MethodGet, "",
			map[string]string{"Accept": "text/event-stream", "Content-Type": ""},
			http.StatusMethodNotAllowed, "", "", "POST"},
		{"DELETE on shutdown is a no-op", http.MethodDelete, "",
			map[string]string{"Mcp-Session-Id": "never-issued"},
			http.StatusNoContent, "", "", ""},
	}
	for _, m := range modes {
		h, err := buildMCPHandler(imcp.NewServer(imcp.Deps{ReadOnly: true}), m.token, m.noAuth)
		if err != nil {
			t.Fatal(err)
		}
		ts := httptest.NewServer(h)
		t.Cleanup(ts.Close)
		for _, tc := range cases {
			t.Run(m.name+"/"+tc.name, func(t *testing.T) {
				hdr := map[string]string{}
				for k, v := range m.auth {
					hdr[k] = v
				}
				for k, v := range tc.hdr {
					hdr[k] = v
				}
				status, header, body := rawMCPRequest(t, ts.URL, tc.method, tc.body, hdr)
				if status != tc.wantStatus {
					t.Fatalf("status = %d, want %d (body %q)", status, tc.wantStatus, body)
				}
				if tc.wantContentType != "" && header.Get("Content-Type") != tc.wantContentType {
					t.Errorf("Content-Type = %q, want %q", header.Get("Content-Type"), tc.wantContentType)
				}
				if tc.wantBody != "" && !strings.Contains(body, tc.wantBody) {
					t.Errorf("body %q does not contain %q", body, tc.wantBody)
				}
				if tc.wantAllow != "" && header.Get("Allow") != tc.wantAllow {
					t.Errorf("Allow = %q, want %q", header.Get("Allow"), tc.wantAllow)
				}
			})
		}
	}
}

func TestBuildMCPHandler_StatelessStillRequiresBearer(t *testing.T) {
	h, err := buildMCPHandler(imcp.NewServer(imcp.Deps{ReadOnly: true}), "s3cret", false)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(h)
	defer ts.Close()

	status, _, body := rawMCPRequest(t, ts.URL, http.MethodPost, listGitForgesCall,
		map[string]string{"Mcp-Session-Id": "never-issued"})
	if status != http.StatusUnauthorized {
		t.Fatalf("session id without bearer: status = %d, want 401 (body %q)", status, body)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./cmd/bridge -run 'TestBuildMCPHandler_Stateless' -v`

Expected: FAIL. Verified against the current stateful handler — in both `no-auth` and `bearer` modes:
- `…/tools/call without initialize or session id` — `Content-Type = "text/event-stream", want "application/json"`, and the body is a JSON-RPC error `method "tools/call" is invalid during initialization`, not a result
- `…/made-up session id is not rejected` — `status = 404, want 200 (body "session not found\n")`
- `…/initialize answers with json` — `Content-Type = "text/event-stream", want "application/json"`
- `…/GET offers no stream` — `status = 400, want 405`
- `…/DELETE on shutdown is a no-op` — `status = 404, want 204`
- `…/initialized notification is accepted` and `TestBuildMCPHandler_StatelessStillRequiresBearer` already PASS — they are regression guards, not red tests.

- [ ] **Step 3: Write the minimal implementation**

In `cmd/bridge/mcp.go`, add above `buildMCPHandler`:

```go
// newStreamableHandler mounts srv on a stateless Streamable HTTP handler that
// answers with plain JSON. No tool makes server→client requests or streams
// progress, so a protocol session buys nothing — and an in-memory one is lost
// on every restart, leaving clients with 404s on a dead Mcp-Session-Id. Both
// auth modes build their transport here so they cannot drift.
func newStreamableHandler(srv *sdkmcp.Server) http.Handler {
	return sdkmcp.NewStreamableHTTPHandler(
		func(*http.Request) *sdkmcp.Server { return srv },
		&sdkmcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true},
	)
}
```

In `buildMCPHandler`, replace

```go
	streamable := sdkmcp.NewStreamableHTTPHandler(func(*http.Request) *sdkmcp.Server { return srv }, nil)
```

with

```go
	streamable := newStreamableHandler(srv)
```

In `buildOAuthHandler`, make the identical replacement of its
`streamable := sdkmcp.NewStreamableHTTPHandler(…, nil)` line with
`streamable := newStreamableHandler(srv)`.

After the edit, `grep -n NewStreamableHTTPHandler cmd/bridge/mcp.go` must show exactly one hit (inside `newStreamableHandler`).

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./cmd/bridge -run 'TestBuildMCPHandler|TestBuildOAuthHandler' -v`

Expected: PASS for all, including the unchanged `TestBuildMCPHandler_ValidBearerListsTools` (real go-sdk client) and `TestBuildOAuthHandler_RoutesAndMiddlewarePlacement`.

- [ ] **Step 5: Full gate**

Run: `test -z "$(gofmt -l .)" && go vet ./... && golangci-lint run && go test -race ./...`
Expected: no output from gofmt, all packages `ok`.

- [ ] **Step 6: Commit**

```bash
git add cmd/bridge/mcp.go cmd/bridge/mcp_test.go
git commit -m "feat(mcp): serve the MCP endpoint statelessly with JSON responses

The stateful transport kept each client's session in memory, so a restart
left clients sending a dead Mcp-Session-Id and getting 404s. No tool needs
server-to-client requests or streaming, so the session bought nothing.

Refs #313"
```

---

### Task 2: Document the stateless transport

**Files:**
- Modify: `cmd/bridge/mcp.go` — the `WriteTimeout` comment in `runMCPServe`
- Modify: `docs/mcp-cheatsheet.md` — the "Running it long-term" paragraph (the sentence starting "Since `WriteTimeout` is intentionally unset (SSE streams are long-lived)")
- Modify: `CHANGELOG.md` — `## [Unreleased]`

**Interfaces:**
- Consumes: Task 1's behaviour (stateless + JSON). No code interfaces.
- Produces: nothing consumed later.

- [ ] **Step 1: Fix the `WriteTimeout` comment**

In `runMCPServe`, replace

```go
		// WriteTimeout is intentionally 0: SSE connections are long-lived streams
		// and a write deadline would terminate them prematurely.
```

with

```go
		// WriteTimeout is intentionally 0: responses are single JSON bodies, but
		// one tool call (cross_forge_status, list_repos across many owners) can
		// legitimately run long, and a write deadline would cut it off mid-reply.
```

- [ ] **Step 2: Update the cheatsheet**

In `docs/mcp-cheatsheet.md` ("Running it long-term"), replace these two lines

```markdown
`WriteTimeout` is intentionally unset (SSE streams are long-lived), don't put
a strict reverse-proxy timeout in front of it either.
```

with

```markdown
`WriteTimeout` is intentionally unset (a single tool call can run long), don't
put a strict reverse-proxy timeout in front of it either.

### Stateless transport

The endpoint runs the Streamable HTTP transport **stateless, with JSON
responses**: every `POST` is a self-contained JSON-RPC round trip answered with
`application/json`. There is no server-side MCP session — an `Mcp-Session-Id`
header is accepted but not validated, so clients keep working across a server
restart without re-initialising. `GET` (a standalone SSE stream) answers `405`
with `Allow: POST`; `DELETE` answers `204`. Bearer auth still runs on every
request.
```

- [ ] **Step 3: CHANGELOG entry**

Under `## [Unreleased]`, append to its existing `### Changed` section (there is one today; if it is gone, create it after `### Added` and before `### Fixed`):

```markdown
- `bridge mcp serve` runs the Streamable HTTP transport stateless with JSON
  responses: no in-memory MCP session, so clients survive a server restart
  instead of getting 404 on a stale `Mcp-Session-Id`; `GET` now answers 405
  (#313)
```

- [ ] **Step 4: Full gate**

Run: `test -z "$(gofmt -l .)" && go vet ./... && golangci-lint run && go test -race ./...`
Expected: clean, all `ok`.

- [ ] **Step 5: Commit**

```bash
git add cmd/bridge/mcp.go docs/mcp-cheatsheet.md CHANGELOG.md
git commit -m "docs(mcp): document the stateless transport

Closes #313"
```
