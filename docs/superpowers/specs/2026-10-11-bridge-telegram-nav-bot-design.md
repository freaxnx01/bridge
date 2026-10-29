# bridge Telegram nav bot (`bridge bot`) — design

**Status:** approved in session 2026-10-07..11
**Replaces:** the Python `bridge-bot/` (spec `docs/specs/2026-05-24-bridge-telegram-bot-design.md`)

## Goal

Steer `bridge nav` from a phone: a Telegram bot with inline-keyboard buttons
that offers nav's repo picker and per-repo dashboard — pick a repo, see its
worktrees and sessions with git status, start/open a session (Remote Control
link), stop it, create a worktree, launch a session on an issue, clone a remote
repo, create a new repo. No Claude in the loop: taps are deterministic, instant
and cost no tokens.

## Scope

In v1 (nav parity A + B + C):

- **A** Repo picker + repo dashboard: worktrees and sessions with status,
  start/open (RC link), stop, new worktree.
- **B** Issues on the dashboard: list a repo's open issues, start a session on
  one ("#123 [title]" label, `issue-<n>-<slug>` worktree) exactly like nav.
- **C** New repo (nav's Ctrl+N) and clone-on-select for remote repos.

Out of v1 (captured in `docs/ideas.md`): Overview and Agents screens,
idea/issue capture, notes (`ideas.md`/`TODO.md`) preview, git side panels
(branches/commits), multiple concurrent users, persisting chat state across
restarts.

## Language

Bot texts are English only: this is personal dev tooling (exempt from the
de/en rule for user-facing apps), like the rest of bridge's CLI/TUI output.

## Bots

A Bot API token can be long-polled by one process only.

- **Buttons bot (this spec):** a new bot with "bridge" in its name (e.g.
  `@freax_bridge_bot`), token in Passbolt.
- **Claude admin** (`bridge-admin` tmux session, `docs/admin-session.md`) keeps
  `@agent_dev_ctl_bot`. Unaffected by this work.

## Architecture

```
Telegram ⇄ internal/telegram   stdlib Bot API client
              │ events (text, /cmd, callback)
              ▼
         internal/bot          screen state machine: Update(state, event) → (state, actions)
              │ actions
              ▼
         internal/navdata      nav's data + mutations, TUI-free
         internal/launch       repo/worktree/agent/slot resolution + detached tmux launch
              ▲
         internal/nav          unchanged behaviour; its tea.Cmds wrap navdata
         cmd/bridge            `bridge bot`, `bridge launch`, `bridge <repo>` wrap launch
```

### Units

| Unit | Responsibility | Depends on |
|---|---|---|
| `internal/navdata` | Local repos (+MRU order), cached remote repos, dashboard rows for a repo (worktrees incl. pinned base row, live session per worktree, dirty/ahead/behind), git fetch, cached open issues, create worktree, issue worktree name/label, clone remote, create repo. Plain functions taking `context.Context`, returning values/errors — no Bubble Tea. | core, forge, launcher, worktree |
| `internal/launch` | Moved from `cmd/bridge/launch_target.go` + the detached part of `launch.go`: `ResolveTarget`, `Finalize`, `StartDetached`, `WaitRCURL`, typed lookup errors. `cmd/bridge` keeps flag parsing, exit codes and printing. | core, agents, launcher, hooks, syncer, store, worktree |
| `internal/telegram` | Minimal Bot API client over `net/http`: `GetUpdates` (long poll, offset), `SendMessage`, `EditMessageText`, `AnswerCallbackQuery`, inline keyboard types. Retry with backoff on 429/5xx; typed error for 409 (another poller). No new Go module. | stdlib |
| `internal/bot` | Per-chat state (screen, page, filter, forge filter, selected repo, pending prompt, id→target maps) and `Update(state, event) (state, []Action)`; `Render(state) (text, keyboard)`. Pure: all I/O happens in actions executed by the runner. | navdata types only |
| `cmd/bridge/bot.go` | `bridge bot` command: load config, run the update loop, execute actions (navdata/launch/tmux) with timeouts, feed results back as events, render + edit. | all of the above |

`internal/nav` keeps its exact behaviour; its data `tea.Cmd`s become thin
wrappers around `navdata`, so nav and the bot always show the same state.

## Screens

One message per chat, edited in place. `/nav` or `/start` opens Screen 1.

**Screen 1 — Repo picker**

```
🗂 bridge · repos   [All ▸]
⭐ bridge            ⭐ FlowHub-CAS-AISE
   agent-workflow       config
   ↓ obsidian-it
[◀] 1/4 [▶]  [🔍 Filter]  [➕ New repo]  [🔄]
```

- MRU first (⭐), then alphabetical; 2 per row, 8 per page; remote-only repos
  marked ↓ (from the remote cache, like `bridge -r`).
- Forge filter button cycles All → each forge present (nav's ctrl+f).
- 🔍 Filter: bot asks for text; the next text message is the filter
  (case-insensitive substring, nav semantics); ✖ clears it.
- ➕ New repo: asks for a name, then forge × visibility buttons (nav's Ctrl+N),
  confirm, create, open its dashboard.
- Tap ↓ remote repo: "Clone <name>? ✅ / ✖", clone, open its dashboard.
- 🔄 refreshes local repos and the remote cache.

**Screen 2 — Repo dashboard**

```
📁 bridge · github/freaxnx01
main      ● attached   ✓
tg-admin  ○            ✎3 ↑2
reorg     ● detached   ✓ ↓1
Issues: #312 fix nav esc · #305 … (+3)
[main ▶] [tg-admin ▶] [reorg ▶]
[reorg ⏹]
[➕ Worktree] [🐞 Issues] [🔄] [◀ Repos]
```

- One line per worktree: session state (● attached/detached/agent state, ○ none)
  and git status (✓ clean, ✎N modified, ↑ahead ↓behind, ∅ no upstream).
- ▶ start/open: `launch.StartDetached` with `--rc` semantics; already running →
  "already running". The RC link is sent as a **separate new message** (phone
  notification); if none appears: the same wording as `bridge launch`
  ("session running, no link yet" / "session exited during startup").
- ⏹ stop (only for running sessions): "Stop <slot>? ✅ / ✖" → `tmux kill-session`.
- ➕ Worktree: asks for a name, creates it, starts a session in it, sends the link.
- 🐞 Issues: list screen, one button per issue (paged); tap → "Start #312? ✅ / ✖"
  → issue worktree + session labelled "#312 [title]" exactly like nav.
- 🔄 reloads rows and runs the git fetch nav runs, for fresh ahead/behind.
- ◀ Repos returns to Screen 1 with its page/filter preserved.

## Data flow (per event)

1. Update arrives. If the sender's user id is not in the allowlist: drop, log one line.
2. Callback → `AnswerCallbackQuery` immediately.
3. `bot.Update` returns new state + actions. Slow actions render "⏳ …" first.
4. The runner executes each action in a goroutine with a `context` timeout
   (30 s; 2 min for clone/fetch/create repo) and feeds the result back as an event.
5. `Render` → `EditMessageText` on the chat's message. RC links go out via
   `SendMessage`.

Callback data is limited to 64 bytes: buttons carry short ids (`r:12`, `w:3`,
`i:7`, `pg:+1`, `ok:stop:3`) resolved against the chat's state — never paths or
names from Telegram.

## Security

- Allowlist of Telegram user ids; everyone else is ignored.
- Free text is only ever a filter value or a new repo/worktree name; names
  must match `^[A-Za-z0-9._-]{1,40}$`. Nothing from Telegram reaches a shell.
- Token only in `~/.config/bridge/bot.env` (mode 0600, `TELEGRAM_BOT_TOKEN=…`,
  `BRIDGE_BOT_ALLOW=<id>[,<id>]`); never logged, never in argv. Missing/too-open
  file → refuse to start with a clear message.
- Destructive or costly actions (stop, clone, new repo) always need a confirm
  button. No delete actions in the bot.
- Launched sessions use `BRIDGE_DEFAULT_AGENT` / `BRIDGE_DEFAULT_AGENT_ARGS`,
  like nav.

## Error handling

- Telegram 429/5xx/network: retry with backoff (cap 60 s). 409: log
  "another process polls this token" and back off — surfaces a revived old bot.
- navdata/launch errors: shown in the message as "⚠ <short text>"; the screen
  stays usable.
- Unknown/outdated button id (restart, old message): "⚠ outdated — /nav".
- "message is not modified": ignored.

## Operations

- systemd user unit `bridge-bot.service` (`Restart=always`,
  `RestartSec=10`, `ExecStart=%h/.local/bin/bridge bot`), installed by
  `just bot-install`; logs via `journalctl --user -u bridge-bot`.
- Setup doc `docs/telegram-bot.md`: create bot, Passbolt, `bot.env`, install.
- Remove `bridge-bot/` (Python) and its references (`setup-claude-channels.sh`,
  README carry-over note) in the same branch.

## Testing

- `internal/navdata`: table-driven tests on fake repos in `t.TempDir()`; the
  existing `internal/nav` tests must stay green unchanged (regression net).
- `internal/launch`: the existing `bridge launch` / preflight tests stay green;
  unit tests move with the code where they are white-box.
- `internal/bot`: pure `Update` tests for every transition (picker paging,
  filter, forge filter, open dashboard, ▶ new / already running / no link /
  exited, ⏹ confirm/cancel, new worktree name validation, issue launch, clone
  confirm, new repo flow, outdated id, non-allowlisted user) + golden tests for
  rendered text/keyboard JSON.
- `internal/telegram`: against `httptest.Server` (long poll offsets, edit,
  callback answer, 429 retry, 409 error).
- E2E: `bridge bot` against a fake Telegram server + fake tmux on PATH.
- Live: the new bot on agent-dev, full flow once.

## Delivery order

1. `internal/navdata` + `internal/launch` extraction (nav and `bridge launch`
   unchanged, all tests green).
2. `internal/telegram`.
3. `internal/bot` (state machine + render).
4. `cmd/bridge/bot.go` runner + config + E2E.
5. Unit, `just bot-install`, docs, remove Python bot.
