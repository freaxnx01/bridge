# CLAUDE.md

Agent context for Claude Code working on the bridge repo. Read this before
taking action.

## Project Overview

**Name:** `bridge`
**Purpose:** Repo picker and agent-session launcher. Walks
`~/projects/repos/`, presents an fzf picker, then opens the selected repo
in a tmux-wrapped agent session (Claude Code, Copilot, opencode, or VS Code)
or just `cd`'s into it.
**Architecture:** Go binary at `~/.local/bin/bridge` + thin shell-function
shim that handles `cd:` / `exec:` / `noop` directives the binary emits.
**Status:** Active; v2.1.0 cutover to pure-Go complete (Phase 4, #35).

See [`README.md`](README.md) for layout, CLI surface, cache files, and
package map. See [`go-migrate.md`](go-migrate.md) for install/update.

## Role in the personal dev workflow

This repo IMPLEMENTS part of the personal dev workflow AND consumes it for
its own day-to-day work. The implementer role is additive: it follows all
workflow conventions like any consumer, and additionally treats the workflow
doc as design input.

**Design source:**
- Workflow doc: `ai-instructions` repo, file
  `workflows/personal-dev-workflow.md`
  (<https://github.com/freaxnx01/ai-instructions/blob/main/workflows/personal-dev-workflow.md>)
- Bridge's own design docs: `docs/specs/` and `docs/plans/`.

**Read both before non-trivial changes.** Changes here may require
corresponding updates to the workflow doc in `ai-instructions`.

Routing thoughts in this repo follows the implementer-repo addendum:
- Changes to bridge's behavior → bridge Issue or `docs/specs/`
- Changes to how the workflow itself is described → `ai-instructions`

## Essential Commands

```bash
# Build + install locally (also: just build)
make install-go

# Build only (no install)
make build-go

# Run all tests
go test ./...

# Run a single test
go test ./cmd/bridge -run TestXxx

# Full Go + shim test pass (also: just test)
make all

# Print version after install
bridge --version
```

Cross-compile for Windows:

```bash
GOOS=windows GOARCH=amd64 go build ./cmd/bridge
```

## Commit & release conventions

- Use **Conventional Commits** format for all commit messages
  (`feat: ...`, `fix: ...`, etc.).
- All code lives in `cmd/bridge` (CLI) and `internal/` (libraries). The
  frozen bash scripts were deleted in Phase 4 (v2.1.0); the Go binary
  is the only implementation now.
- Tag releases as `vX.Y.Z` via `git tag` and add a `CHANGELOG.md` entry
  describing the user-visible changes per release. The `v2.0.0-go.N`
  suffix is retired alongside the bash code.
