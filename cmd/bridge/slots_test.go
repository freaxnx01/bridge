package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func writeSlots(t *testing.T) string {
	t.Helper()
	cache := t.TempDir()
	cacheDir := filepath.Join(cache, "bridge")
	_ = os.MkdirAll(cacheDir, 0o755)
	_ = os.WriteFile(filepath.Join(cacheDir, "slots.json"), []byte(`{"slots":[
      {"id":"a","repo":"x","agent":"claude","created":"2026-05-01T00:00:00Z"}
    ]}`), 0o644)
	return cache
}

func TestSlotsJSON(t *testing.T) {
	cache := writeSlots(t)
	cmd := bridgeCmd("slots", "--json")
	cmd.Env = append(os.Environ(), "XDG_CACHE_HOME="+cache)
	var sout stringBuf
	cmd.Stdout = &sout
	if err := cmd.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	var slots []map[string]any
	if err := json.Unmarshal([]byte(sout.String()), &slots); err != nil {
		t.Fatalf("json: %v in %s", err, sout.String())
	}
	if len(slots) != 1 || slots[0]["id"] != "a" {
		t.Errorf("%+v", slots)
	}
}

func TestSlotsHuman(t *testing.T) {
	cache := writeSlots(t)
	cmd := bridgeCmd("slots")
	cmd.Env = append(os.Environ(), "XDG_CACHE_HOME="+cache)
	var sout stringBuf
	cmd.Stdout = &sout
	if err := cmd.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !contains(sout.String(), "a") || !contains(sout.String(), "claude") {
		t.Errorf("got %s", sout.String())
	}
}
