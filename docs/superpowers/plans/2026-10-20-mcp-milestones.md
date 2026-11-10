# MCP Milestones Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let the bridge MCP read and set milestones on GitHub and Forgejo: a `list_milestones` tool, a `milestone` input on `create_issue`, a new `set_milestone` tool (assign/change/clear), and a `milestone` filter on `list_issues`.

**Architecture:** Forge clients (`internal/forge`) gain milestone methods that speak each forge's dialect (GitHub milestone *number*, Forgejo milestone *id* — both exposed as `forge.Milestone.Number`). One pure resolver in `internal/mcp/milestone.go` turns a caller's title-or-number reference into a `forge.Milestone`, and every MCP handler that accepts a milestone goes through it, against the repo's milestones of **all** states. New capability interfaces live beside the existing ones in `internal/mcp/tools.go`.

**Tech Stack:** Go, stdlib `net/http` + `httptest`, `github.com/modelcontextprotocol/go-sdk/mcp`, hand-rolled fakes.

**Spec:** GitHub issue freaxnx01/bridge#349 (body is the spec). Decisions taken in session: `set_milestone` is a new tool (not an `update_issue` extension); the optional `list_issues` milestone filter is **in** scope.

## Global Constraints

- Use Test-Driven Development for every task: write a failing test first, watch it fail, implement minimally to pass, verify green.
- Hand-rolled fakes only — no testify/mockery/gomock. Table-driven tests with `t.Run` where there are several cases. Test names `TestFunc_State_Expected` style matching neighbours.
- No new Go modules. No `//nolint`. Wrap errors with `%w`, lower-case messages.
- Every commit message references the issue, e.g. `feat(forge): list milestones on github and forgejo (#349)`, and ends with the session's `Co-Authored-By`/`Claude-Session` trailers.
- After each task: `gofmt -l .` empty, `go vet ./...` clean, `go test -race ./...` green (full suite).
- Write tools keep the draft/confirm pattern: without `confirm: true` nothing is written. A draft may *read* (to resolve a milestone) but only when a milestone was supplied — existing drafts without one must stay network-free.
- Forge facts (verified against Forgejo swagger 16.0 / GitHub REST docs):
  - GitHub: `GET /repos/{o}/{r}/milestones?state=open|closed|all&per_page=100&page=N`; issues take `"milestone": <number>`; clearing is `"milestone": null`; issue list filter `?milestone=<number>`.
  - Forgejo: `GET /api/v1/repos/{o}/{r}/milestones?state=open|closed|all&limit=50&page=N` (field `id`, plus `open_issues`, `closed_issues`, `state`, `due_on`); issues take `"milestone": <id>`; clearing is `"milestone": 0` (the field is a nullable int64, so `null` means "unchanged"); issue list filter `?milestones=<id>`.

## Review Focus

1. A milestone whose **title is numeric** (e.g. `"2026"`) referenced as `"2026"` → the title match wins over a number match. (Task 5 test.)
2. **Clearing on Forgejo** sends `"milestone": 0`, not `null`; on GitHub sends JSON `null`. Pinned by request-body assertions. (Task 3 tests.)
3. A **closed milestone** referenced by title resolves (lookups use `state=all`). (Task 5 + Task 7 tests.)
4. A reference with **different case / surrounding whitespace** resolves when unique; two case-insensitive matches are ambiguous and the error lists both with numbers. (Task 5 tests.)
5. A **read-only** server registers `list_milestones` but not `set_milestone`, and `list_git_forges` does not advertise `set_milestone`. (Task 8 test.)

---

### Task 1: forge — list milestones (all states, paginated, with counts)

**Files:**
- Modify: `internal/forge/client.go` (Milestone struct, new `maxMilestonePages` const)
- Modify: `internal/forge/github.go` (`ListMilestones`; `ListOpenMilestones` delegates)
- Modify: `internal/forge/forgejo.go` (`ListMilestones`)
- Test: `internal/forge/github_test.go`, `internal/forge/forgejo_test.go`

**Interfaces:**
- Produces:
  ```go
  // Milestone is a repo milestone. Number is the value the forge's issues API
  // takes: GitHub's milestone number, Forgejo's milestone id. DueOn is zero when unset.
  type Milestone struct {
      Number       int       `json:"number"`
      Title        string    `json:"title"`
      State        string    `json:"state,omitempty"`
      DueOn        time.Time `json:"due_on,omitempty"`
      OpenIssues   int       `json:"open_issues"`
      ClosedIssues int       `json:"closed_issues"`
  }
  const maxMilestonePages = 10
  func (c *GithubClient) ListMilestones(ctx context.Context, owner, repo, state string) ([]Milestone, error)
  func (c *ForgejoClient) ListMilestones(ctx context.Context, owner, repo, state string) ([]Milestone, error)
  ```
  `state` is passed through verbatim (`open`/`closed`/`all`); validation is the MCP handler's job.

- [ ] **Step 1: Write failing tests**
  - `TestGithubListMilestones_AllStates_PaginatesAndMapsCounts`: httptest server asserts `state=all`, `per_page=100`; page 1 returns 100 items (generate in a loop) and page 2 returns 1 item `{"number":7,"title":"old","state":"closed","open_issues":0,"closed_issues":4,"due_on":null}`; assert `len == 101`, last item `Number 7, State "closed", ClosedIssues 4`, zero `DueOn`.
  - `TestForgejoListMilestones_AllStates_PaginatesAndMapsID`: asserts path `/api/v1/repos/o/r/milestones`, `state=all`, `limit=50`; page 1 returns 50 items, page 2 returns `[{"id":12,"title":"v1","state":"open","open_issues":3,"closed_issues":1,"due_on":"2026-11-01T00:00:00Z"}]`; assert `len == 51`, last item `Number 12`, `OpenIssues 3`, `DueOn` 2026-11-01.
  - Keep `TestGithubListOpenMilestones` passing unchanged (it asserts `state=open`).
- [ ] **Step 2:** `go test ./internal/forge -run 'Milestones'` → FAIL (undefined `ListMilestones`).
- [ ] **Step 3:** Implement. Loop pages until a short page (fewer than page size) or `maxMilestonePages`. `ListOpenMilestones` becomes `return c.ListMilestones(ctx, owner, repo, "open")`.
- [ ] **Step 4:** `go test -race ./internal/forge ./cmd/bridge` → PASS.
- [ ] **Step 5:** Commit `feat(forge): list milestones on github and forgejo (#349)`.

---

### Task 2: forge — create issue with a milestone

**Files:**
- Modify: `internal/forge/github.go`, `internal/forge/forgejo.go` (`CreateIssue` signature)
- Modify: `internal/capture/capture.go:78,92` (interface + call passes `0`)
- Modify (fakes/calls only, to the new signature): `internal/capture/capture_test.go`, `internal/api/capture_test.go`, `internal/mcp/tools_test.go`, `internal/mcp/tools.go` (`issueCreator`), `internal/mcp/tools_write.go:57` (pass `0` for now), `cmd/bridge/mcp_test.go:34` (its fake has a stale signature — bring it in line), all `CreateIssue(` calls in `internal/forge/*_test.go`
- Test: `internal/forge/github_test.go`, `internal/forge/forgejo_test.go`

**Interfaces:**
- Produces: `CreateIssue(ctx context.Context, owner, repo, title, body string, labels []string, milestone int) (Issue, error)` on both clients and on the `issueCreator` / capture interfaces. `milestone == 0` means none and the `milestone` key is **omitted** from the request body.

- [ ] **Step 1: Failing tests** — `TestGithubCreateIssue_WithMilestone_SendsNumber` (decoded request body has `"milestone": 5`), `TestForgejoCreateIssue_WithMilestone_SendsID` (`"milestone": 12`), and extend one existing create test per forge to assert the body has **no** `milestone` key when `0`.
- [ ] **Step 2:** `go test ./internal/forge -run CreateIssue` → FAIL (compile: too many arguments).
- [ ] **Step 3:** Implement; update every caller/fake listed above. Capture keeps creating without a milestone.
- [ ] **Step 4:** `go build ./... && go vet ./... && go test -race ./...` → PASS.
- [ ] **Step 5:** Commit `feat(forge): accept a milestone when creating issues (#349)`.

---

### Task 3: forge — set or clear an issue's milestone

**Files:**
- Modify: `internal/forge/github.go`, `internal/forge/forgejo.go`
- Test: `internal/forge/github_test.go`, `internal/forge/forgejo_test.go`

**Interfaces:**
- Produces:
  ```go
  // SetMilestone assigns milestone (GitHub number / Forgejo id) to
  // owner/repo#number; 0 clears it. The returned Issue's Milestone is the
  // title the forge reports after the change ("" when cleared).
  func (c *GithubClient) SetMilestone(ctx context.Context, owner, repo string, number, milestone int) (Issue, error)
  func (c *ForgejoClient) SetMilestone(ctx context.Context, owner, repo string, number, milestone int) (Issue, error)
  ```
  Both PATCH the issue endpoint used by `UpdateIssue`, and parse `milestone.title` (nullable object) from the response.

- [ ] **Step 1: Failing tests** (table-driven per forge, `TestGithubSetMilestone` / `TestForgejoSetMilestone`), asserting the raw decoded request body (`map[string]any`):
  - assign: GitHub `{"milestone": 5}` → response with `"milestone":{"number":5,"title":"v2"}` → `Issue.Milestone == "v2"`.
  - clear: GitHub body has key `milestone` with value `nil` (key present!) → response `"milestone":null` → `Issue.Milestone == ""`.
  - assign: Forgejo `{"milestone": 12}` → `"milestone":{"id":12,"title":"v1"}` → `"v1"`.
  - clear: Forgejo body `{"milestone": 0}` (float64 0 after decode, key present).
  - PATCH path is `/repos/o/r/issues/42` (GitHub) and `/api/v1/repos/o/r/issues/42` (Forgejo).
- [ ] **Step 2:** `go test ./internal/forge -run SetMilestone` → FAIL.
- [ ] **Step 3:** Implement. GitHub: `req := map[string]any{"milestone": nil}` and set the number when non-zero.
- [ ] **Step 4:** `go test -race ./internal/forge` → PASS.
- [ ] **Step 5:** Commit `feat(forge): set and clear issue milestones (#349)`.

---

### Task 4: forge — list open issues filtered by milestone; Forgejo milestone titles

**Files:**
- Modify: `internal/forge/github.go`, `internal/forge/forgejo.go` (`fjIssue` gains `Milestone *struct{ Title string }`, mapped in `ListOpenIssues` too)
- Test: `internal/forge/github_test.go`, `internal/forge/forgejo_test.go`

**Interfaces:**
- Produces:
  ```go
  func (c *GithubClient) ListOpenIssuesInMilestone(ctx context.Context, owner, repo string, milestone int) ([]Issue, error)
  func (c *ForgejoClient) ListOpenIssuesInMilestone(ctx context.Context, owner, repo string, milestone int) ([]Issue, error)
  ```
  Same result shape and page size as each forge's `ListOpenIssues` (extract a shared helper taking the extra query string rather than duplicating the mapping). Filtering is server-side: GitHub `&milestone=<n>`, Forgejo `&milestones=<id>`. `ListOpenIssues` (tier-1 `ForgeReader`) keeps its signature.

- [ ] **Step 1: Failing tests** — `TestGithubListOpenIssuesInMilestone_SendsNumberFilter` (query `milestone=5`, `state=open`), `TestForgejoListOpenIssuesInMilestone_SendsIDFilter` (query `milestones=12`, `type=issues`), and `TestForgejoListOpenIssues_MapsMilestoneTitle` (one issue with `"milestone":{"id":1,"title":"v1"}`, one with `null` → `"v1"` and `""`).
- [ ] **Step 2:** run → FAIL.
- [ ] **Step 3:** Implement.
- [ ] **Step 4:** `go test -race ./internal/forge ./cmd/bridge` → PASS.
- [ ] **Step 5:** Commit `feat(forge): filter open issues by milestone (#349)`.

---

### Task 5: mcp — milestone reference resolver

**Files:**
- Create: `internal/mcp/milestone.go`
- Test: `internal/mcp/milestone_test.go`

**Interfaces:**
- Produces:
  ```go
  var (
      ErrMilestoneNotFound  = errors.New("milestone not found")
      ErrMilestoneAmbiguous = errors.New("milestone reference is ambiguous")
  )
  // resolveMilestone picks the milestone ref names, by title or number.
  func resolveMilestone(ms []forge.Milestone, ref string) (forge.Milestone, error)
  ```
  Order (first rule that yields exactly one wins; a rule yielding >1 is ambiguous):
  1. exact title match on `strings.TrimSpace(ref)`;
  2. case-insensitive title match (`strings.EqualFold`);
  3. `ref` parses (`strconv.Atoi`) to a positive int equal to some `Number`.
  Not found → `fmt.Errorf("%w: %q (available: %s)", ErrMilestoneNotFound, ref, <titles joined ", ", or "none">)`. Ambiguous → `fmt.Errorf("%w: %q matches %s", ErrMilestoneAmbiguous, ref, <"title (#n, state)" joined ", ">)`. Empty ref → not found.

- [ ] **Step 1: Failing table test `TestResolveMilestone`** over `ms := {1 "v1" open}, {2 "2026" closed}, {3 "Backlog" open}, {4 "backlog" closed}, {5 "Test-A" open}`:
  - `"v1"` → 1; `" v1 "` → 1; `"V1"` → 1 (case-insensitive unique); `"2026"` → 2 (title beats number); `"3"` → 3 (number); `"Backlog"` → 3 (exact beats case-insensitive); `"BACKLOG"` → `ErrMilestoneAmbiguous`, message contains `"Backlog (#3, open)"` and `"backlog (#4, closed)"`; `"nope"` → `ErrMilestoneNotFound`, message contains `"v1"`; `"99"` → not found; `""` → not found. Use `errors.Is`.
  - Empty list + `"v1"` → not found, message contains `"none"`.
- [ ] **Step 2:** `go test ./internal/mcp -run TestResolveMilestone` → FAIL.
- [ ] **Step 3:** Implement (small helpers per rule; ≤20 lines each).
- [ ] **Step 4:** PASS.
- [ ] **Step 5:** Commit `feat(mcp): resolve milestone references by title or number (#349)`.

---

### Task 6: mcp — `list_milestones` tool + capability wiring

**Files:**
- Modify: `internal/mcp/tools.go` (interfaces, `Capabilities`), `internal/mcp/milestone.go` (handler + `lookupMilestone`), `internal/mcp/server.go` (register outside the read-only block)
- Test: `internal/mcp/milestone_test.go`, `internal/mcp/tools_test.go` (fakes), `internal/mcp/server_test.go` (both `want` lists), `internal/mcp/tools_read_test.go` if its capability list needs it

**Interfaces:**
- Consumes: `ListMilestones` (Task 1), `resolveMilestone` (Task 5).
- Produces:
  ```go
  type milestoneLister interface {
      ListMilestones(ctx context.Context, owner, repo, state string) ([]forge.Milestone, error)
  }
  type milestoneSetter interface { // used by Task 8
      SetMilestone(ctx context.Context, owner, repo string, number, milestone int) (forge.Issue, error)
  }
  type milestoneIssueLister interface { // used by Task 9
      ListOpenIssuesInMilestone(ctx context.Context, owner, repo string, milestone int) ([]forge.Issue, error)
  }
  // lookupMilestone resolves ref against owner/repo's milestones of all states.
  func (d Deps) lookupMilestone(ctx context.Context, forgeName, owner, repo, ref string) (forge.Milestone, error)
  type listMilestonesInput struct { Forge, Owner, Repo, State string } // json: forge, owner, repo, state(omitempty)
  type listMilestonesOutput struct { Milestones []forge.Milestone `json:"milestones"` }
  func (d Deps) handleListMilestones(ctx context.Context, _ *mcp.CallToolRequest, in listMilestonesInput) (*mcp.CallToolResult, listMilestonesOutput, error)
  ```
  `Capabilities` appends `"list_milestones"` for `milestoneLister` and `"set_milestone"` for `milestoneSetter`, placed right after `"get_issue"` in that order. `lookupMilestone` errors: forge not configured / `forge %q does not support milestones` / list error wrapped / resolver error wrapped.
  Tool description: `"List a repo's milestones: number (GitHub milestone number; Forgejo milestone id — the value the issues API takes), title, state, due date, open/closed issue counts. state: open (default), closed or all."`

- [ ] **Step 1: Failing tests**
  - `TestHandleListMilestones` (table): default state passes `"open"` to the fake; `"all"` passes through; `"bogus"` → error containing `open, closed or all` and the fake is not called; unconfigured forge → error; client without `milestoneLister` → error `does not support milestones`.
  - `TestCapabilities_RealClients_ReportMilestoneTools`: `Capabilities(forge.NewGithubClient("t",""))` and `Capabilities(forge.NewForgejoClient("t",""))` both contain `list_milestones` and `set_milestone` (guards against silent interface drift).
  - Update `server_test.go` expected tool names: full list gains `list_milestones` **and** `set_milestone` (Task 8 registers it — add `set_milestone` to the full list in Task 8, not here); read-only list gains `list_milestones`.
- [ ] **Step 2:** run → FAIL.
- [ ] **Step 3:** Implement and register.
- [ ] **Step 4:** `go test -race ./internal/mcp ./cmd/bridge` → PASS.
- [ ] **Step 5:** Commit `feat(mcp): add list_milestones tool (#349)`.

---

### Task 7: mcp — `create_issue` milestone input

**Files:**
- Modify: `internal/mcp/tools_write.go` (`createIssueInput`, `createIssueOutput`, handler), `internal/mcp/server.go` (description)
- Test: `internal/mcp/tools_write_test.go`, fakes in `internal/mcp/tools_test.go`

**Interfaces:**
- Consumes: `lookupMilestone` (Task 6), `CreateIssue(..., milestone int)` (Task 2).
- Produces: input `Milestone string \`json:"milestone,omitempty" jsonschema:"optional milestone title or number; resolved against the repo's milestones (any state)"\``; output `Milestone *forge.Milestone \`json:"milestone,omitempty"\``.
  Description becomes `"Create an issue, optionally in a milestone (title or number). Without confirm=true this returns a draft and creates nothing; the draft shows the resolved milestone."`

- [ ] **Step 1: Failing tests**
  - draft with `milestone: "v1"` → `out.Milestone.Number == 1`, create not called.
  - draft with unknown milestone → error wrapping `ErrMilestoneNotFound`, create not called.
  - confirm with `milestone: "old"` (a **closed** milestone in the fake) → fake receives milestone number of `"old"`.
  - confirm without milestone → fake receives `0`, and the fake's `ListMilestones` was **not** called (count it).
- [ ] **Step 2:** run → FAIL.
- [ ] **Step 3:** Implement: resolve before building the draft when `in.Milestone != ""`.
- [ ] **Step 4:** `go test -race ./internal/mcp` → PASS.
- [ ] **Step 5:** Commit `feat(mcp): set a milestone when creating an issue (#349)`.

---

### Task 8: mcp — `set_milestone` tool

**Files:**
- Modify: `internal/mcp/milestone.go` (handler), `internal/mcp/server.go` (register inside `!deps.ReadOnly`), `internal/mcp/tools_read.go` (`isWriteTool` gains `"set_milestone"`)
- Test: `internal/mcp/milestone_test.go`, `internal/mcp/server_test.go`, `internal/mcp/tools_read_test.go` (read-only `list_git_forges` case)

**Interfaces:**
- Consumes: `milestoneSetter`, `lookupMilestone`.
- Produces:
  ```go
  type setMilestoneInput struct {
      Forge, Owner, Repo string
      IssueNumber int    `json:"issue_number"`
      Milestone   string `json:"milestone,omitempty"` // title or number
      Clear       bool   `json:"clear,omitempty"`     // remove the milestone
      Confirm     bool   `json:"confirm,omitempty"`
  }
  type setMilestoneOutput struct {
      Draft bool; Forge, Owner, Repo string; IssueNumber int
      Milestone *forge.Milestone `json:"milestone,omitempty"`
      Clear     bool             `json:"clear,omitempty"`
      Issue     *forge.Issue     `json:"issue,omitempty"`
  }
  func (d Deps) handleSetMilestone(ctx context.Context, _ *mcp.CallToolRequest, in setMilestoneInput) (*mcp.CallToolResult, setMilestoneOutput, error)
  ```
  Exactly one of `milestone` / `clear=true` is required, else error `exactly one of milestone or clear is required`. Audit `Tool: "set_milestone"` success/error like `update_issue`. Description: `"Assign, change or clear (clear=true) an issue's milestone, by milestone title or number. Without confirm=true this returns a draft showing the resolved milestone and changes nothing."`

- [ ] **Step 1: Failing tests** (`TestHandleSetMilestone`, table): both set → error; neither → error; draft assign → resolved milestone, setter not called; draft clear → `Clear: true`, no list/set call; confirm assign → setter got the number; confirm clear → setter got `0`; unknown title → `ErrMilestoneNotFound`; setter error → wrapped error and audit outcome error (if neighbours test audit, mirror them). `server_test.go` full list gains `set_milestone`; read-only list does not. Read-only `list_git_forges` does not advertise `set_milestone` while advertising `list_milestones`.
- [ ] **Step 2:** run → FAIL.
- [ ] **Step 3:** Implement and register.
- [ ] **Step 4:** `go test -race ./internal/mcp` → PASS.
- [ ] **Step 5:** Commit `feat(mcp): add set_milestone tool (#349)`.

---

### Task 9: mcp — `list_issues` milestone filter

**Files:**
- Modify: `internal/mcp/tools_read.go` (`listIssuesInput`, `handleListIssues`), `internal/mcp/server.go` (description)
- Test: `internal/mcp/tools_read_test.go`

**Interfaces:**
- Consumes: `lookupMilestone`, `milestoneIssueLister`.
- Produces: input `Milestone string \`json:"milestone,omitempty" jsonschema:"only issues in this milestone (title or number)"\``. With it: resolve, assert `milestoneIssueLister` (else `forge %q does not support filtering issues by milestone`), call `ListOpenIssuesInMilestone`. Without it: unchanged path. Body clearing applies to both paths. Description: `"List open issues for a single repository (live), optionally only those in one milestone (title or number)."`

- [ ] **Step 1: Failing tests** — filter by `"v1"` calls the filtered method with `1` and not `ListOpenIssues`; unknown milestone → `ErrMilestoneNotFound`; no filter → unchanged behaviour (existing tests keep passing); bodies are cleared on the filtered path.
- [ ] **Step 2:** run → FAIL.
- [ ] **Step 3:** Implement (extract a small helper so the handler stays ≤20 lines).
- [ ] **Step 4:** `go test -race ./...` → PASS.
- [ ] **Step 5:** Commit `feat(mcp): filter list_issues by milestone (#349)`.

---

### Task 10: docs + changelog

**Files:**
- Modify: `docs/mcp-cheatsheet.md` (tool table rows for `list_milestones`, `set_milestone`; `create_issue` and `list_issues` rows mention `milestone`; the `--read-only` row's list of omitted write tools gains `set_milestone`)
- Modify: `CHANGELOG.md` `[Unreleased]` → `Added`: one bullet naming the four changes, `(#349)`.

- [ ] **Step 1:** Edit both files, matching surrounding table/bullet style.
- [ ] **Step 2:** `gofmt -l .` empty; `go vet ./...`; `golangci-lint run`; `go test -race ./...` → all clean.
- [ ] **Step 3:** Commit `docs(mcp): document milestone tools (#349)`.

---

### After all tasks (controller, not a subagent)

Live smoke test with a locally built binary against one GitHub repo and one Forgejo repo the user designates: `list_milestones`, `create_issue` draft with milestone, `set_milestone` assign then `clear` — confirming Forgejo's `0` clears in practice. Report results; do not merge.
