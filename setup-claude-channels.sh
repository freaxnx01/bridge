#!/usr/bin/env bash
# setup-claude-channels.sh — interactive setup for bridge Telegram channels.
#
# Writes ~/.cache/bridge/owner.json (Telegram user_id) and
# ~/.cache/bridge/slot-tokens.json (per-slot Passbolt resource IDs).
# Idempotent: re-run anytime to add slots, rotate tokens, or change owner.
#
# slots.json is NOT touched here — bridge self-inits it on first use.

set -eu

CACHE="${BRIDGE_CACHE:-$HOME/.cache/bridge}"
OWNER="$CACHE/owner.json"
TOKENS="$CACHE/slot-tokens.json"
MAX="${BRIDGE_MAX_SLOTS:-6}"

mkdir -p "$CACHE"

command -v passbolt >/dev/null 2>&1 || { echo "ERR: passbolt CLI not on PATH" >&2; exit 1; }
command -v python3  >/dev/null 2>&1 || { echo "ERR: python3 not on PATH" >&2; exit 1; }

prompt_default() {
  local prompt="$1" default="${2:-}" ans
  if [ -n "$default" ]; then
    read -r -p "$prompt [$default]: " ans
    printf '%s' "${ans:-$default}"
  else
    read -r -p "$prompt: " ans
    printf '%s' "$ans"
  fi
}

validate_passbolt() {
  passbolt get resource --id "$1" 2>/dev/null \
    | awk -F': ' '/^Password:/ {found=1} END{exit !found}'
}

json_read() {
  python3 -c '
import json, sys, os
f = sys.argv[1]
print(json.dumps(json.load(open(f))) if os.path.exists(f) else "{}")
' "$1"
}

json_get() {
  python3 -c '
import json, sys
print(json.load(sys.stdin).get(sys.argv[1], ""))
' "$1"
}

json_set() {
  python3 -c '
import json, sys
d = json.load(sys.stdin)
d[sys.argv[1]] = sys.argv[2]
print(json.dumps(d))
' "$1" "$2"
}

write_atomic() {
  local f="$1"
  python3 -c '
import json, sys
json.dump(json.load(sys.stdin), open(sys.argv[1] + ".tmp", "w"), indent=2, sort_keys=True)
' "$f"
  mv "$f.tmp" "$f"
}

echo "bridge: setup-claude-channels.sh"
echo "  cache: $CACHE"
echo "  slots: 1..$MAX"
echo

# --- 1. Telegram owner ---
owner_json=$(json_read "$OWNER")
cur_owner=$(printf '%s' "$owner_json" | json_get telegram_user_id)
echo "Telegram owner — your numeric Telegram user_id (find via @userinfobot)."
new_owner=$(prompt_default "  user_id" "$cur_owner")
if [ -z "$new_owner" ]; then
  echo "  (skipped — no owner configured)"
else
  printf '%s' "$owner_json" | json_set telegram_user_id "$new_owner" | write_atomic "$OWNER"
  echo "  ✓ owner.json: $new_owner"
fi
echo

# --- 2. Per-slot bot tokens (Passbolt resource IDs) ---
echo "Per-slot bot tokens — paste the Passbolt resource ID for each bot."
echo "  Slot 0 = admin bot (BotFather-named). Empty = skip; existing values shown as default."
tokens_json=$(json_read "$TOKENS")
result_json="$tokens_json"

for n in $(seq 0 "$MAX"); do
  cur=$(printf '%s' "$tokens_json" | json_get "$n")
  if [ "$n" = 0 ]; then
    echo "  slot 0 (admin bot — empty disables admin-bot title management)"
  else
    echo "  slot $n (@claude_freax_s${n}_bot)"
  fi
  pb_id=$(prompt_default "    Passbolt id" "$cur")
  if [ -z "$pb_id" ]; then
    echo "    (skipped)"
    continue
  fi
  if [ "$pb_id" = "$cur" ]; then
    echo "    (kept existing)"
    continue
  fi
  if validate_passbolt "$pb_id"; then
    result_json=$(printf '%s' "$result_json" | json_set "$n" "$pb_id")
    echo "    ✓ token resolved"
  else
    echo "    ✗ Passbolt id did not resolve to a token — keeping previous (if any)" >&2
  fi
done

printf '%s' "$result_json" | write_atomic "$TOKENS"

slot_list=$(python3 -c '
import json, sys
d = json.load(open(sys.argv[1]))
print(" ".join(sorted(d, key=lambda x: int(x) if x.isdigit() else 10**9)))
' "$TOKENS")

echo
echo "✓ slot-tokens.json: ${slot_list:-(empty)}"

# --- 3. bridge-bot (standalone Telegram wrapper for spawning sessions) ---
echo
echo "bridge-bot — standalone Telegram bot for spawning new sessions."
echo "  Needs its own BotFather bot + Passbolt resource for the token."
echo "  Press Enter to skip if you don't want to set this up now."

BRIDGE_BOT_CFG="$CACHE/bridge-bot.json"
bot_json=$(json_read "$BRIDGE_BOT_CFG")
cur_bot_pb=$(printf '%s' "$bot_json" | json_get passbolt_resource_id)
cur_bot_owner=$(printf '%s' "$bot_json" | json_get telegram_owner_id)
[ -z "$cur_bot_owner" ] && cur_bot_owner="${new_owner:-$cur_owner}"

new_bot_pb=$(prompt_default "  Passbolt resource id for bot token" "$cur_bot_pb")
if [ -z "$new_bot_pb" ]; then
  echo "  (skipped — bridge-bot not configured)"
else
  if [ "$new_bot_pb" = "$cur_bot_pb" ] || validate_passbolt "$new_bot_pb"; then
    new_bot_owner=$(prompt_default "  Telegram owner user_id (allowlist)" "$cur_bot_owner")
    python3 - "$BRIDGE_BOT_CFG" "$new_bot_pb" "$new_bot_owner" <<'PY'
import json, os, sys
path, pb, owner = sys.argv[1], sys.argv[2], int(sys.argv[3])
d = json.load(open(path)) if os.path.exists(path) else {}
d["passbolt_resource_id"] = pb
d["telegram_owner_id"] = owner
d.setdefault("allowlist", [])
if owner not in d["allowlist"]:
    d["allowlist"].append(owner)
d.setdefault("last_update_id", 0)
json.dump(d, open(path + ".tmp", "w"), indent=2, sort_keys=True)
os.replace(path + ".tmp", path)
PY
    echo "  ✓ bridge-bot.json written"

    UNIT_SRC="$(dirname "$0")/bridge-bot/systemd/bridge-bot.service"
    UNIT_DST="$HOME/.config/systemd/user/bridge-bot.service"
    if [ -f "$UNIT_SRC" ]; then
      read -r -p "  Install + enable systemd --user unit now? [Y/n]: " ans
      case "${ans:-y}" in
        [Yy]*)
          mkdir -p "$(dirname "$UNIT_DST")"
          cp "$UNIT_SRC" "$UNIT_DST"
          systemctl --user daemon-reload
          systemctl --user enable --now bridge-bot.service
          echo "  ✓ bridge-bot.service enabled"
          ;;
        *) echo "  (skipped systemd install — run later: systemctl --user enable --now bridge-bot.service)" ;;
      esac
    fi
  else
    echo "  ✗ Passbolt id did not resolve — bridge-bot config unchanged" >&2
  fi
fi

echo "Done. Run 'bridge --status' to confirm."
