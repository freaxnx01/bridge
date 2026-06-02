package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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
	// Suppress the TTL-gated /releases/latest check so tests don't hit the
	// real GitHub API. Inherited by every bridgeCmd subprocess via os.Environ.
	os.Setenv("BRIDGE_NO_VERSION_CHECK", "1")
	// Skip the pre-launch ff-pull (#90) globally for the same reason —
	// fake fixtures in writeFakeRepos aren't real git repos. Tests that
	// exercise sync explicitly override BRIDGE_NO_SYNC in cmd.Env.
	os.Setenv("BRIDGE_NO_SYNC", "1")

	// Sandbox the Claude config dir so preflight-open subprocesses that install
	// the relabel SessionStart[clear] hook (#85) never write into the
	// developer's real ~/.claude/settings.json. Without this, tests that only
	// set XDG_CACHE_HOME let EffectiveConfigDir fall back to $HOME/.claude and
	// leak a stale hook on every run. Inherited by every bridgeCmd subprocess
	// via os.Environ; tests needing a specific dir override CLAUDE_CONFIG_DIR in
	// cmd.Env (later env entries win). HOME is left untouched so git and other
	// HOME-dependent subprocess behaviour is unaffected.
	claudeDir, err := os.MkdirTemp("", "bridge-claude-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "tempdir:", err)
		os.Exit(1)
	}
	os.Setenv("CLAUDE_CONFIG_DIR", claudeDir)

	dir, err := os.MkdirTemp("", "bridge-bin-")
	if err != nil {
		fmt.Fprintln(os.Stderr, "tempdir:", err)
		os.RemoveAll(claudeDir)
		os.Exit(1)
	}
	// On Windows `go build -o name` appends .exe; match it so exec(bridgeBin)
	// resolves the file that was actually produced.
	name := "bridge-go"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	bridgeBin = filepath.Join(dir, name)
	cmd := exec.Command("go", "build", "-o", bridgeBin, ".")
	cmd.Stderr = os.Stderr
	cmd.Stdout = os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "build:", err)
		os.RemoveAll(dir)
		os.RemoveAll(claudeDir)
		os.Exit(1)
	}
	// os.Exit skips defers, so clean up explicitly.
	code := m.Run()
	os.RemoveAll(dir)
	os.RemoveAll(claudeDir)
	os.Exit(code)
}

// TestMain_SandboxesClaudeConfigDir is a regression guard for the relabel-hook
// leak: a `bridge __preflight open … --agent claude` subprocess installs a
// SessionStart[clear] hook (#85) into CLAUDE_CONFIG_DIR. With no sandbox set,
// EffectiveConfigDir falls back to the developer's real ~/.claude and every
// preflight-open test that only set XDG_CACHE_HOME appended a stale hook there.
// TestMain must point CLAUDE_CONFIG_DIR at a temp dir so subprocesses can never
// touch the real config.
func TestMain_SandboxesClaudeConfigDir(t *testing.T) {
	cfg := os.Getenv("CLAUDE_CONFIG_DIR")
	if cfg == "" {
		t.Fatal("CLAUDE_CONFIG_DIR not set by TestMain; preflight subprocesses would leak relabel hooks into the real ~/.claude")
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if real := filepath.Join(home, ".claude"); cfg == real {
			t.Fatalf("CLAUDE_CONFIG_DIR = %q is the real config dir; subprocesses would pollute it", cfg)
		}
	}
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
