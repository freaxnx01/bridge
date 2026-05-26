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
    PATH="$stubdir:$PATH" bash -c "source $BATS_TEST_DIRNAME/bridge-shim.sh; bridge whatever" || true
    run cat "$out"
    [ "$status" -eq 0 ]
    [ "$output" = "hi there" ]
    rm -rf "$stubdir" "$out"
    unset TEST_OUT
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
