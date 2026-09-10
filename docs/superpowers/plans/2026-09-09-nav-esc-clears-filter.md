# nav esc Clears the Picker Filter — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `esc` in `bridge nav`'s picker clear a non-empty filter value instead of quitting, so a filter preserved across a second screen can be dropped in one keypress from any picker focus.

**Architecture:** One behavioural change in the picker-global key switch of `internal/nav/updatePicker`, plus a hint-line entry advertising it. No new state, no new key, no new dependency. `esc` becomes conditional: non-empty filter → clear and stay; empty filter → quit exactly as today.

**Tech Stack:** Go, `charmbracelet/bubbletea`, `charmbracelet/bubbles` textinput, stdlib `testing` with the repo's own white-box `session` harness (`internal/nav/navtest_test.go`).

**Spec:** `docs/superpowers/specs/2026-09-09-nav-esc-clears-filter-design.md`

## Global Constraints

- **The emptiness test is raw: `m.filter.Value() != ""`.** Never `strings.TrimSpace(...)`. A whitespace-only filter is a real degenerate state (list unfiltered via `format.go:152-155`, Recent hidden via `update.go:395`) and esc must clear it, not quit out of it.
- **Clearing assigns the empty string** — `m.filter.SetValue("")`. Never a space or any whitespace.
- **Do not touch `m.forgeFilter`.** `docs/superpowers/specs/2026-07-04-nav-forge-subfilter-design.md` line 47 makes the forge scope explicitly independent of clearing the text filter.
- **Do not touch `m.pickerFocus`.** Clearing from `focusList` leaves you in the list.
- **Do not touch the dashboard, overview or agents esc handlers** (`update.go:629-632`, `overview.go:47-49`, `agents.go:76-79`). The change lives inside `updatePicker` only.
- **Do not change the legend's key precedence** (`update.go:245-252`) — while the legend is open, esc closes the legend and must never reach `updatePicker`.
- No new Go module. No third-party assertion or mocking library — stdlib plus the existing `session` harness.
- Conventional Commits; scope `nav`.
- `gofmt -l .` empty for touched files, `go vet ./...` clean, `golangci-lint run` clean, `go test -race ./...` green.

---

## File Structure

- `internal/nav/update.go` — **modify** at `:408-409`. The `case "esc":` arm of the picker-global switch in `updatePicker`. This is the entire behavioural change.
- `internal/nav/update_test.go` — **modify** (append). Per-behaviour tests for the new arm.
- `internal/nav/view.go` — **modify** at `:322`. The picker hint line gains an `esc clear` entry.
- `internal/nav/flow_test.go` — **modify** (append). One round-trip flow test: filter → dashboard → esc back → esc clears.
- `internal/nav/testdata/*.golden` — **regenerate.** The goldens embed the hint line.

Two tasks, split where a reviewer could reject one and keep the other: Task 1 is the behaviour, Task 2 is the discoverability copy plus the golden churn it causes.

---

### Task 1: esc clears a non-empty filter

**Files:**
- Modify: `internal/nav/update.go:408-409`
- Test: `internal/nav/update_test.go` (append), `internal/nav/flow_test.go` (append)

**Interfaces:**
- Consumes: `Model.filter` (`textinput.Model`, `model.go:22`), `Model.pickerSel` (`model.go:27`), `Model.pickerFocus`, `Model.forgeFilter` (`model.go:29`) — all already in package `nav`.
- Produces: no new exported or unexported identifier. The change is confined to the body of the existing `case "esc":` arm in `func (m Model) updatePicker(msg tea.KeyMsg) (tea.Model, tea.Cmd)`.

- [ ] **Step 1: Write the failing tests**

Append to `internal/nav/update_test.go`. Note the quit assertion idiom already used at `update_test.go:1030`: invoke the returned `tea.Cmd` and compare its message to `tea.Quit()`.

```go
func TestUpdatePicker_Esc_ClearsNonEmptyFilter(t *testing.T) {
	m := initialModel(Config{}) // starts on focusFilter
	m.localRepos = []repoRow{{label: "github/public/bridge"}, {label: "github/public/agent-os"}}
	m.filter.SetValue("workflow")
	m.pickerSel = 3

	out, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	got := out.(Model)

	if got.filter.Value() != "" {
		t.Errorf("filter = %q, want empty after esc", got.filter.Value())
	}
	if got.pickerSel != 0 {
		t.Errorf("pickerSel = %d, want 0 after clearing the filter", got.pickerSel)
	}
	if cmd != nil {
		t.Errorf("esc that clears the filter must not return a command (it would quit)")
	}
}

func TestUpdatePicker_Esc_EmptyFilterStillQuits(t *testing.T) {
	m := initialModel(Config{})
	m.pickerFocus = focusList // filter empty, not focused

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})

	if cmd == nil {
		t.Fatalf("esc with an empty filter should quit")
	}
	if msg := cmd(); msg != tea.Quit() {
		t.Errorf("esc with an empty filter should return tea.Quit, got %#v", msg)
	}
}

func TestUpdatePicker_Esc_ClearsFromFocusList(t *testing.T) {
	// The focus you land on when returning from the dashboard (update.go:631),
	// where the filter is not focused — the case issue #282 reports.
	m := initialModel(Config{})
	m.localRepos = []repoRow{{label: "github/public/bridge"}}
	m.filter.SetValue("workflow")
	m.pickerFocus = focusList

	out, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	got := out.(Model)

	if got.filter.Value() != "" {
		t.Errorf("filter = %q, want empty after esc from focusList", got.filter.Value())
	}
	if got.pickerFocus != focusList {
		t.Errorf("pickerFocus = %d, want focusList (clearing must not move focus)", got.pickerFocus)
	}
	if cmd != nil {
		t.Errorf("esc that clears the filter must not quit")
	}
}

func TestUpdatePicker_Esc_WhitespaceOnlyFilterCountsAsSet(t *testing.T) {
	// A lone space leaves the list unfiltered but hides Recent
	// (recentVisible tests == "" at update.go:395), so esc must clear it.
	m := initialModel(Config{})
	m.filter.SetValue("   ")

	out, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	got := out.(Model)

	if got.filter.Value() != "" {
		t.Errorf("filter = %q, want empty after esc on a whitespace-only value", got.filter.Value())
	}
	if cmd != nil {
		t.Errorf("esc on a whitespace-only filter should clear, not quit")
	}
}

func TestUpdatePicker_Esc_KeepsForgeFilter(t *testing.T) {
	m := initialModel(Config{})
	m.filter.SetValue("workflow")
	m.forgeFilter = "github"

	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	got := out.(Model)

	if got.forgeFilter != "github" {
		t.Errorf("forgeFilter = %q, want %q — the forge scope is independent of the text filter", got.forgeFilter, "github")
	}
}

func TestUpdatePicker_Q_StillQuitsOnFirstPressWithFilterSet(t *testing.T) {
	// esc gaining a second meaning must not make the picker hard to leave:
	// q from a non-filter focus still quits immediately, filter or no filter.
	m := initialModel(Config{})
	m.filter.SetValue("workflow")
	m.pickerFocus = focusList

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})

	if cmd == nil {
		t.Fatalf("q from focusList should quit on the first press")
	}
	if msg := cmd(); msg != tea.Quit() {
		t.Errorf("q from focusList should return tea.Quit, got %#v", msg)
	}
}

func TestUpdatePicker_Esc_WithLegendOpenClosesLegendNotFilter(t *testing.T) {
	// The legend intercept (update.go:245-252) runs ahead of updatePicker.
	m := initialModel(Config{})
	m.filter.SetValue("workflow")
	m.showLegend = true

	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	got := out.(Model)

	if got.showLegend {
		t.Errorf("esc with the legend open should close the legend")
	}
	if got.filter.Value() != "workflow" {
		t.Errorf("filter = %q, want %q — the legend's esc must not reach the filter", got.filter.Value(), "workflow")
	}
}
```

Append to `internal/nav/flow_test.go` — the round trip nothing currently covers:

```go
func TestFlow_FilterSurvivesDashRoundTripThenEscClears(t *testing.T) {
	s := newSession(t, Config{})
	s.send(reposMsg{rows: []repoRow{
		{label: "github/public/dashonly", repo: coreRepo("dashonly", t.TempDir())},
		{label: "github/public/other", repo: coreRepo("other", t.TempDir())},
	}})
	// The picker starts with the filter focused, so type directly.
	for _, r := range "dashonly" {
		s.key(string(r))
	}
	s.key("enter") // single match -> dashboard
	if s.m.screen != screenDash {
		t.Fatalf("screen = %d, want screenDash after enter on a single match", s.m.screen)
	}

	s.key("esc") // back to the picker, focus lands on focusList
	if s.m.screen != screenPicker {
		t.Fatalf("screen = %d, want screenPicker after esc from the dashboard", s.m.screen)
	}
	if s.m.filter.Value() != "dashonly" {
		t.Fatalf("filter = %q, want it preserved across the round trip", s.m.filter.Value())
	}

	s.key("esc") // the new behaviour: clear it
	if s.m.filter.Value() != "" {
		t.Errorf("filter = %q, want empty after esc in the picker", s.m.filter.Value())
	}
	if !strings.Contains(s.frame(), "other") {
		t.Errorf("clearing the filter should reveal every repo again:\n%s", s.frame())
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

```bash
go test ./internal/nav -run 'TestUpdatePicker_Esc|TestUpdatePicker_Q_StillQuits|TestFlow_FilterSurvivesDashRoundTripThenEscClears' -v
```

Expected: FAIL. `TestUpdatePicker_Esc_ClearsNonEmptyFilter`, `_ClearsFromFocusList`, `_WhitespaceOnlyFilterCountsAsSet` and the flow test fail because esc still quits unconditionally, so the filter is never cleared and a non-nil quit command comes back. `_EmptyFilterStillQuits`, `_KeepsForgeFilter`, `_WithLegendOpenClosesLegendNotFilter` and `TestUpdatePicker_Q_StillQuitsOnFirstPressWithFilterSet` should already PASS — they pin behaviour that must not regress.

- [ ] **Step 3: Write the minimal implementation**

In `internal/nav/update.go`, replace the `case "esc":` arm at `:408-409`:

```go
	case "esc":
		return m, tea.Quit
```

with:

```go
	case "esc":
		// Clear a set filter before quitting: the value survives a trip
		// through a second screen (nothing on those return paths touches
		// m.filter), and backspacing it away is the slow way. Raw != ""
		// on purpose — a whitespace-only value leaves the list unfiltered
		// but hides Recent (recentVisible above), so esc must rescue that
		// state too rather than quit out of it. ctrl+c and q above still
		// quit on the first press. The forge subfilter is deliberately
		// independent and stays put.
		if m.filter.Value() != "" {
			m.filter.SetValue("")
			m.pickerSel = 0
			return m, nil
		}
		return m, tea.Quit
```

- [ ] **Step 4: Run the tests to verify they pass**

```bash
go test ./internal/nav -run 'TestUpdatePicker_Esc|TestUpdatePicker_Q_StillQuits|TestFlow_FilterSurvivesDashRoundTripThenEscClears' -v
```

Expected: PASS, all eight.

Then the full package, to catch anything that relied on esc quitting:

```bash
go test -race ./internal/nav
```

Expected: PASS. If a pre-existing test fails here, it is asserting the old unconditional quit — read it before changing anything, and fix the implementation rather than the test unless the test itself encodes the behaviour this plan deliberately replaces.

- [ ] **Step 5: Commit**

```bash
gofmt -w internal/nav/update.go internal/nav/update_test.go internal/nav/flow_test.go
go vet ./internal/nav
git add internal/nav/update.go internal/nav/update_test.go internal/nav/flow_test.go
git commit -m "feat(nav): esc clears the picker filter before it quits

esc in the picker now clears a non-empty filter value and only quits when
the filter is already empty, so a filter preserved across a second screen
can be dropped in one keypress from any picker focus. ctrl+c and q still
quit on the first press; the forge subfilter is left alone.

Closes #282"
```

---

### Task 2: Advertise esc on the picker hint line

**Files:**
- Modify: `internal/nav/view.go:322`
- Test: `internal/nav/view_test.go` (append)
- Regenerate: `internal/nav/testdata/*.golden`

**Interfaces:**
- Consumes: the `hint` string built in `func (m Model) viewPicker() string` at `view.go:318-323`, and `m.hintLine(...)`.
- Produces: no new identifier. Copy change only.

- [ ] **Step 1: Write the failing test**

Append to `internal/nav/view_test.go`:

```go
func TestViewPicker_HintLine_AdvertisesEscClear(t *testing.T) {
	m := initialModel(Config{})
	m.width, m.height = 120, 40
	m.localRepos = []repoRow{{label: "github/public/bridge"}}

	got := stripANSI(m.View())
	if !strings.Contains(got, "esc clear") {
		t.Errorf("picker hint line should advertise \"esc clear\":\n%s", got)
	}
	if !strings.Contains(got, "q quit") {
		t.Errorf("picker hint line should still advertise \"q quit\":\n%s", got)
	}
}
```

`view_test.go` already imports `strings` (`view_test.go:1-11`); `stripANSI` is the existing helper at `internal/nav/navtest_test.go:115`.

- [ ] **Step 2: Run the test to verify it fails**

```bash
go test ./internal/nav -run TestViewPicker_HintLine_AdvertisesEscClear -v
```

Expected: FAIL — the hint line ends `· q quit` with no `esc` entry.

- [ ] **Step 3: Write the minimal implementation**

In `internal/nav/view.go`, change line `:322`:

```go
	hint += " · q quit"
```

to:

```go
	hint += " · esc clear · q quit"
```

Leave the base hint at `:318` and the conditional `· ctrl+f forge` at `:319-321` untouched, so `esc clear` sits immediately before `q quit` in every variant.

- [ ] **Step 4: Run the test, then regenerate the goldens**

```bash
go test ./internal/nav -run TestViewPicker_HintLine_AdvertisesEscClear -v
```

Expected: PASS.

The goldens embed the hint line, so they now mismatch:

```bash
go test ./internal/nav -update
git diff --stat internal/nav/testdata
```

Expected: only the hint line differs in each changed golden — every diff hunk adds `· esc clear` and nothing else. **Read the diff before staging it.** A golden diff touching anything but the hint line means the copy change had a layout side effect (the longer hint wrapping or truncating at width 120); if that happens, stop and report it rather than accepting the golden.

- [ ] **Step 5: Verify the full suite, then commit**

```bash
gofmt -l internal/nav
go vet ./...
golangci-lint run
go test -race ./...
```

Expected: `gofmt -l` prints nothing for `internal/nav`, vet and lint clean, full suite green.

```bash
git add internal/nav/view.go internal/nav/view_test.go internal/nav/testdata
git commit -m "docs(nav): advertise esc clear on the picker hint line

The picker's hint line is the only place nav lists shortcuts — there is no
key.Binding registry and the ? legend is glyph-only — so the new esc
behaviour is announced there. Goldens regenerated for the copy change.

Refs #282"
```

---

## Verification checklist

Run before calling the work done — evidence, not assertion:

```bash
gofmt -l .            # nothing under internal/nav
go vet ./...
golangci-lint run
go test -race ./...
```

Then confirm by hand against the acceptance criteria in the spec: `bridge nav`, type a filter, press enter into a repo, esc back to the picker, and check that one esc clears the filter and a second esc quits.
