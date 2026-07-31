# bridge dispatch — configurable repo-priority rung

**Date:** 2026-07-31
**Status:** Design approved, not implemented
**Issue:** [#222](https://github.com/freaxnx01/bridge/issues/222)

## Problem

`bridge dispatch` orders eligible issues with a hardcoded 4-rung ladder in
`internal/dispatch/order.go`: milestone due date → type → size → age. All four
rungs are derived from the issue itself (its milestone, labels, or creation
time) — there is no way to steer ordering by which **repo** an issue lives in.

In practice some repos matter more than others regardless of what's queued in
them: repos that are part of the software-factory tooling chain (e.g.
`agent-workflow`, `ai-instructions`) unblock work in every other repo, so
issues there should dispatch first. Conversely, low-stakes repos (e.g.
`game-*` prototypes) should dispatch last. Today the only way to express this
is `dispatch.json`'s `Limits.Overrides`, which caps *how many* PRs a repo may
have open — it has no effect on dispatch *order*.

## Solution

Add a **repo-priority rung** as the new first step of the ladder, configured
as an ordered list of patterns in `dispatch.json`:

```json
{
  "repo_priority": ["agent-workflow", "ai-instructions", "*", "game-*"]
}
```

- Each entry is matched against a candidate's bare repo name with Go's
  `path.Match` (stdlib glob syntax: `*`, `?`, `[...]` — no new dependency).
  A literal string like `"agent-workflow"` matches only that exact name.
- A repo's rank is the index of the **first** entry it matches, scanning the
  list in order. First match wins; list order **is** priority order (index 0
  = highest priority).
- A repo that matches no entry ranks **after every configured entry** — the
  same effect as an implicit trailing `"*"`. No error, no required catch-all
  entry.
- If `repo_priority` is omitted or empty, the rung is **skipped** entirely:
  `Order()` falls straight through to the existing milestone-due-date
  comparison, byte-for-byte identical to current behavior. Every existing
  `dispatch.json` continues to work unchanged.

The new ladder becomes: **repo priority → milestone due date → type → size →
age.** Repo priority is checked first and dominates: a repo ranked earlier in
`repo_priority` always sorts before a later-ranked repo, regardless of what
the other three rungs would say for the individual candidates.

### Why a new first rung, not a tiebreaker

The existing rungs answer "which issue, within a repo, is more urgent."
Repo priority answers a different question — "which repo matters more" — and
the issue's motivating example (software-factory repos before game
prototypes) is meant to dominate, not merely break ties among otherwise-equal
candidates. Putting it first makes that intent explicit and keeps the
remaining three rungs doing exactly what they do today, unchanged, within
each priority tier.

### Why a flat ordered list, not named tiers or forge metadata

- **Named tiers with explicit repo-name lists** (e.g. `{"software_factory":
  [...], "game": [...]}`) would require every repo to be enumerated
  per-tier and kept in sync manually — the flat list gets the same outcome
  with less schema and a simpler mental model ("first match wins").
- **Forge topics/tags** would need new `internal/forge` support to fetch
  per-repo metadata, and couples ordering to forge-side tagging discipline
  that doesn't exist today. The flat list needs no new forge integration and
  keeps configuration local to `dispatch.json`, consistent with how
  `Limits.Overrides` already works.
- Not every priority tier corresponds to a naming convention (the
  software-factory repos share no common prefix), so the list must support
  literal names, not only globs — a single list where entries are either is
  the simplest structure that covers both cases.

## Implementation

### `internal/dispatch/types.go`

Add `RepoPriority []string` as a new top-level field on `Config`, alongside
`Limits` and `Schedule`.

### `internal/dispatch/config.go`

- `DefaultConfig()` sets `RepoPriority: nil` — an unset/absent field means the
  rung is skipped.
- `LoadConfig` merges `repo_priority` from JSON the same way it merges the
  existing fields (JSON overrides default when present).

### `internal/dispatch/order.go`

- Add `repoPriorityRank(repo string, patterns []string) int`: iterate
  `patterns` in order, return the index of the first `path.Match` hit; if
  none match, return `len(patterns)`.
- `Order` gains a second parameter: `Order(cs []Candidate, repoPriority
  []string) []Candidate`. Update all call sites.
- Insert repo priority as the first comparator in the `SortStableFunc` chain:
  `if c := repoPriorityRank(a.Repo, repoPriority) - repoPriorityRank(b.Repo,
  repoPriority); c != 0 { return c }`, followed by the existing `compareDue`,
  `typeRank`, `sizeRank`, `Created.Compare` chain unchanged. When
  `repoPriority` is empty, `repoPriorityRank` returns `0` for every candidate,
  so this comparator always short-circuits to `0` and the chain falls through
  to `compareDue` exactly as today.

### `docs/dispatch.md`

Document the new `repo_priority` field next to the existing `dispatch.json`
schema table: purpose, glob syntax, first-match-wins semantics, and the
"omitted = skipped, unmatched = lowest priority" defaults.

## Testing

Table-driven tests added to `internal/dispatch/order_test.go`:

- `repoPriorityRank`: literal match, glob match (`game-*`), first-match-wins
  when an earlier pattern in the list also matches, no match falls to
  `len(patterns)`, empty pattern list always returns `0`.
- `Order` with `repoPriority` unset/empty: output identical to calling the
  pre-existing 4-rung chain directly — confirms backward compatibility.
- `Order` with `repoPriority` set: a candidate in a higher-priority repo
  sorts before a candidate in a lower-priority repo even when the
  lower-priority candidate has an earlier milestone due date — confirms rung
  0 dominates rung 1.
- Existing tests for `compareDue`/`typeRank`/`sizeRank`/age tiebreak stay
  unchanged and continue to pass, called through the new `Order` signature
  with `repoPriority: nil`.

## Out of scope

- Validating `repo_priority` patterns at load time (e.g. rejecting malformed
  globs) — `path.Match` returns `ErrBadPattern` only on malformed patterns,
  which is treated as "no match" rather than a hard config error, consistent
  with how `dispatch.json` handles other soft-fail config today.
- Any UI/CLI surface to preview computed repo ranks — `docs/dispatch.md`
  documents the semantics; no new subcommand is added.
- Forge-metadata-based categorization (topics/tags) — noted above as a
  rejected alternative, not deferred work.
