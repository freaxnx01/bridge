# nav TUI Test Harness Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a test-only `session` driver to `internal/nav` that scripts key sequences through the full `Model → Update → (resolve Cmd) → View` loop and asserts rendered frames (golden + targeted), so interactive TUI flows are covered in CI with no TTY and no new dependency.

**Architecture:** A white-box test helper in package `nav` builds `initialModel(cfg)` at a pinned size, applies messages via `Update`, explicitly resolves returned `tea.Cmd`s (flattening `tea.BatchMsg`), and returns the ANSI-stripped `View()`. Golden snapshots live under `internal/nav/testdata/`, refreshed with an `-update` flag.

**Tech Stack:** Go (stdlib `testing`/`flag`/`regexp`), Bubble Tea (`tea.KeyMsg`/`tea.BatchMsg`), lipgloss (rendering). No new dependency. Spec: `docs/superpowers/specs/2026-06-15-nav-tui-test-harness-design.md`.

---

## File Structure

- **Create** `internal/nav/navtest_test.go` — the harness: `session` type, `newSession`/`send`/`key`/`resolve`/`frame`, `stripANSI`, `assertGolden`, the `-update` flag. Test-only (`_test.go`), package `nav` (white-box).
- **Create** `internal/nav/flow_test.go` — the flow tests that use the harness.
- **Create** `internal/nav/testdata/*.golden` — golden frames (generated via `-update`).

No production code changes (the spec is explicit: this is test infrastructure only).

---

## Task 1: The harness + a smoke test

**Files:**
- Create: `internal/nav/navtest_test.go`

- [ ] **Step 1: Guard against an existing `update` flag**

Run: `grep -rn "flag.Bool\|\"update\"" internal/nav/*_test.go`
Expected: no existing `update` flag. (If one exists, reuse it instead of redeclaring in Step 3.)

- [ ] **Step 2: Write the failing smoke test**

Create `internal/nav/navtest_test.go`:

```go
package nav

import (
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

var update = flag.Bool("update", false, "update golden files")

// session drives a nav Model through a scripted key sequence for flow tests.
// It is white-box (package nav) so it can build the model via initialModel and
// render via the unexported View pipeline. Update is pure and every effect is
// an explicit tea.Cmd, so a driven sequence reproduces the real runtime flow
// without a tea.Program or a TTY.
type session struct {
	t       *testing.T
	m       Model
	lastCmd tea.Cmd
}

// newSession builds the model at a fixed, layout-deterministic size.
func newSession(t *testing.T, cfg Config) *session {
	t.Helper()
	m := initialModel(cfg)
	m.width, m.height = 120, 40
	return &session{t: t, m: m}
}

// send applies one message via Update and records the returned Cmd.
func (s *session) send(msg tea.Msg) {
	s.t.Helper()
	out, cmd := s.m.Update(msg)
	s.m = out.(Model)
	s.lastCmd = cmd
}

// key sends a key press. Special names map to their tea.KeyType; anything else
// is sent as runes (so "o", "j", "/", and typed text all work).
func (s *session) key(k string) {
	s.t.Helper()
	switch k {
	case "enter":
		s.send(tea.KeyMsg{Type: tea.KeyEnter})
	case "esc":
		s.send(tea.KeyMsg{Type: tea.KeyEsc})
	case "tab":
		s.send(tea.KeyMsg{Type: tea.KeyTab})
	case "up":
		s.send(tea.KeyMsg{Type: tea.KeyUp})
	case "down":
		s.send(tea.KeyMsg{Type: tea.KeyDown})
	case "backspace":
		s.send(tea.KeyMsg{Type: tea.KeyBackspace})
	default:
		s.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)})
	}
}

// resolve runs the last recorded Cmd and feeds its message(s) back through
// Update, flattening tea.BatchMsg. No-op if there is no pending Cmd. Explicit
// by design: tests never resolve self-repeating cmds (e.g. spinner tick), so
// there is no infinite loop.
func (s *session) resolve() {
	s.t.Helper()
	cmd := s.lastCmd
	s.lastCmd = nil
	if cmd == nil {
		return
	}
	for _, msg := range flattenCmd(cmd) {
		s.send(msg)
	}
}

// flattenCmd runs a cmd and returns the resulting message(s), expanding a
// tea.BatchMsg (a []tea.Cmd) one level. tea.Quit's message is dropped (control,
// not state). Nested batches are expanded recursively.
func flattenCmd(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	msg := cmd()
	switch m := msg.(type) {
	case nil:
		return nil
	case tea.BatchMsg:
		var out []tea.Msg
		for _, c := range m {
			out = append(out, flattenCmd(c)...)
		}
		return out
	default:
		// tea.Quit returns an internal quitMsg we can't name; it carries no
		// state, so feeding it to Update is harmless (Update ignores unknown
		// msgs). Keep it simple: return the message as-is.
		return []tea.Msg{msg}
	}
}

// frame returns the current View() with ANSI escapes stripped, so golden
// comparisons are stable across terminals/CI regardless of color profile.
func (s *session) frame() string {
	s.t.Helper()
	return stripANSI(s.m.View())
}

var ansiRE = regexp.MustCompile("\x1b\\[[0-9;]*[a-zA-Z]")

func stripANSI(s string) string { return ansiRE.ReplaceAllString(s, "") }

// assertGolden compares got against internal/nav/testdata/<name>.golden,
// rewriting it when -update is set.
func assertGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name+".golden")
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s: %v (run `go test ./internal/nav -update` to create it)", path, err)
	}
	if got != string(want) {
		t.Errorf("frame mismatch for %s\n--- got ---\n%s\n--- want ---\n%s", name, got, string(want))
	}
}

func TestHarness_PickerSmoke(t *testing.T) {
	s := newSession(t, Config{})
	s.send(reposMsg{rows: []repoRow{{label: "github/public/bridge"}, {label: "github/public/agent-os"}}})
	f := s.frame()
	if !strings.Contains(f, "bridge") {
		t.Errorf("picker frame missing seeded repo:\n%s", f)
	}
}
```

- [ ] **Step 3: Run the smoke test to verify it fails, then passes**

Run: `go test ./internal/nav/ -run TestHarness_PickerSmoke -v`
Expected: the file is self-contained (harness + test in one commit), so it should **compile and PASS** immediately. If it fails to compile, fix the harness — the failing-first signal here is "does the harness drive a real frame": temporarily break the assertion substring to `"NOTPRESENT"`, confirm it FAILS showing a rendered picker frame, then restore `"bridge"` and confirm PASS. This proves `frame()` renders real content through the driver.

- [ ] **Step 4: Verify formatting/vet**

Run: `gofmt -l internal/nav/navtest_test.go && go vet ./internal/nav/`
Expected: no gofmt output; vet clean.

- [ ] **Step 5: Commit**

```bash
git add internal/nav/navtest_test.go
git commit -m "test(nav): hand-rolled TUI session harness (drive keys, render frames)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 2: Overview golden flow test

**Files:**
- Create: `internal/nav/flow_test.go`
- Create: `internal/nav/testdata/picker_to_overview.golden` (via `-update`)

- [ ] **Step 1: Write the flow test**

Create `internal/nav/flow_test.go`:

```go
package nav

import (
	"context"
	"testing"

	"github.com/freaxnx01/bridge/internal/overview"
)

// fixedOverview returns a deterministic snapshot so the rendered Overview frame
// is stable for golden comparison (no forge/network).
func fixedOverview() overview.Snapshot {
	return overview.Snapshot{
		Ranked: []overview.RankedItem{
			{Repo: "bridge", Title: "wire REST api skeleton", Value: 4, Effort: 2, Score: 2.0},
			{Repo: "agent-pipeline", Title: "retry flaky deploy step", Value: 5, Effort: 2, Score: 2.5},
		},
		NeedsWeighting: []overview.RankedItem{{Repo: "mgrabber", Title: "investigate rate limits"}},
		Inbox: []overview.Capture{
			{Source: overview.CaptureRepoTodo, Repo: "bridge", Title: "add tests"},
			{Source: overview.CaptureIdeasLab, Title: "kanban for issues"},
		},
	}
}

func TestFlow_PickerToOverview(t *testing.T) {
	s := newSession(t, Config{
		BuildOverview: func(_ context.Context) (overview.Snapshot, error) {
			return fixedOverview(), nil
		},
	})
	// seed the picker, focus the list, open the Overview, resolve the build cmd
	s.send(reposMsg{rows: []repoRow{{label: "github/public/bridge"}}})
	s.m.pickerFocus = focusList
	s.key("o")
	if s.m.screen != screenOverview {
		t.Fatalf("screen = %d, want screenOverview after 'o'", s.m.screen)
	}
	s.resolve() // run buildOverviewCmd -> overviewMsg -> populate
	if s.m.overviewState != loadOK {
		t.Fatalf("overviewState = %d, want loadOK after resolve", s.m.overviewState)
	}
	assertGolden(t, "picker_to_overview", s.frame())
}
```

- [ ] **Step 2: Run without `-update` to confirm it fails (golden missing)**

Run: `go test ./internal/nav/ -run TestFlow_PickerToOverview -v`
Expected: FAIL with "read golden testdata/picker_to_overview.golden: ... (run `go test ./internal/nav -update` to create it)".

- [ ] **Step 3: Generate the golden, then inspect it**

Run: `go test ./internal/nav/ -run TestFlow_PickerToOverview -update`
Then: `cat internal/nav/testdata/picker_to_overview.golden`
Expected: a plain-text two-tier Overview frame containing "What matters now", the ranked rows ("wire REST api skeleton", scores like "2.5"/"2.0"), the "⚖ needs weighting" group, and "Inbox" with the captures. **Eyeball it** — the golden is the spec of correct output; confirm it looks like a sane Overview screen before trusting it. (No ANSI escapes should be present.)

- [ ] **Step 4: Run without `-update` to confirm it passes**

Run: `go test ./internal/nav/ -run TestFlow_PickerToOverview -v`
Expected: PASS (frame matches the committed golden).

- [ ] **Step 5: Commit (including the golden)**

```bash
git add internal/nav/flow_test.go internal/nav/testdata/picker_to_overview.golden
git commit -m "test(nav): golden flow test for picker -> Overview

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 3: Targeted flow tests (nav/back, dash, filter)

**Files:**
- Modify: `internal/nav/flow_test.go`

- [ ] **Step 1: Add the three targeted flow tests**

Append to `internal/nav/flow_test.go`:

```go
import "strings" // add to the existing import block (do not duplicate)

func TestFlow_OverviewNavAndBack(t *testing.T) {
	s := newSession(t, Config{
		BuildOverview: func(_ context.Context) (overview.Snapshot, error) {
			return fixedOverview(), nil
		},
	})
	s.send(reposMsg{rows: []repoRow{{label: "github/public/bridge"}}})
	s.m.pickerFocus = focusList
	s.key("o")
	s.resolve()
	s.key("tab") // ranked -> inbox pane
	if s.m.ovFocus != ovInboxPane {
		t.Errorf("ovFocus = %d, want ovInboxPane after tab", s.m.ovFocus)
	}
	s.key("down") // move within inbox (no panic on bounds)
	s.key("esc")  // back to picker
	if s.m.screen != screenPicker {
		t.Errorf("screen = %d, want screenPicker after esc", s.m.screen)
	}
}

func TestFlow_PickerToDash(t *testing.T) {
	s := newSession(t, Config{})
	// a local repo row (no remote) so enter goes to the dashboard
	s.send(reposMsg{rows: []repoRow{{
		label: "github/public/bridge",
		repo:  coreRepo("bridge", t.TempDir()),
	}}})
	s.m.pickerFocus = focusList
	s.key("enter")
	if s.m.screen != screenDash {
		t.Fatalf("screen = %d, want screenDash after enter", s.m.screen)
	}
	// m.repo is set synchronously by openRepoRow, so the dash header renders
	// before any git Cmd is resolved — assert on that, don't resolve git cmds.
	if !strings.Contains(s.frame(), "bridge") {
		t.Errorf("dash frame missing repo name:\n%s", s.frame())
	}
}

func TestFlow_FilterTyping(t *testing.T) {
	s := newSession(t, Config{})
	s.send(reposMsg{rows: []repoRow{
		{label: "github/public/bridge"},
		{label: "github/public/agent-os"},
	}})
	// The picker starts with the filter focused (initialModel sets
	// pickerFocus = focusFilter), so type directly — no "/" needed (pressing
	// "/" here would insert a literal slash into the filter).
	for _, r := range "agent" {
		s.key(string(r))
	}
	f := s.frame()
	if !strings.Contains(f, "agent-os") || strings.Contains(f, "public/bridge") {
		t.Errorf("filter should show only agent-os, got:\n%s", f)
	}
}
```

Add a tiny `core.Repo` constructor helper at the bottom of `flow_test.go` (keeps the test readable and imports `core`):

```go
import "github.com/freaxnx01/bridge/internal/core" // add to the import block

func coreRepo(name, path string) core.Repo {
	return core.Repo{Name: name, Path: path, Forge: "github", Owner: "freaxnx01"}
}
```

Note: merge the `strings` and `core` imports into `flow_test.go`'s existing
import block (don't add separate `import` lines); run `gofmt -w`.

- [ ] **Step 2: Run the new flow tests**

Run: `go test ./internal/nav/ -run 'TestFlow_OverviewNavAndBack|TestFlow_PickerToDash|TestFlow_FilterTyping' -v`
Expected: PASS (all three).

If `TestFlow_FilterTyping` fails because the filter narrowing key path differs from the assumption (filter applies on each keystroke via `m.filter.Update`), inspect `internal/nav/update.go`'s `focusFilter` branch and adjust the test to match the real filter behavior (e.g. the visible-rows accessor). Do NOT change production code — fix the test's expectation to match actual behavior.

- [ ] **Step 3: Full nav suite + format/vet**

Run: `go test ./internal/nav/ && gofmt -l internal/nav/ && go vet ./internal/nav/`
Expected: `ok`; no gofmt output; vet clean.

- [ ] **Step 4: Commit**

```bash
git add internal/nav/flow_test.go
git commit -m "test(nav): flow tests for Overview nav/back, picker->dash, filter

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 4: Verification + golden stability

**Files:** none.

- [ ] **Step 1: Golden stability — regenerate and confirm no drift**

Run:
```bash
go test ./internal/nav/ -update      # rewrite goldens
git status --short internal/nav/testdata/   # expect: NO changes (goldens already current)
go test ./internal/nav/                # passes without -update
```
Expected: `git status` shows no diff after `-update` (the committed golden already matches), and the suite passes without `-update`. A diff here means rendering is non-deterministic — investigate before proceeding.

- [ ] **Step 2: Format / vet / race gates**

Run:
```bash
gofmt -l internal/nav/
go vet ./internal/nav/
go test -race ./internal/nav/
```
Expected: no gofmt output; vet clean; race suite `ok`.

- [ ] **Step 3: Lint (best-effort)**

Run: `golangci-lint run ./internal/nav/...` (if installed). Expect clean for the new test files. If not installed, note it — `go vet` is the gate.

- [ ] **Step 4: Report**

Report the actual command output for Steps 1-3. Confirm the golden round-trips cleanly (the determinism guarantee). Do not claim success without the output.

---

## Notes for the implementer

- **Test-only, no production changes.** Everything lives in `*_test.go` + `testdata/`. If a frame renders non-deterministically, that's a separate bug — report it, don't paper over it in the harness.
- **Determinism = pinned size (120×40) + ANSI strip.** If a golden diff shows stray escape sequences, widen the `ansiRE` character class to cover them rather than relying on terminal color-profile detection.
- **Explicit cmd resolution.** Never auto-resolve in a loop — tests call `resolve()` only for cmds whose follow-up they want (e.g. `buildOverviewCmd`). Self-repeating cmds (spinner tick) are simply not resolved.
- **`enter`→dash** sets `m.repo` synchronously, so assert the dash header without resolving git cmds (which would shell out to `git` and be slow/nondeterministic).
- **Reuse existing test seeding idioms** (`reposMsg`, `pickerFocus = focusList`) — they're how the current `update_test.go` sets up state.
- If you hit a blocker, find the fix and note it inline here for the next run.
```
