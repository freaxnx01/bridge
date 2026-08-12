# bridge `get_issue` MCP tool — design

**Issue:** [#235](https://github.com/freaxnx01/bridge/issues/235)
**Date:** 2026-08-12

## Problem

The MCP surface can write issue bodies and comments but cannot read them
back. `list_issues` returns metadata only (number, title, labels, created,
updated, url) — no body. There is no `get_issue`. This blocks the `agp
revise` design (regenerate an issue body from its scope plus every comment)
and makes backlog triage from titles alone unreliable, as seen with the
duplicate filed in #223.

## Goals

- A `get_issue(forge, owner, repo, issue_number)` MCP tool that returns an
  issue's body and its comment thread, in order, with authors.
- Works on both GitHub and Forgejo (parity, per issue's own note that this
  doesn't hit the `search_code`-style parity gap from #221).
- Bounded payload: a long comment thread must not be able to dominate the
  caller's context window.

## Non-goals

- Real pagination (offset/cursor) through comment threads — out of scope for
  this iteration; see "Comment truncation" below.
- Azure DevOps / GitLab support — `ADOClient` and `GitlabClient` only
  implement the tier-1 `Client` interface today and are not wired into the
  MCP server's `ClientFor`; `get_issue` follows the same capability-gated
  pattern as `comment_issue`/`read_file` rather than forcing those clients to
  grow a real-or-stub implementation.

## Design

### Types (`internal/forge/client.go`)

- `Comment` gains an `Author string` field (the commenter's login). This is a
  non-breaking addition that also benefits `comment_issue`'s existing
  response, which returns a `Comment` today without an author.
- `Issue` gains a `Body string` field (`json:"body,omitempty"`). `list_issues`
  continues to omit it in practice — none of the forge `ListOpenIssues`
  implementations will populate it, keeping that call's payload unchanged —
  but the field exists so `get_issue` can reuse `forge.Issue` for the issue
  half of its response instead of introducing a parallel type.

### Capability interface (`internal/mcp/tools.go`)

```go
// issueReader is asserted by get_issue.
type issueReader interface {
	GetIssue(ctx context.Context, owner, repo string, number int) (forge.Issue, []forge.Comment, error)
}
```

Registered in `Capabilities()` as `"get_issue"`, following the same
`if _, ok := r.(issueReader); ok { ... }` shape as the other capability
checks. Not added to `isWriteTool` — it's a read tool.

### Forge clients

`GithubClient.GetIssue` and `ForgejoClient.GetIssue` each make two calls:

1. `GET /repos/{owner}/{repo}/issues/{number}` (GitHub) /
   `GET /repos/{owner}/{repo}/issues/{number}` (Forgejo) — maps to `forge.Issue`
   including `Body`.
2. `GET .../issues/{number}/comments` (both forges expose this) — maps to
   `[]forge.Comment` with `Author` taken from the comment's `user.login`
   (GitHub) / `poster.login` (Forgejo).

Not implemented on `ADOClient` / `GitlabClient` — they simply don't satisfy
`issueReader`, and `get_issue` on those targets fails the same way
`comment_issue` does today: `forge %q does not support reading issues`.

### Handler (`internal/mcp/tools_read.go`)

```go
type getIssueInput struct {
	Forge       string `json:"forge" jsonschema:"forge hosting the repo: github or forgejo"`
	Owner       string `json:"owner" jsonschema:"repository owner"`
	Repo        string `json:"repo" jsonschema:"repository name"`
	IssueNumber int    `json:"issue_number" jsonschema:"issue number"`
}

type getIssueOutput struct {
	Issue             forge.Issue     `json:"issue"`
	Comments          []forge.Comment `json:"comments"`
	TotalComments     int             `json:"total_comments"`
	CommentsTruncated bool            `json:"comments_truncated,omitempty"`
}
```

`handleGetIssue` follows the `handleReadFile`/`handleListTree` shape:
resolve the client via `d.ClientFor`, assert `issueReader`, call `GetIssue`,
wrap any error with `fmt.Errorf("get issue %s/%s#%d: %w", ...)` (matching the
existing wrap style), then apply comment truncation and return.

### Comment truncation

No offset/limit pagination. `handleGetIssue` keeps the **newest 20** comments
(the last 20 in thread order — matches `agp revise`'s need to see the latest
human corrections, which arrive as the most recent comments) and sets:

- `TotalComments` = the full count returned by the forge
- `CommentsTruncated` = `true` when `TotalComments > 20`

The kept comments remain in thread (chronological) order — only the earlier
ones are dropped, not reordered.

### Error handling

A non-existent issue number propagates the forge client's 404 as a wrapped
error, matching how `update_issue`/`close_issue` behave today on the same
condition — no bespoke "not found" boolean or special-cased response shape.

## Testing

- `internal/forge/github_test.go` / `forgejo_test.go`: `GetIssue` happy path
  (issue + comments mapped correctly, including `Author`), a fixture with
  more than 20 comments to exercise the client's raw response shape (client
  returns everything; truncation is the handler's job, not the client's).
- `internal/mcp/tools_read_test.go`: `handleGetIssue` — found (untruncated),
  found with >20 comments (`CommentsTruncated: true`, 20 comments returned,
  `TotalComments` correct), not-found error, forge target lacking `issueReader`
  (e.g. a stub client) returns the "does not support reading issues" error,
  unconfigured forge returns the standard "not configured" error.
- `internal/mcp/tools_test.go`: `Capabilities()` reports `get_issue` for a
  client implementing `issueReader` and omits it otherwise.

## Acceptance criteria

- [ ] `get_issue` returns the body for a known issue on both GitHub and
      Forgejo.
- [ ] Comment thread is returned in chronological order with authors.
- [ ] A thread over 20 comments returns the newest 20, `comments_truncated:
      true`, and the correct `total_comments`.
- [ ] A non-existent issue number returns an error matching the shape of the
      other issue tools (e.g. `update_issue` on a bad number).
- [ ] A forge/owner target whose client doesn't implement `issueReader`
      (or is unconfigured) returns a clear capability/configuration error,
      not a panic or a silently empty result.
