# Ideas

Loose backlog of clrepo feature ideas. Promote to `docs/specs/<date>-<slug>-design.md` when a real design starts.

- Attach to all open sessions (`--status`) via Windows Terminal — open one WT tab per active slot/tmux session so the status picker becomes a one-shot "attach everywhere" flow.
- Directory mode: just `cd` to the repo dir without launching Claude (e.g. `clrepo -c <name>` or a picker keybinding) — useful when you want to poke around in a shell first.
- Analyze: should clrepo support branches in addition to worktrees? When does a worktree make sense vs. a plain branch checkout? Decide the UX and document the guidance.
- **Dashboard TUI (issue #30)** — Bubbletea PoC lives at `prototypes/dashboard-tui/` (commit `864d248`). Decide whether to promote: wire real data (add `clrepo --dashboard --json` or shell out), trim cosmetic glitches (lipgloss `.Width` adds 2 trailing spaces with rounded borders), then decide on packaging — ship `clrepo-tui` as a sibling Go binary, or keep bash `--dashboard` as the default and offer the TUI via `--tui`. Open questions: Windows/PowerShell story (Go cross-compile is trivial, bash isn't an option there), how to surface attach actions back into the bash side. See: https://github.com/freaxnx01/clrepo/tree/main/prototypes/dashboard-tui
