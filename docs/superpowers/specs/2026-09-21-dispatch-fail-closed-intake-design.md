# Fail closed at intake and dispatch

**Issue:** [#303](https://github.com/freaxnx01/bridge/issues/303) — `fix(dispatch): capture-created issues are dispatch-eligible without enrichment`
**Date:** 2026-09-21
**Status:** approved

## Problem

`Eligible()` gates on the *absence* of `needs-enrichment` (`internal/dispatch/eligible.go:91-93`).
Issues arriving via capture carry no labels at all, so they are eligible the moment
they are filed. Observed 2026-09-21: `game-tschau-sepp` #31 (empty body, title
"Tschau Sepp Bug Report"), #32, #33; `game-huusli-jagd` #11; `bridge` #285, #289.

Enabling `dispatch --auto` today would send #31 — an issue with no body — to
implementation. The dispatcher's "running `/enrich` is the approval" contract
(`docs/dispatch.md`) holds only if every intake path stamps `needs-enrichment`.
No capture path does.

The issue filed this as unverified. It is now verified: `CreateIssue` has no
labels parameter anywhere in the codebase — not at `internal/forge/github.go:217`,
not at `internal/forge/forgejo.go:208`, not in either consumer interface
(`internal/capture/capture.go:77`, `internal/mcp/tools.go:58`). The label is
missing because nothing was ever able to set it, on any path. FlowHub is not
bypassing anything.

## Goals

1. Every bridge-originated issue is born carrying `needs-enrichment`.
2. `Eligible()` refuses an empty-body issue regardless of its labels.

The two are independent. Either alone would have stopped #31; both are specified
because intake can be bypassed by a path nobody has written yet, and the dispatch
gate cannot retroactively fix an issue already filed unlabeled.

## Non-goals

- Backfilling the six pre-existing unlabeled issues. The new dispatch gate skips
  `game-tschau-sepp` #31 automatically (empty body). `#32`, `#33`,
  `game-huusli-jagd` #11, `bridge` #285 and #289 have non-empty bodies and remain
  dispatch-eligible until enriched or labeled by hand. No backfill command.
- Any change to `/enrich`, to agent-workflow, or to the dispatcher's caps and
  ordering.
- Judging body *quality*. Only a blank or whitespace-only body is rejected; a
  one-line body passes. Deciding whether a body is good enough is `/enrich`'s job.

## Design

### Mechanism 1 — intake stamps the label

`CreateIssue` grows a `labels []string` parameter:

```go
CreateIssue(ctx context.Context, owner, repo, title, body string, labels []string) (Issue, error)
```

One atomic POST carries the label, so no window exists in which the issue is live
and unlabeled. The rejected alternative — create, then call the existing
`AddLabels` (`github.go:292`, `forgejo.go:282`) — leaves exactly such a window,
and a failed second call silently produces the dispatch-eligible unlabeled issue
this spec exists to prevent.

**Implementors (two).** `GithubClient` and `ForgejoClient`. `GitlabClient` and the
ADO client implement `ListOpenIssues` but not `CreateIssue`, so they are untouched.

**Consumers (two), both of which stamp the label.**

| Path | Interface | Call site |
|---|---|---|
| `POST /api/capture/issue` — FlowHub, locutus, Telegram capture | `capture.IssueCreator` (`internal/capture/capture.go:77`) | `CaptureIssue` (`capture.go:87`) |
| MCP `create_issue` tool, and the same tool over `/api/tools/` | `mcp.issueCreator` (`internal/mcp/tools.go:58`) | `handleCreateIssue` (`internal/mcp/tools_write.go:50`) |

Both pass `[]string{dispatch.LabelNeedsEnrichment}`. The MCP tool is a genuine
second intake path, not a hypothetical one: an agent — or FlowHub over MCP — can
reach it directly, and leaving it open would reproduce this bug through a
different door.

`handleCreateIssue`'s `confirm=false` draft path returns a preview of what would be
created (`tools_write.go:34-40`). That preview must report the label too, or the
draft misrepresents the issue the caller is about to confirm.

There is no opt-out. An agent that files an already-complete issue still gets
`needs-enrichment`; removing it is a deliberate act, which is the fail-closed
direction.

### Mechanism 1a — the label has to exist

`game-huusli-jagd` does not define `needs-enrichment` (verified 2026-09-21 via
`gh label list`). Creating an issue with an undefined label is therefore a live
failure mode, not a hypothetical one.

Each client gets an **unexported** helper that runs before the create. The
exported surface grows by nothing beyond the `CreateIssue` signature — no
`ListLabels`, no `CreateLabel`, no interface additions.

- **GitHub:** `GET /repos/{owner}/{repo}/labels/{name}`; on 404,
  `POST /repos/{owner}/{repo}/labels`. Then pass label *names* to create-issue,
  which is the shape GitHub expects. This also removes the design's dependence on
  GitHub's auto-create-on-issue-create behaviour, which cannot be verified without
  creating a real issue against the real API.
- **Forgejo:** `GET /api/v1/repos/{owner}/{repo}/labels`, match by name, create if
  absent, and pass the resulting **IDs**. The lookup is not optional on this forge:
  the instance's own swagger (`git.home.freaxnx01.ch/swagger.v1.json`, verified
  2026-09-21) types `CreateIssueOption.labels` as `list of label ids`,
  `array of integer/int64`.

Note the asymmetry is in the forge APIs, not in the design: both clients ensure the
label exists, and each then speaks its own forge's dialect.

### Mechanism 2 — dispatch rejects an empty body

```go
if strings.TrimSpace(i.Body) == "" {
    return false, "empty body"
}
```

Placed **first** in `Eligible()`, ahead of the `needs-enrichment` check. An issue
with no body is unfit for dispatch for a reason that has nothing to do with its
labels, and ordering it first makes the dry-run reason the honest one — `#31`
should read `SKIP (empty body)`, not `SKIP (needs-enrichment)`.

### Making the body visible to `Eligible()`

`ListOpenIssues` never maps `Body` — neither `ghIssue` (`internal/forge/github.go:362-379`)
nor `fjIssue` (`internal/forge/forgejo.go:522-531`) declares the field, though both forges return it in the list payload.
Shipping the gate without this makes `Eligible()` reject **every** issue.

Both structs gain a `Body` field and both `ListOpenIssues` implementations map it
into `Issue.Body`. This costs zero additional API calls — the data is already in a
response being parsed and discarded.

The rejected alternative — having the dispatcher call `GetIssue` per candidate —
costs N requests per tick and forces the empty-body check out of `Eligible()`,
which would stop being a pure function of `(issue, milestone, prs)`.

### The boundary rule

Populating `Body` on a list would leak full issue bodies into every consumer of
`[]forge.Issue`, including an MCP tool whose entire job is a *summary* listing.
So the change ships with a rule:

> `Issue.Body` is populated by the forge client for internal consumers, and
> cleared at every outward-facing boundary.

Cleared at two places:

- `handleListIssues` (`internal/mcp/tools_read.go:237-244`). This is the **shared**
  handler, deliberately: `internal/mcp/rest.go` exists so the MCP and
  `/api/tools/` REST transports run one implementation of each tool. Stripping at
  the MCP registration layer instead would leave REST leaking bodies the moment
  `list_issues` is added to the REST toolset.
- `ReposHandler`, before `writeJSON` (`internal/api/repos.go:88-91`). This is the
  endpoint FlowHub's catalogue polls; it needs titles and labels, not bodies.

`cmd/bridge/nav.go` and `cmd/bridge/issues.go` render named fields and are
unaffected either way.

The rule is a test target, not a convention: both boundaries assert an empty
`Body`, or the leak returns silently on the next refactor.

## Testing

Per the Go overlay: table-driven, `t.Run` subtests, hand-rolled fakes, existing
`httptest` servers. No new test dependency.

| Area | Assertion |
|---|---|
| `internal/dispatch/eligible_test.go` | Empty body, whitespace-only body, and non-empty body; plus the #31 shape (empty body, no labels at all) skipping with reason `empty body` |
| `internal/forge/github_test.go` | Create POST carries `labels`; ensure-label creates on 404 and does not create when the label exists; `ListOpenIssues` maps `Body` |
| `internal/forge/forgejo_test.go` | Same three, with label **IDs** on the create POST |
| `internal/capture/capture_test.go` | `CaptureIssue` passes `needs-enrichment` to the injected creator |
| `internal/api/capture_test.go` | The label reaches the creator through `POST /api/capture/issue` — AC #4 |
| `internal/mcp/tools_write_test.go` | `create_issue` passes the label; the `confirm=false` draft reports it |
| `internal/mcp/tools_read_test.go` | `list_issues` returns an empty `Body` |
| `internal/api/repos_test.go` | `GET /api/repos/{repo}` returns an empty `Body` |
| `cmd/bridge/dispatch_test.go` | `collectCandidates` drops an empty-body issue |

**AC #4 and #245.** The capture-path label assertion is what AC #4 asks for.
Issue #245 itself — integration-testing that `runServe` wires `requireBearer`
around `/api/capture/*` while leaving read endpoints open — is a different test
and stays open. This spec does not close it.

**Manual verification.** `bridge dispatch --dry-run` against the live backlog must
show `game-tschau-sepp #31` as `SKIP (empty body)` — AC #3. This cannot be
asserted in a unit test, so it is a step in the plan with its output recorded on
the PR.

## Acceptance criteria

- [ ] An issue created via `POST /api/capture/issue` carries `needs-enrichment`
- [ ] An issue created via MCP `create_issue` carries `needs-enrichment`, and the
      `confirm=false` draft reports it
- [ ] A missing `needs-enrichment` label is created in the target repo rather than
      failing the capture
- [ ] `Eligible()` rejects a blank or whitespace-only body with reason `empty body`;
      table test added
- [ ] `ListOpenIssues` populates `Body` on both GitHub and Forgejo
- [ ] `Body` is empty in `list_issues` output and in `GET /api/repos/{repo}`
- [ ] `bridge dispatch --dry-run` shows `game-tschau-sepp #31` as `SKIP (empty body)`
- [ ] `gofmt -l .` empty, `go vet ./...` clean, `golangci-lint run` clean,
      `go test -race ./...` green

## Known residue

- `game-tschau-sepp` #32, #33, `game-huusli-jagd` #11, `bridge` #285, #289 remain
  dispatch-eligible. They have bodies, so the new gate does not catch them.
  Deliberate: see Non-goals.
- `internal/forge/github.go:144` carries an orphaned doc comment
  (`// CreateIssue creates an issue on owner/repo…`) sitting above `put`, not above
  `CreateIssue`. Pre-existing; noted, not fixed.

## Blocking

`dispatch --auto` must not be enabled anywhere until this lands. It also blocks the
dispatch autonomy-lanes issue and agent-workflow `/autopilot`, both of which link
here.
