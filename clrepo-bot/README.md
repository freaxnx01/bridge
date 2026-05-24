# clrepo-bot

Standalone Telegram bot that wraps `clrepo` on the host. Spawns new Claude
sessions on tap. Independent of bot0/admin and the per-slot Telegram bots.

Spec: `../docs/specs/2026-05-24-clrepo-telegram-bot-design.md`

## Install

1. Create a Telegram bot via @BotFather; store the token in Passbolt.
2. Run `../setup-claude-channels.sh` and answer the "clrepo-bot" section.
3. The setup script offers to install + enable the systemd user unit.

## Run manually (debug)

    ./clrepo_bot.py

## Tests

    python3 -m unittest discover tests -v
