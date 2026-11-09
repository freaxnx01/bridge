# nav: esc and ctrl+c clear the filter value before they quit (#282)

> **Amended 2026-11-09.** The reporter added a requirement on the issue:
> **Ctrl+C** should clear the filter too, alongside `esc`. That conflicts with
> the original acceptance criterion that `ctrl+c` keeps quitting on the first
> press. Decided with the reporter on 2026-11-09:
>
> - `ctrl+c` follows **exactly the same rule as `esc`**: a non-empty filter is
>   cleared, an empty filter quits, from **any** picker focus.
> - The hint line reads `esc/^c clear`.
>
> Line numbers below were re-verified against `main` @ `9fddc65`.

## Problem

`bridge nav`'s picker keeps the typed filter value when you open a second
screen and come back. The `Model` is a value type and none of the return
paths touch `m.filter` — dashboard esc (`internal/nav/update.go:634-637`),
overview esc (`internal/nav/overview.go:47-49`), agents esc
(`internal/nav/agents.go:76-79`) all only reassign `m.screen`. So a filter
like `workflow` survives the round trip verbatim.

That preservation is wanted. What is missing is a cheap way to *undo* it.
Clearing the filter today means holding backspace once character per
character, and the reporter asked for a shortcut instead.

Two details make this sharper than "bind a clear key":

1. **`ctrl+u` already clears the filter — but only sometimes.** `bubbles`'
   textinput binds `ctrl+u` to `DeleteBeforeCursor`
   (`bubbles@v1.0.0/textinput/textinput.go:70-85`), reached through the
   fallthrough at `internal/nav/update.go:557-560`. With the cursor at the
   end that wipes the line. It only works while `pickerFocus == focusFilter`,
   and `ctrl+u` is separately bound to page-up in `focusList`
   (`update.go:588`) and on the dashboard (`update.go:669`).

2. **Returning from a second screen does not focus the filter.** The
   dashboard's esc sets `m.pickerFocus = focusList` (`update.go:636`). So in
   the exact scenario the issue reports, the filter is not focused and no
   textinput binding fires at all. A binding placed only in the `focusFilter`
   block (`update.go:504-560`) would miss the reported case entirely.

3. **`ctrl+c` does nothing in the filter field today.** The picker-global
   switch groups `ctrl+c` with `q` and only quits when
   `m.pickerFocus != focusFilter` (`update.go:408-412`). From `focusFilter`,
   the focus the picker **starts** in, it falls through to the textinput, which
   has no `ctrl+c` binding. Measured on 2026-11-09 with a throwaway test against
   `main`: `ctrl+c` from `focusFilter` returns no command and leaves the value
   unchanged, whether the filter is empty or holds `workflow`. Pressing Ctrl+C
   in the filter field is a dead key, which is what prompted the 2026-11-09
   amendment.

## Solution

Give `esc` and `ctrl+c` the same clear-then-quit behaviour in the picker, and
leave `q` alone.

Today the picker-global switch reads (`internal/nav/update.go:407-414`):

```go
switch msg.String() {
case "ctrl+c", "q":
    if m.pickerFocus != focusFilter {
        return m, tea.Quit
    }
    // fallthrough to filter editing for 'q' typed into the filter
case "esc":
    return m, tea.Quit
```

It becomes: `esc` and `ctrl+c` clear the filter if it holds any value and
stay; with the filter already empty, they quit, from any focus. `q` keeps its
current meaning: it quits from a non-filter focus and types a letter in the
filter.

```go
switch msg.String() {
case "q":
    if m.pickerFocus != focusFilter {
        return m, tea.Quit
    }
    // fallthrough to filter editing for 'q' typed into the filter
case "esc", "ctrl+c":
    if m.filter.Value() != "" {
        m.filter.SetValue("")
        m.pickerSel = 0
        return m, nil
    }
    return m, tea.Quit
```

### Why esc

- It is the conventional "back out of the thing I just did" key, so it needs
  no discovery.
- It is reachable from every picker focus, which is what the reported
  scenario requires.
- It costs no new keybinding.

### Why ctrl+c too, with the identical rule

- It is the reporter's explicit request (issue comment, 2026-11-09), and the
  key is dead in the filter field today (see Problem, point 3), so giving it
  a meaning costs nothing a user relies on.
- "First press clears the line, second press exits" is the established shell
  and REPL convention for `ctrl+c`, so the two-step behaviour is familiar.
- One rule for both keys means one code path (`case "esc", "ctrl+c":`) and no
  focus-dependent special case to remember.

The picker can never become hard to leave. Two presses of `esc` or `ctrl+c`
always quit, whatever the focus or filter state, and `q` from a non-filter
focus still quits on the first press. The price is one extra keypress when a
filter is set. `ctrl+c` from a non-filter focus with an **empty** filter still
quits on the first press, as today.

`ctrl+c` while the legend is open is unaffected: the legend intercept
(`update.go:250-257`) quits on `ctrl+c` before `updatePicker` is reached.

### Why the raw emptiness test

The condition is `m.filter.Value() != ""`, deliberately **not**
`strings.TrimSpace(m.filter.Value()) != ""`.

A filter of nothing but whitespace is a documented degenerate state: the repo
list is unfiltered, because `filterRepos` guards on
`strings.TrimSpace(q) == ""` (`internal/nav/format.go:152-165`), but the
Recent section is hidden, because `recentVisible()` tests exact equality with
`""` (`update.go:399-401`). The multi-term filter spec
(`docs/superpowers/specs/2026-07-04-nav-multi-term-filter-design.md`) calls
this out. Testing raw emptiness means esc also rescues that state instead of
quitting out of it; a `TrimSpace` test would treat `" "` as empty and quit.

For the same reason the clear assigns `""` and never a whitespace string.

### What it deliberately does not touch

- **The forge subfilter.** `m.forgeFilter` (`model.go:29`) is left alone. The
  forge-subfilter spec
  (`docs/superpowers/specs/2026-07-04-nav-forge-subfilter-design.md`, line
  47) states the forge scope is "independent of clearing the text filter";
  resetting it here would contradict that.
- **Focus.** `m.pickerFocus` is unchanged, so clearing from `focusList`
  leaves you in the list, with the full repo set now visible.
- **The other screens.** Dashboard, overview and agents keep their own esc
  handlers — this change is inside `updatePicker` only.
- **The repo modal.** `updatePicker` returns to `updateRepoModal` before the
  switch is reached (`update.go:404-406`), so a modal's esc and ctrl+c are unaffected.

`m.pickerSel = 0` mirrors what the filter's own edit path already does
(`update.go:559`): the visible row set changes, so the old index is
meaningless and the selection returns to the top.

## Discoverability

The picker hint line (`internal/nav/view.go:318-323`) gains an `esc/^c`
entry. It currently ends with `· q quit`; it should read
`· esc/^c clear · q quit`. The `esc/^c` form follows the existing
`r/^r refresh` entry on the same line.

There is no `key.Binding` registry and no `bubbles/help` in this repo —
bindings are raw string switches and the hint line is the only place
shortcuts are listed. The `?` legend (`view.go:59-93`) is a *glyph* legend,
not a keybinding list, so nothing is added there.

Golden files under `internal/nav/testdata/` embed the hint line and must be
regenerated (`go test ./internal/nav -update`, see
`internal/nav/navtest_test.go:118-140`).

## Acceptance criteria

- With a non-empty filter, `esc` **or `ctrl+c`** in the picker clears the
  filter and the app keeps running.
- With an empty filter, `esc` **or `ctrl+c`** in the picker quits, from any
  picker focus, including `focusFilter`, where `ctrl+c` used to do nothing.
- The clear fires regardless of `pickerFocus`, specifically from
  `focusList`, the focus you land on when returning from the dashboard.
- A whitespace-only filter counts as non-empty: `esc`/`ctrl+c` clear it
  rather than quitting.
- Clearing the filter leaves `m.forgeFilter` and `m.pickerFocus` unchanged.
- Clearing the filter resets `m.pickerSel` to 0.
- `q` still quits on the first press from a non-filter focus, even with a
  filter set, and still types a `q` into the filter when the filter is
  focused.
- `esc` with the legend open still closes the legend without touching the
  filter. `ctrl+c` with the legend open still quits.
- `esc`/`ctrl+c` on the dashboard, overview and agents screens are unchanged.
- The picker hint line advertises `esc/^c clear`.
- `gofmt`, `go vet`, `golangci-lint` clean; `go test -race ./...` green with
  goldens regenerated.

## Testing

Per-behaviour tests in `internal/nav/update_test.go`, matching that file's
existing one-function-per-behaviour shape (not table-driven — tables are used
there only for pure helpers such as `format_test.go:309`):

- clears a set filter and does not quit, for `esc` and for `ctrl+c`
- quits when the filter is empty, for `esc` and for `ctrl+c`, including
  `ctrl+c` from `focusFilter`
- clears while `pickerFocus == focusList`
- treats a whitespace-only value as non-empty
- leaves `forgeFilter` untouched
- resets `pickerSel`
- `q` still quits from `focusList` with a filter set, and still types into a
  focused filter
- the legend keeps precedence for both keys

Plus a flow test in `internal/nav/flow_test.go` using the existing `session`
harness (`navtest_test.go:22-45`): type a filter, enter the dashboard, esc
back, esc again, and assert the filter is gone. A second flow test does the
same round trip with `ctrl+c`. `session.key` has no `ctrl+c` case, so that
test sends `tea.KeyMsg{Type: tea.KeyCtrlC}` through `s.send` directly. No test currently asserts the
filter survives that round trip, so this covers both the preservation and the
new clear.

## Out of scope

Noted, not addressed here:

- The stale-focus asymmetry at `update.go:530-533`, where the single-match
  Enter path enters the dashboard without calling `m.filter.Blur()` while the
  multi-match path at `:534-536` does.
- The `ctrl+u` overload (textinput delete-to-start in the filter, page-up in
  the list and dashboard).
- Making the nav palette adaptive, tracked separately.
