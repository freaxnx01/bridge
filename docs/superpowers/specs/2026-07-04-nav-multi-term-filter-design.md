# nav multi-term AND filter (#155)

## Problem

The `bridge nav` picker filter matches a query as a single case-insensitive
substring of each row's label (`filterRepos` in `internal/nav/format.go:122-134`:
`strings.Contains(strings.ToLower(r.label), strings.ToLower(q))`). There is no way
to narrow with two independent fragments. For a deep ADO path the user must type a
single contiguous substring, which the fragments don't form.

Issue #155 asks for **space-separated terms with AND semantics**: `assl archive`
should match `ado/ASSL Customer standard package/archiverestapi` because `assl`
matches `ASSL` (in the owner segment) and `archive` matches `archiverestapi` (the
name), independently and case-insensitively.

## Goal (success criterion)

Typing multiple whitespace-separated terms in the picker filter narrows the list to
rows whose label contains **every** term (case-insensitive, order-independent).
`assl archive` matches `ado/ASSL Customer standard package/archiverestapi`.

## Decisions (settled during brainstorming)

- **Match target is the row label only.** `r.label` is already the full
  `forge/owner/name` path shown in the list — `repoLabel` (`internal/nav/data.go:227`)
  produces `ado/<owner>/<name>` for the default (ADO) forge, i.e. exactly the issue's
  target string. No new searchable fields (description/topics) are introduced —
  matching stays "what you see is what you search".
- **Split with `strings.Fields`.** It splits on any Unicode whitespace, collapses
  runs, and drops empty terms — no manual trimming/empty-filtering needed.
- **AND across terms; substring per term; case-insensitive** (both sides lowercased).
- **Scope is one function.** The change is confined to `filterRepos`; no model, view,
  or config changes.

## Architecture

Change `filterRepos(rows []repoRow, q string) []repoRow` in
`internal/nav/format.go` and add a small helper:

```go
// matchesAllTerms reports whether every term is a substring of labelLower.
// Both labelLower and terms are expected already lower-cased.
func matchesAllTerms(labelLower string, terms []string) bool {
	for _, t := range terms {
		if !strings.Contains(labelLower, t) {
			return false
		}
	}
	return true
}

func filterRepos(rows []repoRow, q string) []repoRow {
	if strings.TrimSpace(q) == "" {
		return rows
	}
	terms := strings.Fields(strings.ToLower(q))
	out := make([]repoRow, 0, len(rows))
	for _, r := range rows {
		if matchesAllTerms(strings.ToLower(r.label), terms) {
			out = append(out, r)
		}
	}
	return out
}
```

The empty-query guard is unchanged (whitespace-only → all rows). A single-term query
yields `terms` of length one and behaves exactly as today.

## Data flow / interactions

- `filterRepos` is called only by `visibleRepos()` (`internal/nav/update.go`). That
  call site is unchanged.
- **Recent section (#183):** its `recentVisible()` gate keys on
  `m.filter.Value() == ""`, which multi-term does not touch. Unaffected.
- **Forge subfilter (#128, enriched but not yet implemented):** it composes a forge
  scope with `filterRepos` (forge-scope AND text match). Whoever implements #128 later
  ANDs the forge scope with the now-multi-term text match — no behavioral conflict; at
  most a trivial textual merge in `visibleRepos`/`format.go`.

## Edge cases

- **Whitespace-only query** (`"   "`): the `TrimSpace(q) == ""` guard returns all
  rows, as today. Note the Recent gate uses exact `== ""`, so a lone space hides the
  Recent section while the list stays unfiltered — a pre-existing degenerate behavior,
  acceptable and out of scope to "fix" here.
- **Collapsed internal spaces** (`"assl   archive"`): `strings.Fields` yields
  `[assl, archive]` — same as single spaces.
- **Single term:** identical to current behavior.
- **A term matching nothing:** row excluded (AND).

## Testing

Table-driven tests on `filterRepos` (and/or `matchesAllTerms`) in
`internal/nav/format_test.go`:

- single-term match and non-match (regression: today's behavior preserved)
- multi-term all-present → match; one term missing → excluded
- case-insensitivity (`ASSL` vs `assl`)
- order independence (`archive assl` matches the same row as `assl archive`)
- the exact issue case: `assl archive` matches `ado/ASSL Customer standard package/archiverestapi`
- whitespace-only query → all rows returned
- collapsed internal whitespace (`"assl   archive"`) behaves like single spaces

Gates: `gofmt -l .` empty, `go vet ./...` clean, `golangci-lint run` clean,
`go test -race ./...` green.

## Acceptance Criteria

- [ ] Space-separated filter terms are treated as independent AND conditions.
- [ ] Matching is case-insensitive.
- [ ] `assl archive` matches `ado/ASSL Customer standard package/archiverestapi`.
- [ ] Term order in the query need not match the order in the target.
- [ ] Table-driven tests cover single-term, multi-term (all-present and one-missing),
      and non-matching cases.
- [ ] `gofmt -l .` empty, `go vet ./...` clean, `golangci-lint run` clean,
      `go test -race ./...` green.

## Non-goals

- Matching against description, topics, or on-disk path (label only).
- Fuzzy matching, regex, or quoted multi-word phrases.
- Any change to the Recent-section or forge-subfilter gating.

## Open questions / follow-ups

- None.
