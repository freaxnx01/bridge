# Merge Card Diff Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A token-guarded, lazy, read-only per-file diff behind `[diff ▸]` on the #382 merge card.

**Architecture:** `forge.PRFile` gains `Status` and `Patch`; #382's evidence drops `Patch` so the open inbox carries no code. A new `GET /api/inbox/diff` (bearer only) checks the issue is a current merge card, reads the PR files through a small LRU keyed by `(repo, pr, headSha)`, and returns them. `MergeCard.svelte` loads it on tap via a new `authedGet`.

**Tech Stack:** Go stdlib, Svelte 5 + vitest.

**Spec:** [`docs/superpowers/specs/2026-11-25-merge-card-diff-design.md`](../specs/2026-11-25-merge-card-diff-design.md)

**Prerequisite:** #382 merged (`Collector.MergeCard`, `MergeEvidence`, `MergeCard.svelte`, `forge.ListPullRequestFiles` callers). Start from `main` after it lands.

## Global Constraints

- Route `GET /api/inbox/diff?repo=<owner/name>&number=<issue>`; guard = `requireTokenConfigured(apiToken, requireBearer(apiToken, …))`, **no** `requireSameOrigin`.
- `409 {"error":"not on the inbox"}` unless `Collector.MergeCard(owner, name, number)` returns a card.
- LRU of **32** entries keyed `(repo, pr, headSha)`; no new Go module.
- Response JSON: `{"pr","headSha","files":[{"path","status","additions","deletions","patch","patchMissing"}],"truncated"}`.
- `/api/inbox` (open) must never contain a `patch` field.
- Forge errors → `502 {"error":"forge call failed"}`, details only in `slog`.
- `gofmt`, `go vet`, `golangci-lint`, `go test -race ./...`, `npx vitest run`, `npx vite build` green.

## Review Focus

1. The open `GET /api/inbox` leaks a patch through `MergeEvidence.Files` — Task 1 test `TestBuildEvidence_FilesCarryNoPatch`.
2. Diff requested for an issue that is not a merge card (or a different repo) → 409, no forge call — Task 2 test `TestDiff_NotOnInbox_409NoForgeCall`.
3. Head SHA changes between two requests → second request misses the cache and re-reads — Task 2 test `TestDiffCache_NewHeadSHA_Refetches`.
4. A binary/huge file (no `patch` in GitHub's payload) renders a "no patch" row, not an empty `<pre>` — Task 3 test `shows no-patch rows`.
5. Missing/wrong token → 401 and the web clears the token and reopens the TokenDialog — Task 3 test `401 clears token`.

---

### Task 1: `PRFile.Status`/`Patch`, and keep patches out of the inbox

**Files:**
- Modify: `internal/forge/client.go` (`PRFile`), `internal/forge/github_pulls.go` (`ListPullRequestFiles`)
- Modify: `internal/cockpit/merge.go` (`BuildEvidence`)
- Test: `internal/forge/github_pulls_test.go`, `internal/cockpit/merge_test.go`

**Interfaces:**
- Produces: `PRFile{Path, Status string; Additions, Deletions int; Patch string \`json:"patch,omitempty"\`}` — `Status` from GitHub's `status`, `Patch` from `patch` (absent → "").

- [ ] **Step 1: Write the failing tests**

```go
func TestListPullRequestFiles_StatusAndPatch(t *testing.T) {
	// payload [{"filename":"a.go","status":"modified","additions":1,"deletions":0,"patch":"@@ -1 +1 @@\n+x"},
	//          {"filename":"img.png","status":"added","additions":0,"deletions":0}]
	// want [0].Status "modified", [0].Patch "@@ -1 +1 @@\n+x"; [1].Patch ""
}
func TestBuildEvidence_FilesCarryNoPatch(t *testing.T) {
	// files with Patch set → every e.Files[i].Patch == "" and json.Marshal(e) has no "patch" key
}
```

- [ ] **Step 2: Run** `go test ./internal/forge ./internal/cockpit -run 'StatusAndPatch|CarryNoPatch'` — Expected: FAIL.
- [ ] **Step 3: Implement.** `BuildEvidence` copies each file with `Patch` cleared.
- [ ] **Step 4: Run** `go test -race ./internal/forge ./internal/cockpit` — Expected: PASS.
- [ ] **Step 5: Commit** `feat(forge): read per-file status and patch of a PR (#383)`

---

### Task 2: `GET /api/inbox/diff` with an LRU per head SHA

**Files:**
- Create: `internal/api/diff.go`, `internal/api/diff_test.go`
- Create: `internal/cockpit/diffcache.go`, `internal/cockpit/diffcache_test.go`
- Modify: `cmd/bridge/serve.go` (route), `cmd/bridge/pipeline.go` or the #382 `cmd/bridge/merge.go` (closure)
- Modify: `docs/cockpit.md` (endpoint, auth note: first token-guarded GET, why)

**Interfaces:**
- Consumes: `Collector.MergeCard(owner, name string, number int) (InboxCard, bool)` (#382), `GithubClient.ListPullRequestFiles`, `GithubClient.GetPullRequest` (for `ChangedFiles`).
- Produces:

```go
// internal/cockpit/diffcache.go
type DiffFile struct {
	Path         string `json:"path"`
	Status       string `json:"status"`
	Additions    int    `json:"additions"`
	Deletions    int    `json:"deletions"`
	Patch        string `json:"patch"`
	PatchMissing bool   `json:"patchMissing"`
}
type Diff struct {
	PR        int        `json:"pr"`
	HeadSHA   string     `json:"headSha"`
	Files     []DiffFile `json:"files"`
	Truncated bool       `json:"truncated"`
}
type DiffKey struct{ Repo string; PR int; HeadSHA string }
type DiffCache struct{ /* mutex, cap, map + container/list */ }
func NewDiffCache(capacity int) *DiffCache
func (c *DiffCache) Get(ctx context.Context, k DiffKey, load func(ctx context.Context) (Diff, error)) (Diff, error) // errors are not cached

// internal/api/diff.go
type DiffHandler struct {
	Diff func(ctx context.Context, owner, repo string, number int) (cockpit.Diff, error)
}
func (h *DiffHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) // GET only
```

Behaviour:
- Handler: `GET` only (else 405 `Allow: GET`); `repo` must be `owner/name`, `number` > 0 → else 400. `Diff` errors: `cockpit.ErrNotCandidate` → 409 "not on the inbox"; `cockpit.ErrUnknownRepo` → 409 "not on the inbox"; other → 502 "forge call failed" + `slog.Error`.
- Closure in `cmd/bridge`: `card, ok := collector.MergeCard(owner, name, number)`; `!ok` → `cockpit.ErrNotCandidate`. Key `{owner/name, card.Merge.PR, card.Merge.HeadSHA}`; load = owner's GitHub client `ListPullRequestFiles` → `DiffFile`s with `PatchMissing = Patch == ""`; `Truncated = card.Merge.FilesTruncated`.
- One `NewDiffCache(32)` created in `runServe`, passed in.
- Route: `apiMux.Handle("/api/inbox/diff", requireTokenConfigured(apiToken, requireBearer(apiToken, diffH)))` registered before `/api/inbox/`.

- [ ] **Step 1: Write the failing tests**

```go
func TestDiffCache_HitDoesNotReload(t *testing.T)        // same key twice → load called once
func TestDiffCache_NewHeadSHA_Refetches(t *testing.T)    // key with other HeadSHA → load called again
func TestDiffCache_EvictsLeastRecent(t *testing.T)       // cap 2: a, b, get a, c → b evicted (b reloads), a cached
func TestDiffCache_ErrorNotCached(t *testing.T)          // load errors once then succeeds → second Get calls load
func TestDiff_StatusMapping_Table(t *testing.T) {
	// ok → 200 JSON with files; ErrNotCandidate → 409 {"error":"not on the inbox"};
	// other error → 502 {"error":"forge call failed"}; POST → 405; repo "x" → 400; number "0" → 400
}
func TestDiff_NotOnInbox_409NoForgeCall(t *testing.T)    // cmd/bridge closure with a collector fake lacking the card → load never called
```

- [ ] **Step 2: Run** `go test ./internal/cockpit ./internal/api ./cmd/bridge -run 'Diff'` — Expected: FAIL.
- [ ] **Step 3: Implement** and document in `docs/cockpit.md`.
- [ ] **Step 4: Run** `go test -race ./... && go vet ./... && golangci-lint run` — Expected: clean.
- [ ] **Step 5: Commit** `feat(serve): token-guarded GET /api/inbox/diff cached per head SHA (#383)`

---

### Task 3: Web — `[diff ▸]` in `MergeCard`

**Files:**
- Modify: `web/src/lib/auth.js` (add `authedGet`), `web/src/lib/api.js` (a `get` variant that sends a bearer and throws `{status, message}` like `post`)
- Create: `web/src/lib/components/DiffView.svelte`, `web/src/lib/components/DiffView.test.js`
- Modify: `web/src/lib/components/MergeCard.svelte`, `web/src/lib/components/MergeCard.test.js`

**Interfaces:**
- Produces: `authedGet(path, token)` — same 401 handling as `authedPost` (`clearToken()`, `requestToken({ rejected: true })`, rethrow). `DiffView` props `{ diff }` (the endpoint's JSON).

Behaviour:
- `[diff ▸]` → `ensureToken()` → `authedGet('/api/inbox/diff?repo=' + encodeURIComponent(card.repo) + '&number=' + card.number, token)`; states idle | loading | loaded | failed; loaded data kept while `card.merge.headSha` is unchanged, dropped when it changes.
- `DiffView`: one row per file `path +a −d` (status `added`/`removed`/`renamed` shown as a small tag), collapsed; tap toggles a `<pre>` of the patch; lines starting `+` / `-` / `@@` get classes `add` / `del` / `hunk`; `patchMissing` → "no patch (binary or too large)" + `Open PR ↗`; `truncated` → "first 100 files shown".
- Errors: 409 → "Diff unavailable: <message>"; 401 → "Token rejected"; other → "request failed".

- [ ] **Step 1: Write the failing tests**

```js
it('loads the diff on tap with the token', ...)   // authedGet called with '/api/inbox/diff?repo=o%2Fa&number=5'
it('renders collapsed rows and expands a patch', ...) // row "a.go +1 −0"; click → <pre> with a line having class "add"
it('shows no-patch rows', ...)                     // patchMissing → "no patch (binary or too large)", no <pre>
it('drops the diff when the head SHA changes', ...)// rerender card with new headSha → diff hidden, [diff ▸] again
it('401 clears token', ...)                        // auth.js unit: authedGet on 401 → clearToken + requestToken({rejected:true})
it('409 shows the reason', ...)
```

- [ ] **Step 2: Run** `cd web && npx vitest run src/lib/components/DiffView.test.js src/lib/components/MergeCard.test.js` — Expected: FAIL.
- [ ] **Step 3: Implement.** Tints via existing theme tokens; phone-first, `<pre>` scrolls horizontally inside the card, the page never does.
- [ ] **Step 4: Run** `cd web && npx vitest run && npx vite build` — Expected: PASS.
- [ ] **Step 5: Commit** `feat(webui): read-only per-file diff in the merge card (#383)`

---

### Task 4: Full verification

- [ ] **Step 1:** `test -z "$(gofmt -l .)" && go vet ./... && golangci-lint run && go test -race ./... && (cd web && npx vitest run && npx vite build)` — Expected: all clean.
- [ ] **Step 2:** `CHANGELOG.md` `[Unreleased]` → `Added`: "Cockpit merge card: read-only per-file diff behind the API token (#383)." Commit `docs(changelog): merge card diff (#383)`.
