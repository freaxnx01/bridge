# Cockpit Unsupported Forges Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Repos on a forge the cockpit has no client for (Azure DevOps today) stop producing one red error line each on the Pipeline board, and show instead as one grouped, muted `skipped` note. A missing token on GitHub or Forgejo stays an error.

**Architecture:** `cmd/bridge/pipeline.go`'s `Issues` source returns a new sentinel, `cockpit.ErrForgeUnsupported`, for forges outside `github`/`forgejo`. The collector routes that sentinel into an `unsupported` map instead of `errs`. `Board` gains `Skipped []SkippedForge`, and `Pipeline.svelte` renders one muted line per forge.

**Tech Stack:** Go (module as pinned in `go.mod`), stdlib `testing` with table-driven `t.Run` subtests and hand-rolled fakes (no testify); Svelte 5 + Vitest + `@testing-library/svelte`; `just test`, `golangci-lint`, `go test -race ./...`, `npm test` in `web/`.

**Spec:** `docs/superpowers/specs/2026-11-11-cockpit-unsupported-forges-design.md`

## Global Constraints

- Supported forges are exactly `github` and `forgejo` (same as `clientForMCP`, `cmd/bridge/mcp.go:367`).
- `errNoForgeClient` for a supported forge with no token is unchanged: it still lands in `Board.Errors`. Every existing test in `cmd/bridge/pipeline_sources_test.go` passes unmodified.
- `Board.Skipped` is `[]`, never `null`; sorted by `forge`; one entry per forge with the repo count.
- `Sources.Issue` (single-issue read) and the Inbox are not changed.
- No new dependencies. No `//nolint`.
- Required after every task: `gofmt -l .` empty, `go vet ./...`, `golangci-lint run`, `go test -race ./...` green; after Task 3 also `cd web && npm test`.
- Conventional Commits, scope `cockpit` (Task 3: `web`), reference `#366`.

## Review Focus

1. A GitHub repo whose owner has no token must still appear in `Errors` as `issues o/x` (not in `Skipped`). Test in Task 1.
2. A repo that was in `Errors` from an earlier failure and is now reported unsupported must have its error entry deleted. Test in Task 2.
3. `skipped` is serialized as `[]` on a board with no unsupported repos. Test in Task 2.

---

### Task 1: Sentinel and source

**Files:**
- Modify: `internal/cockpit/collector.go` (add the sentinel next to `ErrUnknownRepo`, `:40`)
- Modify: `cmd/bridge/pipeline.go` (`cockpitSources` → `Issues`, `:108-114`)
- Test: `cmd/bridge/pipeline_sources_test.go`

**Interfaces:**
- Produces: `var ErrForgeUnsupported = errors.New("forge not supported by the board")` in package `cockpit`; `func boardForge(name string) bool` in `cmd/bridge`.

- [ ] **Step 1: Write the failing test**

```go
func TestCockpitSources_Issues_UnsupportedForge_ReportsSkip(t *testing.T) {
	src := cockpitSources(func(string, string) forge.Client { return nil })
	cases := []struct {
		name  string
		forge string
		want  error
	}{
		{"ado is unsupported", "ado", cockpit.ErrForgeUnsupported},
		{"unknown forge is unsupported", "gitlab", cockpit.ErrForgeUnsupported},
		{"github without token is an error", "github", errNoForgeClient},
		{"forgejo without token is an error", "forgejo", errNoForgeClient},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := core.Repo{Forge: c.forge, Owner: "bossDMS", Name: "x"}
			_, err := src.Issues(context.Background(), r)
			if !errors.Is(err, c.want) {
				t.Fatalf("err = %v, want %v", err, c.want)
			}
		})
	}
}
```

(Add `"github.com/freaxnx01/bridge/internal/cockpit"` to the test's imports.)

- [ ] **Step 2: Run to verify failure**
  Run: `go test ./cmd/bridge -run UnsupportedForge -v`
  Expected: FAIL (`cockpit.ErrForgeUnsupported` undefined).

- [ ] **Step 3: Implement**

`internal/cockpit/collector.go`:

```go
// ErrForgeUnsupported marks a repo on a forge the board has no client for.
// The collector lists such repos under Board.Skipped instead of Errors.
var ErrForgeUnsupported = errors.New("forge not supported by the board")
```

`cmd/bridge/pipeline.go`:

```go
// boardForge reports whether the cockpit has a client for forgeName. It
// mirrors clientForMCP's switch.
func boardForge(name string) bool { return name == "github" || name == "forgejo" }
```

and in `cockpitSources`, `Issues`:

```go
Issues: func(ctx context.Context, r core.Repo) ([]forge.Issue, error) {
	if !boardForge(r.Forge) {
		return nil, fmt.Errorf("%w: %s", cockpit.ErrForgeUnsupported, r.Forge)
	}
	cl, err := repoClient(resolve, r)
	if err != nil {
		return nil, err
	}
	return cl.ListOpenIssues(ctx, r.Owner, r.Name)
},
```

- [ ] **Step 4: Run to verify pass:** `go test -race ./cmd/bridge ./internal/cockpit`, `golangci-lint run`. Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/cockpit/collector.go cmd/bridge/pipeline.go cmd/bridge/pipeline_sources_test.go
git commit -m "fix(cockpit): report unsupported forges as a skip, not a missing client (#366)"
```

### Task 2: Collector routes unsupported repos to `Board.Skipped`

**Files:**
- Modify: `internal/cockpit/board.go` (`Board`, `:62-72`; empty-board constructor, `:109-117`)
- Modify: `internal/cockpit/collector.go` (`Collector` struct, `NewCollector`, `applyRepoLocked`, `rebuildLocked`)
- Test: `internal/cockpit/collector_test.go`

**Interfaces:**
- Consumes: `ErrForgeUnsupported` (Task 1).
- Produces: `type SkippedForge struct { Forge string \`json:"forge"\`; Repos int \`json:"repos"\` }`; `Board.Skipped []SkippedForge \`json:"skipped"\``.

- [ ] **Step 1: Write the failing tests** (reuse `newWorld`, `ghRepo`, `fixedNow`, `DefaultIntervals`)

```go
func adoRepo(name string) core.Repo {
	return core.Repo{Forge: "ado", Owner: "bossDMS", Name: name, Path: "/r/" + name}
}

func TestCollector_UnsupportedForge_SkippedNotError(t *testing.T) {
	w := newWorld(ghRepo("a"), adoRepo("x"), adoRepo("y"))
	w.issuesErr["bossDMS/x"] = fmt.Errorf("%w: ado", ErrForgeUnsupported)
	w.issuesErr["bossDMS/y"] = fmt.Errorf("%w: ado", ErrForgeUnsupported)
	w.issuesErr["o/a"] = errors.New("boom")
	c := NewCollector(w.sources(), DefaultIntervals(), fixedNow, nil)

	c.CollectIssues(context.Background())

	b := c.Board()
	if len(b.Errors) != 1 || b.Errors[0].Source != "issues o/a" {
		t.Fatalf("Errors = %+v, want only 'issues o/a'", b.Errors)
	}
	want := []SkippedForge{{Forge: "ado", Repos: 2}}
	if !slices.Equal(b.Skipped, want) {
		t.Fatalf("Skipped = %+v, want %+v", b.Skipped, want)
	}
}

func TestCollector_RepoTurnsUnsupported_ClearsOldError(t *testing.T) {
	w := newWorld(adoRepo("x"))
	w.issuesErr["bossDMS/x"] = errors.New("boom")
	c := NewCollector(w.sources(), DefaultIntervals(), fixedNow, nil)
	ctx := context.Background()
	c.CollectIssues(ctx)

	w.issuesErr["bossDMS/x"] = fmt.Errorf("%w: ado", ErrForgeUnsupported)
	c.CollectIssues(ctx)

	if got := c.Board().Errors; len(got) != 0 {
		t.Fatalf("Errors = %+v, want none", got)
	}
}

func TestBoard_NoUnsupported_SkippedIsEmptyArray(t *testing.T) {
	c := NewCollector(newWorld(ghRepo("a")).sources(), DefaultIntervals(), fixedNow, nil)
	c.CollectIssues(context.Background())
	raw, err := json.Marshal(c.Board())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(raw, []byte(`"skipped":[]`)) {
		t.Fatalf("board JSON = %s, want \"skipped\":[]", raw)
	}
}
```

(`key(r)` in the fake is `owner/name`; adjust the map keys if the helper differs. Add the `bytes`, `encoding/json`, `fmt`, `slices` imports as needed.)

- [ ] **Step 2: Run to verify failure**
  Run: `go test ./internal/cockpit -run 'Unsupported|Skipped' -v`
  Expected: FAIL (`SkippedForge` / `Board.Skipped` undefined).

- [ ] **Step 3: Implement**
  - `board.go`: add `SkippedForge` and `Skipped []SkippedForge \`json:"skipped"\`` after `Errors`. The empty-board constructor sets `Skipped: []SkippedForge{}`.
  - `collector.go`: add `unsupported map[string]string // repo key → forge` to `Collector`, initialised in `NewCollector`.
  - At the top of `applyRepoLocked`, before `if res.err != nil`, add:

```go
if errors.Is(res.err, ErrForgeUnsupported) {
	c.markUnsupportedLocked(r, prev)
	return
}
delete(c.unsupported, key)
```

    with

```go
// markUnsupportedLocked records r as skipped (no client for its forge) and
// drops any earlier source error for it.
func (c *Collector) markUnsupportedLocked(r core.Repo, prev *RepoData) {
	key := repoKey(r)
	delete(c.errs, "issues "+key)
	c.unsupported[key] = r.Forge
	if prev == nil {
		c.repos = append(c.repos, baseRepoData(r, false))
	}
}
```

  - In `rebuildLocked`, after `b.Errors` is set: `b.Skipped = skippedForges(c.unsupported)` with

```go
// skippedForges groups unsupported repos per forge, sorted by forge.
func skippedForges(unsupported map[string]string) []SkippedForge {
	counts := map[string]int{}
	for _, f := range unsupported {
		counts[f]++
	}
	out := []SkippedForge{}
	for _, f := range slices.Sorted(maps.Keys(counts)) {
		out = append(out, SkippedForge{Forge: f, Repos: counts[f]})
	}
	return out
}
```

- [ ] **Step 4: Run to verify pass:** `go test -race ./...`, `golangci-lint run`, `gofmt -l .`. Expected: PASS, empty.

- [ ] **Step 5: Commit**

```bash
git add internal/cockpit/board.go internal/cockpit/collector.go internal/cockpit/collector_test.go
git commit -m "fix(cockpit): list unsupported-forge repos under skipped, not errors (#366)"
```

### Task 3: One muted line on the board; docs

**Files:**
- Modify: `web/src/routes/Pipeline.svelte` (below the columns, before the `errors` strip, `:193`)
- Test: `web/src/routes/Pipeline.test.js`
- Modify: `docs/cockpit.md` (API JSON block + "Known limits"), `CHANGELOG.md` (`[Unreleased]` → `### Fixed`)

- [ ] **Step 1: Write the failing tests** (in the existing `describe`, using the `board()` helper)

```js
it('shows one muted line per skipped forge, outside the error strip', async () => {
  pipeline.set(board({ skipped: [{ forge: 'ado', repos: 10 }] }))
  render(Pipeline)
  const line = screen.getByText('10 Azure DevOps repos not on the board (forge not supported).')
  expect(line.closest('.errors')).toBeNull()
  expect(screen.queryByRole('button', { name: 'dismiss' })).toBeNull()
})

it('shows nothing for an empty or missing skipped list', async () => {
  pipeline.set(board({ skipped: [] }))
  render(Pipeline)
  expect(screen.queryByText(/not on the board/)).toBeNull()
})
```

- [ ] **Step 2: Run to verify failure:** `cd web && npx vitest run src/routes/Pipeline.test.js`. Expected: FAIL (text not found).

- [ ] **Step 3: Implement** in `Pipeline.svelte`:

```svelte
const forgeLabels = { ado: 'Azure DevOps' }
const skipped = $derived(board?.skipped ?? [])
```

```svelte
{#if skipped.length > 0}
  <div class="skipped" role="note">
    {#each skipped as s (s.forge)}
      <p>{s.repos} {forgeLabels[s.forge] ?? s.forge} repos not on the board (forge not supported).</p>
    {/each}
  </div>
{/if}
```

  Style `.skipped` muted (the existing secondary text colour token), small, no background.

- [ ] **Step 4: Docs**
  - `docs/cockpit.md`, `GET /api/pipeline` JSON: add `"skipped": [{"forge": "ado", "repos": 10}]` after `errors`, and a sentence: "`skipped` lists forges the board has no client for (today: Azure DevOps), one entry per forge with its repo count; those repos are not errors. A GitHub or Forgejo repo without a token still shows under `errors`." Add the same to "Known limits".
  - `CHANGELOG.md` `[Unreleased]` → `### Fixed`: "Cockpit: Azure DevOps clones no longer fill the error strip; the board shows one muted line per unsupported forge (`skipped` in `/api/pipeline`). (#366)"

- [ ] **Step 5: Verify:** `cd web && npm test`; `just test` (or `go test -race ./...`); `golangci-lint run`; `gofmt -l .`. Expected: all green, empty.

- [ ] **Step 6: Commit**

```bash
git add web/src/routes/Pipeline.svelte web/src/routes/Pipeline.test.js docs/cockpit.md CHANGELOG.md
git commit -m "fix(web): show unsupported forges as one muted note on the board (#366)"
```
