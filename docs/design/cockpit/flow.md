# Cockpit — shell + Pipeline board: flow

Approved 2026-10-26 (Phase 2, `/ui:flow`). Wireframe: [`wireframe.md`](wireframe.md).

## 1a — User journey: opening the board

```mermaid
flowchart TD
  E1([Open cockpit URL<br/>via Traefik + Authentik]) --> L
  E2([Open :7777 on LAN]) --> L
  L[Load: GET /api/pipeline<br/>+ subscribe SSE] --> R{Response?}
  R -- network/5xx --> ERR[Column: Couldn't load + Retry]
  ERR -- Retry --> L
  R -- 200 --> B[Board rendered]
  B --> S{Per column}
  S -- 0 items --> EMPTY[Muted 'Nothing running.' line]
  S -- age > 2x interval --> STALE[Rows dimmed, ⚠ in header]
  S -- source errors --> STRIP[Error strip, last good data kept]
  B -- SSE pipeline-updated --> L
  B -- ⟳ refresh --> TOK{Token stored?}
  TOK -- no --> TD[Token dialog]
  TOK -- yes --> RF[POST /api/pipeline/refresh] --> L
  B -- Open ↗ / Open RC ↗ --> X([Exit: Actions run / PR / claude.ai RC])
```

## 1b — User journey: acting from a card

```mermaid
flowchart TD
  C([Click Enrich / Dispatch]) --> T{Token stored?}
  T -- no --> TD[Token dialog] -- Save --> CF
  TD -- Cancel --> B([Back to board])
  T -- yes --> CF[Confirm dialog:<br/>repo, #N, paid session, what starts]
  CF -- Cancel --> B
  CF -- Confirm --> P[POST /api/pipeline/enrich or dispatch<br/>button shows launching…]
  P --> R{Result}
  R -- 200 started --> OK[✓ Started · slot · Open RC ↗]
  R -- 200 alreadyRunning --> AR[Already running · Open RC ↗]
  R -- 401 --> REJ[Token rejected → clear token,<br/>reopen token dialog]
  R -- 403 origin --> E403[✗ Blocked: wrong origin]
  R -- 409 / 422 --> STALEC[✗ Not on the board any more /<br/>GH only → reload board]
  R -- 5xx --> F[✗ error text, button re-enabled]
  OK --> B
  AR --> B
```

## 2 — Component & state map

```mermaid
flowchart TD
  App["App.svelte<br/>owns: activeArea"] --> Hdr["Header<br/>props: signals, area"]
  App --> Pipe["routes/Pipeline.svelte<br/>reads $pipeline, $now"]
  App --> Arch["Architecture (existing SVG)"]
  Hdr -- click 🔑 --> TokDlg["TokenDialog<br/>uses token.js"]
  Pipe --> Col["Column ×3<br/>props: title, cards, collectedAt, interval"]
  Col --> FB["FreshnessBadge<br/>props: collectedAt, intervalSeconds, now"]
  Col --> RC["RunningCard<br/>props: card"]
  Col --> IC["IssueCard<br/>props: card, action<br/>owns: actionState"]
  Pipe --> Held["Held list<br/>owns: expanded"]
  IC -- event: act --> Conf["ConfirmDialog<br/>props: repo, number, action"]
  Conf -- confirmed --> API["api.js post(path, body, token)"]
  Store[("stores/pipeline.js<br/>pipeline, loadPipeline()")] --> Pipe
  SSE[("stores/sse.js<br/>pipeline-updated")] --> Store
  Store -- GET /api/pipeline --> API
  API -- 401 --> Tok[("token.js<br/>get/set/clearToken")]
```

Props down, events up. Shared state lives only in the `pipeline` store and
`token.js`; per-card action state lives in the card; the Held expand toggle in
the Held list. `$now` is a 1 s ticker store so ages stay correct without SSE.

## 3 — State machines

```mermaid
stateDiagram-v2
  state "Column" as Col {
    [*] --> loading
    loading --> live: 200
    loading --> error: fail
    error --> loading: Retry
    live --> stale: age > 2x interval
    stale --> live: fresh collectedAt
    live --> live: SSE update
  }
  state "Card action" as Act {
    [*] --> idle
    idle --> confirm: click
    confirm --> idle: cancel
    confirm --> launching: confirm
    launching --> launched: 200
    launching --> alreadyRunning: 200 + flag
    launching --> failed: 4xx/5xx
    failed --> idle: dismiss
  }
  state "Token" as Tok {
    [*] --> absent
    absent --> entered: save
    entered --> rejected: 401
    rejected --> entered: save new
  }
```

## Screen inventory

- TokenDialog, ConfirmDialog: in the wireframe.
- Error strip "dismiss": hides an error only until the next board update; a
  still-failing source shows again.
- Held / "show N more": in-board toggles, not screens.
- Deliberately absent: card detail view — Open ↗ links to GitHub / claude.ai.
