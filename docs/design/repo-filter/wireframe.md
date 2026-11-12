# Cockpit repo filter bar: wireframe

Approved 2026-11-12 (Phase 1, `/ui:brainstorm`). Design:
[`docs/superpowers/specs/2026-11-12-repo-filter-bar-design.md`](../../superpowers/specs/2026-11-12-repo-filter-bar-design.md).

Decisions: **forge chips + repo dropdown** in one slim row under the header;
**owner chips** (GitHub account / ADO project) appear once a forge is picked
(amended 2026-11-12);
**global** (applies to every tab); remembered in the **URL and browser storage**;
the dropdown lists **all discovered repos** (`GET /api/repos`), grouped by forge.

## Closed (default: no filter)

```text
◆ bridge cockpit [Inbox 4] [Pipeline] ...                     🔑
─────────────────────────────────────────────────────────────────
Forge: (●All) (GitHub 14) (ADO 10)      Repos: [ All repos ▾ ]
─────────────────────────────────────────────────────────────────
<current tab, unfiltered>
```

## Filtered

```text
Forge: (All) (●GitHub 14) (ADO 10)
Owner: (All) (●freaxnx01 9) (anim-bossinfo-ch 5)
                                        Repos: [ 2 repos ▾ ]  ✕ clear
```

- Owner chips: shown only while a forge chip other than `All` is active.
  `All` plus one chip per owner of that forge (GitHub account, ADO project),
  sorted case-insensitively, with its repo count. Single-select. Picking a forge
  resets the owner to `All`.

- Chips: `All` plus one chip per forge that has at least one discovered repo,
  in the order GitHub, Forgejo, ADO, then any other (alphabetical). The count is
  the number of discovered repos on that forge. Single-select.
- Button label: `All repos` (none selected) · the repo's short name (one
  selected) · `N repos` (several).
- `✕ clear` only appears while a forge, an owner or a repo is selected; it
  resets all three.

## Dropdown open

```text
  ┌─────────────────────────────────┐
  │ 🔍 filter repos…                │
  │ ☑ All repos                     │
  │ GitHub                          │
  │   freaxnx01             ☐ all   │
  │     ☑ bridge              4     │
  │     ☑ agent-workflow      2     │
  │   anim-bossinfo-ch      ☐ all   │
  │     ☐ ArchiveRestApiNextGen     │
  │ ADO                             │
  │   personal              ☐ all   │
  │     ☐ agent-workflow-sandbox    │
  └─────────────────────────────────┘
```

- Groups by forge, then owner; repos sorted by name. With a forge chip active
  only that forge's group is shown, with an owner chip only that owner's.
- The number right of a repo is how many items the **current tab** shows for it
  (Inbox: cards + waiting + parked; Pipeline: all columns). No number for 0.
- `☑ All repos` = no repo selected; ticking it clears the repo selection.
- `☐ all` on an owner header selects/deselects every repo of that owner.
- The search box narrows the list by substring on `owner/name`, case-insensitive.
- Esc or a click outside closes it.

## Empty result

```text
Nothing for the selected repos.   [ ✕ clear filter ]
```

Shown in place of each tab's own empty state when the filter hides everything.
