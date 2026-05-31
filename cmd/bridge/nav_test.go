package main

import (
	"strings"
	"testing"
)

func TestNavCmd_Once_RendersFrame(t *testing.T) {
	cmd := bridgeCmd("nav", "--once")
	cmd.Env = append(cmd.Env, "BRIDGE_REPOS_ROOT="+t.TempDir())
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("nav --once: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "filter:") {
		t.Errorf("nav --once produced no picker frame:\n%s", out)
	}
}
