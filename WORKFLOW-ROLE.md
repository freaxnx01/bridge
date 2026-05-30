# WORKFLOW-ROLE.md

This repo's place in the personal dev workflow. Read alongside `CLAUDE.md`.

## Role: implementer + consumer

This repo **implements** part of the personal dev workflow **and** consumes it
for its own day-to-day work. The implementer role is additive: it follows all
workflow conventions like any consumer, and additionally treats the workflow
doc as design input.

## Design source

- Workflow doc: `ai-instructions` repo, file
  `workflows/personal-dev-workflow.md`
  (<https://github.com/freaxnx01/ai-instructions/blob/main/workflows/personal-dev-workflow.md>)
- Bridge's own design docs: `docs/specs/` and `docs/plans/`.

**Read both before non-trivial changes.** Changes here may require
corresponding updates to the workflow doc in `ai-instructions`.

## Routing thoughts (implementer-repo addendum)

- Changes to bridge's behavior → bridge Issue or `docs/specs/`
- Changes to how the workflow itself is described → `ai-instructions`
