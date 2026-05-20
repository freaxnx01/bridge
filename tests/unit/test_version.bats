#!/usr/bin/env bats

load '../helpers/load'

setup() {
  clrepo_load_lib
}

@test "_CLREPO_VERSION is set and looks like semver" {
  [ -n "$_CLREPO_VERSION" ]
  [[ "$_CLREPO_VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]
}

@test "clrepo -V prints the version" {
  run clrepo -V
  [ "$status" -eq 0 ]
  [[ "$output" == *"$_CLREPO_VERSION"* ]]
}

@test "clrepo --version prints the version" {
  run clrepo --version
  [ "$status" -eq 0 ]
  [[ "$output" == *"$_CLREPO_VERSION"* ]]
}

@test "clrepo --help prints a usage line" {
  run clrepo --help
  [ "$status" -eq 0 ]
  [[ "$output" == *"Usage: clrepo"* ]]
}
