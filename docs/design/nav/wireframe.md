# `bridge nav` — Wireframes (UI Phase 1)

A new subcommand `bridge nav`: a two-screen terminal navigator. **Does not touch**
`bridge` (bare), `bridge tui`, or any existing command. New package `internal/nav`.

- **Architecture:** one Bubble Tea program with a `screen` field
  (`screenPicker` → `screenDash`). Async work (remote repo list, per-worktree
  git-dirty) runs as `tea.Cmd`s posting result messages, with a spinner until
  they land. Attaching/launching uses `tea.ExecProcess` so the terminal is handed
  to `tmux attach`; on detach the same program resumes and refreshes — the
  dashboard returns "like we left it". Non-TTY / SSH-child falls back to
  launch-and-replace.
- **Reuse:** `internal/core`, `internal/worktree`, `internal/launcher`,
  `internal/forge` (incl. `cloneRemoteRepo`), and the btop lipgloss palette.

## Scope (established in brainstorming)

- **Primary goal:** pick a repo, then land on a per-repo launchpad to resume an
  existing agent session/worktree or create a new one, without losing the
  dashboard on detach.
- **Role:** single developer at a TTY; graceful non-TTY degradation.
- **Data — Screen 1:** global active sessions (tmux + slots), local repos
  (immediate), remote repos (forge cache/async, clone-on-select).
- **Data — Screen 2:** the selected repo's active sessions + worktrees
  (dot, worktree, branch, agent, last-accessed `Xd Yh Zm`, async git-dirty).

## Screen 1 — Repo picker (`bridge nav`)

```
┌─ bridge nav · pick a repo ─────────────────────────────────── 2026-05-31 14:22 ┐
└─────────────────────────────────────────────────────────────────────────────────┘
┌─ Active sessions ─────────────────────────────────────────────────────────────┐
│ ● freaxnx01/bridge      worktree-fix-x   claude   1d 2h 20m                     │
│ ○ freaxnx01/ai-instr    worktree-docs    copilot  3h 12m                        │
└─────────────────────────────────────────────────────────────────────────────────┘
┌─ Repos ───────────────────────────────────────────────────  ⠋ loading remote… ┐
│ filter: fix_                                                                    │
│                                                                                 │
│ ▸ github/public/bridge              freaxnx01   local                          │
│   github/public/ai-instructions     freaxnx01   local                          │
│   gitlab/acme/infra-tools           acme        local                          │
│ ↓ github/private/secret-svc         freaxnx01   remote (clone on select)        │
│ ↓ github/public/some-remote-repo    someorg     remote (clone on select)        │
└─────────────────────────────────────────────────────────────────────────────────┘
  ↑↓ move · ⏎ open repo / attach session · / filter · r refresh remote · q quit
```

- Typing edits `filter:`. **↓** moves focus from the filter into the list.
- `⏎` on a repo → Screen 2. `⏎` on an active-session row → attach (detach
  returns here). `⏎` on a `↓ remote` row → clone (progress streams) → Screen 2.
- **Loading:** `Repos` title shows `⠋ loading remote…` until forge resolves;
  local rows already interactive.
- **Empty (no local repos):** body shows `no local repos under
  ~/projects/repos — ↓ remote rows will appear once loaded`.
- **No sessions:** the `Active sessions` panel is hidden entirely.
- **Error (remote fetch failed):** title shows `remote unavailable (cached rows
  shown)` in the warn colour.

## Screen 2 — Repo dashboard

```
┌─ bridge nav · freaxnx01/bridge ──────── ~/projects/.../freaxnx01/public/bridge ┐
└─────────────────────────────────────────────────────────────────────────────────┘
┌─ Sessions & Worktrees ──────────────────────────────────  ⠙ checking git…  ───┐
│  ● worktree-fix-x      fix-x        claude    1d 2h 20m    ●3  ↑11             │
│  ○ worktree-docs       docs         copilot   3h 12m       ✓ clean            │
│  ○ worktree-spike      spike        —         (no session) ●1                  │
│                                                                                 │
│  + Create new worktree…                                                         │
└─────────────────────────────────────────────────────────────────────────────────┘
  ↑↓ move · ⏎ attach / launch · n new worktree · esc back · q quit

   (later: Branches · Recent commits · Git status · Open issues · forge statusbar)
```

- Rows: `● attached / ○ detached / · no-session`, worktree, branch, agent
  (`—` if no live session), last-accessed (`Xd Yh Zm`, sorted desc), then
  **async git-dirty** (`●N` modified, `↑N` ahead, `✓ clean`).
- `⏎` attaches an existing session, or launches the default agent for a
  worktree with none. `esc` → back to Screen 1. `q` quits.
- The dimmed footer names deferred btop sections (Branches, Recent commits,
  Git status, Open issues, forge statusbar) — none built in v1.

### Create-worktree (inline modal state) — `n` or the `+` row

```
┌─ New worktree ────────────────────────────────────────────┐
│  name: fix-login_                                          │
│                                                            │
│  → creates worktree-fix-login at .worktrees/fix-login      │
│    then launches claude (BRIDGE_DEFAULT_AGENT) + attaches  │
│                                                            │
│  ⏎ create & launch    esc cancel                           │
└────────────────────────────────────────────────────────────┘
```

- On `⏎`: `git worktree add` (branch `worktree-<name>`, dir
  `.worktrees/<name>`) → `launcher` argv → `tea.ExecProcess` attach. Detach
  returns to this dashboard with the new session listed.
- **Error (name collides with existing worktree/branch):** shown inline under
  the input in the bad colour; input stays focused for a retry.

---

_Status: approved 2026-05-31. Next: UI Phase 2 (flow / Mermaid state diagram)._
