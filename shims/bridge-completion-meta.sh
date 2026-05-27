# shellcheck shell=bash
# bridge-completion-meta.sh — augments cobra's bash completion for `bridge`
# with a meta-keyword fallback. Source AFTER `bridge completion bash`:
#
#   source <(bridge completion bash)
#   source ~/.local/share/bridge/bridge-completion-meta.sh
#
# Why: cobra's emitted completion uses `compgen -W -- "$cur"`, which filters
# suggestions case-sensitively against the typed prefix. Repos whose match
# lives in repo-meta.json topics or description (e.g. typing `nextgen` to
# find `ArchiveRestApiNextGen`) get dropped. This shim wraps cobra's
# `__start_bridge` so when its primary completion comes back empty, we ask
# the binary for meta hits and set COMPREPLY directly — bypassing compgen.

if ! declare -F __start_bridge >/dev/null; then
    return 0 2>/dev/null || exit 0
fi

# Save the cobra-generated function under a new name and replace it with
# one that calls through, then augments when COMPREPLY is empty.
eval "__bridge_meta_orig_start() $(declare -f __start_bridge | tail -n +2)"

__start_bridge() {
    __bridge_meta_orig_start "$@"
    if [ "${#COMPREPLY[@]}" -gt 0 ]; then
        return 0
    fi
    local cur="${COMP_WORDS[$COMP_CWORD]}"
    if [ -z "$cur" ]; then
        return 0
    fi
    # Meta lookup. One repo name per line; empty output = no hits.
    local hits
    hits=$(command bridge __complete-meta "$cur" 2>/dev/null) || return 0
    if [ -z "$hits" ]; then
        return 0
    fi
    # Set COMPREPLY directly — no compgen filter applied.
    mapfile -t COMPREPLY <<<"$hits"
}
