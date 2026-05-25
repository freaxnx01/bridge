#!/usr/bin/env bash
# bridge autosync — best-effort commit & push on session close.
# Default-on for feature branches; opt out with `export BRIDGE_AUTOSYNC=0`.
# Never fails the caller; every error path returns 0.
#
# Two modes:
#   sourced:   defines _bridge_autosync <repo_path> [token]
#   executed:  $1 = tmux session name, $2 = optional bot token.
#              Reads repo path from $_BRIDGE_CACHE/sessions/<session>.path
#
# NOTE: `set -uo pipefail` lives inside the script-mode block at the bottom,
# not at file scope. Sourcing this file (mode 1) must NOT enable strict mode
# in the user's interactive shell — that would crash any bridge code path
# that touches an unset variable.

_BRIDGE_CACHE="${_BRIDGE_CACHE:-$HOME/.cache/bridge}"
_BRIDGE_OWNER="$_BRIDGE_CACHE/owner.json"

_autosync_warn() {
  printf '\033[33mbridge: autosync %s\033[0m\n' "$*" >&2
}

_autosync_ok() {
  printf '\033[32mbridge: autosync %s\033[0m\n' "$*" >&2
}

_autosync_telegram() {
  local token="$1" text="$2"
  [ -z "$token" ] && return 0
  [ -f "$_BRIDGE_OWNER" ] || return 0
  local owner_id
  owner_id=$(python3 -c "
import json
try:
    with open('$_BRIDGE_OWNER') as f: d = json.load(f)
    print(d.get('telegram_user_id', ''))
except Exception:
    pass
" 2>/dev/null)
  [ -z "$owner_id" ] && return 0
  curl -sf -X POST "https://api.telegram.org/bot${token}/sendMessage" \
    -H "Content-Type: application/json" \
    -d "$(python3 -c "import json,sys; print(json.dumps({'chat_id':'$owner_id','text':'''$text'''}))" 2>/dev/null)" \
    >/dev/null 2>&1 || true
}

_bridge_autosync() {
  local repo_path="$1" token="${2:-}"
  [ -z "$repo_path" ] && return 0
  [ -d "$repo_path" ] || return 0

  cd "$repo_path" 2>/dev/null || return 0

  # Load .envrc to discover BRIDGE_AUTOSYNC opt-in.
  if command -v direnv >/dev/null 2>&1; then
    eval "$(direnv export bash 2>/dev/null)" || true
  fi
  [ "${BRIDGE_AUTOSYNC:-1}" = 1 ] || return 0

  git rev-parse --is-inside-work-tree >/dev/null 2>&1 || return 0

  local repo_name branch upstream
  repo_name=$(basename "$repo_path")
  branch=$(git symbolic-ref --quiet --short HEAD) || {
    _autosync_warn "skip ($repo_name): detached HEAD"; return 0; }
  upstream=$(git rev-parse --abbrev-ref --symbolic-full-name '@{u}' 2>/dev/null) || {
    _autosync_warn "skip ($repo_name): no upstream for $branch"; return 0; }

  case "$branch" in
    main|master)
      [ "${BRIDGE_AUTOSYNC_ALLOW_MAIN:-0}" = 1 ] || {
        _autosync_warn "skip ($repo_name): branch '$branch' is protected"
        return 0; } ;;
  esac

  git add -A 2>/dev/null
  if git diff --cached --quiet 2>/dev/null; then
    return 0  # nothing to commit
  fi

  local count
  count=$(git diff --cached --name-only | wc -l | tr -d ' ')

  if ! git commit --quiet -m "chore(autosync): wip from bridge session ($(date -Iminutes))" 2>/dev/null; then
    _autosync_warn "commit failed in $repo_name on $branch"
    _autosync_telegram "$token" "⚠️ autosync FAILED in ${repo_name} on ${branch}: commit error"
    return 0
  fi

  if ! git push --quiet 2>/dev/null; then
    _autosync_warn "push failed in $repo_name on $branch"
    _autosync_telegram "$token" "⚠️ autosync FAILED in ${repo_name} on ${branch}: push rejected"
    return 0
  fi

  _autosync_ok "pushed ${count} file(s) to ${branch} in ${repo_name}"
  _autosync_telegram "$token" "💾 autosync: pushed ${count} change(s) to ${branch} in ${repo_name}"
}

# Script mode: tmux session-closed hook entry point.
if [ "${BASH_SOURCE[0]}" = "$0" ]; then
  set -uo pipefail
  session="${1:-}"
  token="${2:-}"
  [ -z "$session" ] && exit 0
  path_file="$_BRIDGE_CACHE/sessions/${session}.path"
  [ -f "$path_file" ] || exit 0
  repo_path=$(<"$path_file")
  rm -f "$path_file"
  _bridge_autosync "$repo_path" "$token"
fi
