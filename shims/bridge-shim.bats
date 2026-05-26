#!/usr/bin/env bats

# Build the Go binary once for the suite.
setup_file() {
    BRIDGE_TEST_DIR=$(mktemp -d)
    export BRIDGE_TEST_DIR
    (cd "$BATS_TEST_DIRNAME/.." && go build -o "$BRIDGE_TEST_DIR/bridge-go" ./cmd/bridge)
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
