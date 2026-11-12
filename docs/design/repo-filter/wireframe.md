# Cockpit repo filter bar: wireframe

Approved 2026-11-12 (Phase 1, `/ui:brainstorm`). Design:
[`docs/superpowers/specs/2026-11-12-repo-filter-bar-design.md`](../../superpowers/specs/2026-11-12-repo-filter-bar-design.md).

Decisions: **forge chips + repo dropdown** in one slim row under the header;
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
Forge: (All) (●GitHub 14) (ADO 10)      Repos: [ 2 repos ▾ ]  ✕ clear
```

- Chips: `All` plus one chip per forge that has at least one discovered repo,
  in the order GitHub, Forgejo, ADO, then any other (alphabetical). The count is
  the number of discovered repos on that forge. Single-select.
- Button label: `All repos` (none selected) · the repo's short name (one
  selected) · `N repos` (several).
- `✕ clear` only appears while a forge or a repo is selected; it resets both.

## Dropdown open

```text
  ┌─────────────────────────────────┐
  │ 🔍 filter repos…                │
  │ ☑ All repos                     │
  │ GitHub                    ☐ all │
  │   ☑ freaxnx01/bridge        4   │
  │   ☑ freaxnx01/agent-workflow 2  │
  │   ☐ anim-bossinfo-ch/Archive…   │
  │ ADO                       ☐ all │
  │   ☐ personal/agent-workflow-sa… │
  └─────────────────────────────────┘
```

- Groups by forge, repos sorted by `owner/name`. With a forge chip active, only
  that forge's group is shown.
- The number right of a repo is how many items the **current tab** shows for it
  (Inbox: cards + waiting + parked; Pipeline: all columns). No number for 0.
- `☑ All repos` = no repo selected; ticking it clears the repo selection.
- `☐ all` on a group header selects/deselects every repo of that forge.
- The search box narrows the list by substring on `owner/name`, case-insensitive.
- Esc or a click outside closes it.

## Empty result

```text
Nothing for the selected repos.   [ ✕ clear filter ]
```

Shown in place of each tab's own empty state when the filter hides everything.
