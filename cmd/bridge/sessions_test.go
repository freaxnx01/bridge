package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestSessionsJSON(t *testing.T) {
	dir := t.TempDir()
	fixture := filepath.Join(dir, "tmux.txt")
	_ = os.WriteFile(fixture, []byte("a|1|1716000000|1716000150\nb|0|1716000100|1716000180\n"), 0o644)

	cmd := bridgeCmd("sessions", "--json")
	cmd.Env = append(os.Environ(),
		"BRIDGE_TMUX_FIXTURE="+fixture,
		"BRIDGE_NOW=1716000200",
	)
	var sout stringBuf
	cmd.Stdout = &sout
	if err := cmd.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	var sess []map[string]any
	if err := json.Unmarshal([]byte(sout.String()), &sess); err != nil {
		t.Fatalf("json: %v in %s", err, sout.String())
	}
	if len(sess) != 2 || sess[0]["state"] != "attached" || sess[1]["state"] != "detached" {
		t.Errorf("%+v", sess)
	}
}
