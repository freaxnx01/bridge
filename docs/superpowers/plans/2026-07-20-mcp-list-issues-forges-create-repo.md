# MCP `list_issues`, `list_git_forges`, `create_repo` Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Expose three new MCP tools — `list_issues` (raw per-repo issue query), `list_git_forges` (zero-network discovery of configured targets and their capabilities), and `create_repo` (draft-by-default write) — on top of the capability interfaces already in `internal/mcp`.

**Architecture:** `internal/mcp/tools.go` splits along the boundary `Deps.ReadOnly` already gates: `tools.go` keeps the types and `targets()`, `tools_read.go` holds the five read handlers, `tools_write.go` the two write handlers. `list_issues` needs only `ForgeReader` (so it works on any wired forge, including GitLab and ADO once connected); `create_repo` asserts `repoCreator` and fails with *does not support creating repositories* otherwise. `list_git_forges` derives its `capabilities` field from the existing `Capabilities` helper, filtering write tools out under `ReadOnly` so it never advertises a tool the server did not register.

**Tech Stack:** Go (stdlib `testing`, hand-rolled fakes), `github.com/modelcontextprotocol/go-sdk/mcp`. No new dependencies.

**Spec:** `docs/superpowers/specs/2026-07-20-mcp-list-issues-forges-create-repo-design.md`

**Prerequisite (already landed, `f42bf56` on `main`):** `docs/superpowers/plans/2026-07-20-mcp-capability-interfaces.md` — `ForgeReader`, `fileReader`/`issueCreator`/`repoCreator`, and `Capabilities` all exist.

## Global Constraints

- **No new Go modules.** No `testify`, `mockery`, or `gomock` — hand-rolled fakes only.
- **No changes to `internal/forge`.** `*forge.GithubClient` and `*forge.ForgejoClient` already satisfy every capability these tools assert.
- **No changes to the capability interfaces or `Capabilities`.** This plan consumes them as-is.
- Never discard an error with `_ =`.
- Errors return up to the command layer — no `os.Exit` or stderr writes below `main`.
- Error wrap messages are lower-case with no trailing punctuation.
- Inspect wrapped errors with `errors.Is`, never string-matching on `err.Error()`.
- No package-level mutable global state (no mutable `var` maps or singletons).
- No `//nolint` suppressions.
- Every task ends green on: `gofmt -l .` (empty output), `go vet ./...`, `golangci-lint run`, `go test -race ./...`.
- `golangci-lint` is pinned to v2.1.6 (`go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.1.6` if absent).
- Run all commands from the repo root. The local `go` binary is 1.22.2 while `go.mod` requires 1.25, so the toolchain resolves only inside the repo.

## Spec Reconciliation (read before Task 1)

The spec's **Testing** section says to extend "the existing hand-rolled `fakeForge` (`tools_test.go:14`)" with `ListOpenIssues` and `CreateRepo`. That text predates the capability-interface refactor and is **stale**. `fakeForge` no longer exists. The current `internal/mcp/tools_test.go` already provides everything this plan needs:

| Fake | Relevant fields | Used by |
|---|---|---|
| `fakeReader` | `issues []forge.Issue`, `issuesErr error` | `list_issues` (Task 2) |
| `fakeRepos` | `createRepoCalled *int`, `createRepoErr error` | `create_repo` (Task 4) |
| `fakeFull` / `newFakeFull(name)` | composes all four capability structs | all tasks |

`fakeRepos.CreateRepo` already returns `forge.RepoRef{Forge: …, Owner: "token-owner", Name: name, Visibility: …}` — the hardcoded `"token-owner"` is exactly what Task 4 needs to prove the success response takes its owner from the returned `RepoRef` rather than the input. **No fake changes are required by this plan.** Those four fields are currently unexercised; this plan is what exercises them.

## File Structure

| File | Responsibility | Task |
|---|---|---|
| `internal/mcp/tools.go` | `Target`, `ForgeReader`, capability interfaces, `Capabilities`, `Deps`, `targets()` | 1 |
| `internal/mcp/tools_read.go` | `list_repos`, `read_file`, `cross_forge_status`, `list_issues`, `list_git_forges` handlers + I/O types | 1, 2, 3 |
| `internal/mcp/tools_write.go` | `create_issue`, `create_repo` handlers + I/O types | 1, 4 |
| `internal/mcp/tools_test.go` | fakes, `depsWith`, `TestCapabilities_*` | 1 |
| `internal/mcp/tools_read_test.go` | read handler tests | 1, 2, 3 |
| `internal/mcp/tools_write_test.go` | write handler tests | 1, 4 |
| `internal/mcp/server.go` | tool registration, reads then the `!ReadOnly` write block | 2, 3, 4 |
| `internal/mcp/server_test.go` | advertised tool-set assertions | 2, 3, 4 |

Each tool task owns its own registration and `server_test.go` expectation update, so every task ends with a tool that is actually reachable over MCP rather than a handler nothing calls.

---

### Task 1: Split `tools.go` and `tools_test.go` by read/write

A pure move. No handler logic, no test assertion, and no exported name changes — the suite must be green before and after with the identical set of tests. Doing this first keeps Tasks 2–4 reviewable as small additions rather than diffs against a 350-line file.

**Files:**
- Modify: `internal/mcp/tools.go` (remove the handlers and their I/O types)
- Create: `internal/mcp/tools_read.go`, `internal/mcp/tools_write.go`
- Modify: `internal/mcp/tools_test.go` (remove the handler tests)
- Create: `internal/mcp/tools_read_test.go`, `internal/mcp/tools_write_test.go`

**Interfaces:**
- Consumes: nothing new.
- Produces: no new identifiers. Tasks 2 and 3 append to `tools_read.go` / `tools_read_test.go`; Task 4 appends to `tools_write.go` / `tools_write_test.go`.

- [ ] **Step 1: Record the baseline test list**

Run: `go test ./internal/mcp/ -v 2>&1 | grep -c '^=== RUN'`
Write the number down. Step 6 must produce the identical count — that is the proof this was a pure move.

- [ ] **Step 2: Create `internal/mcp/tools_read.go`**

Move these from `tools.go` **verbatim**, in this order: `listReposInput`, `listReposOutput`, `readFileInput`, `readFileOutput`, `crossForgeStatusInput`, `handleListRepos` (with its doc comment), `handleReadFile`, `handleCrossForgeStatus`.

The file starts:

```go
package mcp

import (
	"context"
	"fmt"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/sync/errgroup"

	"github.com/freaxnx01/bridge/internal/forge"
	"github.com/freaxnx01/bridge/internal/overview"
)
```

- [ ] **Step 3: Create `internal/mcp/tools_write.go`**

Move `createIssueInput`, `createIssueOutput`, and `handleCreateIssue` from `tools.go` verbatim. The file starts:

```go
package mcp

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/freaxnx01/bridge/internal/forge"
)
```

- [ ] **Step 4: Trim `internal/mcp/tools.go`**

What remains, in order: the `Target` type, `ForgeReader`, `fileReader`, `issueCreator`, `repoCreator`, `Capabilities`, `Deps`, and `targets()`. Delete everything moved in Steps 2–3. The import block becomes:

```go
import (
	"context"
	"fmt"

	"github.com/freaxnx01/bridge/internal/forge"
	"github.com/freaxnx01/bridge/internal/overview"
)
```

`overview` is still needed by `Deps.BuildOverview`; `mcp`, `sync`, and `errgroup` are not — the compiler will tell you if you leave one behind.

- [ ] **Step 5: Split the test file**

Move test functions out of `tools_test.go` verbatim — bodies unchanged, not one assertion touched.

To `tools_read_test.go` (11 functions): `TestHandleListRepos_AggregatesDefaultOwners`, `TestHandleListRepos_ForgeFilterHonoured`, `TestHandleListRepos_OwnerInputOverridesDefaults`, `TestHandleListRepos_OwnerWithoutForgeIsRejected`, `TestHandleListRepos_UnconfiguredTargetReportsWarningNotSilentDrop`, `TestHandleListRepos_PartialFailureReturnsWarningAndSuccessfulResults`, `TestHandleListRepos_TierOneClientIsFullyUsable`, `TestHandleReadFile_FoundAndAbsent`, `TestHandleReadFile_UnknownForge`, `TestHandleReadFile_TierOneClientReportsUnsupportedNotUnconfigured`, `TestHandleCrossForgeStatus_DelegatesToBuild`, `TestHandleCrossForgeStatus_PropagatesError`.

To `tools_write_test.go` (3 functions): `TestHandleCreateIssue_DraftDoesNotCreate`, `TestHandleCreateIssue_ConfirmCreates`, `TestHandleCreateIssue_TierOneClientReportsUnsupportedNotUnconfigured`.

`tools_test.go` keeps: the fakes (`fakeReader`, `fakeFiles`, `fakeIssues`, `fakeRepos`, `fakeFull`, `newFakeFull`), `depsWith`, and `TestCapabilities_ReportsToolNamesPerCapability`.

Both new test files start `package mcp`. Set each file's imports to exactly what its contents use and let the compiler arbitrate — an unused import is a hard build error, so there is no guesswork:

```go
// tools_test.go
import (
	"context"
	"testing"

	"github.com/freaxnx01/bridge/internal/forge"
)

// tools_read_test.go
import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/freaxnx01/bridge/internal/forge"
	"github.com/freaxnx01/bridge/internal/overview"
)

// tools_write_test.go
import (
	"context"
	"strings"
	"testing"

	"github.com/freaxnx01/bridge/internal/forge"
)
```

- [ ] **Step 6: Verify the move changed nothing**

Run: `go test ./internal/mcp/ -v 2>&1 | grep -c '^=== RUN'`
Expected: the exact number from Step 1. If it differs, a test was dropped or duplicated — find it before continuing.

Then confirm no logic drifted: `git diff --stat` should show insertions and deletions roughly balanced across the six files, with no net new logic.

- [ ] **Step 7: Run the full suite and static checks**

Run: `go test -race ./... && gofmt -l . && go vet ./... && golangci-lint run`
Expected: all packages PASS; no output from `gofmt -l .`; no findings.

- [ ] **Step 8: Commit**

```bash
git add internal/mcp/
git commit -m "refactor(mcp): split tools by read/write boundary

tools.go keeps the types, Capabilities and targets(); handlers move to
tools_read.go and tools_write.go, splitting on the boundary Deps.ReadOnly
already gates. Test files split to match, with the fakes staying in
tools_test.go.

Pure move: no handler logic and no assertion changes."
```

---

### Task 2: `list_issues`

The raw per-repo issue query. Mirrors `handleReadFile`'s shape — single target, fail-fast, no partial-success warnings — but needs **no capability assertion**, because `ListOpenIssues` is part of `ForgeReader` itself.

**Files:**
- Modify: `internal/mcp/tools_read.go` (append)
- Modify: `internal/mcp/tools_read_test.go` (append)
- Modify: `internal/mcp/server.go`
- Modify: `internal/mcp/server_test.go`

**Interfaces:**
- Consumes: `Deps.ClientFor`, `ForgeReader.ListOpenIssues`; `fakeReader.issues` / `fakeReader.issuesErr` from the test fakes.
- Produces: `listIssuesInput{Forge, Owner, Repo string}`, `listIssuesOutput{Issues []forge.Issue}`, `(Deps).handleListIssues`.

- [ ] **Step 1: Write the failing tests**

Append to `internal/mcp/tools_read_test.go`:

```go
func TestHandleListIssues_ReturnsIssuesFromConfiguredForge(t *testing.T) {
	gh := newFakeFull("github")
	gh.issues = []forge.Issue{
		{Forge: "github", Repo: "freaxnx01/bridge", Number: 7, Title: "flaky test"},
	}
	d := depsWith(map[string]*fakeFull{"github": gh}, nil)

	_, out, err := d.handleListIssues(context.Background(), nil,
		listIssuesInput{Forge: "github", Owner: "freaxnx01", Repo: "bridge"})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Issues) != 1 {
		t.Fatalf("want 1 issue, got %+v", out.Issues)
	}
	if out.Issues[0].Number != 7 || out.Issues[0].Title != "flaky test" {
		t.Errorf("unexpected issue: %+v", out.Issues[0])
	}
}

func TestHandleListIssues_UnconfiguredForgeErrors(t *testing.T) {
	d := depsWith(map[string]*fakeFull{}, nil)

	_, _, err := d.handleListIssues(context.Background(), nil,
		listIssuesInput{Forge: "gitlab", Owner: "acme", Repo: "widget"})
	if err == nil {
		t.Fatal("want an error for an unconfigured forge, got nil")
	}
	if !strings.Contains(err.Error(), "gitlab") {
		t.Errorf("error must name the forge, got %v", err)
	}
	if !strings.Contains(err.Error(), "not configured") {
		t.Errorf("want a not-configured error, got %v", err)
	}
}

func TestHandleListIssues_ClientErrorPropagatesWrapped(t *testing.T) {
	sentinel := errors.New("token expired")
	gh := newFakeFull("github")
	gh.issuesErr = sentinel
	d := depsWith(map[string]*fakeFull{"github": gh}, nil)

	_, _, err := d.handleListIssues(context.Background(), nil,
		listIssuesInput{Forge: "github", Owner: "freaxnx01", Repo: "bridge"})
	if err == nil {
		t.Fatal("want the client error to propagate, got nil")
	}
	if !errors.Is(err, sentinel) {
		t.Errorf("want the sentinel preserved via %%w, got %v", err)
	}
	if !strings.Contains(err.Error(), "freaxnx01/bridge") {
		t.Errorf("want the repo path in the wrap, got %v", err)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/mcp/ -run TestHandleListIssues -v`
Expected: FAIL to compile — `undefined: listIssuesInput` and `d.handleListIssues undefined`.

- [ ] **Step 3: Implement the handler**

Append to `internal/mcp/tools_read.go`:

```go
type listIssuesInput struct {
	Forge string `json:"forge" jsonschema:"forge hosting the repo: github or forgejo"`
	Owner string `json:"owner" jsonschema:"repository owner"`
	Repo  string `json:"repo" jsonschema:"repository name"`
}

type listIssuesOutput struct {
	Issues []forge.Issue `json:"issues"`
}

// handleListIssues returns the open issues of a single repo. Scope is
// deliberately one repo rather than a fan-out across configured targets:
// cross_forge_status already aggregates, and fanning out here would multiply
// to repos × issues per call. Needs no capability assertion — ListOpenIssues
// is part of ForgeReader.
func (d Deps) handleListIssues(ctx context.Context, _ *mcp.CallToolRequest, in listIssuesInput) (*mcp.CallToolResult, listIssuesOutput, error) {
	client := d.ClientFor(in.Forge, in.Owner)
	if client == nil {
		return nil, listIssuesOutput{}, fmt.Errorf("forge %q not configured", in.Forge)
	}
	issues, err := client.ListOpenIssues(ctx, in.Owner, in.Repo)
	if err != nil {
		return nil, listIssuesOutput{}, fmt.Errorf("list issues %s/%s: %w", in.Owner, in.Repo, err)
	}
	return nil, listIssuesOutput{Issues: issues}, nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/mcp/ -run TestHandleListIssues -v`
Expected: PASS, all three tests.

- [ ] **Step 5: Register the tool**

In `internal/mcp/server.go`, add after the `read_file` registration (reads stay grouped before the `!ReadOnly` block):

```go
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "list_issues",
		Description: "List open issues for a single repository (live).",
	}, deps.handleListIssues)
```

- [ ] **Step 6: Update the advertised tool-set test**

In `internal/mcp/server_test.go`, rename `TestNewServer_RegistersFourToolsByDefault` to `TestNewServer_RegistersExpectedToolSet` (the count changes again in Tasks 3 and 4, so a count-free name stops the churn) and update its `want`:

```go
func TestNewServer_RegistersExpectedToolSet(t *testing.T) {
	names := advertisedTools(t, Deps{})
	want := []string{"create_issue", "cross_forge_status", "list_issues", "list_repos", "read_file"}
	if len(names) != len(want) {
		t.Fatalf("want %v, got %v", want, names)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("want %v, got %v", want, names)
		}
	}
}
```

`advertisedTools` sorts, so `want` must stay alphabetically ordered.

- [ ] **Step 7: Run the full suite and static checks**

Run: `go test -race ./... && gofmt -l . && go vet ./... && golangci-lint run`
Expected: all packages PASS; no output from `gofmt -l .`; no findings.

- [ ] **Step 8: Commit**

```bash
git add internal/mcp/
git commit -m "feat(mcp): add list_issues tool

Exposes ListOpenIssues over MCP as a raw single-repo query. Needs no
capability assertion — ListOpenIssues is part of ForgeReader, so this
works on any wired forge including GitLab and ADO once connected.

cross_forge_status stays the ranked aggregate view; the overlap is
deliberate, the two answer different questions."
```

---

### Task 3: `list_git_forges`

Discovery, so a client stops guessing forge/owner pairs. Makes **no network requests**: `ClientFor` is wrapped by `newCachingClientResolver`, so after first resolution this is a map lookup.

**Files:**
- Modify: `internal/mcp/tools_read.go` (append)
- Modify: `internal/mcp/tools_read_test.go` (append)
- Modify: `internal/mcp/server.go`
- Modify: `internal/mcp/server_test.go`

**Interfaces:**
- Consumes: `Deps.DefaultOwners`, `Deps.ClientFor`, `Deps.ReadOnly`, `Capabilities`.
- Produces: `forgeStatus`, `listGitForgesInput`, `listGitForgesOutput`, `isWriteTool(name string) bool`, `(Deps).advertisedCapabilities`, `(Deps).handleListGitForges`.

- [ ] **Step 1: Write the failing tests**

Append to `internal/mcp/tools_read_test.go`:

```go
func TestHandleListGitForges_ReportsConfiguredAndUnconfiguredTargets(t *testing.T) {
	// "forgejo" is deliberately absent from clients, so ClientFor returns nil.
	gh := newFakeFull("github")
	d := depsWith(map[string]*fakeFull{"github": gh}, []Target{
		{Forge: "github", Owner: "freaxnx01"},
		{Forge: "forgejo", Owner: "freax"},
	})

	_, out, err := d.handleListGitForges(context.Background(), nil, listGitForgesInput{})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Forges) != 2 {
		t.Fatalf("want 2 targets, got %+v", out.Forges)
	}

	configured := out.Forges[0]
	if configured.Forge != "github" || configured.Owner != "freaxnx01" || !configured.Configured {
		t.Errorf("github target wrong: %+v", configured)
	}
	if configured.Reason != "" {
		t.Errorf("a configured target must carry no reason, got %q", configured.Reason)
	}
	if len(configured.Capabilities) != 5 {
		t.Errorf("a fully capable client must report 5 tools, got %v", configured.Capabilities)
	}

	unconfigured := out.Forges[1]
	if unconfigured.Configured {
		t.Errorf("forgejo must report configured=false: %+v", unconfigured)
	}
	if unconfigured.Reason != "missing token or forge unavailable" {
		t.Errorf("unexpected reason %q", unconfigured.Reason)
	}
	if unconfigured.Capabilities != nil {
		t.Errorf("an unconfigured target must omit capabilities, got %v", unconfigured.Capabilities)
	}
}

func TestHandleListGitForges_TierOneClientReportsOnlyTierOneTools(t *testing.T) {
	d := Deps{
		DefaultOwners: []Target{{Forge: "gitlab", Owner: "acme"}},
		ClientFor:     func(string, string) ForgeReader { return &fakeReader{name: "gitlab"} },
	}

	_, out, err := d.handleListGitForges(context.Background(), nil, listGitForgesInput{})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"list_repos", "list_issues"}
	got := out.Forges[0].Capabilities
	if len(got) != len(want) {
		t.Fatalf("want %v, got %v", want, got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("want %v, got %v", want, got)
		}
	}
}

func TestHandleListGitForges_ReadOnlyDropsWriteCapabilities(t *testing.T) {
	gh := newFakeFull("github")
	d := Deps{
		ReadOnly:      true,
		DefaultOwners: []Target{{Forge: "github", Owner: "freaxnx01"}},
		ClientFor:     func(string, string) ForgeReader { return gh },
	}

	_, out, err := d.handleListGitForges(context.Background(), nil, listGitForgesInput{})
	if err != nil {
		t.Fatal(err)
	}
	if !out.ReadOnly {
		t.Error("read_only must reflect Deps.ReadOnly")
	}
	for _, c := range out.Forges[0].Capabilities {
		if c == "create_issue" || c == "create_repo" {
			t.Errorf("read-only must not advertise write tools, got %v", out.Forges[0].Capabilities)
		}
	}
	if len(out.Forges[0].Capabilities) != 3 {
		t.Errorf("want the 3 read tools, got %v", out.Forges[0].Capabilities)
	}
}

func TestHandleListGitForges_ReadOnlyFalseKeepsWriteCapabilities(t *testing.T) {
	gh := newFakeFull("github")
	d := Deps{
		DefaultOwners: []Target{{Forge: "github", Owner: "freaxnx01"}},
		ClientFor:     func(string, string) ForgeReader { return gh },
	}

	_, out, err := d.handleListGitForges(context.Background(), nil, listGitForgesInput{})
	if err != nil {
		t.Fatal(err)
	}
	if out.ReadOnly {
		t.Error("read_only must be false when Deps.ReadOnly is false")
	}
	if len(out.Forges[0].Capabilities) != 5 {
		t.Errorf("want all 5 tools, got %v", out.Forges[0].Capabilities)
	}
}

func TestHandleListGitForges_EmptyDefaultOwnersReturnsEmptyListNotNil(t *testing.T) {
	d := depsWith(map[string]*fakeFull{}, nil)

	_, out, err := d.handleListGitForges(context.Background(), nil, listGitForgesInput{})
	if err != nil {
		t.Fatal("empty DefaultOwners is an empty result, not an error:", err)
	}
	if len(out.Forges) != 0 {
		t.Fatalf("want no targets, got %+v", out.Forges)
	}
	// Must be non-nil: a nil slice marshals to JSON null, but the contract
	// says the field is an empty array.
	if out.Forges == nil {
		t.Error("Forges must be an empty slice, not nil, so it marshals to [] not null")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/mcp/ -run TestHandleListGitForges -v`
Expected: FAIL to compile — `undefined: listGitForgesInput` and `d.handleListGitForges undefined`.

- [ ] **Step 3: Implement the handler**

Append to `internal/mcp/tools_read.go`:

```go
type listGitForgesInput struct{}

// forgeStatus describes one configured (forge, owner) target. Capabilities and
// Reason are mutually exclusive: an unconfigured target has a reason and no
// capabilities, a configured one the reverse.
type forgeStatus struct {
	Forge        string   `json:"forge"`
	Owner        string   `json:"owner"`
	Configured   bool     `json:"configured"`
	Capabilities []string `json:"capabilities,omitempty"`
	Reason       string   `json:"reason,omitempty"`
}

type listGitForgesOutput struct {
	Forges   []forgeStatus `json:"forges"`
	ReadOnly bool          `json:"read_only"`
}

// isWriteTool reports whether a tool name is registered only when
// Deps.ReadOnly is false. A function rather than a package-level map so there
// is no mutable global state.
func isWriteTool(name string) bool {
	switch name {
	case "create_issue", "create_repo":
		return true
	default:
		return false
	}
}

// advertisedCapabilities is Capabilities narrowed to what this server actually
// registered. Capabilities reports write tools regardless of ReadOnly by
// design, so filtering them here keeps list_git_forges from advertising a tool
// a read-only server never registered.
func (d Deps) advertisedCapabilities(r ForgeReader) []string {
	all := Capabilities(r)
	if !d.ReadOnly {
		return all
	}
	reads := make([]string, 0, len(all))
	for _, c := range all {
		if !isWriteTool(c) {
			reads = append(reads, c)
		}
	}
	return reads
}

// handleListGitForges reports the configured targets so a client does not have
// to guess a (forge, owner) pair. It makes no network requests: ClientFor is
// wrapped by a resolve-once cache, so after the first resolution per target
// this is a map lookup. A live API probe was rejected — it would turn
// discovery into N round-trips and conflate "not configured" with a transient
// API failure.
func (d Deps) handleListGitForges(_ context.Context, _ *mcp.CallToolRequest, _ listGitForgesInput) (*mcp.CallToolResult, listGitForgesOutput, error) {
	forges := make([]forgeStatus, 0, len(d.DefaultOwners))
	for _, t := range d.DefaultOwners {
		status := forgeStatus{Forge: t.Forge, Owner: t.Owner}
		if client := d.ClientFor(t.Forge, t.Owner); client != nil {
			status.Configured = true
			status.Capabilities = d.advertisedCapabilities(client)
		} else {
			// Same wording as handleListRepos's warning, so the two tools
			// describe the same condition identically.
			status.Reason = "missing token or forge unavailable"
		}
		forges = append(forges, status)
	}
	return nil, listGitForgesOutput{Forges: forges, ReadOnly: d.ReadOnly}, nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/mcp/ -run TestHandleListGitForges -v`
Expected: PASS, all five tests.

- [ ] **Step 5: Register the tool**

In `internal/mcp/server.go`, add after the `list_issues` registration:

```go
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "list_git_forges",
		Description: "List the configured forge targets, whether each is configured, and which tools it supports. Makes no network requests.",
	}, deps.handleListGitForges)
```

- [ ] **Step 6: Update the advertised tool-set test**

In `internal/mcp/server_test.go`, update `TestNewServer_RegistersExpectedToolSet`'s `want`:

```go
	want := []string{"create_issue", "cross_forge_status", "list_git_forges", "list_issues", "list_repos", "read_file"}
```

- [ ] **Step 7: Run the full suite and static checks**

Run: `go test -race ./... && gofmt -l . && go vet ./... && golangci-lint run`
Expected: all packages PASS; no output from `gofmt -l .`; no findings.

- [ ] **Step 8: Commit**

```bash
git add internal/mcp/
git commit -m "feat(mcp): add list_git_forges discovery tool

Reports each configured (forge, owner) target, whether it resolved, and
the tools it supports, so a client no longer has to guess a forge/owner
pair that list_repos would reject.

Capabilities come from the Capabilities helper, filtered to drop write
tools under ReadOnly — advertising a tool the server never registered
would be a lie. Makes no network requests."
```

---

### Task 4: `create_repo`

The second write tool, mirroring `create_issue`: registered only when `!Deps.ReadOnly`, draft-by-default via `confirm`. Also closes a coverage gap the prerequisite's final review flagged — `handleCreateIssue`'s `client == nil` branch is currently untested while `handleReadFile`'s equivalent is covered.

**The `owner` input selects credentials, not destination.** Both implementations POST to `/user/repos` (`internal/forge/github.go:101`, `internal/forge/forgejo.go:97`), creating the repo under whichever account the token belongs to; no owner is sent. So the draft echoes the requested `owner` (the real one is unknowable without making the call), while the success response carries the actual owner from the returned `forge.RepoRef` — making a mismatch visible rather than papering over it.

**Files:**
- Modify: `internal/mcp/tools_write.go` (append)
- Modify: `internal/mcp/tools_write_test.go` (append)
- Modify: `internal/mcp/server.go`
- Modify: `internal/mcp/server_test.go`

**Interfaces:**
- Consumes: `Deps.ClientFor`, `repoCreator`, `forge.ErrRepoExists`; `fakeRepos.createRepoCalled` / `fakeRepos.createRepoErr` from the test fakes.
- Produces: `createRepoInput`, `createRepoOutput`, `(Deps).handleCreateRepo`.

- [ ] **Step 1: Write the failing tests**

Append to `internal/mcp/tools_write_test.go`. Note this file needs `"errors"` added to its imports for the `ErrRepoExists` case:

```go
func TestHandleCreateRepo_DraftDoesNotCreate(t *testing.T) {
	calls := 0
	gh := newFakeFull("github")
	gh.createRepoCalled = &calls
	d := depsWith(map[string]*fakeFull{"github": gh}, nil)

	_, out, err := d.handleCreateRepo(context.Background(), nil,
		createRepoInput{Forge: "github", Owner: "freaxnx01", Name: "widget", Private: true})
	if err != nil {
		t.Fatal(err)
	}
	if !out.Draft {
		t.Error("want draft=true without confirm")
	}
	// The assertion that matters: an unconfirmed call creates nothing.
	if calls != 0 {
		t.Errorf("an unconfirmed create must not call the forge, got %d calls", calls)
	}
	if out.Owner != "freaxnx01" {
		t.Errorf("the draft echoes the requested owner, got %q", out.Owner)
	}
	if out.Name != "widget" || !out.Private {
		t.Errorf("the draft must echo the request: %+v", out)
	}
}

func TestHandleCreateRepo_ConfirmCreatesAndTakesOwnerFromRepoRef(t *testing.T) {
	calls := 0
	gh := newFakeFull("github")
	gh.createRepoCalled = &calls
	d := depsWith(map[string]*fakeFull{"github": gh}, nil)

	// The requested owner is deliberately NOT the token's account: the fake
	// returns Owner "token-owner", which is what the response must carry.
	_, out, err := d.handleCreateRepo(context.Background(), nil,
		createRepoInput{Forge: "github", Owner: "requested-owner", Name: "widget", Private: true, Confirm: true})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Errorf("want exactly 1 create call, got %d", calls)
	}
	if out.Draft {
		t.Error("want draft=false after a confirmed create")
	}
	if out.Owner != "token-owner" {
		t.Errorf("the success response must carry the owner from the RepoRef, not the input, got %q", out.Owner)
	}
	if out.Repo == nil {
		t.Fatal("want the created RepoRef in the response")
	}
	if out.Repo.Visibility != "private" {
		t.Errorf("private must reach the client, got visibility %q", out.Repo.Visibility)
	}
}

func TestHandleCreateRepo_UnconfiguredForgeErrors(t *testing.T) {
	d := depsWith(map[string]*fakeFull{}, nil)

	_, _, err := d.handleCreateRepo(context.Background(), nil,
		createRepoInput{Forge: "gitlab", Owner: "acme", Name: "widget", Confirm: true})
	if err == nil {
		t.Fatal("want an error for an unconfigured forge, got nil")
	}
	if !strings.Contains(err.Error(), "not configured") {
		t.Errorf("want a not-configured error, got %v", err)
	}
}

func TestHandleCreateRepo_TierOneClientReportsUnsupportedNotUnconfigured(t *testing.T) {
	d := Deps{ClientFor: func(string, string) ForgeReader { return &fakeReader{name: "gitlab"} }}

	_, _, err := d.handleCreateRepo(context.Background(), nil,
		createRepoInput{Forge: "gitlab", Owner: "acme", Name: "widget", Confirm: true})
	if err == nil {
		t.Fatal("want an error for a client without CreateRepo, got nil")
	}
	if strings.Contains(err.Error(), "not configured") {
		t.Fatalf("a resolved but incapable client must not be reported as unconfigured: %v", err)
	}
	if !strings.Contains(err.Error(), "does not support") {
		t.Fatalf("want a does-not-support error, got %v", err)
	}
}

func TestHandleCreateRepo_RepoExistsGetsDistinctMessage(t *testing.T) {
	gh := newFakeFull("github")
	gh.createRepoErr = forge.ErrRepoExists
	d := depsWith(map[string]*fakeFull{"github": gh}, nil)

	_, _, err := d.handleCreateRepo(context.Background(), nil,
		createRepoInput{Forge: "github", Owner: "freaxnx01", Name: "widget", Confirm: true})
	if err == nil {
		t.Fatal("want an error when the repo exists, got nil")
	}
	if !errors.Is(err, forge.ErrRepoExists) {
		t.Errorf("want ErrRepoExists preserved via %%w, got %v", err)
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("want a distinct already-exists message, got %v", err)
	}
	if !strings.Contains(err.Error(), "widget") {
		t.Errorf("the message must name the repo, got %v", err)
	}
}

// Closes the coverage gap the capability-interface review flagged:
// handleReadFile's nil-client branch was tested, handleCreateIssue's was not.
func TestHandleCreateIssue_UnconfiguredForgeErrors(t *testing.T) {
	d := depsWith(map[string]*fakeFull{}, nil)

	_, _, err := d.handleCreateIssue(context.Background(), nil,
		createIssueInput{Forge: "gitlab", Owner: "acme", Repo: "widget", Title: "t", Confirm: true})
	if err == nil {
		t.Fatal("want an error for an unconfigured forge, got nil")
	}
	if !strings.Contains(err.Error(), "not configured") {
		t.Errorf("want a not-configured error, got %v", err)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/mcp/ -run 'TestHandleCreateRepo|TestHandleCreateIssue_UnconfiguredForgeErrors' -v`
Expected: FAIL to compile — `undefined: createRepoInput` and `d.handleCreateRepo undefined`.

- [ ] **Step 3: Implement the handler**

Append to `internal/mcp/tools_write.go`, and add `"errors"` to that file's imports:

```go
type createRepoInput struct {
	Forge   string `json:"forge" jsonschema:"forge to create the repo on: github or forgejo"`
	Owner   string `json:"owner" jsonschema:"selects which account's token to use; the repo is created under the account that token belongs to, which may differ from this value"`
	Name    string `json:"name" jsonschema:"new repository name"`
	Private bool   `json:"private,omitempty" jsonschema:"create the repository as private"`
	Confirm bool   `json:"confirm,omitempty" jsonschema:"when false, returns a draft without creating; set true to create"`
}

// createRepoOutput carries the requested owner on a draft (the real one is not
// knowable without making the call) and the actual owner from the returned
// RepoRef on success, so a mismatch is visible to the caller.
type createRepoOutput struct {
	Draft   bool           `json:"draft"`
	Forge   string         `json:"forge"`
	Owner   string         `json:"owner"`
	Name    string         `json:"name"`
	Private bool           `json:"private"`
	Repo    *forge.RepoRef `json:"repo,omitempty"`
}

// handleCreateRepo creates a repository, draft-by-default. The owner input
// selects which account's token to use, not the destination: both client
// implementations POST to /user/repos, which creates under the token's own
// account and sends no owner.
func (d Deps) handleCreateRepo(ctx context.Context, _ *mcp.CallToolRequest, in createRepoInput) (*mcp.CallToolResult, createRepoOutput, error) {
	draft := createRepoOutput{
		Draft: true,
		Forge: in.Forge, Owner: in.Owner, Name: in.Name, Private: in.Private,
	}
	if !in.Confirm {
		return nil, draft, nil
	}
	client := d.ClientFor(in.Forge, in.Owner)
	if client == nil {
		return nil, createRepoOutput{}, fmt.Errorf("forge %q not configured", in.Forge)
	}
	repos, ok := client.(repoCreator)
	if !ok {
		return nil, createRepoOutput{}, fmt.Errorf("forge %q does not support creating repositories", in.Forge)
	}
	repo, err := repos.CreateRepo(ctx, in.Name, in.Private)
	if err != nil {
		// Distinct and actionable, but still wrapped so callers keep errors.Is.
		if errors.Is(err, forge.ErrRepoExists) {
			return nil, createRepoOutput{}, fmt.Errorf("repo %q already exists on %s, choose another name: %w", in.Name, in.Forge, err)
		}
		return nil, createRepoOutput{}, fmt.Errorf("create repo %s: %w", in.Name, err)
	}
	return nil, createRepoOutput{
		Draft: false,
		Forge: in.Forge, Owner: repo.Owner, Name: repo.Name, Private: in.Private,
		Repo: &repo,
	}, nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/mcp/ -run 'TestHandleCreateRepo|TestHandleCreateIssue_UnconfiguredForgeErrors' -v`
Expected: PASS, all six tests.

- [ ] **Step 5: Register the tool**

In `internal/mcp/server.go`, add inside the existing `if !deps.ReadOnly {` block, after `create_issue`:

```go
		mcp.AddTool(srv, &mcp.Tool{
			Name:        "create_repo",
			Description: "Create a repository. The owner input selects which account's token to use — the repo is created under that token's account, which may differ. Without confirm=true this returns a draft and creates nothing.",
		}, deps.handleCreateRepo)
```

- [ ] **Step 6: Update `NewServer`'s doc comment**

It currently says "the four cross-forge tools" and "the write tool (create_issue)". Replace with:

```go
// NewServer builds the Bridge MCP server with the seven cross-forge tools
// registered. In read-only mode the write tools (create_issue, create_repo)
// are not registered at all, so there is nothing to bypass.
```

- [ ] **Step 7: Update the server tests**

In `internal/mcp/server_test.go`, update `TestNewServer_RegistersExpectedToolSet`'s `want` to the final set:

```go
	want := []string{"create_issue", "create_repo", "cross_forge_status", "list_git_forges", "list_issues", "list_repos", "read_file"}
```

Then replace `TestNewServer_ReadOnlyOmitsCreateIssue` with a version asserting the exact read-only set, so a future write tool cannot be added without this test noticing:

```go
func TestNewServer_ReadOnlyOmitsBothWriteTools(t *testing.T) {
	names := advertisedTools(t, Deps{ReadOnly: true})
	want := []string{"cross_forge_status", "list_git_forges", "list_issues", "list_repos", "read_file"}
	if len(names) != len(want) {
		t.Fatalf("want %v, got %v", want, names)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("want %v, got %v", want, names)
		}
	}
	for _, n := range names {
		if n == "create_issue" || n == "create_repo" {
			t.Fatalf("read-only server must not advertise write tools: %v", names)
		}
	}
}
```

- [ ] **Step 8: Run the full suite and static checks**

Run: `go test -race ./... && gofmt -l . && go vet ./... && golangci-lint run`
Expected: all packages PASS; no output from `gofmt -l .`; no findings.

- [ ] **Step 9: Update the changelog**

Add to the `[Unreleased]` section's `Added` list in `CHANGELOG.md` (create the `Added` subsection if the section has none):

```markdown
- MCP: `list_issues`, `list_git_forges` and `create_repo` tools. `create_repo` is
  draft-by-default and registered only outside read-only mode.
```

- [ ] **Step 10: Commit**

```bash
git add internal/mcp/ CHANGELOG.md
git commit -m "feat(mcp): add create_repo tool

Draft-by-default like create_issue and registered only when not
read-only. The owner input selects which account's token to use, not the
destination — both clients POST to /user/repos — so the draft echoes the
requested owner while the success response carries the actual owner from
the returned RepoRef, making a mismatch visible.

ErrRepoExists gets a distinct actionable message, still wrapped so
callers keep errors.Is. Also covers handleCreateIssue's unconfigured-forge
branch, a gap the capability-interface review flagged."
```

---

## Verification

After Task 4, this sequence must be clean from the repo root:

```bash
gofmt -l .            # no output
go vet ./...
golangci-lint run
go test -race ./...
```

Behavioural claims to confirm by eye in the final diff:

1. `list_git_forges` returns `"forges": []` (not `null`) when `DefaultOwners` is empty — the handler uses `make([]forgeStatus, 0, …)`.
2. Under `ReadOnly`, the advertised capability list and the registered tool set agree: neither contains `create_issue` or `create_repo`.
3. `create_repo`'s success response owner comes from `repo.Owner`, never `in.Owner`.
4. `create_repo` and `create_issue` each return three distinguishable errors: `not configured`, `does not support`, and the wrapped call failure.

## Self-Review Notes

Checked against the spec:

- `list_issues` contract — required forge/owner/repo, `{"issues": …}` output, nil client → `forge %q not configured`, error wrapped with the repo path, single-target fail-fast — Task 2.
- `list_git_forges` contract — iterate `DefaultOwners`, `configured` flag, `reason` only when false, capabilities from `Capabilities`, capabilities omitted when unconfigured, write caps dropped under `ReadOnly`, `read_only` field, no network requests, empty `DefaultOwners` → empty list not error — Task 3.
- `create_repo` contract — `!ReadOnly` registration, draft-by-default, owner-selects-credentials documented in the tool description and the input schema, draft echoes requested owner, success takes owner from `RepoRef`, distinct `ErrRepoExists` message — Task 4.
- File layout split (`tools.go` / `tools_read.go` / `tools_write.go`, tests matching, fakes staying in `tools_test.go`, registration order reads-then-writes) — Task 1, plus registration placement in Tasks 2–4.
- `server_test.go` — exactly seven tools normally, five reads under `ReadOnly` with both writes absent — Task 4, Step 7.
- No `internal/forge` changes and no interface changes — enforced by Global Constraints.

Deviations from the spec, deliberate:

- **The spec's "extend `fakeForge`" testing instruction is dropped as stale** — see *Spec Reconciliation*. The composable fakes already carry `ListOpenIssues`, `CreateRepo`, and the injectable error/counter fields, so no fake changes are needed.
- **`ErrRepoExists` is wrapped with `%w`** rather than replaced by a bare message. The spec asks for a "distinct, actionable message instead of a generic wrap"; the message is distinct and actionable, and keeping the wrap preserves `errors.Is`, which the repo's own error conventions require. The test asserts both.
- **`TestHandleCreateIssue_UnconfiguredForgeErrors` is added** (Task 4, Step 1), which the spec does not list. It closes the asymmetry the prerequisite's final review flagged as Minor M5.

Not carried over (spec non-goals, unchanged): wiring GitLab/ADO into `clientForMCP`, exposing Bridge's local surface, and `write_file` / `list_pull_requests` / `list_project_items`.
