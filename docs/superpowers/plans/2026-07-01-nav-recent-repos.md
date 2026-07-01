# "Recent" repos section in `bridge nav` — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Surface the 5 most-recently-used local repos as a dedicated "Recent" section in the `bridge nav` picker, sourced read-only from the existing `mru` file.

**Architecture:** Consume the already-persisted MRU (`core.LoadMRU`) that no UI currently reads. A new `tea.Cmd` loads the MRU paths into the Bubble Tea `Model`; a pure `recentRows()` method resolves them against the loaded local repos (skipping stale paths, capped at 5); the picker renders a "Recent" panel above the Repos list, shown only when the filter box is empty, and adds `focusRecent` as a Tab-cycle focus target.

**Tech Stack:** Go, Bubble Tea (Model-Update-View), lipgloss, standard-library `testing` (table-driven, hand-rolled fixtures).

## Global Constraints

- Read-only over the existing `mru` file — no change to how MRU is written (`store.MRUTouch` and its call sites are untouched).
- Cap the Recent section at **5** resolved entries.
- No new third-party dependencies; stdlib `testing`, hand-rolled fixtures only.
- MRU stores `repo.Path`; resolve by matching `repoRow.repo.Path`.
- `core.LoadMRU(path) ([]string, error)` returns paths **most-recent-first, deduped**; a missing file yields `(nil, nil)`.
- After every task: `gofmt -l .` empty, `go vet ./...` clean, `golangci-lint run` clean, `go test -race ./...` green.

---

### Task 1: Load the MRU into the model

Plumb the MRU path through `nav.Config`, load it on startup, and store the raw paths on the `Model`. No UI yet.

**Files:**
- Modify: `internal/nav/types.go` (add `MRUPath` to `Config`; add `mruLoadedMsg`)
- Modify: `internal/nav/model.go` (add `mru`, `recentSel` fields; wire `loadMRUCmd` into `Init`)
- Modify: `internal/nav/data.go` (add `loadMRUCmd`)
- Modify: `internal/nav/update.go` (handle `mruLoadedMsg`)
- Modify: `cmd/bridge/nav.go` (populate `MRUPath`)
- Test: `internal/nav/data_test.go`, `internal/nav/update_test.go`

**Interfaces:**
- Produces: `Config.MRUPath string`; `mruLoadedMsg{ paths []string }`; `loadMRUCmd(path string) tea.Cmd`; `Model.mru []string`; `Model.recentSel int`.

- [ ] **Step 1: Write the failing test for `loadMRUCmd`**

Add to `internal/nav/data_test.go`:

```go
func TestLoadMRUCmd_ReadsMostRecentFirst(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mru")
	// MRU file: most-recent last. LoadMRU returns most-recent-first, deduped.
	if err := os.WriteFile(path, []byte("/r/a\n/r/b\n/r/a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	msg := loadMRUCmd(path)()
	got, ok := msg.(mruLoadedMsg)
	if !ok {
		t.Fatalf("msg type = %T, want mruLoadedMsg", msg)
	}
	want := []string{"/r/a", "/r/b"}
	if len(got.paths) != len(want) || got.paths[0] != want[0] || got.paths[1] != want[1] {
		t.Errorf("paths = %v, want %v", got.paths, want)
	}
}

func TestLoadMRUCmd_EmptyPath_ReturnsEmpty(t *testing.T) {
	if got := loadMRUCmd("")().(mruLoadedMsg); len(got.paths) != 0 {
		t.Errorf("empty path should yield no paths, got %v", got.paths)
	}
}
```

Ensure `data_test.go` imports `os`, `path/filepath`, `testing`.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/nav/ -run TestLoadMRUCmd -v`
Expected: FAIL — `undefined: loadMRUCmd`, `undefined: mruLoadedMsg`.

- [ ] **Step 3: Add `mruLoadedMsg` and `Config.MRUPath`**

In `internal/nav/types.go`, add to the `Config` struct (next to the other cache paths, e.g. after `RemoteCache`):

```go
	// MRUPath is the most-recently-used repo log (newline-delimited repo paths,
	// most-recent last) read to build the picker's Recent section. Empty disables it.
	MRUPath string
```

In the `// --- messages ---` block of `internal/nav/types.go`, add:

```go
type mruLoadedMsg struct{ paths []string } // most-recent-first, deduped repo paths
```

- [ ] **Step 4: Add `loadMRUCmd`**

In `internal/nav/data.go` (after `loadSessionsCmd`), add:

```go
// loadMRUCmd reads the most-recently-used repo log for the picker's Recent
// section. A missing or unreadable file yields an empty list (no Recent
// section), never an error — recency is a convenience, not a hard dependency.
func loadMRUCmd(path string) tea.Cmd {
	return func() tea.Msg {
		if path == "" {
			return mruLoadedMsg{}
		}
		paths, _ := core.LoadMRU(path)
		return mruLoadedMsg{paths: paths}
	}
}
```

(`core` is already imported in `data.go`.)

- [ ] **Step 5: Run the `loadMRUCmd` tests to verify they pass**

Run: `go test ./internal/nav/ -run TestLoadMRUCmd -v`
Expected: PASS.

- [ ] **Step 6: Write the failing test for storing the message on the model**

Add to `internal/nav/update_test.go`:

```go
func TestUpdate_MRULoadedMsg_StoresPaths(t *testing.T) {
	m := initialModel(Config{})
	out, _ := m.Update(mruLoadedMsg{paths: []string{"/r/a", "/r/b"}})
	got := out.(Model)
	if len(got.mru) != 2 || got.mru[0] != "/r/a" {
		t.Errorf("mru = %v, want [/r/a /r/b]", got.mru)
	}
}
```

- [ ] **Step 7: Run it to verify it fails**

Run: `go test ./internal/nav/ -run TestUpdate_MRULoadedMsg -v`
Expected: FAIL — `m.mru undefined` / unhandled message.

- [ ] **Step 8: Add model fields, Init wiring, and the Update handler**

In `internal/nav/model.go`, add two fields to the `Model` struct (in the picker block, after `sessionSel int`):

```go
	mru       []string // MRU repo paths, most-recent-first (Recent section source)
	recentSel int      // selected row in the Recent section
```

In `Model.Init` (`internal/nav/model.go`), add `loadMRUCmd` to the batch:

```go
func (m Model) Init() tea.Cmd {
	return tea.Batch(
		m.spin.Tick,
		loadLocalReposCmd(m.cfg.ReposRoots),
		loadSessionsCmd(m.cfg.SlotsPath),
		loadRemoteCmd(m.cfg.RemoteCache),
		loadMRUCmd(m.cfg.MRUPath),
	)
}
```

In `internal/nav/update.go`, add a case to the `Update` type switch (next to `sessionsMsg`):

```go
	case mruLoadedMsg:
		m.mru = msg.paths
		return m, nil
```

- [ ] **Step 9: Populate `MRUPath` in the command wiring**

In `cmd/bridge/nav.go`, inside the `nav.Config{...}` literal (next to the other `cacheRoot()` paths), add:

```go
			MRUPath: filepath.Join(cacheRoot(), "mru"),
```

- [ ] **Step 10: Run the full nav suite + build**

Run: `go test -race ./internal/nav/ && go build ./...`
Expected: PASS, build succeeds.

- [ ] **Step 11: Commit**

```bash
git add internal/nav/types.go internal/nav/model.go internal/nav/data.go internal/nav/update.go internal/nav/data_test.go internal/nav/update_test.go cmd/bridge/nav.go
git commit -m "feat(nav): load MRU into picker model (#175)"
```

---

### Task 2: Resolve MRU paths to Recent rows

Add the pure `recentRows()` method: resolve MRU paths to local repo rows, most-recent-first, skip stale paths, cap at 5.

**Files:**
- Modify: `internal/nav/update.go` (add `recentLimit` const + `recentRows`)
- Test: `internal/nav/update_test.go`

**Interfaces:**
- Consumes: `Model.mru []string`, `Model.localRepos []repoRow`, `repoRow.repo.Path`.
- Produces: `const recentLimit = 5`; `(Model).recentRows() []repoRow`.

- [ ] **Step 1: Write the failing test**

Add to `internal/nav/update_test.go`:

```go
func TestRecentRows_ResolvesInMRUOrder_SkipsStale_CapsAt5(t *testing.T) {
	mk := func(name, path string) repoRow {
		return repoRow{label: name, repo: core.Repo{Name: name, Path: path}}
	}
	m := initialModel(Config{})
	m.localRepos = []repoRow{
		mk("a", "/r/a"), mk("b", "/r/b"), mk("c", "/r/c"),
		mk("d", "/r/d"), mk("e", "/r/e"), mk("f", "/r/f"),
	}
	// Includes a stale path (/r/gone) that must be skipped, and 6 valid entries.
	m.mru = []string{"/r/f", "/r/gone", "/r/e", "/r/d", "/r/c", "/r/b", "/r/a"}

	got := m.recentRows()
	if len(got) != 5 {
		t.Fatalf("len = %d, want 5 (capped)", len(got))
	}
	wantOrder := []string{"f", "e", "d", "c", "b"}
	for i, w := range wantOrder {
		if got[i].repo.Name != w {
			t.Errorf("row %d = %q, want %q", i, got[i].repo.Name, w)
		}
	}
}

func TestRecentRows_EmptyWhenNoMatch(t *testing.T) {
	m := initialModel(Config{})
	m.localRepos = []repoRow{{repo: core.Repo{Path: "/r/a"}}}
	m.mru = []string{"/r/missing"}
	if got := m.recentRows(); len(got) != 0 {
		t.Errorf("recentRows = %v, want empty", got)
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/nav/ -run TestRecentRows -v`
Expected: FAIL — `m.recentRows undefined`.

- [ ] **Step 3: Implement `recentRows`**

In `internal/nav/update.go`, add near `visibleRepos` (after it):

```go
// recentLimit caps how many most-recently-used repos the Recent section shows.
const recentLimit = 5

// recentRows resolves the MRU paths to up to recentLimit picker rows for
// currently-known local repos, most-recent first. MRU entries that no longer
// match a local repo (moved/deleted) are skipped so every Recent row is
// openable. Rows are resolved fresh on each call, so they reflect the latest
// localRepos (including async issue-count updates).
func (m Model) recentRows() []repoRow {
	byPath := make(map[string]repoRow, len(m.localRepos))
	for _, r := range m.localRepos {
		byPath[r.repo.Path] = r
	}
	rows := make([]repoRow, 0, recentLimit)
	for _, p := range m.mru {
		r, ok := byPath[p]
		if !ok {
			continue
		}
		rows = append(rows, r)
		if len(rows) == recentLimit {
			break
		}
	}
	return rows
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/nav/ -run TestRecentRows -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/nav/update.go internal/nav/update_test.go
git commit -m "feat(nav): resolve MRU paths to Recent rows (#175)"
```

---

### Task 3: Recent visibility helper

Add `showRecent()` — the single source of truth for whether the Recent section is displayed (and, later, focusable).

**Files:**
- Modify: `internal/nav/update.go` (add `showRecent`)
- Test: `internal/nav/update_test.go`

**Interfaces:**
- Consumes: `Model.filter` (textinput), `(Model).recentRows()`.
- Produces: `(Model).showRecent() bool`.

- [ ] **Step 1: Write the failing test**

Add to `internal/nav/update_test.go`:

```go
func TestShowRecent_Conditions(t *testing.T) {
	base := func() Model {
		m := initialModel(Config{})
		m.localRepos = []repoRow{{label: "a", repo: core.Repo{Name: "a", Path: "/r/a"}}}
		m.mru = []string{"/r/a"}
		return m
	}

	if m := base(); !m.showRecent() {
		t.Errorf("empty filter + a resolved recent row should show Recent")
	}

	m := base()
	m.filter.SetValue("a")
	if m.showRecent() {
		t.Errorf("non-empty filter should hide Recent")
	}

	m2 := initialModel(Config{}) // no localRepos, no mru
	if m2.showRecent() {
		t.Errorf("no resolved recent rows should hide Recent")
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/nav/ -run TestShowRecent -v`
Expected: FAIL — `m.showRecent undefined`.

- [ ] **Step 3: Implement `showRecent`**

In `internal/nav/update.go`, add right after `recentRows`:

```go
// showRecent reports whether the Recent section is currently displayed: only
// when the filter box is empty and at least one MRU entry resolves to a repo.
// Both the view and the focus cycle consult this so they never disagree.
func (m Model) showRecent() bool {
	return m.filter.Value() == "" && len(m.recentRows()) > 0
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/nav/ -run TestShowRecent -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/nav/update.go internal/nav/update_test.go
git commit -m "feat(nav): add showRecent visibility helper (#175)"
```

---

### Task 4: `focusRecent` — Tab-cycle target, navigation, and open

Add `focusRecent` to the focus enum, rebuild the picker Tab-cycle so it includes Recent (when shown) and skips it (when hidden), and handle ↑/↓/enter/`/` within the Recent section.

**Files:**
- Modify: `internal/nav/types.go` (add `focusRecent`)
- Modify: `internal/nav/update.go` (replace `cyclePickerFocus`/`cyclePickerFocusBack` with `pickerFocusCycle` + `rotatePickerFocus`; update Tab/Shift+Tab call sites; add a `focusRecent` key block)
- Test: `internal/nav/update_test.go`

**Interfaces:**
- Consumes: `(Model).showRecent()`, `(Model).recentRows()`, `Model.recentSel`, `(Model).openRepoRow(repoRow)`.
- Produces: `focusRecent focus`; `(Model).pickerFocusCycle() []focus`; `(Model).rotatePickerFocus(dir int) Model`.

- [ ] **Step 1: Write the failing tests**

Add to `internal/nav/update_test.go`:

```go
func TestUpdatePicker_TabIncludesRecent_WhenShown(t *testing.T) {
	m := initialModel(Config{})
	m.localRepos = []repoRow{{label: "a", repo: core.Repo{Name: "a", Path: "/r/a"}}}
	m.mru = []string{"/r/a"}
	// filter empty + one resolved recent row => Recent is shown.
	steps := []focus{focusRecent, focusList, focusFilter} // no sessions
	cur := m
	for i, want := range steps {
		out, _ := cur.Update(tea.KeyMsg{Type: tea.KeyTab})
		cur = out.(Model)
		if cur.pickerFocus != want {
			t.Fatalf("tab #%d focus=%d, want %d", i+1, cur.pickerFocus, want)
		}
	}
}

func TestUpdatePicker_TabSkipsRecent_WhenHidden(t *testing.T) {
	m := initialModel(Config{})
	m.localRepos = []repoRow{{label: "a", repo: core.Repo{Name: "a", Path: "/r/a"}}}
	// No mru => nothing resolves => Recent hidden => Tab must not land on it.
	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	if got := out.(Model); got.pickerFocus != focusList {
		t.Fatalf("tab with Recent hidden should go to focusList, got %d", got.pickerFocus)
	}
}

func TestUpdatePicker_RecentEnter_OpensRepo(t *testing.T) {
	m := initialModel(Config{})
	m.localRepos = []repoRow{{label: "bridge", repo: core.Repo{Name: "bridge", Path: "/r/bridge"}}}
	m.mru = []string{"/r/bridge"}
	m.pickerFocus = focusRecent
	m.recentSel = 0
	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	got := out.(Model)
	if got.screen != screenDash || got.repo.Name != "bridge" {
		t.Fatalf("Enter on a Recent row should open it; screen=%d repo=%q", got.screen, got.repo.Name)
	}
}

func TestUpdatePicker_RecentUpAtTop_ReturnsToFilter(t *testing.T) {
	m := initialModel(Config{})
	m.localRepos = []repoRow{{label: "a", repo: core.Repo{Name: "a", Path: "/r/a"}}}
	m.mru = []string{"/r/a"}
	m.pickerFocus = focusRecent
	m.recentSel = 0
	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyUp})
	if got := out.(Model); got.pickerFocus != focusFilter {
		t.Errorf("Up at top of Recent should return to filter, got %d", got.pickerFocus)
	}
}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/nav/ -run TestUpdatePicker_TabIncludesRecent -v`
Expected: FAIL — `undefined: focusRecent`.

- [ ] **Step 3: Add `focusRecent` to the focus enum**

In `internal/nav/types.go`, extend the `focus` const block so the numeric order matches the Tab order (filter → recent → list → sessions):

```go
const (
	focusFilter focus = iota
	focusRecent
	focusList
	focusSessions
)
```

- [ ] **Step 4: Replace the cycle functions with cycle-list + rotate**

In `internal/nav/update.go`, delete `cyclePickerFocus` (lines ~638-658) and `cyclePickerFocusBack` (lines ~680-699) and add in their place:

```go
// pickerFocusCycle is the Tab order of the currently-focusable picker regions:
// the filter, then Recent (when shown), the repo list, and Active sessions
// (when any). Hidden regions are omitted so Tab never lands on them.
func (m Model) pickerFocusCycle() []focus {
	cycle := []focus{focusFilter}
	if m.showRecent() {
		cycle = append(cycle, focusRecent)
	}
	cycle = append(cycle, focusList)
	if len(m.sessions) > 0 {
		cycle = append(cycle, focusSessions)
	}
	return cycle
}

// rotatePickerFocus advances (dir +1, Tab) or reverses (dir -1, Shift+Tab) the
// picker focus through pickerFocusCycle, wrapping around, syncing the filter's
// text-input focus, and clamping the landed region's selection.
func (m Model) rotatePickerFocus(dir int) Model {
	cycle := m.pickerFocusCycle()
	idx := 0
	for i, f := range cycle {
		if f == m.pickerFocus {
			idx = i
			break
		}
	}
	idx = ((idx+dir)%len(cycle) + len(cycle)) % len(cycle)
	m.pickerFocus = cycle[idx]
	if m.pickerFocus == focusFilter {
		m.filter.Focus()
	} else {
		m.filter.Blur()
	}
	switch m.pickerFocus {
	case focusRecent:
		m.recentSel = clampInt(m.recentSel, 0, len(m.recentRows())-1)
	case focusSessions:
		m.sessionSel = clampInt(m.sessionSel, 0, len(m.sessions)-1)
	}
	return m
}
```

- [ ] **Step 5: Update the Tab/Shift+Tab call sites**

In `internal/nav/update.go` `updatePicker`, replace:

```go
	case "tab":
		return m.cyclePickerFocus(), nil
	case "shift+tab":
		return m.cyclePickerFocusBack(), nil
```

with:

```go
	case "tab":
		return m.rotatePickerFocus(1), nil
	case "shift+tab":
		return m.rotatePickerFocus(-1), nil
```

- [ ] **Step 6: Add the `focusRecent` key block**

In `internal/nav/update.go` `updatePicker`, add this block immediately **before** the `if m.pickerFocus == focusSessions {` block (mirrors it):

```go
	if m.pickerFocus == focusRecent {
		rows := m.recentRows()
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
			}
		case "g", "home":
			m.recentSel = 0
		case "G", "end":
			if len(rows) > 0 {
				m.recentSel = len(rows) - 1
			}
		case "/":
			m.pickerFocus = focusFilter
			m.filter.Focus()
		case "enter":
			if m.recentSel >= 0 && m.recentSel < len(rows) {
				return m.openRepoRow(rows[m.recentSel])
			}
		}
		return m, nil
	}
```

- [ ] **Step 7: Run the new tests + the existing cycle tests**

Run: `go test ./internal/nav/ -run 'TestUpdatePicker_(Tab|ShiftTab|Recent)' -v`
Expected: PASS — including the pre-existing `TestUpdatePicker_TabCyclesFocus` and `TestUpdatePicker_ShiftTabCyclesBack` (their fixtures set no `mru`, so Recent is hidden and the old order is preserved).

- [ ] **Step 8: Run the full nav suite under the race detector**

Run: `go test -race ./internal/nav/`
Expected: PASS.

- [ ] **Step 9: Commit**

```bash
git add internal/nav/types.go internal/nav/update.go internal/nav/update_test.go
git commit -m "feat(nav): make Recent a Tab-cycle focus target (#175)"
```

---

### Task 5: Render the Recent panel

Draw the "Recent" panel above the Repos list in `viewPicker`, shown only when `showRecent()`, mirroring the Active-sessions panel styling.

**Files:**
- Modify: `internal/nav/view.go` (`viewPicker`)
- Test: `internal/nav/view_test.go`

**Interfaces:**
- Consumes: `(Model).showRecent()`, `(Model).recentRows()`, `Model.recentSel`, `Model.pickerFocus` (`focusRecent`), `repoIssueTag`, styles `stSel`/`stAccent`/`stText`/`stMuted`, `panel`.

- [ ] **Step 1: Write the failing tests**

Add to `internal/nav/view_test.go`:

```go
func TestView_Picker_ShowsRecentSection_WhenFilterEmpty(t *testing.T) {
	m := initialModel(Config{})
	m.width, m.height = 100, 30
	m.localRepos = []repoRow{{label: "github/public/bridge", repo: core.Repo{Name: "bridge", Path: "/r/bridge"}}}
	m.mru = []string{"/r/bridge"}
	out := m.viewPicker()
	if !strings.Contains(out, "Recent") {
		t.Errorf("picker view should show the Recent section:\n%s", out)
	}
}

func TestView_Picker_HidesRecentSection_WhenFiltering(t *testing.T) {
	m := initialModel(Config{})
	m.width, m.height = 100, 30
	m.localRepos = []repoRow{{label: "github/public/bridge", repo: core.Repo{Name: "bridge", Path: "/r/bridge"}}}
	m.mru = []string{"/r/bridge"}
	m.filter.SetValue("brid")
	out := m.viewPicker()
	if strings.Contains(out, "Recent") {
		t.Errorf("Recent section must collapse while filtering:\n%s", out)
	}
}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./internal/nav/ -run 'TestView_Picker_(Shows|Hides)RecentSection' -v`
Expected: FAIL — no "Recent" panel rendered (first test fails; second passes trivially but keeps us honest).

- [ ] **Step 3: Render the Recent panel**

In `internal/nav/view.go` `viewPicker`, insert this block immediately **after** the Active-sessions `if len(m.sessions) > 0 { ... }` block and **before** the `title := "Repos"` line:

```go
	if m.showRecent() {
		recent := m.recentRows()
		var b strings.Builder
		for i, r := range recent {
			tag := repoIssueTag(r)
			if m.pickerFocus == focusRecent && i == m.recentSel {
				b.WriteString(stSel.Render(stAccent.Render("▸ ")+r.label+tag) + "\n")
			} else {
				b.WriteString("  " + stText.Render(r.label) + tag + "\n")
			}
		}
		sections = append(sections, panel(w, "Recent  "+stMuted.Render("(tab · ⏎ open)"), strings.TrimRight(b.String(), "\n")))
	}
```

(The existing Repos-panel height budget already measures `used` as the height of all prior `sections`, so the appended Recent panel is accounted for automatically — no budget change needed.)

- [ ] **Step 4: Run the view tests to verify they pass**

Run: `go test ./internal/nav/ -run 'TestView_Picker' -v`
Expected: PASS — new Recent tests pass; existing `TestView_Picker_*` stay green (their fixtures set no `mru`, so no Recent panel appears).

- [ ] **Step 5: Full verification**

Run: `gofmt -l internal/nav cmd/bridge && go vet ./... && golangci-lint run && go test -race ./...`
Expected: `gofmt` prints nothing; vet/lint clean; all tests pass.

- [ ] **Step 6: Manual smoke check (non-TTY frame)**

Run: `go run ./cmd/bridge nav --once`
Expected: one picker frame renders to stdout without error. (Whether a Recent panel shows depends on your local `~/.cache/bridge/mru` — its absence is fine.)

- [ ] **Step 7: Commit**

```bash
git add internal/nav/view.go internal/nav/view_test.go
git commit -m "feat(nav): render Recent repos section in picker (#175)"
```

---

## Self-Review

**Spec coverage:**
- Recent section, ≤5, most-recent-first, from `core.LoadMRU` → Tasks 1–2, 5. ✅
- Visible only when filter empty → Task 3 (`showRecent`), Task 5 (render), Task 4 (focus skip). ✅
- Stale paths skipped → Task 2. ✅
- `focusRecent` Tab-cycle target, skipped when hidden, ↑/↓ navigable → Task 4. ✅
- Enter opens via same `openRepoRow` path → Task 4. ✅
- Same label + issue tag as Repos row → Task 5 (reuses `r.label` + `repoIssueTag`). ✅
- No change to MRU writing; no new config/keybinding beyond focus target → whole plan (read-only; `MRUPath` is the only new Config field). ✅
- Repos list / sort / remote handling unchanged → untouched (`visibleRepos`, `sortRepoRows` not modified). ✅
- Tooling gates green → Task 5 Step 5. ✅

**Placeholder scan:** No TBD/TODO; every code step shows complete code. ✅

**Type consistency:** `mruLoadedMsg{paths}`, `loadMRUCmd`, `Model.mru`/`recentSel`, `recentRows`, `showRecent`, `recentLimit`, `focusRecent`, `pickerFocusCycle`, `rotatePickerFocus` are defined once and referenced consistently across tasks. The Task 4 refactor deletes `cyclePickerFocus`/`cyclePickerFocusBack` and both of their only call sites (the Tab/Shift+Tab cases). ✅
