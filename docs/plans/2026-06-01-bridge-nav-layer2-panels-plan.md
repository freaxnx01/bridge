# `bridge nav` Layer-2 Dashboard Panels — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add three read-only detail panels — **Branches**, **Recent commits**, **Git status** — to the `bridge nav` Screen-2 dashboard, bound to the highlighted worktree, laid out master-detail beside the existing worktree list, loaded lazily and cached per worktree, with a narrow-terminal fallback to today's list-only view.

**Architecture:** Purely additive to `internal/nav`. New per-worktree git Cmds (`tea.Cmd` → `tea.Msg`) feed a `details map[string]*worktreeDetails` cache on the model. Moving the dashboard cursor triggers a deduped, cached load for the newly selected worktree; the cache is cleared whenever rows reload (dash entry + detach-return) so panels refresh. The view splits into two columns above a width threshold and renders exactly as today below it. `Update` stays a pure function of `(model, msg)`; no I/O in `View`.

**Tech Stack:** Go, `charmbracelet/bubbletea` (Model-Update-View), `lipgloss` (`JoinHorizontal`/`JoinVertical`), `bubbles/spinner`. Stdlib `testing`, table-driven, hand-rolled — no testify.

**Spec:** [`docs/specs/2026-06-01-bridge-nav-layer2-panels-design.md`](../specs/2026-06-01-bridge-nav-layer2-panels-design.md)

**Conventions for every task:** run `gofmt -w` on touched files; final gate is `go test -race ./...`, `go vet ./...`, `golangci-lint run`. Commit messages are Conventional Commits; do not push until the user asks. All paths below are relative to the repo root.

---

## File structure

All files already exist; every change is a modification.

- `internal/nav/types.go` — add `branchInfo`, `commitInfo`, `statusFile`, `worktreeDetails`, the three new `*Msg` types, and the `details` field on `Model`. *(Model is in `model.go`; the field is added there — see Task 4.)*
- `internal/nav/format.go` (+ `format_test.go`) — pure parsers `parseBranches`, `parseCommits`, `parseStatusFiles`.
- `internal/nav/data.go` — Cmd constructors `gitBranchesCmd`, `gitCommitsCmd`, `gitStatusCmd`.
- `internal/nav/model.go` — initialise `details` map in `initialModel`.
- `internal/nav/update.go` (+ `update_test.go`) — `selectedWorktreePath`, `ensureDetails`, navigation-tail + `dashRowsMsg` wiring, three new msg cases.
- `internal/nav/view.go` (+ `view_test.go`) — master-detail `viewDash`, the per-panel renderers, the narrow fallback, footer text.
- `README.md`, `CHANGELOG.md`.

Task order respects dependencies: types → parsers → Cmds → reducer → view → docs. After Task 1 the package still compiles (unused types are fine in Go only if referenced; see Task 1 Step 2 note). Tasks 2–6 each end green.

---

## Task 1: Types, messages, and the cache field

**Files:**
- Modify: `internal/nav/types.go`
- Modify: `internal/nav/model.go`

- [ ] **Step 1: Add the value + detail types to `types.go`**

Insert after the `dirtyInfo` struct (after line 70, before `newWorktreeModal`):

```go
// branchInfo is one row of the Branches panel. current marks the selected
// worktree's HEAD ("*" in `git branch`); inWorktree marks a branch checked out
// in some worktree ("+"), the across-worktrees overview signal.
type branchInfo struct {
	name       string
	current    bool
	inWorktree bool
}

// commitInfo is one Recent-commits row: short SHA + subject.
type commitInfo struct {
	sha     string
	subject string
}

// statusFile is one Git-status row: the two-char porcelain XY code + path.
type statusFile struct {
	code string
	path string
}

// worktreeDetails is the lazily-loaded, cached panel data for one worktree,
// keyed by worktree path in Model.details. The zero value has every panel in
// loadPending (loadState's zero value), which is what ensureDetails relies on.
type worktreeDetails struct {
	branches      []branchInfo
	commits       []commitInfo
	status        []statusFile
	branchesState loadState
	commitsState  loadState
	statusState   loadState
}
```

- [ ] **Step 2: Add the three messages to `types.go`**

Insert after `type execDoneMsg struct{ err error }` / `type slotRegisteredMsg struct{}` (end of the messages block, after line 121):

```go
type branchesMsg struct {
	path     string
	branches []branchInfo
	err      error
}
type commitsMsg struct {
	path    string
	commits []commitInfo
	err     error
}
type statusMsg struct {
	path  string
	files []statusFile
	err   error
}
```

- [ ] **Step 3: Add the `details` field to `Model` in `model.go`**

In `internal/nav/model.go`, add the field to the `Model` struct, immediately after the `modal *newWorktreeModal` line (line 30):

```go
	repo     core.Repo
	dashRows []dashRow
	dashSel  int
	modal    *newWorktreeModal
	details  map[string]*worktreeDetails // per-worktree panel cache, keyed by path

	status string
```

- [ ] **Step 4: Initialise `details` in `initialModel`**

In `initialModel` (model.go), add `details` to the returned struct literal, after `filter: ti,`:

```go
	return Model{
		cfg:         cfg,
		spin:        sp,
		screen:      screenPicker,
		pickerFocus: focusFilter,
		filter:      ti,
		details:     map[string]*worktreeDetails{},
		remoteState: loadPending,
		status:      "ready",
	}
```

- [ ] **Step 5: Verify it compiles**

Run: `go build ./internal/nav/`
Expected: builds. The new types are referenced only after later tasks; Go does **not** error on unused package-level types or struct fields, so this compiles cleanly on its own.

- [ ] **Step 6: Commit**

```bash
gofmt -w internal/nav/types.go internal/nav/model.go
git add internal/nav/types.go internal/nav/model.go
git commit -m "feat(nav): add Layer-2 panel types, messages, and per-worktree cache field"
```

---

## Task 2: Pure parsers (`parseBranches`, `parseCommits`, `parseStatusFiles`)

**Files:**
- Modify: `internal/nav/format.go`
- Test: `internal/nav/format_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `internal/nav/format_test.go`:

```go
func TestParseBranches_MarkersAndOrder(t *testing.T) {
	// `git branch --sort=-committerdate` output: "* " current, "+ " in another
	// worktree, "  " plain. Order is preserved.
	out := "* worktree-fix-x\n+ worktree-docs\n  main\n"
	got := parseBranches(out)
	want := []branchInfo{
		{name: "worktree-fix-x", current: true},
		{name: "worktree-docs", inWorktree: true},
		{name: "main"},
	}
	if len(got) != len(want) {
		t.Fatalf("parseBranches len = %d, want %d (%+v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("parseBranches[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestParseBranches_Empty(t *testing.T) {
	if got := parseBranches(""); len(got) != 0 {
		t.Errorf("parseBranches(\"\") = %+v, want empty", got)
	}
}

func TestParseCommits_ShaAndSubject(t *testing.T) {
	// `git log --format=%h%x00%s`: short-sha NUL subject, one per line.
	out := "a1b2c3d\x00fix login parsing\ne4f5g6h\x00wip nav panels\n"
	got := parseCommits(out)
	want := []commitInfo{
		{sha: "a1b2c3d", subject: "fix login parsing"},
		{sha: "e4f5g6h", subject: "wip nav panels"},
	}
	if len(got) != len(want) {
		t.Fatalf("parseCommits len = %d, want %d (%+v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("parseCommits[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestParseCommits_Empty(t *testing.T) {
	if got := parseCommits(""); len(got) != 0 {
		t.Errorf("parseCommits(\"\") = %+v, want empty", got)
	}
}

func TestParseStatusFiles_CodesAndRename(t *testing.T) {
	// `git status --porcelain`: 2-char XY code, a space, then the path; a rename
	// renders "old -> new" — keep the new path.
	out := " M internal/nav/view.go\n?? scratch.txt\nR  old.go -> new.go\n"
	got := parseStatusFiles(out)
	want := []statusFile{
		{code: " M", path: "internal/nav/view.go"},
		{code: "??", path: "scratch.txt"},
		{code: "R ", path: "new.go"},
	}
	if len(got) != len(want) {
		t.Fatalf("parseStatusFiles len = %d, want %d (%+v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("parseStatusFiles[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestParseStatusFiles_Clean(t *testing.T) {
	if got := parseStatusFiles(""); len(got) != 0 {
		t.Errorf("parseStatusFiles(\"\") = %+v, want empty (clean)", got)
	}
}
```

- [ ] **Step 2: Run tests, verify they fail**

Run: `go test ./internal/nav -run 'TestParseBranches|TestParseCommits|TestParseStatusFiles' -v`
Expected: FAIL — `undefined: parseBranches` / `parseCommits` / `parseStatusFiles`.

- [ ] **Step 3: Implement the parsers**

Append to `internal/nav/format.go` (the import block already has `strings`; no new imports needed):

```go
// parseBranches parses `git branch --sort=-committerdate` output. Each line's
// first two columns are the marker: "* " current HEAD, "+ " checked out in
// another worktree, "  " plain. Input order is preserved.
func parseBranches(out string) []branchInfo {
	var rows []branchInfo
	for _, l := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if l == "" {
			continue
		}
		var b branchInfo
		switch {
		case strings.HasPrefix(l, "* "):
			b.current = true
		case strings.HasPrefix(l, "+ "):
			b.inWorktree = true
		}
		b.name = strings.TrimSpace(l[min(2, len(l)):])
		rows = append(rows, b)
	}
	return rows
}

// parseCommits parses `git log --format=%h%x00%s` output: one commit per line,
// short SHA and subject separated by a NUL byte.
func parseCommits(out string) []commitInfo {
	var rows []commitInfo
	for _, l := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if l == "" {
			continue
		}
		sha, subject, _ := strings.Cut(l, "\x00")
		rows = append(rows, commitInfo{sha: sha, subject: subject})
	}
	return rows
}

// parseStatusFiles parses `git status --porcelain` output: a 2-char XY code, a
// space, then the path. Rename lines carry "old -> new"; the new path is kept.
func parseStatusFiles(out string) []statusFile {
	var rows []statusFile
	for _, l := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if len(l) < 3 {
			continue
		}
		code := l[:2]
		path := l[3:]
		if _, after, ok := strings.Cut(path, " -> "); ok {
			path = after
		}
		rows = append(rows, statusFile{code: code, path: path})
	}
	return rows
}
```

Note: `min` is a Go 1.21+ builtin; the repo's `go.mod` pins ≥1.21, so no helper is needed. If `go vet` flags `min` as undefined, the toolchain is older than expected — stop and report rather than adding a local `min`.

- [ ] **Step 4: Run tests, verify they pass**

Run: `go test ./internal/nav -run 'TestParseBranches|TestParseCommits|TestParseStatusFiles' -v`
Expected: PASS (all six).

- [ ] **Step 5: Commit**

```bash
gofmt -w internal/nav/format.go internal/nav/format_test.go
git add internal/nav/format.go internal/nav/format_test.go
git commit -m "feat(nav): parse git branch/log/status output for detail panels"
```

---

## Task 3: Data Cmds (`gitBranchesCmd`, `gitCommitsCmd`, `gitStatusCmd`)

**Files:**
- Modify: `internal/nav/data.go`

Thin `git -C <path> …` adapters whose pure cores (the parsers) are already tested; exercised end-to-end by the `--once` smoke + manual TTY run.

- [ ] **Step 1: Add the three Cmds**

Append to `internal/nav/data.go` (imports `os/exec` is already present; no new import needed):

```go
// gitBranchesCmd lists the repo's branches (most-recent committerdate first) for
// the Branches panel, marking the current ("*") and worktree-occupied ("+")
// branches. Runs against the worktree path so "*" reflects that checkout's HEAD.
func gitBranchesCmd(path string) tea.Cmd {
	return func() tea.Msg {
		out, err := exec.Command("git", "-C", path, "branch", "--sort=-committerdate").Output()
		if err != nil {
			return branchesMsg{path: path, err: err}
		}
		return branchesMsg{path: path, branches: parseBranches(string(out))}
	}
}

// gitCommitsCmd reads the worktree HEAD's recent commits for the Recent-commits
// panel. The fixed -n cap bounds output; the view truncates to fit.
func gitCommitsCmd(path string) tea.Cmd {
	return func() tea.Msg {
		out, err := exec.Command("git", "-C", path, "log", "--format=%h%x00%s", "-n", "20").Output()
		if err != nil {
			// A repo with no commits also errors here; render it as "unavailable"
			// rather than guessing — fresh worktrees always have commits.
			return commitsMsg{path: path, err: err}
		}
		return commitsMsg{path: path, commits: parseCommits(string(out))}
	}
}

// gitStatusCmd reads the worktree's changed-file list for the Git-status panel.
// Distinct from gitDirtyCmd, which only counts files for the per-row indicator.
func gitStatusCmd(path string) tea.Cmd {
	return func() tea.Msg {
		out, err := exec.Command("git", "-C", path, "status", "--porcelain").Output()
		if err != nil {
			return statusMsg{path: path, err: err}
		}
		return statusMsg{path: path, files: parseStatusFiles(string(out))}
	}
}
```

- [ ] **Step 2: Verify it compiles**

Run: `go build ./internal/nav/`
Expected: builds.

- [ ] **Step 3: Commit**

```bash
gofmt -w internal/nav/data.go
git add internal/nav/data.go
git commit -m "feat(nav): add git branches/commits/status Cmds for detail panels"
```

---

## Task 4: Reducer wiring — lazy load, cache, and fill

**Files:**
- Modify: `internal/nav/update.go`
- Test: `internal/nav/update_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `internal/nav/update_test.go` (the file already imports `tea "github.com/charmbracelet/bubbletea"`; add nothing if so):

```go
func TestUpdateDash_MoveSelection_FiresDetailLoadAndSeedsPending(t *testing.T) {
	m := initialModel(Config{})
	m.screen = screenDash
	m.dashRows = []dashRow{
		{worktree: "fix-x", path: "/r/.worktrees/fix-x"},
		{worktree: "docs", path: "/r/.worktrees/docs"},
	}
	m.dashSel = 0
	out, cmd := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	got := out.(Model)
	if got.dashSel != 1 {
		t.Fatalf("dashSel = %d, want 1", got.dashSel)
	}
	d, ok := got.details["/r/.worktrees/docs"]
	if !ok {
		t.Fatalf("expected a pending cache entry for the newly selected worktree")
	}
	if d.branchesState != loadPending || d.commitsState != loadPending || d.statusState != loadPending {
		t.Errorf("new cache entry should be all loadPending, got %+v", d)
	}
	if cmd == nil {
		t.Errorf("moving to an uncached worktree should return a load Cmd")
	}
}

func TestUpdateDash_MoveToCachedWorktree_NoRefire(t *testing.T) {
	m := initialModel(Config{})
	m.screen = screenDash
	m.dashRows = []dashRow{
		{worktree: "fix-x", path: "/r/.worktrees/fix-x"},
		{worktree: "docs", path: "/r/.worktrees/docs"},
	}
	m.dashSel = 0
	m.details["/r/.worktrees/docs"] = &worktreeDetails{branchesState: loadOK}
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if cmd != nil {
		t.Errorf("moving to a cached worktree should not refire a load Cmd")
	}
}

func TestUpdateDash_CreateRowSelected_NoLoad(t *testing.T) {
	m := initialModel(Config{})
	m.screen = screenDash
	m.dashRows = []dashRow{{worktree: "fix-x", path: "/r/.worktrees/fix-x"}}
	m.dashSel = 0
	// Down wraps from the single worktree row onto the "+ create" row (index 1).
	out, cmd := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	got := out.(Model)
	if got.dashSel != 1 {
		t.Fatalf("dashSel = %d, want 1 (the create row)", got.dashSel)
	}
	if cmd != nil {
		t.Errorf("the create row has no worktree, so no load Cmd should fire")
	}
}

func TestUpdate_BranchesMsg_FillsCache(t *testing.T) {
	m := initialModel(Config{})
	m.details["/r/x"] = &worktreeDetails{}
	out, _ := m.Update(branchesMsg{path: "/r/x", branches: []branchInfo{{name: "main"}}})
	got := out.(Model)
	d := got.details["/r/x"]
	if d.branchesState != loadOK || len(d.branches) != 1 {
		t.Errorf("branchesMsg not applied: %+v", d)
	}
}

func TestUpdate_StatusMsgErr_SetsErrState(t *testing.T) {
	m := initialModel(Config{})
	m.details["/r/x"] = &worktreeDetails{}
	out, _ := m.Update(statusMsg{path: "/r/x", err: errFake})
	got := out.(Model)
	if got.details["/r/x"].statusState != loadErr {
		t.Errorf("statusMsg error should set loadErr, got %+v", got.details["/r/x"])
	}
}

func TestUpdate_CommitsMsg_EvictedPath_Ignored(t *testing.T) {
	m := initialModel(Config{})
	// No entry for "/gone" — a late msg for an evicted worktree must be a no-op.
	out, _ := m.Update(commitsMsg{path: "/gone", commits: []commitInfo{{sha: "a"}}})
	got := out.(Model)
	if _, ok := got.details["/gone"]; ok {
		t.Errorf("a msg for an evicted path must not create a cache entry")
	}
}

func TestUpdate_DashRowsMsg_ClearsCacheAndLoadsSelection(t *testing.T) {
	m := initialModel(Config{})
	m.screen = screenDash
	m.details["/stale"] = &worktreeDetails{branchesState: loadOK}
	out, cmd := m.Update(dashRowsMsg{rows: []dashRow{{worktree: "fix-x", path: "/r/fix-x"}}})
	got := out.(Model)
	if _, ok := got.details["/stale"]; ok {
		t.Errorf("dashRowsMsg should clear the stale cache")
	}
	if _, ok := got.details["/r/fix-x"]; !ok {
		t.Errorf("dashRowsMsg should seed a load for the current selection")
	}
	if cmd == nil {
		t.Errorf("dashRowsMsg should return Cmds (dirty + detail load)")
	}
}
```

`errFake` already exists in `update_test.go` (used by `TestUpdate_RemoteErrMsg_*`). If it does not, add at the bottom of the file:

```go
var errFake = fakeErr("boom")

type fakeErr string

func (e fakeErr) Error() string { return string(e) }
```

- [ ] **Step 2: Run tests, verify they fail**

Run: `go test ./internal/nav -run 'TestUpdateDash_Move|TestUpdateDash_CreateRow|TestUpdate_BranchesMsg|TestUpdate_StatusMsgErr|TestUpdate_CommitsMsg_Evicted|TestUpdate_DashRowsMsg_Clears' -v`
Expected: FAIL — `ensureDetails`/`selectedWorktreePath` undefined and the new msg cases unhandled.

- [ ] **Step 3: Add `selectedWorktreePath` and `ensureDetails` to `update.go`**

Append to `internal/nav/update.go` (no new imports — `tea` is already imported):

```go
// selectedWorktreePath is the path of the highlighted worktree row, or "" when
// the trailing "+ Create new worktree…" row is selected (no worktree).
func (m Model) selectedWorktreePath() string {
	if m.dashSel < 0 || m.dashSel >= len(m.dashRows) {
		return ""
	}
	return m.dashRows[m.dashSel].path
}

// ensureDetails kicks an async load of the highlighted worktree's three detail
// panels when its data isn't cached yet. A cache hit (entry already present, any
// state) or the "+ create" row returns no Cmd. The new entry's zero-value
// loadStates are loadPending, so the view shows spinners until the msgs land.
func (m Model) ensureDetails() (Model, tea.Cmd) {
	path := m.selectedWorktreePath()
	if path == "" {
		return m, nil
	}
	if m.details == nil {
		m.details = map[string]*worktreeDetails{}
	}
	if _, ok := m.details[path]; ok {
		return m, nil
	}
	m.details[path] = &worktreeDetails{}
	return m, tea.Batch(
		gitBranchesCmd(path),
		gitCommitsCmd(path),
		gitStatusCmd(path),
	)
}
```

- [ ] **Step 4: Wire the navigation tail in `updateDash`**

In `internal/nav/update.go`, change the final `return` of `updateDash` (currently line 285, `return m, nil`, immediately after the closing `}` of the `switch`) to:

```go
	return m.ensureDetails()
```

This fires a deduped detail load after any key that changed `m.dashSel` (the `up/k`, `down/j`, `home/g`, `end/G`, `pgup`, `pgdown` cases all fall through to it). The `n`, `enter`, `esc`, and quit cases `return` earlier and are unaffected.

- [ ] **Step 5: Clear the cache and load the selection on `dashRowsMsg`**

In `Update` (update.go), replace the existing `dashRowsMsg` case (currently lines 38–43):

```go
	case dashRowsMsg:
		m.dashRows = msg.rows
		if m.dashSel >= len(m.dashRows) {
			m.dashSel = 0
		}
		return m, m.dirtyCmds()
```

with:

```go
	case dashRowsMsg:
		m.dashRows = msg.rows
		if m.dashSel >= len(m.dashRows) {
			m.dashSel = 0
		}
		m.details = map[string]*worktreeDetails{} // fresh rows -> reload panels
		m, detailCmd := m.ensureDetails()
		return m, tea.Batch(m.dirtyCmds(), detailCmd)
```

This is the single choke point for cache invalidation: dash entry, detach-return, and create-worktree all refresh through `loadDashRowsCmd` → `dashRowsMsg`.

- [ ] **Step 6: Add the three msg cases**

In `Update` (update.go), add these cases after the existing `dirtyMsg` case (after its `return m, nil`, around line 55):

```go
	case branchesMsg:
		if d := m.details[msg.path]; d != nil {
			if msg.err != nil {
				d.branchesState = loadErr
			} else {
				d.branches = msg.branches
				d.branchesState = loadOK
			}
		}
		return m, nil
	case commitsMsg:
		if d := m.details[msg.path]; d != nil {
			if msg.err != nil {
				d.commitsState = loadErr
			} else {
				d.commits = msg.commits
				d.commitsState = loadOK
			}
		}
		return m, nil
	case statusMsg:
		if d := m.details[msg.path]; d != nil {
			if msg.err != nil {
				d.statusState = loadErr
			} else {
				d.status = msg.files
				d.statusState = loadOK
			}
		}
		return m, nil
```

`d` is a `*worktreeDetails`; mutating through the pointer updates the shared map even though `m` is a value copy. A msg whose `path` is no longer in the map is dropped (the `d != nil` guard).

- [ ] **Step 7: Run tests, verify they pass**

Run: `go test ./internal/nav -run 'TestUpdateDash_Move|TestUpdateDash_CreateRow|TestUpdate_BranchesMsg|TestUpdate_StatusMsgErr|TestUpdate_CommitsMsg_Evicted|TestUpdate_DashRowsMsg_Clears' -v`
Expected: PASS. Then `go test ./internal/nav/...` — whole package still green.

- [ ] **Step 8: Commit**

```bash
gofmt -w internal/nav/update.go internal/nav/update_test.go
git add internal/nav/update.go internal/nav/update_test.go
git commit -m "feat(nav): lazy-load + cache per-worktree detail panels in the reducer"
```

---

## Task 5: View — master-detail layout with narrow fallback

**Files:**
- Modify: `internal/nav/view.go`
- Test: `internal/nav/view_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `internal/nav/view_test.go` (it already imports `strings`, `testing`, and `core`; reuse them):

```go
func TestViewDash_Wide_ShowsDetailPanels(t *testing.T) {
	m := initialModel(Config{})
	m.width, m.height = 130, 40
	m.screen = screenDash
	m.repo = core.Repo{Name: "bridge"}
	m.dashRows = []dashRow{{worktree: "fix-x", branch: "worktree-fix-x", path: "/r/fix-x"}}
	m.dashSel = 0
	m.details["/r/fix-x"] = &worktreeDetails{
		branches:      []branchInfo{{name: "worktree-fix-x", current: true}, {name: "main"}},
		commits:       []commitInfo{{sha: "a1b2c3d", subject: "fix login"}},
		status:        []statusFile{{code: " M", path: "internal/nav/view.go"}},
		branchesState: loadOK, commitsState: loadOK, statusState: loadOK,
	}
	out := m.View()
	for _, want := range []string{"Branches", "Recent commits", "Git status", "fix login", "a1b2c3d"} {
		if !strings.Contains(out, want) {
			t.Errorf("wide dash view missing %q\n%s", want, out)
		}
	}
}

func TestViewDash_Narrow_FallsBackToListOnly(t *testing.T) {
	m := initialModel(Config{})
	m.width, m.height = 80, 30 // below dashTwoColMin
	m.screen = screenDash
	m.repo = core.Repo{Name: "bridge"}
	m.dashRows = []dashRow{{worktree: "fix-x", branch: "worktree-fix-x", path: "/r/fix-x"}}
	out := m.View()
	if !strings.Contains(out, "Sessions & Worktrees") {
		t.Errorf("narrow dash should still show the worktree list")
	}
	for _, absent := range []string{"Recent commits", "Git status"} {
		if strings.Contains(out, absent) {
			t.Errorf("narrow dash should not render the %q panel", absent)
		}
	}
}

func TestViewDash_CreateRowSelected_ShowsHint(t *testing.T) {
	m := initialModel(Config{})
	m.width, m.height = 130, 40
	m.screen = screenDash
	m.repo = core.Repo{Name: "bridge"}
	m.dashRows = []dashRow{{worktree: "fix-x", path: "/r/fix-x"}}
	m.dashSel = 1 // the "+ create" row
	out := m.View()
	if !strings.Contains(out, "select a worktree") {
		t.Errorf("create-row selection should show the select-a-worktree hint\n%s", out)
	}
}
```

- [ ] **Step 2: Run tests, verify they fail**

Run: `go test ./internal/nav -run 'TestViewDash_Wide|TestViewDash_Narrow|TestViewDash_CreateRow' -v`
Expected: FAIL — the current single-column `viewDash` shows none of the panel titles, and `dashTwoColMin` is undefined.

- [ ] **Step 3: Refactor `viewDash` into master-detail**

In `internal/nav/view.go`, add the `lipgloss` width threshold constant near the style vars (after the `var (...)` block, before `func panel`):

```go
// dashTwoColMin is the minimum terminal width for the master-detail dashboard;
// below it the dashboard renders list-only (today's layout), unchanged.
const dashTwoColMin = 90
```

Replace the body of `viewDash` (lines 124–168) — the worktree-list loop that builds `b` is extracted into `dashListBody` so both layouts share it:

```go
func (m Model) viewDash() string {
	w := m.width
	header := panel(w, "bridge nav · "+m.repo.Name, stMuted.Render(m.repo.Path))

	var body string
	if w < dashTwoColMin {
		body = panel(w, "Sessions & Worktrees", m.dashListBody(false))
	} else {
		leftW := clampInt(w*5/12, 40, 64)
		rightW := w - leftW
		left := panel(leftW, "Sessions & Worktrees", m.dashListBody(true))
		right := m.detailColumn(rightW)
		body = lipgloss.JoinHorizontal(lipgloss.Top, left, right)
	}

	hint := m.hintLine("↑↓ move · g/G first/last · ⏎ attach/launch · n new worktree · esc back · q quit")
	footer := m.hintLine("(later: Open issues · forge statusbar)")

	out := header + "\n" + body + "\n" + hint + "\n" + footer
	if m.modal != nil {
		out += "\n" + m.viewModal()
	}
	return out
}

// dashListBody renders the worktree rows + the "+ create" row. compact drops the
// branch/agent/last-accessed columns so the list fits the narrower left column
// of the two-column layout; the full form is today's single-column layout.
func (m Model) dashListBody(compact bool) string {
	var b strings.Builder
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
		var line string
		if compact {
			line = fmt.Sprintf("%s %-18s %-7s %s", dot, trunc(r.worktree, 18), trunc(agent, 7), m.dirtyView(r))
		} else {
			la := r.lastAccessed
			if !r.hasSession {
				la = "(no session)"
			}
			line = fmt.Sprintf("%s %-18s %-14s %-8s %-12s %s",
				dot, trunc(r.worktree, 18), trunc(r.branch, 14), agent, la, m.dirtyView(r))
		}
		if i == m.dashSel {
			line = stSel.Render(line)
		}
		b.WriteString(line + "\n")
	}
	b.WriteString("\n")
	createLine := "  + Create new worktree…"
	if m.dashSel == len(m.dashRows) {
		createLine = stSel.Render(stAccent.Render("▸ ") + "+ Create new worktree…")
	}
	b.WriteString(createLine)
	return strings.TrimRight(b.String(), "\n")
}
```

- [ ] **Step 4: Add the detail-column renderers**

Append to `internal/nav/view.go`:

```go
// detailColumn renders the three stacked detail panels for the highlighted
// worktree, or a hint when the "+ create" row is selected.
func (m Model) detailColumn(w int) string {
	path := m.selectedWorktreePath()
	if path == "" {
		return panel(w, "Details", stMuted.Render("select a worktree to see its branches, commits & status"))
	}
	per := (m.height - 14) / 3
	if per < 3 {
		per = 3
	}
	d := m.details[path]
	branches := panel(w, "Branches", m.branchesBody(d, per))
	commits := panel(w, "Recent commits", m.commitsBody(d, per))
	status := panel(w, "Git status", m.statusBody(d, per))
	return lipgloss.JoinVertical(lipgloss.Left, branches, commits, status)
}

// panelState renders the spinner/unavailable text shared by the three panels
// while their data is loading or after an error; ok == true means render rows.
func (m Model) panelState(state loadState) (text string, ok bool) {
	switch state {
	case loadPending:
		return m.spin.View() + " loading…", false
	case loadErr:
		return stMuted.Render("unavailable"), false
	default:
		return "", true
	}
}

func (m Model) branchesBody(d *worktreeDetails, max int) string {
	if d == nil {
		return m.spin.View() + " loading…"
	}
	if text, ok := m.panelState(d.branchesState); !ok {
		return text
	}
	if len(d.branches) == 0 {
		return stMuted.Render("(only this worktree)")
	}
	var b strings.Builder
	shown, more := windowList(len(d.branches), max)
	for i := 0; i < shown; i++ {
		br := d.branches[i]
		switch {
		case br.current:
			b.WriteString(stAccent.Render("* " + br.name))
		case br.inWorktree:
			b.WriteString(stOk.Render("+ " + br.name))
		default:
			b.WriteString(stText.Render("  " + br.name))
		}
		b.WriteString("\n")
	}
	if more > 0 {
		b.WriteString(stMuted.Render(fmt.Sprintf("  … +%d more", more)))
	}
	return strings.TrimRight(b.String(), "\n")
}

func (m Model) commitsBody(d *worktreeDetails, max int) string {
	if d == nil {
		return m.spin.View() + " loading…"
	}
	if text, ok := m.panelState(d.commitsState); !ok {
		return text
	}
	if len(d.commits) == 0 {
		return stMuted.Render("(no commits)")
	}
	var b strings.Builder
	shown, more := windowList(len(d.commits), max)
	for i := 0; i < shown; i++ {
		c := d.commits[i]
		b.WriteString(stWarn.Render(c.sha) + " " + stText.Render(c.subject) + "\n")
	}
	if more > 0 {
		b.WriteString(stMuted.Render(fmt.Sprintf("… +%d more", more)))
	}
	return strings.TrimRight(b.String(), "\n")
}

func (m Model) statusBody(d *worktreeDetails, max int) string {
	if d == nil {
		return m.spin.View() + " loading…"
	}
	if text, ok := m.panelState(d.statusState); !ok {
		return text
	}
	if len(d.status) == 0 {
		return stOk.Render("✓ clean")
	}
	var b strings.Builder
	shown, more := windowList(len(d.status), max)
	for i := 0; i < shown; i++ {
		f := d.status[i]
		b.WriteString(stBad.Render(f.code) + " " + stText.Render(f.path) + "\n")
	}
	if more > 0 {
		b.WriteString(stMuted.Render(fmt.Sprintf("… +%d more", more)))
	}
	return strings.TrimRight(b.String(), "\n")
}

// windowList returns how many of n items to show given max rows, reserving one
// row for a "… +N more" line when there is overflow. more is the hidden count.
func windowList(n, max int) (shown, more int) {
	if max < 1 {
		max = 1
	}
	if n <= max {
		return n, 0
	}
	shown = max - 1
	if shown < 1 {
		shown = 1
	}
	return shown, n - shown
}
```

- [ ] **Step 5: Run tests, verify they pass**

Run: `go test ./internal/nav -run 'TestViewDash_Wide|TestViewDash_Narrow|TestViewDash_CreateRow' -v`
Expected: PASS. Then `go test ./internal/nav/...` — whole package green (existing `TestView_*` still pass; the narrow path reproduces today's list-only body).

- [ ] **Step 6: Commit**

```bash
gofmt -w internal/nav/view.go internal/nav/view_test.go
git add internal/nav/view.go internal/nav/view_test.go
git commit -m "feat(nav): render Branches/Recent commits/Git status detail panels"
```

---

## Task 6: Docs, full gates, and manual TTY verification

**Files:**
- Modify: `README.md`, `CHANGELOG.md`

- [ ] **Step 1: README**

Find the `bridge nav` description in `README.md` (search: `grep -n "bridge nav" README.md`). Extend its paragraph with a sentence on the detail panels, e.g.:

```
On the dashboard, the highlighted worktree's Branches, recent commits, and git
status show in panels alongside the list (on terminals ≥90 columns; narrower
terminals show the list only).
```

Match the surrounding prose style; do not restructure the section.

- [ ] **Step 2: CHANGELOG**

Under `[Unreleased]` → `Added` in `CHANGELOG.md`, add:

```
- `bridge nav` dashboard: read-only **Branches**, **Recent commits**, and **Git
  status** panels for the highlighted worktree, in a master-detail layout beside
  the worktree list. Loaded lazily and cached per worktree; refreshed on
  detach-return. Terminals narrower than 90 columns keep the list-only view.
```

If no `[Unreleased]`/`Added` section exists, create it above the latest release per Keep a Changelog.

- [ ] **Step 3: Commit docs**

```bash
git add README.md CHANGELOG.md
git commit -m "docs(nav): document dashboard detail panels in README + CHANGELOG"
```

- [ ] **Step 4: Full gates**

Run:
```bash
gofmt -l internal/nav
go vet ./...
go test -race ./...
golangci-lint run
```
Expected: `gofmt -l` empty, vet clean, all tests pass under `-race`, lint clean. If `golangci-lint` is not installed, say so in the PR rather than skipping silently.

- [ ] **Step 5: Manual TTY verification (no code)**

Build and run interactively — the `--once` smoke can't exercise these paths:

1. `just build && bridge nav`
2. Enter a repo with ≥2 worktrees on a **wide** terminal (≥90 cols). Verify: the worktree list is on the left; Branches / Recent commits / Git status panels on the right show a spinner, then resolve for the **highlighted** worktree.
3. Move the cursor down/up: panels update to the newly highlighted worktree; the first visit shows a brief spinner, revisiting a row is instant (cache hit).
4. Branches panel: the current branch shows `*` (accent); a branch checked out in another worktree shows `+` (green).
5. Select the `+ Create new worktree…` row: the right column shows the "select a worktree…" hint.
6. Attach a session (`⏎`), detach (`Ctrl-b d`): you return to the dashboard and the panels reload (e.g. Git status reflects new changes).
7. Resize the terminal **below 90 columns**: the dashboard collapses to the list-only layout (today's behaviour); widen again to restore the panels.
8. `bridge nav --once | cat`: still prints a picker frame (non-TTY smoke unaffected).

---

## Self-review (done while writing)

- **Spec coverage:** Branches/Recent commits/Git status panels (T2 parsers, T3 Cmds, T5 view); contextual binding + Branches cross-worktree overview via `*`/`+` (T2 `parseBranches`, T5 `branchesBody`); master-detail layout + ~90-col narrow fallback (T5 `viewDash`, `dashTwoColMin`); lazy load + per-worktree cache + clear-on-reload (T4 `ensureDetails`, `dashRowsMsg`); per-panel loading/error/empty states (T5 `panelState` + empty branches); read-only / no new keys (T4 wires only the existing nav tail); footer shrink to `(later: Open issues · forge statusbar)` (T5); docs (T6). Forge panels remain out of scope. Every spec section maps to a task.
- **Type consistency:** `branchInfo`/`commitInfo`/`statusFile`/`worktreeDetails` and `branchesMsg`/`commitsMsg`/`statusMsg` defined once (T1), used unchanged in T3/T4/T5. `ensureDetails` returns `(Model, tea.Cmd)` and is called as `return m.ensureDetails()` (nav tail) and `m, detailCmd := m.ensureDetails()` (dashRowsMsg) — both forms consistent. `selectedWorktreePath` (T4) reused by `detailColumn` (T5). `dashListBody(bool)`, `detailColumn`, `panelState`, `branchesBody`/`commitsBody`/`statusBody`, `windowList` names consistent across T5. `dashTwoColMin` defined once (T5) and referenced in the narrow-fallback test (T5).
- **Placeholder scan:** no TBD/TODO; every code step shows complete code; the `min` builtin and `errFake` reuse are called out explicitly with fallbacks.
- **Reuse/DRY:** `parseDirtyStatus`/`gitDirtyCmd` left intact (counts for the per-row indicator); the new status panel reads the file list separately and does not duplicate that logic. `clampInt`, `trunc`, `panel`, the style vars, and `m.dirtyView` are reused, not reimplemented.
```
