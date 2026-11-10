# nav Detail Column Height Budget — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** On `bridge nav`'s two-column dashboard, the detail column (Branches / Recent commits / Git status) never renders taller than its height budget. The frame is then exactly `m.height` rows for every selected worktree, and the header box stops scrolling off the top (#364).

**Architecture:** Three surgical changes in `internal/nav/view.go`:

- `trunc` becomes rune- and width-safe.
- The three detail bodies clip each line to the panel's inner width, so nothing soft-wraps.
- `detailColumn` derives its per-panel row caps from the column height `minH` instead of `m.height`, and gives Git status exactly the rows that remain.

No new types, no new dependencies.

**Tech Stack:** Go, `charmbracelet/lipgloss`, stdlib `testing` (table-driven, hand-rolled fixtures), golden files under `internal/nav/testdata/`.

**Spec:** `docs/superpowers/specs/2026-11-09-nav-detail-column-height-budget-design.md`

## Global Constraints

- Use Test-Driven Development for every task: write a failing test first, watch it fail, implement minimally to pass, verify green.
- **Scope is `detailColumn` and its three bodies, plus `trunc`.** Do not touch `backlogColumn`, `issuesBody`, `noteFileBody`, `viewDash`'s outer budget (`view.go:371-378`), the narrow layout, or the picker.
- **Truncate raw text before styling.** Never pass an ANSI-styled string to `trunc`.
- `trunc` on pure-ASCII input must return exactly what it returns today.
- No new Go module. No third-party assertion or mocking library.
- Conventional Commits; scope `nav`.
- `gofmt -l .` empty, `go vet ./...` clean, `golangci-lint run` clean, `go test -race ./...` green.

---

## File Structure

- `internal/nav/view.go`: **modify**
  - `trunc` at `:506-514`
  - `detailColumn` at `:516-536`
  - `branchesBody` at `:703`, `commitsBody` at `:733`, `statusBody` at `:755`
- `internal/nav/view_test.go`: **modify** (append). `trunc` table test, frame-height invariant test, truncation test.
- `internal/nav/testdata/*.golden`: **regenerate** if a detail-panel item count changes (likely only `dash_base_row_pinned.golden`).

---

### Task 1: `trunc` never splits a rune

**Files:**
- Modify: `internal/nav/view.go:506-514`
- Test: `internal/nav/view_test.go` (append)

**Interfaces:**
- Consumes/produces: `func trunc(s string, n int) string`. The signature is unchanged.

- [ ] **Step 1: Write the failing test**

Append to `internal/nav/view_test.go`, and add `"unicode/utf8"` to its imports:

```go
func TestTrunc_NeverSplitsARuneOrOverrunsWidth(t *testing.T) {
	cases := []struct {
		name string
		in   string
		n    int
		want string // "" = only check validity and width
	}{
		{"ascii fits", "short", 10, "short"},
		{"ascii clipped", "abcdefghij", 5, "abcd…"},
		{"ascii n=1", "abcdef", 1, "…"},
		{"em dash at cut", "fix — the thing", 6, ""},
		{"accented", "café crème brûlée", 5, "café…"},
		{"wide runes", "日本語のテキストです", 6, "日本…"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := trunc(c.in, c.n)
			if !utf8.ValidString(got) {
				t.Fatalf("trunc(%q, %d) = %q, not valid UTF-8", c.in, c.n, got)
			}
			if w := lipgloss.Width(got); w > c.n {
				t.Errorf("trunc(%q, %d) width = %d, want <= %d", c.in, c.n, w, c.n)
			}
			if c.want != "" && got != c.want {
				t.Errorf("trunc(%q, %d) = %q, want %q", c.in, c.n, got, c.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

```bash
go test ./internal/nav -run TestTrunc_NeverSplitsARuneOrOverrunsWidth -v
```

Expected: FAIL.

- `em dash at cut` (`s[:5]` = `"fix \xe2"`), `accented` (`s[:4]` = `"caf\xc3"`) and `wide runes` (`s[:5]` cuts `本`): today's byte slice ends mid-rune, so the result is not valid UTF-8. The cut points were chosen so each one lands inside a multi-byte rune.
- The three ASCII cases already PASS. They pin today's behaviour.

- [ ] **Step 3: Write the minimal implementation**

Replace `trunc` at `internal/nav/view.go:506-514`:

```go
func trunc(s string, n int) string {
	if lipgloss.Width(s) <= n {
		return s
	}
	if n <= 1 {
		return "…"
	}
	return s[:n-1] + "…"
}
```

with:

```go
// trunc clips plain (unstyled) text to at most n display columns, ending in
// "…" when it cuts. It walks runes, so a multi-byte rune (an em dash in a
// commit subject, an accented letter) is never split and a wide rune never
// overruns n.
func trunc(s string, n int) string {
	if lipgloss.Width(s) <= n {
		return s
	}
	if n <= 1 {
		return "…"
	}
	var b strings.Builder
	used := 0
	for _, r := range s {
		rw := lipgloss.Width(string(r))
		if used+rw > n-1 {
			break
		}
		b.WriteRune(r)
		used += rw
	}
	return b.String() + "…"
}
```

- [ ] **Step 4: Run the tests to verify they pass**

```bash
go test ./internal/nav -run TestTrunc_NeverSplitsARuneOrOverrunsWidth -v
go test -race ./internal/nav
```

Expected: PASS. The whole package stays green and no golden changes, because every existing golden uses ASCII labels. If a golden does change, stop and report it.

- [ ] **Step 5: Commit**

```bash
gofmt -w internal/nav/view.go internal/nav/view_test.go
git add internal/nav/view.go internal/nav/view_test.go
git commit -m "fix(nav): make trunc rune- and width-safe

trunc sliced bytes, so clipping a commit subject that contains an em dash
or an accented letter could cut a rune in half and emit invalid UTF-8.
It now walks runes and stops before the display width would exceed n-1.
ASCII results are unchanged.

Refs #364"
```

---

### Task 2: The detail column fits its height budget

**Files:**
- Modify: `internal/nav/view.go:516-536` (`detailColumn`), `:703` (`branchesBody`), `:733` (`commitsBody`), `:755` (`statusBody`)
- Test: `internal/nav/view_test.go` (append)
- Regenerate: `internal/nav/testdata/*.golden` (only if changed)

**Interfaces:**
- Consumes: `worktreeDetails` (`types.go:146`), `branchInfo` (`:125`), `commitInfo` (`:132`), `statusFile` (`:138`), `windowList` (`view.go:779`), `stretchPanel` (`:682`), `panel` (`:147`), `trunc` (Task 1).
- Produces: unexported `const detailPanelChrome = 4`. The three body methods gain a trailing `inner int` parameter: `branchesBody(d *worktreeDetails, max, inner int) string`, and likewise `commitsBody` and `statusBody`. Their only caller is `detailColumn`; no test calls them directly.

- [ ] **Step 1: Write the failing tests**

Append to `internal/nav/view_test.go`:

```go
// detailFixture builds a dashboard model with one selected worktree whose
// detail panels hold d, at the given terminal size.
func detailFixture(w, h int, d *worktreeDetails) Model {
	m := initialModel(Config{})
	m.width, m.height = w, h
	m.screen = screenDash
	m.repo = core.Repo{Name: "bridge", Path: "/r"}
	m.dashRows = []dashRow{{worktree: "wt", branch: "worktree-wt", path: "/r/wt"}}
	m.dashSel = 0
	m.details = map[string]*worktreeDetails{"/r/wt": d}
	return m
}

func detailSet(nBranches, nCommits, nStatus int, branch, subject, path string) *worktreeDetails {
	d := &worktreeDetails{branchesState: loadOK, commitsState: loadOK, statusState: loadOK}
	for i := 0; i < nBranches; i++ {
		d.branches = append(d.branches, branchInfo{name: fmt.Sprintf("%s-%d", branch, i)})
	}
	for i := 0; i < nCommits; i++ {
		d.commits = append(d.commits, commitInfo{sha: fmt.Sprintf("abc%04d", i), subject: subject})
	}
	for i := 0; i < nStatus; i++ {
		d.status = append(d.status, statusFile{code: "??", path: fmt.Sprintf("%s-%d.html", path, i)})
	}
	return d
}

func TestView_Dash_DetailColumn_FrameNeverExceedsPane(t *testing.T) {
	long := "fix(release): never write a partial issue index, and keep the changelog's file mode (#555) (#559) — again"
	longBranch := "worktree-assl-portal-sso-follow-up-for-the-customer-standard-package-and-more"
	longPath := "correspondence/2026-10-21-roadmap-product-effort-estimate-for-the-customer-standard-package"
	sets := []struct {
		name string
		d    *worktreeDetails
	}{
		{"long subjects", detailSet(9, 20, 0, "b", long, "f")},
		{"50 status files", detailSet(9, 20, 50, "b", "short", "f")},
		{"long branch names", detailSet(9, 20, 1, longBranch, "short", "f")},
		{"long status paths", detailSet(3, 3, 9, "b", "short", longPath)},
		{"everything", detailSet(20, 20, 50, longBranch, long, longPath)},
	}
	for _, w := range []int{100, 120, 150} {
		for h := 24; h <= 60; h++ {
			for _, s := range sets {
				m := detailFixture(w, h, s.d)
				out := m.View()
				if got := lipgloss.Height(out); got != h {
					t.Errorf("%dx%d %s: frame = %d rows, want exactly %d", w, h, s.name, got, h)
					continue
				}
				lines := strings.Split(stripANSI(out), "\n")
				if !strings.HasPrefix(strings.TrimSpace(lines[0]), "╭") || !strings.Contains(lines[1], "bridge nav · bridge") {
					t.Errorf("%dx%d %s: header box is not the top of the frame:\n%s\n%s", w, h, s.name, lines[0], lines[1])
				}
			}
		}
	}
}

func TestView_Dash_DetailColumn_LongSubjectClipsToOneRow(t *testing.T) {
	subject := strings.Repeat("very long commit subject ", 10)
	m := detailFixture(120, 40, detailSet(1, 1, 0, "b", subject, "f"))

	out := stripANSI(m.View())

	lines := strings.Split(out, "\n")
	idx := -1
	for i, l := range lines {
		if strings.Contains(l, "abc0000") {
			idx = i
		}
	}
	if idx < 0 || idx+1 >= len(lines) {
		t.Fatalf("commit row with sha abc0000 not rendered:\n%s", out)
	}
	if !strings.Contains(lines[idx], "…") {
		t.Errorf("a subject wider than the panel should be clipped with …, got row:\n%s", lines[idx])
	}
	if strings.Contains(lines[idx+1], "commit subject") {
		t.Errorf("subject wraps onto the next row:\n%s\n%s", lines[idx], lines[idx+1])
	}
}
```

`stripANSI` is the existing helper at `internal/nav/navtest_test.go:115`. `fmt`, `strings`, `lipgloss` and `core` are already imported by `view_test.go`.

- [ ] **Step 2: Run them to verify they fail**

```bash
go test ./internal/nav -run 'TestView_Dash_DetailColumn_' -v 2>&1 | tail -40
```

Expected FAIL:

- `FrameNeverExceedsPane` reports frames 1 to 5 rows taller than `h` for every set except possibly `long status paths` at large heights. This matches the reproduction table in the spec.
- `LongSubjectClipsToOneRow` fails because the row has no `…`; the subject wraps instead.

- [ ] **Step 3: Write the minimal implementation**

**3a.** In `internal/nav/view.go`, replace `detailColumn` (`:516-536`, comment included):

```go
// detailColumn renders the three stacked detail panels for the highlighted
// worktree, or a hint when the "+ create" row is selected. minH stretches the
// column to at least that total height (the last panel absorbs the slack) so it
// bottom-aligns with the worktree list; minH <= 0 renders at natural height.
func (m Model) detailColumn(w, minH int) string {
	path := m.selectedWorktreePath()
	if path == "" {
		hint := stMuted.Render("select a worktree to see its branches, commits & status")
		return stretchPanel(w, minH, "Details", hint)
	}
	per := (m.height - 14) / 3
	if per < 3 {
		per = 3
	}
	d := m.details[path]
	branches := panel(w, "Branches", m.branchesBody(d, per))
	commits := panel(w, "Recent commits", m.commitsBody(d, per))
	statusBody := m.statusBody(d, per)
	statusH := minH - lipgloss.Height(branches) - lipgloss.Height(commits)
	status := stretchPanel(w, statusH, "Git status", statusBody)
	return lipgloss.JoinVertical(lipgloss.Left, branches, commits, status)
}
```

with:

```go
// detailPanelChrome is the rows a detail panel spends outside its body: the
// rounded border top and bottom, the title line, and the blank line under it.
const detailPanelChrome = 4

// detailColumn renders the three stacked detail panels for the highlighted
// worktree, or a hint when the "+ create" row is selected, in exactly minH
// rows. Row caps come from minH, not m.height: Branches and Recent commits get
// a third of the body rows each, and Git status gets whatever remains, so a
// worktree with a lot of data windows its panels instead of growing the frame
// (#364). Every line is clipped to the panel's inner width, so one item is
// always one row.
func (m Model) detailColumn(w, minH int) string {
	path := m.selectedWorktreePath()
	if path == "" {
		hint := stMuted.Render("select a worktree to see its branches, commits & status")
		return stretchPanel(w, minH, "Details", hint)
	}
	per := max(1, (minH-3*detailPanelChrome)/3)
	inner := w - 4 // rounded border + Padding(0, 1), as noteFileBody uses
	d := m.details[path]
	branches := panel(w, "Branches", m.branchesBody(d, per, inner))
	commits := panel(w, "Recent commits", m.commitsBody(d, per, inner))
	statusH := minH - lipgloss.Height(branches) - lipgloss.Height(commits)
	statusMax := max(1, statusH-detailPanelChrome)
	status := stretchPanel(w, statusH, "Git status", m.statusBody(d, statusMax, inner))
	return lipgloss.JoinVertical(lipgloss.Left, branches, commits, status)
}
```

**3b.** In `branchesBody` (`:703`), change the signature to `func (m Model) branchesBody(d *worktreeDetails, max, inner int) string` and clip the name inside the loop. The loop currently reads:

```go
		switch {
		case br.current:
			b.WriteString(stAccent.Render("* " + br.name))
		case br.inWorktree:
			b.WriteString(stOk.Render("+ " + br.name))
		default:
			b.WriteString(stText.Render("  " + br.name))
		}
```

It becomes:

```go
		name := trunc(br.name, inner-2) // 2 = the "* " / "+ " / "  " marker
		switch {
		case br.current:
			b.WriteString(stAccent.Render("* " + name))
		case br.inWorktree:
			b.WriteString(stOk.Render("+ " + name))
		default:
			b.WriteString(stText.Render("  " + name))
		}
```

**3c.** In `commitsBody` (`:733`), change the signature to `func (m Model) commitsBody(d *worktreeDetails, max, inner int) string`, and change:

```go
		b.WriteString(stWarn.Render(c.sha) + " " + stText.Render(c.subject) + "\n")
```

to:

```go
		subject := trunc(c.subject, inner-lipgloss.Width(c.sha)-1)
		b.WriteString(stWarn.Render(c.sha) + " " + stText.Render(subject) + "\n")
```

**3d.** In `statusBody` (`:755`), change the signature to `func (m Model) statusBody(d *worktreeDetails, max, inner int) string`, and change:

```go
		b.WriteString(stBad.Render(f.code) + " " + stText.Render(f.path) + "\n")
```

to:

```go
		path := trunc(f.path, inner-lipgloss.Width(f.code)-1)
		b.WriteString(stBad.Render(f.code) + " " + stText.Render(path) + "\n")
```

- [ ] **Step 4: Run the tests, then regenerate the goldens**

```bash
go test ./internal/nav -run 'TestView_Dash_DetailColumn_|TestView_Dash_FrameHeightStable|TestViewDash_StatusNotice' -v 2>&1 | tail -20
```

Expected: PASS.

```bash
go test ./internal/nav 2>&1 | tail -20
```

If a golden mismatches, regenerate it and read the diff before staging:

```bash
go test ./internal/nav -update
git diff internal/nav/testdata
```

Acceptable changes:

- a different number of Branches, Recent commits or Git status items;
- a changed `… +N more` count;
- a clipped line ending in `…`;
- Git status absorbing rows that Branches or Commits no longer use.

Not acceptable: any change to the header, the left Sessions & Worktrees column, the hint line, or the total line count of the dashboard golden. If you see one, stop and report it.

- [ ] **Step 5: Verify the full suite, then commit**

```bash
gofmt -l .
go vet ./...
golangci-lint run
go test -race ./...
```

Expected: `gofmt -l` prints nothing, vet and lint are clean, and the full suite is green.

```bash
git add internal/nav/view.go internal/nav/view_test.go internal/nav/testdata
git commit -m "fix(nav): keep the dashboard detail column inside its height budget

The detail column budgeted its panels from m.height instead of its own
height, about 4 rows too generous once all three panels were full, and
let long commit subjects and paths soft-wrap onto rows the budget never
counted. Either pushed the frame past the pane and scrolled the header
off the top. Row caps now come from the column height with Git status
taking the remainder, and every line is clipped to the panel width.

Closes #364"
```

---

## Verification checklist

```bash
gofmt -l .
go vet ./...
golangci-lint run
go test -race ./...
```

Then check by hand. Run `bridge nav` on a repo whose worktrees differ: some have long commit subjects, some have untracked files. Move up and down the worktree list. The header box must stay put, and the frame must not change height.
