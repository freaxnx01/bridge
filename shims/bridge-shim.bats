#!/usr/bin/env bats

# Build the Go binary once for the suite.
setup_file() {
    BRIDGE_TEST_DIR=$(mktemp -d)
    export BRIDGE_TEST_DIR
    (cd "$BATS_TEST_DIRNAME/.." && go build -o "$BRIDGE_TEST_DIR/bridge" ./cmd/bridge)
    export PATH="$BRIDGE_TEST_DIR:$PATH"
}

teardown_file() {
    rm -rf "$BRIDGE_TEST_DIR"
}

@test "sourcing the shim exports BRIDGE_SHIM_LOADED=1 (#66)" {
    run bash -c "source $BATS_TEST_DIRNAME/bridge-shim.sh; echo \"\$BRIDGE_SHIM_LOADED\""
    [ "$status" -eq 0 ]
    [ "$output" = "1" ]
}

@test "no-arg preflight is noop (shim falls through)" {
    run bash -c "source $BATS_TEST_DIRNAME/bridge-shim.sh; bridge --version"
    [ "$status" -eq 0 ]
    [[ "$output" == *"bridge"* ]]
}

@test "preflight noop calls the binary verbatim" {
    run bash -c "source $BATS_TEST_DIRNAME/bridge-shim.sh; bridge list --help"
    [ "$status" -eq 0 ]
    [[ "$output" == *"List local repos"* ]]
}

@test "exec directive with quoted argv reaches target as a single argv element" {
    # Stub bridge-go to emit a controlled exec directive whose third argv
    # element ("hi there") contains whitespace. The shim must re-parse the
    # directive so 'hi there' arrives as one argv element, not three.
    stubdir=$(mktemp -d)
    out=$(mktemp)
    export TEST_OUT="$out"
    # Heredoc is single-quoted so no expansion happens inside; the stub
    # references $TEST_OUT (env-injected) at run time.
    # After `bash -c CMD _ 'hi there'`, "hi there" is $1 inside CMD ($0 is "_").
    cat > "$stubdir/bridge" <<'STUB'
#!/usr/bin/env bash
if [ "$1" = "__preflight" ]; then
    printf "exec:bash -c '%s' _ '%s'\n" 'printf %s "$1" > '"$TEST_OUT" 'hi there'
else
    exit 0
fi
STUB
    chmod +x "$stubdir/bridge"
    # Pin the exec (replace) branch so this test is deterministic regardless of
    # whether the suite itself runs over SSH (SSH_CONNECTION would otherwise
    # flip the shim to the child branch). Quoting re-parse is what's under test.
    PATH="$stubdir:$PATH" bash -c "unset SSH_CONNECTION BRIDGE_NO_EXEC; export BRIDGE_FORCE_EXEC=1; source $BATS_TEST_DIRNAME/bridge-shim.sh; bridge whatever" || true
    run cat "$out"
    [ "$status" -eq 0 ]
    [ "$output" = "hi there" ]
    rm -rf "$stubdir" "$out"
    unset TEST_OUT
}

# Emits an exec directive that prints TARGET_RAN, so a caller can tell whether
# the target ran (it always should) and — via a marker after `bridge` — whether
# the shim *replaced* the calling shell (exec, marker swallowed) or ran the
# target as a *child* (marker still prints).
_exec_probe_stub() {
    local d=$1
    cat > "$d/bridge" <<'STUB'
#!/usr/bin/env bash
if [ "$1" = "__preflight" ]; then
    printf "exec:bash -c '%s'\n" 'echo TARGET_RAN'
else
    exit 0
fi
STUB
    chmod +x "$d/bridge"
}

@test "exec runs as a child under SSH so the caller's shell survives (returns to prompt)" {
    stubdir=$(mktemp -d)
    _exec_probe_stub "$stubdir"
    run bash -c "export PATH=\"$stubdir:\$PATH\"; export SSH_CONNECTION='1.2.3.4 5 6.7.8.9 22'; unset BRIDGE_NO_EXEC BRIDGE_FORCE_EXEC; source $BATS_TEST_DIRNAME/bridge-shim.sh; bridge whatever; echo AFTER_MARKER"
    [ "$status" -eq 0 ]
    [[ "$output" == *"TARGET_RAN"* ]]      # target launched
    [[ "$output" == *"AFTER_MARKER"* ]]    # shell NOT replaced — control returned
    rm -rf "$stubdir"
}

@test "exec replaces the calling shell locally (no SSH) — terminal becomes the session" {
    stubdir=$(mktemp -d)
    _exec_probe_stub "$stubdir"
    run bash -c "export PATH=\"$stubdir:\$PATH\"; unset SSH_CONNECTION BRIDGE_NO_EXEC BRIDGE_FORCE_EXEC; source $BATS_TEST_DIRNAME/bridge-shim.sh; bridge whatever; echo AFTER_MARKER"
    [ "$status" -eq 0 ]
    [[ "$output" == *"TARGET_RAN"* ]]      # target launched
    [[ "$output" != *"AFTER_MARKER"* ]]    # shell replaced via exec — marker swallowed
    rm -rf "$stubdir"
}

@test "BRIDGE_NO_EXEC forces the child branch even with no SSH" {
    stubdir=$(mktemp -d)
    _exec_probe_stub "$stubdir"
    run bash -c "export PATH=\"$stubdir:\$PATH\"; unset SSH_CONNECTION BRIDGE_FORCE_EXEC; export BRIDGE_NO_EXEC=1; source $BATS_TEST_DIRNAME/bridge-shim.sh; bridge whatever; echo AFTER_MARKER"
    [ "$status" -eq 0 ]
    [[ "$output" == *"TARGET_RAN"* ]]
    [[ "$output" == *"AFTER_MARKER"* ]]
    rm -rf "$stubdir"
}

@test "BRIDGE_FORCE_EXEC forces the exec branch even under SSH" {
    stubdir=$(mktemp -d)
    _exec_probe_stub "$stubdir"
    run bash -c "export PATH=\"$stubdir:\$PATH\"; export SSH_CONNECTION='1.2.3.4 5 6.7.8.9 22' BRIDGE_FORCE_EXEC=1; unset BRIDGE_NO_EXEC; source $BATS_TEST_DIRNAME/bridge-shim.sh; bridge whatever; echo AFTER_MARKER"
    [ "$status" -eq 0 ]
    [[ "$output" == *"TARGET_RAN"* ]]
    [[ "$output" != *"AFTER_MARKER"* ]]
    rm -rf "$stubdir"
}

@test "completion meta-augmenter populates COMPREPLY when cobra returns empty (#65)" {
    # When cobra's primary completion compgen-filters everything out (e.g. a
    # meta-only keyword), the augmenter must fall back to `bridge
    # __complete-meta` and set COMPREPLY directly. Use a stub bridge that
    # emits one meta hit so we don't need a populated cache.
    stubdir=$(mktemp -d)
    cat > "$stubdir/bridge" <<'STUB'
#!/usr/bin/env bash
if [ "$1" = "__complete-meta" ]; then
    echo "ArchiveRestApiNextGen"
    exit 0
fi
# Anything else from this stub is unexpected in this test.
exit 1
STUB
    chmod +x "$stubdir/bridge"
    run bash -c "
        export PATH=\"$stubdir:\$PATH\"
        # Hand-define __start_bridge to simulate cobra's empty return.
        __start_bridge() { COMPREPLY=(); }
        source $BATS_TEST_DIRNAME/bridge-completion-meta.sh
        COMP_WORDS=(bridge open nextgen)
        COMP_CWORD=2
        __start_bridge
        printf '%s\n' \"\${COMPREPLY[@]}\"
    "
    [ "$status" -eq 0 ]
    [[ "$output" == *"ArchiveRestApiNextGen"* ]]
    rm -rf "$stubdir"
}

@test "completion meta-augmenter is a no-op when cobra already has hits" {
    # The augmenter must not clobber cobra's COMPREPLY. If cobra found
    # matches, the meta lookup should not run at all.
    stubdir=$(mktemp -d)
    cat > "$stubdir/bridge" <<'STUB'
#!/usr/bin/env bash
echo "META RAN — augmenter is overriding cobra hits" >&2
exit 1
STUB
    chmod +x "$stubdir/bridge"
    run bash -c "
        export PATH=\"$stubdir:\$PATH\"
        __start_bridge() { COMPREPLY=(canonical-hit); }
        source $BATS_TEST_DIRNAME/bridge-completion-meta.sh
        COMP_WORDS=(bridge open whatever)
        COMP_CWORD=2
        __start_bridge
        printf '%s\n' \"\${COMPREPLY[@]}\"
    "
    [ "$status" -eq 0 ]
    [ "$output" = "canonical-hit" ]
    [[ "$output" != *"META RAN"* ]]
    rm -rf "$stubdir"
}

@test "cancel directive exits 0 without calling binary fallback (#63)" {
    # If the shim treated cancel like noop, it would re-run `bridge -r`, which
    # would hit the legacy rewrite (-r → list -r) and dump the text list to
    # stdout. cancel must exit silently.
    stubdir=$(mktemp -d)
    cat > "$stubdir/bridge" <<'STUB'
#!/usr/bin/env bash
if [ "$1" = "__preflight" ]; then
    echo cancel
    exit 0
fi
echo "FALLBACK CALLED with args: $*"
exit 0
STUB
    chmod +x "$stubdir/bridge"
    run bash -c "export PATH=\"$stubdir:\$PATH\"; source $BATS_TEST_DIRNAME/bridge-shim.sh; bridge -r"
    [ "$status" -eq 0 ]
    [[ "$output" != *"FALLBACK CALLED"* ]]
    [ -z "$output" ]
    rm -rf "$stubdir"
}
