# nav Recent repos: surface the MRU on the picker (#183)

**Date:** 2026-07-01
**Area:** `internal/nav` (picker Recent section) + `cmd/bridge` (`RecentPath` wiring)
**Status:** Approved (design)

## Problem

`bridge` already records most-recently-used repos: every `bridge open` (and the
nav open path via `cmd/bridge/preflight.go`) calls `store.MRUTouch` to append the
opened repo's path to an `mru` file under `cacheRoot()`. `core.LoadMRU` reads that
file back — most-recent-first, deduped — and is unit-tested. But **no UI reads
it**: the `bridge nav` picker lists every repo alphabetically and offers no way to
jump straight back to what you were just working on.

Issue **#183** asks to add most-recently-used repos to the filtering screen. The
scope decision on the issue narrows this to **MRU only**: surface the 5 most-recent
local repos as a dedicated **"Recent"** section in the picker, sourced read-only
from the existing `mru` file. Starred/favorites is a separate, from-scratch
subsystem, deferred to its own issue.

## Goal (success criterion)

On the `bridge nav` picker, when the filter box is empty, a **Recent** section
lists up to the 5 most-recently-used local repos (most-recent-first). It is a
focus target in the tab-cycle (`focusFilter → focusRecent → focusList →
focusSessions`), navigable with `↑`/`↓`; pressing `enter` on a Recent entry opens
that repo's dashboard through the exact same `openRepoRow` path as the Repos list.
Typing any filter text collapses the section and searches the full Repos list.

## Decisions (from the issue's scope note)

1. **MRU only** — no starred/favorites. The feature is **read-only** over the
   existing `mru` file; nothing changes about how the MRU is written.
2. **Dedicated "Recent" section**, not merged into the Repos list. It renders
   inside the Repos panel, between the filter line and the alphabetical list, so
   the top-to-bottom focus order (`filter → recent → list`) reads naturally.
3. **Cap at 5**, most-recent-first, in `core.LoadMRU` order.
4. **Resolve-or-skip:** an MRU path is shown only if it matches a discovered local
   repo (by `core.Repo.Path`). Stale paths (repo moved/deleted) are skipped, so
   every Recent entry is openable.
5. **Rows reuse the Repos-list `repoRow`** — the same `label` and the same
   `repoIssueTag` issue-count suffix — by looking the MRU path up in the already
   loaded `m.localRepos`. No second discovery, no divergent formatting.
6. **Filter-empty gate:** the section is visible only when `m.filter.Value() ==
   ""`. Any filter text hides it (and collapses `focusRecent` out of the cycle).
7. **nav stays cmd-layer-free:** the mru file path is injected as
   `Config.RecentPath` (like `RemoteCache`/`SlotsPath`); nil/empty disables the
   section. nav never constructs the path itself.

## Architecture

```
  Init ──► loadRecentCmd(cfg.RecentPath) ──► core.LoadMRU ──► recentMsg{paths}
                                                                   │
                                                        m.mruPaths = paths
                                                                   │
  picker render / key handling:
     m.recentRepos()   = first 5 localRepos whose .repo.Path ∈ mruPaths, MRU order
     m.recentVisible() = filter empty  &&  len(recentRepos) > 0

  focus cycle (tab / ↑↓), focusRecent present only while recentVisible():
     focusFilter ─↓/tab─► focusRecent ─↓/tab─► focusList ─tab─► focusSessions? ─► focusFilter
                    ▲                    │
                    └──── ↑ at top ──────┘
     enter on focusRecent row → m.openRepoRow(recentRepos[recentSel]) → enterDash
```

The Recent rows are **computed on demand** (like `visibleRepos()`), not stored, so
they always reflect the latest `m.localRepos` — including issue counts that arrive
asynchronously via `issueCountMsg` and land on the localRepos rows.

### 1. `internal/nav` — state (`types.go`, `model.go`)

- Insert `focusRecent` into the `focus` const block, **after** `focusFilter`:
  ```go
  const (
      focusFilter focus = iota
      focusRecent
      focusList
      focusSessions
  )
  ```
  (The `focus` values are never persisted, so the renumbering is inert.)
- New message: `type recentMsg struct{ paths []string }`.
- New `Model` fields: `mruPaths []string` (raw MRU order from `recentMsg`) and
  `recentSel int` (selected Recent row).
- New `Config` field:
  ```go
  // RecentPath is the MRU file read (read-only) to build the picker's Recent
  // section. Empty disables the section.
  RecentPath string
  ```

### 2. `internal/nav` — data (`data.go`) + helpers (`update.go`)

- `loadRecentCmd(path string) tea.Cmd` → `core.LoadMRU(path)` → `recentMsg{paths}`
  (a read error is swallowed to empty, matching nav's best-effort loaders).
- `Init` appends `loadRecentCmd(m.cfg.RecentPath)` to its `tea.Batch` when
  `RecentPath != ""`.
- `recentRepos() []repoRow` — indexes `m.localRepos` by `.repo.Path`, walks
  `m.mruPaths` in order, appends each resolved row, caps at 5.
- `recentVisible() bool` — `m.filter.Value() == "" && len(m.recentRepos()) > 0`.

### 3. `internal/nav` — Update (`update.go`)

- `case recentMsg: m.mruPaths = msg.paths`.
- `focusFilter` `KeyDown`: if `recentVisible()` → `focusRecent`, `recentSel = 0`;
  else the existing → `focusList`.
- New `focusRecent` key block (mirrors the `focusSessions` block): `up`/`k`
  (at top → back to `focusFilter`), `down`/`j` (past end → `focusList`),
  `g`/`G`/`home`/`end`, `/` → filter, `enter` →
  `m.openRepoRow(recentRepos()[recentSel])`. A defensive guard reroutes to
  `focusFilter` if the section vanished.
- `focusList` `up`/`k` at `pickerSel <= 0`: step into `focusRecent` (last row)
  when `recentVisible()`, else the existing step into `focusFilter`.
- `cyclePickerFocus` / `cyclePickerFocusBack`: include `focusRecent` between
  filter and list **only when** `recentVisible()`, so it is skipped when hidden.

### 4. `internal/nav` — View (`view.go`)

- `recentBlock() (string, int)` renders a muted `Recent` sub-heading plus the rows
  when `recentVisible()`, highlighting `recentSel` when `pickerFocus ==
  focusRecent`; each row reuses `row.label + repoIssueTag(row)`. Returns the block
  and its line height (0 when hidden).
- `viewPicker` inserts the block after the filter line and subtracts its height
  from the existing `maxVisible` budget so the alphabetical list windowing is
  unchanged in the hidden case.

### 5. `cmd/bridge` — wiring (`nav.go`)

- Add to the `nav.Config{…}` literal:
  ```go
  RecentPath: filepath.Join(cacheRoot(), "mru"),
  ```
  the same `cacheRoot()/mru` path `cmd/bridge/open.go` and `preflight.go` write.

## Edge cases

- **Empty / missing mru file:** `core.LoadMRU` returns empty → `recentRepos()`
  empty → `recentVisible()` false → no section, `focusRecent` skipped.
- **Stale MRU paths** (repo moved/deleted): unmatched paths are skipped; only
  resolvable local repos appear, so every entry opens.
- **Fewer than 5 resolvable entries:** the section shows however many resolve
  (1–5); the cap is an upper bound only.
- **Filter typed while on `focusRecent`:** unreachable — the filter is blurred on
  `focusRecent`; text can only be entered from `focusFilter`, which itself hides
  the section, keeping focus and visibility consistent.
- **`recentSel` out of range** after localRepos change: clamped on `enter` and at
  render; recomputation each keystroke keeps it bounded.
- **`RecentPath` empty / nil:** section disabled end-to-end (no load Cmd, never
  visible, never in the cycle).
- **A Recent repo also shown in the alphabetical list:** intended — Recent is a
  shortcut, not a filter; the repo legitimately appears in both places.

## Acceptance Criteria

- [ ] The `bridge nav` picker shows a **"Recent"** section above the Repos list,
  listing up to the **5** most-recently-used local repos in most-recent-first
  order, sourced from the existing `mru` file via `core.LoadMRU`.
- [ ] The Recent section is visible **only when the filter box is empty**; typing
  any filter text collapses it and searches the full Repos list.
- [ ] MRU paths that no longer resolve to a known local repo are **skipped**, so
  every Recent entry is openable.
- [ ] The Recent section is a focus target in the picker tab-cycle (`focusFilter →
  focusRecent → focusList → focusSessions`), navigable with `↑`/`↓`; `focusRecent`
  is **skipped** in the cycle whenever the section is hidden.
- [ ] Pressing `enter` on a Recent entry opens that repo through the **same path**
  as selecting it in the Repos list (`openRepoRow` → dashboard).
- [ ] Recent rows render with the same label and issue-count tag as the
  corresponding Repos-list row.
- [ ] No change to how the MRU is written; the feature is read-only over the
  existing `mru` file. No new configuration or keybinding beyond the focus target.
- [ ] The Repos list, its alphabetical sort, and remote/clone handling are
  unchanged.
- [ ] `gofmt -l .` empty, `go vet ./...` clean, `golangci-lint run` clean, `go
  test -race ./...` green.

## Testing

- **`internal/nav` (pure helpers):** `recentRepos()` resolves MRU paths against
  seeded `localRepos`, preserves MRU order, skips unknown paths, caps at 5;
  `recentVisible()` is true only with an empty filter and ≥1 resolved row.
- **`internal/nav` (Update):** `KeyDown` from `focusFilter` enters `focusRecent`
  when visible (else `focusList`); `↓` past the last Recent row lands on
  `focusList`, `↑` at the top returns to `focusFilter`; `enter` on a Recent row
  reaches `screenDash` with `m.repo` set (same `openRepoRow` path); `tab`/
  `shift+tab` include `focusRecent` when visible and skip it when the filter is
  non-empty.
- **`internal/nav` (golden flow):** with `recentMsg` + `reposMsg` seeded and the
  filter empty, the frame shows the `Recent` heading and rows above the list;
  after typing filter text, the section is gone. Uses the existing white-box
  session harness (`navtest_test.go`) + `assertGolden`.
- **`internal/core`:** `LoadMRU` is already covered (`mru_test.go`) — unchanged.
- **Gates:** `gofmt` / `go vet` / `golangci-lint` / `go test -race ./...`; golden
  stability under `-update`.

## Non-goals

- **Starred / favorites** — a separate subsystem (its own issue); not in #183.
- **Writing / pruning the MRU** — read-only; `store.MRUTouch` is untouched.
- **A configurable count or a new keybinding** — fixed at 5; reachable via the
  existing `tab` / `↑↓`, no new key.
- **Changing the Repos list**, its alphabetical sort, or remote/clone handling.

## Open questions / follow-ups

- **Merging MRU + starred into one "Pinned/Recent" view** becomes a clean
  follow-up once the starred subsystem exists.
- **Showing a recency timestamp** per Recent row — out of scope; the `mru` file
  carries order, not timestamps.
