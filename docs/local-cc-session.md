# Local Claude Code (CC) session — pickup notes

A handoff/cheat-sheet for starting a **local Claude Code CLI session** on the
`bridge` repo and picking up in-flight work. Update the "Current state"
section as work progresses.

## Current state (as of 2026-05-30)

Open issues filed this session — both are TUI/terminal bugs, neither fixed yet:

- **#103** — TUI dashboard top clipped at larger terminal font sizes.
  Root cause: `model.View()` in `internal/tui/tui.go` (~lines 602-630) composes
  the layout with **fixed** heights (`rowH := 11`, sessions `7`, plus
  header/cmd/hint) and never reads `m.height`. Fewer rows → content overflows
  the alt-screen → top scrolls off. Fix: derive panel heights from `m.height`,
  add a min-height guard.
- **#104** — tmux launch under **kitty** fails with
  `missing or unsuitable terminal: xterm-kitty`. Root cause: this is *tmux's*
  error, not bridge's — the host (Chromebook/Crostini) lacks the `xterm-kitty`
  terminfo entry. bridge launches via `tmux new-session`/`switch-client`
  (`internal/launcher/tmux.go:48`). Bridge-side fix options: detect an
  unresolvable `$TERM` before launching tmux and print a hint, document it,
  optionally sanitize `TERM` → `xterm-256color`.

Known limitation (not yet an issue): the TUI dashboard only spans the
**primary** base (`reposRoot()` in `cmd/bridge/bases.go:45`), even though
`discoverAllRoots()` already fans out over all configured bases.

## 1. Start a session

```bash
cd ~/projects/repos/github/freaxnx01/public/bridge
claude
```

On the **Chromebook + kitty** box (see #104), don't launch through
`bridge`/tmux until that's fixed — run `claude` directly, or use a portable
TERM:

```bash
TERM=xterm-256color claude
```

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
