# Telegram admin session

A Claude Code session on agent-dev that you steer from Telegram
(`@agent_dev_ctl_bot`): see what's running, start sessions like `bridge nav`
does, get a Remote Control link to jump in from the phone.

## One-time setup

1. Token → `~/.claude-s0/channels/telegram/.env` (mode 0600) from Passbolt
   resource "Telegram Bot \"Agent Dev - Control\", agent_dev_ctl_bot":
   `printf 'TELEGRAM_BOT_TOKEN=%s\n' "$(passbolt get resource --id bb503b24-f92a-455c-8579-0fb975161940 --json | jq -r .password)" > ~/.claude-s0/channels/telegram/.env && chmod 600 ~/.claude-s0/channels/telegram/.env`
2. `~/.claude-s0/channels/telegram/access.json` allowlists your Telegram user ID
   (`dmPolicy: allowlist`).
3. Make sure no other process polls the same bot (one poller per token).

## First run

1. `tmux attach -t bridge-admin` and accept the workspace-trust / channel prompts.
2. The telegram plugin must be installed for that config dir; if missing:
   `CLAUDE_CONFIG_DIR=~/.claude-s0 claude plugin install telegram@claude-plugins-official`
3. `access.json` must allowlist your Telegram user ID (`dmPolicy: allowlist`).

## Run

    admin/start-admin-session.sh      # idempotent
    tmux attach -t bridge-admin       # look / approve prompts; Ctrl-B D to leave

Run it from the main checkout
(`~/repos/github/freaxnx01/public/bridge/admin/start-admin-session.sh`), not
from a worktree: the workspace symlinks point at the script's location and
would dangle once the worktree is removed.

## Boot start + watchdog (systemd)

    just admin-install                # from the main checkout

installs `admin/bridge-admin.service` as a user unit (linger is on, so it
starts at boot without a login). It runs `admin/run-admin-service.sh`, which
starts the session and then checks every 30 s:

- tmux session `bridge-admin` or its `claude` process gone → exit, systemd
  restarts it after 30 s (`Restart=always`);
- the telegram plugin (`bun server.ts` below this claude) missing for 180 s →
  kill the session, systemd restarts it.

An expired claude.ai login is not detected — attach and `/login`.
Logs: `journalctl --user -u bridge-admin`. Tunables: `BRIDGE_ADMIN_PLUGIN_GRACE`,
`BRIDGE_ADMIN_CHECK_INTERVAL` (seconds).

Retired: the older `agent-dev-admin.service` used the same bot token; keep it
disabled (`systemctl --user disable --now agent-dev-admin.service`) or the two
sessions steal each other's Telegram updates.

## Use (Telegram)

- "what's running?" → `bridge status`
- "start bridge worktree fix-x" → `env -u CLAUDE_CONFIG_DIR bridge launch bridge -w fix-x --rc --json` → link
- "stop bridge-wt-fix-x" → confirms in chat; the kill itself needs a one-time approval in the
  admin's tmux pane (`tmux attach -t bridge-admin`)

`already_running`: the reported `agent` is the one requested, not necessarily
the one the live session was started with.

Permissions are scoped in `admin/settings.json`; anything outside the allow
list prompts in the tmux pane (attach to approve).

## Security model

- The admin runs in **auto mode** (`defaultMode: auto` in `admin/settings.json`
  and `--permission-mode auto`); bypass mode is disabled.
- `admin/settings.json` allows **all Bash commands** (`"Bash"`), so anything
  you ask via Telegram runs without an approval. The deny list
  (`rm`, `bridge rm`, `git push`, `git worktree remove`, `tmux capture-pane`)
  still wins over the allow, but it is prefix-match only and easy to sidestep.
  The real boundary is the Telegram allowlist in `access.json`: only your
  user ID can reach the bot.
- No pane reading: `tmux capture-pane` is denied.
- Launched sessions use `BRIDGE_DEFAULT_AGENT` / `BRIDGE_DEFAULT_AGENT_ARGS`,
  the same as `bridge nav`, so they may run in auto mode. They sit idle until
  you steer them via the RC link. To launch phone-started sessions in default
  mode instead, change the skill and the allow rule to
  `bridge launch --agent claude …` (an explicit `--agent` skips the default args).
- Only allowlisted Telegram users can reach the bot.
