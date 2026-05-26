package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestOpenByExactName(t *testing.T) {
	root := writeFakeRepos(t)
	cache := t.TempDir()
	cmd := exec.Command("go", "run", ".", "open", "bridge", "--json")
	cmd.Env = append(os.Environ(),
		"BRIDGE_REPOS_ROOT="+root,
		"XDG_CACHE_HOME="+cache,
	)
	var sout stringBuf
	cmd.Stdout = &sout
	if err := cmd.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
	var r map[string]any
	if err := json.Unmarshal([]byte(sout.String()), &r); err != nil {
		t.Fatalf("json: %v in %s", err, sout.String())
	}
	if r["name"] != "bridge" {
		t.Errorf("got %+v", r)
	}
	b, _ := os.ReadFile(filepath.Join(cache, "bridge", "mru"))
	if len(b) == 0 {
		t.Error("MRU not touched")
	}
}

func TestOpenUnknownNameExits2(t *testing.T) {
	root := writeFakeRepos(t)
	cache := t.TempDir()
	cmd := exec.Command("go", "run", ".", "open", "does-not-exist")
	cmd.Env = append(os.Environ(),
		"BRIDGE_REPOS_ROOT="+root,
		"XDG_CACHE_HOME="+cache,
	)
	var serr stringBuf
	cmd.Stderr = &serr
	err := cmd.Run()
	if err == nil {
		t.Fatal("expected non-zero exit")
	}
	// `go run` itself exits 1 when the child exits non-zero, and prints
	// "exit status N" to stderr. Check that the child signalled exit 2.
	if ee, ok := err.(*exec.ExitError); ok {
		code := ee.ExitCode()
		// Direct binary run: exit code is propagated exactly.
		// go run wrapper: exits 1 but prints "exit status 2" to stderr.
		if code == 2 {
			return // pass
		}
		if code == 1 && contains(serr.String(), "exit status 2") {
			return // pass (go run wrapper behaviour)
		}
		t.Errorf("expected exit 2, got %d (stderr: %s)", code, serr.String())
	}
}

func TestOpenCaseInsensitive(t *testing.T) {
	root := writeFakeRepos(t)
	cache := t.TempDir()
	cmd := exec.Command("go", "run", ".", "open", "BRIDGE", "--json")
	cmd.Env = append(os.Environ(),
		"BRIDGE_REPOS_ROOT="+root,
		"XDG_CACHE_HOME="+cache,
	)
	if err := cmd.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
}
