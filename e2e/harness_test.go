//go:build e2e

// Package e2e drives the built bridge binary against a fixture repos-root
// and asserts the stdout/exit-code/shim-directive contract that downstream
// consumers (the shell shim, slash-command callers, agents) depend on.
//
// Run with: go test -tags=e2e ./e2e/...
//
// The harness builds bridge once per package and reuses the binary across
// tests. Each test gets its own tempdir-backed repos root + cache root so
// no test mutates global state.
package e2e

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

var (
	binOnce sync.Once
	binPath string
	binErr  error
)

// bridgeBin builds (and caches) the bridge binary for the current OS/arch.
func bridgeBin(t *testing.T) string {
	t.Helper()
	binOnce.Do(func() {
		dir, err := os.MkdirTemp("", "bridge-e2e-bin-*")
		if err != nil {
			binErr = err
			return
		}
		name := "bridge"
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
		out := filepath.Join(dir, name)
		// `go build` resolves the module root by walking up from CWD —
		// we're in ./e2e/, so the parent is the module root.
		cmd := exec.Command("go", "build", "-o", out, "../cmd/bridge")
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			binErr = err
			return
		}
		binPath = out
	})
	if binErr != nil {
		t.Fatalf("build bridge: %v", binErr)
	}
	return binPath
}

// fixtureRoot creates a repos-root containing the three forge layouts the
// discovery walker recognises (github public/private + gitlab). Caller
// gets a path it can pass via BRIDGE_REPOS_ROOT.
func fixtureRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, p := range []string{
		"github/freaxnx01/public/bridge",
		"github/freaxnx01/private/secret",
		"gitlab/freaxnx01/glrepo",
	} {
		if err := os.MkdirAll(filepath.Join(root, p), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// run invokes the built binary with the given args + per-test env. Returns
// stdout, stderr, and exit code. Never errors on non-zero exit — tests
// assert the code themselves.
func run(t *testing.T, reposRoot, cacheRoot string, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	cmd := exec.Command(bridgeBin(t), args...)
	cmd.Env = append(os.Environ(),
		"BRIDGE_REPOS_ROOT="+reposRoot,
		"XDG_CACHE_HOME="+cacheRoot,
	)
	var so, se bytes.Buffer
	cmd.Stdout = &so
	cmd.Stderr = &se
	err := cmd.Run()
	code = 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else {
			t.Fatalf("run bridge %v: %v", args, err)
		}
	}
	return so.String(), se.String(), code
}

// contains is a thin wrapper that fails the test with the full output for
// easier diagnosis than a bare assertion would give.
func contains(t *testing.T, haystack, needle, label string) {
	t.Helper()
	if !strings.Contains(haystack, needle) {
		t.Errorf("%s: expected %q to contain %q\n--- output ---\n%s", label, label, needle, haystack)
	}
}
