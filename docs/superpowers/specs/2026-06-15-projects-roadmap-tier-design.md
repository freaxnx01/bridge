# Plan 1b: GitHub Projects v2 Status Roadmap tier

**Date:** 2026-06-15
**Area:** `internal/forge` (GraphQL + Projects v2 query) · `internal/overview` (Roadmap tier) · `internal/nav` (3-tier view) · `cmd/bridge` (wiring)
**Status:** Approved (design)

## Context

This is **Plan 1b** of sub-project #1 (cross-repo overview). Plan 1a shipped the
overview engine + TUI with a deliberately-nil `FetchRoadmap` seam, because the
GitHub Projects v2 roadmap provider needs GraphQL (bridge had none) and a live
API spike. The spike (2026-06-15) revealed the real shape of the board and
**redirected the design**:

- The roadmap board is **user-level Project #5 "freaxnx01 Backlog"** — 61 items,
  all real GitHub **Issues** (mostly `freaxnx01/bridge`), with a **`Status`**
  single-select (`Todo` / `In Progress` / `Done`, in that canonical order).
- It has **no `Value`/`Effort` number fields** — Plan 1a's assumption that the
  provider would read weights from the board was wrong.
- Board items **overlap** the issues Plan 1a already fetches, so a provider that
  injected them into the weighted "what matters now" list would double-count.
- The `project`-scoped token lives in the **private** `.envrc`; bridge resolves
  owner `freaxnx01`'s token from `private/.envrc` (lexically first), so the
  existing per-owner GitHub token is already `project`-scoped.

**Decision (B):** the board contributes a **Status/Kanban Roadmap tier** — a
distinct section grouped by Status — *not* weighted items. Weighting stays
label-based on issues (Plan 1a, unchanged). This uses the board's real structure
(Status) and asks nothing new of the user (no fields to create, no hand-weighing).

## Goal (success criterion)

The overview shows a third **Roadmap** tier listing the configured board's items
grouped by **Status** in board order (Todo / In Progress / Done) — separate from
the weighted "what matters now" and the raw-capture inbox. Concretely: with
`BRIDGE_PROJECT=freaxnx01/5` set, pressing `o` shows a Roadmap section with the
board's Todo and In-Progress items (repo + title) and a Done count.

## Decisions (from brainstorming + spike)

1. **Board = Status Roadmap tier (option B).** Not weighted; Status-grouped.
2. **Done collapses to a count** by default (forward-looking roadmap); Todo +
   In Progress list their items. Long groups cap with the existing `↓ N more`
   windowing. Adjustable later.
3. **Project configured explicitly** by `owner/number` via env
   (`BRIDGE_PROJECT`), per-environment. Empty disables the tier (no Roadmap,
   graceful) — the overview stays two-tier. No title auto-discovery.
4. **Reuse the per-owner GitHub token** (must have `project` scope — the private
   `.envrc` does). No dedicated token config; documented prerequisite.
5. **Reshape the `FetchRoadmap` seam** from `[]RankedItem` (the 1a scoring stub)
   to `[]RoadmapItem` (Status-bearing, unscored).

## Architecture

```
   cmd/bridge/nav.go
     FetchRoadmap: ctx → GithubClient(owner token).ListProjectV2Items(owner, number)
                         → []forge.ProjectItem → map to []overview.RoadmapItem
            │
            ▼
   internal/overview.Build
     Snapshot.Roadmap = items ordered by Status option index (no scoring)
            │
            ▼
   internal/nav viewOverview  → 3 tiers: "what matters now" · Roadmap · Inbox
```

### 1. `internal/forge` — GraphQL + Projects v2

GitHub's client is REST-only today. Add:

- `graphqlPost(ctx, query string, vars map[string]any, out any) error` — POSTs
  `{query, variables}` to **`c.baseURL + "/graphql"`** (baseURL defaults to
  `https://api.github.com`, so the endpoint is `https://api.github.com/graphql`),
  with the `Authorization: Bearer` header (same as `get`). Decodes
  `{"data": out, "errors": [...]}`; a non-empty `errors` array is returned as an
  error (so `INSUFFICIENT_SCOPES` surfaces clearly).
- `ProjectItem` type: `{Repo, Title, URL, Status, Type string}` (`Type` =
  `Issue` | `DraftIssue` | `PullRequest`).
- `ListProjectV2Items(ctx, ownerLogin string, number int) ([]ProjectItem, error)`
  — queries `user(login:$owner){projectV2(number:$number){items(first:100,after:$cursor){...}}}`,
  **paginating** via `pageInfo{hasNextPage endCursor}`. For each item: read
  `content` (`Issue`/`PullRequest` → `title`, `url`, `repository.nameWithOwner`;
  `DraftIssue` → `title`, no repo/url) and the `Status` single-select value
  (`fieldValues` → `ProjectV2ItemFieldSingleSelectValue` where `field.name ==
  "Status"`). Items with no Status get `Status: ""`.

The exact GraphQL query string is provided in the implementation plan; the spike
confirmed the field/type names against the live API.

### 2. `internal/overview` — Roadmap collection

- New `RoadmapItem{Repo, Title, URL, Status string}`.
- `Snapshot` gains `Roadmap []RoadmapItem`.
- Reshape `Config.FetchRoadmap` to `func(ctx context.Context) ([]RoadmapItem, error)`.
- `Build`: if `FetchRoadmap != nil`, fetch and store into `Snapshot.Roadmap`,
  **sorted by Status option index** (Todo=0, In Progress=1, Done=2, unknown last)
  with original board order preserved within a Status. **No scoring** — roadmap
  items never enter `Ranked`/`NeedsWeighting`. A fetch error is wrapped
  (`fmt.Errorf("fetch roadmap: %w", err)`) and aborts `Build`, consistent with
  the issues path. The Status order is a small known list in `overview`
  (`var statusOrder = []string{"Todo", "In Progress", "Done"}`); unknown statuses
  sort after, alphabetically.

### 3. `internal/nav` — third tier in `viewOverview`

Render a **Roadmap** panel between "what matters now" and "Inbox":

```
┌ Roadmap (freaxnx01 Backlog) ────────────────────────────────────────┐
│ Todo (58)                                                            │
│   • bridge        CLI repo addressing is ambiguous…                  │
│   • bridge        forge subfilter in the repo picker                 │
│   ↓ 56 more                                                          │
│ In Progress (2)                                                      │
│   • bridge        wire REST api skeleton                             │
│ Done · 1                                                             │
└──────────────────────────────────────────────────────────────────────┘
```

- Status sections in board order; **Todo / In Progress list items** (repo +
  title), each capped with `↓ N more` (reuse the picker's windowing helper);
  **Done shown as a count only**.
- Empty Roadmap (no `BRIDGE_PROJECT` / nil seam) → the tier is omitted entirely
  (overview stays two-tier; no empty panel).
- Pure rendering, no I/O (consistent with the other tiers).

### 4. `cmd/bridge` — wiring

- Parse `BRIDGE_PROJECT` as `owner/number` (e.g. `freaxnx01/5`). Empty → leave
  `FetchRoadmap` nil (tier disabled).
- `FetchRoadmap` closure: build a `GithubClient` from the **board owner's
  resolved token** (the same per-owner direnv resolution the issues fetch uses),
  call `ListProjectV2Items(owner, number)`, map `[]forge.ProjectItem` →
  `[]overview.RoadmapItem`. Best-effort like the rest: a fetch failure surfaces
  via `Build`'s wrapped error → the overview shows the existing error status
  (the ranked/inbox tiers still render from their own sources).

## Error handling

- **Missing `project` scope** → GraphQL returns `INSUFFICIENT_SCOPES`;
  `graphqlPost` surfaces it as an error → `Build` wraps it → nav shows
  "overview unavailable: …". (The fix is a token-scope change — a documented
  prerequisite, not a code path.)
- **`BRIDGE_PROJECT` unset/malformed** → tier disabled (nil seam), no error.
- **Draft items / items without Status** → handled (empty Repo/URL or Status).

## Testing

Per the Go overlay (stdlib `testing`, table-driven, hand-rolled fakes/stubs):

- **`internal/forge`**: `ListProjectV2Items` against an `httptest` stub serving a
  canned Projects v2 GraphQL response (Issue + DraftIssue + a second page) —
  assert mapping (Repo/Title/URL/Status/Type) and pagination. A second stub
  returning a GraphQL `errors` array asserts `graphqlPost` surfaces it.
- **`internal/overview`**: `Build` with a fake `FetchRoadmap` → assert
  `Snapshot.Roadmap` is populated, **Status-ordered** (Todo→In Progress→Done,
  unknown last), and that roadmap items do **not** leak into `Ranked`.
- **`internal/nav`**: a **golden flow test** for the 3-tier overview (extend the
  harness merged today) — fake `BuildOverview` returning a snapshot with all
  three tiers; golden the frame showing the Roadmap section (Todo/In Progress
  listed, Done count). Plus a targeted test that an empty `Roadmap` omits the
  tier.
- Verify gates: `gofmt -l`, `go vet`, `golangci-lint` (if available),
  `go test -race ./...`.

## Non-goals

- **`Value`/`Effort` on the board / weighting roadmap items** — option B drops
  this; weighting stays label-based on issues (Plan 1a).
- **Draft-issue creation, status changes, any write-back** — read-only.
- **Azure Boards / ADO roadmap provider** — still deferred (the provider seam
  remains, now shaped for Status-roadmap providers).
- **Dedup of board items against the ranked issues list** — not needed under B
  (the tiers are distinct and clearly labeled; an issue can legitimately appear
  both as weighted "what matters now" and as a roadmap card with its Status).
- **Changes to the issues / inbox tiers.**

## Open questions / follow-ups

- **GraphQL endpoint for GH Enterprise** (`BRIDGE_GITHUB_API` set to a non-default
  base): confirm `base + "/graphql"` is correct for Enterprise (it is for
  `api.github.com`); Enterprise uses `<host>/api/graphql` — out of scope now,
  note for later.
- **Roadmap item cap / "show Done"** toggle — deferred; default hides Done detail.
- **Per-environment business board** (Azure Boards) — the seam is ready; the
  provider is future work.
