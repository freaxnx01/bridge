# Cockpit Merge Card Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A `merge` card in the cockpit inbox for agent PRs that wait for a human merge, with a guarded `POST /api/inbox/merge` that squash-merges pinned to the head SHA the card showed.

**Architecture:** Pure evidence logic (verdict parsing, checks state, eligibility) lives in a new `internal/cockpit/merge.go`. The collector gathers evidence per merge candidate on the issues tick (cached by head SHA) and `classify` turns `ai:done` + `ActionNone` + one trusted closing PR into a `merge` card. The POST handler re-reads live through a narrow interface, re-applies the same eligibility function, then merges and deletes the branch via two new `GithubClient` methods.

**Tech Stack:** Go stdlib (`net/http`, `encoding/json`, `regexp`), Svelte 5 + Vite + vitest in `web/`.

**Spec:** [`docs/superpowers/specs/2026-11-25-cockpit-merge-card-design.md`](../specs/2026-11-25-cockpit-merge-card-design.md)

## Global Constraints

- GitHub only; non-GitHub repos never produce merge cards (`classify` already rejects them).
- No new Go modules; stdlib `testing` + hand-rolled fakes; no testify.
- Trusted identities: env `BRIDGE_PIPELINE_BOTS`, comma-separated logins, default `github-actions[bot]`; resolved once in `cmd/bridge`, passed down explicitly.
- Merge method `squash`, always with `sha: <headSha>`. Never admin-merge, never retry with other parameters.
- Checks green = every check run `completed` with conclusion in {`success`,`neutral`,`skipped`} **and** every commit status `success`; zero of both = "no checks reported".
- Only `mergeable_state == "dirty"` blocks bridge-side; `unknown`, `blocked`, `behind` are left to GitHub.
- SHA comparison: exact, case-insensitive, full SHA.
- `GET /api/inbox` does no forge call; evidence comes from the collector snapshot.
- Forge error text goes to the log, never to the client, except GitHub's refusal message on merge (spec decision 7).
- `gofmt -l .` empty, `go vet ./...`, `golangci-lint run`, `go test -race ./...`, `cd web && npx vitest run && npx vite build` all green.

## Review Focus

1. A review comment by a non-trusted author carrying `verdict: approve` for the head SHA must not enable Merge — Task 2 test `TestReviewVerdict_UntrustedAuthor_Ignored`.
2. A marker whose JSON contains `-->` or is malformed must not panic and must read as "no marker", falling back to the `review-pr` form — Task 2 test `TestReviewVerdict_MalformedMarker_FallsBack`.
3. Head moves between collect and click → `409 head moved`, no merge call — Task 4 test `TestMerge_HeadMoved_409NoMerge`.
4. Merge succeeds but branch delete fails → `200` with `branchDeleted:false` — Task 4 test `TestMerge_DeleteFails_Still200`.
5. An `ai:done` issue that also carries `ai:failed`/blocked/parked keeps its existing card — Task 3 test `TestBuildInbox_DoneWithFailureLabel_NoMergeCard`.

---

### Task 1: Forge client — merge, delete branch, PR comments, head repo

**Files:**
- Modify: `internal/forge/client.go` (`PullRequest`), `internal/forge/github.go` (`ghPull`, `toPullRequest`)
- Modify: `internal/forge/github_pulls.go`
- Test: `internal/forge/github_pulls_test.go`

**Interfaces:**
- Produces:
  - `PullRequest.HeadRepo string` (`json:"head_repo,omitempty"`, from `head.repo.full_name`) and `PullRequest.BaseRepo string` (from `base.repo.full_name`).
  - `type APIError struct{ Status int; Message string }` with `Error() string` — returned by the two new write methods for any status ≥ 400; `Message` is GitHub's JSON `message` field (raw body if not JSON).
  - `func (c *GithubClient) MergePullRequest(ctx context.Context, owner, repo string, number int, sha, method string) error` → `PUT /repos/{o}/{r}/pulls/{n}/merge` body `{"merge_method":method,"sha":sha}`.
  - `func (c *GithubClient) DeleteBranch(ctx context.Context, owner, repo, ref string) error` → `DELETE /repos/{o}/{r}/git/refs/heads/{ref}` (ref path-escaped per segment; refs may contain `/`). `422` whose message contains "Reference does not exist" → `nil`.
  - `func (c *GithubClient) ListPRComments(ctx context.Context, owner, repo string, number int) ([]Comment, error)` — exported wrapper over `listIssueComments`.

- [ ] **Step 1: Write the failing tests** (httptest server, same style as existing `github_pulls_test.go`)

```go
func TestMergePullRequest_SendsSquashAndSHA(t *testing.T) {
	// server asserts: method PUT, path /repos/o/r/pulls/7/merge,
	// body {"merge_method":"squash","sha":"abc"}; answers 200 {"merged":true}
	// want err == nil
}
func TestMergePullRequest_Refused_ReturnsAPIError(t *testing.T) {
	// server answers 405 {"message":"Required status check \"build\" is expected."}
	var apiErr *APIError
	// want errors.As(err, &apiErr) && apiErr.Status == 405 &&
	//      apiErr.Message == `Required status check "build" is expected.`
}
func TestDeleteBranch_NestedRef_EscapesSegments(t *testing.T) {
	// ref "feat/382-merge": want request path /repos/o/r/git/refs/heads/feat/382-merge, method DELETE, 204 → nil
}
func TestDeleteBranch_AlreadyGone_Nil(t *testing.T) {
	// 422 {"message":"Reference does not exist"} → nil
}
func TestGetPullRequest_HeadAndBaseRepo(t *testing.T) {
	// payload head.repo.full_name "o/r", base.repo.full_name "o/r" → HeadRepo/BaseRepo "o/r"
}
```

- [ ] **Step 2: Run** `go test ./internal/forge -run 'Merge|DeleteBranch|HeadAndBaseRepo'` — Expected: FAIL (undefined).
- [ ] **Step 3: Implement** the fields, `APIError`, and the three methods. Add a private `do(ctx, method, path string, body any) error` that builds `APIError` on ≥ 400; do not change the existing `put`/`get` error format (other callers string-match nothing, but keep the diff surgical).
- [ ] **Step 4: Run** `go test -race ./internal/forge` — Expected: PASS.
- [ ] **Step 5: Commit** `feat(forge): merge a PR pinned to a SHA and delete its branch (#382)`

---

### Task 2: Merge evidence — verdict, checks, summary, eligibility (pure)

**Files:**
- Create: `internal/cockpit/merge.go`
- Test: `internal/cockpit/merge_test.go`

**Interfaces:**
- Consumes: `forge.Comment`, `forge.CheckRun`, `forge.CommitStatus`, `forge.PullRequest`, `forge.PRFile` (Task 1 fields).
- Produces:

```go
type ReviewVerdict struct {
	Verdict  string `json:"verdict"`  // approve | request_changes | block | review_failed
	HeadSHA  string `json:"headSha"`
	Current  bool   `json:"current"`
	Blocking *int   `json:"blocking,omitempty"`
	Nits     *int   `json:"nits,omitempty"`
	Source   string `json:"source"` // "marker" | "fallback"
}
type ChecksSummary struct {
	State  string `json:"state"` // green | running | failing | none
	Passed int    `json:"passed"`
	Total  int    `json:"total"`
}
type MergeEvidence struct {
	PR             int            `json:"pr"`
	PRURL          string         `json:"prUrl"`
	HeadSHA        string         `json:"headSha"`
	HeadRef        string         `json:"headRef"`
	BaseRef        string         `json:"baseRef"`
	Draft          bool           `json:"draft"`
	MergeableState string         `json:"mergeableState"`
	Summary        string         `json:"summary"`
	Review         *ReviewVerdict `json:"review"`
	Checks         ChecksSummary  `json:"checks"`
	Files          []forge.PRFile `json:"files"`
	FilesTruncated bool           `json:"filesTruncated"`
	Stale          bool           `json:"stale"` // last read failed; this is older evidence
	CanMerge       bool           `json:"canMerge"`
	Reason         string         `json:"reason"` // "" iff CanMerge
}
func LatestVerdict(comments []forge.Comment, headSHA string, bots []string) *ReviewVerdict
func SummarizeChecks(runs []forge.CheckRun, statuses []forge.CommitStatus) ChecksSummary
func SummarySection(body string) string
func ClosingPipelinePRs(prs []forge.PullRequest, issue int, bots []string) []forge.PullRequest
func BuildEvidence(pr forge.PullRequest, comments []forge.Comment, runs []forge.CheckRun, statuses []forge.CommitStatus, files []forge.PRFile, bots []string) MergeEvidence
func Eligibility(e MergeEvidence) (canMerge bool, reason string)
```

Rules (from the spec):
- `LatestVerdict`: only comments whose `Author` is in `bots`. Marker = last non-empty line matching `^<!-- ai-review: (\{.*\}) -->$`, JSON with `"v":1`; any other `v` or bad JSON → try fallback. Fallback = first line `^<!-- review-pr:([0-9a-fA-F]{7,64}) -->$` plus a line matching ``verdict: `(approve|request_changes|block|review_failed)` ``. Among parsed comments, prefer the **latest** (by `Created`, then slice order) whose SHA equals `headSHA` (`Current: true`); if none matches, return the latest parsed with `Current: false`; none parsed → `nil`.
- `ClosingPipelinePRs`: `dispatch.ClosesIssue(p.Body, issue)` and `p.Author` in `bots`.
- `SummarySection`: text under a `## Summary` heading up to the next `## ` heading, trimmed; "" when absent.
- `Eligibility` order, first hit wins: `Stale` → "forge read failed (evidence stale)"; `Draft` → "draft"; `MergeableState == "dirty"` → "conflicts"; checks `running` → "checks running", `failing` → "checks failing", `none` → "no checks reported"; `Review == nil` → "no review yet"; `!Review.Current` → "review on older commit"; `Verdict != "approve"` → "review: " + verdict; else `(true, "")`.
- `BuildEvidence` fills everything and sets `CanMerge, Reason = Eligibility(e)`; `FilesTruncated = pr.ChangedFiles > len(files)`.

- [ ] **Step 1: Write the failing tests** (table-driven)

```go
const head = "96fae5fd5d9aa4022a3b5a57a69b50510a563891"
var bots = []string{"github-actions[bot]", "my-app[bot]"}

func reviewFallback(sha, verdict, author string, at time.Time) forge.Comment {
	return forge.Comment{Author: author, Created: at,
		Body: "<!-- review-pr:" + sha + " -->\n## Automated review — verdict: `" + verdict + "`\n\n_Agent: claude · head: `" + sha + "`_"}
}
func reviewMarker(sha, verdict, author string, at time.Time) forge.Comment {
	return forge.Comment{Author: author, Created: at,
		Body: "<!-- review-pr:" + sha + " -->\n## Automated review — verdict: `" + verdict + "`\n<!-- ai-review: {\"v\":1,\"verdict\":\"" + verdict + "\",\"head_sha\":\"" + sha + "\",\"blocking\":0,\"nits\":2} -->"}
}

func TestReviewVerdict_Table(t *testing.T) {
	// cases (want Verdict, Current, Source, Blocking/Nits):
	// marker approve on head                      → approve, true, marker, 0, 2
	// fallback approve on head                    → approve, true, fallback, nil, nil
	// approve on older sha only                   → approve, false
	// older approve, newer request_changes on head → request_changes, true
	// re-review by second bot ("my-app[bot]")     → counted
	// uppercase SHA in comment                    → Current true
	// no review comments                          → nil
}
func TestReviewVerdict_UntrustedAuthor_Ignored(t *testing.T) {
	// reviewMarker(head,"approve","mallory",...) only → nil
}
func TestReviewVerdict_MalformedMarker_FallsBack(t *testing.T) {
	// body: fallback form with verdict approve + last line "<!-- ai-review: {\"v\":1,\"verdict\": --> -->"
	// → approve, Source "fallback"; marker with "v":2 → fallback too
}
func TestSummarizeChecks_Table(t *testing.T) {
	// all success/neutral/skipped + status success → green, passed==total
	// one in_progress → running; one failure → failing (failing beats running)
	// one status "pending" → running; status "failure"/"error" → failing
	// none → none, 0/0
}
func TestSummarySection_Table(t *testing.T) {
	// "## Summary\n\nCaps it.\n\n## Changes\n- x" → "Caps it."; no heading → ""
}
func TestEligibility_Table(t *testing.T) {
	// one row per reason in the order above, plus the all-clear row (true,"");
	// mergeableState "unknown","blocked","behind" with everything else green → (true,"")
}
func TestClosingPipelinePRs_FiltersAuthorAndCloses(t *testing.T) {}
```

- [ ] **Step 2: Run** `go test ./internal/cockpit -run 'Verdict|Checks|SummarySection|Eligibility|ClosingPipeline'` — Expected: FAIL.
- [ ] **Step 3: Implement** in `merge.go`; regexes as package-level `var` compiled once (immutable, allowed).
- [ ] **Step 4: Run** `go test -race ./internal/cockpit` — Expected: PASS.
- [ ] **Step 5: Commit** `feat(cockpit): parse review verdicts and decide merge eligibility (#382)`

---

### Task 3: Collector + `classify` — the merge card

**Files:**
- Modify: `internal/cockpit/collector.go` (`Sources`, `fetchIssues`, evidence cache), `internal/cockpit/board.go` (`RepoData`), `internal/cockpit/inbox.go`
- Test: `internal/cockpit/inbox_test.go`, `internal/cockpit/collector_test.go`

**Interfaces:**
- Consumes: Task 2 (`ClosingPipelinePRs`, `BuildEvidence`, `MergeEvidence`).
- Produces:
  - `const CardMerge = "merge"`; `InboxCard.Merge *MergeEvidence` (`json:"merge,omitempty"`).
  - `RepoData.Merge map[int]MergeEvidence` (keyed by issue number) and `RepoData.MergeErrs map[int]string`.
  - `Sources.Bots []string` and `Sources.MergeEvidence func(ctx context.Context, r core.Repo, pr forge.PullRequest) (MergeEvidence, error)` — may be nil (merge cards off). The func does the live reads; the collector owns the cache.
  - `func (c *Collector) MergeCard(owner, name string, number int) (InboxCard, bool)` — the current snapshot's merge card for that issue (Task 4 uses it for the "not on the inbox" guard).

Behaviour:
- `fetchIssues`: for each issue with `ai:done`, not parked, **without** `ai-review-ai-merge`, whose `ClosingPipelinePRs` has exactly one PR, get evidence. The collector caches whole evidence keyed by `(repo, pr, headSHA)` (head SHA from the list-PRs payload) and calls `Sources.MergeEvidence` only when there is no entry for that key **or** the cached `Checks.State` is `running` or `none`, **or** the entry is older than the issues interval. On error: keep the previous evidence for that issue with `Stale = true` and `CanMerge, Reason` recomputed via `Eligibility`; record `MergeErrs[issue]`; never read → evidence `{PR, PRURL, HeadSHA, Stale: true}` + its reason. Prune cache entries for PRs no longer open.
- Several closing pipeline PRs → evidence `{Reason: "several open PRs close this issue"}`, no source call.
- `classify`: unchanged up to `cardKind`. When `action.Kind == dispatch.ActionNone` **and** `slices.Contains(i.Labels, dispatch.LabelAIDone)` **and** `rd.Merge` has the issue → card `Kind: CardMerge`, `Reason: "ready to merge"` or `ev.Reason`, `Since: PR Updated`, `Merge: &ev`.
- `urgency`: `CardMerge` → 1.
- `withCommentErrs` sibling: add `MergeErrs` to `Inbox.Errors` under key `"merge <repo>#<n>"`.

- [ ] **Step 1: Write the failing tests**

```go
func TestBuildInbox_MergeCard_Table(t *testing.T) {
	// rd.Merge[5] = MergeEvidence{PR: 9, CanMerge: true}
	// issue 5 labels {"ai:done"}                    → one card Kind "merge", Reason "ready to merge", Merge.PR 9
	// same, evidence Reason "checks running"        → Kind "merge", Reason "checks running"
	// issue 5 labels {"ai:done"}, no rd.Merge entry → no card
}
func TestBuildInbox_DoneWithFailureLabel_NoMergeCard(t *testing.T) {
	// labels {"ai:done","ai-review-blocked"} + evidence → card Kind "human", Reason "review blocked"
	// labels {"ai:done","parked"} + evidence → Parked entry, not a merge card
}
func TestBuildInbox_MergeCard_UrgencyGroup1(t *testing.T) {
	// merge card sorts before a "task_failure" human card regardless of Since
}
func TestCollector_MergeEvidence_OnlyTrustedSingleClosingPR(t *testing.T) {
	// fake Sources: issue 5 ai:done; PRs: #9 closes #5 by github-actions[bot]; #10 closes #5 by "alice"
	// → MergeEvidence called once with PR 9; Inbox has merge card for 5
	// issue labelled ai-review-ai-merge → source never called
}
func TestCollector_MergeEvidence_ErrorKeepsStale(t *testing.T) {
	// tick 1 ok (CanMerge true); tick 2 source errors → card still there, Merge.Stale true,
	// Reason "forge read failed (evidence stale)", Inbox.Errors has "merge o/a#5"
}
func TestCollector_MergeEvidence_CachedByHeadSHA(t *testing.T) {
	// two ticks, same head SHA, checks green → source called once; head SHA changes → called again
}
```

- [ ] **Step 2: Run** `go test ./internal/cockpit -run 'Merge'` — Expected: FAIL.
- [ ] **Step 3: Implement.** The `ai-review-ai-merge` skip lives in the collector (spec decision 2), not in `classify`.
- [ ] **Step 4: Run** `go test -race ./internal/cockpit` — Expected: PASS (all existing inbox tests unchanged).
- [ ] **Step 5: Commit** `feat(cockpit): merge card for ai:done issues awaiting a human merge (#382)`

---

### Task 4: `POST /api/inbox/merge` + wiring + `BRIDGE_PIPELINE_BOTS`

**Files:**
- Modify: `internal/api/inbox.go`; Create: `internal/api/merge.go`
- Modify: `cmd/bridge/serve.go`, `cmd/bridge/pipeline.go` (sources + merge func); Create: `cmd/bridge/merge.go`
- Modify: `docs/cockpit.md` (endpoint, payload, env var)
- Test: `internal/api/merge_test.go`, `cmd/bridge/merge_test.go`

**Interfaces:**
- Consumes: Task 1 methods/`APIError`, Task 2 `BuildEvidence`/`Eligibility`, Task 3 `Collector.MergeCard`.
- Produces:

```go
// internal/api/merge.go
type MergeRequest struct {
	Repo    string `json:"repo"`
	Number  int    `json:"number"`
	PR      int    `json:"pr"`
	HeadSHA string `json:"headSha"`
}
type MergeResult struct {
	Merged        bool   `json:"merged"`
	BranchDeleted bool   `json:"branchDeleted"`
	BranchError   string `json:"branchError,omitempty"`
}
// ErrNotMergeable carries the reason for a 409.
type ErrNotMergeable struct{ Reason string }
func (e ErrNotMergeable) Error() string
// ErrForgeRefused carries GitHub's status + message.
type ErrForgeRefused struct{ Status int; Message string }
func (e ErrForgeRefused) Error() string
// InboxHandler gains:  Merge func(ctx context.Context, req MergeRequest) (MergeResult, error)
// and a separate exported handler for the guarded route:
func (h *InboxHandler) MergeHandler() http.Handler // POST only, path /api/inbox/merge
```

```go
// cmd/bridge/merge.go
type merger interface {
	GetPullRequest(ctx context.Context, owner, repo string, number int) (forge.PullRequest, error)
	ListPRComments(ctx context.Context, owner, repo string, number int) ([]forge.Comment, error)
	ListCheckRuns(ctx context.Context, owner, repo, sha string) ([]forge.CheckRun, error)
	ListCommitStatuses(ctx context.Context, owner, repo, sha string) ([]forge.CommitStatus, error)
	ListPullRequestFiles(ctx context.Context, owner, repo string, number int) ([]forge.PRFile, error)
	MergePullRequest(ctx context.Context, owner, repo string, number int, sha, method string) error
	DeleteBranch(ctx context.Context, owner, repo, ref string) error
}
func pipelineBots(env string) []string // "" → ["github-actions[bot]"]; trims, drops empties
func mergePR(ctx context.Context, m merger, bots []string, req api.MergeRequest) (api.MergeResult, error)
```

Behaviour:
- Route: `apiMux.Handle("/api/inbox/merge", requireTokenConfigured(apiToken, requireBearer(apiToken, requireSameOrigin(origins, inboxH.MergeHandler()))))` registered **before** `/api/inbox/` (longest pattern wins in `ServeMux`, but register explicitly anyway); the read-only `ServeHTTP` keeps answering `/api/inbox/merge` GETs with 404 via `splitDetailPath` (2 segments).
- Handler: decode with `http.MaxBytesReader` 4 KiB; validate `repo` owner/name, `number > 0`, `pr > 0`, `headSha` matches `^[0-9a-fA-F]{40}$` → else 400. Call `Merge`. Map: `ErrNotMergeable` → 409 `{"error":reason}`; `ErrForgeRefused` 403 → 403, 405/409/422 → 409, message as error; `ErrUnknownRepo`/`ErrNoToken`/`ErrUnsupportedForge` as `clear` does; other → 502 "forge call failed" (log the error).
- Wiring (`cmd/bridge`): `Merge` closure = require `collector.MergeCard(owner,name,number)` with `Merge.PR == req.PR` else `ErrNotMergeable{"not on the inbox"}`; resolve the owner's GitHub client (as `clearEnrichmentLock` does); `mergePR`; on `Merged` → `go col.CollectRepo(ctx, owner, name)` on the server ctx.
- `mergePR`: live `GetPullRequest`; `!strings.EqualFold(pr.HeadSHA, req.HeadSHA)` → `ErrNotMergeable{"head moved"}`; `pr.State != "open"` → `ErrNotMergeable{"PR is not open"}`; read comments, checks, statuses, files; `BuildEvidence`; `!CanMerge` → `ErrNotMergeable{Reason}`; `MergePullRequest(..., req.HeadSHA, "squash")` (`*forge.APIError` → `ErrForgeRefused`); then if `pr.HeadRepo == pr.BaseRepo` `DeleteBranch(pr.HeadRef)`; delete error → `BranchDeleted:false, BranchError: err.Error()`.
- `cockpitSources` gets `Bots: pipelineBots(os.Getenv("BRIDGE_PIPELINE_BOTS"))` and `MergeEvidence` = the same live reads + `BuildEvidence` (share one helper with `mergePR`: `liveEvidence(ctx, m, bots, owner, repo, pr) (cockpit.MergeEvidence, forge.PullRequest, error)`).

- [ ] **Step 1: Write the failing tests**

```go
// cmd/bridge/merge_test.go — fakeMerger records calls
func TestMerge_AllGreen_SquashesWithSHAAndDeletesBranch(t *testing.T) {
	// want MergePullRequest(o,a,9,head,"squash") once, DeleteBranch(o,a,"feat/5-x") once, result {true,true,""}
}
func TestMerge_HeadMoved_409NoMerge(t *testing.T) {
	// live head != req.HeadSHA → ErrNotMergeable{"head moved"}, MergePullRequest not called
}
func TestMerge_ReviewOnOlderCommit_NotMergeable(t *testing.T) {}
func TestMerge_ForgeRefuses405_ForgeRefused(t *testing.T) {
	// MergePullRequest returns &forge.APIError{405,"Required status check..."} → ErrForgeRefused{405, msg}, no delete
}
func TestMerge_DeleteFails_Still200(t *testing.T) {
	// DeleteBranch errors → result {Merged:true, BranchDeleted:false, BranchError non-empty}, err nil
}
func TestMerge_ForkHead_NoDelete(t *testing.T) {}
func TestPipelineBots_Table(t *testing.T) {
	// "" → [github-actions[bot]]; " a[bot] , ,b[bot]" → [a[bot] b[bot]]
}
// internal/api/merge_test.go
func TestMergeHandler_StatusMapping_Table(t *testing.T) {
	// Merge returns nil → 200 JSON MergeResult; ErrNotMergeable → 409 {"error":"head moved"};
	// ErrForgeRefused{403} → 403; ErrForgeRefused{422} → 409; other error → 502 "forge call failed"
}
func TestMergeHandler_BadInput_400_Table(t *testing.T) {
	// headSha "abc" → 400; pr 0 → 400; repo "x" → 400; GET → 405 Allow: POST
}
```

- [ ] **Step 2: Run** `go test ./internal/api ./cmd/bridge -run 'Merge|PipelineBots'` — Expected: FAIL.
- [ ] **Step 3: Implement**, then document the endpoint, the `merge` card payload and `BRIDGE_PIPELINE_BOTS` (including "list the pipeline App's bot login when the repo uses `PIPELINE_APP_ID`") in `docs/cockpit.md`.
- [ ] **Step 4: Run** `go test -race ./... && go vet ./... && golangci-lint run && test -z "$(gofmt -l .)"` — Expected: all clean.
- [ ] **Step 5: Commit** `feat(serve): guarded POST /api/inbox/merge squash-merges at the shown head (#382)`

---

### Task 5: Web — `MergeCard` and the Merge confirm

**Files:**
- Create: `web/src/lib/components/MergeCard.svelte`, `web/src/lib/components/MergeCard.test.js`
- Modify: `web/src/routes/Inbox.svelte` (render `MergeCard` for `card.kind === 'merge'`), `web/src/lib/components/ConfirmDialog.svelte` (a `merge` action), `web/src/lib/components/ConfirmDialog.test.js`

**Interfaces:**
- Consumes: `/api/inbox` card `{kind:"merge", repo, number, title, url, reason, merge:{pr, prUrl, headSha, baseRef, draft, summary, review, checks, files, filesTruncated, stale, canMerge, reason}}`; `authedPost`, `ensureToken` from `web/src/lib/auth.js`; `loadInbox` from `web/src/lib/stores/inbox.js`.
- Produces: `MergeCard` props `{ card, now, onopen }` (same as `InboxCard`). `ConfirmDialog` accepts `action="merge"` plus an optional `detail` prop `{pr, baseRef, checks, review, headSha}`.

Copy (from the wireframe):
- Header line: `✓ <repo> #<number> → PR #<pr>`; second line `checks ✓ <passed>/<total> · review: <verdict>`.
- Summary: first line, tap expands full text; empty → `no summary in PR body`.
- Files: `<n> files +<adds> −<dels>`, expandable list `path +a −d`; `filesTruncated` → `(first 100 shown)`.
- `[Open PR ↗]` link to `prUrl`; `[Merge]` only when `merge.canMerge`, else the reason in that slot.
- Confirm title `Merge PR #<pr> into <baseRef>?`, body `<repo> · squash · checks <passed>/<total> ✓ · review approve on <headSha first 7> (head) · Closes #<number>. The branch is deleted after merge.`, confirm button `Squash & merge`.
- Results: 200 → `✓ Merged PR #<pr>` (+ ` · branch not deleted: <branchError>` when `!branchDeleted`), then `loadInbox()`. 409 → `Not merged: <message>` and `loadInbox()`. 401 → `Token rejected`. 403 → message. Other → `request failed`.
- POST body `{repo, number, pr: merge.pr, headSha: merge.headSha}` to `/api/inbox/merge`.

- [ ] **Step 1: Write the failing tests** (vitest + @testing-library/svelte, like `InboxCard.test.js`)

```js
it('shows Merge only when canMerge', ...)          // canMerge true → button "Merge"; false + reason "checks running" → text, no button
it('confirm names the exact effect', ...)           // open confirm → contains "Merge PR #9 into main?", "squash", "96fae5f", "Closes #5", "branch is deleted"
it('posts the head SHA the card showed', ...)       // mock authedPost; confirm → called with '/api/inbox/merge', {repo:'o/a', number:5, pr:9, headSha: HEAD}
it('409 shows the reason and reloads', ...)         // authedPost rejects {status:409, message:'head moved'} → "Not merged: head moved", loadInbox called
it('reports a branch that was not deleted', ...)    // resolves {merged:true, branchDeleted:false, branchError:'x'} → "branch not deleted: x"
it('empty summary says so', ...)
```

- [ ] **Step 2: Run** `cd web && npx vitest run src/lib/components/MergeCard.test.js` — Expected: FAIL.
- [ ] **Step 3: Implement** `MergeCard.svelte` (states idle | confirm | working | done | failed, as `StuckActions.svelte`), the `merge` entry in `ConfirmDialog` (`VERBS.merge = 'Squash & merge'`, no `slot`), and the `Inbox.svelte` branch. Phone-first layout; leave a spot for #383's `[diff ▸]` but render nothing there.
- [ ] **Step 4: Run** `cd web && npx vitest run && npx vite build` — Expected: PASS, build OK.
- [ ] **Step 5: Commit** `feat(webui): merge card with a confirmed squash merge (#382)`

---

### Task 6: Full verification

- [ ] **Step 1:** `test -z "$(gofmt -l .)" && go vet ./... && golangci-lint run && go test -race ./... && (cd web && npx vitest run && npx vite build)` — Expected: all clean.
- [ ] **Step 2:** `CHANGELOG.md` `[Unreleased]` → `Added`: "Cockpit inbox: merge card for agent PRs awaiting a human merge, with a guarded squash merge pinned to the shown head SHA (#382)." Commit `docs(changelog): merge card (#382)`.
