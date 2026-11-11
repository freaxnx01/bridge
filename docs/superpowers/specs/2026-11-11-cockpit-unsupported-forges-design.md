# Cockpit: unsupported forges are a skip, not an error (#366)

Status: approved 2026-11-11. Part of milestone Cockpit Today; a prerequisite for
the overnight digest (#381), whose trust rule is that no "nothing" appears
without its reason and no noise buries the real reason
([`2026-11-11-cockpit-today-design.md`](2026-11-11-cockpit-today-design.md)).

## Problem

The Pipeline board shows a permanent red error line for every local Azure
DevOps clone. On srvdmsk8s01 that is 10 lines:

```text
errors: issues bossDMS/dgArchiveStamps — no forge client: ado owner bossDMS (since 12h ago)  dismiss
```

Cause: `clientForMCP` (`cmd/bridge/mcp.go:367`) builds clients for `github` and
`forgejo` only and returns nil for anything else. `repoClient`
(`cmd/bridge/pipeline.go:74`) turns nil into `errNoForgeClient`, and the
collector records it under `issues <owner>/<name>`
(`internal/cockpit/collector.go`, `applyRepoLocked`). The error comes back on
every refresh. `dismiss` only hides it until the next board update.

## Decision (operator, 2026-11-11)

Option 1 from the issue: **a forge the board has no client for at all is a
skip.** It shows as one quiet, grouped note. ADO support in the collector
(option 2) is not part of this change.

## Design

- **Two kinds of "no client".**
  - *Unsupported forge:* `r.Forge` is neither `github` nor `forgejo`. The
    `Issues` source returns the new sentinel `cockpit.ErrForgeUnsupported`,
    wrapped with the forge name.
  - *Missing token on a supported forge:* this still returns
    `errNoForgeClient` and stays a **source error**, as the code comment at
    `cmd/bridge/pipeline.go:44` intends ("a missing token shows on the board").
- **Collector.** `applyRepoLocked` checks
  `errors.Is(res.err, ErrForgeUnsupported)` first. If it matches, the repo is
  not recorded in `errs`, any old `issues <key>` error is deleted, and the repo
  is recorded in a new `unsupported` map (repo key → forge). Base repo data is
  appended as on the error path. A later successful fetch deletes the entry.
- **Board payload.** A new field `skipped`: one entry per forge, sorted by
  forge, with the repo count:
  `"skipped": [{"forge": "ado", "repos": 10}]`. It is always `[]` when empty,
  never `null`, like the other sections.
- **UI.** `Pipeline.svelte` renders one muted line per entry below the
  columns, outside the red error strip, with no dismiss:
  `10 Azure DevOps repos not on the board (forge not supported).` Forge
  labels: `ado` → "Azure DevOps"; any other value is shown as-is.
- **Inbox:** unchanged. It is GitHub-only, and its `errors` carry only
  comment-read failures.
- **Docs:** `docs/cockpit.md` (API shape + "Known limits"), CHANGELOG
  `[Unreleased]` → `Fixed`.

## Non-goals

- ADO work items on the board (option 2).
- Changing `Sources.Issue` (single-issue read for the inbox detail). It already
  errors for non-GitHub repos, and the inbox never asks for those.
- Changing how errors for supported forges are shown or dismissed.

## Acceptance

- ADO clones produce no entries in `/api/pipeline` `errors`.
- They appear as `skipped: [{"forge":"ado","repos":N}]`, and the board shows
  one muted line.
- A real fetch failure for a supported forge (for example `boom`, 401) still
  shows in `errors`.
- A GitHub or Forgejo repo whose owner has no token still shows in `errors`
  (`errNoForgeClient`).

## Calls made by the author

- The sentinel lives in `internal/cockpit` (the consumer that interprets it),
  and `cmd/bridge` returns it.
- "Supported" means `github` or `forgejo`, matching `clientForMCP`'s switch.
- The note is grouped per forge, not per repo.
