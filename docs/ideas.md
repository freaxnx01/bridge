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
- id: 2026-08-18-idea-capture-forge-aware · captured: 2026-08-18 · status: raw
- value: An alias that resolves to a forgejo repo can be used for issue capture but not idea capture — closes that asymmetry
The `/api/capture/idea` closure in cmd/bridge/serve.go unconditionally builds a GitHub client + fetches a GitHub token for the target owner, so an alias resolving to a **forgejo** repo yields a confusing 500 (`no github token for owner`) instead of writing the idea. Pre-existing behavior (idea capture was GitHub-only before alias routing), surfaced by the 2026-08-18 alias-capture-routing branch's final review. Fix: branch on the resolved repo's `Forge` like the issue path does (reuse `issueCreatorFor`'s pattern), or explicitly reject a non-github alias with a clear message. Out of scope for the alias-routing branch.

## serve.go capture closures swallow the repo-discovery error
- id: 2026-08-18-serve-discovery-error-swallowed · captured: 2026-08-18 · status: raw
- value: A filesystem-walk failure during discovery would misreport as 404 (unknown alias) instead of 500, hiding the real cause
`repos, _ := discoverAllRoots()` in the cmd/bridge/serve.go capture closures discards the discovery error, so if discovery fails, `core.ResolveAlias` sees an empty slice and returns `ErrAliasNotFound` → 404, masking the actual failure. Pre-existing pattern (the old inline closures did the same); noted in the 2026-08-18 alias-capture-routing final review. Fix: surface the discovery error as a 500. Low urgency (walk failures are rare/local).

## Integration-test the /api/capture bearer wiring in runServe
- id: 2026-08-18-mux-wiring-integration-test · captured: 2026-08-18 · status: raw
- value: Locks the auth boundary (capture gated, read endpoints open) against regression — currently proven only by code inspection
`requireBearer` is unit-tested in isolation (4 states), but nothing asserts that `runServe` actually wraps `/api/capture/` while leaving `/api/repos`/`/api/overview`/`/api/agents` open. Adding this needs a small refactor to extract the mux-building from `runServe` so a test can build the same mux and hit `/api/repos` (200, no token) + `/api/capture/issue` (401, no token). Recommended by the 2026-08-18 alias-capture-routing final review; deferred because it touches production structure beyond that plan.
