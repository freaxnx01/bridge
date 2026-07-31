Resume bridge MCP tools work. Full status/artifact:
`docs/ai-notes/2026-07-31-bridge-mcp-tools-status.md`

Phase: #220, #219, #221 are done and merged to `main` (PRs #224/#225/#226).
Next step: **#223 (tree writes / PR-opening) is blocked on a repo-allowlist
policy decision — ask the user which option before writing any code** (see
artifact for the options). A small independent fix is also flagged there
(stale `claude-pipeline` ref in `freaxnx01/quicktask-vikunja`), unrelated to
#223.

Once #223's policy is decided, implement using
`superpowers:subagent-driven-development`, following the same conventions as
#220/#219/#221 documented in the artifact.
