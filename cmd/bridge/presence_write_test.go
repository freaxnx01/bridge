package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestPresenceSetAway(t *testing.T) {
	cache := t.TempDir()
	cmd := bridgeCmd("presence", "away")
	cmd.Env = append(os.Environ(), "XDG_CACHE_HOME="+cache)
	if err := cmd.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	b, _ := os.ReadFile(filepath.Join(cache, "bridge", "presence.json"))
	var p map[string]any
	_ = json.Unmarshal(b, &p)
	if p["mode"] != "away" {
		t.Errorf("got %+v", p)
	}
}

func TestPresenceSetUnknownExits2(t *testing.T) {
	cache := t.TempDir()
	cmd := bridgeCmd("presence", "bogus")
	cmd.Env = append(os.Environ(), "XDG_CACHE_HOME="+cache)
	err := cmd.Run()
	if err == nil {
		t.Fatal("expected exit 2")
	}
}
