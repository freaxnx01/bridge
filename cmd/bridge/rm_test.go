package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestRmRequiresYes(t *testing.T) {
	root := writeFakeRepos(t)
	cache := t.TempDir()
	cmd := bridgeCmd("rm", "bridge")
	cmd.Env = append(os.Environ(),
		"BRIDGE_REPOS_ROOT="+root,
		"XDG_CACHE_HOME="+cache,
	)
	err := cmd.Run()
	if err == nil {
		t.Fatal("expected non-zero exit without --yes")
	}
	if _, err := os.Stat(filepath.Join(root, "github/freaxnx01/public/bridge")); err != nil {
		t.Errorf("repo should still exist: %v", err)
	}
}

func TestRmWithYesDeletes(t *testing.T) {
	root := writeFakeRepos(t)
	cache := t.TempDir()
	target := filepath.Join(root, "github/freaxnx01/public/bridge")
	cmd := bridgeCmd("rm", "bridge", "--yes")
	cmd.Env = append(os.Environ(),
		"BRIDGE_REPOS_ROOT="+root,
		"XDG_CACHE_HOME="+cache,
	)
	if err := cmd.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Errorf("expected repo deleted, stat err = %v", err)
	}
}

func TestRmUnknownExits2(t *testing.T) {
	root := writeFakeRepos(t)
	cache := t.TempDir()
	cmd := bridgeCmd("rm", "nope", "--yes")
	cmd.Env = append(os.Environ(),
		"BRIDGE_REPOS_ROOT="+root,
		"XDG_CACHE_HOME="+cache,
	)
	err := cmd.Run()
	if err == nil {
		t.Fatal("expected exit 2")
	}
	if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() != 2 {
		t.Errorf("exit %d", ee.ExitCode())
	}
}
