package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestStatusHuman(t *testing.T) {
	cache := t.TempDir()
	cacheDir := filepath.Join(cache, "bridge")
	_ = os.MkdirAll(cacheDir, 0o755)
	_ = os.WriteFile(filepath.Join(cacheDir, "presence.json"), []byte(`{"mode":"away"}`), 0o644)
	_ = os.WriteFile(filepath.Join(cacheDir, "sync.json"), []byte(`{"unpushed":["x"]}`), 0o644)

	cmd := exec.Command("go", "run", ".", "status")
	cmd.Env = append(os.Environ(),
		"XDG_CACHE_HOME="+cache,
		"BRIDGE_TMUX_FIXTURE=",
	)
	var sout stringBuf
	cmd.Stdout = &sout
	if err := cmd.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	s := sout.String()
	if !contains(s, "presence:") || !contains(s, "away") || !contains(s, "unpushed:") {
		t.Errorf("missing keys in %s", s)
	}
}

func TestStatusJSON(t *testing.T) {
	cache := t.TempDir()
	cmd := exec.Command("go", "run", ".", "status", "--json")
	cmd.Env = append(os.Environ(), "XDG_CACHE_HOME="+cache)
	var sout stringBuf
	cmd.Stdout = &sout
	if err := cmd.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	var st map[string]any
	if err := json.Unmarshal([]byte(sout.String()), &st); err != nil {
		t.Fatalf("json: %v in %s", err, sout.String())
	}
	for _, k := range []string{"sessions", "presence", "sync", "version"} {
		if _, ok := st[k]; !ok {
			t.Errorf("missing key %s in %+v", k, st)
		}
	}
}
