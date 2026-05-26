package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestSyncNowWritesState(t *testing.T) {
	root := writeFakeRepos(t)
	cache := t.TempDir()
	bindir := t.TempDir()
	_ = os.WriteFile(filepath.Join(bindir, "git"), []byte("#!/bin/sh\nexit 0\n"), 0o755)

	cmd := bridgeCmd("sync", "now")
	cmd.Env = append(os.Environ(),
		"BRIDGE_REPOS_ROOT="+root,
		"XDG_CACHE_HOME="+cache,
		"PATH="+bindir+":"+os.Getenv("PATH"),
	)
	if err := cmd.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	b, _ := os.ReadFile(filepath.Join(cache, "bridge", "sync.json"))
	var st map[string]any
	_ = json.Unmarshal(b, &st)
	if _, ok := st["last_run"]; !ok {
		t.Errorf("missing last_run in %s", b)
	}
}

func TestSyncNowRecordsUnpushed(t *testing.T) {
	root := writeFakeRepos(t)
	cache := t.TempDir()
	bindir := t.TempDir()
	// Fake git: fetch/pull succeed; rev-list returns "2" so every repo is "ahead".
	_ = os.WriteFile(filepath.Join(bindir, "git"), []byte(`#!/bin/sh
case "$1" in
  rev-list) echo 2 ;;
  *) exit 0 ;;
esac
`), 0o755)

	cmd := bridgeCmd("sync", "now")
	cmd.Env = append(os.Environ(),
		"BRIDGE_REPOS_ROOT="+root,
		"XDG_CACHE_HOME="+cache,
		"PATH="+bindir+":"+os.Getenv("PATH"),
	)
	if err := cmd.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	b, _ := os.ReadFile(filepath.Join(cache, "bridge", "sync.json"))
	var st struct {
		Unpushed []string `json:"unpushed"`
	}
	_ = json.Unmarshal(b, &st)
	if len(st.Unpushed) == 0 {
		t.Errorf("expected unpushed entries, got %s", b)
	}
}
