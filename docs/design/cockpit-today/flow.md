# Cockpit — Today (morning digest): flow

Approved 2026-11-11 (Phase 2, `/ui:flow`). Wireframe: [`wireframe.md`](wireframe.md).
Design: [`docs/superpowers/specs/2026-11-11-cockpit-today-design.md`](../../superpowers/specs/2026-11-11-cockpit-today-design.md).

Endpoint paths are proposals; final names are the builder's call. Today
**extends #368's Inbox page**, so components hang off `routes/Inbox.svelte`
and reuse #368's names. Retry / Retry with / Park are #374's actions.

## 1a — Journey: open and read

```mermaid
flowchart TD
  A([Open cockpit / phone bookmark]) --> B{since anchor stored?}
  B -- yes --> C[since = last visit]
  B -- no / storage blocked --> C2[since = 18:00 yesterday]
  C --> D[GET /api/digest?since=…]
  C2 --> D
  D -- network / 5xx --> E[Error: 'Couldn't load' + Retry · last good data dimmed]
  D -- ok --> F{journal present?}
  F -- no --> G[Note: no dispatch journal yet · outcomes from forge only]
  F -- yes --> H{ticks expected vs seen}
  H -- gap --> I[⚠ red banner: no tick X–Y · why ▸]
  H -- inert --> I2[⚠ ticks saw 0 repos]
  H -- ok --> J[Summary strip + quota]
  G --> J
  I --> J
  I2 --> J
  J --> K{anything since?}
  K -- no --> L[Quiet night: tick counts by reason]
  K -- yes --> M[Needs you · Done · Still going · Didn't happen]
  M --> N([Tap title → #368 issue detail])
  M --> O([Full journal ▸])
```

- `since` is stored as "last visit" when the page is **left or hidden**, not on
  load, so a reload during the morning read does not wipe the window.
- SSE `digest-updated` / `inbox-updated` triggers a re-fetch (same pattern as
  the Board).

## 1b — Journey: Merge and Retry

```mermaid
flowchart TD
  A[Card in Needs you] --> B{kind}
  B -- mergeable PR --> C[Expand: summary · review · files]
  C --> C1([diff ▸ → GET diff, lazy, read-only])
  C --> D[Merge ▸]
  B -- stopped run --> R[Retry / Retry with ▾ — #374]
  D --> T{token stored?}
  R --> T
  T -- no --> TD[TokenDialog] --> T
  T -- yes --> CF[ConfirmDialog: exact effect]
  CF -- cancel --> A
  CF -- confirm --> P[POST with headSha / issue ref]
  P -- 200 --> S[Card → Done ✓ merged / Still going ↻ · toast]
  P -- 409 head moved / not mergeable --> X1[Reason inline · card refreshed · no merge]
  P -- 401 --> TD
  P -- 403 / 503 --> X2[Error: origin not allowed / writes disabled]
  P -- 5xx --> X3[Error toast · card unchanged]
```

- Merge sends the `headSha` the review verdict was for. The server re-checks
  head unchanged, checks green, not draft, mergeable, and answers `409` with the
  reason otherwise. The button's guard is never trusted on its own.

## 2 — Component and state map

```mermaid
graph TD
  App[App.svelte · area=inbox] --> H[Header · Inbox/Pipeline]
  App --> Today[routes/Inbox.svelte · #368, extended]
  Today --> Sum[SummaryStrip · since picker, quota]
  Today --> Ban[TickHealthBanner]
  Today --> Need[Cards · #368 InboxCard list]
  Need --> MC[MergeCard · kind=merge, new]
  Need --> RC[InboxCard · #368, actions #374]
  Today --> Sec[OutcomeSection ×3 · collapsed state]
  Today --> Jr[JournalList]
  Today --> FB[FreshnessBadge]
  MC --> CD[ConfirmDialog]
  RC --> CD
  St[(stores/digest.js: digest, digestError, since)] -->|props down| Today
  IS[(stores/inbox.js · #368)] -->|cards| Need
  SSE[(stores/sse.js)] -->|digest-updated| St
  St -->|GET /api/digest?since| API[(api.js)]
  MC -->|GET diff, POST merge| API
  RC -->|POST retry #374| API
  Tok[(token.js / tokenPrompt.js)] --> CD
```

- **Ownership:** `stores/inbox.js` (#368) stays as is and feeds the cards. A
  sibling `stores/digest.js` holds the digest payload, error and `since`
  (localStorage wrapped in try/catch); `MergeCard` holds expanded + diff state
  (diff fetched once, cached per head SHA); `OutcomeSection` holds its collapsed
  state.
- **Events up:** cards emit `acted`; `Today` re-fetches. No optimistic state
  beyond a pending spinner.
- **Server side:** `GET /api/digest` reads the dispatch journal, run markers, PR
  summary / review / checks from collector snapshots and the #371 meter, with
  no per-request forge fan-out. The diff endpoint is the one lazy, per-click
  exception.

## Screen inventory (implied, not in the wireframe)

1. Since picker (`change ▾`) — dropdown with presets.
2. Full journal view — every tick, filter by lane / dry-run; own route.
3. "why ▸" for missing ticks — inline panel: expected tick times, `journalctl` hint.
4. Full AI review (`full ▸`) — inline rendering of the review comment.
5. Retry-with picker and Park — #374; issue detail — #368. Referenced only.
6. Token entry — existing `TokenDialog`.
