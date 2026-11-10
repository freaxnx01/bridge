# Cockpit — shell + Pipeline board: wireframe

Approved 2026-10-26 (Phase 1, `/ui:brainstorm`). Spec:
[`docs/superpowers/specs/2026-10-24-bridge-cockpit-design.md`](../../superpowers/specs/2026-10-24-bridge-cockpit-design.md).

Decisions: **desktop first, phone usable** (columns stack); **compact rows**
(one or two lines per card, action at the right end). Single user (the
operator); the data and constraints are in the spec.

## Desktop (landing view)

```text
┌──────────────────────────────────────────────────────────────────────────────────────┐
│ ◆ bridge cockpit   [Pipeline] Repos· Issues&PRs· Integration· Stats· Infra·  Arch    │
│                                       ⚠ 1 source stale   ⏸ dispatch paused   [🔑]   │
├──────────────────────────────────────────────────────────────────────────────────────┤
│ RUNNING NOW · 4          ⟳ 12s │ READY TO DISPATCH · 3   ⟳ 4m │ NEEDS ENRICHMENT · 7 ⟳ 4m│
│────────────────────────────────│───────────────────────────────│────────────────────────│
│ ▶ pipeline  rhyflitzer #318    │ bridge #351                   │ flowhub #88            │
│   ai-implement · 23m   [Open ↗]│   fix · size:S     [Dispatch] │   feat · 41d   [Enrich]│
│ ✎ enrich    flowhub #92        │ game-kit-racer #40            │ bridge #349            │
│   autopilot · 6m       [Open ↗]│   feat · size:M    [Dispatch] │   chore · 12d  [Enrich]│
│ ● session   bridge [webui]     │ quotes #12                    │ game-x #3   forgejo    │
│   busy · 2h      [Open RC ↗]   │   docs · size:S    [Dispatch] │   feat · 3d   (GH only)│
│ ○ session   game-rhyflitzer    │                               │ …                      │
│   idle · 5m      [Open RC ↗]   │ ▸ Held · 2 (milestone, open PR)│ [show 4 more]         │
│                                │                               │                        │
├──────────────────────────────────────────────────────────────────────────────────────┤
│ errors: issues freaxnx01/quotes — 401 Bad credentials (since 08:12)          [dismiss]│
└──────────────────────────────────────────────────────────────────────────────────────┘
```

- **Header strip:** area nav (only Pipeline active in slice 1; the others are
  greyed-out placeholders; Arch keeps the existing architecture diagram),
  at-a-glance signals (stale source, dispatch paused), token button `🔑`.
- **Column header:** count + data age (`⟳ 12s`); amber with ⚠ when older than
  2× the source's interval. Clicking `⟳` refreshes (needs the token).
- **Running:** oldest first. Icon = kind: `▶` pipeline, `✎` enrichment,
  `●` busy session, `○` idle session. Unlinked pipeline run shows the run name
  instead of `#N`.
- **Held:** collapsed under Ready by default; expanded rows show their reason.
- **Enrich on a non-GitHub issue:** disabled, labelled `(GH only)`.
- **Error strip:** only when a source is failing; cards keep the last good data.

## Phone (stacked)

```text
┌────────────────────────────┐
│ ◆ cockpit  ☰      ⚠  ⏸ [🔑]│
├────────────────────────────┤
│ RUNNING · 4         ⟳ 12s ▾│
│ ▶ rhyflitzer #318  23m  ↗  │
│ ✎ flowhub #92      6m   ↗  │
│ ● bridge [webui]   2h   ↗  │
├────────────────────────────┤
│ READY · 3           ⟳ 4m  ▾│
│ bridge #351  fix  [Dispatch]│
├────────────────────────────┤
│ NEEDS ENRICHMENT · 7 ⟳ 4m ▸│  (collapsible)
└────────────────────────────┘
```

## States (per column)

```text
Loading (first load):  │ RUNNING NOW · …        │   skeleton rows ░░░░░░
Empty:                 │ RUNNING NOW · 0  ⟳ 8s  │   "Nothing running."  (one muted line)
Error, no data yet:    │ READY · ?        ⚠     │   "Couldn't load: <error>  [Retry]"
Stale:                 │ READY · 3     ⚠ ⟳ 22m  │   rows shown dimmed
```

## Confirmation dialog (Enrich / Dispatch)

```text
┌───────────── Dispatch bridge #351? ─────────────┐
│ Starts a paid Claude session in worktree         │
│ dispatch-351 that runs /gh:implement 351:        │
│ posts the implementation contract and adds       │
│ ai-implement, which starts the pipeline.         │
│                                                  │
│                         [Cancel]  [Dispatch ▶]   │
└──────────────────────────────────────────────────┘
After:  "✓ Started · bridge-wt-dispatch-351 · [Open RC ↗]"
   or   "Already running · [Open RC ↗]"   or   "✗ <error>"
```

## Token entry (`🔑`)

```text
┌────────── API token ──────────┐
│ Needed for actions. Stored in │
│ this browser only.            │
│ [••••••••••••••]   [Save]     │
│ (rejected → "Token rejected") │
└───────────────────────────────┘
```

An action clicked without a stored token opens this dialog first.
