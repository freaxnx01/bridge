# shellcheck shell=bash
# bridge-shim.sh — sourced from ~/.bashrc once Phase 3 cuts over.
# Calls the Go binary's __preflight subcommand and acts on its directive.
# Keep this file small (≤20 lines of logic). Anything complex belongs in the binary.

# Sentinel for the binary so verbs like `sessions attach` / `open` can detect
# they're running under a shell that has the shim loaded. Without this, those
# verbs emit `exec:`/`cd:` directives that nothing consumes — silent no-op.
export BRIDGE_SHIM_LOADED=1

bridge() {
    local directive rc
    directive=$(command bridge __preflight "$@")
    rc=$?
    if [ $rc -ne 0 ]; then
        return $rc
    fi
    case "$directive" in
        cd:*)   cd "${directive#cd:}" || return ;;
        # Use `eval` so the sh-quoting that internal/shellbridge emits
        # (single-quoted args with whitespace) is re-parsed by the shell.
        # Without eval, the unquoted parameter expansion would word-split
        # the directive as data, turning literal quotes into argv chars.
        #
        # `exec` replaces the calling shell so the terminal *becomes* the
        # session — the right default locally. But when that shell is the entry
        # point of an SSH session, exiting/detaching the session then tears down
        # the SSH connection (you "fall through" to wherever you ssh'd from). So
        # over SSH run the launch as a child instead, returning you to the remote
        # shell afterward. Overrides: BRIDGE_NO_EXEC forces child anywhere;
        # BRIDGE_FORCE_EXEC forces exec even over SSH (NO_EXEC wins if both set).
        exec:*)
            if [ -n "$BRIDGE_NO_EXEC" ] ||
               { [ -z "$BRIDGE_FORCE_EXEC" ] && [ -n "$SSH_CONNECTION" ]; }; then
                eval "${directive#exec:}"
            else
                eval "exec ${directive#exec:}"
            fi
            ;;
        noop)   command bridge "$@" ;;
        # `cancel` = interactive cancel (e.g. ESC in picker). Silent exit 0.
        # Distinct from noop so we don't re-run the original argv, which would
        # hit legacy rewrites like `-r` → `list -r` and dump the text list.
        cancel) return 0 ;;
        *)
            printf 'bridge: unknown directive: %s\n' "$directive" >&2
            return 1
            ;;
    esac
}
