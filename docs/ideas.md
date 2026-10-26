# Ideas

## Dispatch REST API + WebUI exposure
- id: 2026-07-28-dispatch-rest-api-webui-exposure · captured: 2026-07-28 · status: raw
- value: Lets the WebUI/MCP consumers surface dispatch status without shelling out to the CLI
Follow the existing internal/api handler pattern (OverviewHandler, ReposHandler, etc.) served by internal/web — e.g. an internal/api/dispatch.go handler wrapping internal/dispatch, and possibly an mcp__bridge__dispatch_status MCP tool. Not in scope for the current 2026-07-27-bridge-dispatcher-plan.md (Tasks 1-9), which is CLI-only (bridge dispatch --dry-run/now/pause/resume/status) with state in forge labels + local JSON — follow-up once that plan lands.

## Configurable repo-priority list for dispatch
- id: 2026-07-30-configurable-repo-priority-list-for-dispatch · captured: 2026-07-30 · status: done (PR #227, https://github.com/freaxnx01/bridge/pull/227)
- value: Lets operators steer which repos get dispatched first (e.g. software factory > non-game > game repos), instead of relying only on the milestone/type/size/age ladder
Example priority order given: 1) software factory repos, 2) non game-* repos, 3) game-* repos. Would sit alongside (or extend) the existing 4-rung dispatch priority ladder (milestone due date → type → size → age) — likely as a new top-level rung or a tiebreaker, configurable via dispatch.json.

## Idea capture is GitHub-only — make it forge-aware for aliases
- id: 2026-08-18-idea-capture-forge-aware · captured: 2026-08-18 · status: filed (#246, https://github.com/freaxnx01/bridge/issues/246)
- value: An alias that resolves to a forgejo repo can be used for issue capture but not idea capture — closes that asymmetry
The `/api/capture/idea` closure in cmd/bridge/serve.go unconditionally builds a GitHub client + fetches a GitHub token for the target owner, so an alias resolving to a **forgejo** repo yields a confusing 500 (`no github token for owner`) instead of writing the idea. Pre-existing behavior (idea capture was GitHub-only before alias routing), surfaced by the 2026-08-18 alias-capture-routing branch's final review. Fix: branch on the resolved repo's `Forge` like the issue path does (reuse `issueCreatorFor`'s pattern), or explicitly reject a non-github alias with a clear message. Out of scope for the alias-routing branch.

## serve.go capture closures swallow the repo-discovery error
- id: 2026-08-18-serve-discovery-error-swallowed · captured: 2026-08-18 · status: filed (#244, https://github.com/freaxnx01/bridge/issues/244)
- value: A filesystem-walk failure during discovery would misreport as 404 (unknown alias) instead of 500, hiding the real cause
`repos, _ := discoverAllRoots()` in the cmd/bridge/serve.go capture closures discards the discovery error, so if discovery fails, `core.ResolveAlias` sees an empty slice and returns `ErrAliasNotFound` → 404, masking the actual failure. Pre-existing pattern (the old inline closures did the same); noted in the 2026-08-18 alias-capture-routing final review. Fix: surface the discovery error as a 500. Low urgency (walk failures are rare/local).

## Integration-test the /api/capture bearer wiring in runServe
- id: 2026-08-18-mux-wiring-integration-test · captured: 2026-08-18 · status: filed (#245, https://github.com/freaxnx01/bridge/issues/245)
- value: Locks the auth boundary (capture gated, read endpoints open) against regression — currently proven only by code inspection
`requireBearer` is unit-tested in isolation (4 states), but nothing asserts that `runServe` actually wraps `/api/capture/` while leaving `/api/repos`/`/api/overview`/`/api/agents` open. Adding this needs a small refactor to extract the mux-building from `runServe` so a test can build the same mux and hit `/api/repos` (200, no token) + `/api/capture/issue` (401, no token). Recommended by the 2026-08-18 alias-capture-routing final review; deferred because it touches production structure beyond that plan.

## bridge bot: Overview and Agents screens
- id: 2026-10-26-bot-overview-agents · captured: 2026-10-26 · status: raw
- value: Phone access to nav's cross-repo Overview (ranked issues/roadmap/captures) and the Agents screen
Out of v1 of the Telegram nav bot (spec docs/superpowers/specs/2026-10-11-bridge-telegram-nav-bot-design.md). Would add screens to internal/bot reusing overview.Build and the agentview data nav already loads.

## bridge bot: idea / issue capture
- id: 2026-10-26-bot-capture · captured: 2026-10-26 · status: raw
- value: Capture an idea or open an issue from the phone without a Claude session
Out of v1 of the Telegram nav bot. Free text would go to the existing capture paths (internal/capture); needs a prompt flow and repo/alias choice.

## bridge bot: notes preview (ideas.md / TODO.md)
- id: 2026-10-26-bot-notes-preview · captured: 2026-10-26 · status: raw
- value: Read a repo's backlog files on the dashboard, like nav's right column
Out of v1 of the Telegram nav bot. nav's readRepoNotes would move to internal/navdata first.

## bridge bot: git side panels (branches / recent commits / status)
- id: 2026-10-26-bot-git-panels · captured: 2026-10-26 · status: raw
- value: nav's per-worktree detail panels on the phone
Out of v1 of the Telegram nav bot. nav's gitBranchesCmd/gitCommitsCmd/gitStatusCmd parsers would move to internal/navdata.

## bridge bot: multiple concurrent users
- id: 2026-10-26-bot-multi-user · captured: 2026-10-26 · status: raw
- value: More than one person steering the same bridge host
v1 keeps per-chat state and an allowlist but assumes one operator: Busy and confirms are per chat, not per user, and there is no per-user audit. Out of v1 of the Telegram nav bot.

## bridge bot: persist chat state across restarts
- id: 2026-10-26-bot-persist-state · captured: 2026-10-26 · status: raw
- value: Buttons keep working after a service restart instead of answering "outdated — /nav"
Out of v1 of the Telegram nav bot. Would store per-chat State (message id, epoch, screen, repo) under the cache dir and reload it on start.
