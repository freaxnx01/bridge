# Ideas

Loose backlog of bridge feature ideas. Promote to `docs/specs/<date>-<slug>-design.md` when a real design starts.

- Attach to all open sessions (`--status`) via Windows Terminal — open one WT tab per active slot/tmux session so the status picker becomes a one-shot "attach everywhere" flow.
- Directory mode: just `cd` to the repo dir without launching Claude (e.g. `bridge -c <name>` or a picker keybinding) — useful when you want to poke around in a shell first.
- Analyze: should bridge support branches in addition to worktrees? When does a worktree make sense vs. a plain branch checkout? Decide the UX and document the guidance.
- **Dashboard TUI (issue #30)** — Bubbletea PoC lives at `prototypes/dashboard-tui/` (commit `864d248`). Decide whether to promote: wire real data (add `bridge --dashboard --json` or shell out), trim cosmetic glitches (lipgloss `.Width` adds 2 trailing spaces with rounded borders), then decide on packaging — ship `bridge-tui` as a sibling Go binary, or keep bash `--dashboard` as the default and offer the TUI via `--tui`. Open questions: Windows/PowerShell story (Go cross-compile is trivial, bash isn't an option there), how to surface attach actions back into the bash side. See: https://github.com/freaxnx01/bridge/tree/main/prototypes/dashboard-tui
- Win/pwsh variant? (e.g. for quicktask 'make windows')

## Agent / Assistant / Bot integration (2026-06-24)

**Capture & Triage**
- `/ask <question>` in bridge-bot — ask about repos/open issues, get a summary
- Auto-label on `/issue` capture — background call adds `value/N` + `effort/N` labels based on title analysis
- `/plan <text>` — capture idea → AI expands to mini-spec draft → creates GitHub issue with structured body

**Status & Report**
- `bridge agents` nav screen / WebUI panel — wraps `claude agents --json`, shows all background Claude sessions across repos (status + last output line)
- bridge-bot `/status` improvement — pull from `claude agents --json` for richer report (what each agent is doing, not just tmux session names)
- Session summary on exit — when a tmux slot closes, bridge-bot sends Telegram summary (last N output lines, files changed)
- "What next?" button (WebUI Overview) — sends current Snapshot to Claude, returns ranked recommendation with reasoning
- Stale issue detector — highlight issues untouched >30 days with AI-generated "still relevant?" nudge
- Agent ping in Radar viz wired to real `claude agents` data (already mocked in bridge-poc2.html)

**Workflow Automation**
- Issue → worktree routing — new GitHub issue webhook → create worktree → optionally spawn Claude agent pre-loaded with issue context
- `/deploy` in bot — trigger `just build` + homelab service restart on a chosen repo via Telegram
