# nav forge subfilter on the repo picker (#128)

## Problem

The `bridge nav` repo picker lists every discovered repo across all forges in one
flat, alphabetically-sorted list. When you work across GitHub, Forgejo, GitLab and
Azure DevOps, there is no way to narrow the list to a single forge — you can only
substring-search by name via the existing `filter:` box. Issue #128 asks for a
forge subfilter (`All | ADO | GitHub | Forgejo`) on the picker.

Two facts from the codebase shape the design:

1. **Forge is already a first-class field**, not something to derive from a URL.
   `core.Repo.Forge` (`internal/core/repo.go:11-22`) and `forge.RepoRef.Forge`
   (`internal/forge/client.go:12-23`) are stamped at discovery to one of
   `"github"`, `"gitlab"`, `"forgejo"`, `"ado"`. The picker already exposes a
   row's forge via `rowParts(r repoRow)` (`internal/nav/format.go:63-68`).
2. **Which forges are present is environment-dependent.** Discovery is
   presence-driven — an ADO target only exists if `ado/` (or `ado/.envrc`) is
   present (`internal/remote/remote.go:105`, `internal/core/repo.go:128-165`);
   likewise per forge. The issue's literal list `All | ADO | GitHub | Forgejo`
   omits **GitLab**, which is a fully supported forge in discovery
   (`internal/core/repo.go:67-101`).

## Goal (success criterion)

From the repo picker, cycling one key narrows the visible repo list to a single
forge (or `All`), composed with the existing text filter, with the available
forges driven by what is actually present in the current environment.

## Decisions (settled during brainstorming)

- **Option set is dynamic / presence-driven.** Show `All` plus only the forges
  that have at least one repo in the current `localRepos`+`remoteRepos`. GitLab
  appears iff GitLab repos are present. Empty forges never appear. This resolves
  the issue's "empty forge — selectable-empty or hidden — TBD" (hidden) and the
  GitLab omission in one move, and matches the existing collapse-when-empty
  precedent (Recent section / sessions panel).
- **Control is `ctrl+f`, global.** Handled before the focus-specific switches so
  it works from any focus, including while typing in the `filter:` box (a plain
  rune like `f` would be captured by the text input — `internal/nav/update.go:323-326`).
  Forward-cycle with wrap-around over `[All, …present]`. No-op when ≤1 forge is
  present.
- **Rendering is a segmented bar** drawn directly under the filter box, active
  segment highlighted; hidden entirely when ≤1 forge is present.
- **Scope is session-local** — resets to `All` on each `bridge nav` launch; no
  persistence. Independent of clearing the text filter.

## Architecture

The feature is additive and localized to `internal/nav`. No `core`/`forge`
changes — forge data already exists on the models.

### 1. State — `internal/nav` (`model.go`, `types.go`)

- New field `forgeFilter string` on `Model` (`internal/nav/model.go:12`), `""`
  meaning `All`.
- A computed helper `presentForges() []forgeOpt` derives the ordered set of
  forges present in the current rows (local + remote), using `rowParts(r).forge`
  / `rowForgeKey` (`internal/nav/data.go:261-274`). Canonical display order:
  `github, gitlab, forgejo, ado`, each mapped to a display label
  (`GitHub`, `GitLab`, `Forgejo`, `ADO`) and filtered to those present.
- A `forgeSubfilterVisible()` predicate: `len(presentForges()) > 1`.

`forgeOpt` is a small `{key, label string}` value defined next to the existing
`repoForgeChoices` table (`internal/nav/types.go:171-179`) so the label mapping
lives in one place.

### 2. Control — `internal/nav` (`update.go`)

- In `updatePicker` (`internal/nav/update.go:226-381`), add a `ctrl+f` case in
  the **global** section (alongside `tab`/`shift+tab` at update.go:238-241),
  before the per-focus switches. It calls `cycleForge(+1)`.
- `cycleForge(dir int)` builds `[All, …presentForges()]`, finds the current
  index by `forgeFilter`, advances mod len with wrap, and sets `forgeFilter`.
  When `len(present) <= 1` it is a no-op (returns unchanged). Modeled on the
  existing wrap-around cycles `cycledDashFocus` (update.go:486-506) and
  `cyclePickerFocus` (update.go:744-762).
- After changing scope, clamp the list selection index to the new visible length
  (reuse the existing `clampInt`, update.go:601-603).

### 3. Filtering — `internal/nav` (`update.go`, `format.go`)

- `visibleRepos()` (`internal/nav/update.go:219-224`) applies the forge match
  before the existing text match: keep rows where `rowParts(r).forge ==
  forgeFilter`, or all rows when `forgeFilter == ""`, then pass the result
  through the existing `filterRepos(all, m.filter.Value())`
  (`internal/nav/format.go:122-134`). Semantics: **forge scope ∧ text query**.
- The forge match is a tiny helper `matchesForge(r repoRow, forge string) bool`
  next to `filterRepos` in `format.go`.

### 4. View — `internal/nav` (`view.go`)

- New `viewForgeBar()` returns the segmented indicator string:
  `forge:  All  [GitHub]  Forgejo` with the active segment styled via a lipgloss
  style matching the create-repo modal's selected style
  (`internal/nav/view.go:274` / `viewRepoModal`).
- Inserted into `viewPicker()` (`internal/nav/view.go:70-148`) immediately after
  the filter line (view.go:106). Returns `""` (renders nothing) when
  `forgeSubfilterVisible()` is false, so single-forge environments see no change.

### 5. Wiring

None beyond `internal/nav` — `forgeFilter` defaults to `""` (All) via the zero
value, so no constructor/`cmd/bridge/nav.go` change is required.

## Edge cases

- **Active forge disappears after refresh** — pressing `r` (refresh remote,
  update.go:331) can drop all repos of the active forge. `visibleRepos()` (or the
  refresh handler) resets `forgeFilter` to `""` when the active forge is no
  longer in `presentForges()`, so the list never appears mysteriously empty.
- **≤1 forge present** — `ctrl+f` is a no-op and the bar is hidden; behaviour is
  identical to today.
- **Remote / clone-on-select rows** — carry `.remote.Forge`; `rowParts` already
  handles the remote-vs-local precedence, so scoped filtering works for
  not-yet-cloned rows too.
- **Selection index** — clamped to the new visible length on every scope change
  so the cursor never points past the end.
- **Recent section (#183)** — not yet on this branch; its interaction with the
  forge scope is deliberately out of scope for #128.

## Testing

- **`presentForges` / `cycleForge`** (table-driven): derivation from a mixed row
  set in canonical order; wrap-around forward cycling; no-op when 0 or 1 forge
  present.
- **`visibleRepos` AND-filtering**: forge scope alone, text query alone, and both
  together; `All` returns the full set unchanged.
- **Reset-on-refresh**: active forge removed from the row set ⇒ `forgeFilter`
  falls back to `""`.
- **Golden render** of `viewForgeBar`: active-segment highlight; empty string
  when ≤1 forge present. Verify picker golden output is unchanged in a
  single-forge fixture (no regression).
- Full gate: `gofmt -l .` empty, `go vet ./...`, `golangci-lint run`,
  `go test -race ./...` green.

## Acceptance Criteria

- [ ] The repo picker exposes a forge subfilter whose options are `All` plus only
      the forges with at least one repo present (GitHub / GitLab / Forgejo / ADO
      as applicable in the environment).
- [ ] Default is `All` — no behaviour change from today.
- [ ] `ctrl+f` cycles the scope forward with wrap-around, from any focus,
      including while typing in the `filter:` box.
- [ ] Selecting a forge narrows the visible list to that forge, composed as
      **AND** with the existing text filter.
- [ ] A segmented bar under the filter box shows the options with the active one
      highlighted; it is hidden when ≤1 forge is present.
- [ ] The scope resets to `All` when the active forge has no repos after a remote
      refresh.
- [ ] The list, its alphabetical sort, remote/clone handling, and all existing
      keybindings are unchanged when the scope is `All`.
- [ ] `gofmt -l .` empty, `go vet ./...` clean, `golangci-lint run` clean,
      `go test -race ./...` green.

## Non-goals

- Persistence of the selected forge across `bridge nav` launches.
- Multi-select forges (union of two forges) or a per-forge sort order.
- Starred / favorite repos (a separate subsystem).
- Any change to how forge is discovered or stamped on repos.

## Open questions / follow-ups

- None. Presence-driven option set + `ctrl+f` + segmented bar are all settled.
