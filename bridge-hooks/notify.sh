#!/usr/bin/env bash
# Notification hook for bridge presence-aware Telegram pages.
#
# Acts only on idle_prompt (debounced 120s) and elicitation_dialog (immediate).
# All other notification types are ignored.
#
# Args: $1 = slot number (passed via the hook command in settings.json)
# Stdin: Claude Code hook payload (JSON) with at least .notification_type or .type.
#
# NOTE: bridge does not (yet) provide _bridge_should_page / _bridge_telegram_page
# helpers — the script sources $BRIDGE_SH if present, else silently exits.

set -u

SLOT="${1:-}"
CACHE="$HOME/.cache/bridge"
LOG="$CACHE/hooks.log"
DEBOUNCE_SEC=120

mkdir -p "$CACHE/sessions"
log() { printf '[%s] notify(s%s): %s\n' "$(date -Iseconds)" "$SLOT" "$*" >>"$LOG" 2>/dev/null; }

# Optional bridge shell helpers — silently no-op if not available.
HOOK_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BRIDGE_SH="${BRIDGE_SH:-$(dirname "$HOOK_DIR")/bridge.sh}"
if [ -f "$BRIDGE_SH" ]; then
  # shellcheck disable=SC1090
  . "$BRIDGE_SH" 2>/dev/null || { log "failed to source $BRIDGE_SH"; exit 0; }
else
  log "no bridge helpers at $BRIDGE_SH; exiting"
  cat >/dev/null 2>&1
  exit 0
fi

[ -z "$SLOT" ] && { log "missing slot arg"; exit 0; }

PAYLOAD=$(cat 2>/dev/null || true)
log "payload: $PAYLOAD"

NTYPE=$(echo "$PAYLOAD" | python3 -c "
import json, sys
try:
    d = json.load(sys.stdin)
    print(d.get('notification_type') or d.get('type') or '')
except Exception:
    pass
" 2>/dev/null)

log "notification_type=$NTYPE"

build_page_text() {
  local slot="$1" header="$2"
  python3 -c "
import json, subprocess, re, sys
slot = '$slot'
header = '''$header'''
slots_file = '${_BRIDGE_SLOTS_FILE:-}'
try:
    with open(slots_file) as f: d = json.load(f)
    v = d.get('slots', {}).get(slot) or {}
except Exception:
    v = {}

repo  = v.get('repo')   or '?'
wt    = v.get('worktree') or ''
sess  = v.get('session') or ''

snippet = ''
if sess:
    try:
        out = subprocess.run(['tmux','capture-pane','-p','-t',sess],
                             stdout=subprocess.PIPE, stderr=subprocess.DEVNULL,
                             timeout=2).stdout.decode('utf-8','replace')
        out = re.sub(r'\x1b\[[0-9;]*[mGKH]', '', out)
        lines = [l.rstrip() for l in out.splitlines() if l.strip()]
        snippet = '\n'.join(lines[-12:])[-500:]
    except Exception:
        pass

bracket = f's{slot}/{repo}'
if wt: bracket += f' worktree:{wt}'
text = f'{header} [{bracket}]'
if snippet:
    text += '\n\nLast:\n> ' + snippet.replace('\n', '\n> ')
print(text)
" 2>/dev/null
}

case "$NTYPE" in
  idle_prompt)
    touch "$CACHE/sessions/${SLOT}.idle-since"
    log "scheduled debounce check in ${DEBOUNCE_SEC}s"
    (
      sleep "$DEBOUNCE_SEC"
      [ -f "$CACHE/sessions/${SLOT}.idle-since" ] || exit 0
      if command -v _bridge_should_page >/dev/null 2>&1; then
        _bridge_should_page "$SLOT" || { log "gate says present at delayed check, skip"; exit 0; }
      fi
      TEXT=$(build_page_text "$SLOT" "🤔 Claude is waiting for input")
      if command -v _bridge_telegram_page >/dev/null 2>&1; then
        _bridge_telegram_page "$SLOT" "$TEXT"
        log "sent idle_prompt page"
      else
        log "no _bridge_telegram_page helper; skip"
      fi
    ) &disown
    ;;
  elicitation_dialog)
    if command -v _bridge_should_page >/dev/null 2>&1 && _bridge_should_page "$SLOT"; then
      TEXT=$(build_page_text "$SLOT" "🤔 Claude needs input (elicitation)")
      if command -v _bridge_telegram_page >/dev/null 2>&1; then
        _bridge_telegram_page "$SLOT" "$TEXT"
        log "sent elicitation_dialog page"
      fi
    else
      log "gate says present or unavailable, skip elicitation_dialog"
    fi
    ;;
  *)
    log "ignoring type=$NTYPE"
    ;;
esac

if [ -f "$LOG" ] && [ "$(stat -c %s "$LOG" 2>/dev/null || echo 0)" -gt 1048576 ]; then
  mv "$LOG" "${LOG}.1" 2>/dev/null
fi

exit 0
