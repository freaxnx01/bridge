# Cockpit repo filter bar: flow

Phase 2 (`/ui:flow`). Wireframe: [`wireframe.md`](wireframe.md).

## State and data

```mermaid
flowchart LR
  URL["URL ?forge=…&repo=…"] -->|on load, wins| F[repoFilter store]
  LS["localStorage bridge.repoFilter"] -->|on load, if URL has none| F
  F -->|on change| URL2[history.replaceState]
  F -->|on change| LS2[localStorage]
  API["GET /api/repos"] --> R[repos store]
  SSE["SSE pipeline-updated"] -->|reload| API
  R --> BAR[RepoFilterBar]
  F --> BAR
  BAR -->|select / clear| F
  F --> M["matches(item)"]
  M --> INBOX[Inbox lists + header badge]
  M --> PIPE[Pipeline columns]
```

## Bar interaction

```mermaid
stateDiagram-v2
  [*] --> Closed
  Closed --> Open: click Repos button
  Open --> Closed: Esc / click outside / click button
  Open --> Open: type in search (narrows list)
  Open --> Open: tick repo / group "all" / "All repos"
  Closed --> Closed: click forge chip (drops selected repos of other forges)
  Closed --> Closed: ✕ clear (forge = All, repos = none)
```

## Matching rule

```mermaid
flowchart TD
  I[item: forge, repo] --> A{forge chip set and item.forge differs?}
  A -- yes --> H[hidden]
  A -- no --> B{any repo selected?}
  B -- no --> S[shown]
  B -- yes --> C{"forge:owner/name in selection?"}
  C -- yes --> S
  C -- no --> H
```

An item without a repo (a session outside every discovered repo, `repo: ""`)
is shown only when no forge and no repo is selected.
