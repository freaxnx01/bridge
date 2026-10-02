# Read-only PR, checks and workflow-run MCP tools — design

**Issue:** #326
**Date:** 2026-10-02
**Status:** approved

## Problem

The MCP surface can dispatch work (`add_labels`) but cannot see what came of it. There
is no tool for pull requests, their checks, or Actions runs, so verifying an
ai-implement run — the step that decides whether a human merges — needs a browser or a
terminal.

`GithubClient.ListOpenPullRequests` (`internal/forge/github.go:949`) already feeds
dispatch (`cmd/bridge/dispatch.go:207`) but is not exposed, and `forge.PullRequest`
(`internal/forge/client.go:137`) carries only number/title/body/draft.

## Non-goals

- No merge, approve, re-run or any other write. Merging stays a human act.
- No Forgejo implementation. Forgejo targets get a warning, not an empty list.
- No pagination beyond one page per endpoint (see *Limits*).

## Approach

Follow the existing capability-interface pattern (`internal/mcp/tools.go:20-112`): the
GitHub client grows methods, the MCP layer asserts small consumer-side interfaces, and
`Capabilities()` reports a tool name per satisfied interface. A separate CI client was
rejected — it would duplicate auth and `ClientFor` target resolution for no gain.

## Forge layer (`internal/forge`)

### `PullRequest` — additive

New fields, all `omitempty` so dispatch's JSON-free use is unaffected:

| Field | JSON | Source (GitHub) |
|---|---|---|
| `State` | `state` | `state` |
| `Author` | `author` | `user.login` |
| `HeadRef` | `head_ref` | `head.ref` |
| `HeadSHA` | `head_sha` | `head.sha` |
| `BaseRef` | `base_ref` | `base.ref` |
| `URL` | `url` | `html_url` |
| `Created` | `created` | `created_at` |
| `Updated` | `updated` | `updated_at` |
| `Merged` | `merged` | `merged_at != null` (list) / `merged` (single) |
| `MergeableState` | `mergeable_state` | `mergeable_state` (single PR endpoint only) |
| `ChangedFiles` | `changed_files` | `changed_files` (single PR endpoint only) |

`ListOpenPullRequests` keeps its signature and request; it populates the new fields from
the same response. Dispatch reads only `Number`/`Title`/`Body`/`Draft`, so its behaviour
and tests are unchanged.

### New types

```go
type PRFile struct { Path string; Additions, Deletions int }
type CheckRun struct { Name, Status, Conclusion, App string }
type CommitStatus struct { Context, State, Description, URL string }
type WorkflowRun struct {
    Name, Event, Status, Conclusion, Actor, TriggeringActor,
    HeadBranch, HeadSHA, URL string
    Created time.Time
}
```

(JSON tags snake_case; `App` is the check-run app slug.)

### New `GithubClient` methods

| Method | Endpoint |
|---|---|
| `ListPullRequests(ctx, owner, repo, state)` | `GET /repos/{o}/{r}/pulls?state=…&sort=updated&direction=desc&per_page=100` |
| `GetPullRequest(ctx, owner, repo, n)` | `GET /repos/{o}/{r}/pulls/{n}` |
| `ListPullRequestFiles(ctx, owner, repo, n)` | `GET /repos/{o}/{r}/pulls/{n}/files?per_page=100` |
| `ListCheckRuns(ctx, owner, repo, sha)` | `GET /repos/{o}/{r}/commits/{sha}/check-runs?per_page=100` |
| `ListCommitStatuses(ctx, owner, repo, sha)` | `GET /repos/{o}/{r}/commits/{sha}/status` (combined: latest per context) |
| `ListWorkflowRuns(ctx, owner, repo, branch, headSHA, limit)` | `GET /repos/{o}/{r}/actions/runs?branch=…&head_sha=…&per_page=limit` |

Owner/repo/branch/sha are escaped (`url.PathEscape` / `url.Values`). Forgejo implements
none of these.

## MCP layer (`internal/mcp`)

### Capability interfaces (`tools.go`)

```go
type prLister interface {
    ListPullRequests(ctx context.Context, owner, repo, state string) ([]forge.PullRequest, error)
}
type prReader interface {
    GetPullRequest(ctx context.Context, owner, repo string, number int) (forge.PullRequest, error)
    ListPullRequestFiles(ctx context.Context, owner, repo string, number int) ([]forge.PRFile, error)
    ListCheckRuns(ctx context.Context, owner, repo, sha string) ([]forge.CheckRun, error)
    ListCommitStatuses(ctx context.Context, owner, repo, sha string) ([]forge.CommitStatus, error)
}
type runLister interface {
    ListWorkflowRuns(ctx context.Context, owner, repo, branch, headSHA string, limit int) ([]forge.WorkflowRun, error)
}
```

`Capabilities()` appends `list_prs`, `get_pr`, `list_runs` for each satisfied
interface, so `list_git_forges` advertises them. None is a write tool (`isWriteTool`
unchanged), so all three are registered in `server.go` regardless of `--read-only`.

### Tools (handlers in a new `tools_prs.go`; `tools_read.go` is already ~400 lines)

**`list_prs`** — input `forge, owner, repo, state?, closes?`.
`state` defaults to `open`; anything other than `open|closed|all` is `invalidInput`.
`closes > 0` keeps only PRs where `dispatch.ClosesIssue(pr.Body, closes)` — the exact
predicate `dispatch.HasOpenPR` uses (`internal/dispatch/eligible.go:67-85`), imported,
not copied. Output `{prs, warnings}`; `Body` is cleared in the list output to keep it
small.

**`get_pr`** — input `forge, owner, repo, number`.
Fetches the PR first; a failure there is the tool's error. Then files, check-runs and
statuses for `HeadSHA`; each of those failing adds a warning and leaves its section
empty (partial result, as `update_repo` does with `topics_error`). Files are capped at
`maxPRFiles = 100`; `files_truncated` is set when `ChangedFiles > len(files)`. Output:

```json
{ "pr": {…}, "files": [...], "files_truncated": false,
  "checks": { "check_runs": [...], "statuses": [...] }, "warnings": [...] }
```

**`list_runs`** — input `forge, owner, repo, branch?, head_sha?, limit?`.
`limit` defaults to 20, clamped to `[1, 100]`. Output `{runs, warnings}`, each run
carrying `actor` and `triggering_actor`.

### Common rules

- **Deadline.** Each handler starts with
  `ctx, cancel := context.WithTimeout(ctx, toolTimeout)` (`const toolTimeout = 30 *
  time.Second`). The per-request 15 s `http.Client` timeout
  (`internal/forge/github.go:29`) bounds one request; this bounds the whole call, which
  for `get_pr` is four requests. A stall surfaces as an error wrapping
  `context.DeadlineExceeded` within 30 s.
- **Unconfigured forge** → error `forge %q not configured` (as `get_issue`).
- **Forge lacks the capability** (Forgejo) → empty result, no error, and
  `warnings: ["<forge> does not support <tool>"]` — the `search_code` convention.
- **Required inputs** (`owner`, `repo`, `number > 0` for `get_pr`) validated up front
  with `invalidInput`.

## Limits

One page per endpoint: 100 PRs (newest-updated first), 100 files, 100 check-runs, 100
runs. A `closes` filter over `state=all` therefore searches the 100 most recently
updated PRs — sufficient for verifying a recent dispatch, documented in the cheatsheet.

## Testing

- `internal/forge/github_test.go`: one `httptest` test per new method, asserting the
  request path/query and the field mapping (including `merged_at` → `Merged`, actor vs
  triggering_actor, app slug).
- `internal/forge`: `ListOpenPullRequests` test extended to assert the new fields; its
  existing assertions untouched.
- `internal/mcp/tools_read_test.go`: hand-rolled fakes per tool — happy path, Forgejo
  warning (fake without the capability), unconfigured forge error, `list_prs closes`
  filter (including `#410` not matching 41), invalid `state`, `list_runs` limit clamp,
  `get_pr` file cap + `files_truncated`, `get_pr` partial result when check-runs fail,
  deadline honoured (fake blocks on `ctx.Done()`).
- `internal/mcp/tools_test.go`: `Capabilities` reports the three tool names.
- Existing dispatch tests unchanged.

## Docs

- `docs/mcp-cheatsheet.md` tool table: rows for `list_prs`, `get_pr`, `list_runs`
  (GitHub-only, read-only, limits).
- `README.md:110`: the stale "four cross-forge tools" sentence points at the cheatsheet
  for the tool list instead of enumerating it.
- `CHANGELOG.md` `[Unreleased]` → `Added`.
