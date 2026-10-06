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

## Run

    admin/start-admin-session.sh      # idempotent
    tmux attach -t bridge-admin       # look / approve prompts; Ctrl-B D to leave

## Use (Telegram)

- "what's running?" → `bridge status`
- "start bridge worktree fix-x" → `env -u CLAUDE_CONFIG_DIR bridge launch bridge -w fix-x --rc --json` → link
- "stop bridge-wt-fix-x" → confirms, then kills the tmux session

Permissions are scoped in `admin/settings.json`; anything outside the allow
list prompts in the tmux pane (attach to approve).
