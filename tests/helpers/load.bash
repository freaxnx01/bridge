# Common bats setup for bridge unit tests.
#
# Each test should `load '../helpers/load'` in its preamble and call
# `bridge_load_lib` from setup() to source bridge.sh against an isolated
# tmpdir.
#
# Install bats locally:
#   - Debian/Ubuntu: sudo apt install bats
#   - macOS:         brew install bats-core
#   - From source:   git clone https://github.com/bats-core/bats-core /opt/bats
#                    and add /opt/bats/bin to PATH

HELPERS_DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )"
TESTS_DIR="$( cd "$HELPERS_DIR/.." && pwd )"
REPO_ROOT="$( cd "$TESTS_DIR/.." && pwd )"

export PATH="$HELPERS_DIR/stubs:$PATH"

# Source bridge.sh into the test shell with isolated cache/config/base dirs.
# Tests can override BRIDGE_BASE / BRIDGE_CACHE / BRIDGE_CONFIG before
# calling this; otherwise per-test defaults under $BATS_TEST_TMPDIR are used.
bridge_load_lib() {
  : "${BRIDGE_CACHE:=$BATS_TEST_TMPDIR/cache}"
  : "${BRIDGE_CONFIG:=$BATS_TEST_TMPDIR/config}"
  : "${BRIDGE_BASE:=$BATS_TEST_TMPDIR/repos}"
  mkdir -p "$BRIDGE_CACHE" "$BRIDGE_CONFIG"
  # If BRIDGE_BASE is a single path (no colons), make sure it exists so
  # _bridge_collect_bases doesn't warn about a missing dir.
  case "$BRIDGE_BASE" in
    *:*) ;;
    *) mkdir -p "$BRIDGE_BASE" ;;
  esac
  export BRIDGE_CACHE BRIDGE_CONFIG BRIDGE_BASE
  # shellcheck source=/dev/null
  source "$REPO_ROOT/bridge.sh"
}
