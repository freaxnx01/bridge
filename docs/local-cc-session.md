# Local Claude Code (CC) session — pickup notes

A handoff/cheat-sheet for starting a **local Claude Code CLI session** on the
`bridge` repo and picking up in-flight work. Update the "Current state"
section as work progresses.

## Current state (as of 2026-05-30)

Two TUI/terminal bugs were filed this session: **#104** is now fixed and
merged to `main`; **#103** is still open.

- **#103** — TUI dashboard top clipped at larger terminal font sizes.
  Root cause: `model.View()` in `internal/tui/tui.go` (~lines 602-630) composes
  the layout with **fixed** heights (`rowH := 11`, sessions `7`, plus
  header/cmd/hint) and never reads `m.height`. Fewer rows → content overflows
  the alt-screen → top scrolls off. Fix: derive panel heights from `m.height`,
  add a min-height guard.
- **#104** — *fixed* (merged to `main`, PR #105). tmux launch under **kitty**
  on a host lacking the `xterm-kitty` terminfo aborted with
  `missing or unsuitable terminal`. bridge now detects an unresolvable `$TERM`
  via `infocmp` before launching tmux and transparently falls back to
  `TERM=xterm-256color` with a one-line notice (helper `maybeTermFallback` in
  `cmd/bridge`, applied via `emitLaunch`). Disable with
  `BRIDGE_NO_TERM_FALLBACK=1`.

Known limitation (not yet an issue): the TUI dashboard only spans the
**primary** base (`reposRoot()` in `cmd/bridge/bases.go:45`), even though
`discoverAllRoots()` already fans out over all configured bases.

## 1. Start a session

```bash
cd ~/projects/repos/github/freaxnx01/public/bridge
claude
```

On the **Chromebook + kitty** box (see #104), bridge now auto-falls-back to
`TERM=xterm-256color` when launching tmux, so `bridge` works directly. To opt
out and see the raw tmux error, set `BRIDGE_NO_TERM_FALLBACK=1`.

## 2. Orient before touching anything

```bash
git fetch --prune
git switch main && git pull
git branch -a
gh pr list
gh issue list
```

## 3. Isolate the work

One worktree per session:

```bash
bridge -w <name>            # lands in <repo>/.worktrees/<name>
# or native git:
git switch -c fix/<short-name>
```

## 4. Build / test loop

```bash
make build-go      # build only
make install-go    # build + install to ~/.local/bin/bridge
go test ./...      # all tests
go test ./internal/tui -run TestXxx -v   # one test
make all           # full Go + shim pass (also: just test)
bridge --version   # confirm installed build
```

## 5. Conventions

- **Conventional Commits** (`feat:`, `fix:`, `docs:`).
- One branch per unit of work; **push the moment you commit**:
  `git push -u origin <branch>`.
- Cross-shell parity is a hard requirement — anything touching the
  shim/launcher/completion must update both `shims/bridge-shim.sh` **and**
  `shims/bridge-shim.ps1`, with tests.
- On merge: `gh pr merge <n> --squash --delete-branch`, then `git pull`
  elsewhere.
- Read `workflows/personal-dev-workflow.md` (in the `ai-instructions` repo)
  plus `docs/specs/` before non-trivial changes.

## 6. End of session

```bash
git status                                  # nothing uncommitted/unpushed
gh pr create --fill                         # if ready for review
git fetch --prune && git branch --merged    # reconcile
```
