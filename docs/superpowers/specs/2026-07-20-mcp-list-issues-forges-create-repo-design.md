# MCP: `list_issues`, `list_git_forges`, `create_repo`

Date: 2026-07-20
Status: approved, not yet implemented

## Problem

The Bridge MCP server registers four tools (`list_repos`, `read_file`,
`cross_forge_status`, `create_issue`). Two gaps follow from that set:

1. **No issue access.** `ListOpenIssues` exists on every forge client and is in
   the `forge.Client` interface, but the MCP layer never exposes it. The only
   way to see issues over MCP is `cross_forge_status`, which returns an
   opinionated ranked inbox — not a raw per-repo query.
2. **No discoverability.** A client calling `list_repos` must guess `"github"`
   or `"forgejo"` and guess an owner. `Deps.targets` rejects an owner given
   without a forge rather than guessing, so a wrong guess is a hard error with
   no way to discover the right answer.

`create_repo` is included because `CreateRepo` already exists on both wired
clients, and adding it alongside `create_issue` keeps write-tool handling in
one review.

## Non-goals

- Wiring the GitLab and ADO forge clients into the MCP resolver. They exist in
  `internal/forge` but `clientForMCP` only builds GitHub and Forgejo clients.
- Exposing Bridge's local surface (worktrees, sessions, capture) over MCP. That
  is a different security posture and needs its own design.
- `write_file`, `list_pull_requests`, `list_project_items`. Deferred.

## Tool contracts

### `list_issues` (read)

Inputs — all required:

| Field | Type | Meaning |
|---|---|---|
| `forge` | string | `github` or `forgejo` |
| `owner` | string | repository owner |
| `repo` | string | repository name |

Output: `{"issues": []forge.Issue}`.

Behaviour: resolve the client via `Deps.ClientFor(forge, owner)`; a `nil`
client returns `forge %q not configured`; otherwise delegate to
`ListOpenIssues` and wrap any error with the repo path. This mirrors
`handleReadFile` — a single target, fail-fast, no partial-success warnings.

Scope is deliberately one repo. Fanning out across all configured targets was
considered and rejected: it multiplies to repos × issues per call, and it
duplicates what `cross_forge_status` already aggregates. `list_issues` is the
raw live query; `cross_forge_status` stays the ranked view. The overlap is
acceptable because they answer different questions.

### `list_git_forges` (read)

No inputs.

```json
{
  "forges": [
    {"forge": "github",  "owner": "freaxnx01", "configured": true},
    {"forge": "forgejo", "owner": "freax",     "configured": false,
     "reason": "missing token or forge unavailable"}
  ],
  "read_only": false
}
```

Behaviour: iterate `Deps.DefaultOwners`, call `Deps.ClientFor` for each, and
report whether a client resolved. `reason` is set only when `configured` is
false, reusing the wording already used in `handleListRepos`'s warnings so the
two tools describe the same condition identically.

The call makes **no network requests**. `ClientFor` is wrapped by
`newCachingClientResolver`, so after the first resolution per target this is a
map lookup. A live probe of each forge's API was considered and rejected: it
turns discovery into N round-trips and conflates "not configured" with
"transient API failure".

An empty `DefaultOwners` returns `{"forges": [], "read_only": …}` — an empty
result, not an error.

### `create_repo` (write)

Registered only when `!Deps.ReadOnly`, and draft-by-default via `confirm`,
exactly mirroring `create_issue`.

| Field | Type | Meaning |
|---|---|---|
| `forge` | string | `github` or `forgejo` |
| `owner` | string | **selects which account's token to use** |
| `name` | string | new repository name |
| `private` | bool | repository visibility |
| `confirm` | bool | when false (default), returns a draft and creates nothing |

**`owner` selects credentials, not destination.** Both implementations POST to
`/user/repos` (`internal/forge/github.go:101`, `internal/forge/forgejo.go:97`),
which creates the repo under the account the token belongs to; no owner is
sent. The tool description must say so explicitly.

Consequences for the response shape:

- The **draft** response echoes the requested `owner`, because the resulting
  owner is not knowable without making the call.
- The **success** response carries the actual owner from the returned
  `forge.RepoRef`, not the requested one, so a mismatch is visible to the
  caller rather than silently papered over.

`errors.Is(err, forge.ErrRepoExists)` gets a distinct, actionable message
instead of a generic wrap.

## Interface change

`imcp.ForgeClient` gains two methods:

```go
ListOpenIssues(ctx context.Context, owner, repo string) ([]forge.Issue, error)
CreateRepo(ctx context.Context, name string, private bool) (forge.RepoRef, error)
```

`*forge.GithubClient` and `*forge.ForgejoClient` already satisfy both
structurally. **No changes to `internal/forge` are required.**

### Known limitation (accepted, not fixed here)

`newCachingClientResolver` (`cmd/bridge/mcp.go:201`) adapts a `forge.Client` to
an `imcp.ForgeClient` with a type assertion, and a failed assertion yields
`nil` — which `list_git_forges` will report as `configured: false` with a
"missing token" reason. Widening the interface widens what can fail this way.

This is inert today: only GitHub and Forgejo are wired and both satisfy the
full interface. It becomes a real constraint the moment a read-only forge is
wired, since GitLab and ADO implement neither `GetFile`, `CreateIssue`, nor
`CreateRepo` and would be silently reported as unconfigured. The fix is
splitting `ForgeClient` into reader and writer interfaces so `ReadOnly` mode
can accept read-only clients. Deferred to its own PR.

## File layout

`internal/mcp/tools.go` is 193 lines; three more tools would push it past 350
with reads and writes interleaved. Split along the boundary `ReadOnly` already
gates:

| File | Contents |
|---|---|
| `tools.go` | `Target`, `ForgeClient`, `Deps`, `targets()` |
| `tools_read.go` | `list_repos`, `read_file`, `cross_forge_status`, `list_issues`, `list_git_forges` handlers + their I/O types |
| `tools_write.go` | `create_issue`, `create_repo` handlers + their I/O types |

`server.go` registration order groups reads first, then the `!ReadOnly` write
block. Test files split to match: `tools_read_test.go`, `tools_write_test.go`,
with the shared `fakeForge` staying in `tools_test.go`.

This is a pure move — no handler logic changes as part of the split.

## Testing

Extend the existing hand-rolled `fakeForge` (`tools_test.go:14`) with
`ListOpenIssues` and `CreateRepo`, each recording its arguments and returning
injectable results/errors. No new test dependencies.

Table-driven cases:

**`list_issues`** — returns issues from a configured forge; unconfigured forge
returns an error naming the forge; a client error propagates wrapped.

**`list_git_forges`** — mixed configured/unconfigured targets produce correct
`configured` flags and a `reason` only on the false ones; empty
`DefaultOwners` yields an empty list, not an error; `read_only` reflects
`Deps.ReadOnly` in both states.

**`create_repo`** — `confirm: false` returns `draft: true` **and the fake
records zero calls** (the assertion that matters); `confirm: true` creates and
returns `draft: false` with the owner taken from the client's `RepoRef`, not
the input; unconfigured forge errors; `ErrRepoExists` produces the distinct
message.

**`server_test.go`** — the registered tool set is exactly the seven tools in
normal mode, and under `ReadOnly` it is the five reads with both
`create_issue` and `create_repo` absent.

Gate before merge: `gofmt -l .` empty, `go vet ./...`, `golangci-lint run`,
`go test -race ./...` all green.
