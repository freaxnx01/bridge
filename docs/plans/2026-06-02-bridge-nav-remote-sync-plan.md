# `bridge nav` Remote Sync Status — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Show per-worktree **ahead / behind / no-upstream** remote sync status on the `bridge nav` Screen-2 dashboard, kept accurate by a non-blocking background `git fetch` on dash entry.

**Architecture:** Additive to `internal/nav`, extending the existing dirty pipeline (`gitDirtyCmd` → `dirtyMsg` → `dirtyInfo` → `dirtyView`) rather than adding a parallel one. The same `git status -sb` call already run per worktree also reports behind/upstream, so parsing gains those with no extra git call. On dash entry the dashboard runs one background `git fetch` for the repo (worktrees share the object store), then re-reads each worktree's status; last-known state renders immediately and updates silently when the fetch lands. `Update` stays pure; fetch and status reads are `tea.Cmd`s.

**Tech Stack:** Go, `charmbracelet/bubbletea` (Model-Update-View), `lipgloss`. Stdlib `testing`, table-driven, hand-rolled — no testify.

**Spec:** [`docs/specs/2026-06-02-bridge-nav-remote-sync-design.md`](../specs/2026-06-02-bridge-nav-remote-sync-design.md)

**Conventions for every task:** run `gofmt -w` on touched files; per-task gate `go test ./internal/nav/...`; final gate `gofmt -l internal/nav` (empty), `go vet ./...`, `go test -race ./...`, `golangci-lint run` (last two in CI). Conventional Commits; do not push until the user asks. All paths relative to repo root.

---

## File structure

All files already exist; every change is a modification.

- `internal/nav/types.go` — add `behind`/`noUpstream` to `dirtyInfo`; add `fetchDoneMsg`.
- `internal/nav/format.go` (+ `format_test.go`) — extend `parseDirtyStatus` to parse the upstream/behind grammar.
- `internal/nav/data.go` — add `gitFetchCmd`.
- `internal/nav/update.go` (+ `update_test.go`) — fire the fetch on `dashRowsMsg`; handle `fetchDoneMsg`.
- `internal/nav/view.go` (+ `view_test.go`) — extend `dirtyView` rendering.
- `README.md`, `CHANGELOG.md`.

Task order respects dependencies: types → parser → fetch Cmd → reducer → view → docs. Tasks 2, 4, 5 are TDD and end green; Tasks 1, 3 are compile-only additions.

---

## Task 1: Type changes — `dirtyInfo` fields and `fetchDoneMsg`

**Files:**
- Modify: `internal/nav/types.go`

- [ ] **Step 1: Add `behind` and `noUpstream` to `dirtyInfo`**

Replace the existing struct (currently):

```go
// dirtyInfo is the async git status for a worktree.
type dirtyInfo struct {
	modified int
	ahead    int
	clean    bool
}
```

with:

```go
// dirtyInfo is the async git status for a worktree. ahead/behind come from the
// `git status -sb` upstream header; behind is only accurate after a fetch.
// noUpstream marks a branch with no remote tracking (ahead/behind meaningless).
type dirtyInfo struct {
	modified   int
	ahead      int
	behind     int
	noUpstream bool
	clean      bool
}
```

- [ ] **Step 2: Add the `fetchDoneMsg` message**

In `internal/nav/types.go`, immediately after the line `type slotRegisteredMsg struct{}`, add:

```go
type fetchDoneMsg struct{ err error }
```

- [ ] **Step 3: Verify it compiles**

Run: `go build ./internal/nav/`
Expected: builds. New fields/message are referenced by later tasks; Go does not error on unused struct fields or package-level types.

- [ ] **Step 4: Commit**

```bash
gofmt -w internal/nav/types.go
git add internal/nav/types.go
git commit -m "feat(nav): add behind/noUpstream to dirtyInfo and fetchDoneMsg"
```

---

## Task 2: Parser — upstream/behind grammar in `parseDirtyStatus`

**Files:**
- Modify: `internal/nav/format.go`
- Test: `internal/nav/format_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `internal/nav/format_test.go`:

```go
func TestParseDirtyStatus_UpstreamGrammar(t *testing.T) {
	tests := []struct {
		name       string
		out        string
		modified   int
		ahead      int
		behind     int
		noUpstream bool
		clean      bool
	}{
		{
			name:  "in sync clean",
			out:   "## main...origin/main\n",
			clean: true,
		},
		{
			name:  "ahead only",
			out:   "## main...origin/main [ahead 2]\n",
			ahead: 2, clean: true,
		},
		{
			name:   "behind only",
			out:    "## main...origin/main [behind 3]\n",
			behind: 3, clean: true,
		},
		{
			name:  "ahead and behind",
			out:   "## main...origin/main [ahead 2, behind 3]\n",
			ahead: 2, behind: 3, clean: true,
		},
		{
			name:       "no upstream",
			out:        "## feature-x\n",
			noUpstream: true, clean: true,
		},
		{
			name:       "detached head",
			out:        "## HEAD (no branch)\n",
			noUpstream: true, clean: true,
		},
		{
			name:     "dirty ahead behind",
			out:      "## main...origin/main [ahead 1, behind 4]\n M internal/nav/view.go\n?? scratch.txt\n",
			modified: 2, ahead: 1, behind: 4, clean: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseDirtyStatus(tt.out)
			if got.modified != tt.modified || got.ahead != tt.ahead || got.behind != tt.behind ||
				got.noUpstream != tt.noUpstream || got.clean != tt.clean {
				t.Errorf("parseDirtyStatus(%q) = %+v, want modified=%d ahead=%d behind=%d noUpstream=%v clean=%v",
					tt.out, got, tt.modified, tt.ahead, tt.behind, tt.noUpstream, tt.clean)
			}
		})
	}
}
```

- [ ] **Step 2: Run tests, verify they fail**

Run: `go test ./internal/nav -run TestParseDirtyStatus_UpstreamGrammar -v`
Expected: FAIL — `behind`/`noUpstream` always zero/false (current parser reads only ahead).

- [ ] **Step 3: Rewrite `parseDirtyStatus` and add the `trackToken` helper**

In `internal/nav/format.go`, replace the existing `parseDirtyStatus` function:

```go
func parseDirtyStatus(out string) dirtyInfo {
	info := dirtyInfo{}
	for _, l := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if l == "" {
			continue
		}
		if strings.HasPrefix(l, "## ") {
			if i := strings.Index(l, "[ahead "); i >= 0 {
				rest := l[i+len("[ahead "):]
				num := rest
				for j, c := range rest {
					if c < '0' || c > '9' {
						num = rest[:j]
						break
					}
				}
				info.ahead, _ = strconv.Atoi(num)
			}
			continue
		}
		info.modified++
	}
	info.clean = info.modified == 0
	return info
}
```

with:

```go
// parseDirtyStatus parses `git status --porcelain=v1 --branch` output into a
// dirtyInfo. The "## " header carries upstream state: a branch with no "..."
// upstream token has no remote tracking (noUpstream); the "[ahead N, behind M]"
// bracket gives the divergence counts (0 when absent). Non-header lines are
// changed files. behind is only accurate after a fetch freshens remote refs.
func parseDirtyStatus(out string) dirtyInfo {
	info := dirtyInfo{}
	for _, l := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		if l == "" {
			continue
		}
		if strings.HasPrefix(l, "## ") {
			header := l[len("## "):]
			info.noUpstream = !strings.Contains(header, "...")
			if i := strings.IndexByte(header, '['); i >= 0 {
				if j := strings.IndexByte(header[i:], ']'); j >= 0 {
					bracket := header[i+1 : i+j]
					info.ahead = trackToken(bracket, "ahead ")
					info.behind = trackToken(bracket, "behind ")
				}
			}
			continue
		}
		info.modified++
	}
	info.clean = info.modified == 0
	return info
}

// trackToken reads the integer following key (e.g. "ahead ") inside a git status
// branch-header bracket such as "ahead 2, behind 3"; returns 0 when key absent.
func trackToken(bracket, key string) int {
	i := strings.Index(bracket, key)
	if i < 0 {
		return 0
	}
	rest := bracket[i+len(key):]
	end := len(rest)
	for k, c := range rest {
		if c < '0' || c > '9' {
			end = k
			break
		}
	}
	n, _ := strconv.Atoi(rest[:end])
	return n
}
```

Note: `strconv` and `strings` are already imported (the original used both); no import change.

- [ ] **Step 4: Run tests, verify they pass**

Run: `go test ./internal/nav -run TestParseDirtyStatus_UpstreamGrammar -v`
Expected: PASS (all 7 subtests). Then `go test ./internal/nav/...` — whole package green.

- [ ] **Step 5: Commit**

```bash
gofmt -w internal/nav/format.go internal/nav/format_test.go
git add internal/nav/format.go internal/nav/format_test.go
git commit -m "feat(nav): parse behind + no-upstream from git status header"
```

---

## Task 3: Fetch Cmd — `gitFetchCmd`

**Files:**
- Modify: `internal/nav/data.go`

Thin `git -C <path> fetch` adapter; exercised by the manual TTY run (no unit test — like the other `git*Cmd` shells).

- [ ] **Step 1: Add `gitFetchCmd`**

Append to `internal/nav/data.go` (imports `os/exec` and `tea` are already present):

```go
// gitFetchCmd freshens the repo's remote-tracking refs so ahead/behind are
// accurate. Runs once per dashboard — worktrees of a repo share the object
// store, so one fetch updates remote refs for all of them. Non-fatal: a failed
// fetch (offline) reports the error; the reducer keeps last-known state.
func gitFetchCmd(path string) tea.Cmd {
	return func() tea.Msg {
		err := exec.Command("git", "-C", path, "fetch", "--quiet").Run()
		return fetchDoneMsg{err: err}
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
git commit -m "feat(nav): add gitFetchCmd to freshen remote-tracking refs"
```

---

## Task 4: Reducer wiring — fetch on entry, reload on completion

**Files:**
- Modify: `internal/nav/update.go`
- Test: `internal/nav/update_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `internal/nav/update_test.go` (the file already imports `tea "github.com/charmbracelet/bubbletea"` and defines `errFake`):

```go
func TestUpdate_FetchDoneMsg_Success_ReloadsDirty(t *testing.T) {
	m := initialModel(Config{})
	m.screen = screenDash
	m.dashRows = []dashRow{{worktree: "fix-x", path: "/r/fix-x"}}
	_, cmd := m.Update(fetchDoneMsg{})
	if cmd == nil {
		t.Errorf("a successful fetch should trigger a dirty reload Cmd")
	}
}

func TestUpdate_FetchDoneMsg_Error_KeepsLastKnown(t *testing.T) {
	m := initialModel(Config{})
	m.screen = screenDash
	m.dashRows = []dashRow{{worktree: "fix-x", path: "/r/fix-x"}}
	_, cmd := m.Update(fetchDoneMsg{err: errFake})
	if cmd != nil {
		t.Errorf("a failed fetch should be a no-op (keep last-known), got a Cmd")
	}
}
```

- [ ] **Step 2: Run tests, verify they fail**

Run: `go test ./internal/nav -run 'TestUpdate_FetchDoneMsg' -v`
Expected: FAIL — `fetchDoneMsg` is unhandled, so `TestUpdate_FetchDoneMsg_Success_ReloadsDirty` gets a nil Cmd. (The error case may pass vacuously before implementation; that is expected.)

- [ ] **Step 3: Fire the fetch on `dashRowsMsg`**

In `internal/nav/update.go`, replace the existing `dashRowsMsg` case:

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

with (only the final `return` changes — add `gitFetchCmd(m.repo.Path)` to the batch):

```go
	case dashRowsMsg:
		m.dashRows = msg.rows
		if m.dashSel >= len(m.dashRows) {
			m.dashSel = 0
		}
		m.details = map[string]*worktreeDetails{} // fresh rows -> reload panels
		m, detailCmd := m.ensureDetails()
		return m, tea.Batch(m.dirtyCmds(), detailCmd, gitFetchCmd(m.repo.Path))
```

- [ ] **Step 4: Handle `fetchDoneMsg`**

In `Update` (update.go), add this case immediately after the `dirtyMsg` case (after its `return m, nil`):

```go
	case fetchDoneMsg:
		if msg.err != nil {
			return m, nil // offline / fetch failed: keep last-known status
		}
		return m, m.dirtyCmds() // re-read status against the now-fresh remote refs
```

- [ ] **Step 5: Run tests, verify they pass**

Run: `go test ./internal/nav -run 'TestUpdate_FetchDoneMsg' -v`
Expected: PASS (both). Then `go test ./internal/nav/...` — whole package green.

- [ ] **Step 6: Commit**

```bash
gofmt -w internal/nav/update.go internal/nav/update_test.go
git add internal/nav/update.go internal/nav/update_test.go
git commit -m "feat(nav): background-fetch on dash entry, reload status on completion"
```

---

## Task 5: View — `dirtyView` ahead/behind/no-upstream rendering

**Files:**
- Modify: `internal/nav/view.go`
- Test: `internal/nav/view_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `internal/nav/view_test.go` (it already imports `strings`, `testing`):

```go
func TestDirtyView_States(t *testing.T) {
	m := initialModel(Config{})
	tests := []struct {
		name   string
		d      dirtyInfo
		want   []string
		absent []string
	}{
		{"clean in sync", dirtyInfo{clean: true}, []string{"✓ clean"}, []string{"●", "↑", "↓", "upstream"}},
		{"no upstream", dirtyInfo{noUpstream: true, clean: true}, []string{"no upstream"}, []string{"✓ clean", "↑", "↓"}},
		{"modified only", dirtyInfo{modified: 2}, []string{"●2"}, []string{"↑", "↓", "clean"}},
		{"ahead only clean", dirtyInfo{ahead: 1, clean: true}, []string{"↑1"}, []string{"●", "↓", "✓ clean"}},
		{"behind only clean", dirtyInfo{behind: 3, clean: true}, []string{"↓3"}, []string{"●", "↑", "✓ clean"}},
		{"modified ahead behind", dirtyInfo{modified: 2, ahead: 1, behind: 3}, []string{"●2", "↑1", "↓3"}, []string{"clean", "upstream"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := m.dirtyView(dashRow{dirty: tt.d, dirtyState: loadOK})
			for _, w := range tt.want {
				if !strings.Contains(got, w) {
					t.Errorf("dirtyView = %q, missing %q", got, w)
				}
			}
			for _, a := range tt.absent {
				if strings.Contains(got, a) {
					t.Errorf("dirtyView = %q, should not contain %q", got, a)
				}
			}
		})
	}
}
```

- [ ] **Step 2: Run tests, verify they fail**

Run: `go test ./internal/nav -run TestDirtyView_States -v`
Expected: FAIL — current `dirtyView` returns `✓ clean` for any `clean` row (so `ahead only clean`/`behind only clean` fail) and never renders `↓` or the no-upstream marker.

- [ ] **Step 3: Rewrite `dirtyView`**

In `internal/nav/view.go`, replace the existing `dirtyView`:

```go
func (m Model) dirtyView(r dashRow) string {
	switch r.dirtyState {
	case loadPending:
		return m.spin.View()
	case loadErr:
		return stMuted.Render("?")
	}
	if r.dirty.clean {
		return stOk.Render("✓ clean")
	}
	s := stBad.Render(fmt.Sprintf("●%d", r.dirty.modified))
	if r.dirty.ahead > 0 {
		s += " " + stWarn.Render(fmt.Sprintf("↑%d", r.dirty.ahead))
	}
	return s
}
```

with:

```go
// dirtyView renders the per-worktree git indicator. Precedence: loading/error
// first, then no-upstream, then the modified/ahead/behind tokens (zeros
// omitted), then "✓ clean" when nothing diverges.
func (m Model) dirtyView(r dashRow) string {
	switch r.dirtyState {
	case loadPending:
		return m.spin.View()
	case loadErr:
		return stMuted.Render("?")
	}
	if r.dirty.noUpstream {
		return stMuted.Render("⤳ no upstream")
	}
	var tokens []string
	if r.dirty.modified > 0 {
		tokens = append(tokens, stBad.Render(fmt.Sprintf("●%d", r.dirty.modified)))
	}
	if r.dirty.ahead > 0 {
		tokens = append(tokens, stWarn.Render(fmt.Sprintf("↑%d", r.dirty.ahead)))
	}
	if r.dirty.behind > 0 {
		tokens = append(tokens, stAccent.Render(fmt.Sprintf("↓%d", r.dirty.behind)))
	}
	if len(tokens) == 0 {
		return stOk.Render("✓ clean")
	}
	return strings.Join(tokens, " ")
}
```

Note: `fmt` and `strings` are already imported in `view.go`; `stBad`/`stWarn`/`stAccent`/`stOk`/`stMuted` are existing style vars.

- [ ] **Step 4: Run tests, verify they pass**

Run: `go test ./internal/nav -run TestDirtyView_States -v`
Expected: PASS (all 6 subtests). Then `go test ./internal/nav/...` — whole package green (existing `TestView_*` unaffected; no existing test asserts the old clean-but-ahead behavior).

- [ ] **Step 5: Commit**

```bash
gofmt -w internal/nav/view.go internal/nav/view_test.go
git add internal/nav/view.go internal/nav/view_test.go
git commit -m "feat(nav): render ahead/behind/no-upstream in the dirty indicator"
```

---

## Task 6: Docs, full gates, and manual TTY verification

**Files:**
- Modify: `README.md`, `CHANGELOG.md`

- [ ] **Step 1: README**

Find the `bridge nav` description in `README.md` (run `grep -n "bridge nav" README.md` and read the surrounding paragraph — it already mentions "async git-dirty status"). Extend that clause to mention remote sync, e.g. change a phrase like "with async git-dirty status" to:

```
with async git-dirty status and remote sync (ahead/behind, refreshed by a
background fetch).
```

Match the surrounding prose; do not restructure the section.

- [ ] **Step 2: CHANGELOG**

Under `[Unreleased]` → `Added` in `CHANGELOG.md` (the section already exists from the panels work — append a bullet to it; create `### Added` under `## [Unreleased]` only if absent):

```
- `bridge nav` dashboard: per-worktree remote sync status — **ahead**, **behind**,
  and a distinct **no-upstream** marker — kept accurate by a non-blocking
  background `git fetch` on dashboard entry (last-known shown immediately; offline
  is a no-op).
```

- [ ] **Step 3: Commit docs**

```bash
git add README.md CHANGELOG.md
git commit -m "docs(nav): document remote sync status indicator in README + CHANGELOG"
```

- [ ] **Step 4: Full gates**

Run:
```bash
gofmt -l internal/nav
go vet ./...
go test -race ./...
golangci-lint run
```
Expected: `gofmt -l` empty, vet clean, tests pass under `-race`, lint clean. If `go test -race` fails with a cgo/"C compiler not found" error (sandbox without a C toolchain), substitute `go test ./...` and note that `-race` must run in CI. If `golangci-lint` is not installed, say so and note it must run in CI — do not skip silently.

- [ ] **Step 5: Manual TTY verification (no code)**

Build and run interactively (`--once` smoke can't exercise these paths):

1. `make install-go && bridge nav`
2. Enter a repo with ≥2 worktrees whose branches track a remote. Verify each worktree's indicator shows the right tokens: `✓ clean` when in sync, `↑N` when ahead, `↓N` when behind, `●N ↑N ↓N` combined when modified + diverged.
3. With a worktree known to be behind its remote (commit on the remote, don't pull): on dash entry the indicator first shows last-known state, then updates to `↓N` once the background fetch lands (a beat later) — without blocking the UI.
4. A branch with no upstream (e.g. a fresh local branch never pushed) shows `⤳ no upstream` (muted), not `✓ clean`.
5. Offline (disable network): the dashboard still loads and shows last-known state; no error appears (fetch fails silently).
6. `bridge nav --once | cat`: still prints a picker frame (non-TTY smoke unaffected).

---

## Self-review (done while writing)

- **Spec coverage:** ahead/behind/no-upstream parse (T2 `parseDirtyStatus`); `dirtyInfo` fields + `fetchDoneMsg` (T1); always-on non-blocking fetch once per repo on dash entry + reload on completion + offline no-op (T3 `gitFetchCmd`, T4 reducer); distinct rendering with documented precedence and concrete style vars (T5 `dirtyView`); docs (T6). Every spec section maps to a task.
- **Type consistency:** `dirtyInfo{modified, ahead, behind, noUpstream, clean}` defined once (T1), parsed in T2, rendered in T5. `fetchDoneMsg{err error}` defined T1, returned by `gitFetchCmd` (T3), handled in T4. `trackToken` defined and used within T2. `gitFetchCmd(m.repo.Path)` (T4) matches the T3 signature. `dirtyCmds()` reused, not changed.
- **Placeholder scan:** no TBD/TODO; every code step shows complete code; cgo/`golangci-lint` fallbacks called out explicitly in T6.
- **Reuse/DRY:** extends the existing `gitDirtyCmd`/`dirtyMsg`/`dirtyView` pipeline; `dirtyCmds` reused for the post-fetch reload; existing style vars reused. No parallel status path introduced.
