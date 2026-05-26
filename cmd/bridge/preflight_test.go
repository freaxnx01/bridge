package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestPreflightNoArgs(t *testing.T) {
	root := writeFakeRepos(t)
	cache := t.TempDir()
	cmd := bridgeCmd("__preflight")
	cmd.Env = append(os.Environ(),
		"BRIDGE_REPOS_ROOT="+root,
		"XDG_CACHE_HOME="+cache,
		"BRIDGE_PICKER_FIXTURE_CANCEL=1",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	s := strings.TrimSpace(string(out))
	if s != "cancel" {
		t.Errorf("got %q, want cancel", s)
	}
}

func TestPreflightDashRRoutesToPicker(t *testing.T) {
	// `bridge -r` must NOT dump text — it routes to the picker. Using the
	// cancel fixture so we get a deterministic cancel without needing fzf.
	root := writeFakeRepos(t)
	cache := t.TempDir()
	cmd := bridgeCmd("__preflight", "-r")
	cmd.Env = append(os.Environ(),
		"BRIDGE_REPOS_ROOT="+root,
		"XDG_CACHE_HOME="+cache,
		"BRIDGE_PICKER_FIXTURE_CANCEL=1",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	s := strings.TrimSpace(string(out))
	if s != "cancel" {
		t.Errorf("got %q, want cancel (picker cancel)", s)
	}
}

func TestPreflightRefreshRoutesToPicker(t *testing.T) {
	root := writeFakeRepos(t)
	cache := t.TempDir()
	cmd := bridgeCmd("__preflight", "--refresh")
	cmd.Env = append(os.Environ(),
		"BRIDGE_REPOS_ROOT="+root,
		"XDG_CACHE_HOME="+cache,
		"BRIDGE_PICKER_FIXTURE_CANCEL=1",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	s := strings.TrimSpace(string(out))
	if s != "cancel" {
		t.Errorf("got %q, want cancel", s)
	}
}

func TestPreflightUnknownVerb(t *testing.T) {
	out, err := bridgeCmd("__preflight", "list").CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	if strings.TrimSpace(string(out)) != "noop" {
		t.Errorf("got %q", out)
	}
}

func TestPreflightIsHidden(t *testing.T) {
	out, err := bridgeCmd("--help").CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	if strings.Contains(string(out), "__preflight") {
		t.Errorf("__preflight should be hidden from --help, got:\n%s", out)
	}
}

func TestPreflightOpenEmitsCD(t *testing.T) {
	root := writeFakeRepos(t)
	cache := t.TempDir()
	cmd := bridgeCmd("__preflight", "open", "bridge")
	cmd.Env = append(os.Environ(),
		"BRIDGE_REPOS_ROOT="+root,
		"XDG_CACHE_HOME="+cache,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	s := strings.TrimSpace(string(out))
	if !strings.HasPrefix(s, "cd:") || !strings.HasSuffix(s, "/bridge") {
		t.Errorf("got %q", s)
	}
}

func TestPreflightOpenWithAgentEmitsExec(t *testing.T) {
	root := writeFakeRepos(t)
	cache := t.TempDir()
	cmd := bridgeCmd("__preflight", "open", "bridge", "--agent", "claude")
	// Clear TMUX so the test exercises the outside-tmux branch deterministically.
	cmd.Env = append(envWithout("TMUX"),
		"BRIDGE_REPOS_ROOT="+root,
		"XDG_CACHE_HOME="+cache,
	)
	out, _ := cmd.CombinedOutput()
	s := strings.TrimSpace(string(out))
	if !strings.HasPrefix(s, "exec:tmux new-session -A -s ") {
		t.Errorf("got %q", s)
	}
	if !strings.Contains(s, " claude") {
		t.Errorf("expected agent in argv: %q", s)
	}
}

func TestPreflightOpenWithWorktreeAgent(t *testing.T) {
	// `bridge open <name> -w <wt> --agent claude` must:
	//   - tmux session name = <repo>-wt-<wt>
	//   - working dir = <repo>/.worktrees/<wt>
	root := writeFakeRepos(t)
	cache := t.TempDir()
	cmd := bridgeCmd("__preflight", "open", "bridge", "-w", "feature-x", "--agent", "claude")
	cmd.Env = append(envWithout("TMUX"),
		"BRIDGE_REPOS_ROOT="+root,
		"XDG_CACHE_HOME="+cache,
	)
	out, _ := cmd.CombinedOutput()
	s := strings.TrimSpace(string(out))
	if !strings.Contains(s, " -s bridge-wt-feature-x ") {
		t.Errorf("missing slot name in: %s", s)
	}
	wtPath := filepath.Join(root, "github", "freaxnx01", "public", "bridge", ".worktrees", "feature-x")
	if !strings.Contains(s, " -c "+wtPath+" ") {
		t.Errorf("missing worktree path %q in: %s", wtPath, s)
	}
}

func TestPreflightOpenWithWorktreeCDOnly(t *testing.T) {
	// Without --agent, -w should still resolve the worktree path for the cd
	// directive.
	root := writeFakeRepos(t)
	cache := t.TempDir()
	cmd := bridgeCmd("__preflight", "open", "bridge", "-w", "feature-x")
	cmd.Env = append(envWithout("TMUX"),
		"BRIDGE_REPOS_ROOT="+root,
		"XDG_CACHE_HOME="+cache,
	)
	out, _ := cmd.CombinedOutput()
	s := strings.TrimSpace(string(out))
	wtPath := filepath.Join(root, "github", "freaxnx01", "public", "bridge", ".worktrees", "feature-x")
	want := "cd:" + wtPath
	if s != want {
		t.Errorf("got %q want %q", s, want)
	}
}

func TestPreflightOpenWithAgentInsideTmuxEmitsSwitchClient(t *testing.T) {
	root := writeFakeRepos(t)
	cache := t.TempDir()
	cmd := bridgeCmd("__preflight", "open", "bridge", "--agent", "claude")
	cmd.Env = append(os.Environ(),
		"BRIDGE_REPOS_ROOT="+root,
		"XDG_CACHE_HOME="+cache,
		"TMUX=/tmp/fake-tmux,1,2", // emulate being inside a tmux client
	)
	out, _ := cmd.CombinedOutput()
	s := strings.TrimSpace(string(out))
	for _, want := range []string{
		"exec:sh -c ",
		"tmux has-session -t bridge",
		"tmux new-session -d -s bridge",
		"exec tmux switch-client -t bridge",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in: %s", want, s)
		}
	}
}

// envWithout returns os.Environ() with any entry whose key equals name removed.
func envWithout(name string) []string {
	src := os.Environ()
	out := make([]string, 0, len(src))
	prefix := name + "="
	for _, e := range src {
		if strings.HasPrefix(e, prefix) {
			continue
		}
		out = append(out, e)
	}
	return out
}

func TestPreflightOpenWithAgentRecordsSlot(t *testing.T) {
	root := writeFakeRepos(t)
	cache := t.TempDir()
	cmd := bridgeCmd("__preflight", "open", "bridge", "--agent", "claude")
	cmd.Env = append(envWithout("TMUX"),
		"BRIDGE_REPOS_ROOT="+root,
		"XDG_CACHE_HOME="+cache,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	// slots.json should now contain a "bridge" entry with agent=claude.
	b, err := os.ReadFile(filepath.Join(cache, "bridge", "slots.json"))
	if err != nil {
		t.Fatalf("read slots.json: %v", err)
	}
	if !strings.Contains(string(b), `"id": "bridge"`) || !strings.Contains(string(b), `"agent": "claude"`) {
		t.Errorf("slot not recorded as expected: %s", b)
	}

	// Second invocation must not duplicate; same ID should be replaced.
	cmd2 := bridgeCmd("__preflight", "open", "bridge", "--agent", "code")
	cmd2.Env = append(envWithout("TMUX"),
		"BRIDGE_REPOS_ROOT="+root,
		"XDG_CACHE_HOME="+cache,
	)
	if out, err := cmd2.CombinedOutput(); err != nil {
		t.Fatalf("run2: %v\n%s", err, out)
	}
	b2, _ := os.ReadFile(filepath.Join(cache, "bridge", "slots.json"))
	// Count occurrences of "id": "bridge" — must be exactly 1.
	if n := strings.Count(string(b2), `"id": "bridge"`); n != 1 {
		t.Errorf("expected 1 slot entry, got %d:\n%s", n, b2)
	}
	if !strings.Contains(string(b2), `"agent": "code"`) {
		t.Errorf("expected agent updated to code: %s", b2)
	}
}

func TestPreflightOpenUnknownRepoExits2(t *testing.T) {
	root := writeFakeRepos(t)
	cache := t.TempDir()
	cmd := bridgeCmd("__preflight", "open", "nope")
	cmd.Env = append(os.Environ(),
		"BRIDGE_REPOS_ROOT="+root,
		"XDG_CACHE_HOME="+cache,
	)
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatal("expected non-zero exit")
	}
	// go run wraps exit codes; accept either direct exit code 2 OR stderr containing "exit status 2".
	if ee, ok := err.(*exec.ExitError); ok {
		if ee.ExitCode() != 2 && !strings.Contains(string(out), "exit status 2") {
			t.Errorf("expected exit 2, got exit %d / output %s", ee.ExitCode(), out)
		}
	}
}
