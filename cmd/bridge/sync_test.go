package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestSyncStatusJSON(t *testing.T) {
	cache := t.TempDir()
	cacheDir := filepath.Join(cache, "bridge")
	_ = os.MkdirAll(cacheDir, 0o755)
	_ = os.WriteFile(filepath.Join(cacheDir, "sync.json"), []byte(`{
      "last_run":"2026-05-01T00:00:00Z","queue":["a","b"],"unpushed":["repo/x"]
    }`), 0o644)

	cmd := exec.Command("go", "run", ".", "sync", "--json")
	cmd.Env = append(os.Environ(), "XDG_CACHE_HOME="+cache)
	var sout stringBuf
	cmd.Stdout = &sout
	if err := cmd.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	var s map[string]any
	if err := json.Unmarshal([]byte(sout.String()), &s); err != nil {
		t.Fatalf("json: %v in %s", err, sout.String())
	}
	if len(s["queue"].([]any)) != 2 || len(s["unpushed"].([]any)) != 1 {
		t.Errorf("%+v", s)
	}
}

func TestSyncStatusMissing(t *testing.T) {
	cache := t.TempDir()
	cmd := exec.Command("go", "run", ".", "sync", "--json")
	cmd.Env = append(os.Environ(), "XDG_CACHE_HOME="+cache)
	var sout stringBuf
	cmd.Stdout = &sout
	if err := cmd.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	var s map[string]any
	_ = json.Unmarshal([]byte(sout.String()), &s)
	if s == nil {
		t.Errorf("expected object even when missing")
	}
}

func TestSyncNowNotImplemented(t *testing.T) {
	cmd := exec.Command("go", "run", ".", "sync", "now")
	err := cmd.Run()
	if err == nil {
		t.Fatal("expected non-zero exit (Plan B)")
	}
}
