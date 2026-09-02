# Nav Dashboard Height Stability Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Stop the `bridge nav` dashboard's two-column frame from resizing ("bouncing") when the user moves the cursor across the worktree list, by deriving its shared panel height from terminal size + fixed chrome instead of from the currently-selected worktree's detail content.

**Architecture:** `viewDash()` (`internal/nav/view.go`) currently probes both columns' *natural* (content-dependent) height and stretches both to the taller one. Replace that probe with a height budget computed once from `m.height` minus the already-rendered `header` and `hint` line heights, and pass that fixed budget straight into both columns — no natural-height probe at all.

**Tech Stack:** Go, `charmbracelet/lipgloss`/`bubbletea`, stdlib `testing` with this package's hand-rolled `Model`-driven tests (`internal/nav/view_test.go`) — no `teatest`.

**Spec:** [`docs/superpowers/specs/2026-09-02-nav-dashboard-height-stability-design.md`](../specs/2026-09-02-nav-dashboard-height-stability-design.md)

## Global Constraints

- `gofmt -l .`, `go vet ./...`, `golangci-lint run`, and `go test -race ./...` must stay green (per this stack's Testing rules — run the full `internal/nav` suite, not just the new test).
- No `teatest` — this package's existing pattern is the hand-rolled harness / direct `Model` construction already in `internal/nav/view_test.go` and `navtest_test.go`. Follow it.
- Don't touch `detailColumn`'s/`backlogColumn`'s internal row-budget math (`per := (m.height-14)/3`) — only `viewDash()`'s outer height computation changes.
- Don't change the narrow single-column layout (`w < dashTwoColMin`) — out of scope.

---

### Task 1: Add a failing test proving the frame height changes with selection

**Files:**
- Modify: `internal/nav/view_test.go` (add a new test function; same file already imports `lipgloss` and `core`)

**Interfaces:**
- Consumes: `initialModel(Config{})`, `Model.View()`, `dashRow{worktree, branch, path}`, `worktreeDetails{branches, commits, status, branchesState, commitsState, statusState}`, `branchInfo{name, current}`, `commitInfo{sha, subject}`, `statusFile{code, path}`, `loadOK` — all already defined in `internal/nav/types.go`.
- Produces: nothing consumed by later tasks; this is the regression test the fix in Task 2 must satisfy.

- [ ] **Step 1: Write the failing test**

Add to `internal/nav/view_test.go`:

```go
func TestView_Dash_FrameHeightStable_AcrossWorktreeSelection(t *testing.T) {
	m := initialModel(Config{})
	m.width, m.height = 120, 40
	m.screen = screenDash
	m.repo = core.Repo{Name: "bridge"}
	m.dashRows = []dashRow{
		{worktree: "sparse", branch: "wt-sparse", path: "/r/sparse"},
		{worktree: "busy", branch: "wt-busy", path: "/r/busy"},
	}
	m.details = map[string]*worktreeDetails{
		"/r/sparse": {
			branches:      []branchInfo{{name: "main", current: true}},
			commits:       []commitInfo{{sha: "abc1234", subject: "init"}},
			branchesState: loadOK,
			commitsState:  loadOK,
			statusState:   loadOK,
		},
		"/r/busy": {
			branches: []branchInfo{
				{name: "main"}, {name: "wt-busy", current: true}, {name: "wt-a"},
				{name: "wt-b"}, {name: "wt-c"}, {name: "wt-d"},
			},
			commits: []commitInfo{
				{sha: "aaa1111", subject: "one"}, {sha: "bbb2222", subject: "two"},
				{sha: "ccc3333", subject: "three"}, {sha: "ddd4444", subject: "four"},
			},
			status: []statusFile{
				{code: "M ", path: "a.go"}, {code: "??", path: "b.go"}, {code: "M ", path: "c.go"},
			},
			branchesState: loadOK,
			commitsState:  loadOK,
			statusState:   loadOK,
		},
	}

	m.dashSel = 0
	sparse := lipgloss.Height(m.View())

	m.dashSel = 1
	busy := lipgloss.Height(m.View())

	if sparse != busy {
		t.Errorf("dashboard frame height changed with selection: sparse-worktree=%d lines, busy-worktree=%d lines", sparse, busy)
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/nav -run TestView_Dash_FrameHeightStable_AcrossWorktreeSelection -v`

Expected: FAIL, with the two heights differing (the busy worktree's extra branches/commits/status rows grow `detailColumn`'s natural height and therefore `h`).

- [ ] **Step 3: Commit**

```bash
git add internal/nav/view_test.go
git commit -m "test(nav): show dashboard frame height varies with worktree selection"
```

---

### Task 2: Fix `viewDash()` to derive height from terminal size, not selection content

**Files:**
- Modify: `internal/nav/view.go:309-358` (`viewDash`)

**Interfaces:**
- Consumes: `m.height` (`int`), `header string` (already computed), `m.hintLine(string) string`, `panelH(w, h int, title, body string) string`, `rightAt func(h int) string` — all already present in this function.
- Produces: no new exported names; `viewDash()`'s signature and return type (`string`) are unchanged, so nothing outside this function needs updating.

- [ ] **Step 1: Move `hint` above `body` and replace the two-pass height probe**

Replace the body of `viewDash()` (`internal/nav/view.go:309-358`) with:

```go
func (m Model) viewDash() string {
	w := m.width
	headerTitle := "bridge nav · " + m.repo.Name
	if n := m.repoIssueCount(); n > 0 {
		headerTitle += "  " + stWarn.Render(fmt.Sprintf("●%d open", n))
	}
	if len(m.notes) > 0 {
		headerTitle += "  " + stAccent.Render("✎ "+m.notesNames())
	}
	header := panel(w, headerTitle, stMuted.Render(m.repo.Path))
	hint := m.hintLine("↑↓ move · tab panes · ⏎ attach/launch · n new worktree · ? legend · esc back · q quit")

	var body string
	if w < dashTwoColMin {
		// Narrow layout has no detail column, so stack the three backlog panes
		// below the worktree list. They are always shown (with placeholders) so
		// the layout is stable and a missing TODO.md is visible.
		parts := []string{
			panel(w, "Sessions & Worktrees", m.dashListBody(false)),
			m.issuesPanel(w, 0),
			m.ideasPanel(w, 0),
			m.todosPanel(w, 0),
		}
		body = strings.Join(parts, "\n")
	} else {
		leftW := clampInt(w*5/12, 40, 64)
		rightW := w - leftW
		listBody := m.dashListBody(true)
		// Right column has two modes: the selected worktree's Details when the
		// worktree list is focused, otherwise the three stacked backlog panes.
		rightAt := func(h int) string {
			if m.dashFocus == dashFocusWorktrees {
				return m.detailColumn(rightW, h)
			}
			return m.backlogColumn(rightW, h)
		}
		// Height budget from terminal size + fixed chrome (header/hint), NOT from
		// either column's natural content height — detailColumn's natural height
		// depends on the *selected* worktree's branch/commit/status data, which
		// would otherwise make the frame resize every time the cursor moves.
		h := m.height - lipgloss.Height(header) - lipgloss.Height(hint)
		if h < 3 {
			h = 3
		}
		left := panelH(leftW, h, "Sessions & Worktrees", listBody)
		body = lipgloss.JoinHorizontal(lipgloss.Top, left, rightAt(h))
	}

	out := header + "\n" + body + "\n" + hint
	if m.modal != nil {
		out += "\n" + m.viewModal()
	}
	return out
}
```

The only behavioral change: `h` is now `m.height - lipgloss.Height(header) - lipgloss.Height(hint)` (clamped to a minimum of 3) instead of `max(lipgloss.Height(panel(...)), lipgloss.Height(rightAt(0)))`. `hint` is unchanged in content, only moved earlier so its height is available before `h` is computed.

- [ ] **Step 2: Run the new test to verify it passes**

Run: `go test ./internal/nav -run TestView_Dash_FrameHeightStable_AcrossWorktreeSelection -v`

Expected: PASS.

- [ ] **Step 3: Run the full `internal/nav` suite**

Run: `go test -race ./internal/nav/...`

Expected: PASS. If any golden-file test (`assertGolden`) fails because the dashboard's rendered height changed shape, inspect the diff — a shift is expected (the frame now always fills to the height budget rather than shrinking to fit sparse content) — and regenerate with `go test ./internal/nav -update` only after confirming the new golden output looks correct (fixed height, no broken borders), then re-run without `-update` to confirm it's green.

- [ ] **Step 4: Run static checks**

Run: `gofmt -l . && go vet ./... && golangci-lint run`

Expected: no output from `gofmt -l .`; `go vet` and `golangci-lint run` clean.

- [ ] **Step 5: Commit**

```bash
git add internal/nav/view.go
git commit -m "fix(nav): derive dashboard frame height from terminal size, not selection"
```

---

### Task 3: Full-repo verification

**Files:** none (verification only)

**Interfaces:** none

- [ ] **Step 1: Run the full test suite with race detector**

Run: `go test -race ./...`

Expected: PASS, no regressions outside `internal/nav`.

- [ ] **Step 2: Manual smoke test**

Run: `bridge nav` against the `bridge` repo itself (it has multiple worktrees with differing branch/commit counts). Open the dashboard, arrow up/down through the worktree list, and confirm the two-column frame's outer border stays at a fixed height while only the inner detail panels' content changes.

- [ ] **Step 3: Commit if any follow-up fixes were needed**

```bash
git add -A
git commit -m "fix(nav): follow-up from manual dashboard verification"
```

(Skip this step if Task 2 already left the tree clean and the manual check found nothing to fix.)
