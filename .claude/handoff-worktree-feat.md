## Resume: #326 shipped — pick up its follow-ups

#326 (read-only `list_prs` / `get_pr` / `list_runs` MCP tools) is **done**: PR #332 squash-merged as `266407b` on 2026-10-02, and the issue is closed.
- Spec: `docs/superpowers/specs/2026-10-02-mcp-pr-checks-runs-tools-design.md`
- Plan: `docs/superpowers/plans/2026-10-02-mcp-pr-checks-runs-tools.md`
- Tool docs: `docs/mcp-cheatsheet.md`

Open follow-ups, none started:
- **bridge#334**: `fix(mcp)`: `get_pr` should serialize `merged:false`, and `ListCommitStatuses` should request `per_page=100`. Small. Labeled `needs-enrichment`.
- **bridge#333**: `ci(agent)`: the `PIPELINE_APP_ID` secret is missing, so pipeline PRs are opened by `github-actions` and their CI sits at `action_required` until approved by hand. Setting the secret is a manual step for the user (see agent-workflow `docs/PIPELINE-APP-SETUP.md`).
- **agent-workflow#446**: the review job's hardcoded 10-minute timeout cancelled #332's re-review after a self-fix. Data-point comment added; nothing more to do from bridge.

**Next step:** run `/enrich 334`, then `/gh:implement 334`. Or implement it locally with `superpowers:subagent-driven-development`; it's two small TDD changes in `internal/forge`. Ask the user about #333, because only they can set the secret. Keep in mind:
- `main` is protected, so changes go in through a PR.
- Dispatch subagents with absolute worktree paths.
- If `gh` returns 401, stop and tell the user. Don't fall back to other auth.
