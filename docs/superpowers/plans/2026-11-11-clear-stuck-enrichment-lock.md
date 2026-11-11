# Clear a Stuck Enrichment Lock Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Cards in the Pipeline board's "Stuck enriching" fold get **Clear lock** (remove `enrichment-ongoing`) and **Clear & re-enrich** (clear, then start the same session Enrich starts). Both are confirmed in `ConfirmDialog` and guarded like `POST /api/pipeline/{enrich,dispatch}`.

**Architecture:** Two new `POST` routes on `api.PipelineHandler` re-check that the issue is still a stuck `Running` enrichment card, then call a `ClearLock` func. `cmd/bridge` implements it with the per-owner forge client (the collector's resolver) through a consumer-side `lockClearer` interface, then posts an audit comment. The Svelte side adds a `StuckActions` component inside `RunningCard`; `Pipeline.svelte` is not touched.

**Tech Stack:** Go (module as pinned in `go.mod`), stdlib `testing` with table-driven `t.Run` subtests and hand-rolled fakes (no testify); Svelte 5 + Vitest + `@testing-library/svelte` (follow `web/src/routes/Pipeline.test.js`); `just test`, `golangci-lint`, `go test -race ./...`, `npm test` in `web/`.

**Spec:** `docs/superpowers/specs/2026-11-11-clear-stuck-enrichment-lock-design.md`

## Global Constraints

- **Dispatch order:** #385 is dispatched only after #366's PR has merged. #366 edits `internal/cockpit/collector.go`, `internal/cockpit/board.go`, `cmd/bridge/pipeline.go`, `web/src/routes/Pipeline.svelte` and `docs/cockpit.md`. Rebase onto main first: `Board.Skipped` / `.skipped` and `cockpit.ErrForgeUnsupported` will exist by then. Re-read `cmd/bridge/pipeline.go` and `docs/cockpit.md` before editing; the line numbers below are from before #366. Do not edit `board.go`, `collector.go` or `Pipeline.svelte`.
- Same guards as Enrich/Dispatch: bearer token, Origin allowlist (`guarded` chain, `cmd/bridge/serve.go:253`), 4 KiB body, same status-code table.
- Label writes use the per-owner client from `cachedClients(clientForMCP(reposRoots()))`, never `GH_TOKEN`.
- No new dependencies. No `//nolint`. No `panic`, no package-level mutable state.
- `StuckEnrichingAfter` (Go, 6 h) and `STUCK_ENRICHING_SECONDS` (`web/src/lib/running.js:5`) are the same number; each side has a test pinning it.
- Required after every Go task: `gofmt -l .` empty, `go vet ./...`, `golangci-lint run`, `go test -race ./...`. After the frontend task: `cd web && npm test`.
- Conventional Commits, scope `cockpit` (Task 3: `web`), reference `#385`.

## Review Focus

1. A request for an enrichment card younger than 6 h must answer `409 not stuck` and never call `ClearLock`. Test in Task 1.
2. `clear-reenrich` must not launch when the clear failed, and must still report the launch failure when only the launch failed. Tests in Task 1.
3. A client without `RemoveLabel` (Forgejo) must give `ErrUnsupportedForge`, not a panic or a silent success. Test in Task 2.

---

### Task 1: Handler routes and re-check

**Files:**
- Modify: `internal/api/pipeline.go`
- Test: `internal/api/pipeline_test.go`

**Interfaces:**
- Produces: `const StuckEnrichingAfter = 6 * time.Hour`; `var ErrUnsupportedForge`, `var ErrNoToken`; `PipelineHandler.ClearLock func(context.Context, LaunchRequest) error`; `PipelineHandler.Now func() time.Time`.

- [ ] **Step 1: Write the failing tests** (append to `internal/api/pipeline_test.go`; add `"time"` to its imports)

```go
var clearNow = time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)

type clearFixture struct {
	h       *PipelineHandler
	cleared *[]LaunchRequest
	enrich  *[]LaunchRequest
}

func newClearFixture() clearFixture {
	var cleared, enriched []LaunchRequest
	h := &PipelineHandler{
		Now: func() time.Time { return clearNow },
		Board: func() cockpit.Board {
			return cockpit.Board{Running: []cockpit.RunningCard{
				{Kind: cockpit.KindEnrichment, Repo: "o/r", Issue: 612, Since: clearNow.Add(-20 * 24 * time.Hour)},
				{Kind: cockpit.KindEnrichment, Repo: "o/r", Issue: 613, Since: clearNow.Add(-time.Hour)},
				{Kind: cockpit.KindEnrichment, Repo: "o/r", Issue: 614, Since: clearNow.Add(-StuckEnrichingAfter)},
				{Kind: cockpit.KindPipeline, Repo: "o/r", Issue: 615, Since: clearNow.Add(-48 * time.Hour)},
			}}
		},
		ClearLock: func(_ context.Context, req LaunchRequest) error {
			cleared = append(cleared, req)
			return nil
		},
		Enrich: func(_ context.Context, req LaunchRequest) (LaunchResult, error) {
			enriched = append(enriched, req)
			return LaunchResult{Slot: "enrich-612"}, nil
		},
	}
	return clearFixture{h, &cleared, &enriched}
}

func TestPipelineHandler_ClearLock_StuckCard_ClearsAndReports(t *testing.T) {
	f := newClearFixture()
	w := doReq(f.h, http.MethodPost, "/api/pipeline/clear-lock", `{"repo":"o/r","number":612}`)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"cleared":true`) {
		t.Fatalf("got %d %s", w.Code, w.Body)
	}
	if len(*f.cleared) != 1 || (*f.cleared)[0] != (LaunchRequest{Repo: "o/r", Number: 612}) || len(*f.enrich) != 0 {
		t.Fatalf("cleared=%v enriched=%v", *f.cleared, *f.enrich)
	}
}

func TestPipelineHandler_Clear_NotStuckOrNotOnBoard_409(t *testing.T) {
	cases := []struct {
		name, body, want string
	}{
		{"fresh lock", `{"repo":"o/r","number":613}`, "not stuck"},
		{"exactly at the threshold", `{"repo":"o/r","number":614}`, "not stuck"},
		{"pipeline card is not an enrichment", `{"repo":"o/r","number":615}`, "not on the board"},
		{"unknown issue", `{"repo":"o/r","number":1}`, "not on the board"},
	}
	for _, route := range []string{"/api/pipeline/clear-lock", "/api/pipeline/clear-reenrich"} {
		for _, tc := range cases {
			t.Run(route+" "+tc.name, func(t *testing.T) {
				f := newClearFixture()
				w := doReq(f.h, http.MethodPost, route, tc.body)
				if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), tc.want) {
					t.Fatalf("got %d %s", w.Code, w.Body)
				}
				if len(*f.cleared) != 0 || len(*f.enrich) != 0 {
					t.Fatalf("acted on a card that is not stuck: cleared=%v enriched=%v", *f.cleared, *f.enrich)
				}
			})
		}
	}
}

func TestPipelineHandler_Clear_ForgeErrors_MapToStatus(t *testing.T) {
	cases := []struct {
		name string
		err  error
		code int
	}{
		{"unsupported forge", ErrUnsupportedForge, http.StatusUnprocessableEntity},
		{"no token", fmt.Errorf("%w: github owner o", ErrNoToken), http.StatusUnprocessableEntity},
		{"unknown repo", ErrUnknownRepo, http.StatusConflict},
		{"forge call failed", errors.New("remove label: 403 Forbidden"), http.StatusBadGateway},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newClearFixture()
			f.h.ClearLock = func(context.Context, LaunchRequest) error { return tc.err }
			w := doReq(f.h, http.MethodPost, "/api/pipeline/clear-lock", `{"repo":"o/r","number":612}`)
			if w.Code != tc.code {
				t.Fatalf("status = %d, want %d (%s)", w.Code, tc.code, w.Body)
			}
			if strings.Contains(w.Body.String(), "403") {
				t.Fatalf("forge detail leaked into the body: %s", w.Body)
			}
		})
	}
}

func TestPipelineHandler_ClearReenrich_ClearsThenLaunches(t *testing.T) {
	f := newClearFixture()
	w := doReq(f.h, http.MethodPost, "/api/pipeline/clear-reenrich", `{"repo":"o/r","number":612}`)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"slot":"enrich-612"`) {
		t.Fatalf("got %d %s", w.Code, w.Body)
	}
	if len(*f.cleared) != 1 || len(*f.enrich) != 1 {
		t.Fatalf("cleared=%v enriched=%v", *f.cleared, *f.enrich)
	}
}

func TestPipelineHandler_ClearReenrich_ClearFails_DoesNotLaunch(t *testing.T) {
	f := newClearFixture()
	f.h.ClearLock = func(context.Context, LaunchRequest) error { return errors.New("boom") }
	w := doReq(f.h, http.MethodPost, "/api/pipeline/clear-reenrich", `{"repo":"o/r","number":612}`)
	if w.Code != http.StatusBadGateway || len(*f.enrich) != 0 {
		t.Fatalf("got %d, enriched=%v", w.Code, *f.enrich)
	}
}

func TestPipelineHandler_ClearReenrich_LaunchFails_500(t *testing.T) {
	f := newClearFixture()
	f.h.Enrich = func(context.Context, LaunchRequest) (LaunchResult, error) { return LaunchResult{}, errors.New("boom") }
	w := doReq(f.h, http.MethodPost, "/api/pipeline/clear-reenrich", `{"repo":"o/r","number":612}`)
	if w.Code != http.StatusInternalServerError || !strings.Contains(w.Body.String(), "launch failed") || len(*f.cleared) != 1 {
		t.Fatalf("got %d %s cleared=%v", w.Code, w.Body, *f.cleared)
	}
}

func TestPipelineHandler_Clear_SharesInflightGuardWithLaunches(t *testing.T) {
	f := newClearFixture()
	req := LaunchRequest{Repo: "o/r", Number: 612}
	f.h.inflight.Store(req, struct{}{})
	w := doReq(f.h, http.MethodPost, "/api/pipeline/clear-lock", `{"repo":"o/r","number":612}`)
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "in progress") || len(*f.cleared) != 0 {
		t.Fatalf("got %d %s", w.Code, w.Body)
	}
}

func TestPipelineHandler_Clear_BadInputAndMethod(t *testing.T) {
	f := newClearFixture()
	for _, route := range []string{"/api/pipeline/clear-lock", "/api/pipeline/clear-reenrich"} {
		if w := doReq(f.h, http.MethodGet, route, ""); w.Code != http.StatusMethodNotAllowed {
			t.Errorf("GET %s: status = %d, want 405", route, w.Code)
		}
		for _, body := range []string{`{`, `{"repo":"o/r","number":0}`, `{"repo":"nope","number":1}`} {
			if w := doReq(f.h, http.MethodPost, route, body); w.Code != http.StatusBadRequest {
				t.Errorf("POST %s %s: status = %d, want 400", route, body, w.Code)
			}
		}
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/api -run 'Clear' -count=1`
Expected: FAIL to compile (`undefined: StuckEnrichingAfter`, `ErrUnsupportedForge`, `ClearLock`).

- [ ] **Step 3: Implement** in `internal/api/pipeline.go`

Add `"time"` to the imports. Add near the other sentinels:

```go
// ErrUnsupportedForge is returned by ClearLock when the repo's forge client
// cannot remove a label (everything except GitHub today).
var ErrUnsupportedForge = errors.New("lock clearing is not supported for this forge")

// ErrNoToken is returned by ClearLock when no token resolves for the repo's owner.
var ErrNoToken = errors.New("no token for this owner")

// StuckEnrichingAfter is how long an enrichment-ongoing issue may stay
// unchanged before the board calls the lock stuck. Keep equal to
// STUCK_ENRICHING_SECONDS in web/src/lib/running.js.
const StuckEnrichingAfter = 6 * time.Hour
```

Add to `PipelineHandler` (before `inflight`):

```go
	ClearLock func(ctx context.Context, req LaunchRequest) error // removes enrichment-ongoing
	Now       func() time.Time                                   // nil means time.Now
```

Update the doc comment to list `clear-lock` and `clear-reenrich`. In `ServeHTTP` add both paths to the POST `case` list and, in the dispatching `switch`:

```go
	case "/api/pipeline/clear-lock":
		h.clearLock(w, r)
	case "/api/pipeline/clear-reenrich":
		h.clearAndReenrich(w, r)
```

Split `respond` into the in-flight guard and the launch response, then add the new handlers:

```go
func (h *PipelineHandler) respond(w http.ResponseWriter, r *http.Request,
	launch func(context.Context, LaunchRequest) (LaunchResult, error), req LaunchRequest) {
	h.exclusive(w, req, func() { h.writeLaunch(w, r, launch, req) })
}

// exclusive runs act unless another action for the same issue is in flight.
func (h *PipelineHandler) exclusive(w http.ResponseWriter, req LaunchRequest, act func()) {
	if _, busy := h.inflight.LoadOrStore(req, struct{}{}); busy {
		writeError(w, http.StatusConflict, "launch already in progress")
		return
	}
	defer h.inflight.Delete(req)
	act()
}

func (h *PipelineHandler) writeLaunch(w http.ResponseWriter, r *http.Request,
	launch func(context.Context, LaunchRequest) (LaunchResult, error), req LaunchRequest) {
	res, err := launch(r.Context(), req)
	switch {
	case errors.Is(err, ErrAmbiguousRepo):
		writeError(w, http.StatusConflict, ErrAmbiguousRepo.Error())
		return
	case errors.Is(err, ErrUnknownRepo):
		writeError(w, http.StatusConflict, "not on the board")
		return
	case err != nil:
		writeError(w, http.StatusInternalServerError, "launch failed")
		return
	}
	writeJSON(w, res)
}

func (h *PipelineHandler) clearLock(w http.ResponseWriter, r *http.Request) {
	req, ok := h.stuckRequest(w, r)
	if !ok {
		return
	}
	h.exclusive(w, req, func() {
		if h.clear(w, r, req) {
			writeJSON(w, map[string]bool{"cleared": true})
		}
	})
}

func (h *PipelineHandler) clearAndReenrich(w http.ResponseWriter, r *http.Request) {
	req, ok := h.stuckRequest(w, r)
	if !ok {
		return
	}
	h.exclusive(w, req, func() {
		if h.clear(w, r, req) {
			h.writeLaunch(w, r, h.Enrich, req)
		}
	})
}

// stuckRequest decodes the body and requires the issue to be a Running
// enrichment card unchanged for longer than StuckEnrichingAfter.
func (h *PipelineHandler) stuckRequest(w http.ResponseWriter, r *http.Request) (LaunchRequest, bool) {
	req, ok := decodeLaunchRequest(w, r)
	if !ok {
		return req, false
	}
	for _, c := range h.Board().Running {
		if c.Kind != cockpit.KindEnrichment || c.Repo != req.Repo || c.Issue != req.Number {
			continue
		}
		if h.now().Sub(c.Since) <= StuckEnrichingAfter {
			writeError(w, http.StatusConflict, "not stuck")
			return req, false
		}
		return req, true
	}
	writeError(w, http.StatusConflict, "not on the board")
	return req, false
}

// clear runs ClearLock and writes the error response itself; it reports whether the lock is gone.
func (h *PipelineHandler) clear(w http.ResponseWriter, r *http.Request, req LaunchRequest) bool {
	err := h.ClearLock(r.Context(), req)
	switch {
	case err == nil:
		return true
	case errors.Is(err, ErrUnsupportedForge):
		writeError(w, http.StatusUnprocessableEntity, ErrUnsupportedForge.Error())
	case errors.Is(err, ErrNoToken):
		writeError(w, http.StatusUnprocessableEntity, ErrNoToken.Error())
	case errors.Is(err, ErrUnknownRepo):
		writeError(w, http.StatusConflict, "not on the board")
	default:
		writeError(w, http.StatusBadGateway, "forge call failed")
	}
	return false
}

func (h *PipelineHandler) now() time.Time {
	if h.Now != nil {
		return h.Now()
	}
	return time.Now()
}
```

Also add a pin test for the constant to `pipeline_test.go`:

```go
func TestStuckEnrichingAfter_MatchesFrontendRule(t *testing.T) {
	if StuckEnrichingAfter != 6*time.Hour {
		t.Fatalf("StuckEnrichingAfter = %v; web/src/lib/running.js STUCK_ENRICHING_SECONDS must change with it", StuckEnrichingAfter)
	}
}
```

- [ ] **Step 4: Run to verify pass**

Run: `go test -race ./internal/api -count=1`
Expected: PASS, including every pre-existing pipeline test unmodified (the `respond` split must not change Enrich/Dispatch behaviour).

- [ ] **Step 5: Commit** `feat(cockpit): add clear-lock and clear-reenrich pipeline routes (#385)`

---

### Task 2: Forge write and wiring

**Files:**
- Create: `cmd/bridge/clearlock.go`
- Modify: `cmd/bridge/pipeline.go` (`pipelineHandler`), `cmd/bridge/serve.go` (client resolver reuse, two routes)
- Test: `cmd/bridge/clearlock_test.go`

**Interfaces:**
- Consumes: `api.ErrUnsupportedForge`, `api.ErrNoToken`, `api.ErrUnknownRepo`, `api.LaunchRequest`, `repoClient`, `errNoForgeClient`, `cockpit.LabelEnrichmentOngoing`.
- Produces: `func clearEnrichmentLock(ctx context.Context, resolve clientResolver, repos func() ([]core.Repo, error), now func() time.Time, req api.LaunchRequest) error`; `pipelineHandler(ctx, col, repos, resolve)`.

- [ ] **Step 1: Write the failing tests** (`cmd/bridge/clearlock_test.go`)

```go
package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/freaxnx01/bridge/internal/api"
	"github.com/freaxnx01/bridge/internal/core"
	"github.com/freaxnx01/bridge/internal/forge"
)

// baseClient satisfies forge.Client only, like ForgejoClient for this purpose.
type baseClient struct{}

func (baseClient) Name() string { return "fake" }
func (baseClient) ListRepos(context.Context, string) ([]forge.RepoRef, error) { return nil, nil }
func (baseClient) ListOpenIssues(context.Context, string, string) ([]forge.Issue, error) {
	return nil, nil
}

// labelClient also removes labels and comments, recording the call order.
type labelClient struct {
	baseClient
	calls      []string
	removeErr  error
	commentErr error
}

func (c *labelClient) RemoveLabel(_ context.Context, owner, repo string, n int, label string) error {
	c.calls = append(c.calls, "remove "+owner+"/"+repo+" "+label)
	return c.removeErr
}

func (c *labelClient) CommentIssue(_ context.Context, _, _ string, _ int, body string) (forge.Comment, error) {
	c.calls = append(c.calls, "comment "+body)
	return forge.Comment{}, c.commentErr
}

func clearRepos() ([]core.Repo, error) {
	return []core.Repo{{Forge: "github", Owner: "anim-bossinfo-ch", Name: "BI-ArchiveUploader"}}, nil
}

var clearReq = api.LaunchRequest{Repo: "anim-bossinfo-ch/BI-ArchiveUploader", Number: 612}
var clearAt = func() time.Time { return time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC) }

func TestClearEnrichmentLock_Success_RemovesLabelThenComments(t *testing.T) {
	c := &labelClient{}
	var gotForge, gotOwner string
	resolve := func(f, o string) forge.Client { gotForge, gotOwner = f, o; return c }
	if err := clearEnrichmentLock(context.Background(), resolve, clearRepos, clearAt, clearReq); err != nil {
		t.Fatal(err)
	}
	if gotForge != "github" || gotOwner != "anim-bossinfo-ch" {
		t.Fatalf("resolved %s/%s, want the repo owner's client", gotForge, gotOwner)
	}
	want := []string{
		"remove anim-bossinfo-ch/BI-ArchiveUploader enrichment-ongoing",
		"comment 🔓 Enrichment lock cleared from the cockpit at 2026-11-11T12:00:00Z",
	}
	if strings.Join(c.calls, "|") != strings.Join(want, "|") {
		t.Fatalf("calls = %q, want %q", c.calls, want)
	}
}

func TestClearEnrichmentLock_Failures(t *testing.T) {
	cases := []struct {
		name    string
		client  forge.Client
		repos   func() ([]core.Repo, error)
		want    error
		wantErr bool
	}{
		{"label removal fails", &labelClient{removeErr: errors.New("403")}, clearRepos, nil, true},
		{"client cannot remove labels", baseClient{}, clearRepos, api.ErrUnsupportedForge, true},
		{"no client for the owner", nil, clearRepos, api.ErrNoToken, true},
		{"repo not cloned", &labelClient{}, func() ([]core.Repo, error) { return nil, nil }, api.ErrUnknownRepo, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resolve := func(string, string) forge.Client { return tc.client }
			err := clearEnrichmentLock(context.Background(), resolve, tc.repos, clearAt, clearReq)
			if (err != nil) != tc.wantErr || (tc.want != nil && !errors.Is(err, tc.want)) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestClearEnrichmentLock_LabelRemovalFails_PostsNoComment(t *testing.T) {
	c := &labelClient{removeErr: errors.New("403")}
	_ = clearEnrichmentLock(context.Background(), func(string, string) forge.Client { return c }, clearRepos, clearAt, clearReq)
	if len(c.calls) != 1 {
		t.Fatalf("calls = %q, want only the failed removal", c.calls)
	}
}

func TestClearEnrichmentLock_CommentFails_StillSucceeds(t *testing.T) {
	c := &labelClient{commentErr: errors.New("boom")}
	if err := clearEnrichmentLock(context.Background(), func(string, string) forge.Client { return c }, clearRepos, clearAt, clearReq); err != nil {
		t.Fatalf("err = %v, the label is the state that matters", err)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./cmd/bridge -run ClearEnrichmentLock -count=1`
Expected: FAIL to compile (`undefined: clearEnrichmentLock`).

- [ ] **Step 3: Implement** `cmd/bridge/clearlock.go`

```go
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/freaxnx01/bridge/internal/api"
	"github.com/freaxnx01/bridge/internal/cockpit"
	"github.com/freaxnx01/bridge/internal/core"
	"github.com/freaxnx01/bridge/internal/forge"
)

// lockClearer is what clearing a lock needs from a forge client. GithubClient
// has both methods; ForgejoClient has no RemoveLabel, so it is refused.
type lockClearer interface {
	RemoveLabel(ctx context.Context, owner, repo string, number int, label string) error
	CommentIssue(ctx context.Context, owner, repo string, number int, body string) (forge.Comment, error)
}

// clearEnrichmentLock removes the enrichment-ongoing label from one issue with
// the repo owner's own client, then leaves an audit comment. The comment is
// best effort: the label is the state that matters.
func clearEnrichmentLock(ctx context.Context, resolve clientResolver, repos func() ([]core.Repo, error),
	now func() time.Time, req api.LaunchRequest) error {
	owner, name, _ := strings.Cut(req.Repo, "/")
	all, err := repos()
	if err != nil {
		return fmt.Errorf("list repos: %w", err)
	}
	i := slices.IndexFunc(all, func(r core.Repo) bool { return r.Owner == owner && r.Name == name })
	if i < 0 {
		return fmt.Errorf("%w: %s", api.ErrUnknownRepo, req.Repo)
	}
	client, err := repoClient(resolve, all[i])
	if errors.Is(err, errNoForgeClient) {
		return fmt.Errorf("%w: %v", api.ErrNoToken, err)
	}
	clearer, ok := client.(lockClearer)
	if !ok {
		return fmt.Errorf("%w: %s", api.ErrUnsupportedForge, all[i].Forge)
	}
	if err := clearer.RemoveLabel(ctx, owner, name, req.Number, cockpit.LabelEnrichmentOngoing); err != nil {
		return fmt.Errorf("remove %s from %s#%d: %w", cockpit.LabelEnrichmentOngoing, req.Repo, req.Number, err)
	}
	body := "🔓 Enrichment lock cleared from the cockpit at " + now().UTC().Format(time.RFC3339)
	if _, err := clearer.CommentIssue(ctx, owner, name, req.Number, body); err != nil {
		slog.Warn("audit comment after clearing lock failed", "repo", req.Repo, "number", req.Number, "error", err)
	}
	return nil
}
```

(`err` from `repoClient` that is not `errNoForgeClient` cannot occur today; if `client` is nil with another error the type assertion on nil fails and gives `ErrUnsupportedForge`. Keep it simple: do not add a branch.)

In `cmd/bridge/pipeline.go`: change the signature to `pipelineHandler(ctx, col, repos, resolve clientResolver)` and add to the returned handler:

```go
		ClearLock: func(c context.Context, req api.LaunchRequest) error {
			if err := clearEnrichmentLock(c, resolve, repos, time.Now, req); err != nil {
				slog.Error("clear enrichment lock failed", "repo", req.Repo, "number", req.Number, "error", err)
				return err
			}
			owner, name, _ := strings.Cut(req.Repo, "/")
			go col.CollectRepo(ctx, owner, name)
			return nil
		},
```

(`go col.CollectRepo` ends when it returns; it is bounded by the server `ctx`, like the launch path at `pipeline.go:181`.)

In `cmd/bridge/serve.go` (`:238-242`): hoist `clients := cachedClients(clientForMCP(reposRoots()))`, pass it to `cockpitSources(clients)` and `pipelineHandler(ctx, collector, reposWithMeta, clients)`, and register next to the existing guarded routes:

```go
	apiMux.Handle("/api/pipeline/clear-lock", guarded)
	apiMux.Handle("/api/pipeline/clear-reenrich", guarded)
```

Update any other caller of `pipelineHandler` (`grep -rn 'pipelineHandler(' cmd`), including tests.

- [ ] **Step 4: Add a wiring test** to `cmd/bridge/serve_test.go` (or the file that already tests the guarded routes; mirror it): `POST /api/pipeline/clear-lock` without a bearer token returns 401 and with a wrong Origin returns 403, exactly like `/api/pipeline/enrich`. Copy the existing enrich guard test and swap the path.

- [ ] **Step 5: Run to verify pass**

Run: `go test -race ./cmd/bridge ./internal/... -count=1`
Expected: PASS.

- [ ] **Step 6: Commit** `feat(cockpit): clear a stuck enrichment lock with the owner's forge client (#385)`

---

### Task 3: Frontend

**Files:**
- Modify: `web/src/lib/running.js`, `web/src/lib/components/ConfirmDialog.svelte`, `web/src/lib/components/RunningCard.svelte`
- Create: `web/src/lib/components/StuckActions.svelte`
- Test: `web/src/lib/running.test.js`, `web/src/lib/components/ConfirmDialog.test.js`, `web/src/lib/components/StuckActions.test.js`, `web/src/lib/components/RunningCard.test.js`

**Interfaces:**
- Produces: `isStuckEnrichment(card, now)` in `running.js`; `ConfirmDialog` actions `clear-lock` and `clear-reenrich`.

- [ ] **Step 1: Write the failing tests**

`running.test.js`:

```js
import { isStuckEnrichment, STUCK_ENRICHING_SECONDS } from './running.js'

describe('isStuckEnrichment', () => {
  const now = Date.parse('2026-11-11T12:00:00Z')
  const at = (s) => new Date(now - s * 1000).toISOString()
  it('pins the threshold at six hours, like api.StuckEnrichingAfter', () => {
    expect(STUCK_ENRICHING_SECONDS).toBe(6 * 3600)
  })
  it.each([
    ['older than 6 h', { kind: 'enrichment', since: at(6 * 3600 + 1) }, true],
    ['exactly 6 h', { kind: 'enrichment', since: at(6 * 3600) }, false],
    ['fresh', { kind: 'enrichment', since: at(60) }, false],
    ['not an enrichment', { kind: 'pipeline', since: at(99 * 3600) }, false],
    ['no timestamp', { kind: 'enrichment' }, false],
  ])('%s', (_name, card, want) => {
    expect(isStuckEnrichment(card, now)).toBe(want)
  })
})
```

(Merge the import into the file's existing import line; keep its `describe`/`it` imports.)

`ConfirmDialog.test.js` (append):

```js
  it('describes clear-lock as label-only, with no paid session', () => {
    render(ConfirmDialog, { repo: 'a/b', number: 612, action: 'clear-lock', onconfirm: vi.fn(), oncancel: vi.fn() })
    const dlg = screen.getByRole('dialog')
    expect(dlg.textContent).toContain('Clear lock a/b #612?')
    expect(dlg.textContent).toContain('enrichment-ongoing')
    expect(dlg.textContent).not.toContain('paid Claude session')
  })

  it('describes clear-reenrich as clearing and then starting a paid session', () => {
    render(ConfirmDialog, { repo: 'a/b', number: 612, action: 'clear-reenrich', onconfirm: vi.fn(), oncancel: vi.fn() })
    const dlg = screen.getByRole('dialog')
    expect(dlg.textContent).toContain('Clear & re-enrich a/b #612?')
    expect(dlg.textContent).toContain('enrich-612')
    expect(dlg.textContent).toContain('starts a paid Claude session')
  })
```

`StuckActions.test.js` (mock the SSE store like `IssueCard.test.js`; mock `../auth.js` to avoid the token dialog):

```js
import { describe, it, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/svelte'
vi.mock('../stores/sse.js', () => ({
  sseEvent: { subscribe: (fn) => { fn(null); return () => {} } },
}))
const authedPost = vi.fn()
vi.mock('../auth.js', () => ({ ensureToken: async () => 'tok', authedPost: (...a) => authedPost(...a) }))

import StuckActions from './StuckActions.svelte'

const card = { kind: 'enrichment', repo: 'o/r', issue: 612, title: 't', since: '2026-10-22T00:00:00Z' }

describe('StuckActions', () => {
  beforeEach(() => authedPost.mockReset())

  it('confirms before posting, then posts clear-lock with repo and number', async () => {
    authedPost.mockResolvedValue({ cleared: true })
    render(StuckActions, { card })
    await fireEvent.click(screen.getByRole('button', { name: 'Clear lock' }))
    expect(authedPost).not.toHaveBeenCalled()
    await fireEvent.click(screen.getByRole('button', { name: /Clear lock ▶/ }))
    expect(authedPost).toHaveBeenCalledWith('/api/pipeline/clear-lock', { repo: 'o/r', number: 612 }, 'tok')
    expect(await screen.findByText(/Lock cleared/)).toBeTruthy()
  })

  it('posts clear-reenrich and reports the started session', async () => {
    authedPost.mockResolvedValue({ slot: 'enrich-612', alreadyRunning: false })
    render(StuckActions, { card })
    await fireEvent.click(screen.getByRole('button', { name: 'Clear & re-enrich' }))
    await fireEvent.click(screen.getByRole('button', { name: /Clear & re-enrich ▶/ }))
    expect(authedPost).toHaveBeenCalledWith('/api/pipeline/clear-reenrich', { repo: 'o/r', number: 612 }, 'tok')
    expect(await screen.findByText(/enrich-612/)).toBeTruthy()
  })

  it('cancelling posts nothing', async () => {
    render(StuckActions, { card })
    await fireEvent.click(screen.getByRole('button', { name: 'Clear lock' }))
    await fireEvent.click(screen.getByRole('button', { name: 'Cancel' }))
    expect(authedPost).not.toHaveBeenCalled()
  })

  it.each([
    [409, 'not stuck', /not stuck/],
    [422, 'lock clearing is not supported for this forge', /not supported/],
    [502, 'forge call failed', /forge call failed/],
  ])('shows the failure for status %s', async (status, message, shown) => {
    authedPost.mockRejectedValue(Object.assign(new Error(message), { status }))
    render(StuckActions, { card })
    await fireEvent.click(screen.getByRole('button', { name: 'Clear lock' }))
    await fireEvent.click(screen.getByRole('button', { name: /Clear lock ▶/ }))
    expect((await screen.findByRole('alert')).textContent).toMatch(shown)
  })
})
```

`RunningCard.test.js` (append; the `StuckActions` import pulls the pipeline store, so add the same `vi.mock('../stores/sse.js', …)` at the top of this file):

```js
describe('RunningCard stuck actions', () => {
  const now = Date.parse('2026-11-11T12:00:00Z')
  const since = (s) => new Date(now - s * 1000).toISOString()

  it('offers both actions on an enrichment older than 6 h', () => {
    render(RunningCard, { card: { ...base, since: since(7 * 3600) }, now })
    expect(screen.getByRole('button', { name: 'Clear lock' })).toBeTruthy()
    expect(screen.getByRole('button', { name: 'Clear & re-enrich' })).toBeTruthy()
  })

  it('offers none on a fresh enrichment or a pipeline run', () => {
    render(RunningCard, { card: { ...base, since: since(600) }, now })
    expect(screen.queryByRole('button')).toBeNull()
  })
})
```

- [ ] **Step 2: Run to verify failure**

Run: `cd web && npx vitest run src/lib/running.test.js src/lib/components`
Expected: FAIL (`isStuckEnrichment` not exported, `StuckActions.svelte` missing, dialog copy absent).

- [ ] **Step 3: Implement**

`running.js`: add and use

```js
// isStuckEnrichment: an enrichment-ongoing label older than STUCK_ENRICHING_SECONDS.
export function isStuckEnrichment(card, now) {
  return card.kind === 'enrichment' && ageSeconds(card.since, now) > STUCK_ENRICHING_SECONDS
}
```

and in `groupOf` replace the enrichment line with `if (isStuckEnrichment(card, now)) return 'stuck'`.

`ConfirmDialog.svelte`: replace the `verb`/`slot`/`effect` derivations with a lookup (enrich and dispatch text unchanged):

```js
  const VERBS = { enrich: 'Enrich', dispatch: 'Dispatch', 'clear-lock': 'Clear lock', 'clear-reenrich': 'Clear & re-enrich' };
  const verb = $derived(VERBS[action] ?? 'Dispatch');
  const slot = $derived(
    action === 'clear-lock' ? null : `${action === 'dispatch' ? 'dispatch' : 'enrich'}-${number}`,
  );
  const effect = $derived(
    {
      enrich: `that runs /enrich ${number} --headless: writes the spec and implementation plan and updates the issue body.`,
      dispatch: `that runs /gh:implement ${number}: posts the implementation contract and adds ai-implement, which starts the pipeline.`,
      'clear-reenrich': `that runs /enrich ${number} --headless after removing the enrichment-ongoing label and posting a comment.`,
    }[action],
  );
```

Template paragraph:

```svelte
    {#if action === 'clear-lock'}
      <p>This removes the <code>enrichment-ongoing</code> label from the issue and posts a comment. No Claude session is started.</p>
    {:else}
      <p>
        This starts a paid Claude session in worktree <code>{slot}</code> {effect}
      </p>
    {/if}
```

`StuckActions.svelte`: follow `IssueCard.svelte`'s state machine (`idle | confirm | working | done | failed`; `ensureToken`, `authedPost`, `loadPipeline`). Routes: `{ 'clear-lock': '/api/pipeline/clear-lock', 'clear-reenrich': '/api/pipeline/clear-reenrich' }`; body `{ repo: card.repo, number: card.issue }`. Buttons labelled exactly `Clear lock` and `Clear & re-enrich` (disabled while `working`); renders `<ConfirmDialog repo={card.repo} number={card.issue} {action} …>` in the `confirm` state. On success: `done` with `Lock cleared` (clear-lock) or `Lock cleared · enrich started in {slot}` / `Lock cleared · enrich already running` (re-enrich), then `loadPipeline()`. On failure `failed` with a `role="alert"` line and a dismiss button: 401 `Token rejected`, 403 `Blocked: wrong origin`, 409 `Not actionable (${message})`, 422 the message, otherwise the message or `request failed`; reload the board on every failure except 401/403 (a failed re-enrich launch has already cleared the lock). Style with the same classes as `IssueCard.svelte`.

`RunningCard.svelte`: import `isStuckEnrichment` and `StuckActions`; after the link render `{#if isStuckEnrichment(card, now)}<StuckActions {card} />{/if}`.

- [ ] **Step 4: Run to verify pass**

Run: `cd web && npm test`
Expected: PASS, including the unchanged `Pipeline.test.js` stuck-fold test and existing `ConfirmDialog`/`IssueCard` tests.

- [ ] **Step 5: Commit** `feat(web): clear a stuck enrichment lock from the Stuck enriching fold (#385)`

---

### Task 4: Docs

**Files:**
- Modify: `docs/cockpit.md`, `CHANGELOG.md`

- [ ] **Step 1:** Re-read `docs/cockpit.md` after rebasing onto #366. In the Running row, after "folds into **Stuck enriching**", add: "(each card offers **Clear lock** and **Clear & re-enrich**)".
- [ ] **Step 2:** After the `enrich`/`dispatch` section add `### POST /api/pipeline/clear-lock and POST /api/pipeline/clear-reenrich`: same body (max 4 KiB); the issue must be a Running enrichment card unchanged for more than 6 h; success `200 {"cleared": true}` for clear-lock and the enrich `LaunchResult` for clear-reenrich; the status table is the enrich/dispatch one plus `409 not stuck`, `422 lock clearing is not supported for this forge` / `no token for this owner`, `502 forge call failed`. State that the label is removed with the repo owner's own token and that a `🔓 Enrichment lock cleared from the cockpit at <UTC>` comment is posted (best effort).
- [ ] **Step 3:** Under "What the buttons run" add: **Clear lock** removes `enrichment-ongoing` (no session); **Clear & re-enrich** does that, then runs the Enrich command.
- [ ] **Step 4:** Add under `[Unreleased]` / `Added` in `CHANGELOG.md`: `- Cockpit: **Clear lock** and **Clear & re-enrich** on Stuck-enriching cards; \`POST /api/pipeline/clear-lock\` and \`/api/pipeline/clear-reenrich\`. (#385)`
- [ ] **Step 5:** Run the full gate: `gofmt -l .`, `go vet ./...`, `golangci-lint run`, `go test -race ./...`, `cd web && npm test`. Commit `docs(cockpit): document clearing a stuck enrichment lock (#385)`.
