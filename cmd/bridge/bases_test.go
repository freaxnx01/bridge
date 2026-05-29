package main

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

// resetEnv clears every env var that influences reposRoots() so each test
// starts from a known floor, then restores them at teardown via t.Setenv.
func resetEnv(t *testing.T) {
	t.Helper()
	t.Setenv("BRIDGE_BASE", "")
	t.Setenv("BRIDGE_REPOS_ROOT", "")
	t.Setenv("BRIDGE_BASE_FILE", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	// Save and clear the flag var; restore at teardown.
	prev := baseFlag
	baseFlag = nil
	t.Cleanup(func() { baseFlag = prev })
}

func TestReposRootsDefault(t *testing.T) {
	resetEnv(t)
	t.Setenv("HOME", "/no/such/home")
	got := reposRoots()
	want := []string{"/no/such/home/projects/repos"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestReposRootsBridgeReposRootLegacy(t *testing.T) {
	resetEnv(t)
	t.Setenv("BRIDGE_REPOS_ROOT", "/legacy/single")
	got := reposRoots()
	if !reflect.DeepEqual(got, []string{"/legacy/single"}) {
		t.Errorf("got %v", got)
	}
}

func TestReposRootsBridgeBaseColonSep(t *testing.T) {
	resetEnv(t)
	t.Setenv("BRIDGE_BASE", "/a"+string(os.PathListSeparator)+"/b")
	t.Setenv("BRIDGE_REPOS_ROOT", "/should-be-ignored")
	got := reposRoots()
	if !reflect.DeepEqual(got, []string{"/a", "/b"}) {
		t.Errorf("got %v", got)
	}
}

func TestReposRootsFlagWinsOverEnv(t *testing.T) {
	resetEnv(t)
	t.Setenv("BRIDGE_BASE", "/env-base")
	baseFlag = []string{"/flag-a", "/flag-b"}
	got := reposRoots()
	if !reflect.DeepEqual(got, []string{"/flag-a", "/flag-b"}) {
		t.Errorf("got %v", got)
	}
}

func TestReposRootsFlagAcceptsCommaSplit(t *testing.T) {
	resetEnv(t)
	baseFlag = []string{"/a,/b", "/c"}
	got := reposRoots()
	if !reflect.DeepEqual(got, []string{"/a", "/b", "/c"}) {
		t.Errorf("got %v", got)
	}
}

func TestReposRootsConfigFileFallback(t *testing.T) {
	resetEnv(t)
	cfgDir := t.TempDir()
	cfgFile := filepath.Join(cfgDir, "base")
	body := "# leading comment\n/cfg/a\n\n/cfg/b\n"
	if err := os.WriteFile(cfgFile, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BRIDGE_BASE_FILE", cfgFile)
	got := reposRoots()
	if !reflect.DeepEqual(got, []string{"/cfg/a", "/cfg/b"}) {
		t.Errorf("got %v", got)
	}
}

func TestReposRootsDedupes(t *testing.T) {
	resetEnv(t)
	t.Setenv("BRIDGE_BASE", "/x"+string(os.PathListSeparator)+"/y"+string(os.PathListSeparator)+"/x")
	got := reposRoots()
	if !reflect.DeepEqual(got, []string{"/x", "/y"}) {
		t.Errorf("got %v", got)
	}
}

func TestDiscoverAllRootsSpansBases(t *testing.T) {
	resetEnv(t)
	baseA := t.TempDir()
	baseB := t.TempDir()
	// One github repo under baseA, one under baseB.
	for _, p := range []string{
		filepath.Join(baseA, "github", "ownerA", "public", "repo1"),
		filepath.Join(baseB, "github", "ownerB", "public", "repo2"),
	} {
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("BRIDGE_BASE", baseA+string(os.PathListSeparator)+baseB)
	repos, err := discoverAllRoots()
	if err != nil {
		t.Fatalf("discoverAllRoots: %v", err)
	}
	names := make([]string, 0, len(repos))
	for _, r := range repos {
		names = append(names, r.Name)
	}
	sort.Strings(names)
	if !reflect.DeepEqual(names, []string{"repo1", "repo2"}) {
		t.Errorf("got %v, want [repo1 repo2]", names)
	}
}

func TestDiscoverAllRootsDedupesByPath(t *testing.T) {
	// Same base listed twice should not double-count repos.
	resetEnv(t)
	base := t.TempDir()
	p := filepath.Join(base, "github", "o", "public", "dup")
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BRIDGE_BASE", base+string(os.PathListSeparator)+base)
	repos, err := discoverAllRoots()
	if err != nil {
		t.Fatal(err)
	}
	if len(repos) != 1 {
		t.Errorf("expected 1 repo after dedupe, got %d: %+v", len(repos), repos)
	}
}
