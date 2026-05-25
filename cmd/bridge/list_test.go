package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func runBridge(t *testing.T, root string, args ...string) (string, string, int) {
	t.Helper()
	cmd := exec.Command("go", append([]string{"run", "."}, args...)...)
	cmd.Env = append(os.Environ(), "BRIDGE_REPOS_ROOT="+root)
	var sout, serr stringBuf
	cmd.Stdout = &sout
	cmd.Stderr = &serr
	err := cmd.Run()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("run: %v", err)
	}
	return sout.String(), serr.String(), code
}

type stringBuf struct{ b []byte }

func (s *stringBuf) Write(p []byte) (int, error) { s.b = append(s.b, p...); return len(p), nil }
func (s *stringBuf) String() string              { return string(s.b) }

func writeFakeRepos(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, p := range []string{
		"github/freaxnx01/public/bridge",
		"github/freaxnx01/private/secret",
		"gitlab/freaxnx01/glrepo",
	} {
		if err := os.MkdirAll(filepath.Join(root, p), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestListLocalHuman(t *testing.T) {
	root := writeFakeRepos(t)
	out, _, code := runBridge(t, root, "list")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !contains(out, "bridge") || !contains(out, "secret") || !contains(out, "glrepo") {
		t.Errorf("missing repo in output: %s", out)
	}
}

func TestListLocalJSON(t *testing.T) {
	root := writeFakeRepos(t)
	out, _, code := runBridge(t, root, "list", "--json")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	var repos []map[string]any
	if err := json.Unmarshal([]byte(out), &repos); err != nil {
		t.Fatalf("json: %v in %s", err, out)
	}
	if len(repos) != 3 {
		t.Errorf("want 3, got %d", len(repos))
	}
}

func contains(s, sub string) bool { return len(s) >= len(sub) && (indexOf(s, sub) >= 0) }
func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
