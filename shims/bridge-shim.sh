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
