# nav base-checkout (main) row (#182) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a pinned base-checkout ("main") row as the first row of the
`bridge nav` dashboard. `enter` on it launches/attaches a session in the repo
root (`repo.Path`) with the bare `<repo>` slot id — the same tmux session as the
shell `bridge open <repo>` — without creating or resolving a worktree.

**Architecture:** `worktree.List` deliberately drops the primary working tree, so
add a `worktree.Primary` counterpart that returns it (path + branch). `buildDashRows`
gains a `baseBranch` parameter and prepends a base `dashRow` (`isBase: true`,
`worktree: ""`, `path: repo.Path`) **after** sorting the worktree rows, so it is
pinned first. The base row rides the existing `launchRow`/`launchPlan` path
unchanged: `core.SlotID(repo.Name, "")` == `<repo>` and it launches in
`repo.Path`, bypassing `worktree.Resolve`. The view renders the base row as
`★ <branch>` (or `★ <repo>` when detached).

**Tech Stack:** Go (Bubble Tea/lipgloss, stdlib `testing`). Spec:
`docs/superpowers/specs/2026-07-01-nav-base-checkout-row-design.md`.

---

## File Structure

- **Modify** `internal/worktree/worktree.go` — add `Primary`.
- **Modify** `internal/worktree/worktree_test.go` — `Primary` tests.
- **Modify** `internal/nav/types.go` — `dashRow.isBase` field.
- **Modify** `internal/nav/format.go` — `buildDashRows` signature + `baseRow`.
- **Modify** `internal/nav/format_test.go` — update the existing test; add base-row tests.
- **Modify** `internal/nav/data.go` — `loadDashRowsCmd` calls `worktree.Primary`.
- **Modify** `internal/nav/view.go` — `dashListBody` renders the `★` base label.
- **Modify** `internal/nav/launch_test.go` — base-row launch test.
- **Modify** `internal/nav/flow_test.go` + `internal/nav/testdata/` — golden flow.

No changes to `internal/worktree/worktree.go`'s `Resolve`, to `cmd/bridge`, or to
`launchRow`/`launchPlan` — the base row reuses the existing launch path.

---

## Task 1: `worktree.Primary`

**Files:**
- Modify: `internal/worktree/worktree.go`
- Test: `internal/worktree/worktree_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `internal/worktree/worktree_test.go`. Reuse the package's existing
`fakeRunner` (`worktree_test.go:12-35`): it returns `listOut`/`listErr` for
`worktree list` calls. The porcelain fixtures below mirror the file's existing
`porcelain` const (first `worktree ` block = primary, `branch refs/heads/…` line,
`detached` for a detached HEAD):

```go
func TestPrimary_ReturnsRepoRootBranch(t *testing.T) {
	r := &fakeRunner{listOut: "worktree /r\nHEAD abc\nbranch refs/heads/main\n\n" +
		"worktree /r/.worktrees/fix\nHEAD def\nbranch refs/heads/worktree-fix\n\n"}
	got, err := Primary(r, "/r")
	if err != nil {
		t.Fatalf("Primary: %v", err)
	}
	if got.Path != "/r" || got.Branch != "main" {
		t.Errorf("Primary = %+v, want {/r main}", got)
	}
}

func TestPrimary_DetachedHead_EmptyBranch(t *testing.T) {
	r := &fakeRunner{listOut: "worktree /r\nHEAD abc\ndetached\n\n"}
	got, err := Primary(r, "/r")
	if err != nil {
		t.Fatalf("Primary: %v", err)
	}
	if got.Path != "/r" || got.Branch != "" {
		t.Errorf("Primary = %+v, want {/r <empty>}", got)
	}
}

func TestPrimary_NotAGitRepo_Errors(t *testing.T) {
	r := &fakeRunner{listErr: errors.New("not a git repository")}
	if _, err := Primary(r, "/r"); err == nil {
		t.Error("Primary should error when git worktree list fails")
	}
}
```

(`errors` is already imported by `worktree_test.go` — no new import. `parsePorcelain` reads `branch refs/heads/<x>` and leaves `branch == ""` when the block has only `detached`, matching `List`'s behaviour.)

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/worktree/ -run TestPrimary -v`
Expected: FAIL — `Primary` undefined.

- [ ] **Step 3: Implement `Primary`**

In `internal/worktree/worktree.go`, add after `List` (it reuses `parsePorcelain`
and the same primary-match logic `List` uses at lines 40-46):

```go
// Primary returns the repo's primary working tree (repoPath itself): its path
// and short branch name ("" when HEAD is detached). It is the counterpart to
// List, which excludes the primary — nav needs the primary's branch to label the
// base-checkout row. A non-nil error means repoPath is not a usable git repo, or
// its primary working tree could not be found in the porcelain output.
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
	return Entry{}, fmt.Errorf("primary working tree not found for %s", repoPath)
}
```

(`fmt`, `filepath`, `parsePorcelain`, `Entry`, `Runner` already exist in the file — no new imports.)

- [ ] **Step 4: Run tests**

Run: `go test ./internal/worktree/ -run TestPrimary -v && go test ./internal/worktree/`
Expected: PASS (new) and the full `worktree` package still green.

- [ ] **Step 5: Commit**

```bash
gofmt -l internal/worktree/ ; go vet ./internal/worktree/
git add internal/worktree/worktree.go internal/worktree/worktree_test.go
git commit -m "feat(worktree): add Primary to return the primary working tree

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 2: base row in `buildDashRows` (pinned first)

**Files:**
- Modify: `internal/nav/types.go`
- Modify: `internal/nav/format.go`
- Test: `internal/nav/format_test.go`

- [ ] **Step 1: Write / update the failing tests**

First, **update** the existing `TestBuildDashRows_MatchesSessionsAndSorts` in
`internal/nav/format_test.go` for the new signature and the prepended base row.
Change the call and re-index the assertions (a base row is now at index 0, so the
worktree rows shift by one, and `len` grows by one):

```go
	now := time.Unix(3000, 0)
	got := buildDashRows(repo, "main", wts, slots, sessions, now)

	if len(got) != 4 { // base + 3 worktrees
		t.Fatalf("got %d rows, want 4", len(got))
	}
	// row[0] is the pinned base row (no session in this fixture).
	if !got[0].isBase || got[0].worktree != "" || got[0].branch != "main" || got[0].hasSession {
		t.Errorf("row[0] = %+v, want pinned base row (branch main, no session)", got[0])
	}
	// Sessioned worktree rows follow, sorted by last-accessed DESC (docs before fix),
	// then session-less worktrees (spike).
	if got[1].worktree != "docs" || !got[1].hasSession || got[1].agent != "copilot" {
		t.Errorf("row[1] = %+v, want docs/copilot/hasSession", got[1])
	}
	if got[2].worktree != "fix-x" || got[2].state != "attached" {
		t.Errorf("row[2] = %+v, want fix-x/attached", got[2])
	}
	if got[3].worktree != "spike" || got[3].hasSession {
		t.Errorf("row[3] = %+v, want spike with no session", got[3])
	}
```

Then **append** two new tests:

```go
func TestBuildDashRows_BasePinnedFirstDespiteRecency(t *testing.T) {
	repo := core.Repo{Name: "bridge", Path: "/r"}
	wts := []worktree.Entry{{Path: "/r/.worktrees/fix", Branch: "worktree-fix"}}
	slots := []core.Slot{{ID: "bridge-wt-fix", Repo: "bridge", Worktree: "fix", Agent: "claude"}}
	// The worktree session is very recent; the base row must still be first.
	sessions := []core.Session{{SlotID: "bridge-wt-fix", State: "attached", LastActivity: time.Unix(9000, 0)}}
	got := buildDashRows(repo, "main", wts, slots, sessions, time.Unix(9001, 0))
	if len(got) != 2 || !got[0].isBase {
		t.Fatalf("base row must be pinned first, got %+v", got)
	}
}

func TestBuildDashRows_BaseLiveSession(t *testing.T) {
	repo := core.Repo{Name: "bridge", Path: "/r"}
	// A live bare-<repo> session (slot id "bridge") started via `bridge open bridge`.
	slots := []core.Slot{{ID: "bridge", Repo: "bridge", Worktree: "", Agent: "codex"}}
	sessions := []core.Session{{SlotID: "bridge", State: "detached", LastActivity: time.Unix(1000, 0)}}
	got := buildDashRows(repo, "main", nil, slots, sessions, time.Unix(1000, 0))
	base := got[0]
	if !base.isBase || !base.hasSession || base.slotID != "bridge" {
		t.Fatalf("base row session fields wrong: %+v", base)
	}
	if base.agent != "codex" || base.state != "detached" {
		t.Errorf("base row = %+v, want agent codex / state detached from the bare slot", base)
	}
}

func TestBuildDashRows_DetachedPrimary_EmptyBranch(t *testing.T) {
	repo := core.Repo{Name: "bridge", Path: "/r"}
	got := buildDashRows(repo, "", nil, nil, nil, time.Unix(0, 0))
	if len(got) != 1 || !got[0].isBase || got[0].branch != "" {
		t.Fatalf("detached primary → base row with empty branch, got %+v", got)
	}
}
```

(Confirm `core.SlotID("bridge", "") == "bridge"` and `core.SlotID("bridge", "fix") == "bridge-wt-fix"` against `internal/core/slot.go:24-33` — the slot ids above rely on it.)

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/nav/ -run TestBuildDashRows -v`
Expected: FAIL — `buildDashRows` still has the old signature and no base row; `dashRow.isBase` undefined.

- [ ] **Step 3: Add the `isBase` field**

In `internal/nav/types.go`, add to the `dashRow` struct (near the other row fields, ~line 80):

```go
	// isBase marks the pinned base-checkout ("main") row: it launches in the repo
	// root (path == repo.Path) with the bare "<repo>" slot (worktree == ""), the
	// same session as the shell `bridge open <repo>`. Never a real worktree.
	isBase bool
```

- [ ] **Step 4: Change `buildDashRows` + add `baseRow`**

In `internal/nav/format.go`, change the `buildDashRows` signature to take
`baseBranch string` and prepend the base row **after** `sortDashRows` (so it is
pinned regardless of recency). Replace the current signature line and the trailing
`sortDashRows(rows, liveBySlot); return rows`:

```go
func buildDashRows(repo core.Repo, baseBranch string, wts []worktree.Entry, slots []core.Slot, sessions []core.Session, now time.Time) []dashRow {
	// … existing body unchanged up to and including the worktree-row loop …
	sortDashRows(rows, liveBySlot)
	base := baseRow(repo, baseBranch, slots, liveBySlot, now)
	return append([]dashRow{base}, rows...)
}

// baseRow builds the pinned base-checkout row for repo's primary working tree. It
// launches in repo.Path with the bare "<repo>" slot id (worktree ""), so it
// shares a session with the shell `bridge open <repo>`. When a live bare-<repo>
// session exists it carries that session's dot/agent/state/last-accessed, exactly
// like a worktree row.
func baseRow(repo core.Repo, branch string, slots []core.Slot, liveBySlot map[string]core.Session, now time.Time) dashRow {
	row := dashRow{isBase: true, branch: branch, path: repo.Path, dirtyState: loadPending}
	id := core.SlotID(repo.Name, "") // == "<repo>"
	sess, live := liveBySlot[id]
	if !live {
		return row
	}
	row.hasSession = true
	row.slotID = id
	row.state = sess.State
	row.lastAccessed = humanLastAccessed(now.Sub(sess.LastActivity))
	for _, sl := range slots {
		if sl.ID == id { // the bare-<repo> slot carries the agent
			row.agent = sl.Agent
			break
		}
	}
	return row
}
```

(`liveBySlot` is already built at the top of `buildDashRows` (`format.go:152-155`); pass it straight into `baseRow`. `humanLastAccessed` and `core.SlotID` are already in scope.)

- [ ] **Step 5: Run tests**

Run: `go test ./internal/nav/ -run TestBuildDashRows -v`
Expected: PASS (updated + new). The rest of the nav package won't compile yet if
other callers of `buildDashRows` exist — grep to confirm the only non-test caller
is `loadDashRowsCmd` (fixed in Task 3): `grep -rn "buildDashRows(" internal/nav/`.
If `go test ./internal/nav/` fails to build solely on `loadDashRowsCmd`, that is
expected until Task 3; the `-run TestBuildDashRows` compile of the test file plus
`format.go` is the RED→GREEN signal here.

- [ ] **Step 6: Commit** (after Task 3 makes the package build; or stage together)

Defer the commit to the end of Task 3 so the package compiles.

---

## Task 3: wire `worktree.Primary` into `loadDashRowsCmd`

**Files:**
- Modify: `internal/nav/data.go`

- [ ] **Step 1: Update `loadDashRowsCmd`**

In `internal/nav/data.go` (`loadDashRowsCmd`, ~line 108), call `worktree.Primary`
and pass its branch to `buildDashRows`:

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

(The `worktree.Primary` error is intentionally ignored — same as `worktree.List` on the line above — so a non-git path yields `primary.Branch == ""` and the base row falls back to `★ <repo-name>`. `worktree` is already imported.)

- [ ] **Step 2: Build + full nav suite**

Run: `go build ./internal/nav/ && go test ./internal/nav/`
Expected: builds; the full nav package green (Task 2's tests + everything else).

- [ ] **Step 3: Commit Tasks 2 + 3**

```bash
gofmt -l internal/nav/ ; go vet ./internal/nav/
git add internal/nav/types.go internal/nav/format.go internal/nav/format_test.go internal/nav/data.go
git commit -m "feat(nav): pin a base-checkout row first on the dashboard

buildDashRows prepends a base row (isBase, worktree \"\", path repo.Path) after
sorting, so it stays first regardless of session recency; loadDashRowsCmd reads
the primary branch via worktree.Primary for its label.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 4: render the `★` base label

**Files:**
- Modify: `internal/nav/view.go`
- Test: `internal/nav/flow_test.go` (unit assertion) + golden below

- [ ] **Step 1: Write the failing test**

Append to `internal/nav/flow_test.go` (white-box, package `nav`):

```go
func TestDashListBody_BaseRowStarLabelFirst(t *testing.T) {
	m := initialModel(Config{})
	m.width, m.height = 120, 40
	m.repo = core.Repo{Name: "bridge", Path: "/r"}
	m.dashRows = []dashRow{
		{isBase: true, branch: "main", path: "/r", dirtyState: loadOK, dirty: dirtyInfo{clean: true}},
		{worktree: "fix", branch: "worktree-fix", path: "/r/.worktrees/fix", dirtyState: loadOK, dirty: dirtyInfo{clean: true}},
	}
	body := m.dashListBody(false)
	star := strings.Index(body, "★ main")
	fix := strings.Index(body, "fix")
	if star < 0 {
		t.Fatalf("base row should render \"★ main\":\n%s", body)
	}
	if fix >= 0 && star > fix {
		t.Errorf("base row must render before worktree rows:\n%s", body)
	}
}

func TestDashListBody_BaseRowDetachedUsesRepoName(t *testing.T) {
	m := initialModel(Config{})
	m.width, m.height = 120, 40
	m.repo = core.Repo{Name: "bridge", Path: "/r"}
	m.dashRows = []dashRow{{isBase: true, branch: "", path: "/r", dirtyState: loadOK, dirty: dirtyInfo{clean: true}}}
	if body := m.dashListBody(false); !strings.Contains(body, "★ bridge") {
		t.Errorf("detached primary should render \"★ bridge\":\n%s", body)
	}
}
```

(`strings` and `core` are already imported by `flow_test.go` — verify; `dashListBody` strips no ANSI, so `strings.Contains` on the raw string is fine here because the label text is not inside an escape.)

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/nav/ -run TestDashListBody_BaseRow -v`
Expected: FAIL — the base row currently renders `trunc(r.worktree, 18)` == "" (blank), not `★ main`.

- [ ] **Step 3: Render the base label in `dashListBody`**

In `internal/nav/view.go` (`dashListBody`, ~line 204), compute the row name once
before building the line, and use it in both the compact and full `fmt.Sprintf`
calls in place of `trunc(r.worktree, 18)`:

```go
	for i, r := range m.dashRows {
		dot := stMuted.Render("·")
		switch r.state {
		case "attached":
			dot = stOk.Render("●")
		case "detached":
			dot = stMuted.Render("○")
		}
		agent := r.agent
		if agent == "" {
			agent = "—"
		}
		name := trunc(r.worktree, 18)
		if r.isBase {
			label := r.branch
			if label == "" {
				label = m.repo.Name
			}
			name = trunc("★ "+label, 18)
		}
		var line string
		if compact {
			line = fmt.Sprintf("%s %-18s %-7s %s", dot, name, trunc(agent, 7), m.dirtyView(r))
		} else {
			la := r.lastAccessed
			if !r.hasSession {
				la = "(no session)"
			}
			line = fmt.Sprintf("%s %-18s %-14s %-8s %-12s %s",
				dot, name, trunc(r.branch, 14), agent, la, m.dirtyView(r))
		}
		// … unchanged: selection highlight + b.WriteString …
	}
```

(Only the two `trunc(r.worktree, 18)` occurrences become `name`; everything else in the loop is unchanged. The full layout still shows `r.branch` in its own column, which for the base row is the primary branch — consistent with worktree rows showing their branch.)

- [ ] **Step 4: Run the unit tests**

Run: `go test ./internal/nav/ -run TestDashListBody_BaseRow -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
gofmt -l internal/nav/ ; go vet ./internal/nav/
git add internal/nav/view.go internal/nav/flow_test.go
git commit -m "feat(nav): render the base-checkout row as ★ <branch>

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 5: base-row launch (bare slot, repo root, no Resolve)

**Files:**
- Test: `internal/nav/launch_test.go`

This task adds **only tests** — the launch behaviour already falls out of the
existing `launchPlan` (`update.go:648-679`): `slot = core.SlotID(m.repo.Name,
row.worktree)` and it launches in `row.path`, never calling `worktree.Resolve`.
The tests lock that contract in for the base row.

- [ ] **Step 1: Write the tests**

Append to `internal/nav/launch_test.go` (mirrors the existing
`TestLaunchArgvFor_*` tests at lines 11-52):

```go
func TestLaunchArgvFor_BaseRow_BareSlotInRepoRoot(t *testing.T) {
	t.Setenv("TMUX", "") // deterministic non-nested LaunchArgv path
	m := initialModel(Config{DefaultAgent: "claude"})
	m.repo = core.Repo{Name: "bridge", Path: "/r"}
	row := dashRow{isBase: true, worktree: "", path: "/r"} // no live session
	argv, err := m.launchArgvFor(row)
	if err != nil {
		t.Fatalf("launchArgvFor: %v", err)
	}
	joined := strings.Join(argv, " ")
	if argv[0] != "tmux" || argv[1] != "new-session" {
		t.Fatalf("argv = %v, want a tmux new-session launch", argv)
	}
	// Launches in the repo root, not a worktree dir.
	if !strings.Contains(joined, "/r") || strings.Contains(joined, ".worktrees") {
		t.Errorf("argv = %v, want repo root /r and no worktree dir", argv)
	}
	// Bare "<repo>" session name — same as `bridge open bridge` (no -w).
	if !strings.Contains(joined, "bridge") || strings.Contains(joined, "bridge-wt-") {
		t.Errorf("argv = %v, want bare session name bridge (not bridge-wt-…)", argv)
	}
}

func TestLaunchPlan_BaseRow_SlotIsBareRepo(t *testing.T) {
	t.Setenv("TMUX", "")
	m := initialModel(Config{DefaultAgent: "claude"})
	m.repo = core.Repo{Name: "bridge", Path: "/r"}
	_, slot, _, err := m.launchPlan(dashRow{isBase: true, worktree: "", path: "/r"})
	if err != nil {
		t.Fatalf("launchPlan: %v", err)
	}
	if slot != core.SlotID("bridge", "") || slot != "bridge" {
		t.Errorf("base slot = %q, want bare %q", slot, core.SlotID("bridge", ""))
	}
}

func TestLaunchArgvFor_BaseRow_LiveSession_Attaches(t *testing.T) {
	m := initialModel(Config{})
	m.repo = core.Repo{Name: "bridge", Path: "/r"}
	row := dashRow{isBase: true, worktree: "", path: "/r", hasSession: true, slotID: "bridge"}
	argv, err := m.launchArgvFor(row)
	if err != nil {
		t.Fatalf("launchArgvFor: %v", err)
	}
	want := []string{"tmux", "attach-session", "-t", "bridge"}
	if strings.Join(argv, " ") != strings.Join(want, " ") {
		t.Errorf("argv = %v, want attach to bare session %v", argv, want)
	}
}
```

- [ ] **Step 2: Run**

Run: `go test ./internal/nav/ -run 'TestLaunchArgvFor_BaseRow|TestLaunchPlan_BaseRow' -v`
Expected: PASS immediately — the base row needs no launch-path change; if any
fails, the launch path is wrong for the base row and must be fixed (do **not**
change a test to go green).

- [ ] **Step 3: Commit**

```bash
git add internal/nav/launch_test.go
git commit -m "test(nav): base row launches bare <repo> slot in repo root

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 6: golden flow — base row pinned on the dashboard

**Files:**
- Test: `internal/nav/flow_test.go` + `internal/nav/testdata/`

- [ ] **Step 1: Write the golden flow test**

Append to `internal/nav/flow_test.go`. Drive a model onto the dashboard with a
base row + one worktree row and snapshot the frame. Set the rows directly (the
harness has no git; the base row + a worktree row is enough to prove ordering and
the `★` label):

```go
func TestFlow_Dashboard_BaseRowPinned_Golden(t *testing.T) {
	s := newSession(t, Config{})
	s.m.screen = screenDash
	s.m.repo = core.Repo{Name: "bridge", Path: "/r"}
	s.m.dashFocus = dashFocusWorktrees
	s.send(dashRowsMsg{rows: []dashRow{
		{isBase: true, branch: "main", path: "/r", dirtyState: loadOK, dirty: dirtyInfo{clean: true}},
		{worktree: "fix-x", branch: "worktree-fix-x", path: "/r/.worktrees/fix-x", dirtyState: loadOK, dirty: dirtyInfo{clean: true}},
	}})
	assertGolden(t, "dash_base_row_pinned", s.frame())
}
```

(Confirm `screenDash`, `dashFocusWorktrees`, `dashRowsMsg` names against `internal/nav/types.go` / `model.go`; `newSession`/`assertGolden`/`s.frame` are the harness helpers in `navtest_test.go`. If `dashRowsMsg` triggers async detail/dirty cmds that would touch git in `s.send`, ignore the returned cmd — `send` records but does not auto-resolve it, so the frame is rendered from the message alone.)

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/nav/ -run TestFlow_Dashboard_BaseRowPinned_Golden -v`
Expected: FAIL — the golden file is missing.

- [ ] **Step 3: Generate the golden, inspect, confirm**

Run: `go test ./internal/nav/ -run TestFlow_Dashboard_BaseRowPinned_Golden -update`
Then: `cat internal/nav/testdata/dash_base_row_pinned.golden` — confirm the base
row renders **first** as `★ main`, the `fix-x` worktree row below it, then the
"+ Create new worktree…" row, with no ANSI escapes. Eyeball it.
Run without `-update`: `go test ./internal/nav/ -run TestFlow_Dashboard_BaseRowPinned_Golden`
Expected: PASS. Confirm only the new golden appeared:
`git status --short internal/nav/testdata/`.

- [ ] **Step 4: Commit**

```bash
git add internal/nav/flow_test.go internal/nav/testdata/dash_base_row_pinned.golden
git commit -m "test(nav): golden — base-checkout row pinned first on the dashboard

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 7: Full verification

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
Expected: no diff (goldens stable; the base-row golden already matches).

- [ ] **Step 3: Lint (best-effort)**

Run: `golangci-lint run ./internal/nav/... ./internal/worktree/...` (if installed). Else note it; `go vet` is the gate.

- [ ] **Step 4: Manual smoke (best-effort — needs a real repo + tmux)**

Run:
```bash
just build
bridge nav   # pick any repo → the dashboard's FIRST row is "★ <branch>"
```
Confirm: the base row is first (above worktrees and "+ Create new worktree…");
`enter` on it opens a session in the repo root; if you also run
`bridge open <repo>` (no `-w`) in a shell, both land in the **same** tmux session
(`tmux ls` shows a single `<repo>` session); the base row shows the live dot/agent
once the session exists. `worktree.Resolve` is not invoked (no new `.worktrees/main`
appears).

- [ ] **Step 5: Report**

Report Steps 1-2 output + the Step 4 smoke result. No success claims without output.

---

## Notes for the implementer

- **Reuse, don't add, a launch path.** The base row is an ordinary `dashRow`
  with `worktree == ""` and `path == repo.Path`; `launchPlan`
  (`update.go:648-679`) already turns that into the bare `<repo>` slot in the repo
  root. Do **not** add a base-specific branch to `launchRow`/`launchPlan`, and do
  **not** touch `worktree.Resolve`.
- **Pin after sorting.** Prepend the base row **after** `sortDashRows`, never
  inside it, so it can't be reordered by last-accessed recency.
- **Slot id is the contract.** `core.SlotID(repo.Name, "") == "<repo>"` is what
  ties the nav base row to the shell `bridge open <repo>` session
  (`cmd/bridge/preflight.go:275-287` registers the same id). Keep the base row's
  `worktree` empty — that is what produces the bare id.
- **Navigation is unaffected:** `updateDashWorktrees` already sizes on
  `len(m.dashRows)+1` (the "+ create" row), so the extra pinned row needs no
  index math changes; the base row is simply `m.dashRows[0]`.
- **Ignore the `Primary` error** in `loadDashRowsCmd` exactly like the adjacent
  `worktree.List` — a non-git path degrades to `★ <repo-name>`, it does not break
  the dashboard.
- If you hit a blocker, find the fix and note it inline here.
