# Ideas

## Dispatch REST API + WebUI exposure
- id: 2026-07-28-dispatch-rest-api-webui-exposure · captured: 2026-07-28 · status: raw
- value: Lets the WebUI/MCP consumers surface dispatch status without shelling out to the CLI
Follow the existing internal/api handler pattern (OverviewHandler, ReposHandler, etc.) served by internal/web — e.g. an internal/api/dispatch.go handler wrapping internal/dispatch, and possibly an mcp__bridge__dispatch_status MCP tool. Not in scope for the current 2026-07-27-bridge-dispatcher-plan.md (Tasks 1-9), which is CLI-only (bridge dispatch --dry-run/now/pause/resume/status) with state in forge labels + local JSON — follow-up once that plan lands.

## Configurable repo-priority list for dispatch
- id: 2026-07-30-configurable-repo-priority-list-for-dispatch · captured: 2026-07-30 · status: issue #222 (https://github.com/freaxnx01/bridge/issues/222)
- value: Lets operators steer which repos get dispatched first (e.g. software factory > non-game > game repos), instead of relying only on the milestone/type/size/age ladder
Example priority order given: 1) software factory repos, 2) non game-* repos, 3) game-* repos. Would sit alongside (or extend) the existing 4-rung dispatch priority ladder (milestone due date → type → size → age) — likely as a new top-level rung or a tiebreaker, configurable via dispatch.json.
