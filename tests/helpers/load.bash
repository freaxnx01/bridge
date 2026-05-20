# Common bats setup for clrepo unit tests.
#
# Each test should `load '../helpers/load'` in its preamble and call
# `clrepo_load_lib` from setup() to source clrepo.sh against an isolated
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

# Source clrepo.sh into the test shell with isolated cache/config/base dirs.
# Tests can override CLREPO_BASE / CLREPO_CACHE / CLREPO_CONFIG before
# calling this; otherwise per-test defaults under $BATS_TEST_TMPDIR are used.
clrepo_load_lib() {
  : "${CLREPO_CACHE:=$BATS_TEST_TMPDIR/cache}"
  : "${CLREPO_CONFIG:=$BATS_TEST_TMPDIR/config}"
  : "${CLREPO_BASE:=$BATS_TEST_TMPDIR/repos}"
  mkdir -p "$CLREPO_CACHE" "$CLREPO_CONFIG"
  # If CLREPO_BASE is a single path (no colons), make sure it exists so
  # _clrepo_collect_bases doesn't warn about a missing dir.
  case "$CLREPO_BASE" in
    *:*) ;;
    *) mkdir -p "$CLREPO_BASE" ;;
  esac
  export CLREPO_CACHE CLREPO_CONFIG CLREPO_BASE
  # shellcheck source=/dev/null
  source "$REPO_ROOT/clrepo.sh"
}
