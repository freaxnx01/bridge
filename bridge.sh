# bridge — personal dev cockpit: pick a repo under ~/projects/repos and launch an agent session (claude, copilot, opencode, VS Code).
#
# Layout: $_BRIDGE_BASE/{github/<owner>/(public|private),gitlab/<owner>,git-forgejo}
#
# Discovery: every .envrc under $_BRIDGE_BASE whose path matches the layout
# is a "forge target" (credential source + clone destination). Target
# metadata is inferred from the path; no sidecar config.
#
# Surface:
#   - local picker (fast, offline, MRU on top)
#   - -r adds uncloned remote repos (streamed per forge)
#   - Ctrl-N in picker creates a new remote repo (fzf query = seed name)
#   - Ctrl-D in picker deletes a repo (local and/or remote)
#   - --delete / -D is the non-interactive delete shortcut
#   - -w/--worktree NAME passes through to `claude --worktree NAME`
#
# SSH persistence: when $SSH_CONNECTION is set (i.e. you're reaching agent-dev
# from a remote client), the final launch is wrapped in `tmux new-session -A`
# so disconnecting the client leaves the Claude session alive on the host.
# Reconnect and re-run `bridge <repo>` (or `bridge <repo> -w <wt>`) to reattach.
#
# The slot/telegram wrapper (see external spec) can replace _bridge_launch
# wholesale without touching the rest of this file.

_BRIDGE_VERSION="1.41.11"

# Disable alias expansion while sourcing so an existing `alias bridge='...'`
# (typical in interactive bashrc) doesn't get expanded inline at the
# `bridge() {` definition below and break re-sourcing (`source ~/.bashrc`).
# Saved state is restored at the end of this file.
_bridge_saved_expand_aliases=0
shopt -q expand_aliases && _bridge_saved_expand_aliases=1
shopt -u expand_aliases

# --- Platform + path helpers (Windows / Git-Bash support) ---
# _bridge_is_windows: true (exit 0) when running under Git Bash / MSYS / Cygwin.
# Detection is by $OSTYPE so callers can override in tests.
_bridge_is_windows() {
  case "${OSTYPE:-}" in
    msys*|cygwin*|mingw*) return 0 ;;
    *) return 1 ;;
  esac
}

# _bridge_norm_path <path>
#   POSIX hosts: echo the path unchanged.
#   Windows hosts: convert C:\foo, C:/foo, or /c/foo to /c/foo via
#   `cygpath -u`. Falls back to a pure-Bash conversion if cygpath is absent.
_bridge_norm_path() {
  local p="$1"
  if ! _bridge_is_windows; then
    printf '%s\n' "$p"
    return 0
  fi
  if command -v cygpath >/dev/null 2>&1; then
    cygpath -u "$p"
    return 0
  fi
  p="${p//\\//}"
  if [[ "$p" =~ ^([A-Za-z]):(/.*)?$ ]]; then
    local drive_lc rest
    drive_lc=$(printf '%s' "${BASH_REMATCH[1]}" | tr '[:upper:]' '[:lower:]')
    rest="${BASH_REMATCH[2]:-}"
    printf '/%s%s\n' "$drive_lc" "$rest"
  else
    printf '%s\n' "$p"
  fi
}

# _bridge_display_path <posix-path>
#   POSIX hosts: echo unchanged.
#   Windows hosts: convert to Windows form (C:\foo) via `cygpath -w` for
#   user-facing messages. Falls back to a pure-Bash conversion.
_bridge_display_path() {
  local p="$1"
  if ! _bridge_is_windows; then
    printf '%s\n' "$p"
    return 0
  fi
  if command -v cygpath >/dev/null 2>&1; then
    cygpath -w "$p"
    return 0
  fi
  if [[ "$p" =~ ^/([A-Za-z])(/.*)?$ ]]; then
    local drive_uc rest
    drive_uc=$(printf '%s' "${BASH_REMATCH[1]}" | tr '[:lower:]' '[:upper:]')
    rest="${BASH_REMATCH[2]:-}"
    rest="${rest//\//\\}"
    printf '%s:%s\n' "$drive_uc" "$rest"
  else
    printf '%s\n' "$p"
  fi
}

# Print all entries of _BRIDGE_BASES space-separated, each mapped through
# _bridge_display_path so Windows users see C:\... paths in error
# messages. No-op on POSIX hosts.
_bridge_display_bases() {
  local b first=1
  for b in "${_BRIDGE_BASES[@]}"; do
    if [ "$first" = 1 ]; then first=0; else printf ' '; fi
    printf '%s' "$(_bridge_display_path "$b")"
  done
}

_BRIDGE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
_BRIDGE_CACHE="$(_bridge_norm_path "${BRIDGE_CACHE:-$HOME/.cache/bridge}")"
_BRIDGE_CONFIG="$(_bridge_norm_path "${BRIDGE_CONFIG:-$HOME/.config/bridge}")"

# Base-dir resolution. Precedence (whole-list, sources never merged):
#   1. BRIDGE_BASE env var — `:`-separated list (PATH-style)
#   2. $_BRIDGE_CONFIG/base config file — one absolute path per line
#   3. Default ["$HOME/projects/repos"]
# `~` and `$HOME` are expanded; trailing `/` stripped; duplicates dropped;
# missing dirs warned-and-skipped. `_BRIDGE_BASE` retained as the first
# element for backward compat — existing code reading $_BRIDGE_BASE keeps
# working on single-base setups. See docs/specs/2026-05-19-bridge-multi-base-design.md.
_bridge_read_base_file_all() {
  local f="$_BRIDGE_CONFIG/base" line
  [ -r "$f" ] || return 1
  while IFS= read -r line || [ -n "$line" ]; do
    line="${line#"${line%%[![:space:]]*}"}"
    line="${line%"${line##*[![:space:]]}"}"
    [ -z "$line" ] && continue
    case "$line" in '#'*) continue ;; esac
    line="${line/#\~/$HOME}"
    line="${line//\$HOME/$HOME}"
    printf '%s\n' "$line"
  done < "$f"
  return 0
}
_BRIDGE_BASES=()
_bridge_collect_bases() {
  local -a raw=()
  local p seen=""
  if [ -n "${BRIDGE_BASE:-}" ]; then
    IFS=':' read -r -a raw <<< "$BRIDGE_BASE"
  else
    while IFS= read -r p; do raw+=("$p"); done < <(_bridge_read_base_file_all 2>/dev/null)
    [ "${#raw[@]}" -eq 0 ] && raw=("$HOME/projects/repos")
  fi
  for p in "${raw[@]}"; do
    [ -z "$p" ] && continue
    p="${p%/}"
    p="${p/#\~/$HOME}"; p="${p//\$HOME/$HOME}"
    # Windows / Git-Bash: accept C:\foo, C:/foo, or /c/foo at the entry
    # boundary and store as POSIX. No-op on POSIX hosts.
    p="$(_bridge_norm_path "$p")"
    case ":$seen:" in *":$p:"*) continue ;; esac
    if [ ! -d "$p" ]; then
      printf '\033[33mbridge: base dir missing, skipping: %s\033[0m\n' "$(_bridge_display_path "$p")" >&2
      continue
    fi
    _BRIDGE_BASES+=("$p")
    seen="$seen:$p"
  done
  [ "${#_BRIDGE_BASES[@]}" -eq 0 ] && _BRIDGE_BASES=("$HOME/projects/repos")
  _BRIDGE_BASE="${_BRIDGE_BASES[0]}"
}
_bridge_collect_bases

# _bridge_collect_bases_with <value> — re-resolve the bases as if BRIDGE_BASE
# were set to <value>. Used by --base/-B to give the flag the highest
# precedence (above env var, config file, default) for one invocation.
# Accepts `:`-separated lists just like the env var.
_bridge_collect_bases_with() {
  _BRIDGE_BASES=()
  BRIDGE_BASE="$1" _bridge_collect_bases
}

# _bridge_base_for_rel <rel> — return the first $base under which $base/$rel
# exists. Falls back to _BRIDGE_BASES[0] if no match (clone target).
_bridge_base_for_rel() {
  local rel="$1" b
  for b in "${_BRIDGE_BASES[@]}"; do
    [ -d "$b/$rel" ] && { printf '%s\n' "$b"; return 0; }
  done
  printf '%s\n' "${_BRIDGE_BASES[0]}"
  return 1
}

# Backward-compat: legacy reader, now wired to read just the first line.
_bridge_read_base_file() {
  _bridge_read_base_file_all | head -1
}
_BRIDGE_REMOTE_TTL=600  # seconds
_BRIDGE_UPDATE_TTL=86400  # seconds; staleness for latest-version cache
_BRIDGE_FOCUS_CACHE="$_BRIDGE_CACHE/focus.json"
_BRIDGE_FOCUS_TTL="${BRIDGE_FOCUS_TTL:-3600}"
_BRIDGE_RAW_URL="https://raw.githubusercontent.com/freaxnx01/bridge/main/bridge.sh"

# Autosync function (opt-in commit & push on session close). Same file is
# also exec'd from the tmux session-closed hook in script mode.
[ -f "$_BRIDGE_DIR/bridge-autosync.sh" ] && . "$_BRIDGE_DIR/bridge-autosync.sh"

# Unpushed-commit warning on session exit (always-on, no opt-in required).
[ -f "$_BRIDGE_DIR/bridge-unpushed-warn.sh" ] && . "$_BRIDGE_DIR/bridge-unpushed-warn.sh"

# User config files (all under $_BRIDGE_CONFIG, never committed to the repo):
#   ado-projects  — one ADO project name per line; limits which projects are
#                   listed/cloned. Empty file or absent = no filter (all projects).
#   base          — single absolute path; the base dir bridge scans for repos.
#                   First non-empty, non-`#` line wins. `~` and `$HOME` are
#                   expanded. Lower precedence than the BRIDGE_BASE env var.
#                   Multi-line support arrives with #4.

# --- Slot / Telegram channel config ---
_BRIDGE_MAX_SLOTS="${BRIDGE_MAX_SLOTS:-6}"
_BRIDGE_SLOTS_FILE="$_BRIDGE_CACHE/slots.json"
_BRIDGE_SLOTS_LOCK="$_BRIDGE_CACHE/slots.lock"
_BRIDGE_SLOT_TOKENS="$_BRIDGE_CACHE/slot-tokens.json"
_BRIDGE_OWNER="$_BRIDGE_CACHE/owner.json"

# Presence file at $_BRIDGE_CACHE/presence holds one of: auto | away | here.
# Missing or unrecognized → treated as auto.
_BRIDGE_PRESENCE_FILE="$_BRIDGE_CACHE/presence"

# Yellow-prefixed warning to stderr. Used by _bridge_sync skip paths.
_bridge_warn() {
  printf '\033[33mbridge: %s\033[0m\n' "$*" >&2
}

# Pretty-print a yellow bordered block summarising _BRIDGE_SYNC_NOTE.
# Called right before agent launch when the note is non-empty.
_bridge_sync_banner() {
  [ -z "${_BRIDGE_SYNC_NOTE:-}" ] && return 0
  local reason_line suggested_line
  reason_line=$(printf '%s' "$_BRIDGE_SYNC_NOTE" | sed -n '1p')
  suggested_line=$(printf '%s' "$_BRIDGE_SYNC_NOTE" \
    | awk '/^Suggested:/{flag=1;next} flag&&NF{print; exit}')
  printf '\033[33m\n' >&2
  printf '┌─ bridge: startup sync was skipped ─────────────────────────────\n' >&2
  printf '│ %s\n' "${reason_line#bridge: startup sync was skipped — }" >&2
  [ -n "$suggested_line" ] && printf '│ Suggested:%s\n' "${suggested_line#  -}" >&2
  printf '│ Full note: .bridge/sync-status.md\n' >&2
  printf '└────────────────────────────────────────────────────────────────\n' >&2
  printf '\033[0m\n' >&2
}

# Write _BRIDGE_SYNC_NOTE to .bridge/sync-status.md in the current repo.
# Creates .bridge/.gitignore on first write so artifacts never get committed.
_bridge_sync_write_marker() {
  [ -z "${_BRIDGE_SYNC_NOTE:-}" ] && return 0
  mkdir -p .bridge 2>/dev/null || return 0
  [ -f .bridge/.gitignore ] || printf '*\n' > .bridge/.gitignore
  {
    printf '<!-- written by bridge at %s -->\n\n' "$(date -Iseconds)"
    printf '%s\n' "$_BRIDGE_SYNC_NOTE"
  } > .bridge/sync-status.md 2>/dev/null || true
}

# Emit forge targets: TSV of rel_dir\tforge\towner\tvisibility
_bridge_targets() {
  local base
  for base in "${_BRIDGE_BASES[@]}"; do
    find "$base" -type f -name .envrc -printf '%h\n' 2>/dev/null \
      | sed "s|^$base/||" \
      | while IFS= read -r rel; do
          case "$rel" in
            github/*/public)
              local o="${rel#github/}"; o="${o%/public}"
              printf '%s\tgithub\t%s\tpublic\n' "$rel" "$o" ;;
            github/*/private)
              local o="${rel#github/}"; o="${o%/private}"
              printf '%s\tgithub\t%s\tprivate\n' "$rel" "$o" ;;
            github/*)
              local o="${rel#github/}"
              [ -d "$base/$rel/public" ] && \
                printf '%s/public\tgithub\t%s\tpublic\n' "$rel" "$o"
              [ -d "$base/$rel/private" ] && \
                printf '%s/private\tgithub\t%s\tprivate\n' "$rel" "$o"
              ;;
            gitlab/*)
              printf '%s\tgitlab\t%s\t-\n' "$rel" "${rel#gitlab/}" ;;
            git-forgejo)
              printf '%s\tforgejo\tfreax\t-\n' "$rel" ;;
            ado)
              printf '%s\tado\tbossinfo\t-\n' "$rel" ;;
          esac
        done
  done
}

# Fetch remote repo names for one target (loaded via direnv in a subshell).
# Emits TSV: <rel_path>\t<description>\t<topics_csv>
# - description: tabs/newlines replaced with spaces; empty if null
# - topics_csv:  comma-separated; empty if none
_bridge_fetch_target() {
  local rel="$1" forge="$2" owner="$3" vis="$4"
  (
    cd "$(_bridge_base_for_rel "$rel")/$rel" 2>/dev/null || exit
    command -v direnv >/dev/null && eval "$(direnv export bash 2>/dev/null)"
    case "$forge" in
      github)
        local tok="${GH_TOKEN:-$GITHUB_TOKEN}"
        [ -z "$tok" ] && exit
        curl -sf -H "Authorization: token $tok" \
          -H "Accept: application/vnd.github+json" \
          "https://api.github.com/user/repos?affiliation=owner&visibility=$vis&per_page=100" \
          | jq -r --arg rel "$rel" --arg o "$owner" '
              [ .[] | select(.owner.login == $o) ]
              | sort_by(.name)
              | .[]
              | [ "\($rel)/\(.name)",
                  ((.description // "") | gsub("[\\t\\n\\r]"; " ")),
                  ((.topics // []) | join(",")) ]
              | @tsv
            ' 2>/dev/null
        ;;
      gitlab)
        [ -z "$GITLAB_TOKEN" ] && exit
        curl -sf -H "PRIVATE-TOKEN: $GITLAB_TOKEN" \
          "https://gitlab.freaxnx01.ch/api/v4/projects?owned=true&per_page=100" \
          | jq -r --arg rel "$rel" '
              sort_by(.path)
              | .[]
              | [ "\($rel)/\(.path)",
                  ((.description // "") | gsub("[\\t\\n\\r]"; " ")),
                  ((.topics // []) | join(",")) ]
              | @tsv
            ' 2>/dev/null
        ;;
      forgejo)
        [ -z "$FORGEJO_TOKEN" ] && exit
        curl -sf -H "Authorization: token $FORGEJO_TOKEN" \
          "https://git.home.freaxnx01.ch/api/v1/user/repos?limit=50" \
          | jq -r --arg rel "$rel" '
              sort_by(.name)
              | .[]
              | [ "\($rel)/\(.name)",
                  ((.description // "") | gsub("[\\t\\n\\r]"; " ")),
                  ((.topics // []) | join(",")) ]
              | @tsv
            ' 2>/dev/null
        ;;
      ado)
        local tok="${AZURE_DEVOPS_EXT_PAT:-${ADO_PAT:-}}"
        [ -z "$tok" ] && exit
        local _ado_projects_file="$_BRIDGE_CONFIG/ado-projects"
        local _ado_allowed="null"
        if [ -f "$_ado_projects_file" ]; then
          _ado_allowed=$(grep -v '^#\|^[[:space:]]*$' "$_ado_projects_file" \
            | jq -Rsc 'split("\n") | map(select(length > 0))')
        fi
        curl -sf -u ":$tok" \
          "https://dev.azure.com/$owner/_apis/git/repositories?api-version=7.1" \
          | jq -r --arg rel "$rel" --argjson allowed "$_ado_allowed" '
              [ .value[]
                | select(
                    $allowed == null or ($allowed | length == 0) or
                    (.project.name as $p | $allowed | index($p) != null)
                  )
              ]
              | sort_by([.project.name, .name])
              | .[]
              | ["\($rel)/\(.project.name)/\(.name)", "", ""]
              | @tsv
            ' 2>/dev/null
        ;;
    esac
  )
}

# Union of remote listings across all targets, cached with TTL.
# Streams per-forge output to stdout (for live fzf) while also writing
# to tmp files that become caches on completion:
#   - remote.list      : plain rel paths (back-compat for the picker stream)
#   - repo-meta.json   : { rel: {description, topics[], fetched_at} }
_bridge_remote_list() {
  local force="$1"
  local cache="$_BRIDGE_CACHE/remote.list"
  local meta_cache="$_BRIDGE_CACHE/repo-meta.json"
  local now age
  now=$(date +%s)
  if [ "$force" != 1 ] && [ -f "$cache" ]; then
    age=$(( now - $(stat -c %Y "$cache" 2>/dev/null || echo 0) ))
    if [ "$age" -lt "$_BRIDGE_REMOTE_TTL" ]; then
      cat "$cache"; return
    fi
  fi
  echo "bridge: fetching remote repo listings..." >&2
  local tmp_list tmp_meta
  tmp_list=$(mktemp)
  tmp_meta=$(mktemp)
  echo '{}' > "$tmp_meta"
  _bridge_targets | while IFS=$'\t' read -r rel forge owner vis; do
    _bridge_fetch_target "$rel" "$forge" "$owner" "$vis" \
      | while IFS= read -r line; do
          # Split 3-field TSV manually: bash `read` with IFS=$'\t' collapses
          # consecutive tabs (tab is POSIX whitespace), which drops empty fields.
          [ -z "$line" ] && continue
          rpath=${line%%$'\t'*}
          rest=${line#*$'\t'}
          desc=${rest%%$'\t'*}
          topics_csv=${rest#*$'\t'}
          [ -z "$rpath" ] && continue
          # Stream path-only to stdout and remote.list (back-compat)
          printf '%s\n' "$rpath" | tee -a "$tmp_list"
          # Merge into repo-meta.json
          jq --arg k "$rpath" --arg d "$desc" --arg t "$topics_csv" --argjson ts "$now" '
            . + {
              ($k): {
                description: $d,
                topics: ($t | if . == "" then [] else split(",") end),
                fetched_at: $ts
              }
            }
          ' "$tmp_meta" > "$tmp_meta.new" && mv "$tmp_meta.new" "$tmp_meta"
        done
  done
  mv "$tmp_list" "$cache"
  mv "$tmp_meta" "$meta_cache"
}

# Search cached forge metadata (~/.cache/bridge/repo-meta.json) for a keyword.
# Case-insensitive substring match against each topic and against description.
# Emits TSV: <hit_type>\t<rel_path>\t<snippet>
#   hit_type = "topic" | "desc"
#   snippet  = matched topic name, or a ~50-char window around the desc match
# Topic hits are listed first, then desc hits; each group sorted by basename.
# A repo with both hit types is reported once, as "topic".
_bridge_meta_search() {
  local kw="$1"
  local meta="$_BRIDGE_CACHE/repo-meta.json"
  [ -z "$kw" ] && return 0
  [ -f "$meta" ] || return 0

  jq -r --arg kw "$kw" '
    def ci($s): $s | ascii_downcase;
    def contains_ci($needle; $hay): ci($hay) | contains(ci($needle));
    def snippet($text; $needle):
      (ci($text) | index(ci($needle))) as $i
      | if $i == null then ""
        else
          ([$i - 20, 0] | max) as $s
          | ([$i + ($needle | length) + 20, ($text | length)] | min) as $e
          | ($text[$s:$e])
          | (if $s > 0 then "..." + . else . end)
          | (if $e < ($text | length) then . + "..." else . end)
        end;

    . as $src
    | [ $src | to_entries[]
        | .key as $path
        | (.value.topics // [])
        | map(select(contains_ci($kw; .)))
        | .[]
        | { type: "topic", path: $path, snippet: . }
      ] as $topics
    | ($topics | map(.path)) as $topic_paths
    | [ $src | to_entries[]
        | .key as $path
        | .value as $v
        | select(($topic_paths | any(. == $path)) | not)
        | select(contains_ci($kw; ($v.description // "")))
        | { type: "desc", path: $path,
            snippet: (snippet($v.description; $kw)) }
      ] as $descs
    | ($topics | sort_by(.path | split("/") | last))
      + ($descs | sort_by(.path | split("/") | last))
    | .[]
    | [.type, .path, .snippet] | @tsv
  ' "$meta" 2>/dev/null
}

# Clone-URL for an (existing) remote repo at rel path, inferred from layout.
# GitHub → HTTPS (auth via GH_TOKEN + inline credential helper; no SSH key needed).
# GitLab → HTTPS (auth via GitLab .envrc GIT_CONFIG_* credential helper).
# Forgejo → SSH on port 222 (uses ~/.ssh/id_ed25519_forgejo).
_bridge_clone_url() {
  local rel="$1"
  local name parent
  name=$(basename "$rel")
  parent=$(dirname "$rel")
  case "$parent" in
    github/*/public|github/*/private)
      local o="${parent#github/}"; o="${o%/public}"; o="${o%/private}"
      printf 'https://github.com/%s/%s.git\n' "$o" "$name" ;;
    gitlab/*)
      printf 'https://gitlab.freaxnx01.ch/%s/%s.git\n' "${parent#gitlab/}" "$name" ;;
    git-forgejo)
      printf 'ssh://git@git.home.freaxnx01.ch:222/freax/%s.git\n' "$name" ;;
    ado/*)
      local proj="${parent#ado/}"
      local enc_proj enc_name
      enc_proj=$(printf '%s' "$proj" | jq -sRr '@uri' | tr -d '\n')
      enc_name=$(printf '%s' "$name" | jq -sRr '@uri' | tr -d '\n')
      printf 'https://dev.azure.com/bossinfo/%s/_git/%s\n' "$enc_proj" "$enc_name" ;;
    *) return 1 ;;
  esac
}

# Run `git clone` inside a target dir with direnv loaded, injecting a
# GitHub HTTPS credential helper inline when cloning a github/* target.
_bridge_git_clone_in() {
  local target="$1" url="$2" name="$3"
  (
    mkdir -p "$_BRIDGE_BASE/$target" 2>/dev/null
    cd "$_BRIDGE_BASE/$target" || exit 1
    command -v direnv >/dev/null && eval "$(direnv export bash 2>/dev/null)"
    case "$target" in
      github/*)
        [ -z "${GH_TOKEN:-${GITHUB_TOKEN:-}}" ] && { echo "bridge: no GH_TOKEN under $target" >&2; exit 1; }
        local tok="${GH_TOKEN:-$GITHUB_TOKEN}"
        GH_TOKEN="$tok" git \
          -c "credential.https://github.com.helper=!f() { echo username=x-access-token; echo \"password=\$GH_TOKEN\"; }; f" \
          clone "$url" "$name"
        ;;
      ado|ado/*)
        local tok="${AZURE_DEVOPS_EXT_PAT:-${ADO_PAT:-}}"
        [ -z "$tok" ] && { echo "bridge: no ADO_PAT under $target" >&2; exit 1; }
        git \
          -c "credential.https://dev.azure.com.helper=!f() { echo username=x; echo \"password=$tok\"; }; f" \
          clone "$url" "$name"
        ;;
      *)
        git clone "$url" "$name"
        ;;
    esac
  )
}

# Clone a known-remote rel into its destination.
_bridge_clone_remote() {
  local rel="$1"
  local url parent name
  url=$(_bridge_clone_url "$rel") || { echo "bridge: unknown forge for $rel"; return 1; }
  parent=$(dirname "$rel")
  name=$(basename "$rel")
  echo "bridge: cloning $url" >&2
  _bridge_git_clone_in "$parent" "$url" "$name" || return 1
  rm -f "$_BRIDGE_CACHE/remote.list" "$_BRIDGE_CACHE/repo-meta.json"
}

# Create a new remote repo on a chosen forge target, then clone + launch.
_bridge_create_new() {
  local seed="$1"
  local targets target
  targets=$(_bridge_targets | cut -f1)
  [ -z "$targets" ] && { echo "bridge: no forge targets discovered"; return 1; }
  target=$(printf '%s\n' "$targets" | fzf --height=40% --reverse --prompt='forge target> ') || return
  local name
  read -r -e -i "$seed" -p "repo name: " name
  [ -z "$name" ] && { echo "aborted"; return 1; }

  local line forge vis
  line=$(_bridge_targets | awk -F'\t' -v t="$target" '$1==t {print; exit}')
  forge=$(printf '%s' "$line" | cut -f2)
  vis=$(printf '%s' "$line" | cut -f4)

  local ado_proj=""
  if [ "$forge" = "ado" ]; then
    read -r -p "ADO project: " ado_proj
    [ -z "$ado_proj" ] && { echo "aborted"; return 1; }
  fi

  echo "bridge: creating $name on $target${ado_proj:+ / $ado_proj}..." >&2
  local url
  url=$(
    cd "$_BRIDGE_BASE/$target" || exit
    command -v direnv >/dev/null && eval "$(direnv export bash 2>/dev/null)"
    case "$forge" in
      github)
        local tok="${GH_TOKEN:-$GITHUB_TOKEN}"
        [ -z "$tok" ] && { echo "ERR: no GH_TOKEN under $target" >&2; exit 1; }
        local priv=false; [ "$vis" = "private" ] && priv=true
        curl -sf -X POST \
          -H "Authorization: token $tok" \
          -H "Accept: application/vnd.github+json" \
          -d "$(jq -cn --arg n "$name" --argjson p "$priv" '{name:$n,private:$p,auto_init:false}')" \
          "https://api.github.com/user/repos" \
          | jq -r '.clone_url // empty' ;;
      gitlab)
        [ -z "$GITLAB_TOKEN" ] && { echo "ERR: no GITLAB_TOKEN under $target" >&2; exit 1; }
        curl -sf -X POST \
          -H "PRIVATE-TOKEN: $GITLAB_TOKEN" \
          -H "Content-Type: application/json" \
          -d "$(jq -cn --arg n "$name" '{name:$n,path:$n,visibility:"private",initialize_with_readme:false}')" \
          "https://gitlab.freaxnx01.ch/api/v4/projects" \
          | jq -r '.http_url_to_repo // empty' ;;
      forgejo)
        [ -z "$FORGEJO_TOKEN" ] && { echo "ERR: no FORGEJO_TOKEN under $target" >&2; exit 1; }
        curl -sf -X POST \
          -H "Authorization: token $FORGEJO_TOKEN" \
          -H "Content-Type: application/json" \
          -d "$(jq -cn --arg n "$name" '{name:$n,private:true,auto_init:false}')" \
          "https://git.home.freaxnx01.ch/api/v1/user/repos" \
          | jq -r '.ssh_url // empty' ;;
      ado)
        local tok="${AZURE_DEVOPS_EXT_PAT:-${ADO_PAT:-}}"
        [ -z "$tok" ] && { echo "ERR: no ADO_PAT under $target" >&2; exit 1; }
        local enc_proj
        enc_proj=$(printf '%s' "$ado_proj" | jq -sRr '@uri' | tr -d '\n')
        local proj_id
        proj_id=$(curl -sf -u ":$tok" \
          "https://dev.azure.com/bossinfo/_apis/projects/$enc_proj?api-version=7.1" \
          | jq -r '.id // empty')
        [ -z "$proj_id" ] && { echo "ERR: project $ado_proj not found" >&2; exit 1; }
        curl -sf -X POST -u ":$tok" \
          -H "Content-Type: application/json" \
          -d "$(jq -cn --arg n "$name" --arg pid "$proj_id" '{name:$n,project:{id:$pid}}')" \
          "https://dev.azure.com/bossinfo/_apis/git/repositories?api-version=7.1" \
          | jq -r '.remoteUrl // empty' ;;
    esac
  )
  [ -z "$url" ] && { echo "bridge: remote creation failed"; return 1; }

  if [ "$forge" = "ado" ]; then
    target="$target/$ado_proj"
  fi

  echo "bridge: cloning $url" >&2
  _bridge_git_clone_in "$target" "$url" "$name" || return 1
  rm -f "$_BRIDGE_CACHE/remote.list" "$_BRIDGE_CACHE/repo-meta.json"
  _bridge_launch "$target/$name"
}

# Delete a repo (local clone and/or remote). `raw` may include the ↓ prefix.
# Safety: requires typing the repo name to confirm remote deletion.
# Refuses local delete if the clone is dirty (uncommitted or unpushed work),
# unless the user types the name a second time to override.
_bridge_delete() {
  local raw="$1"
  local rel="${raw#↓ }"
  [ -z "$rel" ] && return 1

  local name parent local_path owning_base has_local=0 dirty=0
  name=$(basename "$rel")
  parent=$(dirname "$rel")
  # Resolve which base owns this rel so multi-base setups delete from the
  # right tree (and read the right per-dir credentials .envrc).
  owning_base=$(_bridge_base_for_rel "$rel")
  local_path="$owning_base/$rel"
  [ -d "$local_path/.git" ] && has_local=1

  # Classify forge + owner from the path.
  local forge owner
  case "$parent" in
    github/*/public|github/*/private)
      forge=github
      owner="${parent#github/}"; owner="${owner%/public}"; owner="${owner%/private}" ;;
    gitlab/*)
      forge=gitlab; owner="${parent#gitlab/}" ;;
    git-forgejo)
      forge=forgejo; owner=freax ;;
    ado/*)
      forge=ado; owner="${parent#ado/}" ;;
    *)
      echo "bridge: unknown forge for $rel" >&2; return 1 ;;
  esac

  # Dirty check (uncommitted or unpushed).
  if [ "$has_local" = 1 ]; then
    if [ -n "$(git -C "$local_path" status --porcelain 2>/dev/null)" ]; then
      dirty=1
    fi
    local upstream unpushed
    upstream=$(git -C "$local_path" rev-parse --abbrev-ref --symbolic-full-name '@{u}' 2>/dev/null)
    if [ -n "$upstream" ]; then
      unpushed=$(git -C "$local_path" rev-list --count "$upstream..HEAD" 2>/dev/null)
      [ "${unpushed:-0}" -gt 0 ] && dirty=1
    fi
  fi

  printf 'bridge: delete target\n' >&2
  printf '  path:   %s\n' "$rel" >&2
  printf '  forge:  %s (%s)\n' "$forge" "$owner" >&2
  printf '  local:  %s%s\n' \
    "$([ "$has_local" = 1 ] && echo yes || echo no)" \
    "$([ "$dirty" = 1 ] && echo ' (DIRTY/unpushed!)' || echo '')" >&2

  local choice
  if [ "$has_local" = 1 ]; then
    read -r -p "Delete [L]ocal / [R]emote / [B]oth / [c]ancel? " choice
  else
    read -r -p "Delete [R]emote / [c]ancel? " choice
    case "$choice" in R|r) ;; *) choice=c ;; esac
  fi

  local del_local=0 del_remote=0
  case "$choice" in
    L|l) del_local=1 ;;
    R|r) del_remote=1 ;;
    B|b) del_local=1; del_remote=1 ;;
    *)   echo "bridge: cancelled" >&2; return 1 ;;
  esac

  local confirm
  if [ "$del_remote" = 1 ]; then
    read -r -p "Type '$name' to confirm REMOTE delete: " confirm
    [ "$confirm" != "$name" ] && { echo "bridge: cancelled" >&2; return 1; }
  fi
  if [ "$del_local" = 1 ] && [ "$dirty" = 1 ]; then
    read -r -p "Local is DIRTY. Type '$name' again to override: " confirm
    [ "$confirm" != "$name" ] && { echo "bridge: cancelled" >&2; return 1; }
  fi

  # Execute remote delete first (if the remote call fails we keep local intact).
  # All three forges use the same direnv-loaded per-dir PAT pattern as clone/create.
  if [ "$del_remote" = 1 ]; then
    echo "bridge: deleting remote $forge:$owner/$name..." >&2
    (
      local creds_dir
      case "$forge" in
        github)  creds_dir="$owning_base/$parent" ;;
        gitlab)  creds_dir="$owning_base/gitlab/$owner" ;;
        forgejo) creds_dir="$owning_base/git-forgejo" ;;
        ado)     creds_dir="$owning_base/ado" ;;
      esac
      cd "$creds_dir" 2>/dev/null || { echo "ERR: creds dir missing: $creds_dir" >&2; exit 1; }
      command -v direnv >/dev/null && eval "$(direnv export bash 2>/dev/null)"
      case "$forge" in
        github)
          local tok="${GH_TOKEN:-$GITHUB_TOKEN}"
          [ -z "$tok" ] && { echo "ERR: no GH_TOKEN under $parent" >&2; exit 1; }
          local http_code
          http_code=$(curl -s -o /dev/null -w '%{http_code}' -X DELETE \
            -H "Authorization: token $tok" \
            -H "Accept: application/vnd.github+json" \
            "https://api.github.com/repos/$owner/$name")
          if [ "$http_code" != "204" ]; then
            echo "ERR: GitHub DELETE returned HTTP $http_code" >&2
            if [ "$http_code" = "403" ]; then
              echo "     The PAT under $parent likely lacks 'delete_repo' scope." >&2
              echo "     Fix: https://github.com/settings/tokens → edit the PAT →" >&2
              echo "     tick 'delete_repo' → Update token. Same string, no Passbolt edit." >&2
            fi
            exit 1
          fi
          ;;
        gitlab)
          [ -z "$GITLAB_TOKEN" ] && { echo "ERR: no GITLAB_TOKEN" >&2; exit 1; }
          local enc; enc=$(printf '%s/%s' "$owner" "$name" | jq -sRr '@uri' | tr -d '\n')
          curl -sf -X DELETE \
            -H "PRIVATE-TOKEN: $GITLAB_TOKEN" \
            "https://gitlab.freaxnx01.ch/api/v4/projects/$enc" \
            || { echo "ERR: GitLab DELETE failed" >&2; exit 1; }
          ;;
        forgejo)
          [ -z "$FORGEJO_TOKEN" ] && { echo "ERR: no FORGEJO_TOKEN" >&2; exit 1; }
          curl -sf -X DELETE \
            -H "Authorization: token $FORGEJO_TOKEN" \
            "https://git.home.freaxnx01.ch/api/v1/repos/$owner/$name" \
            || { echo "ERR: Forgejo DELETE failed" >&2; exit 1; }
          ;;
        ado)
          local tok="${AZURE_DEVOPS_EXT_PAT:-${ADO_PAT:-}}"
          [ -z "$tok" ] && { echo "ERR: no ADO_PAT" >&2; exit 1; }
          local enc_proj enc_name
          enc_proj=$(printf '%s' "$owner" | jq -sRr '@uri' | tr -d '\n')
          enc_name=$(printf '%s' "$name" | jq -sRr '@uri' | tr -d '\n')
          local repo_id
          repo_id=$(curl -sf -u ":$tok" \
            "https://dev.azure.com/bossinfo/$enc_proj/_apis/git/repositories/$enc_name?api-version=7.1" \
            | jq -r '.id // empty')
          [ -z "$repo_id" ] && { echo "ERR: repo lookup failed for $owner/$name" >&2; exit 1; }
          local http_code
          http_code=$(curl -s -o /dev/null -w '%{http_code}' -X DELETE -u ":$tok" \
            "https://dev.azure.com/bossinfo/_apis/git/repositories/$repo_id?api-version=7.1")
          if [ "$http_code" != "204" ]; then
            echo "ERR: ADO DELETE returned HTTP $http_code" >&2
            exit 1
          fi
          ;;
      esac
    ) || return 1
    echo "bridge: remote deleted" >&2
  fi

  if [ "$del_local" = 1 ]; then
    echo "bridge: removing local $local_path..." >&2
    rm -rf -- "$local_path" || return 1
    if [ -f "$_BRIDGE_CACHE/mru" ]; then
      grep -vxF "$rel" "$_BRIDGE_CACHE/mru" > "$_BRIDGE_CACHE/mru.tmp" 2>/dev/null || : > "$_BRIDGE_CACHE/mru.tmp"
      mv "$_BRIDGE_CACHE/mru.tmp" "$_BRIDGE_CACHE/mru"
    fi
  fi

  rm -f "$_BRIDGE_CACHE/remote.list" "$_BRIDGE_CACHE/repo-meta.json"
  return 0
}

# Final launch step. The slot/telegram wrapper can replace this body later.
#
# Args: $1 = rel repo path (e.g. github/freaxnx01/public/myrepo)
#       $2 = optional worktree name (pass-through to claude --worktree)
#
# When $SSH_CONNECTION is set, wraps the launch in a tmux session named
# after the repo (+worktree), using `new-session -A` so reconnecting and
# re-running the same bridge command attaches to the live session.
# --- Slot allocation helpers ---

# Ensure cache dir + slots.json exist. Idempotent; safe to call repeatedly.
# Slot tracking is the default mode — this runs on first launch with no
# user setup required.
_bridge_slots_init() {
  mkdir -p "$_BRIDGE_CACHE" 2>/dev/null
  [ -f "$_BRIDGE_SLOTS_FILE" ] || echo '{"slots":{}}' > "$_BRIDGE_SLOTS_FILE"
}

# Read slots.json, reconcile PIDs, return JSON on stdout.
_bridge_slots_read() {
  local f="$_BRIDGE_SLOTS_FILE"
  _bridge_slots_init
  python3 -c "
import json, os, sys
with open('$f') as fh: d = json.load(fh)
for k, v in d.get('slots', {}).items():
    if v and not _pid_alive(v.get('pid', 0)):
        d['slots'][k] = None
with open('$f', 'w') as fh: json.dump(d, fh, indent=2)
print(json.dumps(d))

def _pid_alive(pid):
    try: os.kill(int(pid), 0); return True
    except: return False
" 2>/dev/null || cat "$f"
}

# Allocate a slot. Sets _SLOT and _SLOT_TOKEN. Optional: $1 = forced slot number.
_bridge_slot_allocate() {
  local forced="${1:-}"
  local slots_json slot_n now pb_id token

  exec {_lock_fd}>"$_BRIDGE_SLOTS_LOCK"
  flock "$_lock_fd"

  # Reconcile dead slots (tmux session is source of truth when recorded;
  # otherwise fall back to PID liveness for foreground-mode records)
  _bridge_slots_init
  python3 -c "
import json, os, subprocess
f = '$_BRIDGE_SLOTS_FILE'
with open(f) as fh: d = json.load(fh)
changed = False
for k, v in list(d.get('slots', {}).items()):
    if not v: continue
    sess = v.get('session') or ''
    if sess:
        alive = subprocess.run(['tmux', 'has-session', '-t', sess],
                               stdout=subprocess.DEVNULL,
                               stderr=subprocess.DEVNULL).returncode == 0
    else:
        try: os.kill(int(v.get('pid', 0)), 0); alive = True
        except (ProcessLookupError, ValueError): alive = False
        except PermissionError: alive = True
    if not alive:
        d['slots'][k] = None
        changed = True
if changed:
    with open(f, 'w') as fh: json.dump(d, fh, indent=2)
" 2>/dev/null

  slots_json=$(cat "$_BRIDGE_SLOTS_FILE")
  now=$(date +%s)

  if [ -n "$forced" ]; then
    # Check if forced slot is free
    local busy
    busy=$(echo "$slots_json" | python3 -c "
import json, sys
d = json.load(sys.stdin)
v = d.get('slots', {}).get('$forced')
if v: print(v.get('repo', '?'))
" 2>/dev/null)
    if [ -n "$busy" ]; then
      echo "bridge: slot $forced is busy with $busy — use a different slot or bridge --free $forced" >&2
      flock -u "$_lock_fd"
      return 1
    fi
    _SLOT="$forced"
  else
    # Find lowest free slot
    _SLOT=""
    for n in $(seq 1 "$_BRIDGE_MAX_SLOTS"); do
      local val
      val=$(echo "$slots_json" | python3 -c "
import json, sys
d = json.load(sys.stdin)
v = d.get('slots', {}).get('$n')
print('busy' if v else 'free')
" 2>/dev/null)
      if [ "$val" = "free" ] || [ -z "$val" ]; then
        _SLOT="$n"
        break
      fi
    done

    # All busy — displace oldest
    if [ -z "$_SLOT" ]; then
      local oldest_slot oldest_time oldest_repo
      read -r oldest_slot oldest_time oldest_repo < <(echo "$slots_json" | python3 -c "
import json, sys
d = json.load(sys.stdin)
slots = d.get('slots', {})
oldest = min(((k, v) for k, v in slots.items() if v), key=lambda x: x[1].get('started_at', 0))
import time
age = int(time.time()) - oldest[1].get('started_at', 0)
h, m = divmod(age // 60, 60)
print(f\"{oldest[0]} {h}h{m:02d}m {oldest[1].get('repo', '?')}\")
" 2>/dev/null)
      echo "⚠ All $_BRIDGE_MAX_SLOTS slots are busy. Displacing slot $oldest_slot ($oldest_repo, running ${oldest_time})." >&2
      echo "  Press Ctrl-C within 5 seconds to abort." >&2
      sleep 5 || { flock -u "$_lock_fd"; return 1; }
      _SLOT="$oldest_slot"
    fi
  fi

  # Load bot token from Passbolt via slot-tokens.json
  _SLOT_TOKEN=""
  if [ -f "$_BRIDGE_SLOT_TOKENS" ]; then
    pb_id=$(python3 -c "
import json
with open('$_BRIDGE_SLOT_TOKENS') as f: d = json.load(f)
print(d.get('$_SLOT', ''))
" 2>/dev/null)
    if [ -n "$pb_id" ]; then
      _SLOT_TOKEN=$(passbolt get resource --id "$pb_id" 2>/dev/null | awk -F": " '/^Password:/ {print $2}')
    fi
  fi

  flock -u "$_lock_fd"

  # Telegram is opt-in: only warn if slot-tokens.json exists but is missing
  # this slot. With no slot-tokens.json at all, surface a one-time hint
  # pointing to the setup script (gated by a sentinel file).
  if [ -z "$_SLOT_TOKEN" ]; then
    if [ -f "$_BRIDGE_SLOT_TOKENS" ]; then
      echo "bridge: no bot token for slot $_SLOT — Telegram channel disabled for this session." >&2
      echo "  Add slot $_SLOT to $_BRIDGE_SLOT_TOKENS to enable." >&2
    elif [ ! -f "$_BRIDGE_CACHE/.channels-hinted" ]; then
      echo "bridge: tip — Telegram pages not configured. Run $_BRIDGE_DIR/setup-claude-channels.sh to enable." >&2
      touch "$_BRIDGE_CACHE/.channels-hinted" 2>/dev/null
    fi
  fi

  # Wire presence-aware Telegram pages: install per-slot hooks. The watcher
  # is started in _bridge_slot_record (after slots.json is updated) to avoid
  # racing with the watcher's "no active slots → self-exit" path.
  _bridge_install_hooks "$_SLOT"
}

# Record slot as busy in slots.json. $5 is the tmux session name (empty
# in foreground mode); when set, reconciliation uses it as the liveness
# signal instead of $pid (which can race or point at a wrapper).
_bridge_slot_record() {
  local slot="$1" repo="$2" worktree="${3:-}" pid="$4" session="${5:-}"
  exec {_lock_fd}>"$_BRIDGE_SLOTS_LOCK"
  flock "$_lock_fd"
  python3 -c "
import json, time
f = '$_BRIDGE_SLOTS_FILE'
with open(f) as fh: d = json.load(fh)
d.setdefault('slots', {})['$slot'] = {
    'repo': '$repo', 'worktree': '$worktree' or None,
    'pid': $pid, 'started_at': int(time.time()),
    'session': '$session' or None,
}
with open(f, 'w') as fh: json.dump(d, fh, indent=2)
" 2>/dev/null
  flock -u "$_lock_fd"

  # Start the usage-limit watcher AFTER slots.json is updated so its first
  # poll sees this new slot (otherwise it self-exits during Telegram setup).
  _bridge_watcher_start

  # Refresh admin bot title to reflect new aggregate state.
  _bridge_admin_status_update
}

# Free a slot in slots.json
_bridge_slot_free() {
  local slot="$1"
  exec {_lock_fd}>"$_BRIDGE_SLOTS_LOCK"
  flock "$_lock_fd"
  python3 -c "
import json
f = '$_BRIDGE_SLOTS_FILE'
with open(f) as fh: d = json.load(fh)
d.setdefault('slots', {})['$slot'] = None
with open(f, 'w') as fh: json.dump(d, fh, indent=2)
" 2>/dev/null
  flock -u "$_lock_fd"

  # Clean up presence-page markers for this slot
  rm -f "$_BRIDGE_CACHE/sessions/${slot}.idle-since" \
        "$_BRIDGE_CACHE/sessions/${slot}.limit-paged" 2>/dev/null

  # Refresh admin bot title to reflect new aggregate state.
  _bridge_admin_status_update
}

# Sanity-check the slot's OAuth credentials before launch so the user sees
# the real cause in bridge's output (rather than a cryptic "Remote Control
# failed to connect: /login" once Claude is up). Best-effort: any parsing
# error is silent. Args: $1 = slot number.
_bridge_slot_creds_check() {
  local slot="$1"
  local f="$HOME/.claude-s${slot}/.credentials.json"
  if [ ! -f "$f" ]; then
    _bridge_warn "slot s${slot} has no credentials, run /login inside Claude after launch"
    return
  fi
  command -v python3 >/dev/null 2>&1 || return 0
  python3 - "$slot" "$f" <<'PY'
import json, sys, time
slot, path = sys.argv[1], sys.argv[2]
try:
    with open(path) as fh: d = json.load(fh)
except Exception:
    print(f"\033[33mbridge: slot s{slot} credentials unreadable, /login may be required\033[0m", file=sys.stderr)
    sys.exit(0)
oa = d.get('claudeAiOauth') or {}
ea = oa.get('expiresAt') or 0
rt = oa.get('refreshToken') or ''
# expiresAt is milliseconds since epoch; treat smaller magnitudes as seconds defensively.
now_ms = int(time.time() * 1000)
ea_ms = ea if ea > 10**12 else ea * 1000
expired = ea_ms > 0 and ea_ms < now_ms
if expired and not rt:
    print(f"\033[33mbridge: slot s{slot} access token expired and no refresh token — Remote Control will fail until you /login\033[0m", file=sys.stderr)
PY
}

# Call Telegram API to set bot name and pin a banner message.
_bridge_telegram_setup() {
  local slot="$1" repo="$2" worktree="${3:-}" token="$4"
  [ -z "$token" ] && return

  local owner_id
  owner_id=$(python3 -c "
import json
with open('$_BRIDGE_OWNER') as f: d = json.load(f)
print(d.get('telegram_user_id', ''))
" 2>/dev/null)
  [ -z "$owner_id" ] && return

  local bot_name="#${slot} Claude - ${repo}"
  [ -n "$worktree" ] && bot_name="#${slot} Claude - ${repo} [${worktree}]"
  # Truncate to 64 chars (Telegram limit)
  bot_name="${bot_name:0:64}"

  local api="https://api.telegram.org/bot${token}"

  # setMyName (best-effort, rate-limited)
  curl -sf -X POST "$api/setMyName" \
    -H "Content-Type: application/json" \
    -d "$(printf '{"name":"%s"}' "$bot_name")" >/dev/null 2>&1 || true

  # sendMessage + pinChatMessage
  local branch
  branch=$(git rev-parse --abbrev-ref HEAD 2>/dev/null || echo "—")
  local msg
  msg=$(printf '📍 Session started\nSlot: s%s\nRepo: %s\nWorktree: %s\nBranch: %s\nPath: %s\nStarted: %s' \
    "$slot" "$repo" "${worktree:-—}" "$branch" "$PWD" "$(date -Iseconds)")

  local send_result msg_id
  send_result=$(curl -sf -X POST "$api/sendMessage" \
    -H "Content-Type: application/json" \
    -d "$(python3 -c "import json; print(json.dumps({'chat_id': '$owner_id', 'text': '''$msg'''}))" 2>/dev/null)" 2>/dev/null) || true

  msg_id=$(echo "$send_result" | python3 -c "import json,sys; print(json.load(sys.stdin).get('result',{}).get('message_id',''))" 2>/dev/null) || true
  if [ -n "$msg_id" ]; then
    curl -sf -X POST "$api/pinChatMessage" \
      -H "Content-Type: application/json" \
      -d "$(printf '{"chat_id":"%s","message_id":%s,"disable_notification":true}' "$owner_id" "$msg_id")" >/dev/null 2>&1 || true
  fi
}

# Refresh admin bot (#0) title to mirror aggregate slot status:
#   - K of N occupied  → "#0 Claude · K/N active"
#   - all free         → "#0 Claude · idle"
# Looks up the admin bot token via slot-tokens.json key "0" (resolved
# through Passbolt). No-op if slot 0 isn't configured. Best-effort —
# any error path returns 0 so the caller is never blocked.
#
# Telegram caps setMyName at ~2/min; this fires from each slot
# allocation/free, which in normal use is well under the cap. If a
# burst occurs (e.g. multiple sessions closing at once) Telegram will
# 429 and the title will catch up on the next event.
_bridge_admin_status_update() {
  local pb_id token
  [ -f "$_BRIDGE_SLOT_TOKENS" ] || return 0
  pb_id=$(python3 -c "
import json
try:
    with open('$_BRIDGE_SLOT_TOKENS') as f: d = json.load(f)
    print(d.get('0', ''))
except Exception:
    pass
" 2>/dev/null)
  [ -z "$pb_id" ] && return 0

  token=$(passbolt get resource --id "$pb_id" 2>/dev/null \
            | awk -F": " '/^Password:/ {print $2}')
  [ -z "$token" ] && return 0

  local title
  title=$(python3 -c "
import json
try:
    with open('$_BRIDGE_SLOTS_FILE') as f: d = json.load(f)
    slots = d.get('slots', {})
    MAX = $_BRIDGE_MAX_SLOTS
    busy = sum(1 for k, v in slots.items()
               if v and k.isdigit() and 1 <= int(k) <= MAX)
    if busy == 0:
        print(f'#0 Claude · idle')
    else:
        print(f'#0 Claude · {busy}/{MAX} active')
except Exception:
    print('#0 Claude · idle')
" 2>/dev/null)
  [ -z "$title" ] && return 0
  # Telegram setMyName accepts up to 64 chars.
  title="${title:0:64}"

  curl -sf -X POST "https://api.telegram.org/bot${token}/setMyName" \
    -H "Content-Type: application/json" \
    -d "$(python3 -c "import json,sys; print(json.dumps({'name': sys.argv[1]}))" "$title")" \
    >/dev/null 2>&1 || true
}

# Best-effort cleanup: reset bot name, send close message.
_bridge_telegram_cleanup() {
  local slot="$1" token="$2"
  [ -z "$token" ] && return

  local owner_id
  owner_id=$(python3 -c "
import json
with open('$_BRIDGE_OWNER') as f: d = json.load(f)
print(d.get('telegram_user_id', ''))
" 2>/dev/null)
  [ -z "$owner_id" ] && return

  local api="https://api.telegram.org/bot${token}"
  curl -sf -X POST "$api/setMyName" \
    -d "$(printf '{"name":"#%s Claude · idle"}' "$slot")" >/dev/null 2>&1 || true
  curl -sf -X POST "$api/sendMessage" \
    -H "Content-Type: application/json" \
    -d "$(printf '{"chat_id":"%s","text":"🛑 Session s%s closed"}' "$owner_id" "$slot")" >/dev/null 2>&1 || true
  curl -sf -X POST "$api/unpinAllChatMessages" \
    -H "Content-Type: application/json" \
    -d "$(printf '{"chat_id":"%s"}' "$owner_id")" >/dev/null 2>&1 || true
}

# Read the current presence mode. Echoes auto|away|here. Default: auto.
_bridge_presence_mode() {
  local m
  m=$({ tr -d '[:space:]' < "$_BRIDGE_PRESENCE_FILE"; } 2>/dev/null)
  case "$m" in
    auto|away|here) printf '%s' "$m" ;;
    *)              printf 'auto' ;;
  esac
}

# Set presence mode. $1 must be auto|away|here. Prints a one-line confirmation.
_bridge_presence_set() {
  local mode="$1"
  case "$mode" in
    auto|away|here) ;;
    *) echo "bridge: invalid presence mode '$mode' (expected auto|away|here)" >&2; return 2 ;;
  esac
  mkdir -p "$_BRIDGE_CACHE"
  printf '%s\n' "$mode" > "$_BRIDGE_PRESENCE_FILE"
  echo "bridge: presence set to '$mode'"
}

# Print current presence mode and per-slot effective state.
_bridge_presence_show() {
  local mode
  mode=$(_bridge_presence_mode)
  echo "presence mode: $mode"
  [ -f "$_BRIDGE_SLOTS_FILE" ] || { echo "(no slots configured)"; return; }
  python3 -c "
import json, subprocess
with open('$_BRIDGE_SLOTS_FILE') as f: d = json.load(f)
mode = '$mode'
for n in sorted(d.get('slots', {}).keys(), key=int):
    v = d['slots'][n]
    if not v:
        print(f's{n}: free')
        continue
    sess = v.get('session') or ''
    if mode == 'away':
        eff = 'away (forced)'
    elif mode == 'here':
        eff = 'present (forced)'
    elif sess:
        r = subprocess.run(['tmux','list-clients','-t',sess],
                           stdout=subprocess.PIPE, stderr=subprocess.DEVNULL)
        n_clients = len([l for l in r.stdout.decode().splitlines() if l.strip()])
        eff = 'present' if n_clients > 0 else 'away'
    else:
        eff = 'unknown (no session recorded)'
    print(f's{n}: {eff}  (repo={v.get(\"repo\",\"?\")}, session={sess or \"—\"})')
" 2>/dev/null
}

# Decide whether slot $1 should send a Telegram page right now.
# Returns 0 (page) or 1 (silent).
_bridge_should_page() {
  local slot="$1"
  local mode
  mode=$(_bridge_presence_mode)
  case "$mode" in
    away) return 0 ;;
    here) return 1 ;;
    auto)
      # Look up the slot's tmux session name from slots.json
      local sess
      sess=$(python3 -c "
import json
try:
    with open('$_BRIDGE_SLOTS_FILE') as f: d = json.load(f)
    v = d.get('slots', {}).get('$slot')
    print((v or {}).get('session') or '')
except Exception:
    pass
" 2>/dev/null)
      # No recorded session → assume away (page); we'd rather notify than miss
      [ -z "$sess" ] && return 0
      # Dead session → page (slots.json wasn't reconciled yet)
      tmux has-session -t "$sess" 2>/dev/null || return 0
      # Live session — count attached clients
      local n
      n=$(tmux list-clients -t "$sess" 2>/dev/null | wc -l)
      [ "$n" -eq 0 ] && return 0 || return 1
      ;;
  esac
}

# Send arbitrary text via slot $1's bot to the configured owner.
# Args: $1 = slot, $2 = message text. Best-effort; never fails the caller.
# Reads the slot bot token from Passbolt via slot-tokens.json, owner from owner.json.
_bridge_telegram_page() {
  local slot="$1" text="$2"
  [ -z "$slot" ] && return 0
  [ -z "$text" ] && return 0

  local pb_id token owner_id
  pb_id=$(python3 -c "
import json
try:
    with open('$_BRIDGE_SLOT_TOKENS') as f: d = json.load(f)
    print(d.get('$slot', ''))
except Exception:
    pass
" 2>/dev/null)
  [ -z "$pb_id" ] && return 0

  token=$(passbolt get resource --id "$pb_id" 2>/dev/null | awk -F": " '/^Password:/ {print $2}')
  [ -z "$token" ] && return 0

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
    -d "$(python3 -c "import json,sys; print(json.dumps({'chat_id': '$owner_id', 'text': sys.stdin.read()}))" <<< "$text")" \
    >/dev/null 2>&1 || true
}

# Idempotently merge the Notification + UserPromptSubmit + SessionStart
# (matcher: clear) hooks into slot $1's settings.json
# (~/.claude-s<N>/settings.json). The hook commands include the slot
# number as a positional arg so the hook scripts know which slot fired.
_bridge_install_hooks() {
  local slot="$1"
  [ -z "$slot" ] && return 1
  local cfg_dir="$HOME/.claude-s${slot}"
  local cfg="$cfg_dir/settings.json"
  local notify="$_BRIDGE_DIR/bridge-hooks/notify.sh"
  local clear="$_BRIDGE_DIR/bridge-hooks/clear-idle.sh"
  local relabel="$_BRIDGE_DIR/bridge-hooks/relabel.sh"

  [ -x "$notify" ]  || chmod +x "$notify"  2>/dev/null
  [ -x "$clear" ]   || chmod +x "$clear"   2>/dev/null
  [ -x "$relabel" ] || chmod +x "$relabel" 2>/dev/null

  mkdir -p "$cfg_dir" "$_BRIDGE_CACHE"
  exec {_lock_fd}>"$_BRIDGE_CACHE/hooks.lock"
  flock "$_lock_fd"
  python3 -c "
import json, os, re
cfg = '$cfg'
slot = '$slot'
notify_cmd  = '$notify $slot'
clear_cmd   = '$clear $slot'
relabel_cmd = '$relabel $slot'

try:
    with open(cfg) as f: d = json.load(f)
except FileNotFoundError:
    d = {}
except json.JSONDecodeError:
    # Corrupt — back up and start fresh
    os.rename(cfg, cfg + '.corrupt')
    d = {}

hooks = d.setdefault('hooks', {})

# Match any prior entry that targets the same bridge-hooks script for this
# slot, regardless of where the script lived on disk. This makes installs
# replace stale entries when the script path moves (e.g. when the hooks
# get extracted into a new repo), instead of appending duplicates that
# 404 every time the hook fires.
def script_re(basename):
    return re.compile(r'/bridge-hooks/' + re.escape(basename) + r'(\s+' + re.escape(slot) + r')?\s*$')

def upsert(key, basename, cmd, matcher=''):
    pat = script_re(basename)
    entries = hooks.setdefault(key, [])
    pruned = []
    for e in entries:
        keep_hooks = [h for h in (e.get('hooks') or []) if not pat.search(h.get('command', ''))]
        if not keep_hooks:
            continue
        if len(keep_hooks) != len(e.get('hooks') or []):
            e = dict(e, hooks=keep_hooks)
        pruned.append(e)
    pruned.append({'matcher': matcher, 'hooks': [{'type': 'command', 'command': cmd}]})
    hooks[key] = pruned

upsert('Notification',      'notify.sh',     notify_cmd)
upsert('UserPromptSubmit',  'clear-idle.sh', clear_cmd)
upsert('SessionStart',      'relabel.sh',    relabel_cmd, matcher='clear')

with open(cfg, 'w') as f: json.dump(d, f, indent=2)
" 2>/dev/null
  flock -u "$_lock_fd"
}

# Wire slot 0 (admin) for the SessionStart-clear hook + label restore:
#   1. write the label to ~/.claude-s0/bridge-label so the relabel hook
#      can read it.
#   2. install the same hook bundle bridge installs for slots 1..N
#      (Notification, UserPromptSubmit, SessionStart-clear).
#
# Slot 0 is launched manually by the user (BotFather-named bot, no
# bridge allocation), so this setup has no other entry point. Run once
# after picking a label, then again only if you want to change it.
# Args: $1 = display label (e.g. "Claude Admin")
_bridge_setup_admin() {
  local label="${1:-}"
  if [ -z "$label" ]; then
    echo "bridge: --setup-admin requires a label, e.g. \`bridge --setup-admin 'Claude Admin'\`" >&2
    return 2
  fi
  local cfg_dir="$HOME/.claude-s0"
  mkdir -p "$cfg_dir" || { echo "bridge: failed to create $cfg_dir" >&2; return 1; }
  printf '%s\n' "$label" > "$cfg_dir/bridge-label" || return 1
  _bridge_install_hooks 0 || return 1
  echo "bridge: admin (slot 0) wired"
  echo "  label file: $cfg_dir/bridge-label"
  echo "  hooks:      $cfg_dir/settings.json (Notification, UserPromptSubmit, SessionStart[clear])"
  echo "  on /clear   the SessionStart hook will ask Claude to restore the label via /rename"
}

# Symlink the admin slash-command markdown files from
# `bridge-admin-commands/` into ~/.claude-s0/commands/. Slot 0 is the
# admin Claude session (manually launched, BotFather-named bot); these
# commands wrap bridge flags so the user can invoke `--status`,
# `--worktree-status`, `--issues`, etc. via Claude's slash-command UI.
# Idempotent — replaces existing symlinks pointing at our directory,
# leaves any unrelated files alone.
_bridge_install_admin_commands() {
  local src="$_BRIDGE_DIR/bridge-admin-commands"
  local dst="$HOME/.claude-s0/commands"
  [ -d "$src" ] || { echo "bridge: admin commands dir missing: $src" >&2; return 1; }
  mkdir -p "$dst" || { echo "bridge: failed to create $dst" >&2; return 1; }

  local installed=0
  local f name target
  for f in "$src"/*.md; do
    [ -f "$f" ] || continue
    name=$(basename "$f")
    target="$dst/$name"
    # Replace existing symlink only if it points at our source dir;
    # leave a regular file alone (could be user-customised).
    if [ -L "$target" ]; then
      ln -sfn "$f" "$target"
    elif [ -e "$target" ]; then
      echo "bridge: $target exists and is not a symlink; skipping" >&2
      continue
    else
      ln -s "$f" "$target"
    fi
    installed=$((installed + 1))
  done
  echo "bridge: installed $installed admin slash commands at $dst"
  echo "  invoke from inside the slot 0 Claude session via /<command>:"
  for f in "$src"/*.md; do
    [ -f "$f" ] || continue
    printf '    /%s\n' "$(basename "$f" .md)"
  done
}

# Start the usage-limit watcher daemon if not already running. Idempotent.
_bridge_watcher_start() {
  local pid_file="$_BRIDGE_CACHE/watcher.pid"
  if [ -f "$pid_file" ]; then
    if kill -0 "$(cat "$pid_file")" 2>/dev/null; then
      return 0  # already running
    fi
  fi
  local watcher="$_BRIDGE_DIR/bridge-watcher.sh"
  [ -x "$watcher" ] || chmod +x "$watcher" 2>/dev/null
  ( setsid "$watcher" </dev/null >/dev/null 2>&1 & ) 2>/dev/null
  return 0
}

# Reconcile dead slots in slots.json: tmux session is source of truth when
# the slot record has one, otherwise fall back to PID liveness for
# foreground-mode records. Idempotent and silent on no-op. Both
# _bridge_slot_status and _bridge_attach_pick call this before reading.
_bridge_reconcile_slots() {
  python3 -c "
import json, os, subprocess
f = '$_BRIDGE_SLOTS_FILE'
MAX = $_BRIDGE_MAX_SLOTS
with open(f) as fh: d = json.load(fh)
slots = d.setdefault('slots', {})
for k, v in list(slots.items()):
    if not v: continue
    sess = v.get('session') or ''
    if sess:
        alive = subprocess.run(['tmux', 'has-session', '-t', sess],
                               stdout=subprocess.DEVNULL,
                               stderr=subprocess.DEVNULL).returncode == 0
    else:
        try: os.kill(int(v.get('pid', 0)), 0); alive = True
        except (ProcessLookupError, ValueError): alive = False
        except PermissionError: alive = True
    if not alive:
        slots[k] = None
# Drop empty entries whose key isn't a valid slot number (non-numeric, negative,
# or > MAX_SLOTS) — leftover from manual edits or shrunk MAX. Live entries are
# preserved so we never orphan a running session's record.
for k in list(slots.keys()):
    if slots[k] is not None: continue
    try: n = int(k)
    except ValueError: del slots[k]; continue
    if n < 0 or n > MAX: del slots[k]
with open(f, 'w') as fh: json.dump(d, fh, indent=2)
" 2>/dev/null
}

# Print unified session-status table.
#
# Row sources:
#   1. slots.json — all slot rows 0..MAX (occupied or not).
#   2. tmux-tagged sessions — every `tmux list-sessions` entry with
#      @bridge-repo set. Dedup: if its session name matches a slot row's
#      .session, the slot row wins (richer metadata).
#
# Output: one table + optional "Remote Control URLs:" footer for rows
# with an active bridgeSessionId. RC lookup mirrors the old --status-rc
# logic: slot rows read ~/.claude-s<N>/sessions/<pid>.json; synthetic
# rows try ~/.claude first, then fall back to scanning every ~/.claude-s*
# (stray sessions launched outside the bridge launcher land under a
# slot-specific home directory inherited from the parent shell).
_bridge_slot_status() {
  _bridge_slots_init
  _bridge_reconcile_slots

  # Enumerate tmux-tagged sessions. Tab-separated for parse safety.
  # Format fields: name, created, repo, worktree, kind, slot, pid.
  local tmux_rows
  tmux_rows=$(tmux list-sessions -F \
    '#{session_name}	#{session_created}	#{@bridge-repo}	#{@bridge-worktree}	#{@bridge-kind}	#{@bridge-slot}	#{@bridge-pid}' \
    2>/dev/null)

  # Also enumerate panes so we can surface untagged tmux sessions whose
  # pane is running `claude` (started outside the bridge launcher).
  # Format: session_name, pane_pid, pane_current_command, pane_current_path.
  local tmux_panes
  tmux_panes=$(tmux list-panes -a -F \
    '#{session_name}	#{pane_pid}	#{pane_current_command}	#{pane_current_path}' \
    2>/dev/null)

  python3 -c "
import glob, json, os, time

slots_file = '$_BRIDGE_SLOTS_FILE'
MAX = $_BRIDGE_MAX_SLOTS
tmux_rows_raw = '''$tmux_rows'''
tmux_panes_raw = '''$tmux_panes'''

with open(slots_file) as f: d = json.load(f)
slots = d.get('slots', {})

now = int(time.time())

def bridge_for(cfg_dir, pid):
    if not pid: return ''
    sess_dir = os.path.join(os.path.expanduser(cfg_dir), 'sessions')
    if not os.path.isdir(sess_dir): return ''
    p = os.path.join(sess_dir, f'{pid}.json')
    if not os.path.isfile(p): return ''
    try:
        with open(p) as fh: sd = json.load(fh)
        return sd.get('bridgeSessionId') or ''
    except Exception:
        return ''

def bridge_for_any(pid):
    # Try ~/.claude first, then every ~/.claude-s*. Used for non-slot rows
    # whose owning slot dir isn't recoverable from tmux state.
    if not pid: return ''
    b = bridge_for('~/.claude', pid)
    if b: return b
    for cfg in sorted(glob.glob(os.path.expanduser('~/.claude-s*'))):
        b = bridge_for(cfg, pid)
        if b: return b
    return ''

def fmt_age(sa):
    if not sa: return '—'
    age = now - int(sa)
    h, m = divmod(age // 60, 60)
    return f'{h}h{m:02d}m ago'

# --- Source 1: slot rows ---
rows = []      # list of dicts in display order
slot_sessions = set()  # tmux session names already covered by a slot row
slot_keys = {str(n) for n in range(0, MAX + 1)}
for n in sorted(slot_keys, key=int):
    v = slots.get(n)
    if v:
        sess = v.get('session') or ''
        if sess: slot_sessions.add(sess)
        repo = v.get('repo', '')
        wt = v.get('worktree') or ''
        repo_disp = f'{repo} [{wt}]' if wt else repo
        pid = v.get('pid', 0)
        bot = '(admin bot)' if int(n) == 0 else f'@claude_freax_s{n}_bot'
        cfg = f'~/.claude-s{n}'
        bridge = bridge_for(cfg, pid)
        rows.append({
            'slot':    f's{n}',
            'kind':    'slot',
            'repo':    repo_disp or '—',
            'started': fmt_age(v.get('started_at', 0)),
            'pid':     str(pid) if pid else '—',
            'tmux':    sess or '—',
            'bot':     bot,
            'bridge':  bridge,
            'label':   f's{n}',
        })
    else:
        bot = '(admin bot)' if int(n) == 0 else f'@claude_freax_s{n}_bot'
        rows.append({
            'slot': f's{n}', 'kind': 'slot', 'repo': '—',
            'started': '—', 'pid': '—', 'tmux': '—', 'bot': bot,
            'bridge': '', 'label': f's{n}',
        })

# --- Source 2: tmux-tagged rows (synthetic, non-slot) ---
synth = []
tagged_sessions = set()
for line in tmux_rows_raw.strip().split('\n'):
    if not line: continue
    parts = line.split('\t')
    if len(parts) < 7: continue
    name, created, repo, wt, kind, slot, pid = parts[:7]
    if not repo: continue                  # untagged — handled in Source 3
    tagged_sessions.add(name)
    if name in slot_sessions: continue      # dedup: slot row already has it
    repo_disp = f'{repo} [{wt}]' if wt else repo
    if kind in ('no-channel', 'unmanaged'):
        bridge = bridge_for_any(pid)
    else:
        bridge = ''  # code/opencode have no Claude session file
    try: created_i = int(created)
    except ValueError: created_i = 0
    synth.append({
        'slot':    '—',
        'kind':    kind or '—',
        'repo':    repo_disp,
        'started': fmt_age(created_i),
        'pid':     pid or '—',
        'tmux':    name,
        'bot':     '—',
        'bridge':  bridge,
        'label':   repo_disp,
        'created': created_i,
    })

# --- Source 3: untagged tmux sessions running a claude pane ---
# These are claude processes started outside the bridge launcher (e.g.
# bare 'tmux new -s foo claude ...'). Surface them as kind=unmanaged
# (backticks intentionally avoided — this comment lives inside a
# double-quoted python3 -c heredoc, where bash would treat them as
# command substitution and actually spawn the example.)
# so they're discoverable in --status / --attach.
session_created = {}
for line in tmux_rows_raw.strip().split('\n'):
    if not line: continue
    parts = line.split('\t')
    if len(parts) < 2: continue
    try: session_created[parts[0]] = int(parts[1])
    except ValueError: pass

seen_unmanaged = set()
for line in tmux_panes_raw.strip().split('\n'):
    if not line: continue
    parts = line.split('\t')
    if len(parts) < 4: continue
    sess_name, pane_pid, pane_cmd, pane_path = parts[:4]
    if sess_name in tagged_sessions: continue
    if sess_name in slot_sessions:   continue
    if sess_name in seen_unmanaged:  continue
    if pane_cmd != 'claude':         continue
    seen_unmanaged.add(sess_name)
    repo_disp = os.path.basename(pane_path) or sess_name
    created_i = session_created.get(sess_name, 0)
    synth.append({
        'slot':    '—',
        'kind':    'unmanaged',
        'repo':    repo_disp,
        'started': fmt_age(created_i),
        'pid':     pane_pid or '—',
        'tmux':    sess_name,
        'bot':     '—',
        'bridge':  bridge_for_any(pane_pid),
        'label':   repo_disp,
        'created': created_i,
    })

# Sort synthetic rows newest first, then append after slot rows.
synth.sort(key=lambda r: -r.get('created', 0))
rows.extend(synth)

# --- Render table ---
hdr = f\"{'SLOT':<5} {'KIND':<11} {'REPO':<28} {'STARTED':<13} {'PID':<8} {'TMUX':<20} {'BOT':<28} {'RC'}\"
print(hdr)
print('-' * len(hdr))
for r in rows:
    rc = '✓' if r['bridge'] else '—'
    print(f\"{r['slot']:<5} {r['kind']:<11} {r['repo']:<28} {r['started']:<13} {r['pid']:<8} {r['tmux']:<20} {r['bot']:<28} {rc}\")

# --- Render URL footer (only if at least one bridge is active) ---
rc_rows = [r for r in rows if r['bridge']]
if rc_rows:
    print()
    print('Remote Control URLs:')
    for r in rc_rows:
        url = f\"https://claude.ai/code/{r['bridge']}\"
        print(f\"  {r['label']:<12} {url}\")
" 2>/dev/null
}

# Deprecated. RC info is now merged into `bridge --status`'s footer.
# Kept for one release as an alias so muscle memory / scripts don't break;
# scheduled for removal a minor release after 1.28.x.
_bridge_slot_status_rc() {
  echo "bridge: --status-rc is deprecated; use --status (RC URLs now shown in the footer)" >&2
  _bridge_slot_status
}

# Diagnose forge targets: list each, verify direnv exports the expected
# token, and test access by calling the forge's `/user` (or equivalent)
# endpoint. Prints one block per target with ✓/✗ markers and a final
# summary. Returns 0 if all checks passed, 1 otherwise.
_bridge_doctor() {
  local targets
  targets=$(_bridge_targets)
  if [ -z "$targets" ]; then
    echo "bridge: no forge targets discovered under any of: $(_bridge_display_bases)" >&2
    return 1
  fi

  local pass=0 fail=0
  while IFS=$'\t' read -r rel forge owner vis; do
    [ -z "$rel" ] && continue
    local label
    case "$forge" in
      github) label="$forge ($owner/$vis)" ;;
      *)      label="$forge ($owner)" ;;
    esac
    printf '\n\033[1m%s\033[0m  path: %s\n' "$label" "$rel"

    local result
    result=$(
      cd "$(_bridge_base_for_rel "$rel")/$rel" 2>/dev/null || { echo "ERR: target dir missing"; exit 1; }
      if ! command -v direnv >/dev/null; then
        echo "ERR: direnv not on PATH"; exit 1
      fi
      eval "$(direnv export bash 2>/dev/null)"
      case "$forge" in
        github)
          local tok="${GH_TOKEN:-${GITHUB_TOKEN:-}}"
          if [ -z "$tok" ]; then echo "TOKEN: missing GH_TOKEN/GITHUB_TOKEN"; exit 1; fi
          local code body
          body=$(curl -s -o /tmp/.bridge-doctor.$$ -w '%{http_code}' \
            -H "Authorization: token $tok" \
            -H "Accept: application/vnd.github+json" \
            "https://api.github.com/user")
          code="$body"
          local login=""
          login=$(jq -r '.login // empty' /tmp/.bridge-doctor.$$ 2>/dev/null)
          rm -f /tmp/.bridge-doctor.$$ 2>/dev/null
          if [ "$code" = "200" ] && [ -n "$login" ]; then
            echo "OK: GH_TOKEN present; api.github.com/user → $login"
          else
            echo "FAIL: GH_TOKEN present; api.github.com/user → HTTP $code"
            exit 1
          fi
          ;;
        gitlab)
          if [ -z "${GITLAB_TOKEN:-}" ]; then echo "TOKEN: missing GITLAB_TOKEN"; exit 1; fi
          local code body
          body=$(curl -s -o /tmp/.bridge-doctor.$$ -w '%{http_code}' \
            -H "PRIVATE-TOKEN: $GITLAB_TOKEN" \
            "https://gitlab.freaxnx01.ch/api/v4/user")
          code="$body"
          local user=""
          user=$(jq -r '.username // empty' /tmp/.bridge-doctor.$$ 2>/dev/null)
          rm -f /tmp/.bridge-doctor.$$ 2>/dev/null
          if [ "$code" = "200" ] && [ -n "$user" ]; then
            echo "OK: GITLAB_TOKEN present; api/v4/user → $user"
          else
            echo "FAIL: GITLAB_TOKEN present; api/v4/user → HTTP $code"
            exit 1
          fi
          ;;
        forgejo)
          if [ -z "${FORGEJO_TOKEN:-}" ]; then echo "TOKEN: missing FORGEJO_TOKEN"; exit 1; fi
          local code body
          body=$(curl -s -o /tmp/.bridge-doctor.$$ -w '%{http_code}' \
            -H "Authorization: token $FORGEJO_TOKEN" \
            "https://git.home.freaxnx01.ch/api/v1/user")
          code="$body"
          local user=""
          user=$(jq -r '.login // empty' /tmp/.bridge-doctor.$$ 2>/dev/null)
          rm -f /tmp/.bridge-doctor.$$ 2>/dev/null
          if [ "$code" = "200" ] && [ -n "$user" ]; then
            echo "OK: FORGEJO_TOKEN present; api/v1/user → $user"
          else
            echo "FAIL: FORGEJO_TOKEN present; api/v1/user → HTTP $code"
            exit 1
          fi
          ;;
        ado)
          local tok="${AZURE_DEVOPS_EXT_PAT:-${ADO_PAT:-}}"
          if [ -z "$tok" ]; then echo "TOKEN: missing AZURE_DEVOPS_EXT_PAT/ADO_PAT"; exit 1; fi
          local code body
          body=$(curl -s -o /tmp/.bridge-doctor.$$ -w '%{http_code}' -u ":$tok" \
            "https://dev.azure.com/$owner/_apis/connectionData?api-version=7.1")
          code="$body"
          local user=""
          user=$(jq -r '.authenticatedUser.providerDisplayName // empty' /tmp/.bridge-doctor.$$ 2>/dev/null)
          rm -f /tmp/.bridge-doctor.$$ 2>/dev/null
          if [ "$code" = "200" ] && [ -n "$user" ]; then
            echo "OK: ADO PAT present; connectionData → $user"
          else
            echo "FAIL: ADO PAT present; connectionData → HTTP $code"
            exit 1
          fi
          ;;
      esac
    )
    local rc=$?
    case "$result" in
      "OK: "*)
        printf '  \033[32m✓\033[0m %s\n' "${result#OK: }"
        pass=$((pass + 1)) ;;
      "FAIL: "*)
        printf '  \033[31m✗\033[0m %s\n' "${result#FAIL: }"
        fail=$((fail + 1)) ;;
      "TOKEN: "*)
        printf '  \033[31m✗\033[0m %s\n' "${result#TOKEN: }"
        fail=$((fail + 1)) ;;
      "ERR: "*)
        printf '  \033[31m✗\033[0m %s\n' "${result#ERR: }"
        fail=$((fail + 1)) ;;
      *)
        printf '  \033[31m✗\033[0m unknown failure (rc=%s): %s\n' "$rc" "$result"
        fail=$((fail + 1)) ;;
    esac
  done <<< "$targets"

  printf '\nSummary: %d passed, %d failed\n' "$pass" "$fail"
  [ "$fail" = 0 ]
}

# Print git worktree/dirty/ahead status across all local repos under
# $_BRIDGE_BASE. One row per repo, plus one indented row per linked
# worktree (other than the main one) so all in-progress work is visible
# at a glance.
_bridge_worktree_status() {
  local repos
  repos=$(
    for _b in "${_BRIDGE_BASES[@]}"; do
      find "$_b" -type d -name '_archive' -prune -o -type d -name .git -printf '%h\n' 2>/dev/null
    done | sort
  )
  if [ -z "$repos" ]; then
    echo "bridge: no repos found under any of: $(_bridge_display_bases)" >&2
    return 1
  fi

  printf '%-32s %-22s %-6s %-6s %s\n' "REPO" "BRANCH" "DIRTY" "AHEAD" "WORKTREES"
  printf -- '-%.0s' {1..95}; printf '\n'

  local total=0 dirty=0 ahead=0 wt_count=0
  while IFS= read -r repo; do
    [ -z "$repo" ] && continue
    total=$((total + 1))
    # Strip whichever base owns this repo. With one base configured this is
    # identical to the old `${repo#$_BRIDGE_BASE/}`; with multiple, picks the
    # first matching prefix (matches the precedence order of _BRIDGE_BASES).
    local rel="$repo" _b
    for _b in "${_BRIDGE_BASES[@]}"; do
      [[ "$repo" == "$_b/"* ]] && { rel="${repo#$_b/}"; break; }
    done
    local short
    short=$(basename "$rel")

    local branch
    branch=$(git -C "$repo" symbolic-ref --quiet --short HEAD 2>/dev/null) \
      || branch="($(git -C "$repo" rev-parse --short HEAD 2>/dev/null || echo 'detached'))"

    local d='no'
    if [ -n "$(git -C "$repo" status --porcelain 2>/dev/null)" ]; then
      d='yes'; dirty=$((dirty + 1))
    fi

    local upstream a='—'
    upstream=$(git -C "$repo" rev-parse --abbrev-ref --symbolic-full-name '@{u}' 2>/dev/null)
    if [ -n "$upstream" ]; then
      a=$(git -C "$repo" rev-list --count "$upstream..HEAD" 2>/dev/null || echo '?')
      if [ "$a" != '0' ] && [ "$a" != '?' ]; then
        ahead=$((ahead + 1))
      fi
    fi

    local worktrees
    worktrees=$(git -C "$repo" worktree list --porcelain 2>/dev/null \
                  | awk '/^worktree / {print $2}' \
                  | grep -vxF "$repo" \
                  | xargs -r -n1 basename \
                  | paste -sd ',' -)
    [ -z "$worktrees" ] && worktrees='—' || wt_count=$((wt_count + 1))

    printf '%-32s %-22s %-6s %-6s %s\n' "$short" "$branch" "$d" "$a" "$worktrees"
  done <<< "$repos"

  printf '\n%d repos · %d dirty · %d ahead · %d with extra worktrees\n' \
    "$total" "$dirty" "$ahead" "$wt_count"
}

# Aggregate open issues across configured GitHub + Forgejo forges into
# a single overview. Iterates discovered forge targets, dedupes by
# (forge, owner), and queries each forge's "issues across owned repos"
# endpoint:
#   - github  → /search/issues?q=is:issue+is:open+user:<owner>
#   - forgejo → /repos/issues/search?state=open&type=issues&owner=<owner>
# GitLab/ADO are skipped (different issue/work-item models, out of scope
# for the `claude-ready` / `claude-working` workflow this command serves).
_bridge_issues() {
  local targets
  targets=$(_bridge_targets)
  if [ -z "$targets" ]; then
    echo "bridge: no forge targets discovered under any of: $(_bridge_display_bases)" >&2
    return 1
  fi

  # Dedupe by (forge, owner) — public/private subdirs share a token.
  # Pick the first matching rel for each pair (used as the cd target so
  # direnv can load the credentials).
  local pairs
  pairs=$(printf '%s\n' "$targets" \
    | awk -F'\t' '$2=="github" || $2=="forgejo" {
        key = $2 "\t" $3
        if (!(key in seen)) { seen[key] = 1; print $1 "\t" $2 "\t" $3 }
      }')

  if [ -z "$pairs" ]; then
    echo "bridge: no GitHub or Forgejo targets discovered" >&2
    return 1
  fi

  local total=0
  while IFS=$'\t' read -r rel forge owner; do
    [ -z "$rel" ] && continue
    local rows
    rows=$(
      cd "$(_bridge_base_for_rel "$rel")/$rel" 2>/dev/null || exit
      command -v direnv >/dev/null && eval "$(direnv export bash 2>/dev/null)"
      case "$forge" in
        github)
          local tok="${GH_TOKEN:-${GITHUB_TOKEN:-}}"
          [ -z "$tok" ] && exit 0
          # search/issues caps at 100 per page; bump if you ever exceed.
          curl -sf -H "Authorization: token $tok" \
            -H "Accept: application/vnd.github+json" \
            "https://api.github.com/search/issues?q=is:issue+is:open+user:$owner&per_page=100" \
            | jq -r --arg forge "$forge" '
                .items // []
                | sort_by(.repository_url, .number)
                | .[]
                | [ $forge,
                    (.repository_url | sub("https://api.github.com/repos/"; "")),
                    (.number | tostring),
                    (.title | gsub("[\\t\\n\\r]"; " ")),
                    ((.labels // []) | map(.name) | join(",")),
                    .html_url ]
                | @tsv
              ' 2>/dev/null
          ;;
        forgejo)
          [ -z "${FORGEJO_TOKEN:-}" ] && exit 0
          curl -sf -H "Authorization: token $FORGEJO_TOKEN" \
            "https://git.home.freaxnx01.ch/api/v1/repos/issues/search?state=open&type=issues&owner=$owner&limit=50" \
            | jq -r --arg forge "$forge" '
                . // []
                | sort_by(.repository.full_name, .number)
                | .[]
                | [ $forge,
                    .repository.full_name,
                    (.number | tostring),
                    (.title | gsub("[\\t\\n\\r]"; " ")),
                    ((.labels // []) | map(.name) | join(",")),
                    .html_url ]
                | @tsv
              ' 2>/dev/null
          ;;
      esac
    )
    if [ -n "$rows" ]; then
      while IFS= read -r row; do
        [ -z "$row" ] && continue
        total=$((total + 1))
        printf '%s\n' "$row"
      done <<< "$rows"
    fi
  done <<< "$pairs" | awk -F'\t' -v total_var="" '
    BEGIN {
      printf "%-8s %-30s %-5s %-50s %s\n", "FORGE", "REPO", "#", "TITLE", "LABELS"
      for (i=0; i<110; i++) printf "-"
      printf "\n"
    }
    {
      forge=$1; repo=$2; num="#"$3; title=$4; labels=$5; url=$6
      if (length(title) > 50) title = substr(title, 1, 47) "..."
      pad = 5 - length(num); if (pad < 0) pad = 0
      num_link = "\033]8;;" url "\033\\" num "\033]8;;\033\\" sprintf("%*s", pad, "")
      printf "%-8s %-30s %s  %-50s %s\n", forge, repo, num_link, title, labels
      n++
    }
    END {
      printf "\n%d open issue%s\n", n, (n == 1 ? "" : "s")
    }
  '
}

# Cross-repo dashboard: one row per open issue, sorted PLAT → REPO → issue#.
# Fans out `gh issue list` in parallel over every local repo under
# $_BRIDGE_BASE. Repos without a reachable GitHub remote are silently skipped.
_bridge_dashboard() {
  local repos
  repos=$(find "$_BRIDGE_BASE" -type d -name '_archive' -prune -o -type d -name .git -printf '%h\n' 2>/dev/null | sed "s|^$_BRIDGE_BASE/||" | sort)
  if [ -z "$repos" ]; then
    echo "bridge: no local repos found under $(_bridge_display_path "$_BRIDGE_BASE")" >&2
    return 1
  fi

  local tmpdir
  tmpdir=$(mktemp -d) || return 1
  # shellcheck disable=SC2064
  trap "rm -rf '$tmpdir'" RETURN

  (
    while IFS= read -r rel; do
      [ -z "$rel" ] && continue
      (
        cd "$_BRIDGE_BASE/$rel" 2>/dev/null || exit 0
        command -v direnv >/dev/null && eval "$(direnv export bash 2>/dev/null)"
        json=$(gh issue list --state open --json number,title,url --limit 50 2>/dev/null) || exit 0
        IFS='/' read -ra p <<< "$rel"
        n=${#p[@]}
        case "${p[0]}" in
          github)      plat=GH  ;;
          git-forgejo) plat=FJ  ;;
          ado)         plat=ADO ;;
          *)           plat="${p[0]}" ;;
        esac
        name="${p[$((n-1))]}"
        if   [ "$n" -ge 4 ] && [ "${p[$((n-2))]}" = "private" ]; then vis=pri
        elif [ "$n" -ge 4 ]; then vis=pub
        else vis=""
        fi
        printf '%s\n' "$json" \
          | jq -r --arg plat "$plat" --arg vis "$vis" --arg name "$name" '
              sort_by(.number) | .[]
              | [$plat, $vis, $name, (.number | tostring), (.title | gsub("[\\t\\n\\r]"; " ")), .url]
              | @tsv
            ' 2>/dev/null \
          > "$tmpdir/$(printf '%s' "$rel" | tr '/' '_')"
      ) &
    done <<< "$repos"
    wait
  )

  local out
  out=$(cat "$tmpdir"/* 2>/dev/null | grep -v '^[[:space:]]*$' \
        | sort -t$'\t' -k1,1 -k3,3 -k4,4n)
  if [ -z "$out" ]; then
    echo "bridge: no open issues found under $(_bridge_display_path "$_BRIDGE_BASE")" >&2
    return 1
  fi

  local _termw; _termw=$(tput cols 2>/dev/null || echo 120)
  # fixed cols: 4+2+3+2+28+2+5+2 = 48; give the rest to TITLE (min 40)
  local _titlew=$(( _termw - 48 )); (( _titlew < 40 )) && _titlew=40
  printf '%s\n' "$out" | awk -F'\t' -v tw="$_titlew" '
    BEGIN { printf "%-4s  %-3s  %-28s  %-5s  %s\n", "PLAT", "VIS", "REPO", "#", "TITLE" }
    {
      title = $5; url = $6
      if (length(title) > tw) title = substr(title, 1, tw - 3) "..."
      num_text = "#" $4
      pad = 5 - length(num_text); if (pad < 0) pad = 0
      num_link = "\033]8;;" url "\033\\" num_text "\033]8;;\033\\" sprintf("%*s", pad, "")
      printf "%-4s  %-3s  %-28s  %s  %s\n", $1, $2, $3, num_link, title
    }
  '
}

# Focus repos — MVP scope (GitHub only; Forgejo, issue counts, caching, and
# tab completion are deferred follow-ups of issue #9). Source of truth is
# the `focus` repository topic on the source platform.

# Render the focus list from the JSON cache file. Called from
# _bridge_focus_list (cache hit path) and directly in tests.
_bridge_focus_display_cache() {
  local data n total_open total_mine warnings_out
  data=$(jq -r '
    .repos[] |
    [.platform, .name, .url, (.open | tostring), (.mine | tostring)] | @tsv
  ' "$_BRIDGE_FOCUS_CACHE" 2>/dev/null)

  if [ -z "$data" ]; then
    echo "bridge: no focus repos found." >&2
    echo "       Tag a repo via 'bridge --focus-add <name>' or set the 'focus' topic in the platform UI." >&2
    return 0
  fi

  printf 'FOCUS REPOS\n'
  printf -- '─%.0s' {1..56}; printf '\n'

  n=0; total_open=0; total_mine=0
  while IFS=$'\t' read -r platform name url open mine; do
    n=$((n + 1))
    local count_str
    if [ "${open:--1}" -lt 0 ] 2>/dev/null; then
      count_str="? open"
    elif [ "${mine:-0}" -gt 0 ] 2>/dev/null; then
      count_str="$open open · $mine yours"
      total_open=$((total_open + open))
      total_mine=$((total_mine + mine))
    else
      count_str="$open open"
      total_open=$((total_open + ${open:-0}))
    fi
    printf '[%s]  %-36s  %s\n' "$platform" "$name" "$count_str"
    printf '      %s\n' "$url"
  done <<< "$data"

  printf -- '─%.0s' {1..56}; printf '\n'
  if [ "$n" -eq 1 ]; then
    printf '1 focus repo'
  else
    printf '%d focus repos' "$n"
  fi
  if [ "$total_open" -gt 0 ]; then
    printf ' · %d open issues' "$total_open"
    [ "$total_mine" -gt 0 ] && printf ' · %d assigned to you' "$total_mine"
  fi
  printf '\n'

  warnings_out=$(jq -r '.warnings[]?' "$_BRIDGE_FOCUS_CACHE" 2>/dev/null)
  if [ -n "$warnings_out" ]; then
    while IFS= read -r w; do
      printf '  [!] %s\n' "$w"
    done <<< "$warnings_out"
  fi
}

# _bridge_regex_escape <string>
#   Escape ERE metacharacters in <string> so it can be embedded in a
#   `grep -E` pattern as a literal. Used by the focus name resolver so
#   repo names containing `.`, `+`, etc. don't match unintended rows.
_bridge_regex_escape() {
  printf '%s' "$1" | sed 's/[][\\.*^$+?(){}|/]/\\&/g'
}

# Resolve a local repo <name> to its rel path across every base in
# _BRIDGE_BASES, using the same basename matcher as the launch path
# (case-insensitive: exact first, then substring). Echos the rel on
# stdout, or returns 1 with a stderr message.
_bridge_focus_resolve() {
  local name="$1" _b all rel name_re
  all=$(
    for _b in "${_BRIDGE_BASES[@]}"; do
      find "$_b" -type d -name '_archive' -prune -o -type d -name .git -printf '%h\n' 2>/dev/null \
        | sed "s|^$_b/||"
    done
  )
  name_re=$(_bridge_regex_escape "$name")
  rel=$(printf '%s\n' "$all" | grep -Ei "(^|/)${name_re}\$" | head -1)
  [ -z "$rel" ] && rel=$(printf '%s\n' "$all" | grep -Ei "(^|/)[^/]*${name_re}[^/]*\$" | head -1)
  if [ -z "$rel" ]; then
    echo "bridge: no local repo named '$name'" >&2
    return 1
  fi
  printf '%s\n' "$rel"
}

# Add or remove the 'focus' topic on a GitHub repo. $1 = rel path,
# $2 = "add" or "rm". Loads per-dir credentials via direnv.
_bridge_focus_toggle_gh() {
  local rel="$1" action="$2"
  (
    cd "$(_bridge_base_for_rel "$rel")/$rel" 2>/dev/null || exit 1
    command -v direnv >/dev/null && eval "$(direnv export bash 2>/dev/null)"
    local nwo
    nwo=$(gh repo view --json nameWithOwner -q .nameWithOwner 2>/dev/null) || {
      echo "bridge: could not resolve nameWithOwner for $rel" >&2; exit 1
    }
    local current
    current=$(gh api "repos/$nwo/topics" --jq '.names' 2>/dev/null) || {
      echo "bridge: GitHub API error fetching topics for $nwo" >&2; exit 1
    }
    local has_focus
    has_focus=$(echo "$current" | jq 'index("focus") != null')
    if [ "$action" = "add" ]; then
      if [ "$has_focus" = "true" ]; then
        echo "bridge: $nwo already has 'focus' topic"; exit 0
      fi
      echo "$current" | jq '{names: (. + ["focus"])}' \
        | gh api -X PUT "repos/$nwo/topics" --input - >/dev/null || {
            echo "bridge: GitHub API error setting topics on $nwo" >&2; exit 1
          }
      echo "bridge: added 'focus' topic to $nwo"
    else
      if [ "$has_focus" = "false" ]; then
        echo "bridge: $nwo has no 'focus' topic"; exit 0
      fi
      echo "$current" | jq '{names: (. - ["focus"])}' \
        | gh api -X PUT "repos/$nwo/topics" --input - >/dev/null || {
            echo "bridge: GitHub API error setting topics on $nwo" >&2; exit 1
          }
      echo "bridge: removed 'focus' topic from $nwo"
    fi
  )
}

# Add or remove the 'focus' topic on a Forgejo repo. $1 = rel path under
# git-forgejo/, $2 = "add" or "rm". Uses PUT/DELETE /api/v1/repos/freax/<name>/topics/focus.
_bridge_focus_toggle_fj() {
  local rel="$1" action="$2"
  (
    cd "$(_bridge_base_for_rel "$rel")/$rel" 2>/dev/null || exit 1
    command -v direnv >/dev/null && eval "$(direnv export bash 2>/dev/null)"
    [ -z "${FORGEJO_TOKEN:-}" ] && { echo "bridge: no FORGEJO_TOKEN for Forgejo repo" >&2; exit 1; }
    local name method code
    name=$(basename "$rel")
    [ "$action" = "add" ] && method=PUT || method=DELETE
    code=$(curl -sf -o /dev/null -w '%{http_code}' -X "$method" \
      -H "Authorization: token $FORGEJO_TOKEN" \
      "https://git.home.freaxnx01.ch/api/v1/repos/freax/$name/topics/focus" 2>/dev/null)
    case "$code" in
      20[04]) ;;
      404) echo "bridge: Forgejo repo 'freax/$name' not found" >&2; exit 1 ;;
      *)   echo "bridge: Forgejo API error (HTTP $code) on freax/$name" >&2; exit 1 ;;
    esac
    if [ "$action" = "add" ]; then
      echo "bridge: added 'focus' topic to freax/$name"
    else
      echo "bridge: removed 'focus' topic from freax/$name"
    fi
  )
}

_bridge_focus_add() {
  local rel
  rel=$(_bridge_focus_resolve "$1") || return 1
  case "$rel" in
    github/*)      _bridge_focus_toggle_gh "$rel" add || return 1 ;;
    git-forgejo/*) _bridge_focus_toggle_fj "$rel" add || return 1 ;;
    ado/*)    echo "bridge: focus is unsupported for Azure DevOps. Open via 'bridge -c $1'." >&2; return 1 ;;
    *)        echo "bridge: focus not supported for platform of '$rel'." >&2; return 1 ;;
  esac
  rm -f "$_BRIDGE_FOCUS_CACHE"
}

_bridge_focus_rm() {
  local rel
  rel=$(_bridge_focus_resolve "$1") || return 1
  case "$rel" in
    github/*)      _bridge_focus_toggle_gh "$rel" rm || return 1 ;;
    git-forgejo/*) _bridge_focus_toggle_fj "$rel" rm || return 1 ;;
    ado/*)    echo "bridge: focus is unsupported for Azure DevOps." >&2; return 1 ;;
    *)        echo "bridge: focus not supported for platform of '$rel'." >&2; return 1 ;;
  esac
  rm -f "$_BRIDGE_FOCUS_CACHE"
}

# List focus-tagged repos across configured GH owners and Forgejo. Reads from
# the JSON cache when fresh; fetches and writes a new cache when stale.
# $1 = 1 to bypass cache (--no-cache); omit or 0 to use cache.
_bridge_focus_list() {
  local force_refresh="${1:-0}"

  # --- Cache read ---
  if [ -f "$_BRIDGE_FOCUS_CACHE" ] && [ "$force_refresh" != "1" ]; then
    local _age
    _age=$(( $(date +%s) - $(jq '.fetched_at // 0' "$_BRIDGE_FOCUS_CACHE" 2>/dev/null || echo 0) ))
    if [ "$_age" -lt "$_BRIDGE_FOCUS_TTL" ]; then
      _bridge_focus_display_cache
      return
    fi
  fi

  local tmpdir
  tmpdir=$(mktemp -d) || return 1
  # shellcheck disable=SC2064
  trap "rm -rf '$tmpdir'" RETURN

  # --- Phase 1: fetch focus repo lists ---
  local pairs first_rel="" first_owner=""
  pairs=$(_bridge_targets \
    | awk -F'\t' '$2=="github" {
        key = $2 "\t" $3
        if (!(key in seen)) { seen[key] = 1; print $1 "\t" $3 }
      }')
  [ -n "$pairs" ] && IFS=$'\t' read -r first_rel first_owner \
    <<< "$(printf '%s\n' "$pairs" | head -1)"

  local fj_rel
  fj_rel=$(_bridge_targets | awk -F'\t' '$2=="forgejo" { print $1; exit }')
  (
    i=0
    if [ -n "$pairs" ]; then
      while IFS=$'\t' read -r rel owner; do
        [ -z "$owner" ] && continue
        i=$((i + 1))
        (
          cd "$(_bridge_base_for_rel "$rel")/$rel" 2>/dev/null || exit 0
          command -v direnv >/dev/null && eval "$(direnv export bash 2>/dev/null)"
          gh repo list "$owner" --topic focus --json nameWithOwner,url --limit 50 2>/dev/null \
            | jq -r '.[] | "GH\t\(.nameWithOwner)\t\(.url)"' \
            > "$tmpdir/$i"
        ) &
      done <<< "$pairs"
    fi
    if [ -n "$fj_rel" ]; then
      i=$((i + 1))
      (
        cd "$(_bridge_base_for_rel "$fj_rel")/$fj_rel" 2>/dev/null || exit 0
        command -v direnv >/dev/null && eval "$(direnv export bash 2>/dev/null)"
        if [ -z "${FORGEJO_TOKEN:-}" ]; then
          printf 'Forgejo: skipped (no FORGEJO_TOKEN)\n' > "$tmpdir/warn_fj"
          exit 0
        fi
        local result
        result=$(curl -sf -H "Authorization: token $FORGEJO_TOKEN" \
          "https://git.home.freaxnx01.ch/api/v1/repos/search?topic=true&q=focus&limit=50" \
          2>/dev/null) || {
            printf 'Forgejo: skipped (curl error)\n' > "$tmpdir/warn_fj"
            exit 0
          }
        printf '%s\n' "$result" \
          | jq -r '.data[] | "FJ\t\(.full_name)\t\(.html_url)"' \
          > "$tmpdir/$i"
      ) &
    fi
    wait
  )

  local repos
  repos=$(cat "$tmpdir"/[0-9]* 2>/dev/null | grep -v '^[[:space:]]*$' | sort -u)

  local warn_list=()
  [ -f "$tmpdir/warn_fj" ] && warn_list+=("$(cat "$tmpdir/warn_fj")")

  if [ -z "$repos" ]; then
    echo "bridge: no focus repos found." >&2
    echo "       Tag a repo via 'bridge --focus-add <name>' or set the 'focus' topic in the platform UI." >&2
    return 0
  fi

  # --- Phase 2: resolve current users (once each) ---
  local gh_user="" fj_user=""
  if [ -n "$first_rel" ]; then
    gh_user=$(
      cd "$(_bridge_base_for_rel "$first_rel")/$first_rel" 2>/dev/null || exit 0
      command -v direnv >/dev/null && eval "$(direnv export bash 2>/dev/null)"
      gh api user --jq .login 2>/dev/null || true
    )
  fi
  if [ -n "$fj_rel" ]; then
    fj_user=$(
      cd "$(_bridge_base_for_rel "$fj_rel")/$fj_rel" 2>/dev/null || exit 0
      command -v direnv >/dev/null && eval "$(direnv export bash 2>/dev/null)"
      [ -z "${FORGEJO_TOKEN:-}" ] && exit 0
      curl -sf -H "Authorization: token $FORGEJO_TOKEN" \
        "https://git.home.freaxnx01.ch/api/v1/user" 2>/dev/null \
        | jq -r '.login // empty' || true
    )
  fi

  # --- Phase 3: per-repo issue counts (parallel) ---
  (
    local count_idx=0
    while IFS=$'\t' read -r platform nwo url; do
      [ -z "$platform" ] && continue
      count_idx=$((count_idx + 1))
      (
        case "$platform" in
          GH)
            if [ -n "$first_rel" ]; then
              cd "$(_bridge_base_for_rel "$first_rel")/$first_rel" 2>/dev/null || exit 0
              command -v direnv >/dev/null && eval "$(direnv export bash 2>/dev/null)"
            fi
            local r
            r=$(gh issue list --repo "$nwo" --state open \
              --json number,assignees --limit 100 2>/dev/null) \
              || { printf '%s\n' "-1 -1"; exit 0; }
            printf '%s\n' "$r" | jq -r --arg me "$gh_user" '
              (length | tostring) + " " +
              ([.[] | select(any(.assignees[]; .login == $me))] | length | tostring)
            ' 2>/dev/null || printf '%s\n' "-1 -1"
            ;;
          FJ)
            if [ -n "$fj_rel" ]; then
              cd "$(_bridge_base_for_rel "$fj_rel")/$fj_rel" 2>/dev/null || exit 0
              command -v direnv >/dev/null && eval "$(direnv export bash 2>/dev/null)"
            fi
            [ -z "${FORGEJO_TOKEN:-}" ] && { printf '%s\n' "-1 -1"; exit 0; }
            local name="${nwo#*/}"
            local r
            r=$(curl -sf -H "Authorization: token $FORGEJO_TOKEN" \
              "https://git.home.freaxnx01.ch/api/v1/repos/freax/$name/issues?state=open&type=issues&limit=50" \
              2>/dev/null) || { printf '%s\n' "-1 -1"; exit 0; }
            printf '%s\n' "$r" | jq -r --arg me "$fj_user" '
              (length | tostring) + " " +
              ([.[] | select(.assignee.login == $me)] | length | tostring)
            ' 2>/dev/null || printf '%s\n' "-1 -1"
            ;;
        esac
      ) > "$tmpdir/count_$count_idx" &
    done <<< "$repos"
    wait
  )

  # --- Phase 4: assemble cache JSON ---
  local json_repos=() count_idx=0
  while IFS=$'\t' read -r platform nwo url; do
    [ -z "$platform" ] && continue
    count_idx=$((count_idx + 1))
    local open=-1 mine=-1
    [ -f "$tmpdir/count_$count_idx" ] && read -r open mine < "$tmpdir/count_$count_idx" || true
    json_repos+=("$(jq -n \
      --arg p "$platform" --arg n "$nwo" --arg u "$url" \
      --argjson o "${open:--1}" --argjson m "${mine:--1}" \
      '{"platform":$p,"name":$n,"url":$u,"open":$o,"mine":$m}')")
  done <<< "$repos"

  local json_warnings_arr="[]"
  for w in "${warn_list[@]}"; do
    json_warnings_arr=$(printf '%s' "$json_warnings_arr" \
      | jq --arg w "$w" '. + [$w]')
  done

  local repos_json
  repos_json=$(printf '%s\n' "${json_repos[@]}" | jq -s '.')

  local cache_tmp="$_BRIDGE_FOCUS_CACHE.tmp"
  jq -n \
    --argjson repos "$repos_json" \
    --argjson warnings "$json_warnings_arr" \
    --argjson ts "$(date +%s)" \
    '{"fetched_at":$ts,"repos":$repos,"warnings":$warnings}' \
    > "$cache_tmp" 2>/dev/null \
    && mv -f "$cache_tmp" "$_BRIDGE_FOCUS_CACHE"

  _bridge_focus_display_cache
}

# Pick a live tmux-backed session via fzf and reattach. Reads slots.json
# (same source as --status), filters to records with a non-empty `session`
# field. 0 live → error, 1 live → auto-attach (no picker), 2+ → fzf.
# Foreground-mode records (no `session` field) are not attachable and are
# excluded. Standalone — no other flags, no positional args (validated by
# the caller in bridge()).
_bridge_attach_pick() {
  _bridge_slots_init
  _bridge_reconcile_slots

  # Same tmux-tagged enumeration as _bridge_slot_status.
  local tmux_rows
  tmux_rows=$(tmux list-sessions -F \
    '#{session_name}	#{session_created}	#{@bridge-repo}	#{@bridge-worktree}	#{@bridge-kind}	#{@bridge-slot}	#{@bridge-pid}' \
    2>/dev/null)

  # Emit one TSV row per live, attachable session (slot or tmux-tagged):
  #   <label>\t<repo_disp>\t<kind>\t<age>\t<session>
  local rows
  rows=$(python3 -c "
import json, time

with open('$_BRIDGE_SLOTS_FILE') as f: d = json.load(f)
slots = d.get('slots', {})
tmux_rows_raw = '''$tmux_rows'''
now = int(time.time())

def fmt_age(sa):
    if not sa: return '—'
    age = now - int(sa)
    h, m = divmod(age // 60, 60)
    return f'{h}h{m:02d}m'

out = []
slot_sessions = set()

for k in sorted(slots.keys(), key=lambda s: int(s) if s.isdigit() else 999):
    v = slots.get(k)
    if not v: continue
    sess = v.get('session') or ''
    if not sess: continue
    slot_sessions.add(sess)
    repo = v.get('repo', '')
    wt = v.get('worktree') or ''
    repo_disp = f'{repo} [{wt}]' if wt else repo
    out.append('\t'.join([f's{k}', repo_disp, 'slot', fmt_age(v.get('started_at', 0)), sess]))

for line in tmux_rows_raw.strip().split('\n'):
    if not line: continue
    parts = line.split('\t')
    if len(parts) < 7: continue
    name, created, repo, wt, kind, slot, pid = parts[:7]
    if not repo: continue
    if name in slot_sessions: continue
    repo_disp = f'{repo} [{wt}]' if wt else repo
    try: created_i = int(created)
    except ValueError: created_i = 0
    out.append('\t'.join(['—', repo_disp, kind or '—', fmt_age(created_i), name]))

print('\n'.join(out))
" 2>/dev/null)

  local count=0
  [ -n "$rows" ] && count=$(printf '%s\n' "$rows" | grep -c .)

  if [ "$count" = 0 ]; then
    echo "bridge: no live sessions" >&2
    return 1
  fi

  local session
  if [ "$count" = 1 ]; then
    session=$(printf '%s' "$rows" | awk -F'\t' '{print $5; exit}')
  else
    local out
    out=$(printf '%s\n' "$rows" \
      | awk -F'\t' 'BEGIN{OFS=""} {
            printf "%-5s %-32s %-12s %-8s\t%s\n", $1, $2, $3, $4, $5
          }' \
      | fzf --height=40% --reverse --prompt='session> ' \
            -d $'\t' --with-nth=1) || return
    session=$(printf '%s' "$out" | awk -F'\t' '{print $2}')
  fi

  [ -z "$session" ] && return
  tmux attach-session -t "$session"
}

# Interactive picker over the unified `--status` overview. Same row sources
# as _bridge_slot_status (slot records + tmux-tagged sessions), filtered to
# occupied sessions, presented in fzf. Selection dispatches by transport:
#   - tmux session present  → tmux attach-session -t <session>
#   - else bridgeSessionId  → print https://claude.ai/code/<id> (and copy
#                             to clipboard if xclip/wl-copy is available)
#   - neither               → row is shown with a ✗ marker but selecting it
#                             prints a "not attachable" error
# Standalone — no other flags, no positional args (validated by caller).
_bridge_status_pick() {
  _bridge_slots_init
  _bridge_reconcile_slots

  local tmux_rows
  tmux_rows=$(tmux list-sessions -F \
    '#{session_name}	#{session_created}	#{@bridge-repo}	#{@bridge-worktree}	#{@bridge-kind}	#{@bridge-slot}	#{@bridge-pid}' \
    2>/dev/null)

  # Emit one TSV row per occupied session. Fields (tab-separated):
  #   1: pre-formatted display line (with leading ✗ for non-attachable)
  #   2: action_type — one of: tmux, rc, none
  #   3: action_target — session name (tmux) or bridge id (rc) or empty
  local rows
  rows=$(python3 -c "
import json, os, time

slots_file = '$_BRIDGE_SLOTS_FILE'
MAX = $_BRIDGE_MAX_SLOTS
tmux_rows_raw = '''$tmux_rows'''

with open(slots_file) as f: d = json.load(f)
slots = d.get('slots', {})
now = int(time.time())

def bridge_for(cfg_dir, pid):
    if not pid: return ''
    sess_dir = os.path.join(os.path.expanduser(cfg_dir), 'sessions')
    if not os.path.isdir(sess_dir): return ''
    p = os.path.join(sess_dir, f'{pid}.json')
    if not os.path.isfile(p): return ''
    try:
        with open(p) as fh: sd = json.load(fh)
        return sd.get('bridgeSessionId') or ''
    except Exception:
        return ''

def fmt_age(sa):
    if not sa: return '—'
    age = now - int(sa)
    h, m = divmod(age // 60, 60)
    return f'{h}h{m:02d}m ago'

rows = []
slot_sessions = set()

# --- Slot rows (occupied only) ---
for n in sorted({str(x) for x in range(0, MAX + 1)}, key=int):
    v = slots.get(n)
    if not v: continue
    sess = v.get('session') or ''
    if sess: slot_sessions.add(sess)
    repo = v.get('repo', '')
    wt = v.get('worktree') or ''
    repo_disp = f'{repo} [{wt}]' if wt else repo
    pid = v.get('pid', 0)
    bot = '(admin bot)' if int(n) == 0 else f'@claude_freax_s{n}_bot'
    bridge = bridge_for(f'~/.claude-s{n}', pid)
    rows.append({
        'slot': f's{n}', 'kind': 'slot', 'repo': repo_disp or '—',
        'started': fmt_age(v.get('started_at', 0)),
        'tmux': sess, 'bot': bot, 'bridge': bridge, '_ts': 0,
    })

# --- Synthetic tmux-tagged rows ---
for line in tmux_rows_raw.strip().split('\n'):
    if not line: continue
    parts = line.split('\t')
    if len(parts) < 7: continue
    name, created, repo, wt, kind, slot, pid = parts[:7]
    if not repo: continue
    if name in slot_sessions: continue
    repo_disp = f'{repo} [{wt}]' if wt else repo
    bridge = bridge_for('~/.claude', pid) if kind == 'no-channel' else ''
    try: created_i = int(created)
    except ValueError: created_i = 0
    rows.append({
        'slot': '—', 'kind': kind or '—', 'repo': repo_disp,
        'started': fmt_age(created_i),
        'tmux': name, 'bot': '—', 'bridge': bridge, '_ts': created_i,
    })

# Keep slots in slot order; sort synthetic newest first after.
slot_rows  = [r for r in rows if r['slot'] != '—']
synth_rows = sorted([r for r in rows if r['slot'] == '—'], key=lambda r: -r['_ts'])
rows = slot_rows + synth_rows

for r in rows:
    if r['tmux']:
        atype, atarget = 'tmux', r['tmux']
    elif r['bridge']:
        atype, atarget = 'rc', r['bridge']
    else:
        atype, atarget = 'none', ''
    marker = '✗' if atype == 'none' else ' '
    rc_marker = '✓' if r['bridge'] else '—'
    tmux_disp = r['tmux'] or '—'
    disp = f\"{marker} {r['slot']:<3} {r['kind']:<11} {r['repo']:<28} {r['started']:<13} {tmux_disp:<20} {r['bot']:<28} {rc_marker}\"
    print(f'{disp}\t{atype}\t{atarget}')
" 2>/dev/null)

  if [ -z "$rows" ]; then
    echo "bridge: no live sessions" >&2
    return 1
  fi

  local header
  header=$(printf '  %-3s %-11s %-28s %-13s %-20s %-28s %s' \
    "SLOT" "KIND" "REPO" "STARTED" "TMUX" "BOT" "RC")

  local out
  out=$(printf '%s\n' "$rows" \
    | fzf --height=40% --reverse --prompt='session> ' \
          --header="$header" \
          -d $'\t' --with-nth=1) || return

  local atype atarget
  atype=$(printf '%s' "$out"   | awk -F'\t' '{print $2}')
  atarget=$(printf '%s' "$out" | awk -F'\t' '{print $3}')

  case "$atype" in
    tmux)
      [ -z "$atarget" ] && return
      tmux attach-session -t "$atarget"
      ;;
    rc)
      [ -z "$atarget" ] && return
      local url="https://claude.ai/code/$atarget"
      printf '%s\n' "$url"
      if command -v xclip >/dev/null 2>&1; then
        printf '%s' "$url" | xclip -selection clipboard 2>/dev/null \
          && echo "bridge: copied URL to clipboard" >&2
      elif command -v wl-copy >/dev/null 2>&1; then
        printf '%s' "$url" | wl-copy 2>/dev/null \
          && echo "bridge: copied URL to clipboard" >&2
      fi
      ;;
    none)
      echo "bridge: this session has no tmux and no Remote Control URL — cannot attach" >&2
      return 1
      ;;
    *)
      return 1
      ;;
  esac
}

_bridge_print_last() {
  local f="$_BRIDGE_CACHE/last"
  [ -f "$f" ] || return
  printf 'bridge: path:   %s\n' "$(sed -n '1p' "$f")" >&2
  printf 'bridge: remote: %s\n' "$(sed -n '2p' "$f")" >&2
}

# Derive a stable tmux session name from repo basename + optional worktree.
# Identical for a given (repo, worktree) pair so reattach checks match
# session creates.
_bridge_tmux_session_name() {
  local s="$1"
  [ -n "${2:-}" ] && s="$1-$2"
  printf '%s' "${s//[^A-Za-z0-9_-]/_}"
}

# Apply bridge's tmux session defaults so wheel-scroll works and the
# scrollback is deep enough to review long agent output. Scoped to the
# session (not server-global) to avoid touching the user's other tmux
# sessions. Hold Shift while dragging to bypass tmux's mouse capture and
# fall back to the terminal emulator's native selection/clipboard.
#
# Also tags the session with @bridge-* user-options so `bridge --status`
# can enumerate non-slot tmux sessions (--no-channel, --code, --opencode)
# without a sidecar registry file. The tags are scoped per-session and
# never collide with non-bridge tmux sessions.
#
# Args:
#   $1 session    tmux session name
#   $2 repo       repo basename
#   $3 worktree   worktree name or empty
#   $4 kind       one of: slot, no-channel, code, copilot, opencode
#   $5 slot       slot number for kind=slot; empty otherwise
_bridge_tmux_session_defaults() {
  local session="$1" repo="${2:-}" worktree="${3:-}" kind="${4:-}" slot="${5:-}"
  tmux set-option -t "$session" mouse on >/dev/null 2>&1
  tmux set-option -t "$session" history-limit 50000 >/dev/null 2>&1
  # Tags for --status discovery. @bridge-pid is read once from the pane
  # right after creation so synthetic (non-slot) rows can resolve RC.
  local pid
  pid=$(tmux display-message -t "$session" -p '#{pane_pid}' 2>/dev/null || echo "")
  tmux set-option -t "$session" '@bridge-repo'     "$repo"     >/dev/null 2>&1
  tmux set-option -t "$session" '@bridge-worktree' "$worktree" >/dev/null 2>&1
  tmux set-option -t "$session" '@bridge-kind'     "$kind"     >/dev/null 2>&1
  tmux set-option -t "$session" '@bridge-slot'     "$slot"     >/dev/null 2>&1
  tmux set-option -t "$session" '@bridge-pid'      "$pid"      >/dev/null 2>&1
}

# Render the sync skip note into _BRIDGE_SYNC_NOTE for downstream
# consumption by _bridge_launch (banner + marker file + agent injection).
# Args: $1 = kind (fetch|no-upstream|dirty|diverged), $2.. = kind-specific.
# Side effect: sets the global var _BRIDGE_SYNC_NOTE. Empty kind clears it.
_bridge_sync_set_note() {
  local kind="${1:-}"
  local branch_v="${branch:-?}"
  local upstream_v="${upstream:-?}"
  local details="" suggested=""

  case "$kind" in
    fetch)
      local err="${2:-}" rc="${3:-?}"
      if [ "$rc" = "124" ]; then
        details="git fetch timed out after ${BRIDGE_SYNC_TIMEOUT:-20}s"
      else
        details=$(printf '%s' "$err" | head -n 5)
      fi
      suggested='  - direnv exec . git fetch
  - if auth-related: verify GH_TOKEN/GITLAB_TOKEN/ADO PAT in .envrc
  - then: git pull --ff-only'
      _BRIDGE_SYNC_NOTE="bridge: startup sync was skipped — fetch failed.
Branch: $branch_v  Upstream: $upstream_v
$details
Suggested:
$suggested
Before making changes, please bring the branch in sync."
      ;;
    no-upstream)
      _BRIDGE_SYNC_NOTE="bridge: startup sync was skipped — no upstream.
Branch: $branch_v  Upstream: (none)
Branch $branch_v has no upstream configured.
Suggested:
  - when ready to share: git push -u origin $branch_v
Before making changes, please bring the branch in sync."
      ;;
    dirty)
      local porcelain
      porcelain=$(git status --porcelain 2>/dev/null | head -5)
      _BRIDGE_SYNC_NOTE="bridge: startup sync was skipped — dirty working tree.
Branch: $branch_v  Upstream: $upstream_v
Uncommitted changes (first 5):
$porcelain
Suggested:
  - git status
  - commit or stash before continuing
Before making changes, please bring the branch in sync."
      ;;
    diverged)
      local stats ahead behind
      stats=$(git rev-list --left-right --count 'HEAD...@{u}' 2>/dev/null)
      ahead=$(printf '%s' "$stats" | awk '{print $1}')
      behind=$(printf '%s' "$stats" | awk '{print $2}')
      _BRIDGE_SYNC_NOTE="bridge: startup sync was skipped — diverged from upstream.
Branch: $branch_v  Upstream: $upstream_v
Local ahead by ${ahead:-?}, behind by ${behind:-?}.
Suggested:
  - git log --oneline @{u}..HEAD     # inspect local commits
  - git pull --rebase                # integrate (user judgment)
Before making changes, please bring the branch in sync."
      ;;
    "")
      _BRIDGE_SYNC_NOTE=""
      ;;
    *)
      _BRIDGE_SYNC_NOTE=""
      ;;
  esac
}

# Fast-forward sync of the current branch with its upstream before launch.
# Args: $1 = repo basename, $2 = optional worktree name.
# Never fails the launch; every error path returns 0 after a stderr line.
_bridge_sync() {
  local repo="$1" worktree="${2:-}"
  _BRIDGE_SYNC_NOTE=""
  [ "${_BRIDGE_NO_SYNC:-0}" = 1 ] && return 0

  # Skip if we're about to reattach an existing tmux session.
  if [ -n "${SSH_CONNECTION:-}" ] && command -v tmux >/dev/null; then
    local session
    session=$(_bridge_tmux_session_name "$repo" "$worktree")
    tmux has-session -t "$session" 2>/dev/null && return 0
  fi

  local branch upstream
  branch=$(git symbolic-ref --quiet --short HEAD) || {
    _bridge_warn "detached HEAD, skipping sync"; return 0; }
  upstream=$(git rev-parse --abbrev-ref --symbolic-full-name '@{u}' 2>/dev/null) || {
    _bridge_sync_set_note no-upstream
    _bridge_warn "no upstream for $branch, skipping sync"; return 0; }
  if ! git diff --quiet || ! git diff --cached --quiet; then
    _bridge_sync_set_note dirty
    _bridge_warn "dirty working tree, skipping sync"; return 0
  fi

  local log="$_BRIDGE_CACHE/sync.log"
  mkdir -p "$_BRIDGE_CACHE"
  if [ -f "$log" ] && [ "$(wc -l < "$log")" -gt 400 ]; then
    tail -n 200 "$log" > "$log.tmp" && mv "$log.tmp" "$log"
  fi

  local fetch_err fetch_rc
  fetch_err=$(timeout "${BRIDGE_SYNC_TIMEOUT:-20}" git fetch 2>&1)
  fetch_rc=$?
  if [ "$fetch_rc" -ne 0 ]; then
    printf '[%s] %s on %s (rc=%d): %s\n' \
      "$(date -Iseconds)" "$repo" "$branch" "$fetch_rc" \
      "$(printf '%s' "$fetch_err" | tr '\n' ' ' | head -c 500)" >> "$log"
    _bridge_sync_set_note fetch "$fetch_err" "$fetch_rc"
    _bridge_warn "fetch failed (rc=$fetch_rc), see $log"
    return 0
  fi

  local local_sha upstream_sha base
  local_sha=$(git rev-parse HEAD)
  upstream_sha=$(git rev-parse '@{u}')
  [ "$local_sha" = "$upstream_sha" ] && return 0

  base=$(git merge-base HEAD '@{u}')
  if [ "$base" = "$upstream_sha" ]; then
    return 0  # local is ahead of upstream — fine, nothing to pull
  elif [ "$base" = "$local_sha" ]; then
    git merge --ff-only --quiet '@{u}' || {
      _bridge_warn "ff-only merge failed unexpectedly, skipping sync"; return 0; }
    printf 'bridge: pulled %s..%s on %s\n' \
      "$(git rev-parse --short "$local_sha")" \
      "$(git rev-parse --short "$upstream_sha")" "$branch" >&2
  else
    _bridge_sync_set_note diverged
    _bridge_warn "$branch diverged from $upstream, skipping sync"
  fi
}

_bridge_launch() {
  local sel="$1"
  local worktree="${2:-}"
  local editor="${3:-}"
  local remote_control="${4:-1}"
  local mru="$_BRIDGE_CACHE/mru"
  local base
  base=$(_bridge_base_for_rel "$sel")
  cd "$base/$sel" || return
  _bridge_sync "$(basename "$sel")" "$worktree"
  if [ -n "${_BRIDGE_SYNC_NOTE:-}" ]; then
    _bridge_sync_banner
    _bridge_sync_write_marker
  fi
  { printf '%s
' "$sel"; grep -vxF "$sel" "$mru" 2>/dev/null; } | head -10 > "$mru.tmp" && mv "$mru.tmp" "$mru"

  local repo display_name
  repo=$(basename "$sel")
  # Distinguish worktree sessions in `-n` so the prompt box, terminal title,
  # and /resume picker can tell `repo` and `repo -w doc` apart. Matches the
  # Telegram bot title format set in _bridge_telegram_setup.
  display_name="$repo"
  [ -n "$worktree" ] && display_name="$repo [$worktree]"

  local _remote_url _session_path
  _remote_url=$(git remote get-url origin 2>/dev/null || echo '(no remote)')
  # `claude --worktree NAME` runs in <repo>/.claude/worktrees/<NAME>/, so
  # record that path (not the main repo) for `path:` and downstream readers.
  # Copilot mode rewrites this further down after cd'ing into the git worktree.
  _session_path="$PWD"
  if [ -n "$worktree" ] && [ -z "$editor" ]; then
    _session_path="$PWD/.claude/worktrees/$worktree"
  fi
  printf '%s\n%s\n' "$_session_path" "$_remote_url" > "$_BRIDGE_CACHE/last"

  # VS Code mode — open directory, skip slot/Telegram/tmux entirely
  if [ "$editor" = "code" ]; then
    code .
    _bridge_print_last
    return
  fi

  # Copilot mode — run `copilot --yolo`. Honors -w by cd'ing into the matching
  # git worktree (lookup by basename in `git worktree list`). Skips slot/Telegram
  # but keeps the tmux SSH wrap so disconnects don't kill the session.
  if [ "$editor" = "copilot" ]; then
    if [ -n "$worktree" ]; then
      local wt_path=""
      while IFS= read -r p; do
        [ "$(basename "$p")" = "$worktree" ] && { wt_path="$p"; break; }
      done < <(git worktree list --porcelain 2>/dev/null | awk '/^worktree / {print $2}')
      if [ -z "$wt_path" ]; then
        echo "bridge: no worktree named '$worktree' under $sel" >&2
        return 1
      fi
      cd "$wt_path" || return 1
      printf '%s\n%s\n' "$PWD" "$_remote_url" > "$_BRIDGE_CACHE/last"
    fi
    if [ -n "${SSH_CONNECTION:-}" ] && command -v tmux >/dev/null; then
      local session
      session=$(_bridge_tmux_session_name "$repo" "$worktree")
      if ! tmux has-session -t "$session" 2>/dev/null; then
        tmux new-session -d -s "$session" copilot --yolo
        _bridge_tmux_session_defaults "$session" "$repo" "$worktree" copilot ""
      fi
      tmux attach-session -t "$session"
    else
      copilot --yolo
    fi
    _bridge_print_last
    return
  fi

  # --cd mode — pure navigation. Honors -w by cd'ing into the matching
  # git worktree (lookup by basename in `git worktree list`). Skips
  # slot/Telegram/tmux/agent entirely — leaves the shell in the resolved
  # directory and returns. Sync/MRU/`last` are unaffected (handled above).
  if [ "$editor" = "cd" ]; then
    if [ -n "$worktree" ]; then
      local wt_path=""
      while IFS= read -r p; do
        [ "$(basename "$p")" = "$worktree" ] && { wt_path="$p"; break; }
      done < <(git worktree list --porcelain 2>/dev/null | awk '/^worktree / {print $2}')
      if [ -z "$wt_path" ]; then
        echo "bridge: no worktree named '$worktree' under $sel" >&2
        return 1
      fi
      cd "$wt_path" || return 1
      printf '%s\n%s\n' "$PWD" "$_remote_url" > "$_BRIDGE_CACHE/last"
    fi
    _bridge_print_last
    return
  fi

  # OpenCode mode — run `opencode`. Honors -w by cd'ing into the matching
  # git worktree (lookup by basename in `git worktree list`). Skips slot/Telegram
  # but keeps the tmux SSH wrap so disconnects don't kill the session.
  if [ "$editor" = "opencode" ]; then
    if [ -n "$worktree" ]; then
      local wt_path=""
      while IFS= read -r p; do
        [ "$(basename "$p")" = "$worktree" ] && { wt_path="$p"; break; }
      done < <(git worktree list --porcelain 2>/dev/null | awk '/^worktree / {print $2}')
      if [ -z "$wt_path" ]; then
        echo "bridge: no worktree named '$worktree' under $sel" >&2
        return 1
      fi
      cd "$wt_path" || return 1
      printf '%s\n%s\n' "$PWD" "$_remote_url" > "$_BRIDGE_CACHE/last"
    fi
    if [ -n "${SSH_CONNECTION:-}" ] && command -v tmux >/dev/null; then
      local session
      session=$(_bridge_tmux_session_name "$repo" "$worktree")
      if ! tmux has-session -t "$session" 2>/dev/null; then
        tmux new-session -d -s "$session" opencode
        _bridge_tmux_session_defaults "$session" "$repo" "$worktree" opencode ""
      fi
      tmux attach-session -t "$session"
    else
      opencode
    fi
    _bridge_print_last
    return
  fi

  # --- Slot allocation (skip only with explicit --no-channel) ---
  if [ "${_BRIDGE_NO_CHANNEL:-0}" = 1 ]; then
    # User opted out: no slot, no Telegram, shared CLAUDE_CONFIG_DIR (~/.claude).
    echo "bridge: --no-channel set: no slot, no Telegram, shared ~/.claude." >&2
    local -a claude_args=(-n "$display_name" --dangerously-skip-permissions)
    [ -n "$worktree" ] && claude_args+=(--worktree "$worktree")
    [ "$remote_control" = 1 ] && claude_args+=(--remote-control)
    [ -n "${_BRIDGE_SYNC_NOTE:-}" ] && claude_args+=(--append-system-prompt "$_BRIDGE_SYNC_NOTE")
    if [ -n "${SSH_CONNECTION:-}" ] && command -v tmux >/dev/null; then
      local session
      session=$(_bridge_tmux_session_name "$repo" "$worktree")
      if ! tmux has-session -t "$session" 2>/dev/null; then
        tmux new-session -d -s "$session" claude "${claude_args[@]}"
        _bridge_tmux_session_defaults "$session" "$repo" "$worktree" no-channel ""
      fi
      tmux attach-session -t "$session"
    else
      claude "${claude_args[@]}"
    fi
    _bridge_print_last
    return
  fi

  # Allocate a slot (auto-inits slots.json on first use)
  _bridge_slot_allocate "${_BRIDGE_FORCED_SLOT:-}" || return

  local -a claude_args=(-n "$display_name" --dangerously-skip-permissions --channels plugin:telegram@claude-plugins-official)
  [ -n "$worktree" ] && claude_args+=(--worktree "$worktree")
  [ "$remote_control" = 1 ] && claude_args+=(--remote-control)
  [ -n "${_BRIDGE_SYNC_NOTE:-}" ] && claude_args+=(--append-system-prompt "$_BRIDGE_SYNC_NOTE")

  export CLAUDE_CONFIG_DIR="$HOME/.claude-s${_SLOT}"
  export TELEGRAM_BOT_TOKEN="$_SLOT_TOKEN"

  # Persist the display name so the SessionStart-clear hook can reapply
  # it via `/rename` after `/clear` wipes the title (issue #20).
  mkdir -p "$CLAUDE_CONFIG_DIR" 2>/dev/null
  printf '%s\n' "$display_name" > "$CLAUDE_CONFIG_DIR/bridge-label" 2>/dev/null

  echo "bridge: using slot s${_SLOT} (CLAUDE_CONFIG_DIR=$CLAUDE_CONFIG_DIR)" >&2
  _bridge_slot_creds_check "$_SLOT"

  if [ -n "${SSH_CONNECTION:-}" ] && command -v tmux >/dev/null; then
    local session
    session=$(_bridge_tmux_session_name "$repo" "$worktree")

    # Reattach if tmux session already exists (no new slot needed)
    if tmux has-session -t "$session" 2>/dev/null; then
      echo "bridge: reattaching to tmux session '$session' (slot stays as-is)" >&2
      _bridge_slot_free "$_SLOT"
      tmux attach-session -t "$session"
      return
    fi

    # New tmux session
    _bridge_telegram_setup "$_SLOT" "$repo" "$worktree" "$_SLOT_TOKEN"
    tmux new-session -d -s "$session"       -e "CLAUDE_CONFIG_DIR=$CLAUDE_CONFIG_DIR"       -e "TELEGRAM_BOT_TOKEN=$TELEGRAM_BOT_TOKEN"       claude "${claude_args[@]}"
    _bridge_tmux_session_defaults "$session" "$repo" "$worktree" slot "$_SLOT"
    # Keep the pane visible on non-zero exit so the user actually sees claude's
    # startup error on attach instead of just `[exited]`. Auto-close on exit 0
    # so the success path stays clean (no dangling pane to dismiss).
    tmux set-option -t "$session" remain-on-exit on
    tmux set-hook   -t "$session" pane-died       "run-shell '$_BRIDGE_DIR/bridge-unpushed-warn.sh $session'; if-shell -F '#{==:#{pane_dead_status},0}' 'kill-pane'"
    # Record repo path so the session-closed hook can find it for autosync.
    mkdir -p "$_BRIDGE_CACHE/sessions"
    printf '%s\n' "$PWD" > "$_BRIDGE_CACHE/sessions/${session}.path"
    tmux set-hook -t "$session" session-closed "run-shell '$_BRIDGE_DIR/bridge-autosync.sh $session $_SLOT_TOKEN; $HOME/.cache/bridge/cleanup.sh $_SLOT $_SLOT_TOKEN'"

    local pid
    pid=$(tmux display-message -t "$session" -p '#{pane_pid}' 2>/dev/null || echo 0)
    _bridge_slot_record "$_SLOT" "$repo" "$worktree" "$pid" "$session"
    tmux attach-session -t "$session"

    # Failure path: claude exited non-zero, pane stayed (via remain-on-exit)
    # so the user could read the error. After they detach, reap the lingering
    # session, tell them bridge registered the failure, and skip print_last
    # (the path/remote on disk is for a session that never really started).
    if tmux has-session -t "$session" 2>/dev/null; then
      local _live
      _live=$(tmux list-panes -t "$session" -F '#{pane_dead}' 2>/dev/null | grep -c '^0$')
      if [ "$_live" = "0" ]; then
        tmux kill-session -t "$session" 2>/dev/null
        echo "bridge: claude exited unexpectedly — see error above" >&2
        return 1
      fi
    fi

    _bridge_print_last
    # On detach: slot stays allocated (claude is still running in tmux).
    # PID reconciliation will free it when claude actually exits.
  else
    # Foreground mode — cleanup on exit
    _bridge_telegram_setup "$_SLOT" "$repo" "$worktree" "$_SLOT_TOKEN"
    _bridge_slot_record "$_SLOT" "$repo" "$worktree" "$$"
    claude "${claude_args[@]}"
    command -v _bridge_warn_unpushed >/dev/null && _bridge_warn_unpushed "$PWD"
    command -v _bridge_autosync >/dev/null && _bridge_autosync "$PWD" "$_SLOT_TOKEN"
    _bridge_slot_free "$_SLOT"
    _bridge_telegram_cleanup "$_SLOT" "$_SLOT_TOKEN"
    _bridge_print_last
  fi
}

# Return 0 if $1 is a strictly higher semver than $2 (using `sort -V`).
_bridge_version_gt() {
  [ "$1" = "$2" ] && return 1
  local higher
  higher=$(printf '%s\n%s\n' "$1" "$2" | sort -V | tail -1)
  [ "$higher" = "$1" ]
}

# Hint if a newer _BRIDGE_VERSION is available. Local-first: check the
# on-disk bridge.sh that this shell was sourced from (kept current with
# origin by _bridge_autosync). Fall back to a TTL-gated remote curl only
# when the on-disk path can't be resolved or read.
_bridge_check_latest() {
  local script="${BASH_SOURCE[0]}"
  if command -v readlink >/dev/null 2>&1; then
    script=$(readlink -f "$script" 2>/dev/null || echo "$script")
  fi

  if [ -r "$script" ]; then
    local on_disk
    on_disk=$(grep -m1 '^_BRIDGE_VERSION=' "$script" 2>/dev/null \
              | sed -E 's/^_BRIDGE_VERSION="?([^"]+)"?.*/\1/')
    if [ -n "$on_disk" ]; then
      if _bridge_version_gt "$on_disk" "$_BRIDGE_VERSION"; then
        echo "bridge: new version $on_disk available (you have $_BRIDGE_VERSION) — run \`bridge update\`" >&2
      fi
      return 0
    fi
  fi

  # Fallback: on-disk path missing/unreadable/malformed. Use the cached
  # remote check (background-refresh, mtime-gated by TTL).
  local cache="$_BRIDGE_CACHE/latest-version"
  local age
  age=$(( $(date +%s) - $(stat -c %Y "$cache" 2>/dev/null || echo 0) ))
  if [ ! -f "$cache" ] || [ "$age" -gt "$_BRIDGE_UPDATE_TTL" ]; then
    (
      flock -n 9 || exit 0
      local v
      v=$(curl -fsSL --max-time 5 "$_BRIDGE_RAW_URL" 2>/dev/null \
            | grep -m1 '^_BRIDGE_VERSION=' \
            | sed -E 's/^_BRIDGE_VERSION="?([^"]+)"?.*/\1/')
      [ -n "$v" ] && printf '%s\n' "$v" > "$cache"
    ) 9>"$_BRIDGE_CACHE/latest-warm.lock" </dev/null >/dev/null 2>&1 &
    disown 2>/dev/null || true
  fi
  [ -f "$cache" ] || return 0
  local latest
  latest=$(cat "$cache" 2>/dev/null)
  [ -z "$latest" ] && return 0
  if _bridge_version_gt "$latest" "$_BRIDGE_VERSION"; then
    echo "bridge: new version $latest available (you have $_BRIDGE_VERSION) — run \`bridge update\`" >&2
  fi
}

# Pull the config repo that hosts bridge.sh, then re-source the script
# in the calling shell so the new function bodies take effect immediately.
_bridge_update() {
  local script="${BASH_SOURCE[0]}"
  if command -v readlink >/dev/null 2>&1; then
    script=$(readlink -f "$script" 2>/dev/null || echo "$script")
  fi
  local root
  root=$(git -C "$(dirname "$script")" rev-parse --show-toplevel 2>/dev/null) || {
    echo "bridge: cannot locate config repo (script: $script)" >&2
    return 1
  }
  echo "bridge: pulling $root"
  local old_ver="$_BRIDGE_VERSION"
  if ! git -C "$root" pull --ff-only; then
    echo "bridge: git pull failed (resolve manually in $root)" >&2
    return 1
  fi
  # Disable alias expansion during re-source: an interactive shell may have
  # `alias bridge='bridge ...'`, which bash would expand inline at parse time
  # and break the `bridge() {` definition.
  local _ea=0
  shopt -q expand_aliases && _ea=1
  shopt -u expand_aliases
  # shellcheck disable=SC1090
  . "$script"
  [ "$_ea" = 1 ] && shopt -s expand_aliases
  printf '%s\n' "$_BRIDGE_VERSION" > "$_BRIDGE_CACHE/latest-version" 2>/dev/null
  if [ "$old_ver" = "$_BRIDGE_VERSION" ]; then
    echo "bridge: already at $_BRIDGE_VERSION"
  else
    echo "bridge: updated $old_ver → $_BRIDGE_VERSION"
  fi
}

bridge() {
  local with_remote=0 force_refresh=0 mode_delete=0 worktree="" editor="" remote_control=1 _BRIDGE_NO_CHANNEL=0 _BRIDGE_FORCED_SLOT="" _BRIDGE_NO_SYNC=0 mode_attach=0 mode_pick=0 mode_repo_issues=0 mode_focus=0 focus_no_cache=0
  local -a pos=()

  # Shadow the global base-dir state so any in-function mutation (notably
  # -B/--base via _bridge_collect_bases_with) dies when bridge() returns.
  # Bash dynamic scoping makes helpers called from here hit the local
  # binding, not the global — so the "for this invocation only" contract
  # on -B is enforced without explicit save/restore.
  local -a _BRIDGE_BASES=("${_BRIDGE_BASES[@]}")
  local _BRIDGE_BASE="$_BRIDGE_BASE"

  # Pre-pass for -B/--base so the override applies even to flags that early-
  # return inside the main dispatch loop (e.g. --status, --pick, --issues,
  # --doctor, --worktree-status, -V). The flag value can be `:`-separated
  # like BRIDGE_BASE itself.
  local _override_base=""
  local -a _passthrough=()
  while [ $# -gt 0 ]; do
    case "$1" in
      -B|--base)
        if [ -z "${2:-}" ]; then
          echo "bridge: $1 requires a directory path" >&2; return 2
        fi
        _override_base="$2"; shift 2 ;;
      *) _passthrough+=("$1"); shift ;;
    esac
  done
  set -- "${_passthrough[@]}"
  [ -n "$_override_base" ] && _bridge_collect_bases_with "$_override_base"

  while [ $# -gt 0 ]; do
    case "$1" in
      -r|--remote)    with_remote=1; shift ;;
      --refresh)      with_remote=1; force_refresh=1; shift ;;
      --no-channel)   _BRIDGE_NO_CHANNEL=1; shift ;;
      --no-sync)      _BRIDGE_NO_SYNC=1; shift ;;
      --slot)
        [ -z "${2:-}" ] && { echo "bridge: $1 requires a slot number" >&2; return 2; }
        _BRIDGE_FORCED_SLOT="$2"; shift 2 ;;
      -a|--attach)    mode_attach=1; shift ;;
      --pick|--connect) mode_pick=1; shift ;;
      --status)       _bridge_slot_status; return ;;
      --status-rc)    _bridge_slot_status_rc; return ;;
      --doctor)       _bridge_doctor; return ;;
      --worktree-status|--ws) _bridge_worktree_status; return ;;
      --issues)       _bridge_issues; return ;;
      --dashboard)    _bridge_dashboard; return ;;
      -i|--repo-issues) mode_repo_issues=1; shift ;;
      -f|--focus-list) mode_focus=1; shift ;;
      --no-cache)      focus_no_cache=1; shift ;;
      --focus-add)
        [ -z "${2:-}" ] && { echo "bridge: $1 requires <name>" >&2; return 2; }
        _bridge_focus_add "$2"; return ;;
      --focus-rm)
        [ -z "${2:-}" ] && { echo "bridge: $1 requires <name>" >&2; return 2; }
        _bridge_focus_rm "$2"; return ;;
      --setup-admin)
        [ -z "${2:-}" ] && { echo "bridge: $1 requires a label" >&2; return 2; }
        _bridge_setup_admin "$2"; return ;;
      --install-admin-commands) _bridge_install_admin_commands; return ;;
      --free)
        [ -z "${2:-}" ] && { echo "bridge: $1 requires a slot number" >&2; return 2; }
        _bridge_slot_free "$2"; echo "bridge: slot $2 freed"; return ;;
      -D|--delete)    mode_delete=1; shift ;;
      -c|--code)      [ "$editor" = "cd" ] && { echo "bridge: --cd and -c/-p/-o are mutually exclusive" >&2; return 2; }; editor=code; shift ;;
      -p|--copilot)   [ "$editor" = "cd" ] && { echo "bridge: --cd and -c/-p/-o are mutually exclusive" >&2; return 2; }; editor=copilot; shift ;;
      -o|--opencode)  [ "$editor" = "cd" ] && { echo "bridge: --cd and -c/-p/-o are mutually exclusive" >&2; return 2; }; editor=opencode; shift ;;
      --cd)           [ -n "$editor" ] && { echo "bridge: --cd and -c/-p/-o are mutually exclusive" >&2; return 2; }; editor=cd; shift ;;
      --remote-control|--rc) remote_control=1; shift ;;
      --no-remote-control|--no-rc) remote_control=0; shift ;;
      -w|--worktree)
        [ -z "${2:-}" ] && { echo "bridge: $1 requires a worktree name" >&2; return 2; }
        worktree="$2"; shift 2 ;;
      -V|--version)
        echo "bridge $_BRIDGE_VERSION"; return 0 ;;
      -h|--help)
        cat <<'EOF'
Usage: bridge [options] [repo-name|.|update|away|back|here|presence]
  (no args)             launch current repo if CWD is under $BRIDGE_BASE, else picker
  .                     launch current repo (errors if CWD is not inside a known repo)
  update                git pull the config repo hosting bridge.sh and re-source it
  away                  set presence to "away" (Telegram pages enabled for all slots)
  back                  resume auto-detection (per-slot tmux client check)
  here                  set presence to "here" (Telegram pages disabled for all slots)
  presence              show current presence mode and per-slot effective state
  -r, --remote          also list uncloned remote repos from discovered forge targets
      --refresh         force refresh of remote cache (implies -r)
  -D, --delete          delete a repo (local and/or remote); with <name> or via picker
  -c, --code            open repo in VS Code instead of Claude Code CLI
  -p, --copilot         launch `copilot --yolo` instead of Claude Code CLI
  -o, --opencode        launch `opencode` instead of Claude Code CLI
      --cd              just cd into the repo dir; don't launch any agent
      --remote-control, --rc
                        pass `--remote-control` to claude (steer session from
                        claude.ai/code or mobile app); on by default, requires
                        claude.ai OAuth login. Ignored with -c, -p, -o, --cd.
      --no-remote-control, --no-rc
                        opt out of `--remote-control` for this launch.
  -w, --worktree NAME   pass through to `claude --worktree NAME`
                        with -p, -o, or --cd: cd into the matching git worktree first
  -V, --version         print version and exit
  -B, --base <dir>      override the base dir(s) for this invocation only
                        (highest precedence, above env var and config file).
                        Accepts a `:`-separated list like BRIDGE_BASE.
  --slot N              force a specific slot (1..N)
  --no-channel          legacy mode, no slot allocation, no Telegram
  --no-sync             skip the upstream fast-forward pull on startup
  -a, --attach          fzf picker over live sessions; reattach to selection
      --pick, --connect fzf picker over the full --status overview; attach
                        to tmux row, or print/copy URL for an RC-only row
  --status              show session status table (slot + non-slot tmux + RC URLs)
  --doctor              diagnose forge targets (direnv, tokens, API access)
  --worktree-status, --ws
                        show git status per local repo (branch, dirty,
                        ahead, extra worktrees)
  --issues              list open issues across GitHub + Forgejo forges
  --dashboard           cross-repo table: open-issue count + top 2 titles
                        per local GitHub repo under $_BRIDGE_BASE
  -i, --repo-issues [name]
                        list open GitHub issues for one repo via `gh issue
                        list`; with no name, uses the repo at $PWD
  -f [name]             list focus repos (all platforms) or open <name>.
      --focus-list      explicit form of -f with no <name>.
      --no-cache        force refresh, bypassing the 1-hour cache (only
                        meaningful with -f).
  --focus-add <name>    tag repo with the 'focus' topic on its platform
  --focus-rm <name>     remove the 'focus' topic
  --setup-admin LABEL   wire slot 0 (admin) for label-restore hook
  --install-admin-commands
                        symlink admin slash commands into ~/.claude-s0/commands/
  --free N              force-free slot N (escape hatch)
In picker:
  Enter   launch (cloning first if remote)
  Ctrl-N  create new remote repo (current query becomes seed name)
  Ctrl-D  delete highlighted repo (local and/or remote)
  Ctrl-R  refresh remote cache (only with -r)
SSH persistence: when $SSH_CONNECTION is set, the Claude session is wrapped
in `tmux new-session -A` so disconnecting doesn't kill it. Re-run the same
bridge command to reattach.
Base dir(s) (where bridge scans for repos), in precedence order:
  1. -B/--base <dir> flag — overrides for one invocation; `:`-separated OK
  2. $BRIDGE_BASE env var — `:`-separated list (PATH-style); empty = unset
  3. $HOME/.config/bridge/base file — one absolute path per line; `~`/`$HOME`
     expanded; `#` lines ignored
  4. Default: $HOME/projects/repos
Sources are not merged: whichever wins, wins as a whole list. Missing dirs
are warned-and-skipped. Single-base setups behave identically to before.
EOF
        return 0 ;;
      --) shift; while [ $# -gt 0 ]; do pos+=("$1"); shift; done ;;
      *) pos+=("$1"); shift ;;
    esac
  done
  set -- "${pos[@]}"

  if [ "$mode_attach" = 1 ]; then
    local bad=""
    [ "$with_remote" = 1 ]            && bad="${bad:+$bad, }-r/--remote/--refresh"
    [ "$mode_delete" = 1 ]            && bad="${bad:+$bad, }-D/--delete"
    [ -n "$worktree" ]                && bad="${bad:+$bad, }-w/--worktree"
    [ -n "$editor" ]                  && bad="${bad:+$bad, }-c/-p/-o/--cd"
    [ "$mode_pick" = 1 ]              && bad="${bad:+$bad, }--pick/--connect"
    [ "$mode_focus" = 1 ]             && bad="${bad:+$bad, }-f/--focus-list"
    if [ -n "$bad" ]; then
      echo "bridge: --attach takes no other flags (got: $bad). Run \`bridge <repo>\` to launch." >&2
      return 2
    fi
    if [ ${#pos[@]} -gt 0 ]; then
      echo "bridge: --attach takes no positional args (got: ${pos[*]}). Run \`bridge <repo>\` to launch." >&2
      return 2
    fi
    _bridge_attach_pick
    return
  fi

  if [ "$mode_pick" = 1 ]; then
    local bad=""
    [ "$with_remote" = 1 ]            && bad="${bad:+$bad, }-r/--remote/--refresh"
    [ "$mode_delete" = 1 ]            && bad="${bad:+$bad, }-D/--delete"
    [ -n "$worktree" ]                && bad="${bad:+$bad, }-w/--worktree"
    [ -n "$editor" ]                  && bad="${bad:+$bad, }-c/-p/-o/--cd"
    [ "$_BRIDGE_NO_CHANNEL" = 1 ]     && bad="${bad:+$bad, }--no-channel"
    [ "$_BRIDGE_NO_SYNC" = 1 ]        && bad="${bad:+$bad, }--no-sync"
    [ -n "$_BRIDGE_FORCED_SLOT" ]     && bad="${bad:+$bad, }--slot"
    [ "$remote_control" != 1 ]        && bad="${bad:+$bad, }--no-rc"
    [ "$mode_attach" = 1 ]            && bad="${bad:+$bad, }-a/--attach"
    [ "$mode_focus" = 1 ]             && bad="${bad:+$bad, }-f/--focus-list"
    if [ -n "$bad" ]; then
      echo "bridge: --pick takes no other flags (got: $bad)." >&2
      return 2
    fi
    if [ ${#pos[@]} -gt 0 ]; then
      echo "bridge: --pick takes no positional args (got: ${pos[*]})." >&2
      return 2
    fi
    _bridge_status_pick
    return
  fi

  mkdir -p "$_BRIDGE_CACHE"
  local mru="$_BRIDGE_CACHE/mru"
  [ -f "$mru" ] || : > "$mru"

  # `bridge update` — pull the config repo and re-source. Handled before the
  # update hint and meta-warm so we don't nag the user during an update.
  if [ "${1:-}" = "update" ]; then
    _bridge_update
    return
  fi

  # Presence sub-commands. Handled here (before the launch path) so they
  # work from any cwd, regardless of repo membership.
  case "${1:-}" in
    away)     _bridge_presence_set away; return ;;
    back)     _bridge_presence_set auto; return ;;
    here)     _bridge_presence_set here; return ;;
    presence) _bridge_presence_show;     return ;;
  esac

  _bridge_check_latest

  # Background-warm repo-meta.json so tab-completion keyword search works
  # without an explicit `-r`/`--refresh` first. Skipped when -r is set (the
  # picker does it synchronously). flock prevents pile-ups across shells.
  if [ "$with_remote" = 0 ]; then
    local _meta="$_BRIDGE_CACHE/repo-meta.json" _age
    _age=$(( $(date +%s) - $(stat -c %Y "$_meta" 2>/dev/null || echo 0) ))
    if [ ! -f "$_meta" ] || [ "$_age" -gt "$_BRIDGE_REMOTE_TTL" ]; then
      (
        flock -n 9 || exit 0
        _bridge_remote_list 0 >/dev/null 2>&1
      ) 9>"$_BRIDGE_CACHE/meta-warm.lock" </dev/null >/dev/null 2>&1 &
      disown 2>/dev/null || true
    fi
  fi

  # Launch current repo when invoked with "." or bare from inside a repo.
  # Skip when -r/--remote/--refresh is set: user explicitly wants the picker.
  # Skip when -i/--repo-issues is set: $# may be 0 (resolve repo from CWD).
  if [ "$mode_delete" = 0 ] && [ "$with_remote" = 0 ] && [ "$mode_repo_issues" = 0 ] && [ "$mode_focus" = 0 ] && { [ "${1:-}" = "." ] || [ $# -eq 0 ]; }; then
    local git_root="" _b _rel=""
    git_root=$(git -C "$PWD" rev-parse --show-toplevel 2>/dev/null)
    if [ -n "$git_root" ]; then
      for _b in "${_BRIDGE_BASES[@]}"; do
        if [[ "$git_root" == "$_b/"* ]]; then
          _rel="${git_root#$_b/}"
          break
        fi
      done
    fi
    if [ -n "$_rel" ]; then
      _bridge_launch "$_rel" "$worktree" "$editor" "$remote_control"
      return
    fi
    if [ "${1:-}" = "." ]; then
      echo "bridge: '.' requires current dir to be inside a repo under any of: $(_bridge_display_bases)" >&2
      return 1
    fi
  fi

  local all
  all=$(
    for _b in "${_BRIDGE_BASES[@]}"; do
      find "$_b" -type d -name '_archive' -prune -o -type d -name .git -printf '%h\n' 2>/dev/null | sed "s|^$_b/||"
    done
  )

  # -f / --focus-list [name]: list focus repos, or open a named repo.
  if [ "$mode_focus" = 1 ]; then
    if [ -z "${1:-}" ]; then
      _bridge_focus_list "$focus_no_cache"
      return
    fi
    # name provided — fall through to the existing positional-arg launch path below
  fi

  # -i / --repo-issues [name]: print open issues for one repo via `gh issue list`.
  # With no name, resolve from $PWD if inside a repo under $_BRIDGE_BASE.
  if [ "$mode_repo_issues" = 1 ]; then
    local rel=""
    if [ -n "${1:-}" ]; then
      rel=$(printf '%s\n' "$all" | grep -Ei "(^|/)$1$" | head -1)
      [ -z "$rel" ] && rel=$(printf '%s\n' "$all" | grep -Ei "(^|/)[^/]*$1[^/]*$" | head -1)
      if [ -z "$rel" ]; then
        echo "bridge: no such repo: $1" >&2
        return 1
      fi
    else
      local git_root=""
      git_root=$(git -C "$PWD" rev-parse --show-toplevel 2>/dev/null)
      if [ -n "$git_root" ] && [ "${git_root#$_BRIDGE_BASE/}" != "$git_root" ]; then
        rel="${git_root#$_BRIDGE_BASE/}"
      else
        echo "bridge: -i with no name requires CWD to be inside a repo under $(_bridge_display_path "$_BRIDGE_BASE")" >&2
        return 1
      fi
    fi
    (
      cd "$_BRIDGE_BASE/$rel" || exit 1
      command -v direnv >/dev/null && eval "$(direnv export bash 2>/dev/null)"
      gh issue list --state open
    )
    return
  fi

  # --delete <name> (non-interactive shortcut): match local repos by basename.
  if [ "$mode_delete" = 1 ] && [ -n "${1:-}" ]; then
    local matches
    matches=$(printf '%s\n' "$all" | grep -Ei "(^|/)$1$")
    if [ -z "$matches" ]; then
      echo "bridge: no local repo named '$1' (use picker Ctrl-D to delete uncloned remotes)" >&2
      return 1
    fi
    if [ "$(printf '%s\n' "$matches" | wc -l)" -gt 1 ]; then
      echo "bridge: '$1' is ambiguous:" >&2
      printf '  %s\n' $matches >&2
      return 1
    fi
    _bridge_delete "$matches"
    return
  fi

  # Positional shortcut: case-insensitive basename lookup (exact, then substring).
  # If name misses, fall back to metadata (topics + description) search.
  if [ "$mode_delete" = 0 ] && [ -n "${1:-}" ]; then
    local sel
    sel=$(printf '%s\n' "$all" | grep -Ei "(^|/)$1$" | head -1)
    [ -z "$sel" ] && sel=$(printf '%s\n' "$all" | grep -Ei "(^|/)[^/]*$1[^/]*$" | head -1)
    if [ -n "$sel" ]; then
      _bridge_launch "$sel" "$worktree" "$editor" "$remote_control"
      return
    fi

    # Name miss — try metadata search.
    local meta_hits count hit_path was_remote=0
    meta_hits=$(_bridge_meta_search "$1")
    count=$(printf '%s' "$meta_hits" | grep -c '^' 2>/dev/null); count=${count:-0}

    if [ "$count" = 0 ]; then
      echo "bridge: no such repo: $1" >&2
      return 1
    fi

    if [ "$count" = 1 ]; then
      hit_path=$(printf '%s' "$meta_hits" | cut -f2)
      printf '%s\n' "$all" | grep -qxF "$hit_path" || was_remote=1
      if [ "$was_remote" = 1 ]; then
        _bridge_clone_remote "$hit_path" || return 1
      fi
      _bridge_launch "$hit_path" "$worktree" "$editor" "$remote_control"
      return
    fi

    # 2+ hits — annotated fzf picker. Carry the raw path as a trailing
    # tab-separated field so extraction is exact, regardless of any
    # whitespace in the formatted display column.
    local pick
    pick=$(printf '%s\n' "$meta_hits" \
      | awk -F'\t' 'BEGIN{OFS="\t"} { printf "%-50s  [%s: %s]\t%s\n", $2, $1, $3, $2 }' \
      | fzf --height=40% --reverse --prompt="match '$1'> " -d $'\t' --with-nth=1) || return
    hit_path=$(printf '%s' "$pick" | awk -F'\t' '{print $2}')
    [ -z "$hit_path" ] && return
    printf '%s\n' "$all" | grep -qxF "$hit_path" || was_remote=1
    if [ "$was_remote" = 1 ]; then
      _bridge_clone_remote "$hit_path" || return 1
    fi
    _bridge_launch "$hit_path" "$worktree" "$editor" "$remote_control"
    return
  fi

  # Build local list with MRU on top — this part is fast.
  local listed recent rest
  recent=$(while IFS= read -r line; do
             [ -z "$line" ] && continue
             printf '%s\n' "$all" | grep -qxF "$line" && printf '%s\n' "$line"
           done < "$mru")
  rest=$(printf '%s\n' "$all" | grep -vxF -f <(printf '%s\n' "$recent") 2>/dev/null || printf '%s\n' "$all")
  listed=$(printf '%s\n%s\n' "$recent" "$rest" | awk 'NF')

  local expect="ctrl-n,ctrl-d"
  [ "$with_remote" = 1 ] && expect="$expect,ctrl-r"

  # Stream into fzf: local entries immediately, remote entries as each
  # forge API call returns. The producer dies with SIGPIPE when fzf exits.
  local out query key selraw
  out=$({
    printf '%s\n' "$listed"
    if [ "$with_remote" = 1 ]; then
      _bridge_remote_list "$force_refresh" 2>/dev/null | while IFS= read -r line; do
        [ -z "$line" ] && continue
        printf '%s\n' "$all" | grep -qxF "$line" || printf '↓ %s\n' "$line"
      done
    fi
  } | fzf --height=40% --reverse --prompt='repo> ' --tiebreak=index \
          --print-query --expect="$expect") || return
  query=$(printf '%s\n' "$out" | sed -n '1p')
  key=$(printf '%s\n' "$out" | sed -n '2p')
  selraw=$(printf '%s\n' "$out" | sed -n '3p')

  case "$key" in
    ctrl-n) _bridge_create_new "$query"; return ;;
    ctrl-r) bridge --refresh; return ;;
    ctrl-d)
      [ -z "$selraw" ] && return
      _bridge_delete "$selraw"
      return ;;
  esac

  [ -z "$selraw" ] && return
  local sel="${selraw#↓ }"
  if [ "$sel" != "$selraw" ]; then
    _bridge_clone_remote "$sel" || return
  fi
  _bridge_launch "$sel" "$worktree" "$editor" "$remote_control"
}

# Cached basename list for tab completion. Walking every repo's full
# directory tree on each keystroke took 1-2s end-to-end; the cache makes
# completion instant. Every completion call kicks a background rebuild,
# so the list converges within one tab press of any clone/delete — good
# enough for a UI affordance, and we avoid wiring explicit invalidation
# into every mutation site.
_BRIDGE_LOCAL_LIST_CACHE="$_BRIDGE_CACHE/local-repos.list"
_bridge_local_list_build() {
  mkdir -p "$_BRIDGE_CACHE" 2>/dev/null
  local tmp="$_BRIDGE_LOCAL_LIST_CACHE.tmp.$$"
  {
    local _b
    for _b in "${_BRIDGE_BASES[@]}"; do
      find "$_b" -type d -name '_archive' -prune -o \
                 -type d -name '.git' -prune -printf '%h\n' 2>/dev/null
    done
  } | awk -F/ 'NF{print $NF}' | sort -u > "$tmp" 2>/dev/null \
    && mv "$tmp" "$_BRIDGE_LOCAL_LIST_CACHE" 2>/dev/null \
    || rm -f "$tmp" 2>/dev/null
}

_bridge() {
  local cur="${COMP_WORDS[COMP_CWORD]}"
  COMPREPLY=()
  local prev="${COMP_WORDS[COMP_CWORD-1]:-}"
  if [[ "$prev" == "-f" || "$prev" == "--focus-list" ]] && [[ "$cur" != -* ]]; then
    if [ -f "$_BRIDGE_FOCUS_CACHE" ]; then
      local focus_names
      focus_names=$(jq -r '.repos[].name | split("/")[-1]' \
                    "$_BRIDGE_FOCUS_CACHE" 2>/dev/null)
      if [ -n "$focus_names" ]; then
        COMPREPLY=($(compgen -W "$focus_names" -- "$cur"))
        return
      fi
    fi
    # fallthrough: no cache or empty → normal repo name completion below
  fi
  if [[ "$cur" == -* ]]; then
    local flags="-r --remote --refresh -D --delete -c --code -p --copilot -o --opencode --cd --remote-control --rc -w --worktree --no-sync --no-channel --slot --status --status-rc --doctor --worktree-status --ws --issues --dashboard -i --repo-issues -f --focus-list --focus-add --focus-rm --no-cache -B --base --setup-admin --install-admin-commands --free -a --attach --pick --connect -V --version -h --help"
    COMPREPLY=($(compgen -W "$flags" -- "$cur"))
    return
  fi
  local names name
  if [ -s "$_BRIDGE_LOCAL_LIST_CACHE" ]; then
    names=$(cat "$_BRIDGE_LOCAL_LIST_CACHE" 2>/dev/null)
  else
    _bridge_local_list_build
    names=$(cat "$_BRIDGE_LOCAL_LIST_CACHE" 2>/dev/null)
  fi
  # Best-effort async rebuild so the cache converges after clone/delete.
  ( _bridge_local_list_build >/dev/null 2>&1 & ) 2>/dev/null
  shopt -s nocasematch
  while IFS= read -r name; do
    [[ "$name" == *"$cur"* ]] && COMPREPLY+=("$name")
  done <<< "$names"
  # Built-in verbs
  for verb in update away back here presence; do
    [[ "$verb" == *"$cur"* ]] && COMPREPLY+=("$verb")
  done
  shopt -u nocasematch

  # Keyword fallback: only when basename matching produced nothing. Mirrors
  # the positional-arg path (basename first, meta on miss) and prevents a
  # single clean basename hit from being diluted by description-only matches
  # — which would otherwise collapse the completion to a useless common
  # prefix (e.g. `pipe<tab>` → `claude-` instead of `claude-pipeline`).
  if [ ${#COMPREPLY[@]} -eq 0 ] && [ -n "$cur" ] && [ -f "$_BRIDGE_CACHE/repo-meta.json" ]; then
    local meta_names found c
    meta_names=$(_bridge_meta_search "$cur" 2>/dev/null | awk -F'\t' '{print $2}' | awk -F/ '{print $NF}')
    while IFS= read -r name; do
      [ -z "$name" ] && continue
      found=0
      for c in "${COMPREPLY[@]}"; do [ "$c" = "$name" ] && { found=1; break; }; done
      [ "$found" = 0 ] && COMPREPLY+=("$name")
    done <<< "$meta_names"
  fi
}
complete -F _bridge bridge

# Restore expand_aliases setting that was in effect before this file was sourced.
[ "${_bridge_saved_expand_aliases:-0}" = 1 ] && shopt -s expand_aliases
unset _bridge_saved_expand_aliases
