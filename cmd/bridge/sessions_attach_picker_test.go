package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPreflightSessionsAttachPickerFixture(t *testing.T) {
	dir := t.TempDir()
	fixture := filepath.Join(dir, "tmux.txt")
	_ = os.WriteFile(fixture, []byte("alpha|0|1716000000\nbeta|1|1716000100\n"), 0o644)

	cmd := bridgeCmd("__preflight", "sessions", "attach")
	cmd.Env = append(os.Environ(),
		"BRIDGE_TMUX_FIXTURE="+fixture,
		"BRIDGE_PICKER_FIXTURE=beta",
		"BRIDGE_NO_TERM_FALLBACK=1",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "exec:tmux attach-session -t beta") {
		t.Errorf("got %q", out)
	}
}
