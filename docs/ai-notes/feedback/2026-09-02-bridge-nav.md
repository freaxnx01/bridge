# Bridge nav — tester feedback triage (2026-09-02)

status: done

| # | Note (normalized, short EN) | Att. | Topic | Kind | Disposition | Rationale / link | Status |
|---|---|---|---|---|---|---|---|
| 1 | wt/branch selector barely visible under Solarized theme |  | area:nav | Improvement (bug) | Issue | Colors are hardcoded `lipgloss.Color` hex constants (`internal/nav/view.go:11-18`), not `lipgloss.AdaptiveColor` — no adaptation for light/high-contrast terminal profiles like Solarized. No open issue covers this. → [#255](https://github.com/freaxnx01/bridge/issues/255) | done |
| 2 | grid "bounces"/unstable when moving cursor across sessions & worktrees; frame sizes (e.g. top frame) should be fixed |  | area:nav | Improvement (bug) | Issue | `viewDash()` computes panel height from natural content height per selection (`internal/nav/view.go:309-358`, esp. 344-346: "render each at natural height, take the taller, re-render"); height varies with which worktree/session is selected since detail-column content differs, producing the visible resize. No open issue covers layout stability. → [#256](https://github.com/freaxnx01/bridge/issues/256) | done |
| 3 | support `claude --resume` to continue a session |  | area:nav | New feature | Issue | `internal/agents/agents.go` registry has no resume/continue args; nav always launches plain `claude` with no flags (`internal/nav/update.go:878`). New capability needing design (how to detect a resumable session, flag wiring). No open issue covers it. → [#257](https://github.com/freaxnx01/bridge/issues/257) | done |

## Raw notes

```
[#1] wt/branch selector barely visible with Solarized color theme
[#2] when switching btw sessions & worktrees with cursor keys, the grid is "bouncing". Regarding visualization looks "unstable". The frame sizes of e.g. top frame schould be fixed
[#3] support for 'claude --resume' to continue session
```
