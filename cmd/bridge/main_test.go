package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// bridgeBin is the path to the freshly built bridge-go binary, populated by TestMain.
// Tests use it via runBin(t, ...) instead of `go run .`, which would recompile per test.
var bridgeBin string

// TestMain builds the binary once for the whole package.
// `go run .` recompiles every invocation; with ~20 subprocess tests in this
// package, that was ~5 minutes per test cycle. A single up-front compile drops
// it to seconds.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "bridge-bin-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "tempdir:", err)
		os.Exit(1)
	}
	bridgeBin = filepath.Join(dir, "bridge-go")
	cmd := exec.Command("go", "build", "-o", bridgeBin, ".")
	cmd.Stderr = os.Stderr
	cmd.Stdout = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "build:", err)
		os.RemoveAll(dir)
		os.Exit(1)
	}
	// os.Exit skips defers, so clean up explicitly.
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// bridgeCmd returns an *exec.Cmd invoking the prebuilt binary with the given args.
// Drop-in replacement for exec.Command("go", "run", ".", args...).
//
// By default the env carries BRIDGE_SHIM_LOADED=1 so shim-dependent verbs
// (`open`, `sessions attach`) don't trip their no-shim guard inside tests.
// Tests that want to exercise the guard should override cmd.Env explicitly.
func bridgeCmd(args ...string) *exec.Cmd {
	cmd := exec.Command(bridgeBin, args...)
	cmd.Env = append(os.Environ(), "BRIDGE_SHIM_LOADED=1")
	return cmd
}
