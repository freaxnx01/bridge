# `GET /api/digest`: dispatch and pipeline activity since a timestamp (#380)

Parent design: [`2026-11-11-cockpit-today-design.md`](2026-11-11-cockpit-today-design.md)
(issue 2 of the Cockpit Today list). UI it feeds (#381):
[`docs/design/cockpit-today/wireframe.md`](../../design/cockpit-today/wireframe.md),
[`flow.md`](../../design/cockpit-today/flow.md). Producer: the dispatch journal
(#379, [`2026-11-11-dispatch-journal-design.md`](2026-11-11-dispatch-journal-design.md)).
Collector and inbox: #368 ([`2026-11-11-issue-inbox-design.md`](2026-11-11-issue-inbox-design.md),
[`docs/cockpit.md`](../../cockpit.md)).

Status: spec written headless by `/enrich 380 --headless` on 2026-11-11. The
decisions the operator left to the builder are listed under "Assumptions" in
the issue body.

## Why

The overnight digest needs one read-only source. For a `since` timestamp, it
joins what bridge decided (the journal) with what the pipeline did (run
markers and PRs), per issue. It must also say whether the dispatcher ran at
all. The trust rule binds it: **every "nothing" carries a reason.** That means
missing ticks, inert ticks and a missing journal are explicit fields, never
empty arrays.

## The gap this spec has to close first

The #368 collector cannot see most of what the digest reports:

- `Issues` is `ListOpenIssues` (`cmd/bridge/pipeline.go:108-114`). An
  autonomous self-merge closes its issue, so the issue leaves the snapshot.
  `pruneComments` (`internal/cockpit/collector.go`) then drops its thread, and
  with it the run markers and their `cost_usd`.
- `PRs` is `ListOpenPullRequests`, fetched only for wired repos that have a
  gate candidate (`fetchIssues`). Merged PRs are never read.
- `forge.PullRequest` keeps `Merged bool` but drops `merged_at`, although
  `ghPull` decodes it (`internal/forge/github.go:1082,1090`).

Built on the existing snapshots alone, "merged" would read 0, and spend would
miss every run that ended in Done. Both break the parent design ("autonomous
self-merges count as merged", "spend from `cost_usd`").

## Decisions

### 1. New data the collector gathers (the cheapest source)

All of it is gathered on the collector's existing ticks. A request never
reaches a forge.

- **Recent PRs.** On every issues tick (10 min), each **wired GitHub repo**
  gets one call to the existing `GithubClient.ListPullRequests(ctx, o, r,
  "all")`. That is one page of 100 PRs, most recently updated first
  (`internal/forge/github.go:1112-1122`). PRs updated before `now − 7 d` are
  dropped. The cost is one REST call per wired repo per 10 minutes, about 6
  calls per repo per hour. With 20 wired repos that is about 120 calls per
  hour, or 2.4% of the 5,000/h token quota.
- **`forge.PullRequest.MergedAt time.Time`.** This is an additive field, set
  from `ghPull.MergedAt`. `Updated` is not a stand-in for it.
- **Pipeline PR.** A PR is a pipeline PR when either of these holds:
  - its author is `github-actions[bot]` (agent-workflow
    `scripts/find-pipeline-pr.sh`, default `AUTHOR_ALLOWLIST`; the same
    identity `dispatch.pipelineReportAuthor` trusts) **and** its body closes an
    issue (`dispatch.ClosingIssues`, new, same `closesRE` as `ClosesIssue`);
  - a trusted run marker on the issue names it (`RunRecord.PR`).
- **Closed-issue threads.** A pipeline PR can close issue N while N is not in
  the open snapshot. For each such PR, the collector reads N once through the
  existing `Sources.Issue` (`GetIssue`: issue plus comments). It caches the
  thread keyed by `(repo, N)` and the PR's `Updated`, and re-reads only when
  that PR's `Updated` moves. Entries whose PR is no longer in the recent list
  are pruned. This is the only new per-issue call, and each closed issue costs
  it about once.
- **Journal.** On every runs tick (60 s), the collector calls
  `Sources.JournalStamp`, which is cheap: the newest day file's name, size and
  mtime. Only when the stamp has changed does it call `Sources.Journal`, which
  reads the **whole** journal, up to 30 days. The whole journal is needed
  because the expected-tick range needs the first record ever, not the first
  record in the window. Records whose `v` is not `dispatch.JournalVersion` are
  dropped. A missing journal dir is `present = false`, not an error.
- The new `Sources` funcs (`RecentPRs`, `Journal`, `JournalStamp`) **may be
  nil**. A nil func disables that part of the digest. Existing collector tests
  and #366's edits therefore need no change to the test fake.

### 2. The window

- `since` is an RFC 3339 query parameter, and it is required.
- A `since` older than `now − 7 d` (the widest preset) is clamped to
  `now − 7 d`, and the payload says `sinceClamped: true`. The recent-PR data
  reaches back only that far.
- A `since` more than one minute in the future is a `400`.
- Every count and section covers `[since, now]`. The journal is filtered in
  memory.

### 3. Tick health (expected vs. seen)

- **Slots.** The timer fires at `:00` and `:30`
  (`docs/systemd/bridge-dispatch.timer`, `OnCalendar=*-*-* *:00,30:00`). The
  digest uses a constant `TimerSlot = 30 * time.Minute`. A test parses the
  unit file and fails if its `OnCalendar` changes. That is how "taken from the
  unit's schedule where practical" holds without parsing systemd calendar
  syntax at runtime.
- **Range.** The range starts at the later of `since` and the **first journal
  record** (any trigger, as the parent design says). It ends at `now`. The
  first slot is the one that contains the range start, so a fresh install's
  first tick at `S+20s` fills slot S. A slot S is expected once `now ≥ S + TickGrace` (5 min), so the current slot is not
  reported missing before its tick has had time to land. Slots fall on
  multiples of 30 min of UTC time. That matches local `:00`/`:30` in every
  zone whose offset is a whole or half hour.
- **Seen.** Slot S is seen when a `trigger: timer` record has `at` in
  `[S, S+30m)`. Manual records never fill a slot.
- **Gaps.** Adjacent missing slots merge into one gap,
  `{from: first missing S, to: last missing S + 30m, missing: n}`. That
  produces the wireframe's "no dispatcher tick 23:30–02:00 (5 expected, 0
  seen)".
- **Inert.** A record is inert when its outcome is `decided` or
  `no_candidates` and `repos` is empty. Paused, outside-window and error
  records legitimately have no repos and are never inert. The payload carries
  the inert count, the last inert time, and a per-tick `inert` flag.
- **Journal missing.** The payload says `journal.present: false` and has no
  tick math (`expected`/`seen` 0, no gaps). Outcomes still come from the forge
  data: "outcomes from forge only".

### 4. Items (the per-issue story)

An issue becomes a digest item when any of these falls in the window:

- a journal decision `dispatched` for it;
- a trusted run marker `posted` on it;
- one of its pipeline PRs `created` or `mergedAt`.

`would_dispatch`, `skipped` and `not_applied` decisions never make an item.
They belong to "Didn't happen".

**Story steps**, oldest first. A step is one of:

- `dispatched` / `retried`: journal decision `dispatched`, `kind`
  `dispatch` / `retry`, with `by` and `lane`;
- `run`: a marker, with `status`, `class`, `decision`, `attempt`, `costUsd`
  and `pr`;
- `pr_opened`: PR `created`;
- `merged`: PR `mergedAt`;
- `pr_closed`: the PR was closed unmerged, at its `updated`.

Only steps inside the window are listed. The UI compresses them
(`dispatched 02:00 → failed task_failure → retried 03:00 → PR #374 ✓`).

**`by` (dispatched by: timer / manual / session).** Take the item's first
in-window step.

- If that step is a journal dispatch, `by` is its record's `trigger`.
- Otherwise, look back through the whole journal read for a `dispatched`
  decision for this issue. It must be at or before that step and after the
  issue's previous marker. A match gives that record's `trigger`.
- Anything else is `session`: someone ran `/gh:implement` or labelled the
  issue by hand.

**Group**, first match wins:

1. A pipeline PR merged in the window → `done`.
2. The latest marker (at any time) has `status: ai:done` → `done`. A PR open
   and green shows under Done, as in the wireframe.
3. The run is in flight (#368's `inFlight`), the issue is in the inbox's
   `waiting` list, the latest marker says `decision: retry`, or the last step
   is a dispatch with no run after it → `going`.
4. The latest marker says `decision: stop` → `stopped`.
5. Otherwise → `going`.

**`inNeedsYou`** is true when `repo#number` is one of #368's `Inbox().Cards`.
The UI then shows only `→ in Needs you` for it.

Items are sorted newest last-step first. A journal decision for an issue the
snapshot does not know (it is closed, and no pipeline PR names it) still makes
an item. It takes its title from the journal decision and its URL from
`https://github.com/<repo>/issues/<n>`.

### 5. Summary counts

| Count | Definition |
|---|---|
| `dispatched` | journal `dispatched` decisions in the window (both kinds), plus one per item whose `by` is `session` |
| `prsOpened` | pipeline PRs `created` in the window |
| `merged` | pipeline PRs `mergedAt` in the window |
| `stopped` | items in group `stopped` |
| `spendUsd` | sum of `costUsd` of every trusted marker `posted` in the window, on open and closed issues alike |
| `tickOutcomes` | journal records in the window counted by `outcome`, for the quiet-night line ("24 ticks ran: 20 outside window, 4 nothing eligible") |

### 6. Didn't happen and the journal view

- **`journal`**: one `TickLine` per record in the window, newest first:
  - `at`, `trigger`, `mode`, `retryOnly`, `outcome`, `error`, `repos` (a
    count);
  - the counts per decision;
  - `skipReasons` (reason → count);
  - `decisions`, holding the **non-skipped** decisions only;
  - `inert`.

  Skipped decisions are reduced to counts, which keeps a 7-day payload small
  (336 ticks).
- **`didntHappen`**: the journal lines with zero `dispatched` decisions,
  except `outside_window` ones. The UI folds those into the quiet-night line
  and the journal.

### 7. Where the digest is built, and SSE

- `cockpit.BuildDigest(DigestInput) Digest` is a pure function. It is computed
  **per request** from a copy of the snapshot, the closed-thread cache, the
  recent PRs, the journal and the inbox lists. No I/O happens on the request
  path.
- `Collector.Digest(since time.Time) Digest` assembles the input under the
  collector's locks.
- **`digest-updated`.** The payload depends on the per-request `since`, so it
  cannot be diffed the way the board and inbox are. The collector instead
  fingerprints the digest's **inputs** on every `publish`: per-issue markers,
  recent pipeline PRs, closed threads, the journal stamp, and the card and
  waiting keys. It fires `digest-updated` only when that fingerprint changes.
  A gap that appears purely because time passed (an expected tick that never
  came) has no input change. The page's 60 s poll, which #381 adds like the
  Inbox's, is what shows it.

### 8. The endpoint

`GET /api/digest?since=<RFC3339>` is open and read-only, like
`GET /api/inbox`, and is registered in `cmd/bridge/serve.go` next to
`/api/inbox`.

| Status | Meaning |
|---|---|
| 200 | the `Digest` JSON (shape in `docs/cockpit.md`) |
| 400 | `since` missing, not RFC 3339, or in the future |
| 405 | wrong method (`Allow: GET`) |

Empty sections are `[]`, never `null`. `errors` is omitted when empty. Source
errors are keyed like the board's (`prs owner/name`, `closed owner/name#n`,
`journal`). They show once each in #381's collapsible error strip. A
`truncated` list names repos whose recent-PR page was full (100) with its
oldest PR still inside the 7-day horizon, so a busy repo says "may be
incomplete" instead of under-counting silently.

## Non-goals

- The page itself (#381), the Merge card and its PR review/check data (#382),
  the diff (#383) and the quota line (#384). This endpoint carries no
  check-run, review-verdict or quota data.
- Forgejo / Azure DevOps. There is no run marker or pipeline there.
- Changing the board's or inbox's data, intervals or JSON.
- Remote journals. Serve and the timer share one host (parent design).

## Testing

- `internal/forge`: `MergedAt` is mapped from `merged_at`, and is zero when
  `null`.
- `internal/dispatch`: a `ClosingIssues` table covering several keywords, a
  `#410` vs. `#41` boundary, and duplicates removed.
- `internal/cockpit` pure functions, all table-driven:
  - `BuildTickHealth`:
    - slots seen and missing;
    - gaps merged;
    - the range starting at the first record;
    - the grace on the current slot;
    - manual records not filling a slot;
    - inert records, and paused/outside-window records that are not inert;
    - a missing journal;
    - a test parsing the timer unit file.
  - `BuildDigest`:
    - item selection and the would-dispatch exclusion;
    - every group row;
    - `by` timer/manual/session, including the look-back before `since`;
    - every summary count;
    - spend from a closed issue;
    - `inNeedsYou`;
    - `didntHappen` and the `journal` folding;
    - `since` clamping;
    - empty arrays not `null`;
    - unknown `v` dropped.
- `internal/cockpit` collector (hand-rolled fake `Sources`):
  - recent PRs fetched only for wired GitHub repos, on the issues tick;
  - a closed thread read once and re-read only when the PR's `Updated` moves;
  - the journal re-read only when the stamp changes;
  - nil digest sources disabled;
  - `digest-updated` fired on input change only;
  - errors kept with the last good data.
- `internal/api`: the handler's status table and a JSON shape check.
- `cmd/bridge`: `RecentPRs` uses `state=all` (httptest), and the journal
  source gives `present=false` for a missing dir.

## Docs

- `docs/cockpit.md`:
  - a `GET /api/digest` API section;
  - `digest-updated` under SSE;
  - the new rows in "Refresh intervals and rate limits";
  - the limits (`truncated`, the 7-day clamp, session attribution).
- CHANGELOG `Added`.
