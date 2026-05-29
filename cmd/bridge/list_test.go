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
	// Mark each owner directory as a direnv scope so the .envrc-walking
	// target discovery in loadOrFetchRemote picks them up. The content is
	// intentionally empty — tokens are injected via cmd.Env in each test,
	// and direnv exec on an unallowed empty .envrc just inherits parent env.
	for _, p := range []string{
		"github/freaxnx01/.envrc",
		"gitlab/freaxnx01/.envrc",
		"ado/.envrc",
	} {
		full := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(""), 0o644); err != nil {
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

// --- multi-base discovery integration (#86) ---

func TestListSpansMultipleBases(t *testing.T) {
	baseA := t.TempDir()
	baseB := t.TempDir()
	for _, p := range []string{
		filepath.Join(baseA, "github", "ownerA", "public", "alpha"),
		filepath.Join(baseB, "github", "ownerB", "public", "beta"),
	} {
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	cmd := bridgeCmd("list")
	cmd.Env = append(envWithout("BRIDGE_REPOS_ROOT", "BRIDGE_BASE"),
		"BRIDGE_BASE="+baseA+string(os.PathListSeparator)+baseB,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	s := string(out)
	if !contains(s, "alpha") || !contains(s, "beta") {
		t.Errorf("expected both repos in output, got: %s", s)
	}
}

func TestListBaseFlagOverridesEnv(t *testing.T) {
	envBase := t.TempDir()
	flagBase := t.TempDir()
	_ = os.MkdirAll(filepath.Join(envBase, "github", "o", "public", "fromenv"), 0o755)
	_ = os.MkdirAll(filepath.Join(flagBase, "github", "o", "public", "fromflag"), 0o755)

	cmd := bridgeCmd("--base", flagBase, "list")
	cmd.Env = append(envWithout("BRIDGE_REPOS_ROOT", "BRIDGE_BASE"),
		"BRIDGE_BASE="+envBase,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	s := string(out)
	if !contains(s, "fromflag") {
		t.Errorf("expected fromflag, got: %s", s)
	}
	if contains(s, "fromenv") {
		t.Errorf("flag should override env; got env-repo in output: %s", s)
	}
}

func TestListMissingBaseWarns(t *testing.T) {
	base := t.TempDir()
	_ = os.MkdirAll(filepath.Join(base, "github", "o", "public", "real"), 0o755)
	cmd := bridgeCmd("list")
	cmd.Env = append(envWithout("BRIDGE_REPOS_ROOT", "BRIDGE_BASE"),
		"BRIDGE_BASE="+base+string(os.PathListSeparator)+"/nope/does/not/exist",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	s := string(out)
	if !contains(s, "real") {
		t.Errorf("real base ignored: %s", s)
	}
	if !contains(s, "warning") || !contains(s, "/nope/does/not/exist") {
		t.Errorf("expected missing-base warning, got: %s", s)
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
