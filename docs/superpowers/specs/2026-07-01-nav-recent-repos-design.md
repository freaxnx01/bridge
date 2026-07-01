# Design: "Recent" repos section in the `bridge nav` picker

**Issue:** #175
**Date:** 2026-07-01
**Status:** Approved

## Problem

`bridge nav`'s picker screen lists local + remote repos in a single,
alphabetically-sorted "Repos" list, substring-filtered by the filter box
(`internal/nav/update.go:179` `visibleRepos`; `internal/nav/format.go:122`
`filterRepos`; `internal/nav/format.go:256` `sortRepoRows`). There is no fast
path to the repos you just used — you either scroll or type the name.

An MRU (most-recently-used) persistence layer **already exists and is faithfully
maintained**, but nothing reads it:

- **Writer** — `store.MRUTouch(path, target)` (`internal/store/mru_writer.go:15`)
  appends `repo.Path` on every repo open, from `cmd/bridge/preflight.go:133,158,234`
  and `cmd/bridge/open.go:76-79`, at `filepath.Join(cacheRoot(), "mru")`.
- **Reader** — `core.LoadMRU(path)` (`internal/core/mru.go:11`) returns the paths
  **most-recent-first, deduped**; a missing file yields an empty slice, no error.
- **Consumer** — none. `grep LoadMRU` finds only the definition and its test. The
  picker sorts alphabetically and ignores the MRU entirely.

This is a wiring job: surface the already-persisted MRU as a dedicated section in
the picker.

## Goal

The `bridge nav` picker shows a **"Recent"** section — the 5 most-recently-used
local repos — above the Repos list, visible when the filter box is empty.
Selecting a recent entry opens it through the same path as a normal repo row.

Success criteria — see Acceptance Criteria at the end.

## Design decisions (settled)

| Decision | Choice | Rationale |
|---|---|---|
| Scope | **MRU only** | Highest-value, smallest change — the data already exists. Starred/favorites is a separate, from-scratch subsystem, deferred to its own issue. |
| Presentation | **Separate "Recent" section**, above the Repos list | Mirrors the existing "Active sessions" panel (`internal/nav/view.go:71-88`); clear visual grouping, consistent with current UI. |
| Filter interaction | **Visible only when the filter box is empty** | Recent is a landing shortcut. Once the user types, the view is pure search over the full Repos list; recent repos that match still appear there. No duplicate rows during search. |
| Cap | **5 resolved entries** | As requested in the issue. |
| Data written | **None** | Read-only consumption of the existing `mru` file. No change to how MRU is written. |

## Approach

### 1. `nav.Config` gains `MRUPath`

`internal/nav/types.go:151`. Add a field:

```go
// MRUPath is the most-recently-used repo log (newline-delimited repo paths,
// most-recent last) read to build the picker's Recent section. Empty disables it.
MRUPath string
```

Populate in `cmd/bridge/nav.go` (where `cacheRoot()` is already computed for the
other cache paths) with `filepath.Join(cacheRoot(), "mru")` — the same path the
writers use.

### 2. Load the MRU in `Model.Init`

`internal/nav/model.go:63`. Add a `tea.Cmd` alongside the existing load commands
that reads the MRU and delivers it as a new message. The command is pure I/O; a
missing/empty file is not an error (matches `core.LoadMRU`).

```go
type mruLoadedMsg struct{ paths []string } // most-recent-first, deduped

func loadMRUCmd(path string) tea.Cmd {
    return func() tea.Msg {
        if path == "" {
            return mruLoadedMsg{}
        }
        return mruLoadedMsg{paths: core.LoadMRU(path)}
    }
}
```

`Update` stores the raw paths on the model: `mru []string`. Storing raw paths
(not resolved rows) decouples MRU loading from local-repo loading — the two
commands can complete in either order, and resolution happens at render time
against whatever `localRepos` is current.

### 3. Resolve + cap: `recentRows()`

`internal/nav/update.go` (next to `visibleRepos`). A pure method that turns the
raw MRU paths into displayable rows:

```go
// recentRows returns up to 5 rows for the most-recently-used local repos, in MRU
// order. MRU paths that do not resolve to a currently-known local repo (deleted
// or moved) are skipped, so the section only ever shows openable repos.
func (m Model) recentRows() []repoRow
```

Behaviour:

1. Index `m.localRepos` by `repo.Path` (MRU stores `repo.Path`).
2. Walk `m.mru` in order; for each path that matches a local repo, append that
   `repoRow`; skip unmatched paths (stale entries).
3. Stop at **5** rows.

Reusing the existing `repoRow` (with its resolved `label` and issue tag) means
Recent rows render identically to Repos rows. Remote-only repos never appear in
Recent (MRU only logs opened local paths).

### 4. Render the Recent section: `viewPicker`

`internal/nav/view.go:67`. Insert a "Recent" panel above the Repos panel,
mirroring the Active-sessions panel structure and styling helpers. The section is
rendered **only when both**:

- the filter box is empty (`m.filter.Value() == ""`), and
- `recentRows()` is non-empty.

Otherwise it is omitted entirely (no header, no blank space). Rows use the same
row-rendering helper as the Repos list so labels and issue tags match; the
selection highlight applies when `pickerFocus == focusRecent`.

### 5. Focus + selection: `focusRecent`

`internal/nav/types.go:20-26` and `internal/nav/update.go`. Add a new focus target
`focusRecent` to the picker focus enum and weave it into the existing tab-cycle
`cyclePickerFocus` (`update.go:640`) so the order is
`focusFilter → focusRecent → focusList → focusSessions → …`.

- `focusRecent` is **skipped** in the cycle whenever the Recent section is not
  shown (filter non-empty, or `recentRows()` empty) — the same conditional used to
  render it, extracted into a `showRecent()` helper so render and focus agree.
- While `focusRecent`: `↑`/`↓` move a `recentSel int` within the (≤5) rows,
  `enter` opens `recentRows()[recentSel]` via the existing `openRepoRow`
  (`update.go:672`) — identical open semantics to a Repos-list row (local repos
  enter the dashboard; there is no clone path since Recent is local-only).
- Typing anything into the filter (which requires `focusFilter`) collapses the
  section; on the next focus cycle `focusRecent` is skipped. `recentSel` is clamped
  to the current row count whenever the section is (re)shown.

The main Repos list, its alphabetical sort, and remote/clone handling are
**unchanged**.

## Out of scope

- Starred / favorites (toggle keybinding, new persistence file, display) — a
  separate issue.
- Any change to how the MRU is **written** (`store.MRUTouch` and its call sites).
- GitHub/forge stars API.
- Remote repos in Recent (MRU logs only opened local paths).
- Configurability of the count (fixed at 5) or of section visibility.

## Testing

Standard-library table-driven tests, hand-rolled `repoRow`/`Repo` fixtures — no
new deps. `Update` and the helpers are pure, so they are driven directly without a
terminal.

**`recentRows()` (new test in `internal/nav`):**

- Resolves MRU paths to rows in MRU order (most-recent first).
- Skips MRU paths that do not match any `localRepos` entry (stale/deleted).
- Caps at 5 even when more MRU entries resolve.
- Empty MRU or empty `localRepos` → empty result.

**`showRecent()` / visibility:**

- Shown when filter empty **and** `recentRows()` non-empty.
- Hidden when the filter box has any content.
- Hidden when `recentRows()` is empty even with an empty filter.

**Focus cycling (`cyclePickerFocus`):**

- `focusRecent` is included in the cycle when the section is shown.
- `focusRecent` is skipped when the section is hidden (filter non-empty or no
  resolved rows), so focus never lands on an invisible section.
- `enter` on `focusRecent` opens the selected recent row via the same code path as
  a Repos-list selection.
- `recentSel` stays within bounds when the resolved row count changes.

All must pass under `go test -race ./...`; `gofmt -l .` empty, `go vet ./...`
clean, `golangci-lint run` clean.

## Acceptance Criteria

- [ ] The `bridge nav` picker shows a **"Recent"** section above the Repos list,
      listing up to the **5** most-recently-used local repos in most-recent-first
      order, sourced from the existing `mru` file via `core.LoadMRU`.
- [ ] The Recent section is visible **only when the filter box is empty**; typing
      any filter text collapses it and searches the full Repos list.
- [ ] MRU paths that no longer resolve to a known local repo are **skipped**, so
      every Recent entry is openable.
- [ ] The Recent section is a focus target in the picker tab-cycle
      (`focusFilter → focusRecent → focusList → focusSessions`), navigable with
      `↑`/`↓`; `focusRecent` is **skipped** in the cycle whenever the section is
      hidden.
- [ ] Pressing `enter` on a Recent entry opens that repo through the **same path**
      as selecting it in the Repos list (`openRepoRow` → dashboard).
- [ ] Recent rows render with the same label and issue-count tag as the
      corresponding Repos-list row.
- [ ] No change to how the MRU is written; the feature is read-only over the
      existing `mru` file. No new configuration or keybinding beyond the focus
      target.
- [ ] The Repos list, its alphabetical sort, and remote/clone handling are
      unchanged.
- [ ] `gofmt -l .` empty, `go vet ./...` clean, `golangci-lint run` clean,
      `go test -race ./...` green.
