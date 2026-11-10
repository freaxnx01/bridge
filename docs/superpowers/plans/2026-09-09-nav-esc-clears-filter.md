# nav esc and ctrl+c Clear the Picker Filter — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

> **Amended 2026-11-09:** `ctrl+c` gets the same clear-then-quit rule as `esc`. The hint reads `esc/^c clear`. Line numbers were re-verified against `main` @ `9fddc65`.

**Goal:** In `bridge nav`'s picker, make `esc` and `ctrl+c` clear a non-empty filter value instead of quitting. When the filter is already empty, both quit, from any picker focus. A filter preserved across a second screen can then be dropped in one keypress, and `ctrl+c` stops being a dead key in the filter field.

**Architecture:** One behavioural change in the picker-global key switch of `internal/nav/updatePicker`, plus a hint-line entry advertising it. No new state, no new dependency. `esc` and `ctrl+c` share one arm: non-empty filter → clear and stay; empty filter → quit. `q` is split out of the old `ctrl+c`/`q` arm and keeps its current behaviour.

**Tech Stack:** Go, `charmbracelet/bubbletea`, `charmbracelet/bubbles` textinput, stdlib `testing` with the repo's own white-box `session` harness (`internal/nav/navtest_test.go`).

**Spec:** `docs/superpowers/specs/2026-09-09-nav-esc-clears-filter-design.md`

## Global Constraints

- Use Test-Driven Development for every task: write a failing test first, watch it fail, implement minimally to pass, verify green.
- **The emptiness test is raw: `m.filter.Value() != ""`.** Never `strings.TrimSpace(...)`. A whitespace-only filter is a real degenerate state: the list is unfiltered (`format.go:152-165`) but Recent is hidden (`update.go:399-401`). esc/ctrl+c must clear it, not quit out of it.
- **Clearing assigns the empty string**, via `m.filter.SetValue("")`. Never a space or any other whitespace.
- **Do not touch `m.forgeFilter`.** `docs/superpowers/specs/2026-07-04-nav-forge-subfilter-design.md` line 47 makes the forge scope explicitly independent of clearing the text filter.
- **Do not touch `m.pickerFocus`.** Clearing from `focusList` leaves you in the list.
- **`q` keeps its exact current behaviour.** It quits from a non-filter focus, with or without a filter set, and types a `q` when the filter is focused.
- **Do not touch the dashboard, overview or agents key handlers** (`update.go:632-637`, `overview.go`, `agents.go`), nor the `ctrl+c` quits at `update.go:228` and `:234`. The change lives inside `updatePicker` only.
- **Do not change the legend's key precedence** (`update.go:250-257`). While the legend is open, esc closes it, ctrl+c quits, and neither reaches `updatePicker`.
- No new Go module. No third-party assertion or mocking library: stdlib plus the existing `session` harness.
- Conventional Commits; scope `nav`.
- `gofmt -l .` empty for touched files, `go vet ./...` clean, `golangci-lint run` clean, `go test -race ./...` green.

---

## File Structure

- `internal/nav/update.go`: **modify** at `:407-414`, the `ctrl+c`/`q` and `esc` arms of the picker-global switch in `updatePicker`. This is the entire behavioural change.
- `internal/nav/update_test.go`: **modify** (append). Per-behaviour tests for the new arm.
- `internal/nav/view.go`: **modify** at `:322`. The picker hint line gains an `esc/^c clear` entry.
- `internal/nav/view_test.go`: **modify** (append). Hint-line test.
- `internal/nav/flow_test.go`: **modify** (append). Round-trip flow tests: filter → dashboard → back → esc / ctrl+c clears.
- `internal/nav/testdata/*.golden`: **regenerate.** Six goldens embed the hint line.

Two tasks, split where a reviewer could reject one and keep the other: Task 1 is the behaviour, Task 2 is the discoverability copy plus the golden churn it causes.

---

### Task 1: esc and ctrl+c clear a non-empty filter

**Files:**
- Modify: `internal/nav/update.go:407-414`
- Test: `internal/nav/update_test.go` (append), `internal/nav/flow_test.go` (append)

**Interfaces:**
- Consumes: `Model.filter` (`textinput.Model`, `model.go:22`), `Model.pickerSel` (`model.go:27`), `Model.pickerFocus` (type `focus`, `model.go:19`), `Model.forgeFilter` (`model.go:29`), `Model.showLegend` (`model.go:20`), all in package `nav`.
- Produces: no new exported or unexported identifier. The change is confined to the existing picker-global `switch msg.String()` in `func (m Model) updatePicker(msg tea.KeyMsg) (tea.Model, tea.Cmd)`.

- [ ] **Step 1: Write the failing tests**

Append to `internal/nav/update_test.go`. The quit assertion idiom is the one already used at `update_test.go:1028-1031`: invoke the returned `tea.Cmd` and compare its message to `tea.Quit()`. `clearKeys` drives every clear/quit behaviour through both keys.

```go
// clearKeys are the picker keys that clear a non-empty filter and quit on an
// empty one (#282).
var clearKeys = []struct {
	name string
	msg  tea.KeyMsg
}{
	{"esc", tea.KeyMsg{Type: tea.KeyEsc}},
	{"ctrl+c", tea.KeyMsg{Type: tea.KeyCtrlC}},
}

func isQuit(cmd tea.Cmd) bool {
	return cmd != nil && cmd() == tea.Quit()
}

func TestUpdatePicker_ClearKey_ClearsNonEmptyFilter(t *testing.T) {
	for _, k := range clearKeys {
		t.Run(k.name, func(t *testing.T) {
			m := initialModel(Config{}) // starts on focusFilter
			m.localRepos = []repoRow{{label: "github/public/bridge"}, {label: "github/public/agent-os"}}
			m.filter.SetValue("workflow")
			m.pickerSel = 3

			out, cmd := m.Update(k.msg)
			got := out.(Model)

			if got.filter.Value() != "" {
				t.Errorf("filter = %q, want empty after %s", got.filter.Value(), k.name)
			}
			if got.pickerSel != 0 {
				t.Errorf("pickerSel = %d, want 0 after clearing the filter", got.pickerSel)
			}
			if cmd != nil {
				t.Errorf("%s that clears the filter must not return a command (it would quit)", k.name)
			}
		})
	}
}

func TestUpdatePicker_ClearKey_EmptyFilterQuitsFromEveryFocus(t *testing.T) {
	// focusFilter is the start focus, where ctrl+c used to be a dead key.
	for _, k := range clearKeys {
		for _, pf := range []focus{focusFilter, focusList} {
			t.Run(fmt.Sprintf("%s/focus=%d", k.name, pf), func(t *testing.T) {
				m := initialModel(Config{})
				m.pickerFocus = pf

				_, cmd := m.Update(k.msg)

				if !isQuit(cmd) {
					t.Errorf("%s with an empty filter should quit from focus %d", k.name, pf)
				}
			})
		}
	}
}

func TestUpdatePicker_ClearKey_ClearsFromFocusList(t *testing.T) {
	// The focus you land on when returning from the dashboard (update.go:636),
	// where the filter is not focused: the case issue #282 reports.
	for _, k := range clearKeys {
		t.Run(k.name, func(t *testing.T) {
			m := initialModel(Config{})
			m.localRepos = []repoRow{{label: "github/public/bridge"}}
			m.filter.SetValue("workflow")
			m.pickerFocus = focusList

			out, cmd := m.Update(k.msg)
			got := out.(Model)

			if got.filter.Value() != "" {
				t.Errorf("filter = %q, want empty after %s from focusList", got.filter.Value(), k.name)
			}
			if got.pickerFocus != focusList {
				t.Errorf("pickerFocus = %d, want focusList (clearing must not move focus)", got.pickerFocus)
			}
			if cmd != nil {
				t.Errorf("%s that clears the filter must not quit", k.name)
			}
		})
	}
}

func TestUpdatePicker_ClearKey_WhitespaceOnlyFilterCountsAsSet(t *testing.T) {
	// A lone space leaves the list unfiltered but hides Recent
	// (recentVisible tests == "" at update.go:400), so the key must clear it.
	for _, k := range clearKeys {
		t.Run(k.name, func(t *testing.T) {
			m := initialModel(Config{})
			m.filter.SetValue("   ")

			out, cmd := m.Update(k.msg)
			got := out.(Model)

			if got.filter.Value() != "" {
				t.Errorf("filter = %q, want empty after %s on a whitespace-only value", got.filter.Value(), k.name)
			}
			if cmd != nil {
				t.Errorf("%s on a whitespace-only filter should clear, not quit", k.name)
			}
		})
	}
}

func TestUpdatePicker_ClearKey_KeepsForgeFilter(t *testing.T) {
	for _, k := range clearKeys {
		t.Run(k.name, func(t *testing.T) {
			m := initialModel(Config{})
			m.filter.SetValue("workflow")
			m.forgeFilter = "github"

			out, _ := m.Update(k.msg)
			got := out.(Model)

			if got.forgeFilter != "github" {
				t.Errorf("forgeFilter = %q, want %q: the forge scope is independent of the text filter", got.forgeFilter, "github")
			}
		})
	}
}

func TestUpdatePicker_Q_StillQuitsOnFirstPressWithFilterSet(t *testing.T) {
	// esc/ctrl+c gaining a second meaning must not make the picker hard to
	// leave: q from a non-filter focus still quits immediately.
	m := initialModel(Config{})
	m.filter.SetValue("workflow")
	m.pickerFocus = focusList

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})

	if !isQuit(cmd) {
		t.Errorf("q from focusList should quit on the first press")
	}
}

func TestUpdatePicker_Q_StillTypesIntoFocusedFilter(t *testing.T) {
	m := initialModel(Config{}) // focusFilter
	m.filter.SetValue("se")

	out, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("q")})
	got := out.(Model)

	if isQuit(cmd) {
		t.Fatalf("q typed into the focused filter must not quit")
	}
	if got.filter.Value() != "seq" {
		t.Errorf("filter = %q, want %q", got.filter.Value(), "seq")
	}
}

func TestUpdatePicker_LegendOpen_EscClosesLegendNotFilter(t *testing.T) {
	// The legend intercept (update.go:250-257) runs ahead of updatePicker.
	m := initialModel(Config{})
	m.filter.SetValue("workflow")
	m.showLegend = true

	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	got := out.(Model)

	if got.showLegend {
		t.Errorf("esc with the legend open should close the legend")
	}
	if got.filter.Value() != "workflow" {
		t.Errorf("filter = %q, want %q: the legend's esc must not reach the filter", got.filter.Value(), "workflow")
	}
}

func TestUpdatePicker_LegendOpen_CtrlCStillQuitsWithFilterSet(t *testing.T) {
	m := initialModel(Config{})
	m.filter.SetValue("workflow")
	m.showLegend = true

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})

	if !isQuit(cmd) {
		t.Errorf("ctrl+c with the legend open should still quit, not clear the filter")
	}
}
```

Add `"fmt"` to `update_test.go`'s imports; it is not imported yet. The focus type is the existing `focus` (`types.go:27`, field `Model.pickerFocus` at `model.go:19`).

Append to `internal/nav/flow_test.go`. This is the round trip nothing currently covers, once per clear key:

```go
func TestFlow_FilterSurvivesDashRoundTripThenClearKeyClears(t *testing.T) {
	for _, k := range clearKeys {
		t.Run(k.name, func(t *testing.T) {
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

			s.send(k.msg) // the new behaviour: clear it
			if s.m.filter.Value() != "" {
				t.Errorf("filter = %q, want empty after %s in the picker", s.m.filter.Value(), k.name)
			}
			if !strings.Contains(s.frame(), "other") {
				t.Errorf("clearing the filter should reveal every repo again:\n%s", s.frame())
			}
		})
	}
}
```

`session.key` has no `ctrl+c` case (`navtest_test.go:45-62`), so the clear key goes through `s.send` directly. Do not extend `session.key` for this.

- [ ] **Step 2: Run the tests to verify they fail**

```bash
go test ./internal/nav -run 'TestUpdatePicker_ClearKey|TestUpdatePicker_Q_|TestUpdatePicker_LegendOpen|TestFlow_FilterSurvivesDashRoundTripThenClearKeyClears' -v
```

Expected FAIL:

- `_ClearKey_ClearsNonEmptyFilter`, `_ClearKey_ClearsFromFocusList`, `_ClearKey_WhitespaceOnlyFilterCountsAsSet` and the flow test fail for both keys. Today esc quits unconditionally, and ctrl+c either quits (`focusList`) or does nothing (`focusFilter`); neither clears.
- `_ClearKey_EmptyFilterQuitsFromEveryFocus/ctrl+c/focus=0` fails because ctrl+c is a dead key in `focusFilter`.

Expected PASS already, because they pin behaviour that must not regress:

- `_EmptyFilterQuitsFromEveryFocus` for `esc`, and for `ctrl+c` from `focusList`.
- `_KeepsForgeFilter`, both `_Q_` tests, both `_LegendOpen_` tests.

- [ ] **Step 3: Write the minimal implementation**

In `internal/nav/update.go`, replace the two arms at `:408-414`:

```go
	case "ctrl+c", "q":
		if m.pickerFocus != focusFilter {
			return m, tea.Quit
		}
		// fallthrough to filter editing for 'q' typed into the filter
	case "esc":
		return m, tea.Quit
```

with:

```go
	case "q":
		if m.pickerFocus != focusFilter {
			return m, tea.Quit
		}
		// fallthrough to filter editing for 'q' typed into the filter
	case "esc", "ctrl+c":
		// Clear a set filter before quitting: the value survives a trip
		// through a second screen (nothing on those return paths touches
		// m.filter), and backspacing it away is the slow way. Raw != ""
		// on purpose: a whitespace-only value leaves the list unfiltered
		// but hides Recent (recentVisible above), so the key must rescue
		// that state too rather than quit out of it. A second press quits;
		// q still quits on the first press from a non-filter focus. The
		// forge subfilter is deliberately independent and stays put.
		if m.filter.Value() != "" {
			m.filter.SetValue("")
			m.pickerSel = 0
			return m, nil
		}
		return m, tea.Quit
```

- [ ] **Step 4: Run the tests to verify they pass**

```bash
go test ./internal/nav -run 'TestUpdatePicker_ClearKey|TestUpdatePicker_Q_|TestUpdatePicker_LegendOpen|TestFlow_FilterSurvivesDashRoundTripThenClearKeyClears' -v
```

Expected: PASS, all subtests.

Then the full package, to catch anything that relied on esc/ctrl+c quitting:

```bash
go test -race ./internal/nav
```

Expected: PASS. If a pre-existing test fails here, it asserts the old unconditional quit. Read it before changing anything. Fix the implementation rather than the test, unless the test itself encodes the behaviour this plan deliberately replaces.

- [ ] **Step 5: Commit**

```bash
gofmt -w internal/nav/update.go internal/nav/update_test.go internal/nav/flow_test.go
go vet ./internal/nav
git add internal/nav/update.go internal/nav/update_test.go internal/nav/flow_test.go
git commit -m "feat(nav): esc and ctrl+c clear the picker filter before they quit

esc and ctrl+c in the picker now clear a non-empty filter value and only
quit when the filter is already empty, from any picker focus. A filter
preserved across a second screen can be dropped in one keypress, and
ctrl+c is no longer a dead key in the filter field. q is unchanged; the
forge subfilter is left alone.

Closes #282"
```

---

### Task 2: Advertise esc/^c on the picker hint line

> **Revised in review:** the entry is appended only when `m.filter.Value() != ""`,
> mirroring the adjacent `· ctrl+f forge` guard. With an empty filter the keys
> quit, so the hint would be misleading, and the segment's 15 columns push the
> one-line footer budget over on a narrow terminal. The test below is therefore
> split into a filter-set case and an empty-filter case, and the goldens (both
> rendered with an empty filter) keep their pre-change hint line.

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
func TestViewPicker_HintLine_AdvertisesClearKeys(t *testing.T) {
	m := initialModel(Config{})
	m.width, m.height = 120, 40
	m.localRepos = []repoRow{{label: "github/public/bridge"}}

	got := stripANSI(m.View())
	if !strings.Contains(got, "esc/^c clear") {
		t.Errorf("picker hint line should advertise \"esc/^c clear\":\n%s", got)
	}
	if !strings.Contains(got, "q quit") {
		t.Errorf("picker hint line should still advertise \"q quit\":\n%s", got)
	}
}
```

`view_test.go` already imports `strings`; `stripANSI` is the existing helper at `internal/nav/navtest_test.go:115`.

- [ ] **Step 2: Run the test to verify it fails**

```bash
go test ./internal/nav -run TestViewPicker_HintLine_AdvertisesClearKeys -v
```

Expected: FAIL. The hint line ends `· q quit` with no `esc/^c` entry.

- [ ] **Step 3: Write the minimal implementation**

In `internal/nav/view.go`, change line `:322`:

```go
	hint += " · q quit"
```

to:

```go
	hint += " · esc/^c clear · q quit"
```

Leave the base hint at `:318` and the conditional `· ctrl+f forge` at `:319-321` untouched, so `esc/^c clear` sits immediately before `q quit` in every variant.

- [ ] **Step 4: Run the test, then regenerate the goldens**

```bash
go test ./internal/nav -run TestViewPicker_HintLine_AdvertisesClearKeys -v
```

Expected: PASS.

The goldens embed the hint line, so they now mismatch:

```bash
go test ./internal/nav -update
git diff --stat internal/nav/testdata
```

Expected: only the hint line differs in each changed golden; every diff hunk adds `· esc/^c clear` and nothing else. **Read the diff before staging it.** If a golden diff touches anything but the hint line, the copy change had a layout side effect (the longer hint wrapping or truncating). In that case, stop and report it rather than accepting the golden.

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
git commit -m "docs(nav): advertise esc/^c clear on the picker hint line

The picker's hint line is the only place nav lists shortcuts (there is no
key.Binding registry and the ? legend is glyph-only), so the new esc and
ctrl+c behaviour is announced there. Goldens regenerated for the copy
change.

Refs #282"
```

---

## Verification checklist

Run before calling the work done (evidence, not assertion):

```bash
gofmt -l .            # nothing under internal/nav
go vet ./...
golangci-lint run
go test -race ./...
```

Then confirm by hand against the acceptance criteria in the spec. In `bridge nav`:

1. Type a filter, then press ctrl+c. The filter clears.
2. Press ctrl+c again. nav quits.
3. Repeat with esc after a dashboard round trip.
