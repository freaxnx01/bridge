# Base-checkout Row in `bridge nav` — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a pinned first row to each `bridge nav` repo dashboard that launches/attaches a session on the main checkout (repo root) with no worktree.

**Architecture:** A new `worktree.Primary` surfaces the primary tree's branch. `buildDashRows` prepends a `dashRow{isBase:true, worktree:"", path:repo.Path}` whose bare `<repo>` slot (`SlotID(repo,"")`) interoperates with the shell `bridge open <repo>` path. The view renders the base row as `★ <branch>`. No new keybindings — the base row is `dashRows[0]` and reuses the existing `enter`/navigation logic.

**Tech Stack:** Go (stdlib `testing`, table-driven, hand-rolled `Runner` fakes), Bubble Tea (`internal/nav`).

**Spec:** `docs/superpowers/specs/2026-07-01-nav-base-checkout-row-design.md`

## Global Constraints

- Stdlib `testing` only — no testify/mockery/gomock. Hand-rolled fakes.
- `gofmt -l .` empty, `go vet ./...` clean, `golangci-lint run` clean, `go test -race ./...` green after every task.
- Return errors, don't panic; no `os.Exit`/stderr below the command layer.
- `SlotID(repo,"")` = bare `<repo>` is the base slot form (`internal/core/slot.go:24-33`) — do not change it.
- Conventional Commits; commit after each task.

---

### Task 1: `worktree.Primary` — surface the main checkout's branch

**Files:**
- Modify: `internal/worktree/worktree.go` (add `Primary` after `List`, ~line 49)
- Test: `internal/worktree/worktree_test.go` (append tests; reuse existing `fakeRunner`)

**Interfaces:**
- Consumes: existing `Runner` interface, `Entry` struct, `parsePorcelain` helper (all in `worktree.go`).
- Produces: `func Primary(r Runner, repoPath string) (Entry, error)` — returns the primary working-tree entry (path == repoPath, its short branch; `Branch:""` when detached). Non-nil error only when `repoPath` is not a usable git repo. When porcelain omits the primary entry, returns `Entry{Path: repoPath}`.

- [ ] **Step 1: Write the failing tests**

Append to `internal/worktree/worktree_test.go`:

```go
func TestPrimary_ReturnsMainCheckoutBranch(t *testing.T) {
	r := &fakeRunner{listOut: porcelain}
	got, err := Primary(r, "/repo/FlowHub-CAS-AISE")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got.Path != "/repo/FlowHub-CAS-AISE" || got.Branch != "main" {
		t.Errorf("Primary = %+v, want path=/repo/FlowHub-CAS-AISE branch=main", got)
	}
}

func TestPrimary_DetachedHeadHasEmptyBranch(t *testing.T) {
	pc := `worktree /repo/r
HEAD aaa

worktree /repo/r/.worktrees/x
HEAD bbb
branch refs/heads/worktree-x
`
	r := &fakeRunner{listOut: pc}
	got, err := Primary(r, "/repo/r")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got.Path != "/repo/r" || got.Branch != "" {
		t.Errorf("Primary = %+v, want path=/repo/r branch=\"\" (detached)", got)
	}
}

func TestPrimary_ListErrorPropagates(t *testing.T) {
	r := &fakeRunner{listErr: errors.New("fatal: not a git repository")}
	if _, err := Primary(r, "/nope"); err == nil {
		t.Errorf("Primary error = nil, want non-nil on git failure")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/worktree/ -run TestPrimary -v`
Expected: FAIL — `undefined: Primary`.

- [ ] **Step 3: Implement `Primary`**

Insert into `internal/worktree/worktree.go` immediately after `List` (after line 49):

```go
// Primary returns the repo's primary working tree (repoPath itself): its path
// and short branch ("" when detached). List excludes this entry; Primary is how
// nav surfaces the main checkout as a pinned base row. A non-nil error means
// repoPath is not a usable git repo. When porcelain output unexpectedly omits
// the primary entry, it returns Entry{Path: repoPath} so callers can still render.
func Primary(r Runner, repoPath string) (Entry, error) {
	out, err := r.Run(repoPath, "worktree", "list", "--porcelain")
	if err != nil {
		return Entry{}, fmt.Errorf("git worktree list: %w", err)
	}
	main := filepath.Clean(repoPath)
	for _, e := range parsePorcelain(out) {
		if filepath.Clean(e.path) == main {
			return Entry{Path: e.path, Branch: e.branch}, nil
		}
	}
	return Entry{Path: repoPath}, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/worktree/ -run TestPrimary -v`
Expected: PASS (all three).

- [ ] **Step 5: Full package check + commit**

Run: `gofmt -l internal/worktree/ && go test -race ./internal/worktree/`
Expected: no gofmt output; tests PASS.

```bash
git add internal/worktree/worktree.go internal/worktree/worktree_test.go
git commit -m "feat(worktree): add Primary to surface the main checkout (#174)"
```

---

### Task 2: Base row in `buildDashRows` + data wiring

**Files:**
- Modify: `internal/nav/types.go:68-83` (add `isBase bool` to `dashRow`)
- Modify: `internal/nav/format.go:151-180` (`buildDashRows` signature + base row; add `applySession` helper)
- Modify: `internal/nav/data.go:102-109` (`loadDashRowsCmd` calls `worktree.Primary`)
- Test: `internal/nav/format_test.go` (update `TestBuildDashRows_MatchesSessionsAndSorts`; add base-row test)

**Interfaces:**
- Consumes: `worktree.Primary` (Task 1); `core.SlotID`, `core.Slot`, `core.Session`, `core.Repo`; existing `slotRepoMatches`, `humanLastAccessed`, `sortDashRows`.
- Produces:
  - `dashRow.isBase bool` field.
  - New signature `func buildDashRows(repo core.Repo, baseBranch string, wts []worktree.Entry, slots []core.Slot, sessions []core.Session, now time.Time) []dashRow` — result always has the base row at index `0` (`isBase:true, worktree:"", path:repo.Path, branch:baseBranch`), followed by the sorted worktree rows.
  - `func applySession(row *dashRow, sl core.Slot, liveBySlot map[string]core.Session, now time.Time)` — fills a row's live-session fields.

- [ ] **Step 1: Add the `isBase` field**

In `internal/nav/types.go`, extend `dashRow` (the struct at lines 68-83). Add the field just below `hasSession`:

```go
	hasSession   bool
	isBase       bool // true only for the pinned main-checkout row (worktree == "")
	dirty        dirtyInfo
```

- [ ] **Step 2: Update the existing failing test + add the base-row test**

Replace `TestBuildDashRows_MatchesSessionsAndSorts` in `internal/nav/format_test.go` (lines 46-78) with the two tests below. The existing test's call site and assertions change because `buildDashRows` now takes `baseBranch` and prepends the base row.

```go
func TestBuildDashRows_MatchesSessionsAndSorts(t *testing.T) {
	repo := core.Repo{Name: "bridge"}
	wts := []worktree.Entry{
		{Path: "/r/.worktrees/fix-x", Branch: "worktree-fix-x"},
		{Path: "/r/.worktrees/docs", Branch: "worktree-docs"},
		{Path: "/r/.worktrees/spike", Branch: "worktree-spike"},
	}
	slots := []core.Slot{
		{ID: "s-fix", Repo: "bridge", Worktree: "fix-x", Agent: "claude"},
		{ID: "s-docs", Repo: "freaxnx01/bridge", Worktree: "docs", Agent: "copilot"},
	}
	sessions := []core.Session{
		{SlotID: "s-fix", State: "attached", LastActivity: time.Unix(1000, 0)},
		{SlotID: "s-docs", State: "detached", LastActivity: time.Unix(2000, 0)},
	}
	now := time.Unix(3000, 0)
	got := buildDashRows(repo, "main", wts, slots, sessions, now)

	if len(got) != 4 {
		t.Fatalf("got %d rows, want 4 (base + 3 worktrees)", len(got))
	}
	// Base row is always first and pinned (no session in this fixture).
	if !got[0].isBase || got[0].worktree != "" || got[0].branch != "main" || got[0].hasSession {
		t.Errorf("row[0] = %+v, want base row (isBase, worktree=\"\", branch=main, no session)", got[0])
	}
	// Then sessioned worktrees by last-accessed DESC (docs@2000 before fix@1000),
	// then session-less (spike).
	if got[1].worktree != "docs" || !got[1].hasSession || got[1].agent != "copilot" {
		t.Errorf("row[1] = %+v, want docs/copilot/hasSession", got[1])
	}
	if got[2].worktree != "fix-x" || got[2].state != "attached" {
		t.Errorf("row[2] = %+v, want fix-x/attached", got[2])
	}
	if got[3].worktree != "spike" || got[3].hasSession {
		t.Errorf("row[3] = %+v, want spike with no session", got[3])
	}
}

func TestBuildDashRows_BaseRowPinnedFirstWithSession(t *testing.T) {
	repo := core.Repo{Name: "bridge"}
	wts := []worktree.Entry{
		{Path: "/r/.worktrees/fix-x", Branch: "worktree-fix-x"},
	}
	// A live worktree session that is MORE recent than the base session — the
	// base row must still stay pinned at index 0.
	slots := []core.Slot{
		{ID: "bridge", Repo: "bridge", Worktree: "", Agent: "claude"},
		{ID: "s-fix", Repo: "bridge", Worktree: "fix-x", Agent: "copilot"},
	}
	sessions := []core.Session{
		{SlotID: "bridge", State: "attached", LastActivity: time.Unix(1000, 0)},
		{SlotID: "s-fix", State: "detached", LastActivity: time.Unix(9000, 0)},
	}
	now := time.Unix(10000, 0)
	got := buildDashRows(repo, "main", wts, slots, sessions, now)

	if len(got) != 2 {
		t.Fatalf("got %d rows, want 2 (base + 1 worktree)", len(got))
	}
	if !got[0].isBase || !got[0].hasSession || got[0].slotID != "bridge" || got[0].agent != "claude" || got[0].state != "attached" {
		t.Errorf("row[0] = %+v, want base row with live 'bridge' session (claude/attached)", got[0])
	}
	if got[0].path != repo.Path {
		t.Errorf("base row path = %q, want repo.Path %q", got[0].path, repo.Path)
	}
	if got[1].worktree != "fix-x" {
		t.Errorf("row[1] = %+v, want fix-x", got[1])
	}
}
```

- [ ] **Step 3: Run tests to verify they fail (compile error)**

Run: `go test ./internal/nav/ -run TestBuildDashRows -v`
Expected: FAIL — build error: not enough arguments in call to `buildDashRows` (signature not yet updated).

- [ ] **Step 4: Update `buildDashRows` + add `applySession`**

In `internal/nav/format.go`, replace the whole `buildDashRows` function (lines 151-180) with:

```go
// buildDashRows joins the repo's worktrees with the global sessions/slots into
// dashboard rows. The pinned base row (the main checkout) is always index 0,
// never reordered by session recency. A worktree gets a live session when a slot
// for this repo names it and that slot's tmux session is live. Worktree rows with
// a session sort first by last-accessed DESC; session-less worktrees follow,
// name-sorted. baseBranch is the primary tree's branch ("" when detached).
// dirtyState is loadPending (filled later by dirtyMsg).
func buildDashRows(repo core.Repo, baseBranch string, wts []worktree.Entry, slots []core.Slot, sessions []core.Session, now time.Time) []dashRow {
	liveBySlot := make(map[string]core.Session, len(sessions))
	for _, s := range sessions {
		liveBySlot[s.SlotID] = s
	}
	// worktree name -> slot (for this repo only); the base slot (Worktree=="")
	// is captured separately because it keys the pinned base row, not a worktree.
	slotByWt := make(map[string]core.Slot)
	var baseSlot core.Slot
	haveBaseSlot := false
	for _, sl := range slots {
		if !slotRepoMatches(sl.Repo, repo) {
			continue
		}
		if sl.Worktree == "" {
			baseSlot, haveBaseSlot = sl, true
			continue
		}
		slotByWt[sl.Worktree] = sl
	}
	rows := make([]dashRow, 0, len(wts))
	for _, e := range wts {
		name := filepath.Base(e.Path)
		row := dashRow{worktree: name, branch: e.Branch, path: e.Path, dirtyState: loadPending}
		if sl, ok := slotByWt[name]; ok {
			applySession(&row, sl, liveBySlot, now)
		}
		rows = append(rows, row)
	}
	sortDashRows(rows, liveBySlot)

	base := dashRow{isBase: true, branch: baseBranch, path: repo.Path, dirtyState: loadPending}
	if haveBaseSlot {
		applySession(&base, baseSlot, liveBySlot, now)
	}
	return append([]dashRow{base}, rows...)
}

// applySession fills row's live-session fields from slot sl when sl has a live
// session in liveBySlot; otherwise it leaves row session-less.
func applySession(row *dashRow, sl core.Slot, liveBySlot map[string]core.Session, now time.Time) {
	sess, live := liveBySlot[sl.ID]
	if !live {
		return
	}
	row.hasSession = true
	row.slotID = sl.ID
	row.agent = sl.Agent
	row.state = sess.State
	row.lastAccessed = humanLastAccessed(now.Sub(sess.LastActivity))
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/nav/ -run TestBuildDashRows -v`
Expected: FAIL — build error: not enough arguments in call to `buildDashRows` in `data.go` (call site still old). Proceed to Step 6 to fix the caller.

- [ ] **Step 6: Wire `worktree.Primary` into `loadDashRowsCmd`**

In `internal/nav/data.go`, replace `loadDashRowsCmd` (lines 102-109) with:

```go
func loadDashRowsCmd(repo core.Repo, slotsPath string) tea.Cmd {
	return func() tea.Msg {
		wts, _ := worktree.List(worktree.ExecRunner{}, repo.Path)
		primary, _ := worktree.Primary(worktree.ExecRunner{}, repo.Path)
		slots, _ := core.LoadSlots(slotsPath)
		live, _ := core.LiveSessions()
		return dashRowsMsg{rows: buildDashRows(repo, primary.Branch, wts, slots, live, time.Now())}
	}
}
```

- [ ] **Step 7: Run the full nav suite to verify it passes**

Run: `go test ./internal/nav/ -run TestBuildDashRows -v && go vet ./internal/nav/`
Expected: both new tests PASS; `go vet` clean.

- [ ] **Step 8: Commit**

Run: `gofmt -l internal/nav/`
Expected: empty.

```bash
git add internal/nav/types.go internal/nav/format.go internal/nav/data.go internal/nav/format_test.go
git commit -m "feat(nav): pin a base-checkout row on the dashboard (#174)"
```

---

### Task 3: Render the base row as `★ <branch>`

**Files:**
- Modify: `internal/nav/view.go:196-233` (`dashListBody` name column; add `listName` helper above it)
- Test: `internal/nav/view_test.go` (append two view tests)

**Interfaces:**
- Consumes: `dashRow.isBase`, `dashRow.branch`, `dashRow.worktree` (Task 2); `Model.repo` (`core.Repo`).
- Produces: `func (r dashRow) listName(repoName string) string` — the name-column label: `r.worktree`, or `"★ <branch>"` for the base row (falling back to `"★ <repoName>"` when `r.branch == ""`).

- [ ] **Step 1: Write the failing tests**

Append to `internal/nav/view_test.go`:

```go
func TestViewDash_BaseRow_ShowsStarBranch(t *testing.T) {
	m := initialModel(Config{})
	m.width, m.height = 100, 30
	m.screen = screenDash
	m.repo = core.Repo{Name: "bridge"}
	m.dashRows = []dashRow{
		{isBase: true, branch: "main", path: "/r"},
		{worktree: "fix-x", branch: "worktree-fix-x"},
	}
	out := m.View()
	if !strings.Contains(out, "★ main") {
		t.Errorf("dash view missing base row label '★ main':\n%s", out)
	}
}

func TestViewDash_BaseRow_DetachedFallsBackToRepoName(t *testing.T) {
	m := initialModel(Config{})
	m.width, m.height = 100, 30
	m.screen = screenDash
	m.repo = core.Repo{Name: "bridge"}
	m.dashRows = []dashRow{{isBase: true, branch: "", path: "/r"}}
	out := m.View()
	if !strings.Contains(out, "★ bridge") {
		t.Errorf("detached base row should fall back to '★ bridge':\n%s", out)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/nav/ -run TestViewDash_BaseRow -v`
Expected: FAIL — the name column renders empty (`worktree == ""`), so `★ main` / `★ bridge` are absent.

- [ ] **Step 3: Add `listName` and use it in `dashListBody`**

In `internal/nav/view.go`, add this helper immediately above `dashListBody` (before line 193's doc comment):

```go
// listName is the worktree-list name-column label for a row: the worktree name,
// or "★ <branch>" for the pinned base row (falling back to the repo name when the
// primary HEAD is detached and carries no branch).
func (r dashRow) listName(repoName string) string {
	if !r.isBase {
		return r.worktree
	}
	branch := r.branch
	if branch == "" {
		branch = repoName
	}
	return "★ " + branch
}
```

Then in `dashListBody`, replace both `trunc(r.worktree, 18)` occurrences (lines 212 and 218) with `trunc(r.listName(m.repo.Name), 18)`:

```go
		if compact {
			line = fmt.Sprintf("%s %-18s %-7s %s", dot, trunc(r.listName(m.repo.Name), 18), trunc(agent, 7), m.dirtyView(r))
		} else {
			la := r.lastAccessed
			if !r.hasSession {
				la = "(no session)"
			}
			line = fmt.Sprintf("%s %-18s %-14s %-8s %-12s %s",
				dot, trunc(r.listName(m.repo.Name), 18), trunc(r.branch, 14), agent, la, m.dirtyView(r))
		}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/nav/ -run TestViewDash_BaseRow -v`
Expected: PASS (both).

- [ ] **Step 5: Full-suite gate + commit**

Run: `gofmt -l . && go vet ./... && go test -race ./...`
Expected: no gofmt output; vet clean; all tests PASS.

```bash
git add internal/nav/view.go internal/nav/view_test.go
git commit -m "feat(nav): render base-checkout row as ★ <branch> (#174)"
```

---

## Self-Review Notes

- **Spec coverage:** base row always first & pinned (Task 2 test `BaseRowPinnedFirstWithSession`); `★ <branch>` label + detached fallback (Task 3); bare `<repo>` slot via `worktree:""` → `SlotID(repo,"")` in the unchanged `launchPlan` (Task 2 row construction); live-session pickup for `Worktree==""` slots (Task 2); no keybinding change (base row is `dashRows[0]`, existing `enter`/nav untouched); `worktree.Resolve`/shell open unchanged (not touched). Spec item #6 (Details hint) was dropped in the spec — it referenced the issues-pane hint, unrelated to the base row.
- **`enter`/launch path:** verified no code change needed — `launchPlan` (`update.go:546-577`) already computes `slot = core.SlotID(m.repo.Name, row.worktree)` (= bare `<repo>` when `worktree==""`) and launches in `row.path` (= `repo.Path`); `NameArgs` → `displayName(repo,"")` = `<repo>` (`cmd/bridge/preflight.go:330`). No task edits `update.go`.
- **Dirty status:** base row carries `path=repo.Path`, `dirtyState=loadPending`, so the existing per-`path` dirty loader covers it — no new wiring.
- **Type consistency:** `isBase` (types.go) used identically in format.go/view.go/tests; `applySession` signature stable; `buildDashRows`'s new `baseBranch` param threaded from `data.go`.
