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

## Cross-shell parity (bash + PowerShell)

bridge must work end-to-end under both **bash** (Linux/macOS) and
**PowerShell** (Windows). Treat parity as a hard requirement, not a
nice-to-have.

- Two shims ship side-by-side: `shims/bridge-shim.sh` and
  `shims/bridge-shim.ps1`. Any change to the `__preflight` directive
  protocol must update both — and add a test in `shims/bridge-shim.bats`
  (bash) plus an equivalent for the `.ps1` shim.
- Tab-completion goes through Cobra's `ValidArgsFunction`; one
  registration emits scripts for both shells via `bridge completion bash`
  and `bridge completion powershell`. Don't write shell-specific
  completion logic in the shim.
- Launcher paths differ — tmux on Unix, Windows Terminal (`wt.exe`) on
  Windows — but both go through `internal/launcher` argv construction;
  don't fork that.
- If a feature genuinely can't be parity (e.g. relies on tmux), say so
  explicitly in the PR description and degrade gracefully on the other
  platform rather than crashing.
- There is no Windows CI yet (see README), so the PowerShell path must
  be exercised manually for every change touching the shim, launcher,
  completion, or filesystem semantics. The PR template enforces this.

## Commit & release conventions

- Use **Conventional Commits** format for all commit messages
  (`feat: ...`, `fix: ...`, etc.).
- All code lives in `cmd/bridge` (CLI) and `internal/` (libraries). The
  frozen bash scripts were deleted in Phase 4 (v2.1.0); the Go binary
  is the only implementation now.
- Tag releases as `vX.Y.Z` via `git tag` and add a `CHANGELOG.md` entry
  describing the user-visible changes per release. The `v2.0.0-go.N`
  suffix is retired alongside the bash code.

## Working across sessions

Multiple agent/dev sessions often run against this repo at once. To avoid
divergent state, duplicate work, and lost branches:

- **Orient before starting:** `git fetch --prune`, branch off a fresh
  `main` (`git switch main && git pull`), then check `git branch -a`,
  `gh pr list`, `gh issue list`, and `CHANGELOG.md` so you don't redo work
  another session already shipped.
- **Isolate concurrent work:** one git worktree per session — use
  `bridge -w <name>` (lands in `<repo>/.worktrees/<name>`) so sessions don't
  share a working tree. If you can't isolate, serialize: commit/stash before
  another session touches the same files.
- **Push the moment you commit:** `git push -u origin <branch>`. An unpushed
  branch is invisible to other sessions and easily lost.
- **One branch per unit of work**, conventional-commit named (`feat/…`,
  `fix/…`, `docs/…`); small commits keep intent legible across sessions.
- **Leave no residue on merge:** `gh pr merge <n> --squash --delete-branch`,
  then `git pull` in other sessions. Rebase stale branches onto
  `origin/main` before continuing them.
- **Reconcile periodically:** after a release lands, close issues that
  shipped and prune merged branches (`git fetch --prune`,
  `git branch --merged`).
