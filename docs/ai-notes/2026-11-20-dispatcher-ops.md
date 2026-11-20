# Dispatcher operations — state as of 2026-11-20

Operational notes for the live `bridge dispatch --auto` timer. No feature is in
flight; this file is the resume point for follow-up work.

## Current setup

- `bridge-dispatch.timer` (systemd user unit) runs every :00/:30 and has been **live since
  2026-11-13**. The dry-run drop-in is deleted.
- `~/.config/bridge/dispatch.json`:
  - `limits.per_repo: 2`;
  - override `ai-instructions: 1`, which keeps #56 from dispatching while #57 is open;
  - a `reviewed` lane (agent-workflow, ai-instructions, bridge) that adds `ai-review-human-merge`;
  - both windows set `budget_rung: false`.
- **The budget rung is off.** It measures this host's sessions, which run on the business
  Team account, but freaxnx01 pipelines use the private sub. Fix: bridge#401.
- **Cockpit:** #387 is deployed (v2.9.0-228-g43f639c). The journal records ranks and gates.
  The Dispatcher area reads `/api/dispatch/history` and `/api/dispatch/ticks/{at}`.
- **Milestones:** only a *dated* open milestone restricts eligibility, and the earliest due
  date wins, even when past due. The only one right now is ai-instructions
  `general-october-2026` (due 2026-10-30, open).

## Done this session

- Weekend analysis: the global cap was filled with unreviewed drafts. Fixed by adding the
  reviewed lane.
- Merged and deployed bridge#399 (#387). Merged agent-workflow#610.
- Merged agent-workflow#622 (#495) and #623 (#498), which frees cap slots.
- Filed bridge#400, #401 and #402, and agent-workflow#619 and #620.

## Open / next

1. bridge#402: the preview must evaluate exactly the tick's repo set. Its global count
   currently includes anim-bossinfo-ch repos.
2. bridge#401: budget rung per subscription. Then do the TODO.md item "Calibrate
   window_budget_usd" and turn the rung back on.
3. bridge#400: skip issues at the pipeline's attempt cap.
4. agent-workflow#619 and #620: `/enrich` improvements.
5. Small items:
   - remove the stale `ai:failed` and `ai-implement` labels from closed bridge#387;
   - file the agent-workflow#610 review finding (`repo=../r` accepted in autopilot-config).
6. Decisions for the user:
   - ai-instructions #57. Once it merges, drop the `ai-instructions: 1` override.
   - Close or re-date `general-october-2026`.
