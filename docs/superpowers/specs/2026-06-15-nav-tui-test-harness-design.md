# nav TUI test harness: drive key sequences, assert rendered frames

**Date:** 2026-06-15
**Area:** `internal/nav` (test-only harness + flow tests; `testdata/` golden files)
**Status:** Approved (design)

## Problem

`internal/nav` has strong **unit** coverage — `Update` is pure and table-tested,
and `runOnce`/`TestRun_Once_RendersFrameNoTTY` renders a *single* initial frame
headlessly. The `e2e/` harness builds the real binary but only exercises the
**CLI** contract (stdout/exit-code/shim), not the interactive TUI.

The missing layer is **interactive flow testing**: driving a *sequence* of
keypresses through the full `Model → Update → (resolve Cmd) → Update → View`
loop and asserting the rendered frame. This is the exact gap that left the
recent `o`→Overview screen unverifiable headlessly — the unit tests prove the
state transitions, but nothing renders the screen that results from pressing a
key, resolving its command, and re-rendering.

## Goal (success criterion)

From a test, script a key sequence (e.g. `o` → resolve the build command →
land on the Overview) and assert the resulting rendered frame — deterministically,
in plain `go test`, with no TTY and no new dependency. Concretely: a flow test
presses `o`, resolves `buildOverviewCmd`, and golden-matches the two-tier
Overview frame; future screens each get at least one such flow test.

## Decisions (from brainstorming)

1. **Approach: hand-rolled in-repo driver (no new dependency).** Fits the
   project's "stdlib + hand-rolled fakes" ethos and extends the existing
   `runOnce`/pure-`Update` precedent. `teatest` (new experimental `x/exp` dep)
   and PTY/real-binary driving (`creack/pty`) are rejected for now — a single
   PTY launch-smoke is a possible *future* follow-up, not part of this work.
2. **Assertions: both.** Golden snapshots for whole-screen flow tests; targeted
   substring/structural asserts for pinpoint behaviors.
3. **Determinism via ANSI-stripping**, not color-profile detection. Pin
   width/height and strip ANSI escapes before golden compare, so frames are
   stable plain text regardless of terminal/CI environment.
4. **Explicit cmd resolution.** The test decides when to resolve a returned
   `tea.Cmd`, so self-repeating cmds (spinner tick) never loop.

## Architecture

A **test-only** harness lives in `internal/nav` as `*_test.go` (white-box,
package `nav`) — it ships nothing and adds no dependency. It builds
`initialModel(cfg)` at a pinned size and drives it:

```
   session{ model, lastCmd, w, h }
     key("o")  ─► Update(KeyMsg) ─► store lastCmd
     resolve() ─► run lastCmd() ─► flatten tea.BatchMsg ─► Update(each msg)
     frame()   ─► strip ANSI from View()  ─► plain text
```

Because nav's `Update` is a pure function of `(model, msg)` and every side
effect is an explicit `tea.Cmd`, a driven sequence reproduces the real runtime
flow without a `tea.Program` or terminal.

### Harness API (test helper, `internal/nav/navtest_test.go`)

```go
// session drives a nav Model through a scripted sequence for flow tests.
type session struct {
	t       *testing.T
	m       Model
	lastCmd tea.Cmd
}

// newSession builds the model at a fixed size (deterministic layout).
func newSession(t *testing.T, cfg Config) *session

// send applies one message via Update and records the returned Cmd.
func (s *session) send(msg tea.Msg)

// key sends a key press (runes like "o"/"j" or specials "enter"/"esc"/"tab").
func (s *session) key(k string)

// resolve runs the last recorded Cmd and feeds its message(s) back through
// Update, flattening tea.BatchMsg. No-op if there is no pending Cmd. Explicit
// by design: the test never resolves self-repeating cmds (e.g. spinner tick),
// so there is no infinite loop.
func (s *session) resolve()

// frame returns the current View() with ANSI escapes stripped — stable plain
// text suitable for golden comparison and substring assertions.
func (s *session) frame() string
```

Notes:
- `key` maps common names to `tea.KeyMsg`: single runes →
  `tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}`; `"enter"`/`"esc"`/`"tab"`/
  `"up"`/`"down"` → the corresponding `tea.Key*` types.
- `resolve` handles the `tea.BatchMsg` case (a batch is `[]tea.Cmd`): run each,
  collect non-nil messages, and apply each through `Update`. `tea.Quit`'s
  message is recognized and ignored (it's a control message, not state).
- Pinned size default `130×42` (matching `runOnce`) via fields on `session`;
  a test may override before the first `frame()`.

### Determinism helper

```go
// stripANSI removes SGR/escape sequences so golden frames are environment-stable.
var ansiRE = regexp.MustCompile("\x1b\\[[0-9;]*[a-zA-Z]")
func stripANSI(s string) string { return ansiRE.ReplaceAllString(s, "") }
```

(Verify the regex covers the escape forms lipgloss emits at the pinned profile;
extend the class if a non-SGR sequence appears. ANSI-stripping is the primary
mechanism precisely so we don't depend on color-profile detection.)

### Golden helper

```go
var update = flag.Bool("update", false, "update golden files")

// assertGolden compares got against testdata/<name>.golden, refreshing it when
// -update is set. Plain text (ANSI already stripped by frame()).
func assertGolden(t *testing.T, name, got string)
```

- Golden files: `internal/nav/testdata/<name>.golden`.
- Refresh: `go test ./internal/nav -update` (local only; CI never passes it).
- A `flag.Bool` named `update` is package-global in the test binary — declare it
  once in the harness file to avoid redeclaration.

## Initial coverage (flow tests)

Representative, not exhaustive — each exercises the driver end-to-end:

| Test | Sequence | Assertion |
|---|---|---|
| `TestFlow_PickerToOverview` | seed repos+issues fake → `key("o")` → `resolve()` | **golden** the two-tier Overview frame |
| `TestFlow_OverviewNavAndBack` | enter Overview → `tab` → `down` → `esc` | targeted: focus moved to inbox, screen back to picker |
| `TestFlow_PickerToDash` | `key("enter")` on a seeded repo row → `resolve()` | targeted: screen is dash, dash content present |
| `TestFlow_FilterTyping` | `key("/")` → type chars | targeted: filter narrows the visible repo list |

The Overview flow uses an injected fake `BuildOverview` returning a fixed
`overview.Snapshot` (no forge/network), so the golden frame is deterministic.
Going forward, **each new nav screen gets at least one golden flow test.**

## CI

No CI change required — these are ordinary tests under `go test ./...`, which the
existing `.github/workflows/go.yml` already runs (and `-race`). Golden files are
committed; `-update` is a local refresh switch never used in CI. If a future
contributor's environment leaks ANSI that the strip regex misses, the golden
diff will catch it immediately (fail-loud), which is the intended behavior.

## Non-goals

- **No `teatest`** (`charmbracelet/x/exp/teatest`) — no new experimental dep.
- **No PTY / real-binary TUI driving** (`creack/pty`) — a possible future
  launch-smoke, explicitly out of scope here.
- **Not testing the real `tea.Program` runtime** (input parsing, async cmd
  scheduling/timing). Acceptable: nav's `Update` is pure and cmds are explicit,
  so the driven sequence is faithful. If a future feature genuinely depends on
  runtime timing, revisit `teatest` for that case only.
- **No production code changes** — this is purely test infrastructure. (If a
  view turns out to render non-deterministically by size, that's a separate
  bug to fix, not part of this harness.)

## Testing (of the harness itself)

The harness is exercised by the flow tests above; it needs no separate unit
tests beyond them. Verify gates: `gofmt -l`, `go vet`, `golangci-lint` (if
available), `go test -race ./internal/nav/` green, and `go test ./internal/nav
-update` produces stable golden files that then pass without `-update`.

## Open questions / follow-ups

- **ANSI strip completeness:** confirm the regex covers everything lipgloss
  emits at the test profile (golden diffs will reveal gaps immediately).
- **Future PTY launch-smoke:** if real-binary TUI confidence is wanted later,
  add one `creack/pty` smoke for the launch path — separate spec.
- **Backfill:** add golden flow tests for the pre-existing screens (full picker,
  dash panes, backlog panes) opportunistically, beyond the initial set.
