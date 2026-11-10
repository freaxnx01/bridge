#!/usr/bin/env bash
# systemd entry point for the bridge admin session: start it, then watch it.
# Exits (so systemd restarts us) when the tmux session or claude is gone, or
# when the telegram plugin's bun server has been missing for too long.
set -euo pipefail

SESSION="${BRIDGE_ADMIN_SESSION:-bridge-admin}"
PLUGIN_GRACE="${BRIDGE_ADMIN_PLUGIN_GRACE:-180}"   # seconds without bun server.ts
INTERVAL="${BRIDGE_ADMIN_CHECK_INTERVAL:-30}"
HERE="$(cd "$(dirname "$(readlink -f "$0")")" && pwd)"

# is_descendant PID ANCESTOR — walk /proc ppid links up to init.
is_descendant() {
  local pid=$1 ancestor=$2
  while [ "$pid" -gt 1 ]; do
    [ "$pid" = "$ancestor" ] && return 0
    pid=$(awk '{print $4}' "/proc/$pid/stat" 2>/dev/null) || return 1
  done
  return 1
}

# plugin_running PANE_PID — a telegram `bun server.ts` below this session's
# claude. Other claude sessions on the host run their own copy, so match by
# ancestry, not by name alone.
plugin_running() {
  local pane=$1 pid
  for pid in $(pgrep -f 'bun server\.ts' || true); do
    is_descendant "$pid" "$pane" && return 0
  done
  return 1
}

"$HERE/start-admin-session.sh"

missing_since=""
while true; do
  sleep "$INTERVAL"
  if ! tmux has-session -t "$SESSION" 2>/dev/null; then
    echo "watchdog: tmux session $SESSION gone" >&2
    exit 1
  fi
  pane=$(tmux list-panes -t "$SESSION" -F '#{pane_pid}' | head -1)
  if ! kill -0 "$pane" 2>/dev/null; then
    echo "watchdog: claude (pid $pane) gone" >&2
    tmux kill-session -t "$SESSION" 2>/dev/null || true
    exit 1
  fi
  if plugin_running "$pane"; then
    missing_since=""
    continue
  fi
  now=$(date +%s)
  missing_since=${missing_since:-$now}
  if [ $((now - missing_since)) -ge "$PLUGIN_GRACE" ]; then
    echo "watchdog: telegram plugin down for ${PLUGIN_GRACE}s — restarting $SESSION" >&2
    tmux kill-session -t "$SESSION" 2>/dev/null || true
    exit 1
  fi
done
