# Read-only PR diff in the merge card — design (#383)

Status: design, approved 2026-11-25. Builds on the merge card
([`2026-11-25-cockpit-merge-card-design.md`](2026-11-25-cockpit-merge-card-design.md), #382).
Parent: [`2026-11-11-cockpit-today-design.md`](2026-11-11-cockpit-today-design.md);
wireframe "Merge card, expanded (summary + diff)" in
[`docs/design/cockpit-today/wireframe.md`](../../design/cockpit-today/wireframe.md).

## Intent

Before merging, the diff is "maybe useful in some cases" (operator,
2026-11-11): one tap away, never in the way. Read-only; no comments back to the
agent in v1.

## Decisions (approved by the operator, 2026-11-25)

1. **Source:** GitHub's PR files endpoint (`GET /pulls/{n}/files`), which
   carries a per-file `patch`. Rejected: the `.diff` media type on
   `GET /pulls/{n}` — one blob to split client-side, and GitHub answers `406`
   for large PRs.
2. **Access:** the diff is the first cockpit read that serves source code
   (private repos included), while every other GET is open on the LAN
   (`docs/cockpit.md:148`). So `GET /api/inbox/diff` requires the bearer token:
   `requireTokenConfigured` + `requireBearer`, **no** Origin check (browsers
   send no `Origin` on a same-origin GET; a bearer header is never attached
   cross-site, so the read is not CSRF-able).
3. **Scope guard:** answers only for an issue that is a merge card in the
   collector's current snapshot (`Collector.MergeCard`, #382); otherwise
   `409 not on the inbox`. No arbitrary PR reads.
4. **The open inbox never carries code:** `forge.PRFile` gains `Status` and
   `Patch`; #382's merge evidence copies files **without** `Patch`.
5. **Cache:** in memory, keyed `(repo, pr, headSha)`, LRU of 32 entries. A
   push changes the head SHA and therefore the key. This is the single lazy,
   per-request forge read the parent spec allows.
6. **Response:** `{pr, headSha, files:[{path, status, additions, deletions,
   patch, patchMissing}], truncated}`. `patchMissing` is true when GitHub
   returned no patch (binary or too large). First page of 100 files, like the
   card; `truncated` when `changed_files` > 100.
7. **Errors:** forge failure → `502 forge call failed`, detail to the log only.
   `401`/`503` as the other guarded routes.
8. **UI:** `[diff ▸]` on `MergeCard` → `authedGet` (new; reuses
   `ensureToken`, clears the token on 401 like `authedPost`) → collapsed list
   `path +a −d`; tapping a row opens its patch in a monospace `<pre>`, `+`
   lines and `-` lines tinted, `@@` lines dimmed. `patchMissing` rows say
   "no patch (binary or too large)" with the PR link. Loaded once per card per
   head SHA; a new head SHA in the card discards it.

## Non-goals

Inline comments, syntax highlighting, side-by-side view, other forges,
files past the first 100.

## Approaches

- **Per-file patch from the files endpoint (chosen)** — matches the
  collapsed-per-file view directly; already wrapped by `ListPullRequestFiles`.
- **Unified `.diff` media type** — rejected (decision 1).
- **Diff pre-fetched on the issues tick** — rejected: the parent spec keeps
  the diff lazy, and pre-fetching would put patches in the collector's memory
  for every card nobody opens.

## Left to the builder

LRU implementation (a small map + list is enough, no module), exact tint
colours (theme tokens), wording of the empty/error states.
