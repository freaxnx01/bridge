# Cockpit repo filter bar (forge + multi-repo)

**Status:** approved 2026-11-12 (operator). Wireframe and flow:
[`docs/design/repo-filter/`](../../design/repo-filter/wireframe.md).

## Why

The cockpit (`bridge serve`) shows every discovered repo at once: 24 repos on
two forges today, 218 needs-enrichment cards on the Pipeline board. The
operator wants to look at one repo, a handful of repos, or one forge at a time,
on every tab, and keep that selection across reloads and in links.

## Decisions (operator)

- **Layout:** one slim row under the header: single-select forge chips
  (`All` + one per forge present) and a multi-select repo dropdown with search,
  grouped by forge (wireframe: [`wireframe.md`](../../design/repo-filter/wireframe.md)).
- **Scope:** global. One selection filters Inbox, Pipeline and every later tab;
  the header's Inbox badge counts only matching cards.
- **Persistence:** URL query (`?forge=github&repo=github:freaxnx01/bridge&repo=…`)
  and `localStorage`. On load the URL wins; with no filter in the URL the stored
  one applies. Every change rewrites both (`history.replaceState`, no new
  history entry).
- **Repo list:** all discovered repos, from the existing `GET /api/repos`
  (`internal/api/repos.go`, returns `core.Repo` with `forge`, `owner`, `name`).
  No new endpoint.

## Design

### Backend (Go)

Filtering is client-side; the payloads stay complete. Two card types lack the
forge and get it:

- `cockpit.InboxCard` (`internal/cockpit/inbox.go:39`): add
  ``Forge string `json:"forge"` `` set from `rd.Forge` in `classify`.
- `cockpit.RunningCard` (`internal/cockpit/board.go:32`): add
  ``Forge string `json:"forge,omitempty"` ``. Pipeline and enrichment cards take
  `rd.Forge`; session cards take the forge of the repo `repoForCWD` matched
  (`internal/cockpit/board.go:245`), empty when none matched.
- `IssueCard` already carries `forge`.

### Frontend (Svelte 5, `web/src`)

- **`lib/stores/repoFilter.js`**: the selection as `{ forge: '' | '<forge>',
  repos: string[] }` where each repo key is `forge:owner/name` (same-named repos
  on two forges stay distinct, cf. #144). Exports the `repoFilter` store,
  `setForge`, `toggleRepo`, `setRepos`, `clearFilter`, `matches(filter, item)`,
  `parseQuery(search)`, `toQuery(filter, search)` and `initRepoFilter()` (reads
  URL → else localStorage key `bridge.repoFilter`, then subscribes to write both).
  Storage access is wrapped in try/catch.
- **`lib/stores/repos.js`**: `repos` store loaded from `GET /api/repos`
  (`{forge, owner, name}` only), reloaded on SSE `pipeline-updated`.
- **`lib/components/RepoFilterBar.svelte`**: the row from the wireframe. Props:
  `repos`, `filter`, `counts` (map repo key → item count on the current tab).
- **Matching** (`matches`): hidden when a forge is set and differs from
  `item.forge`; otherwise shown when no repo is selected, else when
  `item.forge + ':' + item.repo` is selected. An item with `repo: ''` is shown
  only when no filter is set.
- **Forge chip change** drops selected repos of other forges.
- **Unknown repos** in a stored/URL selection (not in `/api/repos`) are kept,
  not pruned: the list can be empty while the server starts. They still filter.
- **Apply:** `App.svelte` mounts the bar under `Header` and derives the badge
  from filtered inbox cards; `Inbox.svelte` filters cards/waiting/parked;
  `Pipeline.svelte` filters running/ready/held/needsEnrichment before its own
  slicing. When the filter hides everything a tab would show, it renders
  `Nothing for the selected repos.` with a `✕ clear filter` button instead of
  its own empty text.

### Not in scope

- Server-side filtering or query parameters on `/api/inbox` / `/api/pipeline`.
- The tab (area) in the URL.
- #386's drill-down; this bar is a building block it can reuse.

## Acceptance criteria

- With no filter, every tab shows exactly what it shows today.
- Forge chips show `All` plus one chip per forge present in `/api/repos`, with
  repo counts; picking one hides every item of other forges on every tab and
  drops selected repos of other forges.
- Selecting one or more repos shows only those repos' items on every tab; the
  header Inbox badge counts only matching cards.
- The dropdown groups repos by forge, filters by search text, has per-forge
  "all" toggles and an "All repos" entry, and shows per-repo item counts for the
  current tab.
- The selection survives a reload (localStorage) and round-trips through the
  URL; a URL filter beats the stored one.
- `✕ clear` resets forge and repos; an all-hidden tab shows the clear-filter
  empty state.
- Inbox and running cards carry `forge` in the API JSON.
- `go test -race ./...`, `npm --prefix web test -- --run` and
  `npm --prefix web run build` pass; `gofmt -l .` is empty.
