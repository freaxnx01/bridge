Resume bridge MCP `put_file` work (issue #223). Full implementation plan:
`docs/superpowers/plans/2026-07-31-put-file.md`

Phase: plan written and saved (8 tasks, self-review complete). Policy decisions
already recorded on the issues (owner-scoped allowlist, #217 scope-split) —
see the plan's "Global Constraints" section for links.

Next step: **ask the user which execution mode** — subagent-driven (recommended)
or inline — then execute the plan using `superpowers:subagent-driven-development`
task-by-task, per the plan's own header instruction.
