# Telegram nav bot (`bridge bot`)

`bridge bot` puts `bridge nav`'s repo picker and per-repo dashboard on your
phone as Telegram inline-keyboard buttons. Taps are deterministic — no Claude
in the loop, no tokens spent:

- **Repos** (`/nav` or `/start`): MRU repos first (⭐), then A–Z; remote-only
  repos are marked ↓ and cloned on tap (with a confirm). Forge filter
  (`All ▸`), text filter (🔍, then send the text), ➕ new repo, 🔄 refresh.
- **Dashboard**: one line per worktree with session state (● running, ○ none)
  and git status (✓ clean, ✎N modified, ↑ahead ↓behind, ∅ no upstream, … loading).
  `▶` starts or opens a session — the Remote Control link arrives as its own
  message — `⏹` stops one (with a confirm), ➕ creates a worktree and starts a
  session in it, 🐞 lists open issues and starts a session on one
  (`issue` worktree `<n>-<slug>`, session `#<n> [title]`, exactly like nav).

This is a different bot from the Claude admin session (`@agent_dev_ctl_bot`,
[admin-session.md](admin-session.md)): a Bot API token can be long-polled by
one process only, so each needs its own bot.

## 1. Create the bot

1. In Telegram, talk to **@BotFather** → `/newbot`. Give it a name with
   "bridge" in it (e.g. `freax_bridge_bot`). Copy the token.
2. Store the token in Passbolt (entry e.g. `Telegram bridge nav bot`).
3. Find your numeric Telegram user id (e.g. send a message to **@userinfobot**).

## 2. Write `~/.config/bridge/bot.env`

```sh
mkdir -p ~/.config/bridge
install -m 600 /dev/null ~/.config/bridge/bot.env
$EDITOR ~/.config/bridge/bot.env
```

```sh
TELEGRAM_BOT_TOKEN=123456789:AA…
BRIDGE_BOT_ALLOW=111111111          # comma-separated user ids; everyone else is ignored
```

`bridge bot` refuses to start when the file is missing or readable by group or
others (`chmod 600` fixes it). The token is never logged and never passed on a
command line.

Sessions start with `BRIDGE_DEFAULT_AGENT` / `BRIDGE_DEFAULT_AGENT_ARGS`
(claude when unset), like nav. To set them for the service:
`systemctl --user edit bridge-bot` and add e.g.
`Environment=BRIDGE_DEFAULT_AGENT_ARGS=--dangerously-skip-permissions`.

## 3. Install the service

From the main checkout (the unit is symlinked from the repo):

```sh
just build        # installs ~/.local/bin/bridge
just bot-install  # links + enables + starts bridge-bot.service
journalctl --user -u bridge-bot -f
```

Send `/nav` to the bot. A `getUpdates failed … 409` / "another process polls
this token" warning means a second process (an old bot?) uses the same token.

## Security

- Only allowlisted user ids get answers; other members of a group are ignored
  (one log line each).
- Free text is only ever a filter or a new repo/worktree name
  (`^[A-Za-z0-9._-]{1,40}$`); nothing from Telegram reaches a shell.
- Buttons carry short ids (`7:r:12`) resolved against the chat's state, never
  paths. Buttons of an older message answer "⚠ outdated — /nav".
- Stop, clone and new repo always ask for confirmation. There are no delete actions.
