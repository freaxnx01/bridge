# MCP Capability Interfaces Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the fat `imcp.ForgeClient` interface with a tier-1 `ForgeReader` plus small per-capability interfaces, so forge clients that support only some operations can be exposed over MCP and report honest errors.

**Architecture:** `Deps.ClientFor` returns `ForgeReader` (`Name`, `ListRepos`, `ListOpenIssues`) — the method set every forge client in `internal/forge` already satisfies. Handlers needing more assert for a small unexported interface (`fileReader`, `issueCreator`, `repoCreator`) declared at the consumer, and fail with "does not support X" rather than the current misleading "not configured". Because `ForgeReader`'s method set is a subset of `forge.Client`'s, the type assertion in `newCachingClientResolver` is deleted outright rather than narrowed.

**Tech Stack:** Go (stdlib `testing`, hand-rolled fakes), `github.com/modelcontextprotocol/go-sdk/mcp`. No new dependencies.

**Spec:** `docs/superpowers/specs/2026-07-20-mcp-capability-interfaces-design.md`

## Global Constraints

- **No new Go modules.** No `testify`, `mockery`, or `gomock` — hand-rolled fakes only.
- **No changes to `internal/forge`.** This plan only touches `internal/mcp` and `cmd/bridge`.
- Never discard an error with `_ =` to satisfy the linter.
- Errors return up to the command layer — no `os.Exit` or stderr writes below `main`.
- Error wrap messages are lower-case with no trailing punctuation.
- Interfaces are declared at the consumer and kept to 1–3 methods.
- Every task ends green on: `gofmt -l .` (empty output), `go vet ./...`, `golangci-lint run`, `go test -race ./...`.
- Run all commands from the repo root: `/home/freax/repos/github/freaxnx01/public/bridge/.worktrees/mcp-methods`.

## File Structure

| File | Responsibility | Task |
|---|---|---|
| `internal/mcp/tools_test.go` | Composable capability fakes + handler tests | 1, 2, 3 |
| `internal/mcp/tools.go` | `ForgeReader`, capability interfaces, `Capabilities`, `Deps`, handlers | 2, 3 |
| `cmd/bridge/mcp.go` | Resolver returns `ForgeReader`; assertion deleted | 3 |
| `cmd/bridge/mcp_test.go` | Reader-only client resolves non-nil | 3 |

Task 3 changes `Deps.ClientFor`'s type, which breaks `cmd/bridge/mcp.go` compilation the moment it lands. Those two files therefore migrate in a single task — splitting them would leave the tree un-buildable between commits.

---

### Task 1: Composable test fakes

The current `fakeForge` implements every capability at once, so no test can express a client that resolves successfully but lacks `GetFile`. That client is exactly what this plan exists to support. This task is **test-only**: the suite is green before and after, and no production code changes.

**Files:**
- Modify: `internal/mcp/tools_test.go:12-52` (fake definitions and `depsWith`)

**Interfaces:**
- Consumes: nothing.
- Produces: `fakeReader` (tier-1 only), `fakeFiles`, `fakeIssues`, `fakeRepos` capability structs; `fakeFull` composite; `newFakeFull(name string) *fakeFull` constructor. Tasks 2 and 3 build test clients from these.

- [ ] **Step 1: Replace the fake definitions**

In `internal/mcp/tools_test.go`, replace lines 12–39 (the `fakeForge` struct and its four methods) with:

```go
// fakeReader implements the tier-1 surface every forge client supports.
// Capability structs below are embedded alongside it to compose a client with
// more than tier-1, so a test can also construct a deliberately partial one.
type fakeReader struct {
	name      string
	repos     []forge.RepoRef
	issues    []forge.Issue
	listErr   error
	issuesErr error
}

func (f *fakeReader) Name() string { return f.name }

func (f *fakeReader) ListRepos(_ context.Context, _ string) ([]forge.RepoRef, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.repos, nil
}

func (f *fakeReader) ListOpenIssues(_ context.Context, _, _ string) ([]forge.Issue, error) {
	if f.issuesErr != nil {
		return nil, f.issuesErr
	}
	return f.issues, nil
}

// fakeFiles supplies the fileReader capability.
type fakeFiles struct {
	file  []byte
	sha   string
	found bool
}

func (f *fakeFiles) GetFile(_ context.Context, _, _, _ string) ([]byte, string, bool, error) {
	return f.file, f.sha, f.found, nil
}

// fakeIssues supplies the issueCreator capability. forgeName is carried here
// rather than read from fakeReader because embedded structs cannot see one
// another's fields.
type fakeIssues struct {
	forgeName    string
	createCalled *int
}

func (f *fakeIssues) CreateIssue(_ context.Context, owner, repo, title, _ string) (forge.Issue, error) {
	if f.createCalled != nil {
		*f.createCalled++
	}
	return forge.Issue{Forge: f.forgeName, Repo: owner + "/" + repo, Number: 42, Title: title}, nil
}

// fakeRepos supplies the repoCreator capability.
type fakeRepos struct {
	forgeName        string
	createRepoCalled *int
	createRepoErr    error
}

func (f *fakeRepos) CreateRepo(_ context.Context, name string, private bool) (forge.RepoRef, error) {
	if f.createRepoCalled != nil {
		*f.createRepoCalled++
	}
	if f.createRepoErr != nil {
		return forge.RepoRef{}, f.createRepoErr
	}
	visibility := "public"
	if private {
		visibility = "private"
	}
	return forge.RepoRef{Forge: f.forgeName, Owner: "token-owner", Name: name, Visibility: visibility}, nil
}

// fakeFull has every capability — the GitHub/Forgejo shape.
type fakeFull struct {
	*fakeReader
	*fakeFiles
	*fakeIssues
	*fakeRepos
}

// newFakeFull builds a fully capable client. Tests set fields on the embedded
// structs afterwards, e.g. c.repos = … or c.found = false.
func newFakeFull(name string) *fakeFull {
	return &fakeFull{
		fakeReader: &fakeReader{name: name},
		fakeFiles:  &fakeFiles{},
		fakeIssues: &fakeIssues{forgeName: name},
		fakeRepos:  &fakeRepos{forgeName: name},
	}
}
```

`fakeRepos` is added now, unused, because Task 2 needs a client that satisfies `repoCreator` to test `Capabilities`. It costs one struct and saves a second pass over this file.

**Latent fragility, verified but worth knowing:** `fakeIssues` and `fakeRepos` both declare `forgeName`, so `fakeFull.forgeName` is an ambiguous selector. Go only rejects an ambiguous selector where it is *used*, and no test in this plan selects it — `newFakeFull` sets each embedded struct explicitly. This composition was compiled against the repo toolchain and passes. If a future test needs `forgeName` off the composite, set it on the specific embedded struct (`c.fakeIssues.forgeName`) rather than adding a promoted field.

- [ ] **Step 2: Update `depsWith` to the new fake type**

Replace `depsWith` (was lines 41–52) with:

```go
func depsWith(clients map[string]*fakeFull, owners []Target) Deps {
	return Deps{
		DefaultOwners: owners,
		ClientFor: func(forgeName, _ string) ForgeClient {
			c, ok := clients[forgeName]
			if !ok {
				return nil
			}
			return c
		},
	}
}
```

The return type stays `ForgeClient` — it becomes `ForgeReader` in Task 3.

- [ ] **Step 3: Update every existing call site to `newFakeFull`**

Each existing test builds `map[string]*fakeForge` with struct literals. Rewrite each to build `*fakeFull` via `newFakeFull` and assign fields. The six affected tests and their replacements:

```go
// TestHandleListRepos_AggregatesDefaultOwners
gh := newFakeFull("github")
gh.repos = []forge.RepoRef{{Forge: "github", Owner: "freaxnx01", Name: "bridge"}}
fj := newFakeFull("forgejo")
fj.repos = []forge.RepoRef{{Forge: "forgejo", Owner: "freax", Name: "notes"}}
clients := map[string]*fakeFull{"github": gh, "forgejo": fj}

// TestHandleListRepos_ForgeFilterHonoured
gh := newFakeFull("github")
gh.repos = []forge.RepoRef{{Forge: "github", Name: "bridge"}}
fj := newFakeFull("forgejo")
fj.repos = []forge.RepoRef{{Forge: "forgejo", Name: "notes"}}
clients := map[string]*fakeFull{"github": gh, "forgejo": fj}

// TestHandleListRepos_OwnerInputOverridesDefaults
gh := newFakeFull("github")
gh.repos = []forge.RepoRef{{Forge: "github", Owner: "acme", Name: "widget"}}
clients := map[string]*fakeFull{"github": gh}

// TestHandleListRepos_OwnerWithoutForgeIsRejected
d := depsWith(map[string]*fakeFull{}, nil)

// TestHandleListRepos_UnconfiguredTargetReportsWarningNotSilentDrop
gh := newFakeFull("github")
gh.repos = []forge.RepoRef{{Forge: "github", Owner: "freaxnx01", Name: "bridge"}}
// "forgejo" deliberately absent from clients: ClientFor(forgejo, ...) resolves to nil.
clients := map[string]*fakeFull{"github": gh}

// TestHandleListRepos_PartialFailureReturnsWarningAndSuccessfulResults
gh := newFakeFull("github")
gh.repos = []forge.RepoRef{{Forge: "github", Owner: "freaxnx01", Name: "bridge"}}
fj := newFakeFull("forgejo")
fj.listErr = errors.New("token expired")
clients := map[string]*fakeFull{"github": gh, "forgejo": fj}

// TestHandleReadFile_FoundAndAbsent
gh := newFakeFull("github")
gh.file, gh.sha, gh.found = []byte("hello"), "abc", true
clients := map[string]*fakeFull{"github": gh}
// …later in the same test, the mutation lines become:
gh.found = false
gh.file = nil

// TestHandleReadFile_UnknownForge
d := depsWith(map[string]*fakeFull{}, nil)

// TestHandleCreateIssue_DraftDoesNotCreate  and  TestHandleCreateIssue_ConfirmCreates
calls := 0
gh := newFakeFull("github")
gh.createCalled = &calls
clients := map[string]*fakeFull{"github": gh}
```

Leave every assertion body unchanged — this step only changes how clients are constructed.

- [ ] **Step 4: Run the full package suite**

Run: `go test ./internal/mcp/ -v`
Expected: PASS, same set of tests as before, no new ones. A refactor of test scaffolding must not change what is asserted.

- [ ] **Step 5: Verify formatting and lint**

Run: `gofmt -l . && go vet ./... && golangci-lint run`
Expected: no output from `gofmt -l .`, no findings from either tool.

- [ ] **Step 6: Commit**

```bash
git add internal/mcp/tools_test.go
git commit -m "test(mcp): make forge fakes composable by capability

The single fakeForge implemented every method, so no test could express
a client that resolves but lacks GetFile. Split into tier-1 fakeReader
plus embeddable capability structs, with fakeFull composing all of them.

Test-only refactor: assertions are unchanged."
```

---

### Task 2: Declare `ForgeReader`, capability interfaces, and `Capabilities`

Adds the new declarations with no production consumers, so the tree stays green and the migration in Task 3 is reviewable on its own.

**Files:**
- Modify: `internal/mcp/tools.go:22-29` (add alongside `ForgeClient`, which stays until Task 3)
- Modify: `internal/mcp/tools_test.go` (append tests)

**Interfaces:**
- Consumes: `fakeReader`, `fakeFull`, `newFakeFull` from Task 1.
- Produces: `ForgeReader` interface; unexported `fileReader`, `issueCreator`, `repoCreator`; `Capabilities(r ForgeReader) []string` returning tool names. Task 3 migrates handlers onto these.

- [ ] **Step 1: Write the failing test for `Capabilities`**

Append to `internal/mcp/tools_test.go`:

```go
func TestCapabilities_ReportsToolNamesPerCapability(t *testing.T) {
	tests := []struct {
		name   string
		client ForgeReader
		want   []string
	}{
		{
			name:   "nil reader reports nothing",
			client: nil,
			want:   nil,
		},
		{
			name:   "tier-1 only client reports tier-1 tools",
			client: &fakeReader{name: "gitlab"},
			want:   []string{"list_repos", "list_issues"},
		},
		{
			name:   "fully capable client reports every tool",
			client: newFakeFull("github"),
			want:   []string{"list_repos", "list_issues", "read_file", "create_issue", "create_repo"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Capabilities(tt.client)
			if len(got) != len(tt.want) {
				t.Fatalf("Capabilities() = %v, want %v", got, tt.want)
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Errorf("Capabilities()[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/mcp/ -run TestCapabilities -v`
Expected: FAIL to compile — `undefined: ForgeReader` and `undefined: Capabilities`.

- [ ] **Step 3: Add the interfaces and helper**

In `internal/mcp/tools.go`, insert directly after the existing `ForgeClient` block (ending line 29). Leave `ForgeClient` in place — Task 3 deletes it.

```go
// ForgeReader is the tier-1 surface every forge client satisfies. Deps.ClientFor
// returns this, and handlers needing more assert for one of the capability
// interfaces below.
type ForgeReader interface {
	Name() string
	ListRepos(ctx context.Context, owner string) ([]forge.RepoRef, error)
	ListOpenIssues(ctx context.Context, owner, repo string) ([]forge.Issue, error)
}

// fileReader is asserted by read_file.
type fileReader interface {
	GetFile(ctx context.Context, owner, repo, path string) (content []byte, sha string, found bool, err error)
}

// issueCreator is asserted by create_issue.
type issueCreator interface {
	CreateIssue(ctx context.Context, owner, repo, title, body string) (forge.Issue, error)
}

// repoCreator is asserted by create_repo.
type repoCreator interface {
	CreateRepo(ctx context.Context, name string, private bool) (forge.RepoRef, error)
}

// Capabilities returns the names of the MCP tools a resolved client supports.
// It reports tool names rather than method names so a caller can map the result
// directly onto what it may invoke. Returns nil for a nil reader.
//
// Write capabilities are reported regardless of Deps.ReadOnly; filtering them
// to what is actually registered is the caller's job.
func Capabilities(r ForgeReader) []string {
	if r == nil {
		return nil
	}
	capabilities := []string{"list_repos", "list_issues"}
	if _, ok := r.(fileReader); ok {
		capabilities = append(capabilities, "read_file")
	}
	if _, ok := r.(issueCreator); ok {
		capabilities = append(capabilities, "create_issue")
	}
	if _, ok := r.(repoCreator); ok {
		capabilities = append(capabilities, "create_repo")
	}
	return capabilities
}
```

`list_issues` and `create_repo` name tools that do not exist yet — they arrive with the companion spec. Nothing surfaces `Capabilities` to an MCP client until then, so the names are internal-only for now.

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/mcp/ -run TestCapabilities -v`
Expected: PASS, all three subtests.

- [ ] **Step 5: Run the full suite and static checks**

Run: `go test -race ./... && gofmt -l . && go vet ./... && golangci-lint run`
Expected: all packages PASS; no output from `gofmt -l .`; no findings.

If `golangci-lint` flags `repoCreator` as unused, that is expected at this point — it gains its consumer in `Capabilities` in the same step, so it should not fire. If it does, do **not** add `//nolint`; report it instead.

- [ ] **Step 6: Commit**

```bash
git add internal/mcp/tools.go internal/mcp/tools_test.go
git commit -m "feat(mcp): add ForgeReader and per-capability interfaces

Declares the tier-1 ForgeReader that every forge client satisfies, plus
small consumer-side interfaces for the file, issue and repo capabilities,
and a Capabilities helper reporting supported tool names.

No consumers yet: ForgeClient and the handlers migrate next."
```

---

### Task 3: Migrate handlers and the resolver, delete `ForgeClient`

The real change. `Deps.ClientFor`'s type change breaks `cmd/bridge/mcp.go` compilation immediately, so both files move together.

**Files:**
- Modify: `internal/mcp/tools.go:22-29` (delete `ForgeClient`), `:37` (`Deps.ClientFor` type), `:114`, `:152-162`, `:164-185` (handlers)
- Modify: `internal/mcp/tools_test.go` (`depsWith` return type, new negative tests)
- Modify: `cmd/bridge/mcp.go:177-211` (`newCachingClientResolver`)
- Modify: `cmd/bridge/mcp_test.go` (reader-only resolution test)

**Interfaces:**
- Consumes: `ForgeReader`, `fileReader`, `issueCreator` from Task 2; fakes from Task 1.
- Produces: `Deps.ClientFor` of type `func(forgeName, owner string) ForgeReader`; `newCachingClientResolver` returning `func(forgeName, owner string) imcp.ForgeReader`.

- [ ] **Step 1: Write the failing negative tests**

These are the regression tests the whole change exists for: a resolved-but-incapable client must **not** report as unconfigured. Append to `internal/mcp/tools_test.go`, and add `"strings"` to that file's imports:

```go
func TestHandleReadFile_TierOneClientReportsUnsupportedNotUnconfigured(t *testing.T) {
	// A tier-1-only client is the GitLab/ADO shape: resolves fine, has no GetFile.
	d := Deps{ClientFor: func(string, string) ForgeReader { return &fakeReader{name: "gitlab"} }}

	_, _, err := d.handleReadFile(context.Background(), nil,
		readFileInput{Forge: "gitlab", Owner: "o", Repo: "r", Path: "f.md"})

	if err == nil {
		t.Fatal("want an error for a client without GetFile, got nil")
	}
	if strings.Contains(err.Error(), "not configured") {
		t.Fatalf("a resolved but incapable client must not be reported as unconfigured: %v", err)
	}
	if !strings.Contains(err.Error(), "does not support") {
		t.Fatalf("want a does-not-support error, got %v", err)
	}
}

func TestHandleCreateIssue_TierOneClientReportsUnsupportedNotUnconfigured(t *testing.T) {
	d := Deps{ClientFor: func(string, string) ForgeReader { return &fakeReader{name: "gitlab"} }}

	_, _, err := d.handleCreateIssue(context.Background(), nil,
		createIssueInput{Forge: "gitlab", Owner: "o", Repo: "r", Title: "t", Confirm: true})

	if err == nil {
		t.Fatal("want an error for a client without CreateIssue, got nil")
	}
	if strings.Contains(err.Error(), "not configured") {
		t.Fatalf("a resolved but incapable client must not be reported as unconfigured: %v", err)
	}
	if !strings.Contains(err.Error(), "does not support") {
		t.Fatalf("want a does-not-support error, got %v", err)
	}
}

func TestHandleListRepos_TierOneClientIsFullyUsable(t *testing.T) {
	// The payoff: a client with only tier-1 capabilities still serves list_repos.
	reader := &fakeReader{name: "gitlab", repos: []forge.RepoRef{{Forge: "gitlab", Owner: "acme", Name: "widget"}}}
	d := Deps{
		DefaultOwners: []Target{{"gitlab", "acme"}},
		ClientFor:     func(string, string) ForgeReader { return reader },
	}

	_, out, err := d.handleListRepos(context.Background(), nil, listReposInput{})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Repos) != 1 || out.Repos[0].Name != "widget" {
		t.Fatalf("tier-1 client must serve list_repos: %+v", out)
	}
	if len(out.Warnings) != 0 {
		t.Fatalf("a capable tier-1 target must not warn: %+v", out.Warnings)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/mcp/ -run 'TierOne' -v`
Expected: FAIL to compile — `cannot use func literal (type func(string, string) ForgeReader) as func(string, string) ForgeClient`. `Deps.ClientFor` is still typed `ForgeClient`.

- [ ] **Step 3: Change `Deps.ClientFor` and delete `ForgeClient`**

In `internal/mcp/tools.go`, delete the `ForgeClient` interface (lines 22–29, the block whose comment begins "ForgeClient is the consumer-side interface") and change the `Deps` field on line 37 from:

```go
	ClientFor     func(forgeName, owner string) ForgeClient
```

to:

```go
	ClientFor     func(forgeName, owner string) ForgeReader
```

Update the `Deps` doc comment above it so "ready per-(forge, owner) client" reads "ready per-(forge, owner) reader".

- [ ] **Step 4: Migrate `handleReadFile`**

Replace the body of `handleReadFile` (lines 152–162) with:

```go
func (d Deps) handleReadFile(ctx context.Context, _ *mcp.CallToolRequest, in readFileInput) (*mcp.CallToolResult, readFileOutput, error) {
	client := d.ClientFor(in.Forge, in.Owner)
	if client == nil {
		return nil, readFileOutput{}, fmt.Errorf("forge %q not configured", in.Forge)
	}
	files, ok := client.(fileReader)
	if !ok {
		return nil, readFileOutput{}, fmt.Errorf("forge %q does not support reading files", in.Forge)
	}
	content, sha, found, err := files.GetFile(ctx, in.Owner, in.Repo, in.Path)
	if err != nil {
		return nil, readFileOutput{}, fmt.Errorf("read %s/%s/%s: %w", in.Owner, in.Repo, in.Path, err)
	}
	return nil, readFileOutput{Content: string(content), SHA: sha, Found: found}, nil
}
```

- [ ] **Step 5: Migrate `handleCreateIssue`**

Replace the body of `handleCreateIssue` (lines 164–185) with the following. Note the confirm check stays **before** client resolution, so a draft still costs nothing:

```go
func (d Deps) handleCreateIssue(ctx context.Context, _ *mcp.CallToolRequest, in createIssueInput) (*mcp.CallToolResult, createIssueOutput, error) {
	draft := createIssueOutput{
		Draft: true,
		Forge: in.Forge, Owner: in.Owner, Repo: in.Repo, Title: in.Title, Body: in.Body,
	}
	if !in.Confirm {
		return nil, draft, nil
	}
	client := d.ClientFor(in.Forge, in.Owner)
	if client == nil {
		return nil, createIssueOutput{}, fmt.Errorf("forge %q not configured", in.Forge)
	}
	issues, ok := client.(issueCreator)
	if !ok {
		return nil, createIssueOutput{}, fmt.Errorf("forge %q does not support creating issues", in.Forge)
	}
	issue, err := issues.CreateIssue(ctx, in.Owner, in.Repo, in.Title, in.Body)
	if err != nil {
		return nil, createIssueOutput{}, fmt.Errorf("create issue %s/%s: %w", in.Owner, in.Repo, err)
	}
	return nil, createIssueOutput{
		Draft: false,
		Forge: in.Forge, Owner: in.Owner, Repo: in.Repo, Title: in.Title, Body: in.Body,
		Issue: &issue,
	}, nil
}
```

`handleListRepos` and `handleCrossForgeStatus` need no changes — the former uses only tier-1 methods, the latter never calls `ClientFor`.

- [ ] **Step 6: Update `depsWith` in the test file**

In `internal/mcp/tools_test.go`, change `depsWith`'s inner closure return type from `ForgeClient` to `ForgeReader`:

```go
		ClientFor: func(forgeName, _ string) ForgeReader {
```

- [ ] **Step 7: Delete the type assertion in the resolver**

In `cmd/bridge/mcp.go`, replace `newCachingClientResolver` (lines 175–211, including its doc comment) with:

```go
// newCachingClientResolver wraps resolve (typically clientForMCP(roots)) with
// a resolve-once-per-(forge,owner) cache and adapts the returned forge.Client
// to imcp.ForgeReader. Token resolution walks the filesystem and spawns a
// direnv subprocess per call, so caching (including caching an unconfigured
// target's nil result) avoids paying that cost on every tool invocation for
// the life of the process.
func newCachingClientResolver(resolve func(forgeName, owner string) forge.Client) func(forgeName, owner string) imcp.ForgeReader {
	var (
		mu    sync.Mutex
		cache = map[string]imcp.ForgeReader{}
	)
	return func(forgeName, owner string) imcp.ForgeReader {
		key := forgeName + ":" + owner

		mu.Lock()
		if reader, ok := cache[key]; ok {
			mu.Unlock()
			return reader
		}
		mu.Unlock()

		var reader imcp.ForgeReader
		// imcp.ForgeReader's method set is a subset of forge.Client's, so a
		// non-nil client is assignable directly — no type assertion, and so no
		// path where a capable client silently degrades to nil. Assign only
		// when non-nil so a nil concrete pointer is never boxed into a
		// non-nil interface.
		if c := resolve(forgeName, owner); c != nil {
			reader = c
		}

		mu.Lock()
		cache[key] = reader
		mu.Unlock()
		return reader
	}
}
```

- [ ] **Step 8: Add the resolver regression test**

In `cmd/bridge/mcp_test.go`, update the `fakeResolvedClient` doc comment (line 15) to say `forge.Client/imcp.ForgeReader`, then append:

```go
// readerOnlyClient satisfies forge.Client — and therefore imcp.ForgeReader —
// while implementing none of the capability interfaces. This is the GitLab/ADO
// shape.
type readerOnlyClient struct{}

func (readerOnlyClient) Name() string { return "readeronly" }
func (readerOnlyClient) ListRepos(context.Context, string) ([]forge.RepoRef, error) {
	return nil, nil
}
func (readerOnlyClient) ListOpenIssues(context.Context, string, string) ([]forge.Issue, error) {
	return nil, nil
}

func TestNewCachingClientResolver_ReaderOnlyClientResolvesNonNil(t *testing.T) {
	cached := newCachingClientResolver(func(string, string) forge.Client { return readerOnlyClient{} })

	if c := cached("gitlab", "acme"); c == nil {
		t.Fatal("a client with only tier-1 capabilities must resolve non-nil; " +
			"the old type assertion dropped it to nil, which callers misreported as unconfigured")
	}
}
```

- [ ] **Step 9: Run the new tests to verify they pass**

Run: `go test ./internal/mcp/ -run 'TierOne' -v && go test ./cmd/bridge/ -run TestNewCachingClientResolver -v`
Expected: PASS for all four tests.

- [ ] **Step 10: Run the full suite and static checks**

Run: `go test -race ./... && gofmt -l . && go vet ./... && golangci-lint run`
Expected: all packages PASS; no output from `gofmt -l .`; no findings.

Every pre-existing test must still pass unchanged — GitHub and Forgejo satisfy every capability, so each assertion succeeds and their behaviour is identical.

- [ ] **Step 11: Verify `ForgeClient` is fully gone**

Run: `grep -rn "ForgeClient" --include='*.go' .`
Expected: no output. If any reference remains, it is a missed call site — fix it rather than leaving the name half-migrated.

- [ ] **Step 12: Commit**

```bash
git add internal/mcp/tools.go internal/mcp/tools_test.go cmd/bridge/mcp.go cmd/bridge/mcp_test.go
git commit -m "refactor(mcp): resolve clients as ForgeReader, assert capabilities

Deps.ClientFor now returns the tier-1 ForgeReader; read_file and
create_issue assert fileReader and issueCreator, failing with 'does not
support' instead of the misleading 'not configured'.

Because ForgeReader's method set is a subset of forge.Client's, the type
assertion in newCachingClientResolver is deleted rather than narrowed,
removing the path where a capable-but-partial client degraded to nil.

Unblocks exposing GitLab and ADO, which satisfy tier-1 but implement
neither GetFile nor CreateIssue. Wiring them is a separate change."
```

---

## Verification

After Task 3, this sequence must be clean from the repo root:

```bash
gofmt -l .            # no output
go vet ./...
golangci-lint run
go test -race ./...
```

The behavioural claim to confirm by eye in the diff: `handleReadFile` and `handleCreateIssue` each return **two distinguishable** errors — `not configured` for an unresolved target, `does not support` for a resolved but incapable one.

## Self-Review Notes

Checked against the spec:

- Tier-1 `ForgeReader` including `ListOpenIssues` — Task 2, Step 3.
- Three unexported capability interfaces at the consumer — Task 2, Step 3.
- `Capabilities` helper, nil-safe, write caps unfiltered — Task 2, Steps 1–3.
- Assertion deleted rather than narrowed — Task 3, Step 7, with the reason in the code comment.
- Handler table (`handleListRepos` unchanged, `handleCrossForgeStatus` untouched) — Task 3, Step 5 closing note.
- `ForgeClient` deleted — Task 3, Steps 3 and 11.
- Fake split into composable capability structs — Task 1.
- The named regression test (unsupported ≠ unconfigured) — Task 3, Step 1.
- `cmd/bridge/mcp_test.go` reader-only resolution — Task 3, Step 8.

Not in the spec, added here: `fakeRepos` in Task 1 (needed by Task 2's `Capabilities` test for a `repoCreator`-satisfying client) and the `TestHandleListRepos_TierOneClientIsFullyUsable` positive case, which asserts the payoff rather than only the error path.

## Pre-Verified Assumptions

The fake composition and every interface claim in this plan were compiled and
run against the repo toolchain before the plan was committed, via a temporary
test since deleted. Confirmed:

- `fakeFull` compiles and all field promotions used by the tests resolve.
- `*fakeReader` satisfies `ForgeReader` and **not** `fileReader` or
  `issueCreator` — without this, Task 3's regression tests would pass vacuously.
- `*forge.GithubClient` and `*forge.ForgejoClient` satisfy all four interfaces,
  so no existing test changes behaviour.
- `*forge.GitlabClient` and `*forge.ADOClient` satisfy `ForgeReader` and nothing
  more — the capability matrix in the spec is accurate.
- A `forge.Client` value is assignable to `ForgeReader` with no type assertion.
  This is the claim Task 3, Step 7 depends on; if it were false, the assertion
  could only be narrowed rather than deleted and the plan would need reworking.

Note the environment quirk: `go.mod` requires Go 1.25 while the local
`go` binary is 1.22.2, so the toolchain is fetched on demand. Commands run
**inside the repo** resolve it fine; a throwaway module elsewhere on disk
cannot fetch it. Run all verification from the repo root.
