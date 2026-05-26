package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestPresenceReadJSON(t *testing.T) {
	cache := t.TempDir()
	cacheDir := filepath.Join(cache, "bridge")
	_ = os.MkdirAll(cacheDir, 0o755)
	_ = os.WriteFile(filepath.Join(cacheDir, "presence.json"), []byte(`{"mode":"away"}`), 0o644)

	cmd := bridgeCmd("presence", "--json")
	cmd.Env = append(os.Environ(), "XDG_CACHE_HOME="+cache)
	var sout stringBuf
	cmd.Stdout = &sout
	if err := cmd.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	var p map[string]any
	if err := json.Unmarshal([]byte(sout.String()), &p); err != nil {
		t.Fatalf("json: %v in %s", err, sout.String())
	}
	if p["mode"] != "away" {
		t.Errorf("got %+v", p)
	}
}

func TestPresenceWriteRejected(t *testing.T) {
	cmd := bridgeCmd("presence", "away")
	var serr stringBuf
	cmd.Stderr = &serr
	err := cmd.Run()
	if err == nil {
		t.Fatal("expected non-zero exit")
	}
}
