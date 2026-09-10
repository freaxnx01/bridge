# nav: esc clears the filter value before it quits (#282)

## Problem

`bridge nav`'s picker keeps the typed filter value when you open a second
screen and come back. The `Model` is a value type and none of the return
paths touch `m.filter` — dashboard esc (`internal/nav/update.go:629-632`),
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
   fallthrough at `internal/nav/update.go:553-556`. With the cursor at the
   end that wipes the line. It only works while `pickerFocus == focusFilter`,
   and `ctrl+u` is separately bound to page-up in `focusList`
   (`update.go:583`) and on the dashboard (`update.go:664`).

2. **Returning from a second screen does not focus the filter.** The
   dashboard's esc sets `m.pickerFocus = focusList` (`update.go:631`). So in
   the exact scenario the issue reports, the filter is not focused and no
   textinput binding fires at all. A binding placed only in the `focusFilter`
   block (`update.go:500-556`) would miss the reported case entirely.

## Solution

Give `esc` a clear-then-quit behaviour in the picker.

`esc` currently quits unconditionally in the picker-global switch
(`internal/nav/update.go:408-409`):

```go
case "esc":
    return m, tea.Quit
```

It becomes: when the filter holds any value, clear it and stay; when the
filter is already empty, quit as before.

```go
case "esc":
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
- It costs no new keybinding and collides with nothing: the picker's other
  quit paths (`ctrl+c` and `q`, `update.go:403-407`) are untouched and still
  quit on the first press, so the picker can never become hard to leave.

The price is one extra keypress: with a filter set, leaving nav via `esc`
takes two presses instead of one. `ctrl+c`/`q` remain the single-press exit.

### Why the raw emptiness test

The condition is `m.filter.Value() != ""`, deliberately **not**
`strings.TrimSpace(m.filter.Value()) != ""`.

A filter of nothing but whitespace is a documented degenerate state: the repo
list is unfiltered, because `filterRepos` guards on
`strings.TrimSpace(q) == ""` (`internal/nav/format.go:152-165`), but the
Recent section is hidden, because `recentVisible()` tests exact equality with
`""` (`update.go:395`). The multi-term filter spec
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
  switch is reached (`update.go:399-401`), so a modal's esc is unaffected.

`m.pickerSel = 0` mirrors what the filter's own edit path already does
(`update.go:555`): the visible row set changes, so the old index is
meaningless and the selection returns to the top.

## Discoverability

The picker hint line (`internal/nav/view.go:318-323`) gains an `esc` entry.
It currently ends with `· q quit`; it should read `· esc clear · q quit`.

There is no `key.Binding` registry and no `bubbles/help` in this repo —
bindings are raw string switches and the hint line is the only place
shortcuts are listed. The `?` legend (`view.go:59-93`) is a *glyph* legend,
not a keybinding list, so nothing is added there.

Golden files under `internal/nav/testdata/` embed the hint line and must be
regenerated (`go test ./internal/nav -update`, see
`internal/nav/navtest_test.go:118-140`).

## Acceptance criteria

- With a non-empty filter, `esc` in the picker clears the filter and the app
  keeps running.
- With an empty filter, `esc` in the picker quits, exactly as before.
- The clear fires regardless of `pickerFocus` — specifically from
  `focusList`, the focus you land on when returning from the dashboard.
- A whitespace-only filter counts as non-empty: `esc` clears it rather than
  quitting.
- Clearing the filter leaves `m.forgeFilter` unchanged.
- Clearing the filter resets `m.pickerSel` to 0.
- `ctrl+c` and `q` still quit on the first press from a non-filter focus.
- Esc on the dashboard, overview and agents screens is unchanged.
- The picker hint line advertises `esc clear`.
- `gofmt`, `go vet`, `golangci-lint` clean; `go test -race ./...` green with
  goldens regenerated.

## Testing

Per-behaviour tests in `internal/nav/update_test.go`, matching that file's
existing one-function-per-behaviour shape (not table-driven — tables are used
there only for pure helpers such as `format_test.go:309`):

- clears a set filter and does not quit
- quits when the filter is empty
- clears while `pickerFocus == focusList`
- treats a whitespace-only value as non-empty
- leaves `forgeFilter` untouched
- resets `pickerSel`

Plus a flow test in `internal/nav/flow_test.go` using the existing `session`
harness (`navtest_test.go:22-45`): type a filter, enter the dashboard, esc
back, esc again, and assert the filter is gone. No test currently asserts the
filter survives that round trip, so this covers both the preservation and the
new clear.

## Out of scope

Noted, not addressed here:

- The stale-focus asymmetry at `update.go:526-529`, where the single-match
  Enter path enters the dashboard without calling `m.filter.Blur()` while the
  multi-match path at `:530-531` does.
- The `ctrl+u` overload (textinput delete-to-start in the filter, page-up in
  the list and dashboard).
- Making the nav palette adaptive, tracked separately.
