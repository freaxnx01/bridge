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
	cmd := bridgeCmd("open", "bridge", "--json")
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

func TestOpenJSONEnrichesFromRepoMetaCache(t *testing.T) {
	root := writeFakeRepos(t)
	cache := t.TempDir()
	bridgeCache := filepath.Join(cache, "bridge")
	_ = os.MkdirAll(bridgeCache, 0o755)
	_ = os.WriteFile(filepath.Join(bridgeCache, "repo-meta.json"), []byte(`{
		"github/freaxnx01/public/bridge": {
			"description": "the bridge",
			"topics": ["dev-tools","cli"],
			"default_branch": "main",
			"remote_url": "https://github.com/freaxnx01/bridge"
		}
	}`), 0o644)

	cmd := bridgeCmd("open", "bridge", "--json")
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
	if r["desc"] != "the bridge" {
		t.Errorf("desc: %+v", r)
	}
	if r["default_branch"] != "main" {
		t.Errorf("default_branch: %+v", r)
	}
	if r["remote_url"] != "https://github.com/freaxnx01/bridge" {
		t.Errorf("remote_url: %+v", r)
	}
}

func TestOpenUnknownNameExits2(t *testing.T) {
	root := writeFakeRepos(t)
	cache := t.TempDir()
	cmd := bridgeCmd("open", "does-not-exist")
	cmd.Env = append(os.Environ(),
		"BRIDGE_REPOS_ROOT="+root,
		"XDG_CACHE_HOME="+cache,
		"BRIDGE_SHIM_LOADED=1",
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
	cmd := bridgeCmd("open", "BRIDGE", "--json")
	cmd.Env = append(os.Environ(),
		"BRIDGE_REPOS_ROOT="+root,
		"XDG_CACHE_HOME="+cache,
	)
	if err := cmd.Run(); err != nil {
		t.Fatalf("run: %v", err)
	}
}

func TestOpenKeywordFallback(t *testing.T) {
	root := writeFakeRepos(t)
	cache := t.TempDir()
	// "br" should match "bridge" via keyword fallback (no exact "br" repo).
	cmd := bridgeCmd("open", "br", "--json")
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
	_ = json.Unmarshal([]byte(sout.String()), &r)
	if r["name"] != "bridge" {
		t.Errorf("expected fallback to bridge, got %+v", r)
	}
}

func TestOpenWithoutShimFailsLoudly(t *testing.T) {
	// `bridge open foo` outside the shim used to print the repo path and exit 0
	// (silent no-op — see #66). It must now error usefully.
	root := writeFakeRepos(t)
	cache := t.TempDir()
	cmd := bridgeCmd("open", "bridge")
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"BRIDGE_REPOS_ROOT=" + root,
		"XDG_CACHE_HOME=" + cache,
		// no BRIDGE_SHIM_LOADED
	}
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected non-zero exit, got success: %s", out)
	}
	if !contains(string(out), "shim") {
		t.Errorf("expected error mentioning shim, got: %s", out)
	}
}

func TestOpenJSONBypassesShimCheck(t *testing.T) {
	// --json is for programmatic consumers (agents, scripts, CI). It must
	// produce output without the shim being loaded.
	root := writeFakeRepos(t)
	cache := t.TempDir()
	cmd := bridgeCmd("open", "bridge", "--json")
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"BRIDGE_REPOS_ROOT=" + root,
		"XDG_CACHE_HOME=" + cache,
		// no BRIDGE_SHIM_LOADED
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("expected success even without shim (--json), got: %v\n%s", err, out)
	}
	if !contains(string(out), `"name": "bridge"`) {
		t.Errorf("expected JSON for bridge, got: %s", out)
	}
}

func TestOpenAmbiguousKeyword(t *testing.T) {
	// writeFakeRepos creates "bridge", "secret", "glrepo" — "e" matches all 3.
	root := writeFakeRepos(t)
	cache := t.TempDir()
	cmd := bridgeCmd("open", "e")
	cmd.Env = append(os.Environ(),
		"BRIDGE_REPOS_ROOT="+root,
		"XDG_CACHE_HOME="+cache,
		"BRIDGE_SHIM_LOADED=1",
	)
	err := cmd.Run()
	if err == nil {
		t.Fatal("expected non-zero exit for ambiguous match")
	}
}
