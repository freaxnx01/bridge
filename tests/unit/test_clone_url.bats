#!/usr/bin/env bats

load '../helpers/load'

setup() {
  clrepo_load_lib
}

@test "github public path → https github URL" {
  run _clrepo_clone_url "github/freaxnx01/public/clrepo"
  [ "$status" -eq 0 ]
  [ "$output" = "https://github.com/freaxnx01/clrepo.git" ]
}

@test "github private path → https github URL" {
  run _clrepo_clone_url "github/freaxnx01/private/secret-thing"
  [ "$status" -eq 0 ]
  [ "$output" = "https://github.com/freaxnx01/secret-thing.git" ]
}

@test "gitlab path → custom gitlab host URL" {
  run _clrepo_clone_url "gitlab/some-group/some-repo"
  [ "$status" -eq 0 ]
  [ "$output" = "https://gitlab.freaxnx01.ch/some-group/some-repo.git" ]
}

@test "git-forgejo path → ssh forgejo URL" {
  run _clrepo_clone_url "git-forgejo/my-repo"
  [ "$status" -eq 0 ]
  [ "$output" = "ssh://git@git.home.freaxnx01.ch:222/freax/my-repo.git" ]
}

@test "unknown layout → non-zero exit" {
  run _clrepo_clone_url "bitbucket/someone/repo"
  [ "$status" -ne 0 ]
}
