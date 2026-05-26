# bridge-shim.sh — sourced from ~/.bashrc once Phase 3 cuts over.
# Calls the Go binary's __preflight subcommand and acts on its directive.
# Keep this file small (≤20 lines of logic). Anything complex belongs in the binary.

bridge() {
    local directive rc
    directive=$(command bridge-go __preflight "$@")
    rc=$?
    if [ $rc -ne 0 ]; then
        return $rc
    fi
    case "$directive" in
        cd:*)   cd "${directive#cd:}" ;;
        exec:*) exec ${directive#exec:} ;;
        noop)   command bridge-go "$@" ;;
        *)
            printf 'bridge: unknown directive: %s\n' "$directive" >&2
            return 1
            ;;
    esac
}
