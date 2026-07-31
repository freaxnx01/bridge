# Dispatch Repo-Priority Rung Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a configurable repo-priority rung as the new first step of `bridge dispatch`'s ordering ladder, so operators can steer which repos dispatch first via `dispatch.json`, while every existing config file keeps behaving exactly as it does today.

**Architecture:** A new `RepoPriority []string` field on `dispatch.Config` holds an ordered list of glob/literal patterns. A new pure function `repoPriorityRank` maps a repo name to its index in that list (first match wins; `path.Match` glob syntax; no match or empty list ranks last/no-op respectively). `Order` gains a `repoPriority []string` parameter and inserts the new rank as the first comparator in its existing `slices.SortStableFunc` chain, ahead of milestone due date.

**Tech Stack:** Go stdlib only — `path` package for glob matching (`path.Match`), `slices` for the existing sort. No new dependencies.

## Global Constraints

- Backward compatibility is mandatory: an empty or nil `RepoPriority` must make `Order`'s output byte-for-byte identical to the current 4-rung behavior — every existing `dispatch.json` must keep working unchanged.
- Go stdlib only (`path.Match`) — no new third-party dependency.
- Follow the repo's Go stack overlay: table-driven tests, hand-rolled fakes only (no testify/mockery), `gofmt`/`go vet`/`golangci-lint`/`go test -race` all clean after every task.
- TDD strictly: write the failing test, run and watch it fail, implement minimally, run and watch it pass, commit.

---

### Task 1: Add `repoPriorityRank` and thread `RepoPriority` through `Config`

**Files:**
- Modify: `internal/dispatch/types.go` — add `RepoPriority []string` field to `Config`
- Modify: `internal/dispatch/order.go` — add `repoPriorityRank` function, change `Order` signature
- Modify: `internal/dispatch/config.go` — no default value needed (nil is the correct default), but confirm `DefaultConfig()` comment covers it
- Modify: `cmd/bridge/dispatch.go:155` — update the one call site to pass `cfg.RepoPriority`
- Modify: `internal/dispatch/order_test.go` — update existing `Order(cs)` calls to `Order(cs, nil)`
- Test: `internal/dispatch/order_test.go`

**Interfaces:**
- Consumes: existing `Candidate{Issue forge.Issue, Owner, Repo string, MilestoneDue time.Time}` (unchanged), existing `Config{Limits, Schedule}` struct.
- Produces:
  - `func repoPriorityRank(repo string, patterns []string) int` — index of first `path.Match` hit in `patterns` (scanned in order), `len(patterns)` if no pattern matches, `0` if `patterns` is empty/nil.
  - `func Order(cs []Candidate, repoPriority []string) []Candidate` — new second parameter; all existing behavior for the 4 old rungs is unchanged, repo priority is compared first.
  - `Config.RepoPriority []string` (JSON key `"repo_priority"`, `omitempty`).

- [ ] **Step 1: Write the failing tests for `repoPriorityRank`**

Add to `internal/dispatch/order_test.go`:

```go
func TestRepoPriorityRank(t *testing.T) {
	patterns := []string{"agent-workflow", "ai-instructions", "*", "game-*"}
	tests := []struct {
		name, repo string
		want       int
	}{
		{"first literal match", "agent-workflow", 0},
		{"second literal match", "ai-instructions", 1},
		{"catch-all wildcard match", "bridge", 2},
		{"game glob would match but catch-all wins first", "game-foo", 2},
		{"empty patterns always ranks 0", "anything", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ps := patterns
			if tt.name == "empty patterns always ranks 0" {
				ps = nil
			}
			got := repoPriorityRank(tt.repo, ps)
			if got != tt.want {
				t.Errorf("repoPriorityRank(%q, %v) = %d, want %d", tt.repo, ps, got, tt.want)
			}
		})
	}
}

func TestRepoPriorityRank_NoMatchFallsToEnd(t *testing.T) {
	patterns := []string{"agent-workflow", "game-*"}
	got := repoPriorityRank("bridge", patterns)
	want := len(patterns)
	if got != want {
		t.Errorf("repoPriorityRank(%q, %v) = %d, want %d (len(patterns))", "bridge", patterns, got, want)
	}
}

func TestRepoPriorityRank_GlobMatch(t *testing.T) {
	patterns := []string{"agent-workflow", "game-*", "*"}
	got := repoPriorityRank("game-tetris", patterns)
	if got != 1 {
		t.Errorf("repoPriorityRank(%q, %v) = %d, want 1", "game-tetris", patterns, got)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/dispatch/ -run TestRepoPriorityRank -v`
Expected: FAIL — `undefined: repoPriorityRank`

- [ ] **Step 3: Implement `repoPriorityRank`**

Add to `internal/dispatch/order.go`, after `sizeRank`:

```go
// repoPriorityRank maps repo to the index of the first pattern in patterns
// that matches it (path.Match glob syntax; literal strings match exactly).
// A repo matching nothing ranks after every configured entry. An empty
// pattern list ranks every repo 0, making this rung a no-op.
func repoPriorityRank(repo string, patterns []string) int {
	for i, p := range patterns {
		if ok, _ := path.Match(p, repo); ok {
			return i
		}
	}
	return len(patterns)
}
```

Add `"path"` to the import block in `internal/dispatch/order.go`.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/dispatch/ -run TestRepoPriorityRank -v`
Expected: PASS

- [ ] **Step 5: Write the failing test for `Order`'s new signature and rung ordering**

Add to `internal/dispatch/order_test.go`:

```go
func TestOrderRepoPriorityDominatesMilestone(t *testing.T) {
	cs := []Candidate{
		{Repo: "low-priority", Issue: forge.Issue{Number: 1, Created: day("2026-01-01")},
			MilestoneDue: day("2026-02-01")}, // earliest due date, but low priority repo
		{Repo: "high-priority", Issue: forge.Issue{Number: 2, Created: day("2026-01-01")}},
	}
	got := Order(cs, []string{"high-priority", "*"})
	if got[0].Issue.Number != 2 {
		t.Fatalf("position 0: got #%d, want #2 (repo priority must dominate milestone due date, full order %v)",
			got[0].Issue.Number, numbers(got))
	}
}

func TestOrderRepoPriorityEmptyIsNoOp(t *testing.T) {
	cs := []Candidate{
		{Repo: "a", Issue: forge.Issue{Number: 1, Labels: []string{"chore"}, Created: day("2026-01-01")}},
		{Repo: "b", Issue: forge.Issue{Number: 2, Labels: []string{"bug"}, Created: day("2026-05-01")}},
	}
	got := Order(cs, nil)
	if got[0].Issue.Number != 2 {
		t.Fatalf("empty repoPriority must behave like the pre-existing 4-rung ladder: got %v", numbers(got))
	}
}
```

- [ ] **Step 6: Run tests to verify they fail**

Run: `go test ./internal/dispatch/ -run TestOrderRepoPriority -v`
Expected: FAIL — `not enough arguments in call to Order`

- [ ] **Step 7: Update `Order`'s signature and insert the new rung**

Replace `Order` in `internal/dispatch/order.go`:

```go
// Order sorts candidates by the deterministic ladder: repo priority, then
// milestone due date, then type, then size, then age. It returns a new
// slice.
func Order(cs []Candidate, repoPriority []string) []Candidate {
	out := slices.Clone(cs)
	slices.SortStableFunc(out, func(a, b Candidate) int {
		if c := repoPriorityRank(a.Repo, repoPriority) - repoPriorityRank(b.Repo, repoPriority); c != 0 {
			return c
		}
		if c := compareDue(a.MilestoneDue, b.MilestoneDue); c != 0 {
			return c
		}
		if c := typeRank(a.Issue.Labels) - typeRank(b.Issue.Labels); c != 0 {
			return c
		}
		if c := sizeRank(a.Issue.Labels) - sizeRank(b.Issue.Labels); c != 0 {
			return c
		}
		return a.Issue.Created.Compare(b.Issue.Created)
	})
	return out
}
```

Update every existing call in `internal/dispatch/order_test.go` (`TestOrderLadder`, `TestOrderSizeThenAge`, `TestOrderDoesNotMutateInput`) from `Order(cs)` to `Order(cs, nil)`.

Update the call site in `cmd/bridge/dispatch.go:155` from:

```go
dispatch.Order(collectCandidates(repos)),
```

to:

```go
dispatch.Order(collectCandidates(repos), cfg.RepoPriority),
```

- [ ] **Step 8: Add the `RepoPriority` field to `Config`**

In `internal/dispatch/types.go`, modify the `Config` struct:

```go
type Config struct {
	Limits       Limits   `json:"limits"`
	Schedule     Schedule `json:"schedule"`
	RepoPriority []string `json:"repo_priority,omitempty"`
}
```

`DefaultConfig()` in `internal/dispatch/config.go` needs no change — the zero value (`nil`) is already correct default behavior (rung skipped). `LoadConfig`'s existing `json.Unmarshal(b, &c)` over the pre-populated default already merges `repo_priority` correctly (Go's JSON unmarshal only overwrites fields present in the JSON), no code change needed there.

- [ ] **Step 9: Run the full test suite to verify everything passes**

Run: `go test ./internal/dispatch/... ./cmd/bridge/... -race -v`
Expected: PASS, all tests including the pre-existing `TestOrderLadder`, `TestOrderSizeThenAge`, `TestOrderDoesNotMutateInput` (now called with `Order(cs, nil)`), and the new repo-priority tests.

- [ ] **Step 10: Run static checks**

```bash
gofmt -l internal/dispatch/ cmd/bridge/
go vet ./internal/dispatch/... ./cmd/bridge/...
golangci-lint run ./internal/dispatch/... ./cmd/bridge/...
```

Expected: no output from `gofmt -l`, clean `go vet`, clean `golangci-lint`.

- [ ] **Step 11: Commit**

```bash
git add internal/dispatch/types.go internal/dispatch/order.go internal/dispatch/order_test.go cmd/bridge/dispatch.go
git commit -m "feat(dispatch): add configurable repo-priority rung

Order() now takes a repo_priority pattern list and ranks candidates by
first-match-wins index as the new first rung, ahead of milestone due
date. Empty/nil list is a no-op, preserving existing dispatch.json
behavior exactly.

Closes #222"
```

---

### Task 2: Document `repo_priority` in `docs/dispatch.md`

**Files:**
- Modify: `docs/dispatch.md`

**Interfaces:**
- Consumes: `Config.RepoPriority []string` (Task 1), the `repoPriorityRank` semantics (first-match-wins, `path.Match` glob syntax, unmatched → last, empty → no-op).
- Produces: nothing consumed by later tasks — this is the terminal documentation task.

- [ ] **Step 1: Update the "Priority" section to describe the new first rung**

In `docs/dispatch.md`, replace the "## Priority" section:

```markdown
## Priority

Issues are sorted by a five-rung ladder before applying caps:

1. **Repo priority** — If `repo_priority` is configured, a repo's rank is the index of the first pattern it matches (`path.Match` glob syntax — literal names match exactly; entries containing `*`, `?`, `[...]` match as patterns), scanned in list order. Repos matching no pattern sort after every configured entry. If `repo_priority` is unset or empty, this rung is skipped and ordering falls straight through to milestone due date, identical to the pre-existing behavior.
2. **Milestone due date** — Issues in milestones with earlier due dates sort first. Issues with no active milestone sort last.
3. **Type** — Bug/fix issues (labels `bug` or `fix`, case-insensitive) sort first (rank 0), then feature issues (label `feat`, rank 1), then everything else (rank 2).
4. **Size** — Issues labeled `size:s` sort first (rank 0), then `size:m` (rank 1), then `size:l` (rank 2). Unlabeled issues default to rank 1 (medium).
5. **Age** — Older issues (earlier creation date) sort first within the same size bucket.

The sort is stable: equal-rank issues retain their input order.
```

- [ ] **Step 2: Add `repo_priority` to the config example and schema table**

In `docs/dispatch.md`, update the "## Config" example JSON block to add the new top-level key:

```json
{
  "limits": {
    "global_open_prs": 3,
    "per_repo": 1,
    "max_dispatches_per_night": 5,
    "overrides": {
      "quotes": 2,
      "otherepo": 1
    }
  },
  "schedule": {
    "dispatch_at": "22:00",
    "retry_until": "06:00"
  },
  "repo_priority": ["agent-workflow", "ai-instructions", "*", "game-*"]
}
```

And add a row to the schema table:

```markdown
| `repo_priority` | array of strings | [] (rung skipped) | Ordered list of repo-name patterns (`path.Match` glob syntax); a repo's dispatch priority is the index of the first pattern it matches, scanned in order. Unmatched repos rank after every entry. Empty/absent disables this rung entirely. |
```

- [ ] **Step 3: Verify the doc renders correctly and matches the implemented behavior**

Read back `docs/dispatch.md`'s "Priority" and "Config" sections and confirm every claim matches `internal/dispatch/order.go` and `internal/dispatch/types.go` from Task 1 (rung count, JSON key name, default behavior, glob semantics).

- [ ] **Step 4: Commit**

```bash
git add docs/dispatch.md
git commit -m "docs(dispatch): document repo_priority config field

Closes #222"
```
