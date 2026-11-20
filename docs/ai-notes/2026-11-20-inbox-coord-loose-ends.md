# Inbox coordination session: state and loose ends (2026-11-20)

Coordinator session over freaxnx01/bridge + freaxnx01/agent-workflow. Rule of the
session: nothing is implemented locally. All code goes through the agent-workflow
pipeline (`ai-implement` + `ai-review-human-merge`), and every GitHub write needs
the user's explicit OK in that step.

## Done (all merged on bridge main)

| PR | What |
|---|---|
| #389 | #253: retry/redispatch (4-step label swap, `RedispatchError{Step}`) |
| #392 | #390: retry guard. Stale re-dispatch over 3 h → Human `re-dispatch never reported`. Pending → Wait `retry pending`. Open PR → Human `open PR`. Shared `StateFor` constructor. |
| #394 | #393: cockpit repo filter bar (forge chips, owner chips, multi-repo dropdown; URL + localStorage `bridge.repoFilter`) |
| #396 | `.github/workflows/web.yml`: vitest + vite build on PRs and main |
| #397 | #395: filter-bar review fixes (`a4bef0a`) |

The cockpit (`bridge-serve`, :7777) was rebuilt and restarted at `a4bef0a`. The
dispatch timer has been live since 2026-11-13 (see the memory, not this session).

## Open: needs a user decision before any action

1. **#397 low review concerns.** Not filed. File one follow-up issue only if the user wants it:
   - comment-read warnings are not repo-filtered
   - no backoff while `/api/repos` keeps failing
   - `loadRepos` has no in-flight guard (startup race)
   - the Arch-tab gate predicate can drift from the render branch
2. **`web / test` as a required check.** Optional branch-protection change, for the user.
3. **Unfiled discoveries.** Park as issues only on request:
   - agent-workflow: a self-fix commit touching `.github/workflows/**` is rejected on push (the token has no workflows scope), and every fix in that commit is lost.
   - agent-workflow #351: the consumer stub has no `workflow_dispatch`.
   - bridge has no `PIPELINE_APP_ID`, so each pipeline commit's CI needs `gh api -X POST .../actions/runs/<id>/approve`.
   - `ensure-toolchain.sh` has no fallback when apt is unreachable (the first #253 run failed on that).
   - `check-attempt-cap.sh` has no author filter.

## Next step

Ask the user (AskUserQuestion) which of items 1 and 3 to file. Then enrich and
dispatch through the pipeline as before, using `/enrich` and `/gh:implement`.
