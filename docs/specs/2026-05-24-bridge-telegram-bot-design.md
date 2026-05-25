# bridge Telegram wrapper bot — design

**Status:** draft
**Date:** 2026-05-24

## Problem

The existing bot0/admin Telegram bot is attached to a Claude session (slot 0) and can advise on repos (`/open`, `/status` admin slash commands) but cannot actually spawn new bridge sessions: launching Claude from inside Claude would block the admin session (see comment in `bridge-admin-commands/open.md`).

We want to **create new bridge sessions on claude-dev from Telegram** — pick a repo (local or remote, similar to `bridge -r`), tap a button, get a tmux-backed Claude session running on the host.

## Solution overview

Add a new **standalone Telegram bot** (`bridge-bot`) that wraps `bridge` on the host. It is independent of any Claude session: a small Python daemon, own BotFather token, own allowlist, runs under `systemd --user`. The existing bot0/admin stays unchanged for non-repo admin chat; the new bot is dedicated to spawning and lightly managing sessions.

```
   Telegram cloud
        │
        ▼
   long-poll getUpdates
        │
        ▼
  bridge-bot (Python, systemd --user)
        │
        │ Bash: tmux new-session -d 'bash -lc "bridge <name>"'
        ▼
   detached tmux session ── runs bridge wrapper ── allocates slot N
                                                  │
                                                  ▼
                                              claude --slot N
                                              (per-slot bot picks it up
                                               via existing setup)
```

Key properties:

- **No Claude in the loop** for command handling — zero token cost per command, instant response.
- **bridge stays the source of truth** for launches. The bot just shells out to `bridge <name>` with forwarded args; no flag duplication.
- **Independent failure domain.** Stopping/breaking the bot does not affect any Claude session or the bot0/admin status flow.

## New bot setup & secrets

**BotFather:** create a new bot, suggested username `@claude_freax_bridge_bot`. Token stored as a new Passbolt resource (e.g. `claude-bridge-bot-token`). Never written to disk.

**Config file:** `~/.cache/bridge/bridge-bot.json` — sibling to the existing `slots.json`, `slot-tokens.json`, `owner.json`:

```json
{
  "passbolt_resource_id": "…",
  "telegram_owner_id": 123456789,
  "allowlist": [123456789],
  "last_update_id": 0
}
```

**Setup script:** `setup-claude-channels.sh` gains a new optional prompt block ("bridge-bot"):

- prompt for Passbolt resource id (validate against Passbolt, same pattern as slot tokens)
- prompt for owner Telegram user_id (default: reuse value from `owner.json` if present)
- write `bridge-bot.json`
- offer to install + `systemctl --user enable --now` the unit

Re-running the script with existing config preserves and updates idempotently.

**Log file:** `~/.cache/bridge/bridge-bot.log` — append-only JSON lines, written by the systemd unit via `StandardOutput=append:`.

## Daemon — process, runtime, lifecycle

**Language:** Python 3, stdlib only (`urllib`, `json`, `subprocess`, `logging`, `signal`, `shlex`, `secrets`). No new runtime, no `node_modules`. Target ~300 LOC.

**Layout** at `~/projects/repos/github/freaxnx01/public/bridge/bridge-bot/`:

```
bridge-bot/
├── bridge_bot.py    # entrypoint: poll loop + dispatcher
├── handlers.py      # one function per command (/new, /status, /kill, /help)
├── picker.py        # pagination state, inline-keyboard rendering
├── tg.py            # Bot API wrapper (sendMessage, editMessageText, answerCallbackQuery)
├── spawn.py         # tmux detached launch + slot-poll confirmation
└── auth.py          # allowlist + rate-limit check
```

**Process model:**

- Single long-poll loop (`getUpdates` with `timeout=30`).
- **In-memory** picker state keyed by `chat_id`: `{query, page, include_remote, message_id, resolved_list}`. Lost on restart; user re-runs `/new`. No persistence.
- `last_update_id` persisted to `bridge-bot.json` after each successful update so restarts do not replay messages.
- Heartbeat line written every 60s to the log so a future `bridge --doctor` extension can detect stale daemons.

**systemd unit** at `~/.config/systemd/user/bridge-bot.service`:

```ini
[Unit]
Description=bridge Telegram wrapper bot
After=network-online.target

[Service]
Type=simple
ExecStart=%h/projects/repos/github/freaxnx01/public/bridge/bridge-bot/bridge_bot.py
Restart=on-failure
RestartSec=5s
StandardOutput=append:%h/.cache/bridge/bridge-bot.log
StandardError=inherit

[Install]
WantedBy=default.target
```

Reload (`systemctl --user reload`) → SIGHUP → re-read `bridge-bot.json` and re-fetch the Passbolt token (rotation).

## Command surface (v1)

| Command | Behavior |
|---|---|
| `/start` or `/help` | One-screen help: list of commands + bridge repo link |
| `/new` | Open picker (local repos, MRU order) |
| `/new <query>` | Open picker filtered by query (basename + cached topics/description) |
| `/new <name>` | If exactly one match → launch immediately; else open picker pre-filtered |
| `/status` | Run `bridge --status`, send output verbatim in `<pre>` block |
| `/kill <slot>` | Kill the slot's tmux session (confirmation via inline button) |
| `/cancel` | Drop the current picker session for this chat |

**Argument forwarding:** anything after the repo name in `/new <name> …` is parsed via `shlex.split` and passed through to `bridge`. Lets the bot inherit bridge features for free (`-w <wt>`, `--rc`, etc.).

## Picker UX

**Trigger:** `/new` (optionally with query suffix).

**Single message, edited in place** via `editMessageText` on every interaction — no chat spam. Inline keyboard, ~10 items per page.

**Source data:**

- **Local list:** `find ~/projects/repos -type d -name .git -printf '%h\n'` → strip prefix → sort by `~/.cache/bridge/mru` (MRU first, rest alpha).
- **Remote list** (when `[🌐 Include remote]` toggled): reuse `~/.cache/bridge/remote.list` directly. If absent or older than 10min, shell out to `bridge --refresh` (cache warm only) and re-read. Shares cache with `bridge -r`.

**Layout per page:**

```
Pick a repo (local, MRU — page 1/3)
Filter: «<query>»                 ← only when query non-empty

[ bridge                              ]
[ dotfiles                            ]
[ homelab                             ]
…

[◀ Prev]              [Next ▶]
[🌐 Include remote]   [🔍 Search]
[✖ Cancel]
```

Each repo row's `callback_data = "pick:<idx>"` indexing into the in-memory resolved list. Remote-only entries get a `🌐 ` prefix and trigger clone-then-launch (handled by bridge natively).

**Search button:** edits message to "Reply with a search query to filter:" and flips chat into "awaiting query" mode. Next plain text DM is consumed as the query, then picker re-renders.

**Tap → launch flow:**

1. User taps a repo row.
2. `answerCallbackQuery` with toast "Launching <repo>…" (instant feedback).
3. Daemon spawns (see §Spawn mechanism).
4. Poll `~/.cache/bridge/slots.json` for up to 3s for the new slot to appear.
5. Edit original message:
   - On success: `✅ Launched: <repo> → slot N (tmux: <session>) — reattach: ssh claude-dev && tmux a -t <session>`
   - On timeout: `⏳ Spawn dispatched. Check /status in a few seconds.`

## Spawn mechanism

The host-side launch primitive. No new `bridge` flag — bot stays a wrapper.

```python
session = f"bridge-spawn-{secrets.token_hex(3)}"
cmd = ["tmux", "new-session", "-d", "-s", session,
       "bash", "-lc", f"bridge {shlex.quote(name)} {extra_args}"]
subprocess.run(cmd, check=True, env=_clean_env())
```

**Why this works:**

- `tmux new-session -d` returns immediately; tmux server adopts the session. Daemon uncoupled from its lifetime.
- `bash -lc` sources `~/.bashrc` → `bridge.sh` → `bridge` function in scope.
- `bridge` runs its normal slot allocation and spawns its **own** repo-named tmux session via `_bridge_launch`. The outer `bridge-spawn-<rand>` wrapper exits when the function returns. Net result: one persistent tmux session named after the repo (reattachable as today); wrapper session is gone.

**Env hygiene** (`_clean_env`): strip `TMUX`, `TMUX_PANE`, `STY`, `CLAUDE_CODE_SSE_PORT`. Keep `HOME`, `PATH`, `USER`, `XDG_RUNTIME_DIR`, `DBUS_SESSION_BUS_ADDRESS` (needed for Passbolt direnv).

**Failure modes:**

| Failure | Detection | Reply |
|---|---|---|
| `tmux` binary missing | `FileNotFoundError` | `❌ tmux not installed on host` |
| `bridge: no such repo: <name>` | bridge exits non-zero inside detached tmux; daemon does not see exit | 3s slot-poll times out → `⏳ Spawn dispatched. Check /status…` (acceptable for v1) |
| Daemon-side exception | try/except around subprocess | `❌ Spawn failed: <truncated traceback>` |

## Auth

Independent allowlist for this bot. Does **not** share state with the telegram MCP plugin or per-slot bots.

- **Policy:** strict allowlist only. No pairing, no DM-policy modes. This bot can spawn processes and kill sessions — it is a privileged surface, not a social one.
- **Bootstrap:** owner adds their own user_id at setup. Adding others is a manual edit + SIGHUP. No UI for it.
- **Enforcement:** every inbound update (message + `callback_query`) checks `from.id ∈ allowlist`. Mismatch → silent drop + single `WARN unauthorized from=<id>` log line. Silent so guessing the username does not confirm the bot exists.
- **Group chats:** rejected entirely. Only `chat.type == "private"`. Group messages silent-dropped even from allowlisted users.
- **Rate limit:** per-user token bucket, 20 commands / minute. Excess → silent drop + log line.

## Observability and ops

**Logging:** `~/.cache/bridge/bridge-bot.log`, JSON lines.

```json
{"ts":"2026-05-24T10:14:22Z","evt":"cmd","user":123,"chat":123,"text":"/new bridge"}
{"ts":"2026-05-24T10:14:23Z","evt":"spawn","repo":"bridge","tmux":"bridge","slot":2,"ok":true}
{"ts":"2026-05-24T10:14:25Z","evt":"unauthorized","user":987}
```

Rotation via `logrotate` user config (or manual truncate — low volume).

**Health check:** `systemctl --user status bridge-bot.service` is authoritative. Heartbeat line every 60s.

**Reload:** `systemctl --user reload bridge-bot.service` → SIGHUP → re-read `bridge-bot.json` + re-fetch Passbolt token. No restart needed for allowlist or token rotation.

**Error surfacing:**

- Token fetch fail on startup → daemon exits, systemd retries with `RestartSec=5s`. Visible via `systemctl --user status`.
- Telegram API outage → `urllib.error.URLError` caught, log, sleep 5s, retry. Silent unless prolonged.
- Spawn failure → see §Spawn mechanism table.

**Title management:** the new bot does **not** participate in the existing `_bridge_admin_status_update` aggregation. Its BotFather description stays static (`bridge wrapper — DM /help`).

## Out of scope for v1

- Worktree picker UI (use `/new <name> -w <wt>` raw)
- Repo creation (Ctrl-N equivalent)
- Repo deletion
- Multi-user / role-based access (allowlist is flat)
- Live bot title updates
- `bridge --doctor` consumer of the heartbeat (heartbeat ships, doctor wiring is a follow-up)
- Web/SSH attach from inside Telegram

## Open questions

None blocking. Possible follow-ups (intentionally deferred):

- Should `/kill <slot>` also `bridge --free <slot>`-style cleanup, or just kill the tmux session and let reconciliation handle the rest?
- Should the bot expose `/refresh` to force a `bridge --refresh` cache rebuild, or always rely on the 10min TTL?
- Should heartbeat go into `slots.json` or a sibling `bot-heartbeat.json` to keep slot state pure?
