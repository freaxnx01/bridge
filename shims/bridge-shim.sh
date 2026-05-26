# bridge-shim.sh — sourced from ~/.bashrc once Phase 3 cuts over.
# Calls the Go binary's __preflight subcommand and acts on its directive.
# Keep this file small (≤20 lines of logic). Anything complex belongs in the binary.

bridge() {
    local directive rc
    directive=$(command bridge __preflight "$@")
    rc=$?
    if [ $rc -ne 0 ]; then
        return $rc
    fi
    case "$directive" in
        cd:*)   cd "${directive#cd:}" ;;
        # Use `eval` so the sh-quoting that internal/shellbridge emits
        # (single-quoted args with whitespace) is re-parsed by the shell.
        # Without eval, the unquoted parameter expansion would word-split
        # the directive as data, turning literal quotes into argv chars.
        exec:*) eval "exec ${directive#exec:}" ;;
        noop)   command bridge "$@" ;;
        *)
            printf 'bridge: unknown directive: %s\n' "$directive" >&2
            return 1
            ;;
    esac
}
