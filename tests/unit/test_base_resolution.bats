#!/usr/bin/env bats

load '../helpers/load'

setup() {
  # Two real dirs to act as bases; BRIDGE_BASE picks them up via colon-list.
  BASE_A="$BATS_TEST_TMPDIR/a"
  BASE_B="$BATS_TEST_TMPDIR/b"
  mkdir -p "$BASE_A" "$BASE_B/github/freaxnx01/public/foo"
  export BRIDGE_BASE="$BASE_A:$BASE_B"
  bridge_load_lib
}

@test "_bridge_collect_bases picks up both bases from BRIDGE_BASE" {
  # _bridge_collect_bases ran at source time; assert resulting state.
  [ "${#_BRIDGE_BASES[@]}" -eq 2 ]
  [ "${_BRIDGE_BASES[0]}" = "$BASE_A" ]
  [ "${_BRIDGE_BASES[1]}" = "$BASE_B" ]
  [ "$_BRIDGE_BASE" = "$BASE_A" ]
}

@test "_bridge_base_for_rel returns base containing the relative path" {
  run _bridge_base_for_rel "github/freaxnx01/public/foo"
  [ "$status" -eq 0 ]
  [ "$output" = "$BASE_B" ]
}

@test "_bridge_base_for_rel falls back to first base when no match" {
  run _bridge_base_for_rel "github/nobody/public/nope"
  [ "$status" -ne 0 ]
  [ "$output" = "$BASE_A" ]
}

@test "duplicate bases are dropped" {
  export BRIDGE_BASE="$BASE_A:$BASE_A:$BASE_B"
  _BRIDGE_BASES=()
  _bridge_collect_bases
  [ "${#_BRIDGE_BASES[@]}" -eq 2 ]
}

@test "missing base dirs are skipped" {
  export BRIDGE_BASE="$BASE_A:$BATS_TEST_TMPDIR/does-not-exist:$BASE_B"
  _BRIDGE_BASES=()
  # Direct call (not `run`) — we need the function to mutate this shell's array.
  _bridge_collect_bases 2>/dev/null
  [ "${#_BRIDGE_BASES[@]}" -eq 2 ]
  [ "${_BRIDGE_BASES[0]}" = "$BASE_A" ]
  [ "${_BRIDGE_BASES[1]}" = "$BASE_B" ]
}
