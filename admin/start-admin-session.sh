#!/usr/bin/env bash
# Start (or report) the Telegram-reachable bridge admin session in tmux.
# Workspace: ~/.local/share/bridge-admin (symlinks into this repo's admin/).
set -euo pipefail

SESSION="${BRIDGE_ADMIN_SESSION:-bridge-admin}"
CONFIG_DIR="${BRIDGE_ADMIN_CONFIG_DIR:-$HOME/.claude-s0}"
WORKSPACE="${BRIDGE_ADMIN_WORKSPACE:-$HOME/.local/share/bridge-admin}"
HERE="$(cd "$(dirname "$(readlink -f "$0")")" && pwd)"

if tmux has-session -t "$SESSION" 2>/dev/null; then
  echo "already running — attach: tmux attach -t $SESSION"
  exit 0
fi
if [ ! -s "$CONFIG_DIR/channels/telegram/.env" ]; then
  echo "missing $CONFIG_DIR/channels/telegram/.env (TELEGRAM_BOT_TOKEN=…) — see docs/admin-session.md" >&2
  exit 1
fi

mkdir -p "$WORKSPACE/.claude/skills"
ln -sfn "$HERE/CLAUDE.md"                 "$WORKSPACE/CLAUDE.md"
ln -sfn "$HERE/settings.json"             "$WORKSPACE/.claude/settings.json"
ln -sfn "$HERE/skills/bridge-operator"    "$WORKSPACE/.claude/skills/bridge-operator"

# The telegram plugin's MCP server runs on bun; the tmux server's PATH
# usually lacks ~/.bun/bin, so pass it explicitly.
if [ ! -x "$HOME/.bun/bin/bun" ] && ! command -v bun >/dev/null; then
  echo "bun not found — the telegram plugin needs it: curl -fsSL https://bun.sh/install | bash" >&2
  exit 1
fi

tmux new-session -d -s "$SESSION" -c "$WORKSPACE" \
  env CLAUDE_CONFIG_DIR="$CONFIG_DIR" PATH="$HOME/.bun/bin:$PATH" \
  claude -n bridge-admin --permission-mode default --channels plugin:telegram@claude-plugins-official
echo "started $SESSION — attach: tmux attach -t $SESSION"
