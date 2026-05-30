package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/freaxnx01/bridge/internal/agents"
	"github.com/freaxnx01/bridge/internal/core"
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
	cmd.Env = append(envWithout("BRIDGE_DEFAULT_AGENT", "BRIDGE_DEFAULT_AGENT_ARGS"),
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
		"BRIDGE_NO_TERM_FALLBACK=1",
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
	cmd.Env = append(envWithout("TMUX", "BRIDGE_DEFAULT_AGENT", "BRIDGE_DEFAULT_AGENT_ARGS"),
		"BRIDGE_REPOS_ROOT="+root,
		"XDG_CACHE_HOME="+cache,
	)
	// stdout only: the fake fixture isn't a git repo, so worktree resolution
	// falls back to the .worktrees/<wt> convention and logs a banner to stderr.
	out, _ := cmd.Output()
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
		"BRIDGE_NO_TERM_FALLBACK=1",
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

// envWithout returns os.Environ() with any entry whose key matches one of
// names removed. Variadic so test callers can strip several inherited vars
// in one call (commonly TMUX, BRIDGE_DEFAULT_AGENT, BRIDGE_DEFAULT_AGENT_ARGS
// — anything that would leak from the developer's shell into a test process
// and flip directives from cd: to exec:).
func envWithout(names ...string) []string {
	src := os.Environ()
	out := make([]string, 0, len(src))
	prefixes := make([]string, len(names))
	for i, n := range names {
		prefixes[i] = n + "="
	}
NextLine:
	for _, e := range src {
		for _, p := range prefixes {
			if strings.HasPrefix(e, p) {
				continue NextLine
			}
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

func TestPreflightOpenAutoLaunchesWithDefaultAgentEnv(t *testing.T) {
	// `bridge open <name>` with BRIDGE_DEFAULT_AGENT set and no --agent flag
	// should auto-launch the agent (parity with the bash bridge and with
	// the picker entry points, which already consult this env var).
	root := writeFakeRepos(t)
	cache := t.TempDir()
	cmd := bridgeCmd("__preflight", "open", "bridge")
	cmd.Env = append(envWithout("TMUX"),
		"BRIDGE_REPOS_ROOT="+root,
		"XDG_CACHE_HOME="+cache,
		"BRIDGE_DEFAULT_AGENT=claude",
		"BRIDGE_NO_TERM_FALLBACK=1",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	s := strings.TrimSpace(string(out))
	if !strings.HasPrefix(s, "exec:") {
		t.Errorf("expected exec: directive when BRIDGE_DEFAULT_AGENT is set, got %q", s)
	}
	if !strings.Contains(s, " claude") {
		t.Errorf("expected claude in argv: %q", s)
	}
}

func TestPreflightOpenAppendsDefaultAgentArgs(t *testing.T) {
	// BRIDGE_DEFAULT_AGENT_ARGS is appended to the agent spec's Args so the
	// user can wire `--remote-control --dangerously-skip-permissions`
	// (claude's typical interactive setup) once in their shell rc.
	root := writeFakeRepos(t)
	cache := t.TempDir()
	cmd := bridgeCmd("__preflight", "open", "bridge")
	cmd.Env = append(envWithout("TMUX"),
		"BRIDGE_REPOS_ROOT="+root,
		"XDG_CACHE_HOME="+cache,
		"BRIDGE_DEFAULT_AGENT=claude",
		"BRIDGE_DEFAULT_AGENT_ARGS=--remote-control --dangerously-skip-permissions",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	s := strings.TrimSpace(string(out))
	if !strings.Contains(s, "--remote-control") {
		t.Errorf("missing --remote-control in argv: %q", s)
	}
	if !strings.Contains(s, "--dangerously-skip-permissions") {
		t.Errorf("missing --dangerously-skip-permissions in argv: %q", s)
	}
}

func TestPreflightOpenExplicitAgentOverridesEnv(t *testing.T) {
	// `--agent code` on the command line wins over BRIDGE_DEFAULT_AGENT=claude.
	// The default-args env var still applies to whichever agent is launched.
	root := writeFakeRepos(t)
	cache := t.TempDir()
	cmd := bridgeCmd("__preflight", "open", "bridge", "--agent", "code")
	cmd.Env = append(envWithout("TMUX"),
		"BRIDGE_REPOS_ROOT="+root,
		"XDG_CACHE_HOME="+cache,
		"BRIDGE_DEFAULT_AGENT=claude",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	s := strings.TrimSpace(string(out))
	if !strings.Contains(s, " code") {
		t.Errorf("explicit --agent code should win; got %q", s)
	}
	if strings.Contains(s, " claude") {
		t.Errorf("claude should not appear when --agent code is passed: %q", s)
	}
}

func TestPreflightOpenNoAgentNoEnvStillCDs(t *testing.T) {
	// Without --agent and without BRIDGE_DEFAULT_AGENT, the historical cd-only
	// path is preserved — important so users who don't want auto-launch can
	// just unset the env var.
	root := writeFakeRepos(t)
	cache := t.TempDir()
	cmd := bridgeCmd("__preflight", "open", "bridge")
	env := envWithout("TMUX")
	env = stripPrefix(env, "BRIDGE_DEFAULT_AGENT=")
	env = stripPrefix(env, "BRIDGE_DEFAULT_AGENT_ARGS=")
	cmd.Env = append(env,
		"BRIDGE_REPOS_ROOT="+root,
		"XDG_CACHE_HOME="+cache,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	s := strings.TrimSpace(string(out))
	if !strings.HasPrefix(s, "cd:") {
		t.Errorf("expected cd: directive without default-agent env, got %q", s)
	}
}

func stripPrefix(env []string, prefix string) []string {
	out := env[:0]
	for _, e := range env {
		if strings.HasPrefix(e, prefix) {
			continue
		}
		out = append(out, e)
	}
	return out
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

// --- claude launch-naming helpers (issue #84) ---

func TestDisplayName(t *testing.T) {
	repo := core.Repo{Name: "bridge"}
	if got := displayName(repo, ""); got != "bridge" {
		t.Errorf("no-worktree: got %q want %q", got, "bridge")
	}
	if got := displayName(repo, "feature-x"); got != "bridge [feature-x]" {
		t.Errorf("worktree: got %q want %q", got, "bridge [feature-x]")
	}
}

func TestWithClaudeNameClaudePrependsFlag(t *testing.T) {
	spec := agents.AgentSpec{Name: "claude", Bin: "claude", Args: []string{"--remote-control"}}
	repo := core.Repo{Name: "bridge"}
	got := withClaudeName(spec, repo, "")
	want := []string{"-n", "bridge", "--remote-control"}
	if !reflect.DeepEqual(got.Args, want) {
		t.Errorf("Args = %v, want %v", got.Args, want)
	}
}

func TestWithClaudeNameClaudeWithWorktree(t *testing.T) {
	spec := agents.AgentSpec{Name: "claude", Bin: "claude"}
	repo := core.Repo{Name: "bridge"}
	got := withClaudeName(spec, repo, "feature-x")
	want := []string{"-n", "bridge [feature-x]"}
	if !reflect.DeepEqual(got.Args, want) {
		t.Errorf("Args = %v, want %v", got.Args, want)
	}
}

func TestWithClaudeNameNonClaudePassthrough(t *testing.T) {
	for _, name := range []string{"copilot", "opencode", "code"} {
		spec := agents.AgentSpec{Name: name, Bin: name, Args: []string{"."}}
		got := withClaudeName(spec, core.Repo{Name: "bridge"}, "wt")
		if !reflect.DeepEqual(got.Args, []string{"."}) {
			t.Errorf("%s: Args mutated to %v", name, got.Args)
		}
	}
}

func TestWithClaudeNameDoesNotMutateRegistry(t *testing.T) {
	// Resolve twice through the real registry. If withClaudeName mutated the
	// shared slice, the second call's Args would already contain "-n ...".
	first, err := agents.Resolve("claude")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	_ = withClaudeName(first, core.Repo{Name: "bridge"}, "")
	second, _ := agents.Resolve("claude")
	if len(second.Args) != 0 {
		t.Errorf("registry spec.Args mutated: %v", second.Args)
	}
}

// --- launch-naming integration (#84) ---

func TestPreflightOpenClaudeNamesSession(t *testing.T) {
	// `bridge open bridge --agent claude` must emit `claude -n bridge` in argv.
	// The -n bridge token appears at the end of the exec: directive (no trailing
	// space), so we check for the leading space + the suffix together.
	root := writeFakeRepos(t)
	cache := t.TempDir()
	cmd := bridgeCmd("__preflight", "open", "bridge", "--agent", "claude")
	cmd.Env = append(envWithout("TMUX"),
		"BRIDGE_REPOS_ROOT="+root,
		"XDG_CACHE_HOME="+cache,
	)
	out, _ := cmd.CombinedOutput()
	s := strings.TrimSpace(string(out))
	if !strings.Contains(s, " claude -n bridge") {
		t.Errorf("expected ' claude -n bridge' in directive, got: %s", s)
	}
}

func TestPreflightOpenClaudeWorktreeNamesSession(t *testing.T) {
	// With -w feature-x the display name must be the sh-quoted 'bridge [feature-x]'.
	// The token appears at the end of the exec: directive (no trailing space).
	root := writeFakeRepos(t)
	cache := t.TempDir()
	cmd := bridgeCmd("__preflight", "open", "bridge", "-w", "feature-x", "--agent", "claude")
	cmd.Env = append(envWithout("TMUX"),
		"BRIDGE_REPOS_ROOT="+root,
		"XDG_CACHE_HOME="+cache,
	)
	out, _ := cmd.CombinedOutput()
	s := strings.TrimSpace(string(out))
	if !strings.Contains(s, " claude -n 'bridge [feature-x]'") {
		t.Errorf("expected sh-quoted worktree name in directive, got: %s", s)
	}
}

func TestPreflightOpenDefaultAgentClaudeNamesSession(t *testing.T) {
	// With BRIDGE_DEFAULT_AGENT=claude (no explicit --agent) the name must
	// still be injected.
	root := writeFakeRepos(t)
	cache := t.TempDir()
	cmd := bridgeCmd("__preflight", "open", "bridge")
	cmd.Env = append(envWithout("TMUX", "BRIDGE_DEFAULT_AGENT_ARGS"),
		"BRIDGE_REPOS_ROOT="+root,
		"XDG_CACHE_HOME="+cache,
		"BRIDGE_DEFAULT_AGENT=claude",
	)
	out, _ := cmd.CombinedOutput()
	s := strings.TrimSpace(string(out))
	if !strings.Contains(s, " claude -n bridge") {
		t.Errorf("expected ' claude -n bridge' in directive, got: %s", s)
	}
}

// --- pre-launch ff-pull integration (#90) ---

// writeFakeGit drops an executable `git` shim into bindir that:
//   - logs argv to bindir/git.log
//   - reports a clean main branch with upstream origin/main, 2 commits behind
//   - succeeds on fetch and pull
//
// SafePull's queries are: symbolic-ref, rev-parse, status, rev-list, fetch,
// pull. Any other invocation exits 0 silently.
func writeFakeGit(t *testing.T, bindir string) {
	t.Helper()
	script := `#!/bin/sh
echo "$@" >> "$0.log"
case "$1 $2" in
  "symbolic-ref -q") echo "refs/heads/main" ;;
  "rev-parse --abbrev-ref") echo "origin/main" ;;
  "status --porcelain") : ;;
  "rev-list --count")
    case "$3" in
      "@{u}..HEAD") echo 0 ;;
      "HEAD..@{u}") echo 2 ;;
    esac ;;
  "fetch --quiet") : ;;
  "pull --ff-only") : ;;
esac
exit 0
`
	if err := os.WriteFile(filepath.Join(bindir, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

// writeEmptyTmuxFixture creates a BRIDGE_TMUX_FIXTURE file that reports
// zero live sessions, so the reattach-skip in maybePreLaunchSync doesn't
// false-positive against whatever real tmux sessions exist on the dev
// host. (Without this, a developer running these tests inside a tmux
// session named "bridge" would silently skip the pre-launch sync.)
func writeEmptyTmuxFixture(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "tmux-empty")
	if err := os.WriteFile(p, []byte{}, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestPreflightOpenPreLaunchSyncFetchesAndPulls(t *testing.T) {
	root := writeFakeRepos(t)
	cache := t.TempDir()
	bindir := t.TempDir()
	writeFakeGit(t, bindir)

	cmd := bridgeCmd("__preflight", "open", "bridge", "--agent", "claude")
	cmd.Env = append(envWithout("TMUX", "BRIDGE_NO_SYNC"),
		"BRIDGE_REPOS_ROOT="+root,
		"XDG_CACHE_HOME="+cache,
		"PATH="+bindir+":"+os.Getenv("PATH"),
		"BRIDGE_TMUX_FIXTURE="+writeEmptyTmuxFixture(t),
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}

	log, err := os.ReadFile(filepath.Join(bindir, "git.log"))
	if err != nil {
		t.Fatalf("read git.log (bridge output: %s): %v", out, err)
	}
	s := string(log)
	if !strings.Contains(s, "fetch --quiet") {
		t.Errorf("expected fetch call, got log: %s", s)
	}
	if !strings.Contains(s, "pull --ff-only --quiet") {
		t.Errorf("expected pull call, got log: %s", s)
	}
}

func TestPreflightOpenNoSyncFlagSkipsSync(t *testing.T) {
	root := writeFakeRepos(t)
	cache := t.TempDir()
	bindir := t.TempDir()
	writeFakeGit(t, bindir)

	cmd := bridgeCmd("__preflight", "open", "bridge", "--agent", "claude", "--no-sync")
	cmd.Env = append(envWithout("TMUX", "BRIDGE_NO_SYNC"),
		"BRIDGE_REPOS_ROOT="+root,
		"XDG_CACHE_HOME="+cache,
		"PATH="+bindir+":"+os.Getenv("PATH"),
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(bindir, "git.log")); !os.IsNotExist(err) {
		// Log exists means git ran. Read it and fail loudly.
		log, _ := os.ReadFile(filepath.Join(bindir, "git.log"))
		t.Errorf("--no-sync should skip git invocations, got log:\n%s", log)
	}
}

func TestPreflightOpenBridgeNoSyncEnvSkipsSync(t *testing.T) {
	root := writeFakeRepos(t)
	cache := t.TempDir()
	bindir := t.TempDir()
	writeFakeGit(t, bindir)

	cmd := bridgeCmd("__preflight", "open", "bridge", "--agent", "claude")
	cmd.Env = append(envWithout("TMUX"),
		"BRIDGE_REPOS_ROOT="+root,
		"XDG_CACHE_HOME="+cache,
		"PATH="+bindir+":"+os.Getenv("PATH"),
		"BRIDGE_NO_SYNC=1",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(bindir, "git.log")); !os.IsNotExist(err) {
		log, _ := os.ReadFile(filepath.Join(bindir, "git.log"))
		t.Errorf("BRIDGE_NO_SYNC=1 should skip git invocations, got log:\n%s", log)
	}
}

// --- relabel hook install integration (#85) ---

func TestPreflightOpenClaudeInstallsRelabelHook(t *testing.T) {
	root := writeFakeRepos(t)
	cache := t.TempDir()
	configDir := t.TempDir()
	bridgeCache := t.TempDir()
	cmd := bridgeCmd("__preflight", "open", "bridge", "--agent", "claude")
	cmd.Env = append(envWithout("TMUX"),
		"BRIDGE_REPOS_ROOT="+root,
		"XDG_CACHE_HOME="+cache,
		"CLAUDE_CONFIG_DIR="+configDir,
		"BRIDGE_CACHE="+bridgeCache,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}

	label, err := os.ReadFile(filepath.Join(configDir, "bridge-label"))
	if err != nil {
		t.Fatalf("read bridge-label: %v", err)
	}
	if string(label) != "bridge" {
		t.Errorf("label = %q, want %q", label, "bridge")
	}

	settings, err := os.ReadFile(filepath.Join(configDir, "settings.json"))
	if err != nil {
		t.Fatalf("read settings.json: %v", err)
	}
	s := string(settings)
	if !strings.Contains(s, `"SessionStart"`) {
		t.Errorf("settings.json missing SessionStart entry: %s", s)
	}
	if !strings.Contains(s, `"matcher": "clear"`) {
		t.Errorf("settings.json missing matcher 'clear': %s", s)
	}
	if !strings.Contains(s, "relabel.sh 0") {
		t.Errorf("settings.json missing relabel.sh command: %s", s)
	}

	scriptPath := filepath.Join(bridgeCache, "hooks", "relabel.sh")
	if fi, err := os.Stat(scriptPath); err != nil {
		t.Errorf("extracted script missing: %v", err)
	} else if fi.Mode().Perm()&0o100 == 0 {
		t.Errorf("extracted script not executable: %v", fi.Mode())
	}
}

func TestPreflightOpenClaudeWorktreeLabelIncludesWorktree(t *testing.T) {
	root := writeFakeRepos(t)
	cache := t.TempDir()
	configDir := t.TempDir()
	bridgeCache := t.TempDir()
	cmd := bridgeCmd("__preflight", "open", "bridge", "-w", "feature-x", "--agent", "claude")
	cmd.Env = append(envWithout("TMUX"),
		"BRIDGE_REPOS_ROOT="+root,
		"XDG_CACHE_HOME="+cache,
		"CLAUDE_CONFIG_DIR="+configDir,
		"BRIDGE_CACHE="+bridgeCache,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	label, err := os.ReadFile(filepath.Join(configDir, "bridge-label"))
	if err != nil {
		t.Fatalf("read bridge-label: %v", err)
	}
	if string(label) != "bridge [feature-x]" {
		t.Errorf("label = %q, want %q", label, "bridge [feature-x]")
	}
}

func TestPreflightOpenNonClaudeAgentSkipsRelabelHook(t *testing.T) {
	root := writeFakeRepos(t)
	cache := t.TempDir()
	configDir := t.TempDir()
	bridgeCache := t.TempDir()
	cmd := bridgeCmd("__preflight", "open", "bridge", "--agent", "code")
	cmd.Env = append(envWithout("TMUX"),
		"BRIDGE_REPOS_ROOT="+root,
		"XDG_CACHE_HOME="+cache,
		"CLAUDE_CONFIG_DIR="+configDir,
		"BRIDGE_CACHE="+bridgeCache,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(configDir, "bridge-label")); !os.IsNotExist(err) {
		t.Errorf("non-claude agent unexpectedly wrote bridge-label (err=%v)", err)
	}
	if _, err := os.Stat(filepath.Join(configDir, "settings.json")); !os.IsNotExist(err) {
		t.Errorf("non-claude agent unexpectedly wrote settings.json (err=%v)", err)
	}
}

func TestPreflightOpenNonClaudeAgentUnchanged(t *testing.T) {
	// A non-claude agent (code) must not get -n injected.
	root := writeFakeRepos(t)
	cache := t.TempDir()
	cmd := bridgeCmd("__preflight", "open", "bridge", "--agent", "code")
	cmd.Env = append(envWithout("TMUX"),
		"BRIDGE_REPOS_ROOT="+root,
		"XDG_CACHE_HOME="+cache,
	)
	out, _ := cmd.CombinedOutput()
	s := strings.TrimSpace(string(out))
	if strings.Contains(s, " -n ") {
		t.Errorf("non-claude agent unexpectedly got -n: %s", s)
	}
}

func TestPreflightOpenTermFallback(t *testing.T) {
	if _, err := exec.LookPath("infocmp"); err != nil {
		t.Skip("infocmp not installed; fallback can't trigger")
	}
	root := writeFakeRepos(t)
	cache := t.TempDir()
	// "bridge" is one of the fake repos created by writeFakeRepos
	// (github/freaxnx01/public/bridge).
	cmd := bridgeCmd("__preflight", "open", "bridge", "--agent", "claude")
	// Scrub TMUX so we exercise the non-nested launch path (direct `tmux
	// new-session`) deterministically. Inheriting an ambient TMUX — e.g. when
	// the suite runs inside a tmux session — would route through
	// LaunchArgvNested (`sh -c '… tmux …'`) and break the assertion below,
	// even though the fallback itself is applied identically either way.
	cmd.Env = append(envWithout("TMUX"),
		"BRIDGE_REPOS_ROOT="+root,
		"XDG_CACHE_HOME="+cache,
		"BRIDGE_NO_SYNC=1",
		"TERM=definitely-not-a-real-terminfo-xyz",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	s := string(out)
	if !strings.Contains(s, "exec:env TERM=xterm-256color tmux") {
		t.Errorf("expected env-prefixed exec directive, got:\n%s", s)
	}
}
