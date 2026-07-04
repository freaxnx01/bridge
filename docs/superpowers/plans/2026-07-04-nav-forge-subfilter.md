# nav forge subfilter (#128) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** From the `bridge nav` repo picker, cycling one key (`ctrl+f`) narrows the visible repo list to a single forge (or `All`), composed as **AND** with the existing text filter, with the option set driven by the forges actually present in the current environment.

**Architecture:** Additive and localized to `internal/nav`. A new session-local `Model.forgeFilter string` (`""` = All). A computed `presentForges()` derives the ordered set of forges present in `localRepos`+`remoteRepos` via `rowParts`; `cycleForge(+1)` advances `forgeFilter` over `[All, …present]` with wrap; `visibleRepos()` applies `matchesForge` before the existing `filterRepos` text match; `viewForgeBar()` renders a segmented bar under the filter. No `core`/`forge` change — forge is already a first-class field on the models. No `cmd/bridge/nav.go` change — the zero value defaults to All.

**Tech Stack:** Go, Cobra, Bubble Tea (charmbracelet), lipgloss; stdlib testing (table-driven, hand-rolled fakes — NO testify/mockery).

## Global Constraints
- gofmt -l . empty; go vet ./... clean; golangci-lint run clean; go test -race ./... green.
- No new dependencies. No core/forge changes (forge is already a field on `core.Repo`/`forge.RepoRef`).
- Session-local scope: `forgeFilter` resets to `All` on each launch (zero value); no persistence.
- Command-Query Separation: `visibleRepos()`/`presentForges()`/`viewForgeBar()` are pure queries and MUST NOT mutate `forgeFilter`. The reset-on-refresh mutation lives in the `Update` message handlers (`normalizeForgeFilter`).
- Single-forge / zero-forge environments must be byte-for-byte unchanged: the bar renders `""`, `ctrl+f` is a no-op, and the picker layout math is unaffected. Existing goldens must stay stable.

---

## File Structure

- **Modify** `internal/nav/types.go` — `forgeOpt` type + `forgeOptOrder` table (next to `repoForgeChoices`, ~line 171-179).
- **Modify** `internal/nav/model.go` — `forgeFilter string` field on `Model` (~line 26, after `sessionSel`).
- **Modify** `internal/nav/format.go` — `matchesForge` helper (next to `filterRepos`, ~line 122-134).
- **Modify** `internal/nav/update.go` — `presentForges`/`forgeSubfilterVisible`/`cycleForge`/`normalizeForgeFilter` helpers (next to `visibleRepos`, ~line 219-224); `visibleRepos` forge match; global `ctrl+f` case (in the `switch msg.String()` at ~line 230-242); `normalizeForgeFilter` calls in the `reposMsg`/`remoteMsg`/`remoteErrMsg` handlers (~line 25-45).
- **Modify** `internal/nav/view.go` — `viewForgeBar`/`forgeSeg`; insert into `viewPicker` after the filter line (~line 106); conditional hint (~line 146).
- **Modify** `internal/nav/*_test.go` — helper/Update tests + a golden flow test + a new golden under `testdata/`.

Nothing in `internal/core`, `internal/forge`, or `cmd/bridge` changes: forge is already stamped on `core.Repo.Forge` / `forge.RepoRef.Forge`, and `forgeFilter` defaults to `""` (All) via the zero value.

---

## Task 1: state + computed helpers (`forgeOpt`, `presentForges`, `cycleForge`, `matchesForge`)

**Files:**
- Modify: `internal/nav/types.go`, `internal/nav/model.go`, `internal/nav/format.go`, `internal/nav/update.go`
- Test: `internal/nav/update_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `internal/nav/update_test.go` (white-box, package `nav`; `core`, `forge`, `tea` are already imported there):

```go
func TestPresentForges_DerivesCanonicalOrder(t *testing.T) {
	m := initialModel(Config{})
	m.localRepos = []repoRow{
		{repo: core.Repo{Forge: "forgejo", Owner: "o", Name: "f"}},
		{repo: core.Repo{Forge: "github", Owner: "o", Name: "g"}},
	}
	m.remoteRepos = []repoRow{
		{remote: &forge.RepoRef{Forge: "ado", Owner: "o", Name: "a"}},
		{remote: &forge.RepoRef{Forge: "github", Owner: "o", Name: "g2"}},
	}
	got := m.presentForges()
	want := []forgeOpt{{"github", "GitHub"}, {"forgejo", "Forgejo"}, {"ado", "ADO"}}
	if len(got) != len(want) {
		t.Fatalf("presentForges() = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestForgeSubfilterVisible_CountGate(t *testing.T) {
	m := initialModel(Config{})
	m.localRepos = []repoRow{{repo: core.Repo{Forge: "github", Owner: "o", Name: "a"}}}
	if m.forgeSubfilterVisible() {
		t.Error("one forge present -> subfilter hidden")
	}
	m.localRepos = append(m.localRepos, repoRow{repo: core.Repo{Forge: "gitlab", Owner: "o", Name: "b"}})
	if !m.forgeSubfilterVisible() {
		t.Error("two forges present -> subfilter visible")
	}
}

func TestCycleForge_WrapForward(t *testing.T) {
	base := func() Model {
		m := initialModel(Config{})
		m.localRepos = []repoRow{
			{repo: core.Repo{Forge: "github", Owner: "o", Name: "a"}},
			{repo: core.Repo{Forge: "gitlab", Owner: "o", Name: "b"}},
		}
		return m
	}
	tests := []struct{ name, start, want string }{
		{"all_to_github", "", "github"},
		{"github_to_gitlab", "github", "gitlab"},
		{"gitlab_wraps_to_all", "gitlab", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := base()
			m.forgeFilter = tt.start
			if got := m.cycleForge(1).forgeFilter; got != tt.want {
				t.Errorf("cycleForge(1) from %q = %q, want %q", tt.start, got, tt.want)
			}
		})
	}
}

func TestCycleForge_NoopWhenSingleOrZeroForge(t *testing.T) {
	m := initialModel(Config{}) // zero forges
	if got := m.cycleForge(1).forgeFilter; got != "" {
		t.Errorf("zero forges cycle should be a no-op, got %q", got)
	}
	m.localRepos = []repoRow{{repo: core.Repo{Forge: "github", Owner: "o", Name: "a"}}}
	if got := m.cycleForge(1).forgeFilter; got != "" {
		t.Errorf("single forge cycle should be a no-op, got %q", got)
	}
}

func TestMatchesForge_AllAndSpecific(t *testing.T) {
	local := repoRow{repo: core.Repo{Forge: "github"}}
	if !matchesForge(local, "") {
		t.Error(`empty forge ("" = All) should match every row`)
	}
	if !matchesForge(local, "github") {
		t.Error("github row should match github scope")
	}
	if matchesForge(local, "gitlab") {
		t.Error("github row must not match gitlab scope")
	}
	remote := repoRow{remote: &forge.RepoRef{Forge: "ado"}}
	if !matchesForge(remote, "ado") {
		t.Error("remote ado row should match ado scope (via rowParts)")
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/nav/ -run 'TestPresentForges|TestForgeSubfilterVisible|TestCycleForge|TestMatchesForge' -v`
Expected: FAIL — `forgeOpt`, `m.presentForges`, `m.forgeSubfilterVisible`, `m.cycleForge`, `m.forgeFilter`, and `matchesForge` are undefined.

- [ ] **Step 3: Add `forgeOpt` + `forgeOptOrder` (`types.go`)**

In `internal/nav/types.go`, immediately after the `repoForgeChoices` table (ends at line 179):

```go
// forgeOpt is one forge subfilter option: the canonical forge key (matched
// against a row's forge) and its display label. Kept next to repoForgeChoices so
// the forge->label mapping lives in one place.
type forgeOpt struct {
	key, label string
}

// forgeOptOrder is the canonical display order of the forge subfilter options.
// presentForges filters it to the forges actually present in the current rows.
var forgeOptOrder = []forgeOpt{
	{"github", "GitHub"},
	{"gitlab", "GitLab"},
	{"forgejo", "Forgejo"},
	{"ado", "ADO"},
}
```

- [ ] **Step 4: Add the `forgeFilter` field (`model.go`)**

In the `Model` struct, after `sessionSel  int` (line 26):

```go
	forgeFilter string // active forge subfilter key ("" = All); session-local, ctrl+f cycles it
```

(No constructor change: `initialModel` leaves it at the `""` zero value, which is All.)

- [ ] **Step 5: Add `matchesForge` (`format.go`)**

In `internal/nav/format.go`, immediately after `filterRepos` (ends at line 134):

```go
// matchesForge reports whether row r belongs to forge, using rowParts so remote
// (clone-on-select) rows match on their forge ref too. An empty forge ("" = All)
// matches every row.
func matchesForge(r repoRow, forge string) bool {
	if forge == "" {
		return true
	}
	rf, _, _, _ := rowParts(r)
	return rf == forge
}
```

- [ ] **Step 6: Add `presentForges`/`forgeSubfilterVisible`/`cycleForge` (`update.go`)**

In `internal/nav/update.go`, immediately after `visibleRepos` (ends at line 224):

```go
// presentForges returns the forge subfilter options for the forges that have at
// least one repo in the current local+remote rows, in canonical display order
// (GitHub, GitLab, Forgejo, ADO). Environment-driven: an option appears only when
// a matching repo is present, so empty forges never show.
func (m Model) presentForges() []forgeOpt {
	present := map[string]bool{}
	mark := func(rows []repoRow) {
		for _, r := range rows {
			if forge, _, _, _ := rowParts(r); forge != "" {
				present[forge] = true
			}
		}
	}
	mark(m.localRepos)
	mark(m.remoteRepos)
	out := make([]forgeOpt, 0, len(forgeOptOrder))
	for _, o := range forgeOptOrder {
		if present[o.key] {
			out = append(out, o)
		}
	}
	return out
}

// forgeSubfilterVisible reports whether the forge subfilter bar shows and ctrl+f
// does anything: only when more than one forge is present.
func (m Model) forgeSubfilterVisible() bool {
	return len(m.presentForges()) > 1
}

// cycleForge advances the forge subfilter by dir over [All, ...presentForges()]
// with wrap-around, where All is the empty key. A no-op when one forge or fewer is
// present (nothing to cycle). Modeled on cycledDashFocus / cyclePickerFocus.
func (m Model) cycleForge(dir int) Model {
	present := m.presentForges()
	if len(present) <= 1 {
		return m
	}
	keys := make([]string, 0, len(present)+1)
	keys = append(keys, "") // All
	for _, o := range present {
		keys = append(keys, o.key)
	}
	idx := 0
	for i, k := range keys {
		if k == m.forgeFilter {
			idx = i
			break
		}
	}
	idx = ((idx+dir)%len(keys) + len(keys)) % len(keys)
	m.forgeFilter = keys[idx]
	return m
}
```

- [ ] **Step 7: Run tests + commit**

Run: `go test ./internal/nav/ -run 'TestPresentForges|TestForgeSubfilterVisible|TestCycleForge|TestMatchesForge' -v && go test ./internal/nav/`
Expected: PASS (new) and the full nav package still green (nothing is wired into filtering/view/control yet, so behaviour is unchanged).

```bash
gofmt -l internal/nav/ ; go vet ./internal/nav/
git add internal/nav/types.go internal/nav/model.go internal/nav/format.go internal/nav/update.go internal/nav/update_test.go
git commit -m "feat(nav): forge subfilter state + computed helpers

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 2: filtering (AND) + control (`ctrl+f`) + reset-on-refresh

**Files:**
- Modify: `internal/nav/update.go`
- Test: `internal/nav/update_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `internal/nav/update_test.go`:

```go
// multiForgeModel is a picker at focusFilter with one github and one gitlab repo.
func multiForgeModel() Model {
	m := initialModel(Config{})
	m.localRepos = []repoRow{
		{label: "github/public/bridge", repo: core.Repo{Forge: "github", Owner: "o", Name: "bridge"}},
		{label: "github/public/agent", repo: core.Repo{Forge: "github", Owner: "o", Name: "agent"}},
		{label: "gitlab/o/bridge-gl", repo: core.Repo{Forge: "gitlab", Owner: "o", Name: "bridge-gl"}},
	}
	return m
}

func TestVisibleRepos_ForgeAndTextAND(t *testing.T) {
	m := multiForgeModel()
	if got := len(m.visibleRepos()); got != 3 {
		t.Fatalf("All + empty filter should show all 3 rows, got %d", got)
	}
	m.forgeFilter = "github"
	if got := len(m.visibleRepos()); got != 2 {
		t.Errorf("github scope should show 2 github rows, got %d", got)
	}
	m.filter.SetValue("bridge")
	rows := m.visibleRepos()
	if len(rows) != 1 || rows[0].label != "github/public/bridge" {
		t.Errorf("github AND \"bridge\" should yield only github/public/bridge, got %+v", rows)
	}
}

func TestVisibleRepos_AllScopeUnchanged(t *testing.T) {
	m := multiForgeModel()
	m.forgeFilter = "" // All
	if got := len(m.visibleRepos()); got != 3 {
		t.Errorf("All scope should not drop any row, got %d", got)
	}
}

func TestUpdatePicker_CtrlF_CyclesFromFilterFocus(t *testing.T) {
	m := multiForgeModel() // pickerFocus == focusFilter (initial)
	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlF})
	got := out.(Model)
	if got.pickerFocus != focusFilter {
		t.Errorf("ctrl+f must not change focus, got %d", got.pickerFocus)
	}
	if got.forgeFilter != "github" {
		t.Errorf("ctrl+f from All should advance to first present forge, got %q", got.forgeFilter)
	}
}

func TestUpdatePicker_CtrlF_WorksWhileTypingFilter(t *testing.T) {
	m := multiForgeModel()
	m.filter.SetValue("br")
	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlF})
	got := out.(Model)
	if got.forgeFilter != "github" {
		t.Errorf("ctrl+f should cycle even with filter text present, got %q", got.forgeFilter)
	}
	if got.filter.Value() != "br" {
		t.Errorf("ctrl+f must not be captured as filter text, got %q", got.filter.Value())
	}
}

func TestUpdatePicker_CtrlF_NoopWhenSingleForge(t *testing.T) {
	m := initialModel(Config{})
	m.localRepos = []repoRow{{label: "github/public/a", repo: core.Repo{Forge: "github", Owner: "o", Name: "a"}}}
	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlF})
	if got := out.(Model).forgeFilter; got != "" {
		t.Errorf("ctrl+f with one forge should be a no-op, got %q", got)
	}
}

func TestUpdate_RemoteMsg_ResetsForgeFilterWhenGone(t *testing.T) {
	m := initialModel(Config{})
	m.localRepos = []repoRow{{repo: core.Repo{Forge: "github", Owner: "o", Name: "a"}}}
	m.remoteRepos = []repoRow{{remote: &forge.RepoRef{Forge: "gitlab", Owner: "o", Name: "g"}}}
	m.forgeFilter = "gitlab"
	// A refresh whose rows no longer contain any gitlab repo.
	out, _ := m.Update(remoteMsg{rows: []repoRow{{remote: &forge.RepoRef{Forge: "github", Owner: "o", Name: "b"}}}})
	if got := out.(Model).forgeFilter; got != "" {
		t.Errorf("forgeFilter should reset to All when the active forge is gone, got %q", got)
	}
}

func TestUpdate_RemoteMsg_KeepsForgeFilterWhenStillPresent(t *testing.T) {
	m := initialModel(Config{})
	m.localRepos = []repoRow{{repo: core.Repo{Forge: "github", Owner: "o", Name: "a"}}}
	m.forgeFilter = "gitlab"
	out, _ := m.Update(remoteMsg{rows: []repoRow{{remote: &forge.RepoRef{Forge: "gitlab", Owner: "o", Name: "g"}}}})
	if got := out.(Model).forgeFilter; got != "gitlab" {
		t.Errorf("forgeFilter should be kept while its forge is still present, got %q", got)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/nav/ -run 'TestVisibleRepos_Forge|TestVisibleRepos_AllScope|TestUpdatePicker_CtrlF|TestUpdate_RemoteMsg_(Resets|Keeps)ForgeFilter' -v`
Expected: FAIL — `visibleRepos` ignores `forgeFilter`; `ctrl+f` is unhandled (falls through to filter text input, so `forgeFilter` stays `""`); `normalizeForgeFilter` undefined so the reset never happens.

- [ ] **Step 3: Apply the forge match in `visibleRepos`**

In `internal/nav/update.go`, replace `visibleRepos` (lines 219-224):

```go
func (m Model) visibleRepos() []repoRow {
	all := append([]repoRow{}, m.localRepos...)
	all = append(all, dedupRemoteRows(m.localRepos, m.remoteRepos)...)
	all = disambiguateOwners(all)
	return filterRepos(all, m.filter.Value())
}
```

with (forge scope applied before the text match — forge ∧ text):

```go
func (m Model) visibleRepos() []repoRow {
	all := append([]repoRow{}, m.localRepos...)
	all = append(all, dedupRemoteRows(m.localRepos, m.remoteRepos)...)
	all = disambiguateOwners(all)
	if m.forgeFilter != "" {
		scoped := make([]repoRow, 0, len(all))
		for _, r := range all {
			if matchesForge(r, m.forgeFilter) {
				scoped = append(scoped, r)
			}
		}
		all = scoped
	}
	return filterRepos(all, m.filter.Value())
}
```

- [ ] **Step 4: Add `normalizeForgeFilter` + wire it into the row-changing handlers**

In `internal/nav/update.go`, add the helper next to `cycleForge` (added in Task 1):

```go
// normalizeForgeFilter clears the forge subfilter back to All when the active
// forge is no longer present in the current rows (e.g. a remote refresh dropped
// the last repo of that forge), so the list never appears mysteriously empty.
// A command (mutates forgeFilter); called from the Update handlers that reassign
// the row slices, never from the pure query helpers.
func (m Model) normalizeForgeFilter() Model {
	if m.forgeFilter == "" {
		return m
	}
	for _, o := range m.presentForges() {
		if o.key == m.forgeFilter {
			return m
		}
	}
	m.forgeFilter = ""
	return m
}
```

Then call it in each handler that reassigns `localRepos`/`remoteRepos`. Replace the `reposMsg` case (lines 25-27):

```go
	case reposMsg:
		m.localRepos = msg.rows
		return m, m.issueCountCmds(msg.rows)
```

with:

```go
	case reposMsg:
		m.localRepos = msg.rows
		m = m.normalizeForgeFilter()
		return m, m.issueCountCmds(msg.rows)
```

Replace the `remoteMsg` case (lines 31-34):

```go
	case remoteMsg:
		m.remoteRepos = msg.rows
		m.remoteState = loadOK
		return m, m.issueCountCmds(msg.rows)
```

with:

```go
	case remoteMsg:
		m.remoteRepos = msg.rows
		m.remoteState = loadOK
		m = m.normalizeForgeFilter()
		return m, m.issueCountCmds(msg.rows)
```

In the `remoteErrMsg` case, add the reset to the partial-success branch (lines 36-42):

```go
		if len(msg.rows) > 0 {
			// Partial success: at least one forge loaded. Show the fresh rows
			// rather than discarding them; the cache would only be staler.
			m.remoteRepos = msg.rows
			m.remoteState = loadPartial
			m = m.normalizeForgeFilter()
			return m, m.issueCountCmds(msg.rows)
		}
```

- [ ] **Step 5: Handle `ctrl+f` globally in `updatePicker`**

In `updatePicker`, the global `switch msg.String()` block (lines 230-242) currently ends with the `shift+tab` case. Add a `ctrl+f` case so it is handled **before** the per-focus switches (including the `focusFilter` text-input path), which is what makes it work while typing in the filter:

```go
	case "shift+tab":
		return m.cyclePickerFocusBack(), nil
	case "ctrl+f":
		m = m.cycleForge(1)
		m.pickerSel = clampInt(m.pickerSel, 0, len(m.visibleRepos())-1)
		return m, nil
	}
```

(Clamping `pickerSel` to the new visible length keeps the cursor from pointing past the end after the scope narrows. `cycleForge` is a no-op when <=1 forge, so a single-forge picker is unchanged.)

- [ ] **Step 6: Run tests + commit**

Run: `go test ./internal/nav/ -run 'TestVisibleRepos_Forge|TestVisibleRepos_AllScope|TestUpdatePicker_CtrlF|TestUpdate_RemoteMsg_(Resets|Keeps)ForgeFilter' -v && go test ./internal/nav/`
Expected: PASS (new) and the full nav package still green (existing focus/filter tests are unaffected — `forgeFilter` defaults to `""`, so `visibleRepos` and the new global case are inert until a forge is selected).

```bash
gofmt -l internal/nav/ ; go vet ./internal/nav/
git add internal/nav/update.go internal/nav/update_test.go
git commit -m "feat(nav): forge subfilter filtering, ctrl+f cycle, reset-on-refresh

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 3: View (`viewForgeBar`) + golden

**Files:**
- Modify: `internal/nav/view.go`
- Test: `internal/nav/flow_test.go` + `internal/nav/testdata/`

- [ ] **Step 1: Write the failing tests**

Append to `internal/nav/flow_test.go` (uses the `session` harness in `navtest_test.go`; `core`, `tea`, `strings` are imported there):

```go
func TestViewForgeBar_MultiForge_Highlights(t *testing.T) {
	m := initialModel(Config{})
	m.width, m.height = 120, 40
	m.localRepos = []repoRow{
		{label: "github/public/a", repo: core.Repo{Forge: "github", Owner: "o", Name: "a"}},
		{label: "gitlab/o/b", repo: core.Repo{Forge: "gitlab", Owner: "o", Name: "b"}},
	}
	bar := stripANSI(m.viewForgeBar())
	for _, want := range []string{"forge:", "[All]", "GitHub", "GitLab"} {
		if !strings.Contains(bar, want) {
			t.Errorf("forge bar missing %q: %q", want, bar)
		}
	}
	m.forgeFilter = "github"
	bar = stripANSI(m.viewForgeBar())
	if !strings.Contains(bar, "[GitHub]") || strings.Contains(bar, "[All]") {
		t.Errorf("active segment should move to [GitHub]: %q", bar)
	}
}

func TestViewForgeBar_SingleForge_Empty(t *testing.T) {
	m := initialModel(Config{})
	m.width, m.height = 120, 40
	m.localRepos = []repoRow{
		{label: "github/public/a", repo: core.Repo{Forge: "github", Owner: "o", Name: "a"}},
		{label: "github/public/b", repo: core.Repo{Forge: "github", Owner: "o", Name: "b"}},
	}
	if got := m.viewForgeBar(); got != "" {
		t.Errorf("single forge should render no bar, got %q", got)
	}
	if strings.Contains(stripANSI(m.viewPicker()), "forge:") {
		t.Errorf("single-forge picker must not show the forge bar")
	}
}

func TestFlow_ForgeBar_Golden(t *testing.T) {
	s := newSession(t, Config{})
	s.send(reposMsg{rows: []repoRow{
		{label: "github/public/bridge", repo: core.Repo{Forge: "github", Owner: "o", Name: "bridge"}},
		{label: "gitlab/o/agent", repo: core.Repo{Forge: "gitlab", Owner: "o", Name: "agent"}},
	}})
	assertGolden(t, "picker_forge_bar", s.frame())
}
```

- [ ] **Step 2: Run to verify it fails**

Run: `go test ./internal/nav/ -run 'TestViewForgeBar|TestFlow_ForgeBar' -v`
Expected: FAIL — `m.viewForgeBar` is undefined (and the golden is missing).

- [ ] **Step 3: Implement `viewForgeBar` + `forgeSeg` and insert them into `viewPicker`**

In `internal/nav/view.go`, add the helpers (near `viewRepoModal`; `stMuted`/`stSel`/`stText`/`stAccent` styles already exist, line 20-27):

```go
// viewForgeBar renders the forge subfilter as a segmented indicator drawn under
// the filter box: "forge:  [All]  GitHub  GitLab" with the active segment
// bracketed and highlighted (stSel, matching the create-repo modal's selected
// style). Returns "" (renders nothing) when one forge or fewer is present, so
// single-forge environments see no change.
func (m Model) viewForgeBar() string {
	if !m.forgeSubfilterVisible() {
		return ""
	}
	segs := []string{m.forgeSeg("", "All")}
	for _, o := range m.presentForges() {
		segs = append(segs, m.forgeSeg(o.key, o.label))
	}
	return stMuted.Render("forge:") + "  " + strings.Join(segs, "  ")
}

// forgeSeg renders one forge subfilter segment: bracketed + highlighted (stSel)
// when it is the active scope, plain otherwise.
func (m Model) forgeSeg(key, label string) string {
	if m.forgeFilter == key {
		return stSel.Render("[" + label + "]")
	}
	return stText.Render(label)
}
```

In `viewPicker`, replace the filter line + `visibleRepos` fetch (lines 105-107):

```go
	var rb strings.Builder
	rb.WriteString(m.filter.View() + "\n\n")
	rows := m.visibleRepos()
```

with (filter line, then the forge bar when visible, then the blank line — so the byte layout is identical to today when the bar is hidden):

```go
	var rb strings.Builder
	rb.WriteString(m.filter.View() + "\n")
	forgeH := 0
	if bar := m.viewForgeBar(); bar != "" {
		rb.WriteString(bar + "\n")
		forgeH = 1
	}
	rb.WriteString("\n")
	rows := m.visibleRepos()
```

Adjust the row budget so the bar's line is accounted for. Change the budget line (line 124):

```go
		maxVisible := m.height - used - 9
```

to:

```go
		maxVisible := m.height - used - 9 - forgeH
```

Finally, make the picker hint mention `ctrl+f` only when the bar shows (so single-forge goldens stay byte-identical). Replace the hint line (line 146):

```go
	sections = append(sections, m.hintLine("↑↓ move · g/G first/last · ⏎ open/attach · / filter · r refresh · ctrl+n new · tab panes · q quit"))
```

with:

```go
	hint := "↑↓ move · g/G first/last · ⏎ open/attach · / filter · r refresh · ctrl+n new · tab panes · q quit"
	if m.forgeSubfilterVisible() {
		hint += " · ctrl+f forge"
	}
	sections = append(sections, m.hintLine(hint))
```

(When the bar is hidden, `forgeH == 0`, no bar line is written, and the hint is unchanged, so the Repos-list windowing math and every existing single-forge golden are byte-for-byte unchanged.)

- [ ] **Step 4: Generate the golden, inspect, confirm**

Run: `go test ./internal/nav/ -run TestViewForgeBar -v` (should PASS now).
Run: `go test ./internal/nav/ -run TestFlow_ForgeBar_Golden -update`
Then: `cat internal/nav/testdata/picker_forge_bar.golden` — confirm the picker shows, under the filter line, a `forge:  [All]  GitHub  GitLab` bar with `[All]` active, the Repos list below, and the hint ending in `· ctrl+f forge`; no ANSI. Eyeball it.
Run without `-update`: `go test ./internal/nav/ -run TestFlow_ForgeBar_Golden`
Expected: PASS. Confirm existing goldens are untouched: `git status --short internal/nav/testdata/` shows only the new `picker_forge_bar.golden`.

- [ ] **Step 5: Full suite + commit**

Run: `go test ./internal/nav/ && gofmt -l internal/nav/ && go vet ./internal/nav/`
Expected: `ok`; no gofmt output; vet clean.

```bash
git add internal/nav/view.go internal/nav/flow_test.go internal/nav/testdata/picker_forge_bar.golden
git commit -m "feat(nav): render the forge subfilter bar on the picker

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 4: Full verification

**Files:** none.

- [ ] **Step 1: Gates**

Run:
```bash
gofmt -l . | grep -v '.worktrees/'   # empty
go vet ./...                          # clean
go test -race ./...                   # all ok
```
Expected: no gofmt output; vet clean; every package `ok`.

- [ ] **Step 2: Golden stability**

Run: `go test ./internal/nav/ -update && git status --short internal/nav/testdata/`
Expected: no diff beyond the already-committed `picker_forge_bar.golden` (the single-forge / overview goldens are unchanged — the bar and hint addition are gated on `forgeSubfilterVisible()`).

- [ ] **Step 3: Lint (best-effort)**

Run: `golangci-lint run ./internal/nav/...` (if installed). Else note it; `go vet` is the gate.

- [ ] **Step 4: Manual smoke (best-effort — needs a multi-forge environment)**

Run:
```bash
just build
bridge nav   # on the picker (multiple forges discovered): a "forge:" bar shows under the filter
```
Confirm: `ctrl+f` cycles `All -> GitHub -> …present… -> All` (from the filter box and from the list), the visible list narrows to the active forge, typing in `filter:` narrows further (AND), and the bar/hint disappear entirely in a single-forge environment.
Expected: the bar highlights the active forge; the list narrows; `All` restores today's behaviour.

- [ ] **Step 5: Report**

Report Steps 1-2 output + the Step 4 manual smoke. No success claims without command output.

---

## Notes for the implementer

- **Zero value is the default:** `forgeFilter == ""` is All, so a fresh launch and every single-forge environment behave exactly as today — no `cmd/bridge/nav.go` wiring is needed.
- **CQS:** `presentForges`/`forgeSubfilterVisible`/`visibleRepos`/`viewForgeBar` are pure queries — they never touch `forgeFilter`. The only mutation is `cycleForge` (from the `ctrl+f` case) and `normalizeForgeFilter` (from the row-changing `Update` handlers).
- **Global `ctrl+f`:** it lives in the `switch msg.String()` at the top of `updatePicker`, *before* the `focusFilter` text-input path, which is precisely why it works while typing in `filter:` — the plain rune `f` would otherwise be captured by the input.
- **AND semantics:** `visibleRepos` applies `matchesForge` first, then the existing `filterRepos` text match — forge scope ∧ text query. `All` (`""`) short-circuits the forge pass, leaving today's list untouched.
- **Layout stability:** the bar is written only when visible and the hint suffix is gated the same way, so `forgeH == 0` and the byte layout (and every existing golden) is unchanged in single-forge/zero-forge environments.
- **Canonical order** is `github, gitlab, forgejo, ado` (`forgeOptOrder`); `presentForges` filters, never reorders. GitLab appears iff GitLab repos are present (resolving the issue's literal-list omission).
- If you hit a blocker, find the fix and note it inline here.
```
