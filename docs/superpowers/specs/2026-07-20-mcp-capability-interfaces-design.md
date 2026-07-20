# MCP: capability interfaces for forge clients

Date: 2026-07-20
Status: approved, not yet implemented
Blocks: `2026-07-20-mcp-list-issues-forges-create-repo-design.md`

## Problem

`internal/mcp` declares one fat `ForgeClient` interface (`tools.go:24`)
requiring `Name`, `ListRepos`, `GetFile` and `CreateIssue`. Only GitHub and
Forgejo satisfy it. The actual capability matrix is tiered:

| Capability | Github | Forgejo | Gitlab | ADO |
|---|---|---|---|---|
| `Name`, `ListRepos`, `ListOpenIssues` | yes | yes | yes | yes |
| `GetFile` | yes | yes | — | — |
| `CreateIssue`, `CreateRepo` | yes | yes | — | — |
| `PutFile` | yes | — | — | — |

Two consequences:

1. **GitLab and ADO cannot be exposed over MCP at all**, even for the tier-1
   operations they fully support. They exist in `internal/forge` but are
   unreachable from the MCP server.
2. **Failure is silent and misreported.** `newCachingClientResolver`
   (`cmd/bridge/mcp.go:201`) adapts `forge.Client` to `imcp.ForgeClient` with a
   type assertion, and a failed assertion yields `nil` — indistinguishable from
   "no token configured". A forge that is present and authenticated but merely
   lacks `GetFile` reports as unconfigured.

A plain reader/writer split does **not** fix this: `GetFile` is a read
operation that GitLab and ADO lack, so it would sit in the reader interface and
keep them excluded. The axis that matters is capability, not read vs. write.

## Non-goals

- Wiring GitLab and ADO into `clientForMCP`. This change unblocks them
  structurally; wiring needs token resolution in `internal/remote`, which today
  knows only GitHub and Forgejo. Separate PR.
- Adding new tools. Covered by the companion spec.
- Any change to `internal/forge`.

## Design

### Tier-1 interface

```go
// ForgeReader is the baseline every forge client satisfies.
type ForgeReader interface {
	Name() string
	ListRepos(ctx context.Context, owner string) ([]forge.RepoRef, error)
	ListOpenIssues(ctx context.Context, owner, repo string) ([]forge.Issue, error)
}
```

`Deps.ClientFor` returns `ForgeReader` instead of `ForgeClient`.

`ForgeReader`'s method set is a **subset** of `forge.Client`'s, so a
`forge.Client` is assignable to a `ForgeReader` implicitly. The type assertion
in `newCachingClientResolver` is therefore **deleted, not narrowed** — with it
goes the silent-nil path and the misleading "not configured" report. The
resolver keeps its caching behaviour, including caching a genuine `nil` for an
unconfigured target.

`ListOpenIssues` is included even though no tool uses it yet. It is part of
`forge.Client`, every client implements it, and including it means the
companion spec needs no further interface change.

### Capability interfaces

Declared at the consumer, one to three methods each, per the Go overlay rule
*"Define interfaces at the consumer, keep them small."* Unexported — nothing
outside `internal/mcp` needs to name them.

```go
type fileReader interface {
	GetFile(ctx context.Context, owner, repo, path string) ([]byte, string, bool, error)
}

type issueCreator interface {
	CreateIssue(ctx context.Context, owner, repo, title, body string) (forge.Issue, error)
}

type repoCreator interface {
	CreateRepo(ctx context.Context, name string, private bool) (forge.RepoRef, error)
}
```

A handler needing a capability asserts for it and fails with a message that
names the real problem:

```go
reader := d.ClientFor(in.Forge, in.Owner)
if reader == nil {
	return nil, readFileOutput{}, fmt.Errorf("forge %q not configured", in.Forge)
}
fr, ok := reader.(fileReader)
if !ok {
	return nil, readFileOutput{}, fmt.Errorf("forge %q does not support reading files", in.Forge)
}
```

The two failures are now distinct: *absent* versus *present but incapable*.

### Capability reporting helper

A single exported helper returns the capabilities a resolved client actually
has, so the companion spec's `list_git_forges` has one source of truth rather
than duplicating assertions:

```go
// Capabilities returns the tool names a resolved client supports.
func Capabilities(r ForgeReader) []string
```

Returns tier-1 names unconditionally, then appends per successful assertion.
Returns nil for a nil reader. Write capabilities are included here regardless
of `ReadOnly`; filtering by registration state is the caller's job.

## Handler changes

Mechanical, no behavioural change for GitHub or Forgejo — both satisfy every
capability, so every assertion succeeds:

| Handler | Change |
|---|---|
| `handleListRepos` | none beyond the `ForgeReader` type |
| `handleReadFile` | assert `fileReader` |
| `handleCreateIssue` | assert `issueCreator` |
| `handleCrossForgeStatus` | none — does not use `ClientFor` |

`ForgeClient` is deleted.

## Testing

`fakeForge` (`tools_test.go:14`) currently implements everything at once, which
cannot express a partial client. Replace with a fake whose capabilities are
selectable, so tests can construct a tier-1-only client:

- Embed the tier-1 methods on `fakeForge`.
- Put `GetFile`, `CreateIssue` and `CreateRepo` on separate embeddable structs
  so a test composes exactly the capability set it wants.

Cases:

- A tier-1-only client resolves successfully and serves `list_repos`.
- `read_file` against a tier-1-only client returns the *does not support*
  error, and specifically **not** the *not configured* error — the regression
  this whole change exists to prevent.
- `create_issue` likewise.
- Full clients behave exactly as before (existing assertions unchanged).
- `Capabilities` returns tier-1 for a partial client and the full set for a
  complete one; nil for nil.
- `cmd/bridge/mcp_test.go`: `newCachingClientResolver` returns a usable
  non-nil reader for a client that lacks write methods — impossible before this
  change, and the direct test of the deleted assertion.

Gate before merge: `gofmt -l .` empty, `go vet ./...`, `golangci-lint run`,
`go test -race ./...` all green.

## Sequencing

Land this first. The companion tools spec then adds `list_issues`,
`list_git_forges` and `create_repo` against a stable interface, and its
`list_git_forges` output reports real per-target capabilities instead of a bare
`configured` boolean.
