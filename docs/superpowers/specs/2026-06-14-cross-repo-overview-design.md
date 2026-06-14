# Cross-repo overview: weighted ideas / roadmap / todos

**Date:** 2026-06-14
**Area:** `internal/nav` (new Overview screen) + new `internal/overview` aggregation package
**Status:** Approved (design)

## Bigger picture (why this exists)

This is **sub-project #1** of a larger reshaping of the personal dev cockpit. The
full vision and its decomposition were brainstormed separately; the decisions
that bound *this* spec:

- **bridge becomes a per-environment headless core + clients.** Each environment
  (Personal, Business) runs its own isolated bridge instance — own repos, forge
  tokens, worktrees, store, Telegram bot — with **zero shared storage**
  (compliance: business data never touches personal/homelab infra). Same
  software, deployed twice.
- **Three client surfaces, by network reality:** the **Telegram/FlowHub bot** is
  the universal mobile capture surface (crosses any network); a future **WebUI**
  (#4) is the rich surface where the network allows (Personal via the homelab
  Traefik/Cloudflare routing); the **TUI** is the desktop/agent-dev surface and
  the only place worktrees (which are local-only) truly live.
- **Decomposition order:** **#1 this** (data model + cross-repo overview) → #2
  capture/routing → #3 REST API → #4 WebUI → #5 session continuity (independent
  quick-win). #1 is the keystone: it defines what the API and WebUI later serve.

Everything below is **#1 only.** The REST API (#3) and WebUI (#4) are explicit
non-goals here, but #1 is built **API-ready** so they reuse its core unchanged.

> The day-to-day division-of-labor picture (capture on phone → triage in bridge →
> implement via agent-pipeline → review in CC CLI) is workflow-doc material and
> should be promoted to `ai-instructions/workflows/personal-dev-workflow.md`
> separately. It is summarized here only as usage context.

## Problem

bridge nav today is a strong **per-repo** cockpit (Issues / branches / commits /
status panels, shipped in v2.8.0), but there is **no cross-repo overview**. Ideas,
roadmap items, and todos are scattered across many sources and forges with no
single weighted view of "what matters most right now across everything." The
content also lives in genuinely different stages — raw captures in files vs.
committed work in Issues/boards — with no place that shows both and ranks the
committed work.

## Goal (success criterion)

From one place (the bridge TUI in this environment), I can see **every active
idea, roadmap item, and todo across all repos**, with committed work **ranked by
an importance weighting I trust**, and raw captures visible as an unranked inbox —
and for each item I know where its source-of-truth lives and how to act on it.

## Decisions (from brainstorming)

1. **Roadmap store = GitHub Projects v2** (one org-level, cross-repo board) for
   both environments *for now*. Business ADO/**Azure Boards** and personal
   **Forgejo** roadmap items are deferred — but the design keeps a **pluggable
   "roadmap provider" seam** (mirroring bridge's existing forge-client
   abstraction) so Azure Boards can slot in later. Only the **GitHub Projects**
   provider is built now (YAGNI).
2. **Weighting = W3 hybrid (value/effort → rank).** You supply cheap manual
   judgment (`value`, optional `effort`); bridge computes the cross-source
   ranking and folds in light signals (deadline urgency, staleness). Items with
   no manual `value` are treated as unweighted.
3. **Two-tier overview (P3).** Structured items (GitHub Issues + Projects cards)
   that carry fields get the weighted "what matters now" ranking. Raw file
   captures (`ideas-lab`, `<repo>/ideas.md`, `<repo>/TODO.md`) show **alongside**
   as a grouped, unranked **inbox** — never interleaved into the ranked list.
   *Caring enough to weight something = promoting it* from inbox to a
   structured item.
4. **Surface = TUI first, API-ready.** The cross-repo overview lands as a new
   Overview screen in bridge nav. All aggregation + scoring lives in a
   client-agnostic `internal/overview` package returning plain data, so #3's
   REST API and #4's WebUI reuse it unchanged.
5. **Multi-source aggregation** is the core mechanic — bridge already aggregates
   forges; this extends that to Issues + Projects cards + three file sources,
   per environment.

## Architecture

```
                         internal/overview  (client-agnostic)
   ┌───────────────────────────────────────────────────────────────┐
   │  Sources (per environment)            →  Normalize  →  Score    │
   │                                                                 │
   │  RANKED tier (structured, weighted):                            │
   │   • GitHub Issues (open)        ─┐                              │
   │   • GitHub Projects v2 cards    ─┤→ Item{value,effort,due,…}    │
   │       via RoadmapProvider seam   │   → score = value/effort     │
   │                                  │            + urgencyBoost     │
   │  INBOX tier (raw captures, unranked, grouped):                  │
   │   • ideas-lab/ideas/*.md        ─┐                              │
   │   • <repo>/ideas.md             ─┤→ Capture{source,repo,text,…} │
   │   • <repo>/TODO.md              ─┘                              │
   └───────────────────────────────────────────────────────────────┘
            │                                   │
            ▼ (now)                             ▼ (later: #3/#4)
     nav Overview screen (TUI)            REST API → WebUI
```

`internal/overview` exposes something like:

```go
// Snapshot is the full cross-repo overview for one environment.
type Snapshot struct {
    Ranked []RankedItem // structured, weighted, sorted desc by Score
    Inbox  []Capture    // raw file captures, grouped by Source+Repo
}

type RankedItem struct {
    Source   ItemSource // githubIssue | projectsCard
    Repo     string     // owner/name ("" for board-level draft items)
    Title    string
    URL      string     // forge/web link to act on it
    Value    int        // 1..5, manual (0 = unweighted)
    Effort   int        // 1..5, manual (default 3)
    Due      *time.Time // nil if none
    Score    float64    // computed; see Weighting
    Stale    bool       // flagged, not scored (see Weighting)
}

type Capture struct {
    Source CaptureSource // ideasLab | repoIdeas | repoTodo
    Repo   string        // "" for ideas-lab
    Title  string        // first line / bullet text
    Path   string        // file path (+ optional line) to jump to
    Age    time.Duration // since file/bullet last modified
}

// Build aggregates all sources for the configured environment.
func Build(ctx context.Context, cfg Config) (Snapshot, error)
```

The TUI calls `overview.Build` and renders; the future API serves `Snapshot`
directly. No bridge/forge logic leaks into the nav layer beyond calling `Build`.

## Sources & how each maps to a tier

**Ranked tier (structured, weighted):**

| Source | value / effort come from | Notes |
|---|---|---|
| GitHub Projects v2 card | custom **number fields** `Value` and `Effort` on the board | The roadmap items. Read via GraphQL. |
| GitHub Issue (open) | the linked Projects card's fields if on the board; else `value/N` + `effort/N` **labels**; else unweighted | Issues already on the board inherit card fields — no double entry. |

**Inbox tier (raw captures, unranked, grouped):**

| Source | Granularity | Grouped by |
|---|---|---|
| `ideas-lab/ideas/*.md` | one file = one capture (cross-project incubator) | recency / `status:` frontmatter |
| `<repo>/ideas.md` | one top-level bullet = one capture | repo |
| `<repo>/TODO.md` | one `- [ ]` line = one capture | repo |

Aggregation reads structured sources via the forge clients / RoadmapProvider and
file sources via the filesystem (nav already reads `ideas.md`/`TODO.md` per
worktree — reuse that parsing where possible, lift it into `internal/overview`
if it currently lives in nav).

## Weighting model (W3)

Each **ranked** item gets an explainable score:

```
score = round( value / effort + urgencyBoost , 2)
```

- `value` ∈ 1..5, **manual** (the only required judgment). `value == 0` → item is
  **unweighted**: excluded from the ranked list, surfaced in a small
  "⚖ needs weighting" group so it prompts you to weigh or drop it.
- `effort` ∈ 1..5, **manual**, **default 3** when unset (so value-only items still
  rank sensibly: 4/3 ≈ 1.33).
- `urgencyBoost` is **computed** from `Due` (thresholds configurable):
  `+2` if due within 3 days or overdue · `+1` if due within 14 days · `0`
  otherwise. Additive so a real deadline can override raw bang-for-buck.
- `Stale` is a **flag, not a score term**: set when a high-`value` item
  (`value ≥ 4`) has had no activity for > N days (default 30). Rendered as a ⚠
  badge so important-but-forgotten work gets noticed without distorting the
  ordering.

The score is **shown** next to each item (e.g. `4/2 +1 = 3.0`) so you can trust
and sanity-check the ranking rather than treating it as a black box. The formula
is a **tunable default**, not sacred — validating it in real use is an explicit
follow-up.

## Surface: the Overview screen (TUI)

A new **top-level Overview screen** in bridge nav (distinct from the per-repo
dashboard; reachable from the picker, e.g. a key or a "★ Overview" row):

```
┌ bridge · Personal · Overview ───────────────────────────────────────┐
│ What matters now (ranked)                          12 items · 3 due  │
│  ▸ 3.0  bridge        Wire REST API skeleton     v4/e2 +1 ⚠         │
│    2.5  agent-pipeline Retry flaky deploy step    v5/e2               │
│    1.3  quicktask     Localize settings screen    v4/e3               │
│    …                                                                 │
│ ⚖ needs weighting (4)                                                │
│    -    mgrabber      Investigate rate limits                        │
├─ Inbox (raw captures) ──────────────────────────────────────────────┤
│  ideas-lab (7)   bridge/ideas.md (3)   bridge/TODO.md (5)   …        │
│    • multi-pane focus model                       2d                  │
│    • kanban for issues                            5d                  │
└─ ↑↓ move · ⏎ open · tab pane · q quit ──────────────────────────────┘
```

- **Ranked pane:** all weighted items across repos, sorted by `Score` desc, each
  showing repo, title, `value/effort`, urgency/stale badges, and score. A small
  "needs weighting" group for `value == 0` structured items.
- **Inbox pane:** raw captures grouped by source/repo with counts and age.
- **Actions (v1, read-mostly):** `⏎` opens the item — forge/web URL for ranked
  items, jumps to file (path:line) for captures. Navigation + pane switching.
- **Non-TTY / `--once`** renders a single frame like the existing nav screens.

Per the Go UI workflow, build order: model + empty View → Update transitions →
`tea.Cmd` for `overview.Build` → lipgloss polish.

## Non-goals (explicit)

- **The REST API itself (#3)** — only the API-*ready* `internal/overview` package.
- **WebUI (#4).**
- **Azure Boards / ADO roadmap provider** — seam only, not implemented.
- **Forgejo roadmap items** — deferred.
- **Telegram/FlowHub changes (#2)** and **session continuity (#5).**
- **In-TUI editing of `value`/`effort`** (writing back to Projects fields/labels)
  and **in-TUI promotion** (inbox capture → Issue/card) — both are strong
  near-term follow-ups, but v1 is read-mostly (open/jump only) to keep scope
  tight. Capture of *new* items already happens via the Telegram bot (#2).

## Testing

Per the Go overlay (stdlib `testing`, table-driven, hand-rolled fakes):

- **Scoring** (`internal/overview`): table tests for `score` — value/effort
  combinations, `effort` default, each `urgencyBoost` threshold, `value == 0`
  exclusion, and `Stale` flagging. Pure function, exhaustive and cheap.
- **Aggregation**: `Build` against **fake sources** — a fake RoadmapProvider and
  a `t.TempDir()` with sample `ideas-lab`/`ideas.md`/`TODO.md` files — asserting
  the Ranked/Inbox split, grouping, and sort order. No network, no real forge.
- **TUI**: `Update` tests for the Overview screen (navigation, pane switch,
  `⏎` action target resolution) driven directly as pure `(model, msg)`, plus an
  `--once` render smoke test.
- Verify gates: `gofmt -l`, `go vet`, `golangci-lint`, `go test -race ./...`.

## Open questions / follow-ups

- **Projects v2 field setup:** create `Value` and `Effort` number fields on the
  board; document the exact field names the provider reads.
- **Issues off the board:** confirm the label fallback (`value/N`, `effort/N`)
  vs. simply leaving them unweighted.
- **File parsing granularity** for `ideas.md` (per top-level bullet) and
  `TODO.md` (per `- [ ]`) — confirm against your real files; reuse nav's
  existing parsers if they fit.
- **Environment identity & config:** how each instance knows it's "Personal" vs
  "Business" (config value) and which board/roots it owns — small, but needed.
- **Follow-ups to schedule after v1:** in-TUI weighting write-back; in-TUI
  promotion (capture → Issue); validate the weighting formula in real use;
  promote the division-of-labor section to the workflow doc.
