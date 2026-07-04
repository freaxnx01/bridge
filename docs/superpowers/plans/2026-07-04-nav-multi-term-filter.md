# nav multi-term AND filter (#155) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the `bridge nav` picker filter split on whitespace and match a row
only when **every** term is a case-insensitive substring of its label — so
`assl archive` matches `ado/ASSL Customer standard package/archiverestapi`.

**Architecture:** A single-function change to `filterRepos` in
`internal/nav/format.go`, plus a small testable helper `matchesAllTerms`. The
empty/whitespace guard is unchanged; a single-term query behaves exactly as today.
No model, view, config, or call-site changes — `filterRepos` is still called only
by `visibleRepos()`.

**Tech Stack:** Go (stdlib `strings`, stdlib `testing` — table-driven, hand-rolled
fakes; NO testify/mockery). Spec:
`docs/superpowers/specs/2026-07-04-nav-multi-term-filter-design.md`.

## Global Constraints

- Match target is the row label ONLY (`r.label`) — no description/topics/path.
- Terms are AND'd; each term is a case-insensitive substring; order-independent.
- Empty/whitespace-only query returns all rows (guard unchanged).
- No new dependencies. Change confined to `internal/nav/format.go` (+ its test).
- `gofmt -l .` empty; `go vet ./...` clean; `golangci-lint run` clean;
  `go test -race ./...` green.

---

## File Structure

- **Modify** `internal/nav/format.go` — rewrite `filterRepos`; add `matchesAllTerms`.
- **Modify** `internal/nav/format_test.go` — table-driven tests for the new behavior.

No other files change. `filterRepos`'s only caller (`visibleRepos()` in
`internal/nav/update.go`) keeps the same signature `filterRepos(rows, q)`.

---

## Task 1: multi-term AND matching in `filterRepos`

**Files:**
- Modify: `internal/nav/format.go` (`filterRepos`, ~line 122-134; add `matchesAllTerms`)
- Test: `internal/nav/format_test.go`

**Interfaces:**
- Produces: `filterRepos(rows []repoRow, q string) []repoRow` (unchanged signature);
  `matchesAllTerms(labelLower string, terms []string) bool` (new, package-private).

- [ ] **Step 1: Write the failing tests**

Append to `internal/nav/format_test.go` (white-box, package `nav`; the file already
constructs `repoRow` values, so no new imports are needed — verify `repoRow` and its
`label` field are in scope, which they are for existing tests in this file):

```go
func TestFilterRepos_MultiTerm(t *testing.T) {
	rows := []repoRow{
		{label: "ado/ASSL Customer standard package/archiverestapi"},
		{label: "github/public/bridge"},
		{label: "github/private/archive-tool"},
		{label: "forgejo/notes"},
	}
	tests := []struct {
		name  string
		query string
		want  []string // wanted labels, in order
	}{
		{"empty returns all", "", []string{
			"ado/ASSL Customer standard package/archiverestapi",
			"github/public/bridge",
			"github/private/archive-tool",
			"forgejo/notes",
		}},
		{"whitespace only returns all", "   ", []string{
			"ado/ASSL Customer standard package/archiverestapi",
			"github/public/bridge",
			"github/private/archive-tool",
			"forgejo/notes",
		}},
		{"single term substring", "bridge", []string{"github/public/bridge"}},
		{"single term no match", "zzz", nil},
		// The headline issue case: two independent fragments, case-insensitive.
		{"multi-term AND across path", "assl archive", []string{
			"ado/ASSL Customer standard package/archiverestapi",
		}},
		{"order independent", "archive assl", []string{
			"ado/ASSL Customer standard package/archiverestapi",
		}},
		{"one term missing excludes row", "archive bridge", nil},
		{"collapsed internal whitespace", "assl   archive", []string{
			"ado/ASSL Customer standard package/archiverestapi",
		}},
		{"term matches two rows narrowed by second", "archive tool", []string{
			"github/private/archive-tool",
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := filterRepos(rows, tt.query)
			var gotLabels []string
			for _, r := range got {
				gotLabels = append(gotLabels, r.label)
			}
			if len(gotLabels) != len(tt.want) {
				t.Fatalf("filterRepos(%q) = %v, want %v", tt.query, gotLabels, tt.want)
			}
			for i := range tt.want {
				if gotLabels[i] != tt.want[i] {
					t.Errorf("filterRepos(%q)[%d] = %q, want %q", tt.query, i, gotLabels[i], tt.want[i])
				}
			}
		})
	}
}

func TestMatchesAllTerms(t *testing.T) {
	label := "ado/assl customer standard package/archiverestapi" // already lower-cased
	tests := []struct {
		name  string
		terms []string
		want  bool
	}{
		{"all present", []string{"assl", "archive"}, true},
		{"one missing", []string{"assl", "zzz"}, false},
		{"single present", []string{"archiverestapi"}, true},
		{"empty terms vacuously true", nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := matchesAllTerms(label, tt.terms); got != tt.want {
				t.Errorf("matchesAllTerms(%v) = %v, want %v", tt.terms, got, tt.want)
			}
		})
	}
}
```

(If `internal/nav/format_test.go` already has a `TestFilterRepos*` test asserting
single-term behavior, leave it — the new behavior is a strict superset, so it must
still pass. If it does NOT still pass, that is a real signal; investigate rather than
editing the old test.)

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/nav/ -run 'TestFilterRepos_MultiTerm|TestMatchesAllTerms' -v`
Expected: FAIL — `matchesAllTerms` undefined, and `filterRepos("assl archive", …)`
currently returns nothing (single-substring match of the literal `"assl archive"`).

- [ ] **Step 3: Rewrite `filterRepos` + add `matchesAllTerms`**

In `internal/nav/format.go`, replace the current `filterRepos`:

```go
func filterRepos(rows []repoRow, q string) []repoRow {
	if strings.TrimSpace(q) == "" {
		return rows
	}
	needle := strings.ToLower(q)
	out := make([]repoRow, 0, len(rows))
	for _, r := range rows {
		if strings.Contains(strings.ToLower(r.label), needle) {
			out = append(out, r)
		}
	}
	return out
}
```

with:

```go
// matchesAllTerms reports whether every term is a substring of labelLower. Both
// labelLower and terms must already be lower-cased. No terms is vacuously true.
func matchesAllTerms(labelLower string, terms []string) bool {
	for _, t := range terms {
		if !strings.Contains(labelLower, t) {
			return false
		}
	}
	return true
}

// filterRepos keeps rows whose label contains every whitespace-separated term in
// q (case-insensitive, order-independent AND). An empty/whitespace-only query
// returns all rows; a single term behaves like a plain substring filter.
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

(`strings` is already imported in `format.go`. No signature change, so
`visibleRepos()` needs no edit.)

- [ ] **Step 4: Run tests**

Run: `go test ./internal/nav/ -run 'TestFilterRepos_MultiTerm|TestMatchesAllTerms' -v`
Expected: PASS. Then the full package: `go test ./internal/nav/`
Expected: green (existing filter/picker tests unaffected — single-term behavior
preserved).

- [ ] **Step 5: Commit**

```bash
gofmt -l internal/nav/ ; go vet ./internal/nav/
git add internal/nav/format.go internal/nav/format_test.go
git commit -m "feat(nav): multi-term AND filter on the picker

Split the filter query on whitespace and keep a row only when every term is a
case-insensitive substring of its label (order-independent). Single-term and
empty queries behave as before. Closes #155.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 2: Full verification

**Files:** none.

- [ ] **Step 1: Gates**

Run:
```bash
gofmt -l . | grep -v '.worktrees/'   # empty
go vet ./...                          # clean
go test -race ./...                   # all ok
```

- [ ] **Step 2: Golden stability**

Run: `go test ./internal/nav/ -update && git status --short internal/nav/testdata/`
Expected: no diff (this change touches no rendered output, so goldens are stable).

- [ ] **Step 3: Lint (best-effort)**

Run: `golangci-lint run ./internal/nav/...` (if installed). Else note it; `go vet`
is the gate.

- [ ] **Step 4: Manual smoke (best-effort — needs a real repo set incl. an ADO path)**

Run:
```bash
just build
bridge nav   # type "assl archive" → the ADO archiverestapi repo appears; typing
             # a term that no single repo satisfies together shows nothing
```

- [ ] **Step 5: Report**

Report Steps 1-2 output + the Step 4 smoke result. No success claims without output.

---

## Notes for the implementer

- **One function, one helper.** Do not touch `visibleRepos`, the Recent-section gate
  (`recentVisible`), or any forge-subfilter code — `filterRepos`'s signature is
  unchanged, so everything composing around it keeps working.
- **`strings.Fields`, not `strings.Split`.** `Fields` collapses whitespace runs and
  drops empty terms; `Split(q, " ")` would leave empty terms that match everything.
- **Lower-case once per side.** Lower-case the query into `terms` once, and each label
  once per row — don't re-lower inside `matchesAllTerms`.
- If you hit a blocker, find the fix and note it inline here.
