# Ideas

## Dispatch REST API + WebUI exposure
- id: 2026-07-28-dispatch-rest-api-webui-exposure · captured: 2026-07-28 · status: raw
- value: Lets the WebUI/MCP consumers surface dispatch status without shelling out to the CLI
Follow the existing internal/api handler pattern (OverviewHandler, ReposHandler, etc.) served by internal/web — e.g. an internal/api/dispatch.go handler wrapping internal/dispatch, and possibly an mcp__bridge__dispatch_status MCP tool. Not in scope for the current 2026-07-27-bridge-dispatcher-plan.md (Tasks 1-9), which is CLI-only (bridge dispatch --dry-run/now/pause/resume/status) with state in forge labels + local JSON — follow-up once that plan lands.
