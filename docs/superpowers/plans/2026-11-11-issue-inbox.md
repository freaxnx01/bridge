# Issue Inbox Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A read-only **Inbox** area, the cockpit's new start page, listing every open issue in a wired GitHub repo that needs a person (from `dispatch.NextAction` over the pipeline's run marker), with everything running/retrying/paused as one line each, an issue detail with the run history, and a phone-first layout.

**Architecture:** The collector gains one source (`Issue`, GitHub `GetIssue`) that it calls only for inbox candidates whose `Updated` changed, keeps the comments on `RepoData`, and builds an `Inbox` next to the `Board` with a pure `BuildInbox`. It publishes `inbox-updated` separately from `pipeline-updated`. A new `InboxHandler` serves `GET /api/inbox` and `GET /api/inbox/{owner}/{repo}/{number}`. The Svelte SPA gets an `inbox` store, an `Inbox` route (default area), `InboxCard`, `WaitLine` and `IssueDetail`.

**Tech Stack:** Go (as pinned in `go.mod`), stdlib `testing` (table-driven `t.Run`, hand-rolled fakes), `net/http/httptest`; Svelte 5 + Vite + Vitest + `@testing-library/svelte` (jsdom).

**Spec:** `docs/superpowers/specs/2026-11-11-issue-inbox-design.md`

## Global Constraints

- Read-only: no new write endpoint, no label or comment writes, no change to `NextAction`, the Pipeline board, or `singleAIImplement`.
- GitHub only: candidates are issues in repos with `Forge == "github"` and `Wired == true`. Forgejo/ADO repos get no comment fetch and no inbox entries.
- Candidate labels (exact): `ai-implement`, `ai:failed`, `ai:partial`, `ai:done`, `ai:paused`, `ai:review-blocked`, `ai:review-failed`, `ai:runner-blocked`, `ai:checks-blocked`, `parked`, `🧊 parked` — use the existing `dispatch.Label*` constants.
- In flight = some run with `Event == "issues"`, `Status` `queued` or `in_progress`, and `DisplayTitle == issue.Title`.
- SSE event names: `pipeline-updated` (unchanged), `inbox-updated` (new). Each fires only when its JSON changed, ignoring `CollectedAt`.
- Go: stdlib `testing` only, table-driven, hand-rolled fakes; **no** testify/mockery/gomock; **no new Go modules**. Test names `TestFunc_State_Expected`.
- Web: **no new npm dependencies**. Titles and reasons render as text (`{…}`), never `{@html}`. Links open in a new tab (`target="_blank" rel="noopener noreferrer"`). Tap targets ≥ 44 px. Breakpoint stays `max-width: 899px` / `min-width: 900px`.
- TDD: failing test first, then implementation. Never change an existing test to go green. The only existing tests that change do so **because the spec changes behaviour**. Each is named in its task: the `onChange` closure signature in `TestCollector_OnChange_FiresOnlyOnContentChange`, and `App.test.js` "lands on the Pipeline view", which becomes Inbox.
- Gates after every task (all must be green before commit):
  - Go: `gofmt -l .` empty, `go vet ./...`, `golangci-lint run`, `go test -race ./...`. Use `just test-fast` while iterating; `cmd/bridge` tests take ~150 s, so run the full suite only once at the end of each task. `just test` also needs `bats`.
  - Web (Tasks 5–6): `cd web && npm ci` once, then `npx vitest run` and `npm run build`. CI runs no web tests, so these are the only gate. Never commit `internal/web/dist/**` (gitignored).
- Conventional Commits, reference `#368`; scopes `forge`/`dispatch`, `cockpit`, `api`, `web`, `docs`.

## Review Focus

1. A re-dispatched failed issue (`ai-implement`, `ai:failed`, old `transient`/`stop` marker) with a title-matched `in_progress` `issues` run must be in `Waiting` with reason `running`, never a card. Test in Task 2.
2. A comment fetch that fails after an earlier success keeps the stale comments (the card stays), shows in `Errors["comments o/a#N"]`, and is retried on the next tick even though `Updated` did not change. Test in Task 3.
3. A `decision: retry` marker that crosses the 3 h stale line with no new data must still flip to a `retry never reported` card and fire `inbox-updated`. The 5 s sessions tick rebuilds with `c.now()`. Test in Task 3.
4. Two open issues in one repo with the same title both read in flight while one run is going. This is the accepted, documented limit, pinned by a test in Task 2 and listed under Known limits in Task 7.
5. A 360 px phone: long titles and reasons wrap, and every link and the card's open button stay tappable. Clicking a link inside a card must not also open the detail. Test in Task 5.

---

### Task 1: Run data — display title, all markers, JSON tags

**Files:**
- Modify: `internal/forge/client.go:252-263` (`WorkflowRun`), `internal/forge/github_pulls.go:87-130` (`ghWorkflowRun`, `ListWorkflowRuns`)
- Modify: `internal/dispatch/runrecord.go` (`RunRecord` tags, new `Runs`, `LatestRun` reuses it)
- Test: `internal/forge/github_pulls_test.go`, `internal/dispatch/runrecord_test.go`

**Interfaces:**
- Produces: `forge.WorkflowRun.DisplayTitle string \`json:"display_title"\``, filled from GitHub's `display_title`.
- Produces: `func Runs(comments []forge.Comment) []RunRecord` in package `dispatch`. It returns every trusted (`pipelineReportAuthor`), parseable marker with `Posted` set, **oldest first**, stable-sorted by `Created` so equal times keep slice order. It returns an empty non-nil slice when there are none.
- Produces: `RunRecord` JSON tags, lower-camel: `attempt, counts, retryAttempt, class, decision, status, subtype, agent, model, turns, costUsd, pr, runUrl, posted`. Split `Agent, Model string` into two tagged fields. `runMarker` (wire parsing) is unchanged.

- [ ] **Step 1: Write the failing tests**
  - `TestGithubListWorkflowRuns_FiltersAndMapsActors`: add `"display_title":"fix(nav): x"` to the fixture JSON and assert `runs[0].DisplayTitle == "fix(nav): x"`. This adds a new assertion; nothing existing changes.
  - `TestRuns` (table):
    - no comments → `len == 0` and non-nil.
    - three pipeline markers at t+2h, t, t+1h → `Posted` order t, t+1h, t+2h.
    - two with equal `Created` → slice order kept.
    - a marker by `"someone"` → excluded.
    - a pipeline comment without a marker → excluded.
  - `TestLatestRun_EqualsLastOfRuns`: for each `TestRuns` input, `LatestRun` returns `(last, true)`, or `(RunRecord{}, false)` when empty. `TestLatestRun` must pass **unmodified**.
  - `TestRunRecord_MarshalJSON_LowerCamelKeys`: marshal a full record and unmarshal it into `map[string]any`. The keys must equal exactly the 14 above.
- [ ] **Step 2: Run to verify failure** — `go test ./internal/forge ./internal/dispatch -run 'WorkflowRuns|Runs|LatestRun|RunRecord' -v` → FAIL (`DisplayTitle`, `Runs` undefined; keys `Attempt` …).
- [ ] **Step 3: Implement** — the field and mapping in forge, the tags, and `Runs`. `LatestRun` becomes "`Runs`, return the last element". Keep its doc comment's tie rule.
- [ ] **Step 4: Run the Go gates** (Global Constraints). Expected: green.
- [ ] **Step 5: Commit**
```bash
git add internal/forge/client.go internal/forge/github_pulls.go internal/forge/github_pulls_test.go internal/dispatch/runrecord.go internal/dispatch/runrecord_test.go
git commit -m "feat(dispatch): list all run markers and expose run display titles (#368)"
```

### Task 2: `BuildInbox` — pure classification

**Files:**
- Create: `internal/cockpit/inbox.go`, `internal/cockpit/inbox_test.go`
- Modify: `internal/cockpit/board.go` (`RepoData` gains two fields)

**Interfaces:**
- Consumes: `dispatch.NextAction`, `IssueState`, `Runs`/`LatestRun`, `IsParked`, `Label*` (Task 1); `forge.WorkflowRun.DisplayTitle`.
- Produces (package `cockpit`):
  - `RepoData.Comments map[int][]forge.Comment`: issue number → comments. An entry exists only when a fetch has ever succeeded.
  - `RepoData.CommentErrs map[int]string`: issue number → last fetch error.
  - `InboxCard`, `Inbox` exactly as in the spec ("Classification"), with `Run *dispatch.RunRecord \`json:"run,omitempty"\``. Kind constants: `CardHuman = "human"`, `CardRetry = "retry"`, `CardWait = "wait"`, `CardParked = "parked"`.
  - `type InboxInput struct{ Repos []RepoData; Now time.Time }`
  - `func BuildInbox(in InboxInput) Inbox`: `Cards`, `Waiting` and `Parked` are non-nil. `Errors` maps `"comments owner/name#N"` → message, and is nil when empty. It does not fill `CollectedAt`.
  - `func isInboxCandidate(labels []string) bool`
  - `func inFlight(i forge.Issue, runs []forge.WorkflowRun) bool`
  - `type IssueRef struct{ Repo string; Number int; Title, URL string; Labels []string }`, JSON `repo, number, title, url, labels`; `Labels` is never nil.
  - `type IssueDetail struct{ Issue IssueRef \`json:"issue"\`; Runs []dispatch.RunRecord \`json:"runs"\` }`
  - `func BuildIssueDetail(repo string, i forge.Issue, comments []forge.Comment) IssueDetail`: `Runs` = `dispatch.Runs` reversed (newest first), non-nil.

Rules: an issue goes to `NextAction` only if its repo is GitHub and wired, it is a candidate, and `rd.Comments` has its number. Otherwise it is skipped: never classify unread comments as "no run marker". `Since` = `Run.Posted` when `HasRun`, else `issue.Updated`. `Reason` = `Action.Reason` verbatim. Mapping table as in the spec. Urgency group of a card, then oldest `Since` first, stable:

```text
1  human, reason in {"review blocked", "review failed", "checks blocked", "partial"}
2  human, any other reason except group 4
3  retry
4  human, reason in {"no run marker", "retry never reported"}
```
`Waiting` and `Parked` are sorted oldest `Since` first.

- [ ] **Step 1: Write the failing tests** (`now := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)`; helpers `marker(json string, at time.Time) forge.Comment` authored `github-actions[bot]` and `repo(issues, runs, comments)` for a wired GitHub `RepoData` that gives **every** issue an empty `Comments` entry by default — "unread" is opt-in per test, otherwise the skip rule silently drops rows)
  - `TestBuildInbox_MappingRows_Table`, one row per spec mapping row, asserting bucket, `Kind`, `Reason`:
    - `parked` → Parked/`parked`/`"parked"`; `🧊 parked` → same.
    - `ai:review-blocked` → Cards/`human`/`"review blocked"`.
    - `ai-implement` + marker `{"v":1,"class":"rate_limit","decision":"stop","attempt":2}` → Cards/`retry`/`"rate_limit, retries exhausted"`.
    - `ai:paused` → Waiting/`wait`/`"paused"`.
    - `ai-implement`, no marker → Waiting/`"no run report yet"`.
    - `ai:done`, no marker → in no bucket.
  - `TestBuildInbox_NonCandidateOrUnreadComments_Skipped`: labels `["feat"]` with comments; `ai:failed` with no `Comments` entry; `ai:failed` in an unwired repo; `ai:failed` in a `forgejo` repo → all three buckets empty.
  - `TestBuildInbox_CardsUrgency_GroupThenOldest`: one card per group plus two group-1 cards (`ai:review-blocked` with `Updated` now-1h, `ai:checks-blocked` with `Updated` now-2h). Expected order: checks-blocked, review-blocked, `task_failure`/`stop` marker (group 2), retry card (group 3), `ai:failed` without marker (group 4, `"no run marker"`). This holds even when group 4 is the oldest.
  - `TestBuildInbox_Since_RunPostedElseUpdated`: with a marker, `Since == marker.Created`; without one, `Since == issue.Updated`. `Run` is nil without a marker and `Run.PR == 42` with `"pr":42`.
  - `TestBuildInbox_InFlight_Table`: issue `ai-implement, ai:failed`, title `"T"`, marker `transient`/`stop`/`attempt:1`.
    - run `{Event:"issues", Status:"in_progress", DisplayTitle:"T"}` → Waiting `"running"`.
    - `Status:"queued"` → Waiting `"running"`.
    - `Status:"completed"` → Cards retry `"transient, retries exhausted"`.
    - `Event:"workflow_dispatch"` → Cards retry.
    - `DisplayTitle:"other"` → Cards retry.
  - `TestBuildInbox_SameTitleTwoIssues_BothInFlight`: two `ai-implement` issues titled `"T"` and one matching run → both Waiting `"running"`. This is the documented limit.
  - `TestBuildInbox_WaitingAndParked_OldestFirst`.
  - `TestBuildInbox_CommentErrs_InErrors`: `CommentErrs{5: "boom"}` on repo `o/a` → `Errors["comments o/a#5"] == "boom"`.
  - `TestBuildInbox_Empty_NonNilSlices`: marshal → `"cards":[]`, `"waiting":[]`, `"parked":[]`, no `"errors"` key.
  - `TestBuildIssueDetail_RunsNewestFirst`: two pipeline markers plus one marker by `"someone"` → 2 runs, newest first. A nil `Labels` marshals as `[]`.
- [ ] **Step 2: Run to verify failure** — `go test ./internal/cockpit -run 'Inbox|IssueDetail' -v` → FAIL (undefined).
- [ ] **Step 3: Implement** in `inbox.go`. Keep functions ≤ 20 lines: `classify(rd, i, now) (InboxCard, Action, bool)`, `urgency(InboxCard) int`, `sortBySince`. The board's code and tests stay untouched apart from the two new `RepoData` fields.
- [ ] **Step 4: Run the Go gates.** Expected: green.
- [ ] **Step 5: Commit**
```bash
git add internal/cockpit/inbox.go internal/cockpit/inbox_test.go internal/cockpit/board.go
git commit -m "feat(cockpit): classify inbox candidates with NextAction (#368)"
```

### Task 3: Collector — comments cache, inbox snapshot, `inbox-updated`

**Files:**
- Modify: `internal/cockpit/collector.go`, `internal/cockpit/collector_test.go`
- Modify: `cmd/bridge/pipeline.go` (`cockpitSources`), `cmd/bridge/pipeline_sources_test.go`, `cmd/bridge/serve.go:238-240` (onChange)

**Interfaces:**
- Consumes: Task 2's `BuildInbox`, `BuildIssueDetail`, `isInboxCandidate`, `inFlight`, `RepoData.Comments/CommentErrs`.
- Produces:
  - `Sources.Issue func(ctx context.Context, r core.Repo, number int) (forge.Issue, []forge.Comment, error)`, which is `GithubClient.GetIssue`. This one source serves both candidate comments and the live detail.
  - `const EventPipeline = "pipeline-updated"`, `const EventInbox = "inbox-updated"`
  - `NewCollector(src Sources, iv Intervals, now func() time.Time, onChange func(event string)) *Collector`: the parameter type changes from `func()`.
  - `func (c *Collector) Inbox() Inbox`, a deep-enough copy (clone the slices and the `Errors` map) like `Board()`.
  - `func (c *Collector) IssueDetail(ctx context.Context, owner, name string, number int) (IssueDetail, error)`. It returns `ErrUnknownRepo` unless `owner/name` is in the last snapshot with `Forge == "github" && Wired`. Otherwise it makes one live `src.Issue` call, wraps any error with `%w`, and returns `BuildIssueDetail`.
  - `var ErrUnknownRepo = errors.New("unknown repo")` in package `cockpit`.

Behaviour:
- **Fetch.** `fetchIssues` fetches comments for each candidate of a wired GitHub repo, sequentially within the repo; it already runs inside `fanOut`. It also covers `CollectRepo`.
- **Cache.** The cache is `map[issueKey]cachedComments{updated time.Time; comments []forge.Comment}` with its own `cacheMu sync.Mutex`, because `fetchIssues` runs without `c.mu`. On a hit (same `Updated`) it skips the call.
- **Fetch failure.** On failure, never store the new `Updated`. Use the stale comments if cached, and record `rd.CommentErrs[n]`. The failure is per issue and never fails the repo fetch.
- **Pruning.** `CollectIssues` prunes cache entries that are no longer candidates.
- **Rebuild.** `rebuildLocked` also builds the inbox (`BuildInbox{Repos, Now: c.now()}`) and compares its JSON without `CollectedAt` (stored `inboxJSON`). It sets `Inbox.CollectedAt = collectedAt["issues"]` and reports both changes. `publish` calls `onChange(EventPipeline)` and/or `onChange(EventInbox)` outside the lock.
- **Finished runs.** Inside `CollectRuns`, under `c.mu`, collect the URLs of runs that are in flight for a candidate (`inFlight`) before and after the swap. When a URL from before is missing afterwards, or its run is no longer queued/in_progress, call `c.CollectRepo(ctx, owner, name)` for that repo **after** unlocking and publishing.

- [ ] **Step 1: Write the failing tests.** Extend `fakeWorld` with `comments map[string][]forge.Comment` keyed `"o/a#N"`, `issueErr map[string]error`, `detailCalls map[string]int` (all under `w.mu`), and `Issue` in `sources()`. The events helper records `[]string`.
  - `TestCollector_CollectIssues_CommentsOnlyForWiredGithubCandidates`: `#1 ["feat"]`, `#2 ["ai:failed"]` on wired GitHub; `#3 ["ai:failed"]` on unwired; `#4` on a `forgejo` repo → `detailCalls` = `{"o/a#2": 1}` only.
  - `TestCollector_CollectIssues_UnchangedUpdated_SkipsCommentFetch`: collect twice → 1 call; bump `#2.Updated`, collect → 2.
  - `TestCollector_CommentFetchFails_KeepsStaleAndRetries`:
    - First fetch OK with a `task_failure`/`stop` marker → one card.
    - Bump `Updated`, set `issueErr` → card still present and `Inbox().Errors["comments o/a#2"]` set.
    - Clear the error and collect with the same `Updated` → a third call, `Errors` empty.
  - `TestCollector_CommentFetchFails_NoCache_IssueNotShown`: no card, no Waiting line, `Errors` set.
  - `TestCollector_Inbox_EventFiresOnlyOnInboxChange`: add `ai:failed` to an issue → exactly one `inbox-updated`. Collect again unchanged → none.
  - `TestCollector_Inbox_StaleRetryCrossesThreshold_FiresInboxEvent`: marker `transient`/`retry` posted at `t0`. Clock `t0+3h` → Waiting `"pipeline retrying"`. Advance to `t0+3h+1s` and `CollectSessions` → one `inbox-updated`, card `"retry never reported"`.
  - `TestCollector_CollectRuns_InFlightRunFinished_RefreshesRepo`: `#1 ["ai-implement"]` titled `"T"` and run `{issues, in_progress, DisplayTitle "T", URL "u1"}`. CollectIssues + CollectRuns → `issueCalls["o/a"] == 1`. Run becomes `completed`, CollectRuns → `issueCalls["o/a"] == 2`.
  - `TestCollector_CollectRuns_UnrelatedRunFinished_NoRefresh`: same, but `DisplayTitle "other"` → stays 1.
  - `TestCollector_IssueDetail_Table`:
    - unknown repo → `errors.Is(err, ErrUnknownRepo)`.
    - forgejo repo → `ErrUnknownRepo`.
    - unwired GitHub repo → `ErrUnknownRepo`.
    - wired GitHub repo → `Issue.Title` from the live call, runs newest first.
    - source error → wrapped error, not `ErrUnknownRepo`.
  - `TestCollector_OnChange_FiresOnlyOnContentChange`: change **only** the closure, to `func(ev string) { if ev == EventPipeline { calls++ } }`, because the signature changes by design. Its assertions stay.
  - `TestCockpitSources_Issue_NoClientForOwner_ReportsError` in `cmd/bridge`, mirroring `TestCockpitSources_NoClientForOwner_ReportsError`: `errors.Is(err, errNoForgeClient)`.
- [ ] **Step 2: Run to verify failure** — `go test ./internal/cockpit ./cmd/bridge -run 'Collector|CockpitSources' -v` → FAIL (compile: `Issue`, `EventInbox`, `Inbox` undefined).
- [ ] **Step 3: Implement** the collector changes. In `cockpitSources`, add `Issue` via `githubOnly`; for a non-GitHub repo return `errNoForgeClient`-wrapped, never nil/nil. In `serve.go`, the collector callback becomes `func(ev string) { hub.Broadcast(web.Event{Type: ev}) }`.
- [ ] **Step 4: Run the Go gates.** Expected: green, `-race` included.
- [ ] **Step 5: Commit**
```bash
git add internal/cockpit/collector.go internal/cockpit/collector_test.go cmd/bridge/pipeline.go cmd/bridge/pipeline_sources_test.go cmd/bridge/serve.go
git commit -m "feat(cockpit): collect run markers and publish inbox-updated (#368)"
```

### Task 4: Inbox API

**Files:**
- Create: `internal/api/inbox.go`, `internal/api/inbox_test.go`
- Modify: `cmd/bridge/serve.go` (register next to `/api/pipeline`)

**Interfaces:**
- Consumes: `cockpit.Inbox`, `cockpit.IssueDetail`, `cockpit.ErrUnknownRepo`, `Collector.Inbox`, `Collector.IssueDetail` (Task 3).
- Produces: `type InboxHandler struct{ Inbox func() cockpit.Inbox; Detail func(ctx context.Context, owner, repo string, number int) (cockpit.IssueDetail, error) }` with `ServeHTTP`.

Routing, parsed by hand like `PipelineHandler`:
- `GET /api/inbox` returns the inbox. `nil` slices are serialized as `[]`.
- `GET /api/inbox/{owner}/{repo}/{number}` takes exactly three non-empty segments.
- Any other path under `/api/inbox/` → 404 `not found`.
- A `number` that is not a positive integer → 400 `number must be positive`.
- Any method but GET → 405 with `Allow: GET`.
- `ErrUnknownRepo` → 404 `not found`.
- Any other `Detail` error → 502 `forge request failed`. The detail is logged with `slog.Error`, never echoed.

Use the existing `writeJSON` / `writeError`.

- [ ] **Step 1: Write the failing tests** (function-field fakes, `doReq` from `pipeline_test.go`)
  - `TestInboxHandler_GetInbox_ReturnsJSON`: one card → 200, `cards[0].reason` as given.
  - `TestInboxHandler_EmptyInbox_SerializesEmptyArrays`: the body contains `"cards":[]`, `"waiting":[]`, `"parked":[]`.
  - `TestInboxHandler_GetDetail_PassesOwnerRepoNumber`: `/api/inbox/freaxnx01/bridge/412` → the fake sees `("freaxnx01","bridge",412)`, 200, `runs` array.
  - `TestInboxHandler_DetailUnknownRepo_404`.
  - `TestInboxHandler_DetailForgeError_502GenericBody`: the body does not contain the fake's error text.
  - `TestInboxHandler_BadNumber_400` (table: `abc`, `0`, `-1`).
  - `TestInboxHandler_MalformedPath_404` (table: `/api/inbox/o/r`, `/api/inbox/o/r/1/x`, `/api/inbox/o//1`, `/api/inbox/`).
  - `TestInboxHandler_WrongMethod_405` (POST to both shapes, `Allow == "GET"`).
- [ ] **Step 2: Run to verify failure** — `go test ./internal/api -run InboxHandler -v` → FAIL (undefined).
- [ ] **Step 3: Implement**, then wire `serve.go`: `inboxH := &api.InboxHandler{Inbox: collector.Inbox, Detail: collector.IssueDetail}`; `apiMux.Handle("/api/inbox", inboxH)`; `apiMux.Handle("/api/inbox/", inboxH)`. Both stay open like `GET /api/pipeline`, with no guard.
- [ ] **Step 4: Run the Go gates.** Expected: green.
- [ ] **Step 5: Commit**
```bash
git add internal/api/inbox.go internal/api/inbox_test.go cmd/bridge/serve.go
git commit -m "feat(api): serve the issue inbox and issue detail (#368)"
```

### Task 5: Inbox page — store, header, start page, cards, waiting lines

**Files:**
- Create: `web/src/lib/stores/inbox.js` (+ `inbox.test.js`), `web/src/lib/inbox.js` (+ `inbox.test.js`), `web/src/routes/Inbox.svelte` (+ `Inbox.test.js`), `web/src/lib/components/InboxCard.svelte` (+ `InboxCard.test.js`), `web/src/lib/components/WaitLine.svelte`
- Modify: `web/src/App.svelte`, `web/src/App.test.js`, `web/src/lib/components/Header.svelte`

**Interfaces:**
- Consumes: `GET /api/inbox`, SSE `inbox-updated` (Tasks 3–4); `ageSeconds`, `formatAge` from `lib/freshness.js` (they already give `12m`/`5h`/`3d`, so no `lib/age.js`); `now` store.
- Produces:
  - `stores/inbox.js`: `inbox` (writable, null), `inboxError`, `loadInbox()`, `startInboxPolling()` (fixed `INBOX_POLL_SECONDS = 60`, returns stop), and `loadIssueDetail(repo, number)`, which returns `apiGet(\`/api/inbox/${repo}/${number}\`)`. `repo` is `owner/name`. It reloads on SSE `inbox-updated`.
  - `lib/inbox.js`: `shortRef(card)` → `"bridge#412"` (name after the slash), `prUrl(repo, pr)` → `https://github.com/${repo}/pull/${pr}`, `runMeta(run)` → `"claude · opus-5 · $3.10 · #2"`. Parts are dropped when empty; cost uses `toFixed(2)`; `#attempt` appears only when `attempt > 0`.
  - `InboxCard` props `{ card, now, onopen }`. `WaitLine` props `{ card, now, onopen }`. `Header` gains prop `inboxCount = 0`.

Layout per the spec's phone wireframe:
- **Card.** The card is a `<button class="open">` that holds the kind glyph (`●` human, `↻` retry), `shortRef`, age, title, the reason in bold, and `runMeta`. It calls `onopen(card)`. Below it, a link row: `Issue ↗` (`card.url`) always; `PR ↗` only when `run.pr > 0`; `Run ↗` only when `run.runUrl`.
- **Page.** Cards come first, then a `Parked (n)` toggle button, collapsed by default and rendering nothing when n = 0, then the heading `Running & waiting` with `WaitLine`s (`shortRef  reason  age`). `errors` show as one warn line per entry: `Could not read comments: <key>`.
- **Empty and error states.** With no cards (including no wired GitHub repo at all) the page shows `Nothing needs you.` and the waiting list still renders. With no data and a load error it shows the error text.
- **Desktop (`min-width: 900px`).** Cards sit in a two-column grid; the waiting list goes to a narrow right column.
- **Start page and header.** `App.svelte` starts with `area = $state('inbox')`, renders `<Inbox />` for `inbox`, and starts `loadInbox()` + `startInboxPolling()` in `onMount`, so the header badge stays live on every area. `Header` adds an enabled `Inbox` nav button before `Pipeline`, with a badge `<span class="badge">{inboxCount}</span>` shown only when > 0. `App` passes `$inbox?.cards?.length ?? 0`. In Task 5, `onopen` sets the selected card in `Inbox.svelte`. The detail panel arrives in Task 6.

- [ ] **Step 1: Write the failing tests**
  - `lib/inbox.test.js`: `shortRef({repo:'freaxnx01/bridge', number:412}) === 'bridge#412'`; `prUrl('o/r', 7) === 'https://github.com/o/r/pull/7'`; `runMeta({agent:'claude', model:'opus-5', costUsd:3.1, attempt:2}) === 'claude · opus-5 · $3.10 · #2'`; with `attempt: 0` the result has no `#`.
  - `stores/inbox.test.js` (mock `./sse.js` like `pipeline.test.js`): polling fetches `/api/inbox` once per 60 s; `loadIssueDetail('o/r', 5)` fetches `/api/inbox/o/r/5`.
  - `InboxCard.test.js`:
    - The human card shows `●`, `bridge#412`, the bold reason, and `runMeta`.
    - The retry card shows `↻`.
    - With `run.pr = 415` and `runUrl`, the PR link's href is `https://github.com/freaxnx01/bridge/pull/415` and the Run link's href is `runUrl`; every link has `target="_blank"`.
    - With no `run`, only the `Issue ↗` link is present.
    - Clicking the open button calls `onopen` once; clicking `Issue ↗` does not call it.
    - A title containing `<b>x</b>` renders as literal text.
  - `Inbox.test.js` (mock sse):
    - empty cards with one waiting line → `Nothing needs you.` and the `Running & waiting` line both show.
    - `Parked (2)` is collapsed by default (parked titles absent), and a click shows them.
    - an `errors` entry → `Could not read comments: comments o/a#5`.
    - no data with `inboxError` set → the error text.
  - `App.test.js`: also mock `./lib/stores/inbox.js` (writable `inbox`, `inboxError`, `loadInbox`, `startInboxPolling: vi.fn(() => () => {})`, `loadIssueDetail`). Without this mock, `sse.js` builds an `EventSource` that jsdom lacks. **Rewrite** "lands on the Pipeline view" as "lands on the Inbox view" (`Nothing needs you.` visible, `RUNNING NOW` absent): the spec moves the start page. Add "reaches the Pipeline view via the nav" (click `Pipeline` → `RUNNING NOW`) and "shows the inbox card count in the header" (2 cards → badge text `2`). The Architecture test stays unchanged.
- [ ] **Step 2: Run to verify failure** — `cd web && npx vitest run` → FAIL (missing modules and the new assertions).
- [ ] **Step 3: Implement** the files above, styling from the existing `app.css` tokens, with `min-height: 44px` on buttons and links.
- [ ] **Step 4: Run the web gates** — `npx vitest run` (all files, including the existing `Pipeline.test.js`) and `npm run build`. Then run the Go gates (`go build ./...` embeds the build output). Expected: green; `git status` shows no `internal/web/dist` files.
- [ ] **Step 5: Commit**
```bash
git add web/src/lib/stores/inbox.js web/src/lib/stores/inbox.test.js web/src/lib/inbox.js web/src/lib/inbox.test.js web/src/routes/Inbox.svelte web/src/routes/Inbox.test.js web/src/lib/components/InboxCard.svelte web/src/lib/components/InboxCard.test.js web/src/lib/components/WaitLine.svelte web/src/App.svelte web/src/App.test.js web/src/lib/components/Header.svelte
git commit -m "feat(web): add the inbox as the cockpit's start page (#368)"
```

### Task 6: Issue detail panel

**Files:**
- Create: `web/src/lib/components/IssueDetail.svelte`, `web/src/lib/components/IssueDetail.test.js`
- Modify: `web/src/routes/Inbox.svelte`, `web/src/routes/Inbox.test.js`

**Interfaces:**
- Consumes: `loadIssueDetail(repo, number)`, `shortRef`, `prUrl`, `formatAge`/`ageSeconds` (Task 5).
- Produces: `IssueDetail` props `{ repo, number, onclose }`.

The panel is `role="dialog"` with `aria-label={shortRef}`. It fetches on mount and shows `Loading…`, then the content, or `Could not load issue.` on error. The header row holds a `←` back button (`aria-label="Back"`), `shortRef`, the title, and `labels: …` (comma-joined). The `Runs (newest first)` list keeps API order. Each run row shows `#attempt` (when > 0), `class`, `decision`, `$cost`; then `agent · model · turns t`; then `PR #n ↗` (only when `pr > 0`), `run ↗` (only when `runUrl`), and the age of `posted`. With no runs it shows `No run reports.` Esc (keydown on `window`) and `←` call `onclose`. On the phone (`max-width: 899px`) it is a fixed full-screen sheet (`inset: 0`); on desktop it is a fixed right side panel (`width: min(480px, 100vw)`). There is no browser-history integration: the app has no router, so back means the in-UI `←` and Esc.

- [ ] **Step 1: Write the failing tests** (stub `fetch`)
  - `renders runs newest first`: two runs in API order → rows in the same order. The first row has `task_failure`, `stop`, `$3.10`, `#2`.
  - `PR and run links only when present`: run A `{pr: 415, runUrl: 'u'}` has both links, with the PR href `https://github.com/o/r/pull/415`. Run B `{pr: 0, runUrl: ''}` has neither.
  - `no runs` → `No run reports.`
  - `fetch failure` → `Could not load issue.`
  - `Escape and Back call onclose` (once each).
  - `Inbox.test.js`: clicking a card's open button shows a dialog named `bridge#412`; Esc removes it. Clicking a `WaitLine` opens the dialog too.
- [ ] **Step 2: Run to verify failure** — `cd web && npx vitest run` → FAIL.
- [ ] **Step 3: Implement**, and render `<IssueDetail>` from `Inbox.svelte` when a card or line is selected.
- [ ] **Step 4: Run the web gates and the Go gates.** Expected: green.
- [ ] **Step 5: Commit**
```bash
git add web/src/lib/components/IssueDetail.svelte web/src/lib/components/IssueDetail.test.js web/src/routes/Inbox.svelte web/src/routes/Inbox.test.js
git commit -m "feat(web): show an issue's run history in the inbox (#368)"
```

### Task 7: Document the inbox

**Files:**
- Modify: `docs/cockpit.md` (intro, new `## Inbox` section before `## Board columns`, `## API`, `### SSE`, `## Known limits`), `CHANGELOG.md` (`## [Unreleased]`)

- [ ] **Step 1: `docs/cockpit.md`**
  - Intro: the Inbox is the start page; the Pipeline board is the second area.
  - `## Inbox`:
    - candidates (the label list, wired GitHub repos only).
    - the mapping table from the spec, and a pointer to `docs/dispatch.md` "When a run fails" for the reasons.
    - the four urgency groups.
    - Parked is folded.
    - the comment cache (re-fetched only when the issue's `updated` changes) and the refresh on a finished in-flight run.
  - `### GET /api/inbox`: the `Inbox` JSON shape. `### GET /api/inbox/{owner}/{repo}/{number}`: the shape plus 400/404/405/502.
  - SSE: `inbox-updated`, and the page polls `/api/inbox` every 60 s as a fallback.
  - Known limits:
    - a pipeline retry (`workflow_dispatch`) cannot be attributed by title, but its marker already reads "pipeline retrying".
    - two open issues with the same title in one repo both read as running.
    - issues from before agent-workflow v2.2.0 read "no run marker".
    - read-only; write actions are a follow-up.
- [ ] **Step 2: CHANGELOG** — `### Added`: "Cockpit: **Inbox** start page listing the issues that need a person (from the pipeline's run marker), with run history per issue; `GET /api/inbox`, `GET /api/inbox/{owner}/{repo}/{number}` and the SSE event `inbox-updated`. (#368)". `### Changed`: "Cockpit opens on the Inbox instead of the Pipeline board. `forge.WorkflowRun` (MCP `list_runs`) gains `display_title`. (#368)"
- [ ] **Step 3: Verify** — run the Go gates (docs-only, sanity) and confirm `grep -n 'inbox-updated' docs/cockpit.md` matches.
- [ ] **Step 4: Commit**
```bash
git add docs/cockpit.md CHANGELOG.md
git commit -m "docs(cockpit): document the issue inbox (#368)"
```
