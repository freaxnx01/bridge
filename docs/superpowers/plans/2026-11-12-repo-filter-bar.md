# Cockpit Repo Filter Bar Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A global filter row under the cockpit header — single-select forge chips plus a multi-select, forge-grouped repo dropdown — that filters every tab client-side and persists in the URL and localStorage.

**Architecture:** The backend adds `forge` to inbox and running cards; everything else is client-side. A `repoFilter` store holds `{forge, repos}` and a pure `matches(filter, item)`; a `repos` store loads the existing `GET /api/repos`; `RepoFilterBar.svelte` edits the filter; `App`, `Inbox` and `Pipeline` apply it.

**Tech Stack:** Go (stdlib testing), Svelte 5 runes, Vitest + @testing-library/svelte + jsdom.

**Spec:** [`docs/superpowers/specs/2026-11-12-repo-filter-bar-design.md`](../specs/2026-11-12-repo-filter-bar-design.md) · wireframe/flow: [`docs/design/repo-filter/`](../../design/repo-filter/wireframe.md)

## Global Constraints

- Repo key everywhere: `` `${forge}:${owner}/${name}` `` (e.g. `github:freaxnx01/bridge`). Items expose `forge` and `repo` (`owner/name`).
- URL format: `?forge=<forge>&repo=<key>&repo=<key>`; absent params = no filter. Written with `history.replaceState` (no new history entry), keeping other query params.
- localStorage key: `bridge.repoFilter`, JSON `{"forge":"","repos":[]}`; every read/write in try/catch, a failure behaves like "nothing stored".
- On load the URL wins whenever it has `forge` or `repo`; otherwise the stored value applies.
- Forge chip order: `github`, `forgejo`, `ado`, then any other forge alphabetically. Labels: `GitHub`, `Forgejo`, `ADO`, else the raw value. Only forges present in `/api/repos` get a chip.
- Exact copy: `All`, `All repos`, `N repos`, `filter repos…`, `✕ clear`, `Nothing for the selected repos.`, `✕ clear filter`.
- No new npm or Go dependencies. Hand-rolled fakes only.
- After every task: `gofmt -l .` empty, `go vet ./...`, `go test -race ./...`, `npm --prefix web test -- --run`, `npm --prefix web run build` all pass.

## Review Focus

- **Same repo name on two forges** — `github:o/a` and `ado:o/a` must filter independently. Pinned in Task 2 (`matches`).
- **Stored selection with repos `/api/repos` doesn't (yet) list** — kept and still applied, never pruned. Pinned in Task 2 and Task 3.
- **Session card outside every repo** (`repo: ''`) — visible only with no filter. Pinned in Task 2.
- **localStorage throwing** (private mode) — the bar still works, filter starts empty. Pinned in Task 2.
- **Filter hides everything** — the tab shows the clear-filter empty state, not its normal "Nothing needs you." text. Pinned in Task 4.

---

### Task 1: `forge` on inbox and running cards

**Files:**
- Modify: `internal/cockpit/inbox.go:39` (InboxCard), `classify` (~line 146)
- Modify: `internal/cockpit/board.go:32` (RunningCard), running-card construction (~lines 163, 210, 237), `repoForCWD` (line 245)
- Test: `internal/cockpit/inbox_test.go`, `internal/cockpit/board_test.go`

**Interfaces:**
- Produces: JSON `forge` on every inbox card (always set) and on running cards (`omitempty`; empty for a session outside every repo).

- [ ] **Step 1: Write the failing tests**
  - `TestBuildInbox_Card_CarriesForge`: one human card from `inboxRepo(...)` → `card.Forge == "github"`, and `json.Marshal(card)` contains `"forge":"github"`.
  - `TestBuildBoard_RunningCards_CarryForge` (table): an in-progress `issues` run in a github repo → `Forge "github"`; an enrichment-ongoing issue → `"github"`; a session whose CWD is inside an `ado` repo's Path → `"ado"`; a session outside every repo → `""` and the marshalled card has no `"forge"` key.
- [ ] **Step 2: Run** `go test ./internal/cockpit/ -run 'CarriesForge|CarryForge'` — Expected: FAIL (unknown field `Forge`).
- [ ] **Step 3: Implement.** Add the fields; set them from `rd.Forge`. Change `repoForCWD(repos []RepoData, cwd string) string` to `repoForCWD(repos []RepoData, cwd string) (repo, forge string)` and update its one caller.
- [ ] **Step 4: Run** `go test -race ./...` — Expected: PASS.
- [ ] **Step 5: Commit** `feat(cockpit): carry the forge on inbox and running cards`

### Task 2: `repoFilter` and `repos` stores

**Files:**
- Create: `web/src/lib/stores/repoFilter.js`, `web/src/lib/stores/repoFilter.test.js`
- Create: `web/src/lib/stores/repos.js`, `web/src/lib/stores/repos.test.js`

**Interfaces:**
- Produces (`repoFilter.js`):
  - `repoFilter` — writable `{ forge: string, repos: string[] }`, initially `{ forge: '', repos: [] }`
  - `repoKey(item) → string` (`${item.forge}:${item.repo}`; for a `/api/repos` entry `${forge}:${owner}/${name}`)
  - `matches(filter, item) → boolean`
  - `isFiltered(filter) → boolean`
  - `setForge(forge)` — sets forge, drops repos whose key does not start with `${forge}:` (`''` keeps all)
  - `toggleRepo(key)`, `setRepos(keys)`, `clearFilter()`
  - `parseQuery(search) → filter`, `toQuery(filter, search) → string` (preserves unrelated params)
  - `initRepoFilter(win = window) → unsubscribe` — URL → else localStorage; then subscribes and writes both
- Produces (`repos.js`): `repos` — writable array of `{ forge, owner, name }`; `loadRepos()`; reloads on SSE `pipeline-updated` (same pattern as `stores/pipeline.js`).

- [ ] **Step 1: Write the failing tests** (`repoFilter.test.js`, `describe` blocks):
  - `matches`: no filter → true for any item incl. `repo: ''`; forge `github` → false for `{forge:'ado',repo:'o/a'}`; repos `['github:o/a']` → true for `{forge:'github',repo:'o/a'}`, false for `{forge:'ado',repo:'o/a'}`; any filter → false for `{forge:'',repo:''}`.
  - `setForge('ado')` with repos `['github:o/a','ado:p/b']` → repos `['ado:p/b']`.
  - `parseQuery('?forge=github&repo=github:o/a&repo=github:o/b')` → `{forge:'github', repos:['github:o/a','github:o/b']}`; `toQuery({forge:'',repos:[]}, '?x=1')` → `'?x=1'`; round-trip equality.
  - `initRepoFilter`: fake `win` with `location.search` + `history.replaceState` spy + `localStorage` fake. URL filter beats stored; empty URL uses stored; a `setForge` afterwards calls `replaceState` with the new query and writes `bridge.repoFilter`; `localStorage.getItem` throwing → filter stays empty, no throw; a stored key not in any repo list is kept.
  - `repos.test.js`: `loadRepos()` with a stubbed `fetch` of two `core.Repo` objects → store holds `[{forge,owner,name}]` only.
- [ ] **Step 2: Run** `npm --prefix web test -- --run src/lib/stores/repo` — Expected: FAIL (modules missing).
- [ ] **Step 3: Implement** both modules; `repos.js` uses `get` from `../api.js` like `pipeline.js`.
- [ ] **Step 4: Run** `npm --prefix web test -- --run` — Expected: PASS.
- [ ] **Step 5: Commit** `feat(web): repo filter state with URL and storage persistence`

### Task 3: `RepoFilterBar` component, mounted under the header

**Files:**
- Create: `web/src/lib/components/RepoFilterBar.svelte`, `web/src/lib/components/RepoFilterBar.test.js`
- Modify: `web/src/App.svelte` (mount bar, `initRepoFilter`, `loadRepos`)

**Interfaces:**
- Consumes: Task 2 exports.
- Produces: `<RepoFilterBar repos={...} filter={...} counts={...} />` — `repos` as from `repos.js`, `filter` the store value, `counts` a `Record<repoKey, number>`. Writes through the store functions; no callback props.

- [ ] **Step 1: Write the failing tests** (render with fixtures of 2 github + 1 ado repo):
  - chips read `All`, `GitHub 2`, `ADO 1` in that order; `All` is pressed (`aria-pressed="true"`) with no filter.
  - clicking `ADO 1` sets `repoFilter.forge` to `ado`.
  - button text: `All repos` → after selecting one repo its short name (`name` part) → after two, `2 repos`.
  - opening the dropdown shows group headers `GitHub`, `ADO`; with forge `ado` only `ADO`.
  - typing `brid` in `filter repos…` leaves only matching rows.
  - the group `all` checkbox selects every repo of that forge; `All repos` clears the selection.
  - a row shows its count from `counts`; no number when 0/absent.
  - `Escape` closes the dropdown.
  - `✕ clear` is absent with no filter, present with one, and resets to `{forge:'',repos:[]}`.
  - a selected key not in `repos` stays in the store after rendering.
- [ ] **Step 2: Run** `npm --prefix web test -- --run src/lib/components/RepoFilterBar` — Expected: FAIL.
- [ ] **Step 3: Implement** the component per the wireframe (button + popover with checkboxes, click-outside closes). In `App.svelte`: `onMount` calls `initRepoFilter()` and `loadRepos()`; render the bar directly under `<Header>`; compute `counts` for the current `area` from the filtered-by-forge-only items of that tab (Inbox: cards+waiting+parked; Pipeline: running+ready+held+needsEnrichment).
- [ ] **Step 4: Run** full web tests + build — Expected: PASS.
- [ ] **Step 5: Commit** `feat(web): repo filter bar under the cockpit header`

### Task 4: Apply the filter to Inbox, Pipeline and the badge; docs

**Files:**
- Modify: `web/src/routes/Inbox.svelte`, `web/src/routes/Pipeline.svelte`, `web/src/App.svelte` (badge)
- Test: `web/src/routes/Inbox.test.js`, `web/src/routes/Pipeline.test.js`, `web/src/App.test.js`
- Modify: `CHANGELOG.md` (`[Unreleased]` → Added)

- [ ] **Step 1: Write the failing tests**
  - Inbox: with two cards (`forge:'github'`, repos `o/a` and `o/b`) and `repoFilter` set to `['github:o/a']`, only `o/a`'s card renders; waiting and parked follow the same rule; with a filter that hides everything the text `Nothing for the selected repos.` and a `✕ clear filter` button render (and not `Nothing needs you.`); clicking it clears the store.
  - Pipeline: same two-repo fixture across `ready`, `held`, `needsEnrichment`, `running` → only matching items render; the needs-enrichment "show more" count is computed after filtering.
  - App: header badge shows the count of matching inbox `cards` only.
- [ ] **Step 2: Run** `npm --prefix web test -- --run` — Expected: FAIL on the new cases.
- [ ] **Step 3: Implement** — `$derived` filtered lists via `matches($repoFilter, item)` before any slicing; the empty-state branch uses `isFiltered`.
- [ ] **Step 4: Run** all checks from Global Constraints — Expected: PASS.
- [ ] **Step 5: Changelog + commit** — `CHANGELOG.md` `[Unreleased]` → `Added`: "Cockpit: a repo filter bar under the header — forge chips and a multi-select repo dropdown grouped by forge — filters every tab and is kept in the URL and the browser." Commit `feat(web): filter inbox, pipeline and the inbox badge by repo and forge`.
