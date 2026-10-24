# Bridge Cockpit — Shell + Pipeline Board Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** The WebUI served by `bridge serve` opens on a Pipeline board (Running now / Ready to dispatch / Needs enrichment) across all repos, fed by background collectors, with Enrich and Dispatch buttons.

**Architecture:** A new `internal/cockpit` package holds a pure `BuildBoard` (forge data + local sessions → board) and a `Collector` that refreshes each source on its own interval and keeps the last good snapshot. `internal/api` serves the snapshot and the token-guarded actions; actions shell out to `bridge launch --prompt` so the existing `/enrich` and `/gh:implement` commands stay the single source of their logic. The Svelte app gets a shell and a Pipeline route that live-updates over the existing SSE hub.

**Tech Stack:** Go (stdlib `net/http`, `testing`, hand-rolled fakes), Svelte 5 + Vite + vitest (existing `web/`).

**Spec:** `docs/superpowers/specs/2026-10-24-bridge-cockpit-design.md`

## Global Constraints

- No new Go module dependency (no `errgroup`: it is not in `go.mod`; use a semaphore channel + `sync.WaitGroup`). No new npm dependency.
- No request handler calls a forge API synchronously, except the two action endpoints' own launch.
- Every board source carries its `collectedAt`; the UI shows age and warns when older than 2× its interval.
- Writes: `requireBearer(BRIDGE_API_TOKEN)` **and** a same-origin check. Reads stay as today.
- Any action that starts paid/unattended work asks for confirmation in the page, naming repo, issue and what starts.
- One definition of "ready": `dispatch.Eligible`. The cockpit never re-implements it.
- Labels, exact strings: `needs-enrichment`, `❓ to-be-defined`, `🧊 parked`, `roadmap`, `ai-implement`, `enrichment-ongoing`.
- Repo's Go rules: `gofmt -l .` empty, `go vet ./...`, `golangci-lint run`, `go test -race ./...` all green after every task; `cd web && npm test -- --run` green after web tasks.
- UI phase gates: no Svelte component code before the wireframe (Task 1) is approved.

## Review Focus

1. **Forge token expired / rate-limited mid-run** → the board keeps the last good data for that repo, shows the source as erroring and stale, never empties a column. (Task 4 test `TestCollector_RepoFetchFails_KeepsLastGoodData`.)
2. **Session running in a worktree** (`<repo>/.worktrees/x`) → attributed to that repo; a session outside any repo still shows, with empty repo. (Task 3 test `TestBuildBoard_SessionInWorktree_AttributedToRepo`.)
3. **Double-click on Dispatch/Enrich** → no second session; the response says "already running". (Task 5 test `TestPipelineHandler_LaunchAlreadyRunning_Returns200WithFlag`.)
4. **Issue carrying both `needs-enrichment` and `enrichment-ongoing`** → only in Running, never twice. (Task 3 test `TestBuildBoard_EnrichmentOngoing_OnlyInRunning`.)
5. **SSE connection dropped silently** → staleness comes from `collectedAt`, not from the last event, so the page still turns stale. (Task 6 test `FreshnessBadge warns when collectedAt is older than 2x interval`.)

---

### Task 1: UI gates — wireframe and flow (human approval)

Interactive with the user; can run in parallel with Tasks 2–5. Blocks Task 6.

**Files:**
- Create: `docs/design/cockpit/wireframe.md`
- Create: `docs/design/cockpit/flow.md`

- [ ] **Step 1:** Run `/ui:brainstorm` for the cockpit shell + Pipeline board. Inputs: the spec's "Shell and navigation" and "First slice" sections, and the `Board` JSON shape from Task 3. Must show: header with area nav (only Pipeline active), per-column freshness, the collapsed "Held" list under Ready, card actions, confirmation dialog, token entry.
- [ ] **Step 2:** Get the ASCII wireframe approved by the user; save it to `docs/design/cockpit/wireframe.md`.
- [ ] **Step 3:** Run `/ui:flow`; get the Mermaid state diagram approved (states: loading, live, stale, error per column; action: idle → confirm → launching → launched / already-running / failed; token: absent → entered → rejected). Save to `docs/design/cockpit/flow.md`.
- [ ] **Step 4: Commit** `docs(web): add cockpit wireframe and flow`

### Task 2: One "ready" rule in `dispatch.Eligible`

**Files:**
- Modify: `internal/dispatch/eligible.go`
- Test: `internal/dispatch/eligible_test.go`

**Interfaces:**
- Produces: `dispatch.LabelToBeDefined = "❓ to-be-defined"`; `Eligible` unchanged signature, new reasons `"to-be-defined"` and `"no implementation plan"`.

- [ ] **Step 1: Write the failing tests** (table-driven, in `eligible_test.go`)

```go
func TestEligible_ToBeDefinedLabel_NotEligible(t *testing.T) {
	i := forge.Issue{Number: 7, Body: "x\n## Implementation Plan\n- a", Labels: []string{"❓ to-be-defined"}}
	if ok, why := Eligible(i, "", nil); ok || why != "to-be-defined" {
		t.Fatalf("got %v %q", ok, why)
	}
}

func TestEligible_BodyWithoutImplementationPlan_NotEligible(t *testing.T) {
	i := forge.Issue{Number: 7, Body: "## Acceptance criteria\n- a"}
	if ok, why := Eligible(i, "", nil); ok || why != "no implementation plan" {
		t.Fatalf("got %v %q", ok, why)
	}
}

func TestEligible_PlanHeadingAnyLevelCaseInsensitive_Eligible(t *testing.T) {
	for _, h := range []string{"## Implementation Plan", "### implementation plan"} {
		i := forge.Issue{Number: 7, Body: "x\n" + h + "\n- a"}
		if ok, why := Eligible(i, "", nil); !ok {
			t.Errorf("%q: got %v %q", h, ok, why)
		}
	}
}
```

- [ ] **Step 2: Run** `go test ./internal/dispatch -run TestEligible` → the three new tests FAIL.
- [ ] **Step 3: Implement.** Add the label constant. In `Eligible`, check `to-be-defined` right after `needs-enrichment`; check the plan heading after `attempts exhausted` and before `open PR`. Plan heading = a line matching `(?im)^#{2,6}\s*implementation plan\s*$`.
- [ ] **Step 4: Fix the fixtures, not the assertions.** This is a deliberate behaviour change from the spec: existing tests whose eligible fixtures lack a plan now fail. Add `"\n## Implementation Plan\n- step"` to those fixtures' bodies in `internal/dispatch/*_test.go` and `cmd/bridge/dispatch*_test.go`. Do not change any expected outcome or reason string. If a test fails for any other reason, stop and report.
- [ ] **Step 5: Run** `go test -race ./...` → PASS.
- [ ] **Step 6: Commit** `feat(dispatch)!: require an implementation plan and treat to-be-defined as unenriched` with a `BREAKING CHANGE:` footer explaining that `bridge dispatch` now skips issues without a plan.

### Task 3: `cockpit.BuildBoard` — pure board builder

**Files:**
- Create: `internal/cockpit/board.go`
- Test: `internal/cockpit/board_test.go`

**Interfaces:**
- Consumes: `dispatch.Eligible`, `dispatch.ActiveMilestone`, `dispatch.Order`, `dispatch.Candidate`, label constants from `internal/dispatch`; `forge.Issue/PullRequest/Milestone/WorkflowRun`; `agentview.Session`.
- Produces (JSON tags camelCase as written):

```go
type RunKind string
const (
	KindPipeline   RunKind = "pipeline"
	KindEnrichment RunKind = "enrichment"
	KindSession    RunKind = "session"
)
const (
	LabelEnrichmentOngoing = "enrichment-ongoing"
	LabelRoadmap           = "roadmap"
)
type RunningCard struct {
	Kind      RunKind   `json:"kind"`
	Repo      string    `json:"repo"`            // "owner/name"; "" when unknown
	Issue     int       `json:"issue,omitempty"`
	Title     string    `json:"title"`
	URL       string    `json:"url,omitempty"`
	Status    string    `json:"status"`
	Since     time.Time `json:"since"`
	SessionID string    `json:"sessionId,omitempty"`
	CWD       string    `json:"cwd,omitempty"`
}
type IssueCard struct {
	Forge  string   `json:"forge"`
	Repo   string   `json:"repo"` // "owner/name"
	Number int      `json:"number"`
	Title  string   `json:"title"`
	URL    string   `json:"url"`
	Labels []string `json:"labels"`
	Reason string   `json:"reason,omitempty"`
}
type SourceError struct {
	Source string    `json:"source"` // "issues owner/name", "runs owner/name", "sessions"
	Error  string    `json:"error"`
	Since  time.Time `json:"since"`
}
type Board struct {
	Running          []RunningCard        `json:"running"`
	Ready            []IssueCard          `json:"ready"`
	Held             []IssueCard          `json:"held"`
	NeedsEnrichment  []IssueCard          `json:"needsEnrichment"`
	DispatchPaused   bool                 `json:"dispatchPaused"`
	CollectedAt      map[string]time.Time `json:"collectedAt"`      // keys: "issues","runs","sessions"
	IntervalsSeconds map[string]int       `json:"intervalsSeconds"` // same keys
	Errors           []SourceError        `json:"errors"`
}
type RepoData struct {
	Forge, Owner, Name, Path string
	Wired      bool
	Issues     []forge.Issue
	PRs        []forge.PullRequest
	Milestones []forge.Milestone
	Runs       []forge.WorkflowRun
}
type Input struct {
	Repos          []RepoData
	Sessions       []agentview.Session
	DispatchPaused bool
}
func BuildBoard(in Input) Board // fills everything except CollectedAt, IntervalsSeconds, Errors
```

Rules `BuildBoard` must implement (per issue, first match wins):
1. `🧊 parked` or `roadmap` → dropped.
2. `enrichment-ongoing` → Running, `KindEnrichment`, `Since = issue.Updated`, `Status = "enriching"`.
3. `needs-enrichment` or `❓ to-be-defined` → NeedsEnrichment (all forges).
4. `ai-implement` → not a column (it shows via its run).
5. Only for `Forge == "github" && Wired`: `Eligible(i, ActiveMilestone(ms), prs)`; ok → Ready; reason `outside active milestone` / `attempts exhausted` / `open PR` → Held with that Reason; any other reason → dropped.

Runs: `Status` in {`queued`,`in_progress`} and `Event == "issues"` → Running `KindPipeline`, `Since = Created`, `URL = run.URL`, `Title = run.Name`; set `Issue` (and use its title) only when the repo has exactly one open issue labelled `ai-implement`.
Sessions: every session → Running `KindSession`; `Repo` = repo whose `Path` is the longest prefix of `CWD` on a path boundary; `Title = Name`, `Status = Status`, `Since = StartedAt`.
Sort: Running by `Since` ascending; Ready via `dispatch.Order(candidates, nil)`; Held and NeedsEnrichment by issue `Created` ascending. All slices non-nil (`[]`, never `null` in JSON).

- [ ] **Step 1: Write the failing tests** — one table-driven test per rule group:
  - `TestBuildBoard_ParkedAndRoadmap_Dropped`
  - `TestBuildBoard_EnrichmentOngoing_OnlyInRunning` (issue has both `needs-enrichment` and `enrichment-ongoing` → `len(Running)==1`, `len(NeedsEnrichment)==0`)
  - `TestBuildBoard_ToBeDefined_InNeedsEnrichment_AllForges` (a `forgejo` issue included)
  - `TestBuildBoard_EligibleOnWiredGithub_InReady` and `..._UnwiredRepo_NotInReady`
  - `TestBuildBoard_OutsideMilestone_InHeldWithReason` (`Reason == "outside active milestone"`)
  - `TestBuildBoard_InProgressIssuesRun_RunningLinkedToSingleAIImplementIssue` and `..._TwoAIImplementIssues_BareRun` (`Issue == 0`)
  - `TestBuildBoard_CompletedRun_NotRunning`
  - `TestBuildBoard_SessionInWorktree_AttributedToRepo` (`CWD = "/r/bridge/.worktrees/webui"`, repo Path `/r/bridge`, sibling repo `/r/bridge-bot` must **not** match) and session with `CWD="/tmp"` → `Repo == ""`
  - `TestBuildBoard_EmptyInput_SlicesNotNil` (marshal to JSON, assert contains `"running":[]`)
- [ ] **Step 2: Run** `go test ./internal/cockpit` → FAIL (package missing).
- [ ] **Step 3: Implement** `board.go` (package doc comment: "Package cockpit builds the WebUI cockpit's board from forge data and local sessions."). Keep functions ≤20 lines: one classifier per source.
- [ ] **Step 4: Run** `go test -race ./internal/cockpit` → PASS.
- [ ] **Step 5: Commit** `feat(cockpit): build the pipeline board from forge data and sessions`

### Task 4: `cockpit.Collector` — background refresh with last-good snapshots

**Files:**
- Create: `internal/cockpit/collector.go`
- Test: `internal/cockpit/collector_test.go`

**Interfaces:**
- Consumes: `BuildBoard`, `Input`, `RepoData`, `Board`, `SourceError` (Task 3); `core.Repo`.
- Produces:

```go
type Sources struct {
	Repos      func() ([]core.Repo, error)
	Wired      func(r core.Repo) bool // local check: .github/workflows/agent.yml or agent-implement.yml exists under r.Path
	Issues     func(ctx context.Context, r core.Repo) ([]forge.Issue, error)
	PRs        func(ctx context.Context, r core.Repo) ([]forge.PullRequest, error)
	Milestones func(ctx context.Context, r core.Repo) ([]forge.Milestone, error)
	Runs       func(ctx context.Context, r core.Repo) ([]forge.WorkflowRun, error)
	Sessions   func(ctx context.Context) ([]agentview.Session, error)
	Paused     func() bool
}
type Intervals struct{ Issues, Runs, Sessions time.Duration }
func DefaultIntervals() Intervals // Issues 10*time.Minute, Runs 60*time.Second, Sessions 5*time.Second
type Collector struct{ /* mutex-guarded state */ }
func NewCollector(src Sources, iv Intervals, now func() time.Time, onChange func()) *Collector
func (c *Collector) Run(ctx context.Context)               // tickers per interval; collects all three once at start; returns on ctx.Done
func (c *Collector) Board() Board                          // copy of the latest board, CollectedAt/IntervalsSeconds/Errors filled
func (c *Collector) CollectIssues(ctx context.Context)
func (c *Collector) CollectRuns(ctx context.Context)
func (c *Collector) CollectSessions(ctx context.Context)
func (c *Collector) CollectRepo(ctx context.Context, owner, name string) // issues+PRs+milestones+runs for one repo, after an action
```

Rate-limit rules (the reason for the shape — keep them):
- `CollectIssues`: `Issues` for every repo; `PRs` + `Milestones` **only** for wired GitHub repos that have at least one issue passing label rules 1–4 (a Ready/Held candidate).
- `CollectRuns`: `Runs` **only** for repos with an open issue labelled `ai-implement` (from the last issues snapshot).
- Fan-out bounded to 4 concurrent repo fetches (semaphore channel).
- A failing repo fetch keeps that repo's previous `RepoData`, records `SourceError{Source: "issues owner/name", …}` (cleared on next success), and does not fail the rest.
- `onChange` fires only when the board minus `CollectedAt` differs from the previous one (compare JSON).

- [ ] **Step 1: Write the failing tests** with fake `Sources` (closures over counters/maps):
  - `TestCollector_CollectIssues_PRsOnlyForWiredReposWithCandidates` (count PRs calls: wired+candidate =1, wired-no-candidate =0, unwired =0)
  - `TestCollector_CollectRuns_OnlyReposWithAIImplementIssue`
  - `TestCollector_RepoFetchFails_KeepsLastGoodData` (first collect OK with 1 ready issue; second collect `Issues` returns error → `Board().Ready` still has it, `Errors` has `"issues o/a"`; third collect OK → `Errors` empty)
  - `TestCollector_OnChange_FiresOnlyOnContentChange` (two identical collects with advancing clock → 1 call)
  - `TestCollector_Board_CollectedAtAndIntervalsFilled`
  - `TestCollector_FanOut_NeverExceedsFourConcurrent` (fake `Issues` tracks max in-flight with atomic counter + short sleep)
  - `TestCollector_Run_StopsOnContextCancel` (Run returns within 1s after cancel)
- [ ] **Step 2: Run** `go test ./internal/cockpit -run TestCollector` → FAIL.
- [ ] **Step 3: Implement** `collector.go`.
- [ ] **Step 4: Run** `go test -race ./internal/cockpit` → PASS.
- [ ] **Step 5: Commit** `feat(cockpit): collect board sources in the background with last-good snapshots`

### Task 5: `bridge launch --prompt` and the pipeline API

**Files:**
- Modify: `cmd/bridge/launch.go` (new `--prompt` flag)
- Test: `cmd/bridge/launch_test.go`
- Create: `internal/api/pipeline.go`
- Test: `internal/api/pipeline_test.go`
- Modify: `cmd/bridge/serve.go` (collector wiring, routes, `requireSameOrigin`, SSE)
- Test: `cmd/bridge/serve_test.go` (create if absent) for `requireSameOrigin`

**Interfaces:**
- Consumes: `cockpit.Collector`, `cockpit.Board`, `cockpit.NewCollector`, `cockpit.DefaultIntervals` (Task 4); `requireBearer` (existing, `cmd/bridge/serve.go:68`); `web.Event`, `hub.Broadcast`.
- Produces:

```go
// internal/api/pipeline.go
type LaunchRequest struct {
	Repo   string `json:"repo"`   // "owner/name"
	Number int    `json:"number"`
}
type LaunchResult struct {
	Slot           string `json:"slot"`
	AlreadyRunning bool   `json:"alreadyRunning"`
	RCURL          string `json:"rcUrl,omitempty"`
}
type PipelineHandler struct {
	Board    func() cockpit.Board
	Refresh  func(ctx context.Context)
	Enrich   func(ctx context.Context, req LaunchRequest) (LaunchResult, error)
	Dispatch func(ctx context.Context, req LaunchRequest) (LaunchResult, error)
}
// Routes: GET /api/pipeline · POST /api/pipeline/refresh · POST /api/pipeline/enrich · POST /api/pipeline/dispatch
// cmd/bridge/serve.go
func requireSameOrigin(next http.Handler) http.Handler // 403 unless Origin header present and its host == r.Host
```

Behaviour:
- `--prompt <text>`: claude-only (else exit 2 like `--rc`); appended as the last arg of the claude invocation (an initial prompt).
- `Enrich`: request must match a card in `NeedsEnrichment` with `forge == "github"` (headless enrich is GitHub-only) → else 409 `{"error":"not on the board"}` / 422 `{"error":"headless enrich is GitHub-only"}`. Launches `bridge launch <name> -w enrich-<N> --agent claude --rc --rc-wait 15s --json --prompt "/enrich <N> --headless"`.
- `Dispatch`: request must match a card in `Ready` → else 409. Launches `… -w dispatch-<N> … --prompt "/gh:implement <N>"`.
- Launch = exec of `os.Executable()` with those args; parse its `--json` into `LaunchResult`; then `collector.CollectSessions` + `collector.CollectRepo`.
- Validation: `Number > 0`, `Repo` has exactly one `/` → else 400.
- Routing in `serve.go`: GET open; the three POSTs wrapped `requireBearer(apiToken, requireSameOrigin(h))`. Collector `onChange` → `hub.Broadcast(web.Event{Type: "pipeline-updated"})`. Start `collector.Run(ctx)` in a goroutine owned by `runServe`'s ctx. Repos source = `reposWithMeta`; Issues/PRs/Milestones/Runs via `clientFor(r.Forge)` (PRs/Milestones/Runs GitHub-only: type-assert `*forge.GithubClient`, nil otherwise, as `fetchRepoInputs` in `dispatch.go` does); Paused from the dispatch state file (`dispatchStatePath()`).
- Note: `bridge launch` resolves the repo by **basename**. If two repos share a basename, return 409 `{"error":"ambiguous repo name"}` instead of launching the wrong one (check against `reposWithMeta`).

- [ ] **Step 1: Write the failing tests**
  - `launch_test.go`: `TestLaunch_PromptFlag_AppendedToClaudeArgs`, `TestLaunch_PromptWithNonClaudeAgent_Exit2` (follow the file's existing `launchEnv` fake-binary style).
  - `pipeline_test.go`: `TestPipelineHandler_GetBoard_ReturnsJSON`, `TestPipelineHandler_EnrichNotOnBoard_409`, `TestPipelineHandler_EnrichForgejoIssue_422`, `TestPipelineHandler_DispatchReadyIssue_CallsDispatch`, `TestPipelineHandler_BadNumber_400`, `TestPipelineHandler_LaunchAlreadyRunning_Returns200WithFlag`, `TestPipelineHandler_WrongMethod_405`.
  - `serve_test.go`: `TestRequireSameOrigin_MissingOrigin_403`, `TestRequireSameOrigin_ForeignOrigin_403`, `TestRequireSameOrigin_SameHost_PassesThrough`.
- [ ] **Step 2: Run** `go test ./cmd/bridge ./internal/api` → new tests FAIL.
- [ ] **Step 3: Implement** the flag, the handler, `requireSameOrigin` and the serve wiring.
- [ ] **Step 4: Run** full Go gate (`gofmt -l .` empty, `go vet ./...`, `golangci-lint run`, `go test -race ./...`) → all PASS.
- [ ] **Step 5: Smoke** (tmux-free, no TUI): `go run ./cmd/bridge serve --port 7799 &` then `curl -s localhost:7799/api/pipeline | jq '.collectedAt'` shows three keys within ~10 s; kill it.
- [ ] **Step 6: Commit** `feat(api): serve the pipeline board and token-guarded enrich/dispatch actions`

### Task 6: Svelte shell + Pipeline board

Blocked by Task 1 approval. Build to the approved wireframe and flow; where they differ from this task, they win.

**Files:**
- Modify: `web/src/lib/api.js` (`post(path, body, token)` sends `Authorization: Bearer <token>` when given)
- Create: `web/src/lib/token.js` (`getToken(): string|null`, `setToken(t)`, `clearToken()`; localStorage wrapped in try/catch)
- Create: `web/src/lib/stores/pipeline.js` (`pipeline` writable, `loadPipeline()`, reload on SSE `pipeline-updated`)
- Create: `web/src/lib/components/FreshnessBadge.svelte` (props `collectedAt`, `intervalSeconds`, `now`)
- Create: `web/src/lib/components/RunningCard.svelte`, `IssueCard.svelte`, `ConfirmDialog.svelte`
- Create: `web/src/routes/Pipeline.svelte`
- Modify: `web/src/App.svelte` (shell: header + area nav; Pipeline is the landing view; the old Agents list is replaced by Running; keep Architecture reachable from the nav)
- Test: `web/src/lib/components/*.test.js`, `web/src/routes/Pipeline.test.js`, update `web/src/App.test.js`

- [ ] **Step 1: Write the failing tests** (vitest + @testing-library/svelte):
  - `FreshnessBadge warns when collectedAt is older than 2x interval` (collectedAt = now − 130 s, interval 60 → has warning class/text) and `shows age without warning when fresh`.
  - `ConfirmDialog names repo, issue and the action` (text contains `freaxnx01/bridge`, `#42`, and "starts a paid Claude session").
  - `Pipeline renders the three columns from the store` with a fixture board; `hides the Held list until expanded`; `shows dispatch-paused note when dispatchPaused`.
  - `Dispatch asks for a token when none is stored` (no token → token prompt, not the POST).
  - `shows "already running" when the action returns alreadyRunning` and `shows an rcUrl link when returned`.
  - `Refresh button posts /api/pipeline/refresh with the token`.
  - `App lands on the Pipeline view` (update the existing App test accordingly; keep its Architecture assertion reachable via nav).
- [ ] **Step 2: Run** `cd web && npm test -- --run` → new tests FAIL.
- [ ] **Step 3: Implement** shell → stores → components → actions → styling, in that order.
- [ ] **Step 4: Run** `cd web && npm test -- --run` → PASS; `just web-build` succeeds; `go test -race ./...` still PASS (embedded dist).
- [ ] **Step 5: Run `/ui:review`** against the wireframe and flow; fix findings.
- [ ] **Step 6: Commit** `feat(web): cockpit shell with the pipeline board`

### Task 7: Docs, changelog, and rollout

**Files:**
- Create: `docs/cockpit.md` (what it is, endpoints and JSON shape, labels it reads, intervals, auth)
- Modify: `README.md` (one line under CLI surface pointing to `bridge serve` cockpit + `docs/cockpit.md`)
- Modify: `CHANGELOG.md` (`[Unreleased]`: Added cockpit pipeline board, pipeline API, `launch --prompt`; Changed: dispatch requires an implementation plan)
- Modify: `TODO.md` (replace the "Bridge WebUI — Plan 2" block's overview items with a pointer to the spec's roadmap panels)

- [ ] **Step 1:** Write the docs above.
- [ ] **Step 2: Commit** `docs(web): document the cockpit and its API`
- [ ] **Step 3: Rollout (operator steps, listed in the PR body, not automated):** `just build`; restart `bridge-serve.service`; add the Traefik route `bridge.home.freaxnx01.ch → 192.168.1.108:7777` behind the Authentik forward-auth middleware via the `homelab-service-routing` skill; open the page, enter `BRIDGE_API_TOKEN` once, click Enrich on one GitHub issue and confirm a session appears in Running with a Remote Control link.

Note for the reviewer: `bridge-serve.service` binds `0.0.0.0:7777` (FlowHub on CT 136 needs `/api/capture`), so the page is also reachable directly on the LAN without Authentik. Reads there are unauthenticated, as today; writes still need token + same origin. The spec's "keeps binding 127.0.0.1" line is wrong about the current deployment and is corrected in Task 7's docs.
