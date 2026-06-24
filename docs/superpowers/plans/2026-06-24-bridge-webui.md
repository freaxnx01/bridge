# Bridge WebUI Implementation Plan — Part 1: Backend + Scaffold

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Deliver `bridge serve` — a Go HTTP server that exposes a REST API + SSE stream backed by existing `internal/` packages, embeds a compiled Svelte SPA, and serves both from a single binary.

**Architecture:** A new `internal/web` package holds the SSE hub and HTTP server (with `//go:embed dist`). A new `internal/api` package holds thin HTTP handlers that delegate to existing packages (`internal/overview`, `internal/capture`, `internal/remote`, forge clients). `cmd/bridge/serve.go` wires them via the same helper functions used by `nav.go`. The Svelte project lives in `web/`; its compiled output goes to `internal/web/dist/` so the embed is co-located with the server code.

**Tech Stack:** Go stdlib `net/http` (Go 1.22+ ServeMux), `//go:embed`, Svelte 5, Vite 5, Vitest 2, `@testing-library/svelte` 5.

## Global Constraints

- No new Go module dependencies — stdlib only for the server layer
- Go tests: table-driven, hand-rolled fakes, `go test -race ./...`
- Never call `os.Exit` below `cmd/bridge/serve.go`
- `WriteTimeout: 0` on the HTTP server (SSE streams need no write deadline); document this in a comment
- Svelte scaffold uses Svelte 5 (runes available but not required in scaffold)
- `internal/web/dist/` is gitignored except `internal/web/dist/placeholder` (keeps `//go:embed dist` compiling before first `npm run build`)
- This is **Plan 1 of 2** — actual Svelte UI components are Plan 2, written after `/ui-brainstorm` → `/ui-flow`

---

## File Map

| File | Action | Purpose |
|---|---|---|
| `internal/web/hub.go` | Create | SSE hub: Event type, Hub struct, Run/Broadcast/Subscribe/Unsubscribe |
| `internal/web/hub_test.go` | Create | Hub unit tests |
| `internal/web/server.go` | Create | HTTP server, route registration, `//go:embed dist`, SPA fallback |
| `internal/web/server_test.go` | Create | Server routing smoke tests |
| `internal/web/dist/placeholder` | Create | Empty file so `//go:embed dist` compiles before first Svelte build |
| `internal/api/errors.go` | Create | `writeJSON`, `writeError`, `httpStatus` helpers |
| `internal/api/errors_test.go` | Create | Helper tests |
| `internal/api/overview.go` | Create | `GET /api/overview` handler (`OverviewHandler`) |
| `internal/api/overview_test.go` | Create | Overview handler tests |
| `internal/api/repos.go` | Create | `GET /api/repos`, `GET /api/repos/{owner}/{name}`, `POST /api/repos` |
| `internal/api/repos_test.go` | Create | Repos handler tests |
| `internal/api/capture.go` | Create | `POST /api/capture/idea`, `POST /api/capture/issue` |
| `internal/api/capture_test.go` | Create | Capture handler tests |
| `cmd/bridge/serve.go` | Create | Cobra `serve` subcommand; wires all handlers |
| `web/package.json` | Create | Svelte/Vite/Vitest deps |
| `web/vite.config.js` | Create | Vite config; outDir → `../internal/web/dist`; proxy `/api` → Go server |
| `web/svelte.config.js` | Create | Svelte plugin config |
| `web/src/app.html` | Create | HTML shell |
| `web/src/App.svelte` | Create | Root component (placeholder) |
| `web/src/lib/api.js` | Create | `get(path)` / `post(path, body)` fetch helpers |
| `web/src/lib/stores/sse.js` | Create | `createSseStore` — wraps EventSource, auto-reconnects |
| `web/src/lib/stores/overview.js` | Create | `overview` writable store, refreshes on SSE |
| `web/src/lib/stores/repos.js` | Create | `repos` writable store, refreshes on SSE |
| `web/src/lib/stores/sse.test.js` | Create | Vitest SSE store test |
| `web/.gitignore` | Create | Ignore `node_modules/`, `dist/` (managed at internal/web/dist) |
| `justfile` | Modify | Add `web-build` recipe; update `build` to run `web-build` first |
| `.gitignore` | Modify | Add `internal/web/dist/*` + `!internal/web/dist/placeholder` |

---

### Task 1: SSE Hub

**Files:**
- Create: `internal/web/hub.go`
- Create: `internal/web/hub_test.go`

**Interfaces:**
- Produces: `Event{Type string, Data any}`, `NewHub() *Hub`, `(*Hub).Run(ctx)`, `(*Hub).Broadcast(Event)`, `(*Hub).Subscribe() chan []byte`, `(*Hub).Unsubscribe(chan []byte)`

- [ ] **Step 1: Write the failing test**

```go
// internal/web/hub_test.go
package web

import (
	"bytes"
	"context"
	"testing"
	"time"
)

func TestHub_BroadcastReachesAllClients(t *testing.T) {
	t.Parallel()
	hub := NewHub()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go hub.Run(ctx)

	ch1 := hub.Subscribe()
	ch2 := hub.Subscribe()

	hub.Broadcast(Event{Type: "repo-updated", Data: map[string]string{"name": "foo"}})

	for _, ch := range []chan []byte{ch1, ch2} {
		select {
		case msg := <-ch:
			if !bytes.Contains(msg, []byte("repo-updated")) {
				t.Errorf("event missing type: %s", msg)
			}
		case <-time.After(time.Second):
			t.Fatal("timeout waiting for event")
		}
	}
}

func TestHub_UnsubscribeClosesChannel(t *testing.T) {
	t.Parallel()
	hub := NewHub()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go hub.Run(ctx)

	ch := hub.Subscribe()
	hub.Unsubscribe(ch)

	_, ok := <-ch
	if ok {
		t.Error("channel should be closed after Unsubscribe")
	}
}

func TestHub_BroadcastIsSSEFormat(t *testing.T) {
	t.Parallel()
	hub := NewHub()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go hub.Run(ctx)

	ch := hub.Subscribe()
	hub.Broadcast(Event{Type: "test"})

	select {
	case msg := <-ch:
		if !bytes.HasPrefix(msg, []byte("data: ")) {
			t.Errorf("SSE message must start with 'data: ', got: %s", msg)
		}
		if !bytes.HasSuffix(msg, []byte("\n\n")) {
			t.Errorf("SSE message must end with double newline, got: %q", msg)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout")
	}
}
```

- [ ] **Step 2: Run test — expect compile error (Hub not defined)**

```
go test ./internal/web/...
```

- [ ] **Step 3: Implement**

```go
// internal/web/hub.go
package web

import (
	"context"
	"encoding/json"
)

// Event is an SSE event broadcast to all connected clients.
type Event struct {
	Type string `json:"type"`
	Data any    `json:"data,omitempty"`
}

// Hub manages SSE client channels. Call Run in a goroutine before Subscribe.
type Hub struct {
	broadcast  chan Event
	register   chan chan []byte
	unregister chan chan []byte
	clients    map[chan []byte]struct{}
}

// NewHub returns a Hub ready to Run.
func NewHub() *Hub {
	return &Hub{
		broadcast:  make(chan Event, 16),
		register:   make(chan chan []byte),
		unregister: make(chan chan []byte),
		clients:    make(map[chan []byte]struct{}),
	}
}

// Run processes hub events until ctx is cancelled. Call in a dedicated goroutine.
func (h *Hub) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case ch := <-h.register:
			h.clients[ch] = struct{}{}
		case ch := <-h.unregister:
			delete(h.clients, ch)
			close(ch)
		case ev := <-h.broadcast:
			b, _ := json.Marshal(ev)
			msg := append([]byte("data: "), b...)
			msg = append(msg, '\n', '\n')
			for ch := range h.clients {
				select {
				case ch <- msg:
				default: // slow client; drop rather than block
				}
			}
		}
	}
}

// Broadcast sends ev to all connected clients. Non-blocking; drops if the
// internal channel is full (capacity 16).
func (h *Hub) Broadcast(ev Event) {
	select {
	case h.broadcast <- ev:
	default:
	}
}

// Subscribe registers a new SSE client and returns its receive channel.
// The channel is closed by the hub when Unsubscribe is called.
func (h *Hub) Subscribe() chan []byte {
	ch := make(chan []byte, 8)
	h.register <- ch
	return ch
}

// Unsubscribe removes the client and closes its channel.
func (h *Hub) Unsubscribe(ch chan []byte) {
	h.unregister <- ch
}
```

- [ ] **Step 4: Run tests — expect PASS**

```
go test -race ./internal/web/...
```

- [ ] **Step 5: Commit**

```bash
git add internal/web/hub.go internal/web/hub_test.go
git commit -m "feat(web): SSE hub — broadcast/subscribe/unsubscribe"
```

---

### Task 2: API helpers

**Files:**
- Create: `internal/api/errors.go`
- Create: `internal/api/errors_test.go`

**Interfaces:**
- Produces: `writeJSON(w, v)`, `writeError(w, status, msg)`, `httpStatus(err) int`

- [ ] **Step 1: Write the failing test**

```go
// internal/api/errors_test.go
package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/freaxnx01/bridge/internal/forge"
)

func TestWriteError_SetsStatusAndBody(t *testing.T) {
	w := httptest.NewRecorder()
	writeError(w, http.StatusBadRequest, "bad input")
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
	if !strings.Contains(w.Body.String(), "bad input") {
		t.Errorf("body missing message: %s", w.Body.String())
	}
}

func TestHttpStatus_ErrRepoExists_Conflict(t *testing.T) {
	if got := httpStatus(forge.ErrRepoExists); got != http.StatusConflict {
		t.Errorf("httpStatus(ErrRepoExists) = %d, want 409", got)
	}
}

func TestHttpStatus_Generic_InternalServerError(t *testing.T) {
	if got := httpStatus(fmt.Errorf("boom")); got != http.StatusInternalServerError {
		t.Errorf("httpStatus(generic) = %d, want 500", got)
	}
}
```

- [ ] **Step 2: Run — expect compile error**

```
go test ./internal/api/...
```

- [ ] **Step 3: Implement**

```go
// internal/api/errors.go
package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/freaxnx01/bridge/internal/forge"
)

type errorResponse struct {
	Error string `json:"error"`
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v) //nolint:errcheck // best-effort; client disconnect is benign
}

func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(errorResponse{Error: msg}) //nolint:errcheck
}

func httpStatus(err error) int {
	if errors.Is(err, forge.ErrRepoExists) {
		return http.StatusConflict
	}
	return http.StatusInternalServerError
}
```

- [ ] **Step 4: Run — expect PASS**

```
go test -race ./internal/api/...
```

- [ ] **Step 5: Commit**

```bash
git add internal/api/errors.go internal/api/errors_test.go
git commit -m "feat(api): writeJSON/writeError/httpStatus helpers"
```

---

### Task 3: Overview API handler

**Files:**
- Create: `internal/api/overview.go`
- Create: `internal/api/overview_test.go`

**Interfaces:**
- Consumes: `overview.Snapshot`, `overview.RankedItem` (from `internal/overview`)
- Produces: `OverviewHandler{Build func(ctx) (overview.Snapshot, error)}`; implements `http.Handler`

- [ ] **Step 1: Write the failing test**

```go
// internal/api/overview_test.go
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/freaxnx01/bridge/internal/overview"
)

func TestOverviewHandler_ReturnsSnapshot(t *testing.T) {
	want := overview.Snapshot{
		Ranked: []overview.RankedItem{{Title: "fix bug", Score: 3.5}},
	}
	h := &OverviewHandler{
		Build: func(_ context.Context) (overview.Snapshot, error) { return want, nil },
	}
	r := httptest.NewRequest(http.MethodGet, "/api/overview", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var got overview.Snapshot
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Ranked) != 1 || got.Ranked[0].Title != "fix bug" {
		t.Errorf("got Ranked = %+v", got.Ranked)
	}
}

func TestOverviewHandler_BuildError_Returns500(t *testing.T) {
	h := &OverviewHandler{
		Build: func(_ context.Context) (overview.Snapshot, error) {
			return overview.Snapshot{}, fmt.Errorf("forge down")
		},
	}
	r := httptest.NewRequest(http.MethodGet, "/api/overview", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", w.Code)
	}
}

func TestOverviewHandler_WrongMethod_Returns405(t *testing.T) {
	h := &OverviewHandler{
		Build: func(_ context.Context) (overview.Snapshot, error) { return overview.Snapshot{}, nil },
	}
	r := httptest.NewRequest(http.MethodPost, "/api/overview", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405", w.Code)
	}
}
```

- [ ] **Step 2: Run — expect compile error**

```
go test ./internal/api/...
```

- [ ] **Step 3: Implement**

```go
// internal/api/overview.go
package api

import (
	"context"
	"net/http"

	"github.com/freaxnx01/bridge/internal/overview"
)

// OverviewHandler handles GET /api/overview.
type OverviewHandler struct {
	Build func(ctx context.Context) (overview.Snapshot, error)
}

func (h *OverviewHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	snap, err := h.Build(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, snap)
}
```

- [ ] **Step 4: Run — expect PASS**

```
go test -race ./internal/api/...
```

- [ ] **Step 5: Commit**

```bash
git add internal/api/overview.go internal/api/overview_test.go
git commit -m "feat(api): GET /api/overview handler"
```

---

### Task 4: Repos API handlers

**Files:**
- Create: `internal/api/repos.go`
- Create: `internal/api/repos_test.go`

**Interfaces:**
- Consumes: `core.Repo`, `core.Session`, `core.LiveSessions()`, `forge.Issue` (from `internal/core`, `internal/forge`)
- Produces: `RepoDetail{Repo, Sessions, Issues}`, `ReposHandler{Discover, Issues, Create, Notify}`; implements `http.Handler`

- [ ] **Step 1: Write the failing test**

```go
// internal/api/repos_test.go
package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/freaxnx01/bridge/internal/core"
	"github.com/freaxnx01/bridge/internal/forge"
)

func fakeRepos() []core.Repo {
	return []core.Repo{
		{Owner: "alice", Name: "myrepo", Forge: "github"},
	}
}

func TestReposHandler_List_ReturnsRepos(t *testing.T) {
	h := &ReposHandler{Discover: func() ([]core.Repo, error) { return fakeRepos(), nil }}
	r := httptest.NewRequest(http.MethodGet, "/api/repos", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var got []core.Repo
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got) != 1 || got[0].Name != "myrepo" {
		t.Errorf("got repos = %+v", got)
	}
}

func TestReposHandler_Detail_ReturnsRepoDetail(t *testing.T) {
	h := &ReposHandler{
		Discover: func() ([]core.Repo, error) { return fakeRepos(), nil },
		Issues: func(_ context.Context, _, _, _ string) ([]forge.Issue, error) {
			return []forge.Issue{{Title: "open bug"}}, nil
		},
	}
	r := httptest.NewRequest(http.MethodGet, "/api/repos/alice/myrepo", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var got RepoDetail
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Repo.Name != "myrepo" {
		t.Errorf("got repo name = %q", got.Repo.Name)
	}
	if len(got.Issues) != 1 || got.Issues[0].Title != "open bug" {
		t.Errorf("got issues = %+v", got.Issues)
	}
}

func TestReposHandler_Detail_NotFound_Returns404(t *testing.T) {
	h := &ReposHandler{Discover: func() ([]core.Repo, error) { return fakeRepos(), nil }}
	r := httptest.NewRequest(http.MethodGet, "/api/repos/nobody/nope", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", w.Code)
	}
}

func TestReposHandler_Create_Returns201(t *testing.T) {
	var notified string
	h := &ReposHandler{
		Discover: func() ([]core.Repo, error) { return fakeRepos(), nil },
		Create: func(_ context.Context, name, _ string, _ bool) (core.Repo, error) {
			return core.Repo{Name: name, Owner: "alice", Forge: "github"}, nil
		},
		Notify: func(eventType string, _ any) { notified = eventType },
	}
	body := strings.NewReader(`{"name":"newrepo","forge":"github","private":true}`)
	r := httptest.NewRequest(http.MethodPost, "/api/repos", body)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201", w.Code)
	}
	if notified != "overview-updated" {
		t.Errorf("Notify called with %q, want overview-updated", notified)
	}
}
```

- [ ] **Step 2: Run — expect compile error**

```
go test ./internal/api/...
```

- [ ] **Step 3: Implement**

```go
// internal/api/repos.go
package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/freaxnx01/bridge/internal/core"
	"github.com/freaxnx01/bridge/internal/forge"
)

// RepoDetail is the payload for GET /api/repos/{owner}/{name}.
type RepoDetail struct {
	Repo     core.Repo      `json:"repo"`
	Sessions []core.Session `json:"sessions"`
	Issues   []forge.Issue  `json:"issues"`
}

// ReposHandler handles /api/repos and /api/repos/{owner}/{name}.
type ReposHandler struct {
	Discover func() ([]core.Repo, error)
	Issues   func(ctx context.Context, forge, owner, repo string) ([]forge.Issue, error)
	Create   func(ctx context.Context, name, forgeName string, private bool) (core.Repo, error)
	Notify   func(eventType string, data any) // nil = no-op
}

func (h *ReposHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/repos")
	path = strings.TrimPrefix(path, "/")

	switch {
	case path == "" && r.Method == http.MethodGet:
		h.list(w, r)
	case path == "" && r.Method == http.MethodPost:
		h.create(w, r)
	case path == "":
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	default:
		parts := strings.SplitN(path, "/", 2)
		if len(parts) != 2 {
			writeError(w, http.StatusBadRequest, "path must be /api/repos/{owner}/{name}")
			return
		}
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		h.detail(w, r, parts[0], parts[1])
	}
}

func (h *ReposHandler) list(w http.ResponseWriter, r *http.Request) {
	repos, err := h.Discover()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, repos)
}

func (h *ReposHandler) detail(w http.ResponseWriter, r *http.Request, owner, name string) {
	repos, err := h.Discover()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	var repo *core.Repo
	for i := range repos {
		if strings.EqualFold(repos[i].Owner, owner) && strings.EqualFold(repos[i].Name, name) {
			repo = &repos[i]
			break
		}
	}
	if repo == nil {
		writeError(w, http.StatusNotFound, "repo not found")
		return
	}
	// Best-effort: empty if tmux is not running
	sessions, _ := core.LiveSessions()
	var repoSessions []core.Session
	for _, s := range sessions {
		if strings.Contains(s.TmuxName, repo.Name) {
			repoSessions = append(repoSessions, s)
		}
	}
	var issues []forge.Issue
	if h.Issues != nil {
		issues, _ = h.Issues(r.Context(), repo.Forge, repo.Owner, repo.Name)
	}
	writeJSON(w, RepoDetail{Repo: *repo, Sessions: repoSessions, Issues: issues})
}

type createRepoRequest struct {
	Name    string `json:"name"`
	Forge   string `json:"forge"`
	Private bool   `json:"private"`
}

func (h *ReposHandler) create(w http.ResponseWriter, r *http.Request) {
	if h.Create == nil {
		writeError(w, http.StatusNotImplemented, "create not configured")
		return
	}
	var req createRepoRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	repo, err := h.Create(r.Context(), req.Name, req.Forge, req.Private)
	if err != nil {
		writeError(w, httpStatus(err), err.Error())
		return
	}
	if h.Notify != nil {
		h.Notify("overview-updated", nil)
	}
	w.WriteHeader(http.StatusCreated)
	writeJSON(w, repo)
}
```

- [ ] **Step 4: Run — expect PASS**

```
go test -race ./internal/api/...
```

- [ ] **Step 5: Commit**

```bash
git add internal/api/repos.go internal/api/repos_test.go
git commit -m "feat(api): GET /api/repos, GET /api/repos/{owner}/{name}, POST /api/repos"
```

---

### Task 5: Capture API handlers

**Files:**
- Create: `internal/api/capture.go`
- Create: `internal/api/capture_test.go`

**Interfaces:**
- Consumes: `forge.Issue`
- Produces: `CaptureHandler{Idea, Issue, Notify}`; implements `http.Handler`

- [ ] **Step 1: Write the failing test**

```go
// internal/api/capture_test.go
package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/freaxnx01/bridge/internal/forge"
)

func TestCaptureHandler_Idea_Returns200(t *testing.T) {
	var notified string
	h := &CaptureHandler{
		Idea: func(_ context.Context, target, text string) (string, error) {
			return "https://github.com/alice/ideas/commit/abc", nil
		},
		Notify: func(et string, _ any) { notified = et },
	}
	body := strings.NewReader(`{"target":"ideas-lab","text":"great idea"}`)
	r := httptest.NewRequest(http.MethodPost, "/api/capture/idea", body)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if notified != "overview-updated" {
		t.Errorf("Notify called with %q, want overview-updated", notified)
	}
}

func TestCaptureHandler_Issue_Returns200(t *testing.T) {
	h := &CaptureHandler{
		Issue: func(_ context.Context, owner, repo, title string) (forge.Issue, error) {
			return forge.Issue{Title: title, URL: "https://github.com/alice/myrepo/issues/1"}, nil
		},
	}
	body := strings.NewReader(`{"owner":"alice","repo":"myrepo","title":"bug found"}`)
	r := httptest.NewRequest(http.MethodPost, "/api/capture/issue", body)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
}

func TestCaptureHandler_MissingFields_Returns400(t *testing.T) {
	h := &CaptureHandler{
		Idea: func(_ context.Context, _, _ string) (string, error) { return "", nil },
	}
	body := strings.NewReader(`{"target":""}`)
	r := httptest.NewRequest(http.MethodPost, "/api/capture/idea", body)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestCaptureHandler_UnknownKind_Returns404(t *testing.T) {
	h := &CaptureHandler{}
	r := httptest.NewRequest(http.MethodPost, "/api/capture/roadmap", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", w.Code)
	}
}
```

- [ ] **Step 2: Run — expect compile error**

```
go test ./internal/api/...
```

- [ ] **Step 3: Implement**

```go
// internal/api/capture.go
package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/freaxnx01/bridge/internal/forge"
)

// CaptureHandler handles POST /api/capture/idea and POST /api/capture/issue.
type CaptureHandler struct {
	Idea   func(ctx context.Context, target, text string) (string, error)
	Issue  func(ctx context.Context, owner, repo, title string) (forge.Issue, error)
	Notify func(eventType string, data any)
}

func (h *CaptureHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	kind := strings.TrimPrefix(r.URL.Path, "/api/capture/")
	switch kind {
	case "idea":
		h.captureIdea(w, r)
	case "issue":
		h.captureIssue(w, r)
	default:
		writeError(w, http.StatusNotFound, "unknown capture kind")
	}
}

type ideaRequest struct {
	Target string `json:"target"`
	Text   string `json:"text"`
}

func (h *CaptureHandler) captureIdea(w http.ResponseWriter, r *http.Request) {
	var req ideaRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.Target == "" || req.Text == "" {
		writeError(w, http.StatusBadRequest, "target and text are required")
		return
	}
	url, err := h.Idea(r.Context(), req.Target, req.Text)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if h.Notify != nil {
		h.Notify("overview-updated", nil)
	}
	writeJSON(w, map[string]string{"url": url})
}

type issueRequest struct {
	Owner string `json:"owner"`
	Repo  string `json:"repo"`
	Title string `json:"title"`
}

func (h *CaptureHandler) captureIssue(w http.ResponseWriter, r *http.Request) {
	var req issueRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.Owner == "" || req.Repo == "" || req.Title == "" {
		writeError(w, http.StatusBadRequest, "owner, repo, and title are required")
		return
	}
	issue, err := h.Issue(r.Context(), req.Owner, req.Repo, req.Title)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if h.Notify != nil {
		h.Notify("overview-updated", nil)
	}
	writeJSON(w, issue)
}
```

- [ ] **Step 4: Run — expect PASS**

```
go test -race ./internal/api/...
```

- [ ] **Step 5: Commit**

```bash
git add internal/api/capture.go internal/api/capture_test.go
git commit -m "feat(api): POST /api/capture/idea and /api/capture/issue"
```

---

### Task 6: HTTP server + SSE endpoint + embed placeholder

**Files:**
- Create: `internal/web/server.go`
- Create: `internal/web/server_test.go`
- Create: `internal/web/dist/placeholder` (empty file)

**Interfaces:**
- Consumes: `*Hub` (from this package), `*http.ServeMux` (API routes from serve.go)
- Produces: `NewServer(hub, apiMux) *Server`, `(*Server).Handler() http.Handler`, `(*Server).Hub() *Hub`

- [ ] **Step 1: Create the embed placeholder**

```bash
mkdir -p internal/web/dist
touch internal/web/dist/placeholder
```

- [ ] **Step 2: Write the failing test**

```go
// internal/web/server_test.go
package web

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestServer_RoutesAPIRequest(t *testing.T) {
	t.Parallel()
	hub := NewHub()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go hub.Run(ctx)

	apiMux := http.NewServeMux()
	apiMux.HandleFunc("/api/overview", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	})

	s := NewServer(hub, apiMux)
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/overview")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

func TestServer_StaticFile_ServesPlaceholder(t *testing.T) {
	t.Parallel()
	hub := NewHub()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go hub.Run(ctx)

	s := NewServer(hub, http.NewServeMux())
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/placeholder")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	// placeholder is served (empty file = 200 with empty body)
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Errorf("status = %d, want 200, body: %s", resp.StatusCode, body)
	}
}
```

- [ ] **Step 3: Run — expect compile error**

```
go test ./internal/web/...
```

- [ ] **Step 4: Implement**

```go
// internal/web/server.go
package web

import (
	"embed"
	"fmt"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed dist
var staticFiles embed.FS

// Server wires the SSE Hub, API handlers, and embedded Svelte SPA.
type Server struct {
	hub     *Hub
	handler http.Handler
}

// NewServer creates a Server that routes:
//   - /api/events  → SSE hub (handled in-package; avoids circular import with internal/api)
//   - /api/*       → apiMux (registered by cmd/bridge/serve.go)
//   - /            → embedded Svelte SPA with client-side routing fallback
func NewServer(hub *Hub, apiMux *http.ServeMux) *Server {
	mux := http.NewServeMux()

	// SSE handled here to avoid circular import between internal/web and internal/api
	mux.HandleFunc("/api/events", func(w http.ResponseWriter, r *http.Request) {
		serveEvents(hub, w, r)
	})

	// Delegate all other /api/ routes to the caller-supplied mux
	mux.Handle("/api/", apiMux)

	// SPA: embedded Svelte assets with fallback to index.html for client-side routing
	dist, _ := fs.Sub(staticFiles, "dist")
	mux.Handle("/", spaHandler(dist))

	return &Server{hub: hub, handler: mux}
}

// Handler returns the http.Handler for use with http.Server.
func (s *Server) Handler() http.Handler { return s.handler }

// Hub exposes the SSE hub for broadcasting events from cmd/bridge/serve.go.
func (s *Server) Hub() *Hub { return s.hub }

// serveEvents upgrades the connection to an SSE stream and blocks until the
// client disconnects or the request context is cancelled.
func serveEvents(hub *Hub, w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	ch := hub.Subscribe()
	defer hub.Unsubscribe(ch)

	for {
		select {
		case <-r.Context().Done():
			return
		case msg, ok := <-ch:
			if !ok {
				return
			}
			fmt.Fprintf(w, "%s", msg)
			flusher.Flush()
		}
	}
}

// spaHandler serves files from dist; falls back to index.html for paths that
// don't match a file (enables Svelte client-side routing).
func spaHandler(dist fs.FS) http.Handler {
	fileServer := http.FileServer(http.FS(dist))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path == "" {
			path = "index.html"
		}
		f, err := dist.Open(path)
		if err == nil {
			f.Close()
			fileServer.ServeHTTP(w, r)
			return
		}
		// Unknown path: serve index.html so Svelte router handles it
		r2 := r.Clone(r.Context())
		r2.URL.Path = "/"
		fileServer.ServeHTTP(w, r2)
	})
}
```

- [ ] **Step 5: Run — expect PASS**

```
go test -race ./internal/web/...
```

- [ ] **Step 6: Commit**

```bash
git add internal/web/server.go internal/web/server_test.go internal/web/dist/placeholder
git commit -m "feat(web): HTTP server, SSE endpoint, embedded SPA with routing fallback"
```

---

### Task 7: `bridge serve` command

**Files:**
- Create: `cmd/bridge/serve.go`

**Interfaces:**
- Consumes (all from `cmd/bridge` main package): `discoverAllRoots()`, `reposRoots()`, `overviewRepos()`, `ideasLabDir()`, `roadmapFetcher()`, `fetchAllOpenIssues()`, `clientFor()`, `createAndClone()`, `resolveCaptureTarget()`, `resolveIssueTarget()`, `cacheRoot()`
- Consumes (from `internal/`): `api.OverviewHandler`, `api.ReposHandler`, `api.CaptureHandler`, `web.NewHub`, `web.NewServer`, `web.Event`

- [ ] **Step 1: Implement `cmd/bridge/serve.go`** (no test — wiring code; covered by handler tests above)

```go
// cmd/bridge/serve.go
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/freaxnx01/bridge/internal/api"
	"github.com/freaxnx01/bridge/internal/capture"
	"github.com/freaxnx01/bridge/internal/core"
	"github.com/freaxnx01/bridge/internal/forge"
	"github.com/freaxnx01/bridge/internal/overview"
	"github.com/freaxnx01/bridge/internal/remote"
	"github.com/freaxnx01/bridge/internal/web"
)

var servePort int
var serveHost string

func init() {
	rootCmd.AddCommand(newServeCmd())
}

func newServeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Start the Bridge WebUI HTTP server",
		RunE:  runServe,
	}
	cmd.Flags().IntVar(&servePort, "port", 7777, "port to listen on")
	cmd.Flags().StringVar(&serveHost, "host", "127.0.0.1", "host to bind to")
	return cmd
}

func runServe(cmd *cobra.Command, _ []string) error {
	hub := web.NewHub()
	ctx, cancel := context.WithCancel(cmd.Context())
	defer cancel()
	go hub.Run(ctx)

	notify := func(eventType string, data any) {
		hub.Broadcast(web.Event{Type: eventType, Data: data})
	}

	overviewH := &api.OverviewHandler{
		Build: func(c context.Context) (overview.Snapshot, error) {
			repos := overviewRepos()
			return overview.Build(c, overview.Config{
				Environment:  os.Getenv("BRIDGE_ENV"),
				Repos:        repos,
				IdeasLabDir:  ideasLabDir(),
				FetchIssues:  func(c context.Context) ([]overview.Issue, error) { return fetchAllOpenIssues(c, repos) },
				FetchRoadmap: roadmapFetcher(),
			})
		},
	}

	reposH := &api.ReposHandler{
		Discover: func() ([]core.Repo, error) { return discoverAllRoots() },
		Issues: func(c context.Context, forgeName, owner, repo string) ([]forge.Issue, error) {
			cl := clientFor(forgeName)
			if cl == nil {
				return nil, nil
			}
			return cl.ListOpenIssues(c, owner, repo)
		},
		Create: func(c context.Context, name, forgeName string, private bool) (core.Repo, error) {
			repo, _, err := createAndClone(c, name, forgeName, private)
			return repo, err
		},
		Notify: notify,
	}

	captureH := &api.CaptureHandler{
		Idea: func(c context.Context, target, text string) (string, error) {
			repos, _ := discoverAllRoots()
			tgt, err := resolveCaptureTarget(target, os.Getenv("BRIDGE_IDEAS_LAB_REPO"), repos)
			if err != nil {
				return "", err
			}
			tok, ok := remote.GitHubToken(reposRoots(), tgt.Owner)
			if !ok {
				return "", fmt.Errorf("no github token for owner %q", tgt.Owner)
			}
			return capture.CaptureIdea(c, forge.NewGithubClient(tok, os.Getenv("BRIDGE_GITHUB_API")), tgt, text, time.Now())
		},
		Issue: func(c context.Context, owner, repo, title string) (forge.Issue, error) {
			repos, _ := discoverAllRoots()
			tgt, err := resolveIssueTarget(owner+"/"+repo, repos)
			if err != nil {
				return forge.Issue{}, err
			}
			var creator capture.IssueCreator
			switch tgt.Forge {
			case "github":
				tok, ok := remote.GitHubToken(reposRoots(), tgt.Owner)
				if !ok {
					return forge.Issue{}, fmt.Errorf("no github token for owner %q", tgt.Owner)
				}
				creator = forge.NewGithubClient(tok, os.Getenv("BRIDGE_GITHUB_API"))
			case "forgejo":
				tok, ok := remote.ForgejoToken(reposRoots())
				if !ok {
					return forge.Issue{}, fmt.Errorf("no forgejo token")
				}
				creator = forge.NewForgejoClient(tok, os.Getenv("BRIDGE_FORGEJO_API"))
			default:
				return forge.Issue{}, fmt.Errorf("forge %q not supported for issue capture", tgt.Forge)
			}
			return capture.CaptureIssue(c, creator, tgt.Owner, tgt.Repo, title)
		},
		Notify: notify,
	}

	apiMux := http.NewServeMux()
	apiMux.Handle("/api/overview", overviewH)
	apiMux.Handle("/api/repos/", reposH)
	apiMux.Handle("/api/repos", reposH)
	apiMux.Handle("/api/capture/", captureH)

	// Broadcast overview-updated every 10s so connected clients stay live
	go func() {
		t := time.NewTicker(10 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				hub.Broadcast(web.Event{Type: "overview-updated"})
			}
		}
	}()

	addr := fmt.Sprintf("%s:%d", serveHost, servePort)
	srv := &http.Server{
		Addr:    addr,
		Handler: web.NewServer(hub, apiMux).Handler(),
		// WriteTimeout is intentionally 0: SSE connections are long-lived streams
		// and a write deadline would terminate them prematurely.
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	slog.Info("Bridge WebUI", "addr", "http://"+addr)

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-quit
		cancel()
		shutCtx, shutCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutCancel()
		srv.Shutdown(shutCtx) //nolint:errcheck
	}()

	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}
```

- [ ] **Step 2: Verify it compiles**

```
go build ./cmd/bridge/...
```

Expected: no errors.

- [ ] **Step 3: Smoke test**

```bash
bridge serve &
sleep 1
curl -s http://localhost:7777/api/repos | head -c 200
kill %1
```

Expected: JSON array of repos.

- [ ] **Step 4: Run full Go test suite**

```
go test -race ./...
```

Expected: all pass.

- [ ] **Step 5: Commit**

```bash
git add cmd/bridge/serve.go
git commit -m "feat(bridge): bridge serve command — REST API + SSE + embedded SPA"
```

---

### Task 8: Svelte scaffold

**Files:**
- Create: `web/package.json`
- Create: `web/vite.config.js`
- Create: `web/svelte.config.js`
- Create: `web/src/app.html`
- Create: `web/src/App.svelte`
- Create: `web/src/lib/api.js`
- Create: `web/src/lib/stores/sse.js`
- Create: `web/src/lib/stores/overview.js`
- Create: `web/src/lib/stores/repos.js`
- Create: `web/src/lib/stores/sse.test.js`
- Create: `web/.gitignore`

- [ ] **Step 1: Create `web/package.json`**

```json
{
  "name": "bridge-web",
  "private": true,
  "version": "0.0.1",
  "type": "module",
  "scripts": {
    "dev": "vite",
    "build": "vite build",
    "preview": "vite preview",
    "test": "vitest"
  },
  "devDependencies": {
    "@sveltejs/vite-plugin-svelte": "^4.0.0",
    "@testing-library/svelte": "^5.0.0",
    "jsdom": "^24.0.0",
    "svelte": "^5.0.0",
    "vite": "^5.0.0",
    "vitest": "^2.0.0"
  }
}
```

- [ ] **Step 2: Create `web/vite.config.js`**

```js
import { defineConfig } from 'vite'
import { svelte } from '@sveltejs/vite-plugin-svelte'

export default defineConfig({
  plugins: [svelte()],
  build: {
    // Output goes to internal/web/dist so go:embed picks it up.
    // Vite will NOT empty the outDir because it's outside the Vite root (web/).
    outDir: '../internal/web/dist',
  },
  server: {
    proxy: {
      '/api': 'http://localhost:7777',
    },
  },
  test: {
    environment: 'jsdom',
    globals: true,
  },
})
```

- [ ] **Step 3: Create `web/svelte.config.js`**

```js
import { vitePreprocess } from '@sveltejs/vite-plugin-svelte'

export default {
  preprocess: vitePreprocess(),
}
```

- [ ] **Step 4: Create `web/src/app.html`**

```html
<!doctype html>
<html lang="en">
  <head>
    <meta charset="UTF-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1.0" />
    <title>Bridge</title>
  </head>
  <body>
    <div id="app"></div>
    <script type="module" src="/src/main.js"></script>
  </body>
</html>
```

- [ ] **Step 5: Create `web/src/main.js`**

```js
import { mount } from 'svelte'
import App from './App.svelte'

const app = mount(App, { target: document.getElementById('app') })

export default app
```

- [ ] **Step 6: Create `web/src/App.svelte`** (placeholder — replaced in Plan 2)

```svelte
<script>
  import { onMount } from 'svelte';
  import { loadRepos, repos } from './lib/stores/repos.js';

  onMount(() => { loadRepos(); });
</script>

<main>
  <h1>Bridge WebUI</h1>
  <p>{$repos.length} repos loaded.</p>
</main>
```

- [ ] **Step 7: Create `web/src/lib/api.js`**

```js
export async function get(path) {
  const res = await fetch(path)
  if (!res.ok) throw new Error(`${path}: ${res.status} ${res.statusText}`)
  return res.json()
}

export async function post(path, body) {
  const res = await fetch(path, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  })
  if (!res.ok) {
    const err = await res.json().catch(() => ({ error: res.statusText }))
    throw new Error(err.error || res.statusText)
  }
  return res.json()
}
```

- [ ] **Step 8: Create `web/src/lib/stores/sse.js`**

```js
import { writable } from 'svelte/store'

// createSseStore wraps EventSource, auto-reconnects on error,
// and exposes the latest parsed event as a Svelte store.
export function createSseStore(url) {
  const { subscribe, set } = writable(null)
  let es = null

  function connect() {
    es = new EventSource(url)
    es.onmessage = (e) => {
      try { set(JSON.parse(e.data)) } catch {}
    }
    es.onerror = () => {
      es?.close()
      setTimeout(connect, 3000)
    }
  }

  if (typeof window !== 'undefined') connect()

  return { subscribe, disconnect: () => es?.close() }
}

export const sseEvent = createSseStore('/api/events')
```

- [ ] **Step 9: Create `web/src/lib/stores/overview.js`**

```js
import { writable } from 'svelte/store'
import { get as apiGet } from '../api.js'
import { sseEvent } from './sse.js'

export const overview = writable(null)

export async function loadOverview() {
  const data = await apiGet('/api/overview')
  overview.set(data)
}

sseEvent.subscribe(ev => {
  if (ev?.type === 'overview-updated') loadOverview()
})
```

- [ ] **Step 10: Create `web/src/lib/stores/repos.js`**

```js
import { writable } from 'svelte/store'
import { get as apiGet } from '../api.js'
import { sseEvent } from './sse.js'

export const repos = writable([])

export async function loadRepos() {
  const data = await apiGet('/api/repos')
  repos.set(data)
}

sseEvent.subscribe(ev => {
  if (ev?.type === 'overview-updated' || ev?.type === 'repo-updated') loadRepos()
})
```

- [ ] **Step 11: Create `web/src/lib/stores/sse.test.js`**

```js
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'

class MockEventSource {
  constructor(url) {
    this.url = url
    MockEventSource.lastInstance = this
  }
  set onmessage(fn) { this._onmessage = fn }
  set onerror(fn) { this._onerror = fn }
  close() {}
  trigger(data) { this._onmessage?.({ data: JSON.stringify(data) }) }
}

describe('createSseStore', () => {
  beforeEach(() => {
    vi.stubGlobal('EventSource', MockEventSource)
    vi.stubGlobal('window', {})
  })
  afterEach(() => vi.unstubAllGlobals())

  it('updates store value on valid message', async () => {
    const { createSseStore } = await import('./sse.js?t=' + Date.now())
    const store = createSseStore('/api/events')

    let received = null
    store.subscribe(v => { received = v })

    MockEventSource.lastInstance.trigger({ type: 'repo-updated', data: { name: 'foo' } })

    expect(received?.type).toBe('repo-updated')
    store.disconnect()
  })

  it('ignores invalid JSON without throwing', async () => {
    const { createSseStore } = await import('./sse.js?t=' + Date.now())
    const store = createSseStore('/api/events')
    expect(() => {
      MockEventSource.lastInstance._onmessage?.({ data: 'not-json' })
    }).not.toThrow()
    store.disconnect()
  })
})
```

- [ ] **Step 12: Create `web/.gitignore`**

```
node_modules/
# dist is managed at internal/web/dist — not here
```

- [ ] **Step 13: Install dependencies and verify build**

```bash
cd web && npm install
npm run build
cd ..
```

Expected: `internal/web/dist/` now has `index.html` and `assets/`.

- [ ] **Step 14: Run Svelte tests**

```bash
cd web && npm test -- --run
```

Expected: SSE store tests pass.

- [ ] **Step 15: Commit**

```bash
git add web/
git commit -m "feat(web): Svelte scaffold — Vite, stores, SSE, api helpers"
```

---

### Task 9: Build integration

**Files:**
- Modify: `justfile`
- Modify: `.gitignore`

- [ ] **Step 1: Read current justfile top** to confirm the pattern before editing

```bash
head -20 justfile
```

- [ ] **Step 2: Add `web-build` recipe and update `build`**

In `justfile`, add before the `build` recipe:

```just
# Build the Svelte frontend into internal/web/dist/.
web-build:
    cd web && npm ci && npm run build
```

Then update the Unix `build` recipe to run `web-build` first:

```just
[unix]
build: web-build
    make install
    bridge --version
```

- [ ] **Step 3: Update `.gitignore`**

Add at the end of `.gitignore`:

```
# Svelte build output — embedded into binary; committed placeholder keeps go:embed happy
internal/web/dist/*
!internal/web/dist/placeholder
```

- [ ] **Step 4: Verify full build from scratch**

```bash
just web-build
go build ./cmd/bridge/...
go test -race ./...
```

Expected: all pass; binary includes embedded SPA.

- [ ] **Step 5: Smoke test `bridge serve`**

```bash
bridge serve &
sleep 1
curl -s http://localhost:7777/api/repos | python3 -m json.tool | head -20
curl -s http://localhost:7777/api/overview | python3 -m json.tool | head -20
curl -s http://localhost:7777/ | head -5   # should return index.html
kill %1
```

- [ ] **Step 6: Commit**

```bash
git add justfile .gitignore
git commit -m "build: web-build recipe; embed dist into bridge binary"
```

---

## Next: Plan 2

Plan 2 covers the actual Svelte UI components (Overview page with Radar + Word Cloud, Repo dashboard, CreateRepo modal, CaptureModal). It is written **after** running:

1. `/ui-brainstorm` → ASCII wireframes approved
2. `/ui-flow` → Mermaid state diagrams approved

Do not implement Svelte page/component code before those gates.
