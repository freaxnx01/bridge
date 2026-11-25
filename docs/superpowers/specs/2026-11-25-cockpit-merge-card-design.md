# Cockpit merge card — merge agent PRs behind a head-SHA guard (#382)

Status: design, approved 2026-11-25. Parent design:
[`2026-11-11-cockpit-today-design.md`](2026-11-11-cockpit-today-design.md)
("Merge card: what the operator needs to trust Merge"). Wireframe and flow:
[`docs/design/cockpit-today/wireframe.md`](../../design/cockpit-today/wireframe.md)
("Merge card", "Confirm (Merge)", "Merge disabled"),
[`flow.md`](../../design/cockpit-today/flow.md) ("1b — Journey: Merge and Retry").

## Intent

An agent PR in a repo that needs a human merge waits until the operator opens
GitHub. The operator wants to merge it from the cockpit's inbox, trusting the
agent's own summary and the AI review verdict, without leaving the page. Today
that PR never becomes a card: a successful run (`ai:done`) maps to
`ActionNone` (`internal/dispatch/failure.go:115,157`) and `classify` drops it
(`internal/cockpit/inbox.go:129`).

## Goals

- A **merge card** in the inbox's `Cards` for every `ai:done` issue whose
  pipeline PR waits for a human merge.
- The card shows: the PR's `## Summary`, the review verdict with a "for the
  current head" check, check status, changed files with +/− counts.
- **[Merge]** renders only when checks are green, the verdict is `approve` for
  the PR's current head SHA, and the PR is not a draft. Otherwise the slot
  shows the reason.
- A guarded `POST` that re-checks everything live, squash-merges pinned to the
  head SHA the client saw, and deletes the head branch.

## Non-goals

- The diff (#383 builds on this card).
- The overnight digest sections (#381). After a merge the card leaves the
  inbox on the next collect; there is no "Done ✓ merged" row until #381.
- Retry / Retry with / Park (#374). This card has [Open PR ↗] and [Merge] only.
- Bypassing anything: no admin merge, no skipping required checks or reviews.
- Azure DevOps / Forgejo merge. GitHub only, like the rest of the inbox
  (`classify` already rejects non-GitHub repos).

## Anti-goals

- A Merge button that can merge something other than what the card showed.
  Every merge is pinned to the `headSha` the client saw (GitHub's
  `PUT /pulls/{n}/merge` `sha` parameter), so a push between render and click
  fails at GitHub even if bridge's own re-check raced.
- A verdict read from a comment anyone could post. Only trusted pipeline
  identities count.
- Per-request forge fan-out on `GET /api/inbox`.

## Decisions (approved by the operator, 2026-11-25)

1. **Dependencies dropped.** #382 no longer waits for #381 or #374. The card
   lives in #368's inbox (merged); the write path reuses the existing
   `guarded` chain in `cmd/bridge/serve.go:247` (token configured + bearer +
   same-origin), which Enrich, Dispatch and Clear-lock already use.
2. **Candidate rule.** A merge card exists when all hold:
   - the issue carries `ai:done`, is open, and is not parked;
   - the issue does **not** carry `ai-review-ai-merge`
     (`dispatch.LabelAIReviewAIMerge`) — that lane merges itself;
   - exactly one open PR closes the issue (`dispatch.ClosesIssue`) **and** is
     authored by a trusted pipeline identity (rule 4).
   This replaces the parent spec's repo-level `ai-review-human-merge` rule: a
   repo wired for AI merge still needs a human for issues dispatched without
   the `ai-review-ai-merge` label, and the repo rule would miss them.
   More than one matching PR → the card shows the reason "several open PRs
   close this issue" and no [Merge].
3. **Verdict source.** Read from the PR's issue comments authored by a trusted
   identity:
   - **Marker (preferred):** the comment's last line is
     `<!-- ai-review: {json} -->` (agent-workflow ADR-022). Take `verdict`,
     `head_sha`, `blocking`, `nits`. `v != 1` or unparsable JSON → no marker.
   - **Fallback:** the comment's first line is `<!-- review-pr:<sha> -->` and
     a line matches ``verdict: `(approve|request_changes|block|review_failed)` ``.
     `blocking`/`nits` are unknown (omitted).
   - The **latest** such comment whose head SHA equals the PR's current
     `head_sha` is the verdict for the head. A verdict only on an older SHA →
     reason "review on older commit". No review comment at all → "no review
     yet".
   Verified 2026-11-25: bridge PRs #389–#398 carry the fallback form only
   (bridge's `agent.yml` predates the marker), so the fallback is the
   day-one path, not an edge case.
4. **Trusted identities.** `BRIDGE_PIPELINE_BOTS` — a comma-separated list of
   GitHub logins, default `github-actions[bot]`. Used for both PR authorship
   and review comments. Re-reviews are posted with the pipeline App's token
   when one is configured (agent-workflow ADR-022 item 8), so a repo using
   `PIPELINE_APP_ID` must list the App's bot login here. Resolved once at
   startup into the config, not read deep in the code. Do not reuse
   `dispatch.pipelineReportAuthor`'s single-author rule.
5. **Evidence is collected on the issues tick, not per request.** For each
   merge candidate, `fetchIssues` adds: the single-PR read
   (`GetPullRequest`: draft, `mergeable_state`, head SHA, body), the PR's
   issue comments (verdict), `ListCheckRuns` + `ListCommitStatuses` on the
   head SHA, and `ListPullRequestFiles`. Body, comments and files are cached
   keyed by `(repo, pr, head_sha)` — a new push changes the key. Checks and
   the PR read itself are re-read every tick (they change without a push).
6. **Checks green** = every check run on the head SHA is `completed` with
   conclusion `success`, `neutral` or `skipped`, and every commit status is
   `success`. Any run still queued/in progress → "checks running"; any other
   conclusion/state → "checks failing". Zero checks and zero statuses → "no
   checks reported" (no [Merge]: a repo whose CI has not started must not
   look green).
7. **Merge.** `POST /api/inbox/merge` with
   `{"repo":"owner/name","number":<issue>,"pr":<pr>,"headSha":"<sha>"}`,
   behind `guarded`. The server re-reads the PR, checks and review comments
   **live** (not from the snapshot) and applies the same eligibility function
   the card uses; any reason → `409 {"error":"<reason>"}`. Also `409` when
   `headSha` ≠ current head ("head moved"). Then `PUT /repos/{o}/{r}/pulls/{n}/merge`
   with `merge_method: squash` and `sha: headSha`, using the owner's token
   (`remote.GitHubToken`, as Clear-lock does). GitHub refusals are mapped:
   405/409/422 → `409` with GitHub's message as the reason; 403 → `403` with
   the message. Nothing is retried with different parameters.
8. **Branch deletion.** After a `200` merge,
   `DELETE /repos/{o}/{r}/git/refs/heads/{head_ref}`. Only when the head repo
   is the base repo (no fork branches). A delete failure does **not** fail
   the merge: the response is `200` with `"branchDeleted": false` and the
   error text, and the UI says "merged; branch not deleted: …". A `422`
   "Reference does not exist" counts as deleted (the repo's own
   `delete_branch_on_merge` got there first).
9. **After merge.** The handler triggers `CollectRepo` for that repo so the
   card leaves the inbox without waiting for the 10-minute tick; the SSE
   `inbox-updated` event refreshes open pages.

## Settled after the builder check (author's calls)

- **Precedence.** The merge rule runs only where today's mapping yields
  `ActionNone` *and* the issue carries `ai:done`: `classify` computes
  `NextAction` first, and only an `ActionNone` result is tested against the
  candidate rule. Any failure, blocked, paused or parked state keeps its
  existing card and never becomes a merge card. `ai:done` is already in
  `inboxLabels` (`internal/cockpit/inbox.go:22-27`), so comments are read.
- **No matching open PR** (already merged, closed, or not yet opened) → no
  card. That is the `ActionNone` behaviour today.
- **Evidence read fails on a tick** → keep the last good evidence for that
  `(repo, pr)` (the collector already keeps stale comment threads,
  `collector.go:626-650`), set `canMerge=false` with reason "forge read
  failed (evidence stale)", and record the error in `Inbox.Errors` like
  comment errors. If no evidence was ever read, the card still shows, with
  that reason and no evidence block.
- **Review comment pagination.** PR comments are read through the existing
  paginated `listIssueComments` (`internal/forge/github.go:710`, bounded by
  `maxCommentPages`). The latest verdict on a long thread may fall past the
  cap; the card then says "no review yet" or "review on older commit". That
  fails safe, which is enough.
- **SHA comparison** is exact and case-insensitive on the full 40-hex SHA. The
  fallback's `<!-- review-pr:<sha> -->` carries the full SHA (verified on PR
  #398: `96fae5fd5d9aa4022a3b5a57a69b50510a563891`), and the marker's
  `head_sha` is the same `HEAD_SHA`.
- **`mergeable_state: unknown`** is **not** a blocker, on the card or on
  POST. GitHub computes it lazily, and the merge call itself refuses
  conflicts, so GitHub decides. Only `dirty` ("conflicts") blocks
  bridge-side. The "mergeability unknown" reason below is dropped.
- **POST handler wiring.** `POST /api/inbox/merge` goes through
  `InboxHandler`, which gains a method/path split: GETs stay open, POST
  `merge` is wrapped in `guarded` in `serve.go`. The handler takes a narrow
  consumer-side interface (read PR, checks, statuses, PR comments, merge,
  delete branch; resolved per owner token) plus the bots list. It first
  requires the issue to be a merge card in the current snapshot ("not on the
  inbox" → `409`), then re-reads live.
- **No `## Summary` section** → `summary` is "" and the card says "no summary
  in PR body". No guessing from other paragraphs.

## Card payload

`InboxCard` gains one optional field, filled only for `Kind == "merge"`:

```text
merge: {
  pr, prUrl, headSha, headRef, draft, mergeableState,
  summary,                       // the PR body's "## Summary" section, or ""
  review: { verdict, headSha, current: bool, blocking?, nits?, source: "marker"|"fallback" } | null,
  checks: { state: "green"|"running"|"failing"|"none", passed, total },
  files: [{path, additions, deletions}], filesTruncated: bool,
  canMerge: bool, reason: string  // reason is "" iff canMerge
}
```

Exact JSON names are the builder's call; document them in `docs/cockpit.md`.
The card's `reason` (existing field) is `"ready to merge"` or the blocking
reason, so the existing card list renders something sensible before the UI
learns the new kind.

## Urgency

Merge cards sort in group 1 (work waits for review), with `review blocked`
etc. Within the group, oldest `Since` first as today. `Since` is the PR's
`Updated`.

## Reasons (exact wording is the builder's, these are the cases)

draft · checks running · checks failing · no checks reported · no review yet ·
review on older commit · review: request_changes / block / review_failed ·
conflicts (`mergeable_state` `dirty`) · several open PRs close this issue ·
forge read failed (evidence stale) · not on the inbox (POST only) · head
moved (POST only).

`mergeable_state` `blocked` (branch protection not yet satisfied) is **not**
a bridge-side reason when checks are green — GitHub decides at merge time and
its refusal is shown (decision 7). `behind` is allowed (squash of a behind
branch is legal unless protection requires up-to-date, which GitHub enforces).

## UI

Per the wireframe "Merge card" (collapsed) and "Confirm (Merge)":

- New `MergeCard.svelte` rendered by the inbox for `kind === "merge"`:
  `✓ <repo> #<issue> → PR #<pr>`, title, `checks ✓ n/n · review: <verdict>`,
  first line of the summary (tap expands the full summary), files count with
  +/− totals and an expandable file list, [Open PR ↗], and [Merge] or the
  reason in its slot.
- [Merge] → `TokenDialog` if no token → `ConfirmDialog` naming the exact
  effect: "Merge PR #<pr> into <base>? <repo> · squash · checks n/n ✓ · review
  approve on <sha7> (head) · Closes #<issue>. The branch is deleted after
  merge." → POST.
- `409` → reason inline on the card, card refreshed, no merge. `401` →
  TokenDialog. `403`/`503` → error text. 5xx → error toast, card unchanged.
  `200` → toast "Merged PR #n" (plus the branch note when not deleted).
- The diff button is #383's; leave room in the layout, do not stub it.

## Constraints

- Go stdlib + existing forge client; no new modules. Hand-rolled fakes in tests.
- New forge methods on `GithubClient`: `MergePullRequest(ctx, owner, repo, number, sha, method)`
  and `DeleteBranch(ctx, owner, repo, ref)`, plus listing a PR's issue
  comments (reuse `listIssueComments` — a PR is an issue for that endpoint).
- `go test -race ./...`, `golangci-lint run`, web `vitest` + `vite build`
  green.

## Approaches considered

- **A. Evidence on the issues tick, cached by head SHA (chosen).** Matches the
  cockpit rule "no per-request forge fan-out"; `GET /api/inbox` stays a
  snapshot read. Cost: a few extra REST calls per merge candidate per 10-min
  tick, bounded by the number of waiting agent PRs (single digits).
- **B. Card carries only the PR number; evidence fetched lazily per card.**
  Fresher, but every page load fans out to GitHub — rejected by the parent
  spec's constraint.
- **C. Wait for #374's write-action framework.** Rejected by the operator: the
  existing `guarded` chain already gives bearer + Origin + confirm.

## Left to the builder

- JSON field names; where the eligibility function lives (it must be one
  function shared by the collector and the POST handler).
- Summary extraction details (heading level, trimming).
- Files list cap (first page of 100 is fine; set `filesTruncated` when
  `changed_files` > len).
- Rate-limit budget logging.

## Assumptions (author's calls)

- **A1** [high] The pipeline opens exactly one closing PR per issue; several
  is an anomaly shown as a reason, not resolved by bridge.
- **A2** [med] `ai-review-ai-merge` on the issue is the signal that the lane
  merges itself. Evidence: `internal/dispatch/eligible.go:18`,
  `internal/dispatch/redispatch_test.go:42`.
- **A3** [med] Branch deletion only for same-repo heads; agent PRs are never
  from forks.
- **A4** [high] `mergeable_state: blocked` is left to GitHub (decision 7) so
  bridge never second-guesses branch protection it cannot fully read.
