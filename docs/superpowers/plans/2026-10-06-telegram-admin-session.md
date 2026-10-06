# Telegram Admin Session (`bridge launch` + bridge-operator skill) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let a Telegram-connected Claude Code "admin" session on agent-dev steer bridge — read status, and start new Claude sessions (repo / worktree / `--rc`) the way `bridge nav` does — without a TTY.

**Architecture:** A new shim-less verb `bridge launch` reuses the exact repo/worktree/agent/slot resolution of `bridge <repo>` (extracted from `preflightOpen` into a shared helper) but runs `tmux new-session -d` itself instead of emitting an exec directive, then reports JSON (slot, dir, RC URL scraped best-effort from the pane). A `bridge-operator` skill teaches the admin session which bridge commands to call and the guardrails; a small start script runs that session in tmux with `CLAUDE_CONFIG_DIR=~/.claude-s0` and the Telegram channel plugin.

**Tech Stack:** Go (cobra), tmux, bash, Claude Code skills + `telegram@claude-plugins-official` channel plugin.

**Spec:** No separate spec doc — design agreed in-session 2026-10-06 (this plan's Goal/Architecture are the spec). Key decisions: CLI via skill (not REST — `/api/*` is read-only); detached launch shares nav/CLI resolution; attach is replaced by the Remote Control URL; Telegram bot = existing `@agent_dev_ctl_bot` (Passbolt `bb503b24-f92a-455c-8579-0fb975161940`).

## Global Constraints

- `bridge launch` is unix-only (`//go:build !windows`), like `internal/launcher/tmux.go`.
- `bridge <repo>` / `bridge open` / `__preflight` behaviour must not change (all existing `cmd/bridge` tests stay green).
- No secrets in the repo: the bot token lives only in `~/.claude-s0/channels/telegram/.env` (mode 0600), sourced from Passbolt.
- Admin session tmux name: `bridge-admin` (the existing `agent-dev-admin` session is something else — don't reuse it).
- Run tests with `go test ./...` from the worktree root; `cmd/bridge` tests build the binary once (`bridgeBin`) and drive it via `bridgeCmd(...)`.

## Review Focus

1. **Session already running** — `bridge launch` on a live slot must not start a second tmux session nor pull the tree; it reports `already_running: true`. Test in Task 3.
2. **Ambiguous / unknown repo name** — exit 2 with a message listing candidates, no tmux call. Test in Task 3.
3. **`--rc` with a non-claude agent** — reject with exit 2 (only claude has `--remote-control`). Test in Task 3.
4. **RC URL never appears** (RC disabled for the account, slow start) — launch still succeeds, `rc_url` omitted, stderr note. Test in Task 3.
5. **No agent configured** (no `--agent`, no `BRIDGE_DEFAULT_AGENT`) — exit 2 with a hint instead of silently doing nothing. Test in Task 3.

---

### Task 1: Detached tmux launch argv

**Files:**
- Modify: `internal/launcher/tmux.go` (add method after `LaunchArgvNested`)
- Test: `internal/launcher/tmux_test.go`

**Interfaces:**
- Produces: `func (Tmux) LaunchArgvDetached(slot, dir string, agent agents.AgentSpec) ([]string, error)` — returns `["sh","-c","tmux has-session -t S 2>/dev/null || tmux new-session -d -s S -c DIR CMD..."]`. Not added to the `Launcher` interface (Windows has no equivalent).

- [ ] **Step 1: Write the failing test** (append to `internal/launcher/tmux_test.go`)

```go
func TestTmuxLaunchArgvDetached(t *testing.T) {
	l := &Tmux{}
	got, err := l.LaunchArgvDetached("bridge-wt-x", "/repo/.worktrees/x",
		agents.AgentSpec{Name: "claude", Bin: "claude", Args: []string{"-n", "bridge [x]", "--remote-control"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0] != "sh" || got[1] != "-c" {
		t.Fatalf("want sh -c <body>, got %v", got)
	}
	body := got[2]
	for _, want := range []string{
		"tmux has-session -t bridge-wt-x 2>/dev/null || ",
		"tmux new-session -d -s bridge-wt-x -c /repo/.worktrees/x claude -n 'bridge [x]' --remote-control",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in %q", want, body)
		}
	}
	if strings.Contains(body, "switch-client") || strings.Contains(body, "attach") {
		t.Errorf("detached launch must not attach/switch: %q", body)
	}
}

func TestTmuxLaunchArgvDetachedValidates(t *testing.T) {
	l := &Tmux{}
	if _, err := l.LaunchArgvDetached("", "/d", agents.AgentSpec{Bin: "claude"}); err == nil {
		t.Error("empty slot: want error")
	}
	if _, err := l.LaunchArgvDetached("s", "", agents.AgentSpec{Bin: "claude"}); err == nil {
		t.Error("empty dir: want error")
	}
	if _, err := l.LaunchArgvDetached("s", "/d", agents.AgentSpec{}); err == nil {
		t.Error("empty bin: want error")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/launcher/ -run Detached -v`
Expected: FAIL — `l.LaunchArgvDetached undefined`

- [ ] **Step 3: Implement** (in `internal/launcher/tmux.go`, after `LaunchArgvNested`)

```go
// LaunchArgvDetached returns argv that creates the session in the background
// if it doesn't exist yet and returns immediately — no attach, no
// switch-client. For non-interactive callers (the Telegram admin session via
// `bridge launch`) that have no terminal to hand over.
func (Tmux) LaunchArgvDetached(slot, dir string, agent agents.AgentSpec) ([]string, error) {
	if slot == "" {
		return nil, errors.New("launcher: empty slot")
	}
	if dir == "" {
		return nil, errors.New("launcher: empty dir")
	}
	if agent.Bin == "" {
		return nil, errors.New("launcher: agent has no Bin")
	}
	innerParts := append([]string{agent.Bin}, agent.Args...)
	body := fmt.Sprintf(
		"tmux has-session -t %s 2>/dev/null || tmux new-session -d -s %s -c %s %s",
		shellQuote(slot), shellQuote(slot), shellQuote(dir), joinShellQuoted(innerParts),
	)
	return []string{"sh", "-c", body}, nil
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/launcher/ -v`
Expected: PASS (all, including existing)

- [ ] **Step 5: Commit**

```bash
git add internal/launcher/tmux.go internal/launcher/tmux_test.go
git commit -m "feat(launcher): add detached tmux launch argv"
```

---

### Task 2: Extract shared launch resolution from `preflightOpen`

Behaviour-preserving refactor so `bridge launch` (Task 3) resolves repo / worktree / agent / slot exactly like `bridge <repo>`.

**Files:**
- Create: `cmd/bridge/launch_target.go`
- Modify: `cmd/bridge/preflight.go` (`preflightOpen`, lines ~192-307)
- Test: `cmd/bridge/launch_target_test.go`

**Interfaces:**
- Produces:

```go
type launchTarget struct {
	Repo            core.Repo
	Worktree        string // "" = main checkout
	WorkDir         string
	WorktreeCreated bool
	Slot            string
	AgentName       string
	Spec            agents.AgentSpec
	HasAgent        bool // false: no --agent and no BRIDGE_DEFAULT_AGENT
}

// errRepoLookup is returned for unknown/ambiguous names; Error() is the
// user-facing message (callers print it and exit 2).
type errRepoLookup struct{ msg string }

func resolveLaunchTarget(name, worktree, agentName string) (launchTarget, error)
func finalizeLaunch(t *launchTarget, noSync bool) // -n label, relabel hook, pre-launch sync, slot upsert
```

- [ ] **Step 1: Write the failing test** (`cmd/bridge/launch_target_test.go`) — unit-level, runs in-process, so set env with `t.Setenv`.

```go
package main

import (
	"errors"
	"testing"
)

func TestResolveLaunchTargetMainCheckout(t *testing.T) {
	root := writeFakeRepos(t)
	t.Setenv("BRIDGE_REPOS_ROOT", root)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("BRIDGE_DEFAULT_AGENT", "")
	tg, err := resolveLaunchTarget("bridge", "", "claude")
	if err != nil {
		t.Fatal(err)
	}
	if tg.Slot != "bridge" || tg.AgentName != "claude" || !tg.HasAgent || tg.Spec.Bin == "" {
		t.Errorf("unexpected target: %+v", tg)
	}
	if tg.WorkDir != tg.Repo.Path {
		t.Errorf("workdir %q != repo path %q", tg.WorkDir, tg.Repo.Path)
	}
}

func TestResolveLaunchTargetNoAgent(t *testing.T) {
	root := writeFakeRepos(t)
	t.Setenv("BRIDGE_REPOS_ROOT", root)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("BRIDGE_DEFAULT_AGENT", "")
	tg, err := resolveLaunchTarget("bridge", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if tg.HasAgent {
		t.Errorf("want HasAgent=false, got %+v", tg)
	}
}

func TestResolveLaunchTargetUnknownRepo(t *testing.T) {
	root := writeFakeRepos(t)
	t.Setenv("BRIDGE_REPOS_ROOT", root)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	_, err := resolveLaunchTarget("nope", "", "claude")
	var le errRepoLookup
	if !errors.As(err, &le) {
		t.Fatalf("want errRepoLookup, got %v", err)
	}
}

func TestResolveLaunchTargetUnknownAgent(t *testing.T) {
	root := writeFakeRepos(t)
	t.Setenv("BRIDGE_REPOS_ROOT", root)
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	if _, err := resolveLaunchTarget("bridge", "", "no-such-agent"); err == nil {
		t.Fatal("want error for unknown agent")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/bridge/ -run ResolveLaunchTarget -v`
Expected: FAIL — `undefined: resolveLaunchTarget`

- [ ] **Step 3: Implement `cmd/bridge/launch_target.go`** — move the body of `preflightOpen` from repo lookup through slot upsert here, returning errors instead of `os.Exit`.

```go
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/freaxnx01/bridge/internal/agents"
	"github.com/freaxnx01/bridge/internal/core"
	"github.com/freaxnx01/bridge/internal/store"
	worktreepkg "github.com/freaxnx01/bridge/internal/worktree"
)

// launchTarget is everything needed to start (or reattach) an agent session
// for a repo — shared by `bridge <repo>` (preflightOpen, which emits an exec
// directive for the shim) and `bridge launch` (which runs tmux detached).
type launchTarget struct {
	Repo            core.Repo
	Worktree        string
	WorkDir         string
	WorktreeCreated bool
	Slot            string
	AgentName       string
	Spec            agents.AgentSpec
	HasAgent        bool
}

// errRepoLookup marks unknown/ambiguous repo names; callers print it and exit 2.
type errRepoLookup struct{ msg string }

func (e errRepoLookup) Error() string { return e.msg }

func resolveLaunchTarget(name, worktree, agentName string) (launchTarget, error) {
	repos, err := reposWithMeta()
	if err != nil {
		return launchTarget{}, err
	}
	repo, ok := findRepoByName(repos, name)
	if !ok {
		matches := findReposByKeyword(repos, name)
		switch len(matches) {
		case 1:
			repo = matches[0]
		case 0:
			return launchTarget{}, errRepoLookup{fmt.Sprintf("bridge: unknown repo %q", name)}
		default:
			names := make([]string, len(matches))
			for i, m := range matches {
				names[i] = m.Name
			}
			return launchTarget{}, errRepoLookup{fmt.Sprintf("bridge: %q is ambiguous (%d matches): %s",
				name, len(matches), strings.Join(names, ", "))}
		}
	}
	_ = store.MRUTouch(filepath.Join(cacheRoot(), "mru"), repo.Path)

	t := launchTarget{Repo: repo, Worktree: worktree, WorkDir: repo.Path}
	// With -w, consult `git worktree list --porcelain` so an existing worktree
	// is found wherever it lives; otherwise create `<repo>/.worktrees/<wt>`.
	// Non-git repo / git failure falls back to the bare convention path.
	if worktree != "" {
		if dir, created, werr := worktreepkg.Resolve(worktreepkg.ExecRunner{}, repo.Path, worktree); werr == nil {
			t.WorkDir, t.WorktreeCreated = dir, created
			if created {
				fmt.Fprintf(os.Stderr, "bridge: created worktree %s\n", dir)
			}
		} else {
			t.WorkDir = filepath.Join(repo.Path, ".worktrees", worktree)
			fmt.Fprintf(os.Stderr, "bridge: worktree resolve failed (%v); using %s\n", werr, t.WorkDir)
		}
	}

	// Explicit --agent wins; otherwise BRIDGE_DEFAULT_AGENT (+ its args).
	if agentName != "" {
		spec, err := agents.Resolve(agentName)
		if err != nil {
			return launchTarget{}, err
		}
		t.Spec, t.AgentName, t.HasAgent = spec, agentName, true
	} else if spec, ok := resolveDefaultAgent(); ok {
		t.Spec, t.AgentName, t.HasAgent = spec, spec.Name, true
	}
	t.Slot = slotIDFor(repo, worktree)
	return t, nil
}

// finalizeLaunch applies the side effects every agent launch shares: the
// claude `-n` display label + relabel hook, the best-effort pre-launch sync
// (skipped for a live slot), and the slot-registry upsert (non-fatal).
func finalizeLaunch(t *launchTarget, noSync bool) {
	t.Spec = withClaudeName(t.Spec, t.Repo, t.Worktree)
	ensureClaudeRelabel(t.Spec, t.Repo, t.Worktree)
	maybePreLaunchSync(t.WorkDir, t.Slot, noSync)
	if err := core.UpsertSlot(filepath.Join(cacheRoot(), "slots.json"), core.Slot{
		ID:       t.Slot,
		Repo:     t.Repo.Name,
		Worktree: t.Worktree,
		Agent:    t.AgentName,
		Created:  time.Now().UTC(),
	}); err != nil {
		fmt.Fprintf(os.Stderr, "warning: slot upsert failed: %v\n", err)
	}
}
```

- [ ] **Step 4: Rewrite `preflightOpen`** (keep the arg-parsing loop as is; replace everything after `if name == "" { return shellbridge.EmitNoop(out) }` with):

```go
	t, err := resolveLaunchTarget(name, worktree, agentName)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		if _, ok := err.(errRepoLookup); ok {
			os.Exit(2)
		}
		if agentName != "" { // unknown --agent: previous behaviour was exit 2
			os.Exit(2)
		}
		return err
	}
	if !t.HasAgent {
		return shellbridge.EmitCD(out, t.WorkDir)
	}
	finalizeLaunch(&t, noSync)
	l := launcher.New()
	var argv []string
	if os.Getenv("TMUX") != "" {
		// Already inside tmux: nesting `tmux new-session -A` fails, so use the
		// nested launcher that creates-detached-then-switches the current client.
		argv, err = l.LaunchArgvNested(t.Slot, t.WorkDir, t.Spec)
	} else {
		argv, err = l.LaunchArgv(t.Slot, t.WorkDir, t.Spec)
	}
	if err != nil {
		return err
	}
	return emitLaunch(out, argv)
```

Note: the old code printed `bridge: %v` for an unknown agent; `agents.Resolve`'s error is printed as-is now — check `TestPreflightOpen*` still pass; if one asserts the `bridge: ` prefix, wrap: `fmt.Errorf("bridge: %w", err)` in `resolveLaunchTarget`'s agent branch. Remove now-unused imports from `preflight.go` (`worktreepkg`, `time` if unused — `go build` will tell).

- [ ] **Step 5: Run all cmd tests**

Run: `go vet ./cmd/bridge/ && go test ./cmd/bridge/ 2>&1 | tail -20`
Expected: PASS — new tests and every existing `TestPreflightOpen*` test.

- [ ] **Step 6: Commit**

```bash
git add cmd/bridge/launch_target.go cmd/bridge/launch_target_test.go cmd/bridge/preflight.go
git commit -m "refactor(cmd): extract launch target resolution from preflightOpen"
```

---

### Task 3: `bridge launch` verb

**Files:**
- Create: `cmd/bridge/launch.go` (`//go:build !windows`)
- Test: `cmd/bridge/launch_test.go` (`//go:build !windows`)
- Modify: `README.md` (usage block, after the `bridge <name> --rc` line)

**Interfaces:**
- Consumes: `resolveLaunchTarget`, `finalizeLaunch`, `errRepoLookup` (Task 2); `launcher.Tmux{}.LaunchArgvDetached` (Task 1); `sessionLive(slot)` (preflight.go); `emitJSON` (output.go).
- Produces: CLI `bridge launch <repo> [-w wt] [--agent a] [--rc] [--rc-wait 20s] [--no-sync] [--json]`; JSON:

```json
{"slot":"bridge-wt-x","repo":"bridge","worktree":"x","dir":"/…/.worktrees/x","agent":"claude",
 "worktree_created":true,"already_running":false,"rc_url":"https://claude.ai/code/…"}
```

Test strategy: a fake `tmux` script on `PATH` logs its argv to a file, answers `list-sessions` from `$FAKE_TMUX_LIVE`, and prints `$FAKE_TMUX_PANE` for `capture-pane`.

- [ ] **Step 1: Write the failing tests** (`cmd/bridge/launch_test.go`)

```go
//go:build !windows

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeTmux puts a stub tmux on PATH. It logs every invocation to
// <dir>/tmux.log, lists $FAKE_TMUX_LIVE (pipe format) for list-sessions,
// prints $FAKE_TMUX_PANE for capture-pane, and fails has-session so the
// detached `sh -c` body proceeds to new-session.
func fakeTmux(t *testing.T) (binDir, logPath string) {
	t.Helper()
	binDir = t.TempDir()
	logPath = filepath.Join(binDir, "tmux.log")
	script := `#!/bin/sh
echo "$*" >> "` + logPath + `"
case "$1" in
  list-sessions) [ -n "$FAKE_TMUX_LIVE" ] && printf '%s\n' "$FAKE_TMUX_LIVE" && exit 0; exit 1 ;;
  capture-pane) printf '%s\n' "$FAKE_TMUX_PANE"; exit 0 ;;
  has-session) exit 1 ;;
esac
exit 0
`
	if err := os.WriteFile(filepath.Join(binDir, "tmux"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return binDir, logPath
}

func launchEnv(t *testing.T, binDir string, extra ...string) []string {
	root := writeFakeRepos(t)
	env := append(envWithout("TMUX", "BRIDGE_DEFAULT_AGENT", "BRIDGE_DEFAULT_AGENT_ARGS", "PATH"),
		"PATH="+binDir+":"+os.Getenv("PATH"),
		"BRIDGE_REPOS_ROOT="+root,
		"XDG_CACHE_HOME="+t.TempDir(),
		"BRIDGE_NO_SYNC=1",
	)
	return append(env, extra...)
}

func TestLaunchStartsDetachedSessionJSON(t *testing.T) {
	bin, logPath := fakeTmux(t)
	cmd := bridgeCmd("launch", "bridge", "--agent", "claude", "--json")
	cmd.Env = launchEnv(t, bin)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	var res launchResult
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatalf("json: %v\n%s", err, out)
	}
	if res.Slot != "bridge" || res.Agent != "claude" || res.AlreadyRunning {
		t.Errorf("unexpected result: %+v", res)
	}
	log, _ := os.ReadFile(logPath)
	if !strings.Contains(string(log), "new-session -d -s bridge") {
		t.Errorf("tmux new-session -d not called; log:\n%s", log)
	}
	if strings.Contains(string(log), "attach") || strings.Contains(string(log), "switch-client") {
		t.Errorf("launch must not attach; log:\n%s", log)
	}
}

func TestLaunchAlreadyRunningDoesNotStartAgain(t *testing.T) {
	bin, logPath := fakeTmux(t)
	cmd := bridgeCmd("launch", "bridge", "--agent", "claude", "--json")
	cmd.Env = launchEnv(t, bin, "FAKE_TMUX_LIVE=bridge|0|1700000000|1700000000")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	var res launchResult
	_ = json.Unmarshal(out, &res)
	if !res.AlreadyRunning {
		t.Errorf("want already_running=true: %s", out)
	}
	log, _ := os.ReadFile(logPath)
	if strings.Contains(string(log), "new-session") {
		t.Errorf("must not create a session when live; log:\n%s", log)
	}
}

func TestLaunchRCScrapesURL(t *testing.T) {
	bin, _ := fakeTmux(t)
	cmd := bridgeCmd("launch", "bridge", "--agent", "claude", "--rc", "--rc-wait", "2s", "--json")
	cmd.Env = launchEnv(t, bin, "FAKE_TMUX_PANE=  Remote Control: https://claude.ai/code/session_01ABCxyz  ")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	var res launchResult
	_ = json.Unmarshal(out, &res)
	if res.RCURL != "https://claude.ai/code/session_01ABCxyz" {
		t.Errorf("rc_url = %q", res.RCURL)
	}
}

func TestLaunchRCURLMissingStillSucceeds(t *testing.T) {
	bin, _ := fakeTmux(t)
	cmd := bridgeCmd("launch", "bridge", "--agent", "claude", "--rc", "--rc-wait", "1s", "--json")
	cmd.Env = launchEnv(t, bin, "FAKE_TMUX_PANE=starting…")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("launch must succeed without RC URL: %v\n%s", err, out)
	}
	if strings.Contains(string(out), "rc_url") {
		t.Errorf("rc_url should be omitted: %s", out)
	}
}

func TestLaunchErrorsExit2(t *testing.T) {
	bin, logPath := fakeTmux(t)
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"unknown repo", []string{"launch", "nope", "--agent", "claude"}},
		{"no agent", []string{"launch", "bridge"}},
		{"rc non-claude", []string{"launch", "bridge", "--agent", "code", "--rc"}},
	} {
		cmd := bridgeCmd(tc.args...)
		cmd.Env = launchEnv(t, bin)
		out, err := cmd.CombinedOutput()
		if err == nil {
			t.Errorf("%s: want non-zero exit, got success: %s", tc.name, out)
			continue
		}
		if ee, ok := err.(interface{ ExitCode() int }); ok && ee.ExitCode() != 2 {
			t.Errorf("%s: exit %d, want 2: %s", tc.name, ee.ExitCode(), out)
		}
	}
	if log, _ := os.ReadFile(logPath); strings.Contains(string(log), "new-session") {
		t.Errorf("error paths must not launch; log:\n%s", log)
	}
}
```

- [ ] **Step 2: Run to verify failure**

Run: `go test ./cmd/bridge/ -run TestLaunch -v 2>&1 | tail -20`
Expected: FAIL — `undefined: launchResult` (compile error)

- [ ] **Step 3: Implement `cmd/bridge/launch.go`**

```go
//go:build !windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"time"

	"github.com/spf13/cobra"

	"github.com/freaxnx01/bridge/internal/launcher"
)

var (
	launchWorktree string
	launchAgent    string
	launchRC       bool
	launchRCWait   time.Duration
	launchNoSync   bool
	launchJSON     bool
)

// launchResult is `bridge launch --json` output.
type launchResult struct {
	Slot            string `json:"slot"`
	Repo            string `json:"repo"`
	Worktree        string `json:"worktree,omitempty"`
	Dir             string `json:"dir"`
	Agent           string `json:"agent"`
	WorktreeCreated bool   `json:"worktree_created"`
	AlreadyRunning  bool   `json:"already_running"`
	RCURL           string `json:"rc_url,omitempty"`
}

var launchCmd = &cobra.Command{
	Use:   "launch <repo>",
	Short: "Start an agent session in the background (no attach) — for scripts and the Telegram admin session",
	Long: `launch resolves <repo> (and -w worktree) exactly like ` + "`bridge <repo>`" + `, but
creates the tmux session detached and returns instead of attaching. Idempotent:
a live slot is reported as already_running and left untouched. With --rc the
Remote Control URL is scraped from the pane (best effort, up to --rc-wait).`,
	Args:              cobra.ExactArgs(1),
	RunE:              runLaunch,
	ValidArgsFunction: completeRepoName,
}

func init() {
	launchCmd.Flags().StringVarP(&launchWorktree, "worktree", "w", "", "worktree name (created under .worktrees/ if missing)")
	launchCmd.Flags().StringVar(&launchAgent, "agent", "", "agent (claude|copilot|opencode|code); default BRIDGE_DEFAULT_AGENT")
	launchCmd.Flags().BoolVar(&launchRC, "rc", false, "enable Remote Control (claude only) and report its URL")
	launchCmd.Flags().DurationVar(&launchRCWait, "rc-wait", 20*time.Second, "how long to wait for the Remote Control URL")
	launchCmd.Flags().BoolVar(&launchNoSync, "no-sync", false, "skip the pre-launch git pull")
	launchCmd.Flags().BoolVar(&launchJSON, "json", false, "machine-readable output")
	rootCmd.AddCommand(launchCmd)
}

// rcURLPattern matches the Remote Control link claude prints in its pane.
var rcURLPattern = regexp.MustCompile(`https://claude\.ai/code/[A-Za-z0-9_\-/?=&.]+`)

func runLaunch(cmd *cobra.Command, args []string) error {
	cmd.SilenceUsage = true
	fail := func(format string, a ...any) error {
		fmt.Fprintf(cmd.ErrOrStderr(), format+"\n", a...)
		os.Exit(2)
		return nil
	}
	t, err := resolveLaunchTarget(args[0], launchWorktree, launchAgent)
	if err != nil {
		return fail("%v", err)
	}
	if !t.HasAgent {
		return fail("bridge: no agent — pass --agent or set BRIDGE_DEFAULT_AGENT")
	}
	if launchRC {
		if t.Spec.Name != "claude" {
			return fail("bridge: --rc needs the claude agent (got %s)", t.Spec.Name)
		}
		if !slices.Contains(t.Spec.Args, "--remote-control") {
			t.Spec.Args = append(slices.Clone(t.Spec.Args), "--remote-control")
		}
	}

	running := sessionLive(t.Slot)
	if !running {
		finalizeLaunch(&t, launchNoSync)
		argv, err := launcher.Tmux{}.LaunchArgvDetached(t.Slot, t.WorkDir, t.Spec)
		if err != nil {
			return err
		}
		if out, err := exec.Command(argv[0], argv[1:]...).CombinedOutput(); err != nil {
			return fmt.Errorf("tmux: %v: %s", err, out)
		}
	}

	res := launchResult{
		Slot: t.Slot, Repo: t.Repo.Name, Worktree: t.Worktree, Dir: t.WorkDir,
		Agent: t.AgentName, WorktreeCreated: t.WorktreeCreated, AlreadyRunning: running,
	}
	if launchRC {
		res.RCURL = waitRCURL(t.Slot, launchRCWait)
		if res.RCURL == "" {
			fmt.Fprintf(cmd.ErrOrStderr(), "bridge: no Remote Control URL seen within %s (session is running)\n", launchRCWait)
		}
	}
	if launchJSON {
		return emitJSON(cmd.OutOrStdout(), res)
	}
	state := "launched"
	if running {
		state = "already running"
	}
	fmt.Fprintf(cmd.OutOrStdout(), "%s: %s (%s)\n", state, t.Slot, t.WorkDir)
	if res.RCURL != "" {
		fmt.Fprintf(cmd.OutOrStdout(), "remote control: %s\n", res.RCURL)
	}
	return nil
}

// waitRCURL polls the slot's pane for the Remote Control link. Best effort:
// returns "" on timeout or any tmux error.
func waitRCURL(slot string, wait time.Duration) string {
	deadline := time.Now().Add(wait)
	for {
		out, err := exec.Command("tmux", "capture-pane", "-p", "-J", "-t", slot, "-S", "-200").Output()
		if err == nil {
			if m := rcURLPattern.FindString(string(out)); m != "" {
				return m
			}
		}
		if time.Now().After(deadline) {
			return ""
		}
		time.Sleep(500 * time.Millisecond)
	}
}
```

- [ ] **Step 4: Run tests**

Run: `go vet ./... && go test ./cmd/bridge/ ./internal/launcher/ 2>&1 | tail -20`
Expected: PASS. Also confirm Windows still compiles: `GOOS=windows go build ./...`.

- [ ] **Step 5: README** — in the usage block after `bridge <name> --rc …` add:

```
bridge launch <name> [-w <wt>] [--rc] [--json]   # start a session detached (no attach) — scripts / Telegram admin; --rc reports the Remote Control URL
```

- [ ] **Step 6: Commit**

```bash
git add cmd/bridge/launch.go cmd/bridge/launch_test.go README.md
git commit -m "feat(cmd): add bridge launch for detached, non-interactive session starts"
```

---

### Task 4: Admin session kit (skill, workspace, start script, docs)

**Files:**
- Create: `admin/skills/bridge-operator/SKILL.md`
- Create: `admin/CLAUDE.md` (role of the admin session)
- Create: `admin/settings.json` (project permissions for the admin workspace)
- Create: `admin/start-admin-session.sh` (executable)
- Create: `docs/admin-session.md`
- Modify: `README.md` (one line under the usage block linking `docs/admin-session.md`)

**Interfaces:**
- Consumes: `bridge launch` (Task 3); existing `bridge status|slots|sessions|issues|dispatch status --json`.
- Produces: workspace `~/.local/share/bridge-admin/` (symlinks: `CLAUDE.md`, `.claude/settings.json`, `.claude/skills/bridge-operator` → repo `admin/…`); tmux session `bridge-admin`.

- [ ] **Step 1: `admin/skills/bridge-operator/SKILL.md`**

```markdown
---
name: bridge-operator
description: Operate bridge on agent-dev from chat (Telegram) — show what's running, start new Claude sessions on a repo/worktree with a Remote Control link, stop sessions on request. Use whenever a message asks about sessions, slots, repos, issues or dispatch, or to start/stop/open a session.
---

# bridge operator

You are the operator console for `bridge` on this host. The user is on their
phone; keep Telegram replies short (a few lines, no tables wider than ~40 chars).

## Read (always fine)

- `bridge status --json` / `bridge status --slim` — overview
- `bridge sessions --json` — live tmux sessions
- `bridge slots --json` — slot registry
- `bridge list` — local repos (names to launch)
- `bridge issues --json` — open issues across forges
- `bridge dispatch status` — dispatcher state

## Start a session (like `bridge nav` → launch)

`bridge launch <repo> [-w <worktree>] --rc --json`

- Resolve vague names with `bridge list` first; if ambiguous, ask which one.
- A worktree is created under `.worktrees/<name>` when missing.
- Reply with slot name + the `rc_url` (that link is how the user "attaches"
  from the phone). If `already_running` is true, say so and still send the link.
- If `rc_url` is missing, say the session is running and the link didn't show
  within the wait; offer `tmux capture-pane -p -t <slot>` to look again.

## Stop a session

Only on an explicit request naming the session. Confirm once in chat
("Stop `<slot>`? It has N min of activity"), then `tmux kill-session -t <slot>`.
Never kill `bridge-admin` (that's you).

## Never

- Delete repos/worktrees (`bridge rm`, `git worktree remove`, `rm -rf`).
- Push, merge, or label issues for dispatch (`bridge dispatch now`) unless the
  user asks for that exact action.
- Paste secrets/tokens into chat.

If you run into blockers, find a solution and update this skill for the future.
```

- [ ] **Step 2: `admin/CLAUDE.md`**

```markdown
# bridge admin session (agent-dev)

This session is the Telegram-reachable operator for `bridge` on agent-dev
(tmux `bridge-admin`, config `~/.claude-s0`, bot `@agent_dev_ctl_bot`).

- Messages arrive from Telegram; answer with the telegram `reply` tool — the
  terminal transcript is not seen by the user.
- Use the `bridge-operator` skill for anything about sessions, repos, issues,
  dispatch. Don't do coding work here: start a session for it instead
  (`bridge launch <repo> -w <wt> --rc`) and hand back the link.
- Memory-heavy commands must run under
  `systemd-run --user --scope -q -p MemoryMax=2G -p MemorySwapMax=0 <cmd>`.
```

- [ ] **Step 3: `admin/settings.json`**

```json
{
  "permissions": {
    "allow": [
      "Bash(bridge status:*)",
      "Bash(bridge sessions:*)",
      "Bash(bridge slots:*)",
      "Bash(bridge list:*)",
      "Bash(bridge issues:*)",
      "Bash(bridge dispatch status:*)",
      "Bash(bridge launch:*)",
      "Bash(tmux list-sessions:*)",
      "Bash(tmux capture-pane:*)",
      "Bash(tmux kill-session:*)"
    ],
    "deny": [
      "Bash(bridge rm:*)",
      "Bash(rm:*)",
      "Bash(git push:*)",
      "Bash(git worktree remove:*)"
    ]
  }
}
```

- [ ] **Step 4: `admin/start-admin-session.sh`** (then `chmod +x`)

```bash
#!/usr/bin/env bash
# Start (or report) the Telegram-reachable bridge admin session in tmux.
# Workspace: ~/.local/share/bridge-admin (symlinks into this repo's admin/).
set -euo pipefail

SESSION="${BRIDGE_ADMIN_SESSION:-bridge-admin}"
CONFIG_DIR="${BRIDGE_ADMIN_CONFIG_DIR:-$HOME/.claude-s0}"
WORKSPACE="${BRIDGE_ADMIN_WORKSPACE:-$HOME/.local/share/bridge-admin}"
HERE="$(cd "$(dirname "$(readlink -f "$0")")" && pwd)"

if tmux has-session -t "$SESSION" 2>/dev/null; then
  echo "already running — attach: tmux attach -t $SESSION"
  exit 0
fi
if [ ! -s "$CONFIG_DIR/channels/telegram/.env" ]; then
  echo "missing $CONFIG_DIR/channels/telegram/.env (TELEGRAM_BOT_TOKEN=…) — see docs/admin-session.md" >&2
  exit 1
fi

mkdir -p "$WORKSPACE/.claude/skills"
ln -sfn "$HERE/CLAUDE.md"                 "$WORKSPACE/CLAUDE.md"
ln -sfn "$HERE/settings.json"             "$WORKSPACE/.claude/settings.json"
ln -sfn "$HERE/skills/bridge-operator"    "$WORKSPACE/.claude/skills/bridge-operator"

tmux new-session -d -s "$SESSION" -c "$WORKSPACE" \
  env CLAUDE_CONFIG_DIR="$CONFIG_DIR" \
  claude -n bridge-admin --channels plugin:telegram@claude-plugins-official
echo "started $SESSION — attach: tmux attach -t $SESSION"
```

- [ ] **Step 5: Verify the script statically**

Run: `bash -n admin/start-admin-session.sh && shellcheck admin/start-admin-session.sh || true; python3 -m json.tool admin/settings.json >/dev/null && echo json-ok`
Expected: no syntax errors, `json-ok` (shellcheck warnings reviewed, none blocking).

- [ ] **Step 6: `docs/admin-session.md`**

```markdown
# Telegram admin session

A Claude Code session on agent-dev that you steer from Telegram
(`@agent_dev_ctl_bot`): see what's running, start sessions like `bridge nav`
does, get a Remote Control link to jump in from the phone.

## One-time setup

1. Token → `~/.claude-s0/channels/telegram/.env` (mode 0600) from Passbolt
   resource "Telegram Bot \"Agent Dev - Control\", agent_dev_ctl_bot":
   `printf 'TELEGRAM_BOT_TOKEN=%s\n' "$(passbolt get resource --id bb503b24-f92a-455c-8579-0fb975161940 --json | jq -r .password)" > ~/.claude-s0/channels/telegram/.env && chmod 600 ~/.claude-s0/channels/telegram/.env`
2. `~/.claude-s0/channels/telegram/access.json` allowlists your Telegram user ID
   (`dmPolicy: allowlist`).
3. Make sure no other process polls the same bot (one poller per token).

## Run

    admin/start-admin-session.sh      # idempotent
    tmux attach -t bridge-admin       # look / approve prompts; Ctrl-B D to leave

## Use (Telegram)

- "what's running?" → `bridge status`
- "start bridge worktree fix-x" → `bridge launch bridge -w fix-x --rc` → link
- "stop bridge-wt-fix-x" → confirms, then kills the tmux session

Permissions are scoped in `admin/settings.json`; anything outside the allow
list prompts in the tmux pane (attach to approve).
```

- [ ] **Step 7: README** — under the usage block add: `Telegram admin session (steer bridge from your phone): see [docs/admin-session.md](docs/admin-session.md).`

- [ ] **Step 8: Commit**

```bash
git add admin docs/admin-session.md README.md
git commit -m "feat(admin): Telegram-steerable admin session kit with bridge-operator skill"
```

---

### Task 5: Live smoke test on agent-dev (controller, not a subagent)

- [ ] **Step 1:** `go build -o /tmp/claude-1000/bridge-tg ./cmd/bridge` then `/tmp/claude-1000/bridge-tg launch bridge -w tg-smoke --agent claude --rc --json` → session `bridge-wt-tg-smoke` exists (`tmux ls`), JSON has `rc_url`. Kill it afterwards: `tmux kill-session -t bridge-wt-tg-smoke`; remove the smoke worktree with `git worktree remove .worktrees/tg-smoke` (main checkout).
- [ ] **Step 2:** Write the bot token per docs/admin-session.md step 1, run `admin/start-admin-session.sh`, DM `@agent_dev_ctl_bot` "what's running?" and "start bridge worktree tg-smoke2" — expect a reply with a Remote Control link.
- [ ] **Step 3:** Record the result in the PR description.
