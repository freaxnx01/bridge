# Cockpit — Today (morning digest): wireframe

Approved 2026-11-11 (Phase 1, `/ui:brainstorm`). Design:
[`docs/superpowers/specs/2026-11-11-cockpit-today-design.md`](../../superpowers/specs/2026-11-11-cockpit-today-design.md).
Builds on the Pipeline board ([`../cockpit/wireframe.md`](../cockpit/wireframe.md))
and the issue-centric inbox (#368).

Decisions: **phone first, desktop usable**; Today **extends #368's Inbox
page** (the start page). There is no separate page; the `[Today]` tab label
below stands for the Inbox entry. Retry / Retry with / Park come from #374.
**Needs you** is #368's inbox (always current); the sections below it cover
only what changed **since you last looked**. Single user (the operator).

Reused components: `Header`, `FreshnessBadge`, `ConfirmDialog`, `TokenDialog`,
`IssueCard`'s row pattern.

## Phone (landing, morning)

```text
┌──────────────────────────────────┐
│ ◆ cockpit [Today] Board ☰  ⚠ [🔑]│
├──────────────────────────────────┤
│ Since 18:40 yesterday   [change ▾]│
│ 6 dispatched · 3 PRs · 1 merged · │
│ 2 stopped · $14.20                │
│ quota: weekly 41% · 5h 0%  (#371) │
├──────────────────────────────────┤
│ ⚠ no dispatcher tick 23:30–02:00  │
│   (5 expected, 0 seen)    [why ▸] │  ← only when ticks are missing
├──────────────────────────────────┤
│ NEEDS YOU · 3                     │  ← #368 inbox cards
│ ┌──────────────────────────────┐ │
│ │ ✓ bridge #364 → PR #369       │ │
│ │ fix(nav): detail col budget   │ │
│ │ checks ✓ · review: approve    │ │
│ │ "Caps the detail column at…" ▸│ │
│ │ 3 files +42 −7   [diff ▸]     │ │
│ │                     [Merge]   │ │
│ └──────────────────────────────┘ │
│ ┌──────────────────────────────┐ │
│ │ ✕ quotes #12  task_failure    │ │
│ │ attempt 2/2 · stopped 03:14   │ │
│ │ [Retry] [Retry with ▾] [⋯]    │ │
│ └──────────────────────────────┘ │
│ ┌──────────────────────────────┐ │
│ │ ◐ bridge #367 → PR #373       │ │
│ │ review: request_changes       │ │
│ │ self-fix exhausted · draft    │ │
│ │ [Open PR ↗] [Retry] [⋯]       │ │
│ └──────────────────────────────┘ │
├──────────────────────────────────┤
│ DONE · 2                        ▾ │
│ ✓ rhyflitzer #318 merged 05:02    │
│ ✓ flowhub #88 PR #91 open, green  │
├──────────────────────────────────┤
│ STILL GOING · 1                 ▾ │
│ ↻ kit-racer #40 retrying (rate_…) │
├──────────────────────────────────┤
│ DIDN'T HAPPEN · 4               ▸ │  (collapsed)
│ 22:00 3 skipped: 2 per-repo cap,  │
│       1 open PR                   │
│ 03:00 auto lane dry-run: would    │
│       dispatch bridge #351        │
├──────────────────────────────────┤
│ [Full journal ▸]   ⟳ 40s          │
└──────────────────────────────────┘
```

- **Header:** new `Today` tab next to the existing `Board` (Pipeline); `Today`
  is the landing route.
- **Summary strip:** "since" anchor = last visit (stored in the browser),
  fallback 18:00 yesterday. `change ▾` presets: last visit / since 18:00 /
  24 h / 7 d.
- **Card glyphs:** `✓` mergeable PR, `✕` stopped run, `◐` PR blocked on review.
- **Merge** renders only when checks are green, the AI review verdict is
  `approve` **for the PR's head SHA**, and the PR is not a draft. Otherwise the
  slot shows the reason.
- **Card title** taps through to #368's issue detail.
- An overnight event already shown as a Needs-you card appears in the lower
  sections only as `→ in Needs you`, never as a second card.

## Merge card, expanded (summary + diff)

```text
│ ✓ bridge #364 → PR #369  fix(nav): keep detail column in budget │
│ checks ✓ 6/6 · review: approve (head a1b2c3 ✓ current)          │
│ ── Agent summary ────────────────────────────────────────────── │
│ Caps the detail column at the remaining height so the dashboard │
│ never scrolls… (full ## Summary)                                │
│ ── AI review ────────────────────────────────────────────────── │
│ verdict: approve · 0 blocking · 2 nits                [full ▸]  │
│ ── Files (3) ─────────────────────────────── +42 −7 ─────────── │
│ ▸ internal/nav/dashboard.go     +30 −5                          │
│ ▾ internal/nav/dashboard_test.go +12 −2                         │
│   │ @@ -88,6 +88,18 @@                                          │
│   │ + func TestDetail_TallContent_StaysInBudget(t *testing.T) { │
│   │   (read-only, no comments)                                  │
│ [Open PR ↗]                                         [Merge ▸]   │
```

## Confirm (Merge)

```text
┌──────────── Merge PR #369 into main? ────────────┐
│ freaxnx01/bridge · squash (repo default)         │
│ checks 6/6 ✓ · review approve on a1b2c3 (head)   │
│ Closes #364. The branch is deleted after merge.  │
│                                                  │
│                      [Cancel]  [Squash & merge]  │
└──────────────────────────────────────────────────┘
```

## Desktop

```text
┌──────────────────────────────────────────────────────────────────────────────┐
│ ◆ bridge cockpit  [Today] Board  Repos· Issues&PRs· Stats·  Arch   ⚠  [🔑]   │
├──────────────────────────────────────────────────────────────────────────────┤
│ Since 18:40 yesterday [▾] · 6 dispatched · 3 PRs · 1 merged · 2 stopped ·    │
│ $14.20 · quota weekly 41% / 5h 0%                                ⟳ 40s       │
├──────────────────────────────────────┬───────────────────────────────────────┤
│ NEEDS YOU · 3                        │ OVERNIGHT                             │
│ (cards as on phone, wider; expanded  │ DONE · 2         ✓ rhyflitzer #318 …  │
│  merge card shows summary | files    │ STILL GOING · 1  ↻ kit-racer #40 …    │
│  side by side)                       │ DIDN'T HAPPEN · 4 ▸                   │
│                                      │ ── Journal (ticks) ──────────────────│
│                                      │ 05:30 nothing eligible (all held)     │
│                                      │ 05:00 dispatched rhyflitzer #318 retry│
│                                      │ 02:00 ⚠ 23:30–02:00 no ticks          │
│                                      │ 22:00 dispatched 6 · skipped 3 [▸]    │
└──────────────────────────────────────┴───────────────────────────────────────┘
```

## States

```text
Loading:          summary strip ░░░░░░  · cards skeleton
Quiet night:      "Nothing dispatched since 18:40 — 24 ticks ran: 20 outside window,
                   4 nothing eligible (all held: open PR)"      ← never a bare "nothing"
Dry-run ticks:    "dry-run: would have dispatched bridge #351 (lane auto)" — shown
                   in DIDN'T HAPPEN, tick marked "dry-run" in the journal
No ticks at all:  ⚠ "Dispatcher didn't run since 18:40 (0 of 24 ticks).
                   Check bridge-dispatch.timer."               ← red, top of page
Inert ticks:      ⚠ "Ticks ran but saw 0 repos — no GitHub client?"
Journal missing:  "No dispatch journal yet (older bridge?)" — outcomes from forge only
Stale source:     strip dimmed + FreshnessBadge ⚠, as on the Board
Errors:           collapsible error strip at bottom (unsupported forges = one quiet
                   note, see #366), not one red line per repo
Merge disabled:   [Merge] replaced by reason: "checks running" / "review on older
                   commit" / "draft" / "conflicts"
Write w/o token:  tap → TokenDialog, then the confirm
```
