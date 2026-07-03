# nav Recent repos (#183) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Surface the 5 most-recently-used local repos as a read-only **"Recent"** section on the `bridge nav` picker, sourced from the existing `mru` file via `core.LoadMRU`. Visible only when the filter is empty; a focus target (`focusFilter → focusRecent → focusList → focusSessions`) navigable with `↑`/`↓`; `enter` opens the repo through the same `openRepoRow` → dashboard path as the Repos list.

**Architecture:** `Init` fires `loadRecentCmd(cfg.RecentPath)` → `core.LoadMRU` → `recentMsg{paths}`, stored in `m.mruPaths`. `recentRepos()` resolves those paths against the already-loaded `m.localRepos` (by `.repo.Path`), most-recent-first, capped at 5, skipping unresolved paths — reusing the Repos-list `repoRow` so labels + issue-count tags match for free. `recentVisible()` gates the section (and the `focusRecent` cycle stop) on an empty filter. nav stays cmd-layer-free: the mru path is injected as `Config.RecentPath`.

**Tech Stack:** Go (Bubble Tea/lipgloss, stdlib `testing`). Spec: `docs/superpowers/specs/2026-07-01-nav-recent-repos-design.md`.

---

## File Structure

- **Modify** `internal/nav/types.go` — `focusRecent` const, `recentMsg`, `Config.RecentPath`.
- **Modify** `internal/nav/model.go` — `mruPaths`/`recentSel` fields, `loadRecentCmd` in `Init`.
- **Modify** `internal/nav/data.go` — `loadRecentCmd`.
- **Modify** `internal/nav/update.go` — `recentMsg` case, `recentRepos`/`recentVisible` helpers, `focusRecent` navigation, focus-cycle inclusion.
- **Modify** `internal/nav/view.go` — `recentBlock` render + `viewPicker` insertion.
- **Modify** `internal/nav/*_test.go` — helper/Update tests + a golden flow test.
- **Modify** `cmd/bridge/nav.go` — wire `RecentPath` to `cacheRoot()/mru`.

Nothing in `internal/core` or `internal/store` changes: `core.LoadMRU` already exists and is tested (`internal/core/mru_test.go`); the MRU is still written only by `store.MRUTouch` (`cmd/bridge/open.go`, `cmd/bridge/preflight.go`).

---

## Task 1: state + data + computed helpers

**Files:**
- Modify: `internal/nav/types.go`, `internal/nav/model.go`, `internal/nav/data.go`, `internal/nav/update.go`
- Test: `internal/nav/data_test.go` (or `update_test.go`)

- [ ] **Step 1: Write the failing tests**

Append to `internal/nav/update_test.go` (white-box, package `nav`; `core` and `tea` are already imported there):

```go
func TestRecentRepos_ResolvesCapsAndSkips(t *testing.T) {
	m := initialModel(Config{})
	m.localRepos = []repoRow{
		{label: "github/public/a", repo: core.Repo{Path: "/r/a"}},
		{label: "github/public/b", repo: core.Repo{Path: "/r/b"}},
		{label: "github/public/c", repo: core.Repo{Path: "/r/c"}},
		{label: "github/public/d", repo: core.Repo{Path: "/r/d"}},
		{label: "github/public/e", repo: core.Repo{Path: "/r/e"}},
		{label: "github/public/f", repo: core.Repo{Path: "/r/f"}},
	}
	// most-recent-first, with one stale path (/r/gone) that must be skipped.
	m.mruPaths = []string{"/r/c", "/r/gone", "/r/a", "/r/f", "/r/b", "/r/d", "/r/e"}
	got := m.recentRepos()
	want := []string{"/r/c", "/r/a", "/r/f", "/r/b", "/r/d"} // capped at 5, stale skipped
	if len(got) != len(want) {
		t.Fatalf("got %d rows, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i].repo.Path != want[i] {
			t.Errorf("[%d] = %q, want %q", i, got[i].repo.Path, want[i])
		}
	}
}

func TestRecentRepos_EmptyWhenNoMRU(t *testing.T) {
	m := initialModel(Config{})
	m.localRepos = []repoRow{{repo: core.Repo{Path: "/r/a"}}}
	if got := m.recentRepos(); len(got) != 0 {
		t.Errorf("no MRU should yield no recent rows, got %+v", got)
	}
}

func TestRecentVisible_FilterGate(t *testing.T) {
	m := initialModel(Config{})
	m.localRepos = []repoRow{{repo: core.Repo{Path: "/r/a"}}}
	m.mruPaths = []string{"/r/a"}
	if !m.recentVisible() {
		t.Fatal("empty filter + resolved MRU should be visible")
	}
	m.filter.SetValue("a")
	if m.recentVisible() {
		t.Error("non-empty filter should hide the Recent section")
	}
}

func TestUpdate_RecentMsg_StoresPaths(t *testing.T) {
	m := initialModel(Config{})
	out, _ := m.Update(recentMsg{paths: []string{"/r/x", "/r/y"}})
	if got := out.(Model).mruPaths; len(got) != 2 || got[0] != "/r/x" {
		t.Errorf("recentMsg should store paths, got %+v", got)
	}
}
```

(Confirm `filter.SetValue` is the bubbles `textinput` setter — it is; `m.filter` is a `textinput.Model`.)

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/nav/ -run 'TestRecentRepos|TestRecentVisible|TestUpdate_RecentMsg' -v`
Expected: FAIL — `recentMsg`, `m.mruPaths`, `m.recentRepos`, `m.recentVisible` undefined.

- [ ] **Step 3: Add the focus const, message, and Config field (`types.go`)**

Insert `focusRecent` into the `focus` const block (after `focusFilter`):

```go
const (
	focusFilter focus = iota
	focusRecent
	focusList
	focusSessions
)
```

Add the message next to `type reposMsg struct{ rows []repoRow }`:

```go
type recentMsg struct{ paths []string }
```

Add to the `Config` struct (near `RemoteCache`/`SlotsPath`):

```go
	// RecentPath is the MRU file read (read-only) to build the picker's Recent
	// section. Empty disables the section.
	RecentPath string
```

- [ ] **Step 4: Add model fields + wire `Init` (`model.go`)**

Add to the `Model` struct (near `pickerSel`/`sessionSel`):

```go
	mruPaths  []string // raw MRU order (from recentMsg); resolved lazily by recentRepos
	recentSel int
```

Replace `Init` so it loads the MRU when configured:

```go
func (m Model) Init() tea.Cmd {
	cmds := []tea.Cmd{
		m.spin.Tick,
		loadLocalReposCmd(m.cfg.ReposRoots),
		loadSessionsCmd(m.cfg.SlotsPath),
		loadRemoteCmd(m.cfg.RemoteCache),
	}
	if m.cfg.RecentPath != "" {
		cmds = append(cmds, loadRecentCmd(m.cfg.RecentPath))
	}
	return tea.Batch(cmds...)
}
```

- [ ] **Step 5: Add `loadRecentCmd` (`data.go`)**

`data.go` already imports `core` and `tea`. Add:

```go
// loadRecentCmd reads the MRU file (read-only) and returns its paths, most-
// recent-first. A read error is swallowed to an empty list, matching nav's
// best-effort loaders.
func loadRecentCmd(path string) tea.Cmd {
	return func() tea.Msg {
		paths, _ := core.LoadMRU(path)
		return recentMsg{paths: paths}
	}
}
```

- [ ] **Step 6: Add `recentMsg` case + `recentRepos`/`recentVisible` (`update.go`)**

Add the message case next to `case reposMsg:` in the top-level `Update` switch:

```go
	case recentMsg:
		m.mruPaths = msg.paths
		return m, nil
```

Add the computed helpers next to `visibleRepos` (line ~217):

```go
// recentRepos returns up to 5 most-recently-used local repos, most-recent-first,
// resolved from m.mruPaths against m.localRepos by path. Unresolved paths (repo
// moved/deleted) are skipped so every entry is openable. Computed on demand so it
// tracks localRepos (including async issue counts), mirroring visibleRepos.
func (m Model) recentRepos() []repoRow {
	const maxRecent = 5
	if len(m.mruPaths) == 0 {
		return nil
	}
	byPath := make(map[string]repoRow, len(m.localRepos))
	for _, r := range m.localRepos {
		byPath[r.repo.Path] = r
	}
	out := make([]repoRow, 0, maxRecent)
	for _, p := range m.mruPaths {
		if r, ok := byPath[p]; ok {
			out = append(out, r)
			if len(out) == maxRecent {
				break
			}
		}
	}
	return out
}

// recentVisible reports whether the Recent section shows: only with an empty
// filter and at least one resolved recent repo. Also gates the focusRecent cycle.
func (m Model) recentVisible() bool {
	return m.filter.Value() == "" && len(m.recentRepos()) > 0
}
```

- [ ] **Step 7: Run tests + commit**

Run: `go test ./internal/nav/ -run 'TestRecentRepos|TestRecentVisible|TestUpdate_RecentMsg' -v && go test ./internal/nav/`
Expected: PASS (new) and the full nav package still green (no navigation wired yet, so behaviour is unchanged).

```bash
gofmt -l internal/nav/ ; go vet ./internal/nav/
git add internal/nav/types.go internal/nav/model.go internal/nav/data.go internal/nav/update.go internal/nav/update_test.go
git commit -m "feat(nav): load MRU into Recent state (read-only)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 2: focus navigation + cycle

**Files:**
- Modify: `internal/nav/update.go`
- Test: `internal/nav/update_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `internal/nav/update_test.go`. Helper to build a picker with a resolved Recent section:

```go
func recentModel() Model {
	m := initialModel(Config{})
	m.localRepos = []repoRow{
		{label: "github/public/a", repo: core.Repo{Path: "/r/a"}},
		{label: "github/public/b", repo: core.Repo{Path: "/r/b"}},
	}
	m.mruPaths = []string{"/r/b", "/r/a"} // 2 resolved recent rows
	return m
}

func TestUpdatePicker_FilterDown_EntersRecent(t *testing.T) {
	m := recentModel() // pickerFocus == focusFilter (initial)
	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	got := out.(Model)
	if got.pickerFocus != focusRecent || got.recentSel != 0 {
		t.Errorf("↓ from filter with Recent visible: focus=%d sel=%d", got.pickerFocus, got.recentSel)
	}
}

func TestUpdatePicker_FilterDown_SkipsRecentWhenHidden(t *testing.T) {
	m := initialModel(Config{}) // no MRU -> Recent hidden
	m.localRepos = []repoRow{{repo: core.Repo{Path: "/r/a"}}}
	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if out.(Model).pickerFocus != focusList {
		t.Errorf("↓ from filter with Recent hidden should go to focusList, got %d", out.(Model).pickerFocus)
	}
}

func TestFocusRecent_DownPastEnd_GoesToList(t *testing.T) {
	m := recentModel()
	m.pickerFocus = focusRecent
	m.recentSel = 1 // last of 2
	m.filter.Blur()
	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	got := out.(Model)
	if got.pickerFocus != focusList || got.pickerSel != 0 {
		t.Errorf("↓ past last recent row should land focusList@0, got focus=%d sel=%d", got.pickerFocus, got.pickerSel)
	}
}

func TestFocusRecent_UpAtTop_GoesToFilter(t *testing.T) {
	m := recentModel()
	m.pickerFocus = focusRecent
	m.recentSel = 0
	m.filter.Blur()
	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyUp})
	if out.(Model).pickerFocus != focusFilter {
		t.Errorf("↑ at top of Recent should return to focusFilter, got %d", out.(Model).pickerFocus)
	}
}

func TestFocusRecent_Enter_OpensDashboard(t *testing.T) {
	m := recentModel()
	m.pickerFocus = focusRecent
	m.recentSel = 0 // -> /r/b
	m.filter.Blur()
	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got := out.(Model)
	if got.screen != screenDash || got.repo.Path != "/r/b" {
		t.Errorf("enter on recent row should enter dashboard for /r/b, got screen=%d repo=%q", got.screen, got.repo.Path)
	}
}

func TestCyclePickerFocus_IncludesRecentWhenVisible(t *testing.T) {
	m := recentModel() // focusFilter
	m = m.cyclePickerFocus()
	if m.pickerFocus != focusRecent {
		t.Fatalf("tab from filter should reach focusRecent, got %d", m.pickerFocus)
	}
	m = m.cyclePickerFocus()
	if m.pickerFocus != focusList {
		t.Errorf("tab from recent should reach focusList, got %d", m.pickerFocus)
	}
}

func TestCyclePickerFocus_SkipsRecentWhenHidden(t *testing.T) {
	m := recentModel()
	m.filter.SetValue("x") // hides Recent
	m = m.cyclePickerFocus()
	if m.pickerFocus == focusRecent {
		t.Errorf("tab must skip focusRecent when the section is hidden")
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/nav/ -run 'TestUpdatePicker_FilterDown|TestFocusRecent|TestCyclePickerFocus' -v`
Expected: FAIL — `focusFilter` `↓` still routes to `focusList`; no `focusRecent` handling; cycle omits `focusRecent`.

- [ ] **Step 3: Route `↓` from the filter into Recent**

In `updatePicker`, the `focusFilter` block, replace the `tea.KeyDown` case (currently at line ~290):

```go
		case tea.KeyDown:
			m.filter.Blur()
			if m.recentVisible() {
				m.pickerFocus = focusRecent
				m.recentSel = 0
			} else {
				m.pickerFocus = focusList
				m.pickerSel = 0
			}
			return m, nil
```

- [ ] **Step 4: Add the `focusRecent` key block**

In `updatePicker`, add a block **after** the `focusSessions` block and **before** the `focusFilter` block (so `tab`/`shift+tab`/`esc`/`q` from the top switch still take precedence):

```go
	if m.pickerFocus == focusRecent {
		rows := m.recentRepos()
		if !m.recentVisible() || len(rows) == 0 {
			// section vanished (filter set / repos changed): fall back to filter.
			m.pickerFocus = focusFilter
			m.filter.Focus()
			return m, nil
		}
		switch msg.String() {
		case "up", "k":
			if m.recentSel <= 0 {
				m.pickerFocus = focusFilter
				m.filter.Focus()
			} else {
				m.recentSel--
			}
		case "down", "j":
			if m.recentSel < len(rows)-1 {
				m.recentSel++
			} else {
				m.pickerFocus = focusList
				m.pickerSel = 0
			}
		case "home", "g":
			m.recentSel = 0
		case "end", "G":
			m.recentSel = len(rows) - 1
		case "/":
			m.pickerFocus = focusFilter
			m.filter.Focus()
		case "enter":
			m.recentSel = clampInt(m.recentSel, 0, len(rows)-1)
			return m.openRepoRow(rows[m.recentSel])
		}
		return m, nil
	}
```

- [ ] **Step 5: Let `↑` from the list top step into Recent**

In the `focusList` switch, replace the `up`/`k` case (line ~330):

```go
	case "up", "k":
		if m.pickerSel <= 0 {
			if m.recentVisible() {
				m.pickerFocus = focusRecent
				m.recentSel = len(m.recentRepos()) - 1
			} else {
				m.pickerFocus = focusFilter
				m.filter.Focus()
			}
			return m, nil
		}
		m.pickerSel--
```

- [ ] **Step 6: Include `focusRecent` in the Tab cycles**

Replace `cyclePickerFocus` (line ~742):

```go
func (m Model) cyclePickerFocus() Model {
	switch m.pickerFocus {
	case focusFilter:
		m.filter.Blur()
		if m.recentVisible() {
			m.pickerFocus = focusRecent
			m.recentSel = clampInt(m.recentSel, 0, len(m.recentRepos())-1)
		} else {
			m.pickerFocus = focusList
		}
	case focusRecent:
		m.pickerFocus = focusList
	case focusList:
		if len(m.sessions) > 0 {
			m.pickerFocus = focusSessions
			m.sessionSel = clampInt(m.sessionSel, 0, len(m.sessions)-1)
		} else {
			m.pickerFocus = focusFilter
			m.filter.Focus()
		}
	default: // focusSessions
		m.pickerFocus = focusFilter
		m.filter.Focus()
	}
	return m
}
```

Replace `cyclePickerFocusBack` (line ~784):

```go
func (m Model) cyclePickerFocusBack() Model {
	switch m.pickerFocus {
	case focusFilter:
		m.filter.Blur()
		if len(m.sessions) > 0 {
			m.pickerFocus = focusSessions
			m.sessionSel = clampInt(m.sessionSel, 0, len(m.sessions)-1)
		} else {
			m.pickerFocus = focusList
		}
	case focusSessions:
		m.pickerFocus = focusList
	case focusList:
		if m.recentVisible() {
			m.pickerFocus = focusRecent
			m.recentSel = clampInt(m.recentSel, 0, len(m.recentRepos())-1)
		} else {
			m.pickerFocus = focusFilter
			m.filter.Focus()
		}
	default: // focusRecent
		m.pickerFocus = focusFilter
		m.filter.Focus()
	}
	return m
}
```

- [ ] **Step 7: Run tests + commit**

Run: `go test ./internal/nav/ -run 'TestUpdatePicker_FilterDown|TestFocusRecent|TestCyclePickerFocus' -v && go test ./internal/nav/`
Expected: PASS (new) and the full nav package still green (existing focus tests unaffected — the cycle order is a strict superset that skips `focusRecent` when hidden, which is the state in older tests).

```bash
gofmt -l internal/nav/ ; go vet ./internal/nav/
git add internal/nav/update.go internal/nav/update_test.go
git commit -m "feat(nav): focusRecent navigation + tab-cycle inclusion

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 3: View (render the Recent section) + golden

**Files:**
- Modify: `internal/nav/view.go`
- Test: `internal/nav/flow_test.go` + `internal/nav/testdata/`

- [ ] **Step 1: Write the failing tests**

Append to `internal/nav/flow_test.go` (uses the `session` harness in `navtest_test.go`; `core`, `tea`, `strings` are imported there):

```go
func TestViewPicker_RecentSection(t *testing.T) {
	m := initialModel(Config{})
	m.width, m.height = 120, 40
	m.localRepos = []repoRow{
		{label: "github/public/bridge", repo: core.Repo{Path: "/r/bridge"}, issueCount: 3, issueState: loadOK},
		{label: "github/public/agent-os", repo: core.Repo{Path: "/r/agent"}},
	}
	m.mruPaths = []string{"/r/bridge", "/r/agent"}
	// filter empty -> section shown, with the issue-count tag reused
	frame := stripANSI(m.viewPicker())
	if !strings.Contains(frame, "Recent") {
		t.Errorf("empty filter should show the Recent heading:\n%s", frame)
	}
	if !strings.Contains(frame, "●3") {
		t.Errorf("recent row should reuse the issue-count tag:\n%s", frame)
	}
	// typing filter text collapses the section
	m.filter.SetValue("agent")
	if strings.Contains(stripANSI(m.viewPicker()), "Recent") {
		t.Errorf("non-empty filter should hide the Recent section")
	}
}

func TestFlow_RecentSection_Golden(t *testing.T) {
	s := newSession(t, Config{})
	s.send(reposMsg{rows: []repoRow{
		{label: "github/public/bridge", repo: core.Repo{Path: "/r/bridge"}},
		{label: "github/public/agent-os", repo: core.Repo{Path: "/r/agent"}},
	}})
	s.send(recentMsg{paths: []string{"/r/bridge", "/r/agent"}})
	assertGolden(t, "picker_recent_section", s.frame())
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./internal/nav/ -run 'TestViewPicker_RecentSection|TestFlow_RecentSection' -v`
Expected: FAIL — `viewPicker` renders no Recent heading (and the golden is missing).

- [ ] **Step 3: Implement `recentBlock` + insert it into `viewPicker`**

In `internal/nav/view.go`, add the helper (near `viewPicker`; `lipgloss`, `strings`, and the `st*` styles + `repoIssueTag` already exist in the package):

```go
// recentBlock renders the "Recent" section (heading + rows) for the picker,
// highlighting recentSel when focused. Rows reuse the Repos-list label and
// repoIssueTag so formatting matches. Returns the block (with a trailing blank
// line) and its rendered height; ("", 0) when the section is hidden.
func (m Model) recentBlock() (string, int) {
	if !m.recentVisible() {
		return "", 0
	}
	rows := m.recentRepos()
	var b strings.Builder
	b.WriteString(stMuted.Render("Recent") + "\n")
	for i, r := range rows {
		tag := repoIssueTag(r)
		if m.pickerFocus == focusRecent && i == m.recentSel {
			b.WriteString(stSel.Render(stAccent.Render("▸ ")+r.label+tag) + "\n")
		} else {
			b.WriteString("  " + stText.Render(r.label) + tag + "\n")
		}
	}
	s := b.String()
	return s + "\n", lipgloss.Height(s) + 1
}
```

In `viewPicker`, insert the block right after the filter line and subtract its height from the list budget. Change:

```go
	var rb strings.Builder
	rb.WriteString(m.filter.View() + "\n\n")
	rows := m.visibleRepos()
```

to:

```go
	var rb strings.Builder
	rb.WriteString(m.filter.View() + "\n\n")
	recent, recentH := m.recentBlock()
	rb.WriteString(recent)
	rows := m.visibleRepos()
```

and change the budget line:

```go
		maxVisible := m.height - used - 9
```

to:

```go
		maxVisible := m.height - used - 9 - recentH
```

(When the section is hidden, `recent == ""` and `recentH == 0`, so the existing layout is byte-for-byte unchanged — existing goldens stay stable.)

- [ ] **Step 4: Generate the golden, inspect, confirm**

Run: `go test ./internal/nav/ -run TestViewPicker_RecentSection -v` (should PASS now).
Run: `go test ./internal/nav/ -run TestFlow_RecentSection_Golden -update`
Then: `cat internal/nav/testdata/picker_recent_section.golden` — confirm a `Recent` heading with `github/public/bridge` then `github/public/agent-os` above the alphabetical Repos list, no ANSI. Eyeball it.
Run without `-update`: `go test ./internal/nav/ -run TestFlow_RecentSection_Golden`
Expected: PASS. Confirm existing goldens unchanged: `git status --short internal/nav/testdata/` shows only the new file.

- [ ] **Step 5: Full suite + commit**

Run: `go test ./internal/nav/ && gofmt -l internal/nav/ && go vet ./internal/nav/`

```bash
git add internal/nav/view.go internal/nav/flow_test.go internal/nav/testdata/picker_recent_section.golden
git commit -m "feat(nav): render the Recent section on the picker

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 4: cmd/bridge wiring (`RecentPath`)

**Files:**
- Modify: `cmd/bridge/nav.go`

- [ ] **Step 1: Wire the mru path**

In `cmd/bridge/nav.go`, add to the `nav.Config{…}` literal (next to `RemoteCache`/`SlotsPath`, which already use `filepath.Join(cacheRoot(), …)`):

```go
			RecentPath:   filepath.Join(cacheRoot(), "mru"),
```

This is the exact path `cmd/bridge/open.go` (`mruPath := filepath.Join(cacheRoot(), "mru")`) and `cmd/bridge/preflight.go` write via `store.MRUTouch`, so nav reads what those write. (`filepath` and `cacheRoot` are already in scope in `nav.go`.)

- [ ] **Step 2: Build + vet + suite**

Run:
```bash
go build ./... && go vet ./... && gofmt -l . | grep -v '.worktrees/'
go test ./cmd/bridge/ ./internal/nav/ ./internal/core/
```
Expected: builds; vet clean; no gofmt output; tests `ok`.

- [ ] **Step 3: Manual smoke (best-effort — needs prior `bridge open` history)**

Run:
```bash
just build
bridge open <somerepo>   # appends to cacheRoot()/mru
bridge open <otherrepo>
bridge nav               # picker: with the filter empty, a "Recent" section lists them, most-recent-first
```
Confirm: the Recent section appears above the Repos list with the empty filter; `tab` (or `↓`) reaches it; `↑`/`↓` move within it; `enter` opens the repo's dashboard; typing a filter collapses the section.
Expected: the two just-opened repos appear under Recent, newest first; enter lands on the dashboard.

- [ ] **Step 4: Commit**

```bash
git add cmd/bridge/nav.go
git commit -m "feat(bridge): wire nav RecentPath to the mru cache file

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 5: Full verification

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
Expected: no diff beyond the new `picker_recent_section.golden` already committed (goldens stable).

- [ ] **Step 3: Lint (best-effort)**

Run: `golangci-lint run ./internal/nav/... ./cmd/bridge/...` (if installed). Else note it; `go vet` is the gate.

- [ ] **Step 4: Report**

Report Steps 1-2 output + the Task 4 manual smoke. No success claims without output.

---

## Notes for the implementer

- **Read-only over the MRU:** the only new touch of `mru` is `core.LoadMRU` via `loadRecentCmd`; `store.MRUTouch` and the write sites (`cmd/bridge/open.go`, `preflight.go`) are unchanged.
- **`recentRepos()` is computed, not stored** — like `visibleRepos()` — so it reflects live `m.localRepos` (labels + async `issueCount`), which is what makes the "same label and issue-count tag" criterion fall out for free.
- **Filter-empty gate is the single source of truth:** `recentVisible()` drives the section render, the `focusRecent` cycle stop, and the `↓`/`↑` step transitions — keep them all going through it.
- **`focusRecent` sits between `focusFilter` and `focusList`** in the const block and every cycle/step, matching `focusFilter → focusRecent → focusList → focusSessions`.
- **Hidden-section layout is byte-identical:** `recentBlock` returns `("", 0)` when hidden, so the Repos-list windowing math (`maxVisible`) is unchanged and existing goldens stay stable.
- **Path matching is exact** on `core.Repo.Path`; both the MRU writer and `DiscoverRepos` produce the same absolute path, so no normalization is needed.
- If you hit a blocker, find the fix and note it inline here.
