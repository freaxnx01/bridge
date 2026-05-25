#!/usr/bin/env bats

load '../helpers/load'

setup() {
  bridge_load_lib
}

@test "_BRIDGE_VERSION is set and looks like semver" {
  [ -n "$_BRIDGE_VERSION" ]
  [[ "$_BRIDGE_VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]
}

@test "bridge -V prints the version" {
  run bridge -V
  [ "$status" -eq 0 ]
  [[ "$output" == *"$_BRIDGE_VERSION"* ]]
}

@test "bridge --version prints the version" {
  run bridge --version
  [ "$status" -eq 0 ]
  [[ "$output" == *"$_BRIDGE_VERSION"* ]]
}

@test "bridge --help prints a usage line" {
  run bridge --help
  [ "$status" -eq 0 ]
  [[ "$output" == *"Usage: bridge"* ]]
}
