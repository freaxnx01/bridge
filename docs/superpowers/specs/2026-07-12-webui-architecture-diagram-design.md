# WebUI: bridge <-> agent-pipeline <-> ai-instructions Diagram — Design Spec

**Date:** 2026-07-12
**Issue:** #200
**Status:** draft

---

## Overview

Add a static diagram to the Bridge WebUI (`web/src/App.svelte`) that explains, for
a human reading the app, how the three workflow-infrastructure repos relate:
`ai-instructions`, `bridge`, and `agent-pipeline`. It is documentation, not a
live/data-driven visualization — no REST API or backend changes, and it is
unrelated to the roadmap's interactive architecture visualizer (`bridge-poc2.html`,
roadmap item #4 in `docs/history.md`), which visualizes a single repo's internal
clean-architecture layers rather than the relationship between these three repos.

## Content

Three boxes with labeled arrows:

```
ai-instructions --(sync via /sync-ai-instructions: CLAUDE.md/SKILL.md)--> bridge
ai-instructions --(sync via /sync-ai-instructions: CLAUDE.md/SKILL.md)--> agent-pipeline
bridge          --(issue labeled ai-implement)--------------------------> agent-pipeline
agent-pipeline  --(opens draft PR)---------------------------------------> (GitHub/Forgejo)
```

This mirrors the actual mechanics already in place: `bridge`'s `/gh:implement`
and `/fj:implement`-style flows label an issue for the pipeline; `agent-pipeline`
watches for that label and implements, opening a draft PR; both repos (and every
other repo) pull their `CLAUDE.md`/`SKILL.md` conventions from `ai-instructions`
via the `sync-ai-instructions` skill.

## Implementation

- New file `web/src/lib/architecture.svg` — hand-authored SVG, three boxes +
  arrows per the content above, styled plainly (no colors/fonts beyond what
  `App.svelte` already uses — the app currently has no theme/CSS system, so the
  SVG uses `currentColor`/inherited text color rather than hardcoded values).
- `App.svelte` gets a new `<section>` below the existing "Agents" section, with
  an `<h2>Architecture</h2>` heading and the SVG inlined directly in the markup
  (not `<img src="...">`) so it inherits page styles and stays crisp at any
  zoom level.
- No Go changes: `internal/web`, `internal/api`, and the REST/SSE surface are
  untouched. No new npm dependency.

## Testing

No unit-testable behavior — it's static markup with nothing to assert. Verification
is visual: run the Vite dev server (`cd web && npm run dev`), confirm the new
section renders below Agents without breaking that section's existing layout.

## Out of scope

- Any theming/dark-mode system for the WebUI (doesn't exist yet)
- The interactive per-repo architecture visualizer (roadmap #4 / `bridge-poc2.html`)
- Wiring this diagram to live/dynamic data
