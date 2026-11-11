# Clear a stuck enrichment lock from the board — design

Issue: #385. Parent design: `2026-11-11-cockpit-today-design.md` (item 7).

## Problem

The Pipeline board folds an `enrichment-ongoing` issue unchanged for 6 h+ into
**Stuck enriching** (`web/src/lib/running.js:5-26`), but offers no fix. On
2026-11-11 `anim-bossinfo-ch/BI-ArchiveUploader` #612/#614 held a dead lock for
20 days until it was removed by hand. A dead lock also blocks `/enrich` itself
(its Step 1.5 stops on a fresh label), so the card cannot be re-enriched either.

## Goal

Two confirmed actions on every Stuck-enriching card:

- **Clear lock**: remove `enrichment-ongoing` from the issue.
- **Clear & re-enrich**: clear, then start the same session the Enrich button
  starts (`/enrich <N> --headless` in worktree `enrich-<N>`).

Both are guarded exactly like `POST /api/pipeline/{enrich,dispatch}`: bearer
token, Origin allowlist, 4 KiB body, same status-code table.

Non-goals: Forgejo/GitLab/Azure DevOps (no `RemoveLabel` on their clients),
clearing a lock that is not yet stuck, auto-clearing, changing the 6 h rule.

## Design

### API (`internal/api/pipeline.go`)

`PipelineHandler` gains `ClearLock func(ctx, LaunchRequest) error` and
`Now func() time.Time` (nil means `time.Now`). Two routes, both `POST`, wired
through the existing `guarded` chain in `cmd/bridge/serve.go:254`:

| Route | Success |
|---|---|
| `/api/pipeline/clear-lock` | `200 {"cleared": true}` |
| `/api/pipeline/clear-reenrich` | `200` with the `LaunchResult` of the enrich launch |

Server-side re-check (mirrors `enrich` requiring a card on the board): the
request must match a `Running` card with `kind == "enrichment"` whose `since`
is older than `StuckEnrichingAfter` (6 h, a Go constant kept in step with
`STUCK_ENRICHING_SECONDS`). Otherwise `409 not stuck` (not on the board at all:
`409 not on the board`). A lock that a live run just refreshed therefore cannot
be cleared by a stale page.

Status codes: the existing table, plus `422 lock clearing is not supported for
this forge` (client lacks `RemoveLabel`), `422 no token for this owner`, and
`502 forge call failed` (details in the server log only). The `inflight` guard
is shared with Enrich/Dispatch, so a clear and a launch for the same issue
cannot overlap (`409 launch already in progress`).

`clear-reenrich`: the clear runs first and aborts the request on failure. If the
launch then fails, the response is the usual `500 launch failed`; the lock stays
cleared and the page reloads the board, so the card is gone from Stuck.

### Forge write (`cmd/bridge`)

`clearEnrichmentLock` resolves the client with the same per-owner resolver the
collector uses (`cachedClients(clientForMCP(reposRoots()))`,
`cmd/bridge/serve.go:238`), because the service has no global `GH_TOKEN` and
the case that motivated this lives in another owner's private repo. It asserts
the client against a consumer-side interface:

```go
type lockClearer interface {
    RemoveLabel(ctx context.Context, owner, repo string, number int, label string) error
    CommentIssue(ctx context.Context, owner, repo string, number int, body string) (forge.Comment, error)
}
```

`GithubClient` satisfies it (`internal/forge/github.go:1127`, `:403`);
`ForgejoClient` has `CommentIssue` but no `RemoveLabel`
(`internal/forge/forgejo.go:363,380`), so it yields the 422. After the label is
gone, the collector refreshes that repo (`CollectRepo`, as launches do,
`cmd/bridge/pipeline.go:181-184`) so the board updates without waiting a tick.

Audit comment: after the label is removed, post
`🔓 Enrichment lock cleared from the cockpit at <UTC ts>`, the sibling of
`/enrich`'s `🔓 Enrichment lock released at` comment. It does not match
`/enrich`'s lock-detection pattern (`🔒 …`), so it cannot confuse a later run.
A failed comment is logged and does not fail the request: the label is the
state that matters.

### Frontend

- `web/src/lib/running.js`: export `isStuckEnrichment(card, now)`; `groupOf`
  uses it (one definition of "stuck").
- `RunningCard.svelte`: when `isStuckEnrichment(card, now)`, render **Clear
  lock** and **Clear & re-enrich** buttons. It decides stuckness itself, so
  `Pipeline.svelte` is not touched.
- `ConfirmDialog.svelte`: two new `action` values, `clear-lock` and
  `clear-reenrich`. Clear lock says it only removes the label and posts a
  comment (no Claude session); Clear & re-enrich also says it starts a paid
  session in `enrich-<N>`.
- State machine and failure texts follow `IssueCard.svelte`: confirm, working,
  done / failed, `409`/`422` reload the board.

## Assumptions

- **A1** [high] Two routes (`clear-lock`, `clear-reenrich`), not one route with a
  flag. Rejected: `POST /api/pipeline/clear` with `{"reenrich": true}`; a boolean
  switch is the flag argument the repo's rules forbid, and the two have
  different result shapes. Evidence: `internal/api/pipeline.go:53-73` already
  routes one path per action.
- **A2** [high] The server re-checks "still stuck" and answers 409. Rejected:
  trusting the page. Evidence: `enrich` and `dispatch` already re-check board
  membership (`internal/api/pipeline.go:101,118`); `RunningCard.Since` is the
  issue's `updated` time (`internal/cockpit/board.go:156`), so a refreshed lock
  is detectable.
- **A3** [med] Post an audit comment, best effort. Rejected: silent label
  removal. The lock comments are `/enrich`'s audit trail (`🔒`/`🔓`), and a
  cockpit-originated clear should leave the same kind of trace.
- **A4** [high] GitHub only, selected by capability (`RemoveLabel`) rather than
  by forge name. Rejected: a `Forge == "github"` check, which the `Running`
  card cannot do (it has no forge field, `board.go:30-40`). Adding one would
  touch `board.go`, which #366 is editing.
- **A5** [med] The stored token has label-write scope on every repo whose card
  can be stuck. Evidence: `GithubClient.RemoveLabel` exists (`github.go:1127`) but
  has no non-test caller yet, and the cockpit collector only reads, so write
  scope for the BI-ArchiveUploader owner is unproven. A forge 403 surfaces as `502 forge call failed`.
- **A6** [high] "Stuck" stays client-defined (6 h) with a duplicated Go
  constant. Rejected: a `stuck` flag on `RunningCard`, which edits `board.go`
  (#366 territory) and moves the existing frontend rule.
- **A7** [med] Re-enrich launches `/enrich <N> --headless` in `enrich-<N>`,
  reusing `pipelineHandler`'s `launch`. A leftover `enrich-<N>` worktree from the
  dead run is handled by `bridge launch` (`alreadyRunning`).

## Consequences

- Clearing posts a comment and removes a label, so the issue's `updated` time
  moves; if the re-enrich then fails to start, the issue reads as
  recently-updated and unlabelled and shows up under Needs enrichment on the next
  collect, not Stuck.
- The 6 h threshold now lives in two places (Go and JS); a test on each side pins
  the same number.
- #385 touches `docs/cockpit.md`, which #366 also edits.

## Testing

Go: handler tests (route table, 409 not stuck / not on board, 422, 502, shared
inflight, wrong method 405); `clearEnrichmentLock` with hand-rolled fakes
(remove then comment order, comment failure tolerated, no `RemoveLabel` 422, nil
client). Vitest: `isStuckEnrichment`, `RunningCard` buttons and confirm flow,
`ConfirmDialog` copy.
