# PROJECT-OVERVIEW.md

Product / project context for the `bridge` repo. Read alongside `CLAUDE.md`
(which carries the generic + Go-stack conventions).

## Name

`bridge`

## Purpose

Repo picker and agent-session launcher. Walks `~/repos/`, presents an
fzf picker, then opens the selected repo in a tmux-wrapped agent session
(Claude Code, Copilot, opencode, or VS Code) or just `cd`'s into it.

## Architecture (one paragraph)

A Go binary at `~/.local/bin/bridge` plus a thin shell-function shim that
handles the `cd:` / `exec:` / `noop` directives the binary emits. All code
lives in `cmd/bridge` (CLI) and `internal/` (libraries) — the frozen bash
scripts were deleted in Phase 4 (v2.1.0); the Go binary is the only
implementation now.

## Status

Active; v2.1.0 cutover to pure-Go complete (Phase 4, #35).

## Pointers

- [`README.md`](README.md) — layout, CLI surface, cache files, package map.
- [`go-migrate.md`](go-migrate.md) — install / update.
- [`docs/history.md`](docs/history.md) — evolution (clrepo → bridge → Go → nav TUI) + roadmap (next: WebUI with visualization; PoC at `docs/design/bridge-poc2.html`).
- Bridge's own design docs: `docs/specs/` and `docs/plans/`.
