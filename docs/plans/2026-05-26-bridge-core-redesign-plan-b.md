# bridge core redesign — Plan B (Phase 2)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship the interactive subcommands (`open`, picker, `rm`, `presence` writes, `sync now`, `sync --auto`, `watch`, `sessions attach`), the `internal/launcher` (tmux/WT), the `internal/agents` spawn layer, the `internal/shellbridge` directive protocol, and the new shell shim — all alongside the existing bash `bridge`. **Phase 3 (the actual `~/.bashrc` cutover) is out of scope for this plan and lands in a separate Plan C.**

**Architecture:** Reuse the Go core from Plan A. Add three new internal packages: `internal/shellbridge` (directive protocol), `internal/launcher` (tmux on Linux, WT on Windows), `internal/agents` (resolve agent name → command line). The shell shim is a tiny (~20 line) wrapper that invokes `bridge __preflight` and acts on its directive. Long-running daemons (`sync --auto`, `watch`) use PID files + signal handling; they share state with one-shot invocations through the same `~/.cache/bridge/` files via flock. Filesystem-as-bus, no sockets.

**Tech Stack:** Go 1.25+, cobra (already in), `log/slog` (stdlib), `gopkg.in/natefinch/lumberjack.v2` (log rotation), `github.com/fsnotify/fsnotify` (watch), external `fzf` binary (picker), external `tmux` (launcher Linux), external `wt.exe` (launcher Windows).

**Spec:** `docs/specs/2026-05-25-bridge-core-redesign-design.md`
**Plan A (already shipped):** `docs/plans/2026-05-25-bridge-core-redesign-plan-a.md` — `v2.0.0-go.0`

---

## File Structure

Plan B creates or modifies:

```
internal/shellbridge/
  directive.go        Directive type + Emit helpers (CD/Exec/Noop)
  directive_test.go

internal/launcher/
  launcher.go         Launcher interface + shared types
  tmux.go             Linux implementation (build tag: !windows)
  tmux_test.go
  wt.go               Windows implementation (build tag: windows)
  wt_smoke_test.go    minimal build-only test (no WT in CI)

internal/agents/
  agents.go           AgentSpec (name, default cmd, env)
  agents_test.go

internal/syncer/
  syncer.go           per-repo git fetch+pull driver
  syncer_test.go      with fake git via PATH override

internal/store/
  mru_writer.go       Append + Touch (writes; reads already in core)
  mru_writer_test.go
  pidfile.go          atomic PID file + IsRunning + Remove
  pidfile_test.go

cmd/bridge/
  preflight.go        hidden __preflight subcommand
  preflight_test.go
  open.go             open subcommand
  open_test.go
  positional.go       root-level positional <name> dispatch
  positional_test.go
  picker.go           no-arg picker (fzf subprocess)
  picker_test.go
  rm.go               rm subcommand
  rm_test.go
  presence_write.go   presence away|back (extends existing presence.go)
  presence_write_test.go
  sessions_attach.go  sessions attach <name>
  sessions_attach_test.go
  sync_now.go         sync now subcommand body (extends existing sync.go)
  sync_now_test.go
  sync_auto.go        sync --auto daemon
  sync_auto_test.go
  watch.go            watch subcommand (foreground + --daemonize + --status + --stop)
  watch_test.go
  logging.go          slog plumbing (-v, -vv, file handler)
  logging_test.go

shims/
  bridge-shim.sh      ≤20 line bash shim (NOT installed yet; that's Phase 3)
  bridge-shim.ps1     ≤20 line powershell shim
  bridge-shim.bats    bats test of directive protocol

docs/
  cli-json-schema.md  extend with new commands' --json shapes

Makefile               add `install-shim` target (does NOT touch ~/.bashrc)
.gitignore             add /sync-auto-test-*, /watch-test-*
```

`bridge.sh` is NOT modified in this plan. `CHANGELOG.md` and `_BRIDGE_VERSION` are NOT touched until Phase 3 — per the spec's sunset rule.

---

## Notes that apply to every task

- TDD: failing test first, watch it fail, implement, watch it pass.
- After each task, run `go test ./...` and confirm green before committing.
- Use Conventional Commits.
- Use `t.TempDir()` and inject paths via env vars (`XDG_CACHE_HOME`, `BRIDGE_REPOS_ROOT`) — never write to the user's real cache during tests.
- For external tool stubs in tests (`fzf`, `tmux`, `wt.exe`, `git`), prepend a fake-binary dir to `PATH` and put a shell script there that prints what the test expects. Pattern is reusable.
- When a test would invoke a daemon (`sync --auto`, `watch --daemonize`), use `--max-iterations N` or `BRIDGE_DAEMON_EXIT_AFTER=1s` env hooks (introduced per-task) so tests don't hang.

---

## Task 1: `internal/shellbridge` — directive type + emitters

**Files:**
- Create: `internal/shellbridge/directive.go`
- Create: `internal/shellbridge/directive_test.go`

- [ ] **Step 1: Write the failing test**

```go
package shellbridge

import (
    "bytes"
    "testing"
)

func TestDirectiveCD(t *testing.T) {
    var buf bytes.Buffer
    if err := EmitCD(&buf, "/home/me/projects/repos/github/me/public/bridge"); err != nil {
        t.Fatal(err)
    }
    got := buf.String()
    want := "cd:/home/me/projects/repos/github/me/public/bridge\n"
    if got != want {
        t.Errorf("got %q want %q", got, want)
    }
}

func TestDirectiveExec(t *testing.T) {
    var buf bytes.Buffer
    if err := EmitExec(&buf, []string{"tmux", "new-session", "-A", "-s", "slot-x"}); err != nil {
        t.Fatal(err)
    }
    got := buf.String()
    want := "exec:tmux new-session -A -s slot-x\n"
    if got != want {
        t.Errorf("got %q want %q", got, want)
    }
}

func TestDirectiveNoop(t *testing.T) {
    var buf bytes.Buffer
    if err := EmitNoop(&buf); err != nil {
        t.Fatal(err)
    }
    if buf.String() != "noop\n" {
        t.Errorf("got %q", buf.String())
    }
}

func TestDirectiveExecRejectsEmpty(t *testing.T) {
    var buf bytes.Buffer
    if err := EmitExec(&buf, nil); err == nil {
        t.Error("expected error on empty argv")
    }
}

func TestDirectiveExecQuotesArgsWithSpaces(t *testing.T) {
    var buf bytes.Buffer
    if err := EmitExec(&buf, []string{"sh", "-c", "echo hi there"}); err != nil {
        t.Fatal(err)
    }
    // Single-quote arguments containing whitespace; embedded single quotes
    // get the standard '\'' escape sequence.
    want := "exec:sh -c 'echo hi there'\n"
    if buf.String() != want {
        t.Errorf("got %q want %q", buf.String(), want)
    }
}

func TestDirectiveExecQuotesArgsWithSingleQuote(t *testing.T) {
    var buf bytes.Buffer
    if err := EmitExec(&buf, []string{"echo", "it's me"}); err != nil {
        t.Fatal(err)
    }
    want := "exec:echo 'it'\\''s me'\n"
    if buf.String() != want {
        t.Errorf("got %q want %q", buf.String(), want)
    }
}
```

- [ ] **Step 2: Run; FAIL.**

`go test ./internal/shellbridge -v`

- [ ] **Step 3: Implement**

Create `internal/shellbridge/directive.go`:
```go
// Package shellbridge encodes the directive protocol between the Go binary
// and the parent shell shim. The binary writes exactly one directive line
// to stdout via __preflight; the shim parses it and changes the parent
// shell's state accordingly.
package shellbridge

import (
    "errors"
    "fmt"
    "io"
    "strings"
)

// EmitCD writes "cd:<path>\n".
func EmitCD(w io.Writer, path string) error {
    if path == "" {
        return errors.New("cd directive requires non-empty path")
    }
    _, err := fmt.Fprintf(w, "cd:%s\n", path)
    return err
}

// EmitExec writes "exec:<shell-quoted argv>\n". The shim does `exec ${argv}`,
// so arguments containing whitespace must be quoted.
func EmitExec(w io.Writer, argv []string) error {
    if len(argv) == 0 {
        return errors.New("exec directive requires non-empty argv")
    }
    quoted := make([]string, len(argv))
    for i, a := range argv {
        quoted[i] = shellQuote(a)
    }
    _, err := fmt.Fprintf(w, "exec:%s\n", strings.Join(quoted, " "))
    return err
}

// EmitNoop writes "noop\n".
func EmitNoop(w io.Writer) error {
    _, err := fmt.Fprintln(w, "noop")
    return err
}

// shellQuote returns s safely quoted for /bin/sh.
// Only quotes when needed; arguments without whitespace or shell metacharacters
// are passed through unchanged.
func shellQuote(s string) string {
    if s == "" {
        return "''"
    }
    safe := true
    for _, r := range s {
        if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') ||
            r == '_' || r == '-' || r == '.' || r == '/' || r == ':' || r == '@' || r == '+' || r == '=' || r == ',') {
            safe = false
            break
        }
    }
    if safe {
        return s
    }
    return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}
```

- [ ] **Step 4: Run; PASS. `go test ./...` green.**

- [ ] **Step 5: Commit**

```bash
git add internal/shellbridge/directive.go internal/shellbridge/directive_test.go
git commit -m "feat(go): shellbridge directive protocol (cd/exec/noop)"
```

---

## Task 2: `cmd/bridge __preflight` hidden subcommand

The shim's only entry point. Receives the same argv the user typed; decides what the parent shell must do.

**Files:**
- Create: `cmd/bridge/preflight.go`
- Create: `cmd/bridge/preflight_test.go`

For Phase 2, `__preflight` only needs to handle: no args (→ noop, picker runs in normal cobra flow), a known repo name (→ TBD by Task 4+ once `open` exists; for now emit noop), a known verb (→ noop, let cobra take over). The dispatcher is grown in later tasks.

- [ ] **Step 1: Write the failing test**

```go
package main

import (
    "os/exec"
    "strings"
    "testing"
)

func TestPreflightNoArgs(t *testing.T) {
    out, err := exec.Command("go", "run", ".", "__preflight").CombinedOutput()
    if err != nil {
        t.Fatalf("run: %v\n%s", err, out)
    }
    s := strings.TrimSpace(string(out))
    if s != "noop" {
        t.Errorf("got %q, want noop", s)
    }
}

func TestPreflightUnknownVerb(t *testing.T) {
    // __preflight for an unknown subcommand must still emit noop —
    // the real cobra invocation will surface the unknown-verb error.
    out, err := exec.Command("go", "run", ".", "__preflight", "list").CombinedOutput()
    if err != nil {
        t.Fatalf("run: %v\n%s", err, out)
    }
    if strings.TrimSpace(string(out)) != "noop" {
        t.Errorf("got %q", out)
    }
}

func TestPreflightIsHidden(t *testing.T) {
    out, err := exec.Command("go", "run", ".", "--help").CombinedOutput()
    if err != nil {
        t.Fatalf("run: %v\n%s", err, out)
    }
    if strings.Contains(string(out), "__preflight") {
        t.Errorf("__preflight should be hidden from --help, got:\n%s", out)
    }
}
```

- [ ] **Step 2: FAIL.**

- [ ] **Step 3: Implement**

Create `cmd/bridge/preflight.go`:
```go
package main

import (
    "github.com/spf13/cobra"

    "github.com/freaxnx01/bridge/internal/shellbridge"
)

var preflightCmd = &cobra.Command{
    Use:    "__preflight [user-args...]",
    Short:  "internal: emit a shell directive for the shim",
    Hidden: true,
    DisableFlagParsing: true, // pass everything through verbatim
    RunE: runPreflight,
}

func init() {
    rootCmd.AddCommand(preflightCmd)
}

func runPreflight(cmd *cobra.Command, args []string) error {
    return dispatchPreflight(cmd.OutOrStdout(), args)
}

// dispatchPreflight inspects the user-typed args and decides what directive
// (if any) the parent shell must perform. For verbs handled entirely inside
// the Go binary (list, slots, status, …), it returns noop. For verbs that
// must change the shell (open, sessions attach, the no-arg picker, a bare
// positional repo name), it emits the corresponding directive.
//
// Phase 2 grows this function task by task. Phase 1 only knows noop.
func dispatchPreflight(out interface{ Write(p []byte) (int, error) }, args []string) error {
    return shellbridge.EmitNoop(out)
}
```

- [ ] **Step 4: PASS. `go test ./...` green.**

- [ ] **Step 5: Commit**

```bash
git add cmd/bridge/preflight.go cmd/bridge/preflight_test.go
git commit -m "feat(go): __preflight hidden subcommand (noop default)"
```

---

## Task 3: Shell shim — `shims/bridge-shim.sh` + bats test

The shim is a tiny wrapper. It is NOT installed to `~/.bashrc` by this plan; that's Phase 3. Here we just ship the file and a bats test that exercises the directive contract through a real subshell.

**Files:**
- Create: `shims/bridge-shim.sh`
- Create: `shims/bridge-shim.bats`

- [ ] **Step 1: Write the shim**

Create `shims/bridge-shim.sh`:
```sh
# bridge-shim.sh — sourced from ~/.bashrc once Phase 3 cuts over.
# Calls the Go binary's __preflight subcommand and acts on its directive.
# Keep this file small (≤20 lines of logic). Anything complex belongs in the binary.

bridge() {
    local directive rc
    directive=$(command bridge-go __preflight "$@")
    rc=$?
    if [ $rc -ne 0 ]; then
        return $rc
    fi
    case "$directive" in
        cd:*)   cd "${directive#cd:}" ;;
        exec:*) exec ${directive#exec:} ;;
        noop)   command bridge-go "$@" ;;
        *)
            printf 'bridge: unknown directive: %s\n' "$directive" >&2
            return 1
            ;;
    esac
}
```

Notes:
- `command bridge-go` bypasses the function. During Phase 2 we always reference `bridge-go` so installing the shim does NOT collide with the bash `bridge` script.
- In Phase 3 cutover, `bridge-go` will be renamed/symlinked to `bridge` (the bash script is removed), and the shim will switch to `command bridge`.
- `exec ${directive#exec:}` deliberately leaves the right-hand side unquoted so the shell re-tokenizes (`sh` handles the quoting we emit).

- [ ] **Step 2: Write the bats test**

Create `shims/bridge-shim.bats`:
```bats
#!/usr/bin/env bats

# Build the Go binary once for the suite.
setup_file() {
    BRIDGE_TEST_DIR=$(mktemp -d)
    export BRIDGE_TEST_DIR
    (cd "$BATS_TEST_DIRNAME/.." && go build -o "$BRIDGE_TEST_DIR/bridge-go" ./cmd/bridge)
    export PATH="$BRIDGE_TEST_DIR:$PATH"
}

teardown_file() {
    rm -rf "$BRIDGE_TEST_DIR"
}

@test "no-arg preflight is noop (shim falls through)" {
    run bash -c "source $BATS_TEST_DIRNAME/bridge-shim.sh; bridge --version"
    [ "$status" -eq 0 ]
    [[ "$output" == *"bridge"* ]]
}

@test "preflight noop calls the binary verbatim" {
    run bash -c "source $BATS_TEST_DIRNAME/bridge-shim.sh; bridge list --help"
    [ "$status" -eq 0 ]
    [[ "$output" == *"List local repos"* ]]
}
```

- [ ] **Step 3: Run bats**

```bash
bats shims/bridge-shim.bats
```

Expect: 2 tests pass.

If bats is not installed: `apt install bats` or `brew install bats-core`.

- [ ] **Step 4: Commit**

```bash
git add shims/bridge-shim.sh shims/bridge-shim.bats
git commit -m "feat(shim): bash shell shim + bats smoke test"
```

---

## Task 4: PowerShell shim — `shims/bridge-shim.ps1`

Analogous to bash shim. No bats test (no Windows CI); we only verify it parses with `pwsh -Command "Get-Command bridge"` if available, otherwise this task is build-time only.

**Files:**
- Create: `shims/bridge-shim.ps1`

- [ ] **Step 1: Create `shims/bridge-shim.ps1`**

```powershell
# bridge-shim.ps1 — dot-sourced from $PROFILE once Phase 3 cuts over.
function bridge {
    $directive = & bridge-go.exe __preflight @args
    if ($LASTEXITCODE -ne 0) { return }
    switch -Regex ($directive) {
        '^cd:(.+)$'   { Set-Location $matches[1] }
        '^exec:(.+)$' {
            $parts = $matches[1] -split ' ', 2
            $cmd, $rest = $parts[0], $parts[1]
            Start-Process -FilePath $cmd -ArgumentList $rest -NoNewWindow -Wait
        }
        '^noop$'      { & bridge-go.exe @args }
        default       {
            Write-Error "bridge: unknown directive: $directive"
        }
    }
}
```

Note: PowerShell does not have a true `exec` replacement; `Start-Process -Wait` is the closest practical equivalent and matches the spec's "launcher" expectation on Windows where `wt.exe` opens a new tab anyway.

- [ ] **Step 2: Lint with pwsh if available, otherwise verify file exists**

```bash
if command -v pwsh >/dev/null; then
    pwsh -NoProfile -NonInteractive -Command "Get-Content shims/bridge-shim.ps1 | Out-Null"
fi
ls -l shims/bridge-shim.ps1
```

- [ ] **Step 3: Commit**

```bash
git add shims/bridge-shim.ps1
git commit -m "feat(shim): powershell shell shim"
```

---

## Task 5: `internal/store/mru_writer.go` — append + touch

Plan A read MRU; now we need to write to it (so `bridge open <name>` records a hit). Append-only with dedup-on-read keeps the file simple.

**Files:**
- Create: `internal/store/mru_writer.go`
- Create: `internal/store/mru_writer_test.go`

- [ ] **Step 1: Write the failing tests**

```go
package store

import (
    "os"
    "path/filepath"
    "strings"
    "testing"
)

func TestMRUTouchAppendsLine(t *testing.T) {
    p := filepath.Join(t.TempDir(), "mru")
    if err := MRUTouch(p, "/a"); err != nil {
        t.Fatal(err)
    }
    if err := MRUTouch(p, "/b"); err != nil {
        t.Fatal(err)
    }
    b, _ := os.ReadFile(p)
    lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
    if len(lines) != 2 || lines[0] != "/a" || lines[1] != "/b" {
        t.Errorf("got %v", lines)
    }
}

func TestMRUTouchCreatesParent(t *testing.T) {
    p := filepath.Join(t.TempDir(), "nested", "mru")
    if err := MRUTouch(p, "/x"); err != nil {
        t.Fatal(err)
    }
    if _, err := os.Stat(p); err != nil {
        t.Errorf("file not created: %v", err)
    }
}

func TestMRUTouchRejectsEmptyPath(t *testing.T) {
    if err := MRUTouch(filepath.Join(t.TempDir(), "mru"), ""); err == nil {
        t.Error("expected error on empty path")
    }
}

func TestMRUTouchHoldsLockDuringWrite(t *testing.T) {
    // Acquire the MRU lock first, then ensure MRUTouch blocks until released.
    // Done as a basic smoke; deeper concurrency is covered by lock_test.go.
    dir := t.TempDir()
    p := filepath.Join(dir, "mru")
    lock, err := AcquireLock(p + ".lock")
    if err != nil {
        t.Fatal(err)
    }
    done := make(chan error, 1)
    go func() {
        done <- MRUTouch(p, "/a")
    }()
    select {
    case <-done:
        t.Fatal("MRUTouch returned while lock held")
    case <-time.After(100 * time.Millisecond):
        // good, still blocked
    }
    _ = lock.Release()
    if err := <-done; err != nil {
        t.Errorf("MRUTouch after release: %v", err)
    }
}
```

You'll need `import "time"`.

- [ ] **Step 2: Run; FAIL.**

- [ ] **Step 3: Implement**

Create `internal/store/mru_writer.go`:
```go
package store

import (
    "errors"
    "os"
    "path/filepath"
)

// MRUTouch appends path to the MRU file. The most-recently-used path is the
// last line. Concurrent writers serialize via flock on "<file>.lock".
//
// Append-only: dedupe happens at read time (see core.LoadMRU). Keeps writes
// O(1) and crash-safe — no rewrite, so a torn write can only ever drop the
// current append, never corrupt earlier history.
func MRUTouch(path, target string) error {
    if target == "" {
        return errors.New("MRUTouch: empty target")
    }
    if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
        return err
    }
    lock, err := AcquireLock(path + ".lock")
    if err != nil {
        return err
    }
    defer lock.Release()

    f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
    if err != nil {
        return err
    }
    defer f.Close()
    if _, err := f.WriteString(target + "\n"); err != nil {
        return err
    }
    return f.Sync()
}
```

- [ ] **Step 4: PASS. `go test ./...` green.**

- [ ] **Step 5: Commit**

```bash
git add internal/store/mru_writer.go internal/store/mru_writer_test.go
git commit -m "feat(go): store.MRUTouch (append-only writer)"
```

---

## Task 6: `internal/store/pidfile.go` — PID file helpers

Used by `sync --auto` and `watch --daemonize`.

**Files:**
- Create: `internal/store/pidfile.go`
- Create: `internal/store/pidfile_test.go`

- [ ] **Step 1: Tests**

```go
package store

import (
    "os"
    "path/filepath"
    "testing"
)

func TestWritePIDFile(t *testing.T) {
    p := filepath.Join(t.TempDir(), "sync.pid")
    if err := WritePIDFile(p, 12345); err != nil {
        t.Fatal(err)
    }
    pid, err := ReadPIDFile(p)
    if err != nil {
        t.Fatal(err)
    }
    if pid != 12345 {
        t.Errorf("got %d", pid)
    }
}

func TestReadPIDFileMissing(t *testing.T) {
    pid, err := ReadPIDFile(filepath.Join(t.TempDir(), "missing.pid"))
    if err != nil {
        t.Fatal(err)
    }
    if pid != 0 {
        t.Errorf("expected 0 for missing, got %d", pid)
    }
}

func TestIsPIDRunningSelf(t *testing.T) {
    // The current process is always running.
    if !IsPIDRunning(os.Getpid()) {
        t.Error("expected self to be running")
    }
}

func TestIsPIDRunningBogus(t *testing.T) {
    // PID 0 (or extremely high improbable) is not running.
    if IsPIDRunning(0) {
        t.Error("PID 0 should report not running")
    }
    if IsPIDRunning(99999999) {
        t.Error("nonexistent PID should report not running")
    }
}

func TestRemovePIDFile(t *testing.T) {
    p := filepath.Join(t.TempDir(), "x.pid")
    _ = WritePIDFile(p, 1)
    if err := RemovePIDFile(p); err != nil {
        t.Fatal(err)
    }
    if _, err := os.Stat(p); !os.IsNotExist(err) {
        t.Errorf("expected file gone, stat err = %v", err)
    }
    // Removing again is a no-op.
    if err := RemovePIDFile(p); err != nil {
        t.Errorf("second remove: %v", err)
    }
}
```

- [ ] **Step 2: FAIL.**

- [ ] **Step 3: Implement**

Create `internal/store/pidfile.go`:
```go
package store

import (
    "errors"
    "os"
    "strconv"
    "strings"
    "syscall"
)

// WritePIDFile atomically writes pid to path.
func WritePIDFile(path string, pid int) error {
    return AtomicWrite(path, []byte(strconv.Itoa(pid)+"\n"))
}

// ReadPIDFile returns the PID stored at path, or 0 if missing.
func ReadPIDFile(path string) (int, error) {
    b, err := ReadFile(path)
    if err != nil {
        return 0, err
    }
    s := strings.TrimSpace(string(b))
    if s == "" {
        return 0, nil
    }
    return strconv.Atoi(s)
}

// RemovePIDFile deletes path; no error if missing.
func RemovePIDFile(path string) error {
    err := os.Remove(path)
    if err != nil && !errors.Is(err, os.ErrNotExist) {
        return err
    }
    return nil
}

// IsPIDRunning reports whether a process with pid currently exists.
// Cross-platform: on Unix, signal 0 probes existence without affecting the
// target; on Windows, FindProcess succeeds for non-running PIDs so we sample
// with OpenProcess via syscall.
func IsPIDRunning(pid int) bool {
    if pid <= 0 {
        return false
    }
    p, err := os.FindProcess(pid)
    if err != nil {
        return false
    }
    err = p.Signal(syscall.Signal(0))
    return err == nil
}
```

Windows note: `syscall.Signal(0)` is a no-op on Windows that returns `nil` for any handle FindProcess returned. If you observe false positives in Windows CI, swap to a build-tagged file that opens with PROCESS_QUERY_LIMITED_INFORMATION. For Plan B we don't have Windows CI, so this is good enough.

- [ ] **Step 4: PASS, `go test ./...`**

- [ ] **Step 5: Commit**

```bash
git add internal/store/pidfile.go internal/store/pidfile_test.go
git commit -m "feat(go): store.PIDFile helpers (write/read/remove/IsRunning)"
```

---

## Task 7: `internal/agents` — resolve agent name → command line

For Phase 2 we support: `claude`, `copilot` (alias `gh copilot suggest`? no — `copilot-cli`), `opencode`, `code`. Each agent boils down to: executable + args. Caller adds repo path / worktree as needed.

**Files:**
- Create: `internal/agents/agents.go`
- Create: `internal/agents/agents_test.go`

- [ ] **Step 1: Tests**

```go
package agents

import "testing"

func TestResolveClaude(t *testing.T) {
    s, err := Resolve("claude")
    if err != nil {
        t.Fatal(err)
    }
    if s.Name != "claude" || s.Bin != "claude" || len(s.Args) != 0 {
        t.Errorf("%+v", s)
    }
}

func TestResolveCopilot(t *testing.T) {
    s, err := Resolve("copilot")
    if err != nil {
        t.Fatal(err)
    }
    if s.Bin != "copilot" {
        t.Errorf("%+v", s)
    }
}

func TestResolveOpencode(t *testing.T) {
    s, err := Resolve("opencode")
    if err != nil {
        t.Fatal(err)
    }
    if s.Bin != "opencode" {
        t.Errorf("%+v", s)
    }
}

func TestResolveCode(t *testing.T) {
    // "code" means "open VS Code in the repo". We expect bin=code, args=["."]
    s, err := Resolve("code")
    if err != nil {
        t.Fatal(err)
    }
    if s.Bin != "code" || len(s.Args) != 1 || s.Args[0] != "." {
        t.Errorf("%+v", s)
    }
}

func TestResolveUnknown(t *testing.T) {
    if _, err := Resolve("bogus"); err == nil {
        t.Error("expected error")
    }
}

func TestResolveDefault(t *testing.T) {
    s := Default()
    if s.Name != "claude" {
        t.Errorf("default should be claude, got %s", s.Name)
    }
}
```

- [ ] **Step 2: FAIL → Implement**

Create `internal/agents/agents.go`:
```go
// Package agents resolves a user-facing agent name into the command that
// should run inside the launched session.
package agents

import "fmt"

// AgentSpec describes how to launch an agent.
type AgentSpec struct {
    Name string   // canonical name
    Bin  string   // executable to invoke
    Args []string // base args (caller may append repo-specific args)
}

var registry = map[string]AgentSpec{
    "claude":   {Name: "claude", Bin: "claude"},
    "copilot":  {Name: "copilot", Bin: "copilot"},
    "opencode": {Name: "opencode", Bin: "opencode"},
    "code":     {Name: "code", Bin: "code", Args: []string{"."}},
}

// Resolve returns the spec for name. Returns an error for unknown agents.
func Resolve(name string) (AgentSpec, error) {
    if s, ok := registry[name]; ok {
        return s, nil
    }
    return AgentSpec{}, fmt.Errorf("unknown agent %q (known: claude, copilot, opencode, code)", name)
}

// Default returns the default agent (claude).
func Default() AgentSpec { return registry["claude"] }
```

- [ ] **Step 3: PASS, `go test ./...`**

- [ ] **Step 4: Commit**

```bash
git add internal/agents/agents.go internal/agents/agents_test.go
git commit -m "feat(go): internal/agents (resolve agent name → command)"
```

---

## Task 8: `internal/launcher` — interface + Linux tmux implementation

Launchers turn `(slot, repo path, agent spec)` into a concrete shell command that, when exec'd by the parent shell, lands the user inside their session. For Linux this is `tmux new-session -A -s <slot> -c <path> <agent argv...>`.

**Files:**
- Create: `internal/launcher/launcher.go`
- Create: `internal/launcher/tmux.go`
- Create: `internal/launcher/tmux_test.go`

- [ ] **Step 1: Tests**

```go
package launcher

import (
    "reflect"
    "testing"

    "github.com/freaxnx01/bridge/internal/agents"
)

func TestTmuxLaunchArgv(t *testing.T) {
    l := &Tmux{}
    got, err := l.LaunchArgv("bridge-main", "/home/me/projects/repos/github/me/public/bridge",
        agents.AgentSpec{Name: "claude", Bin: "claude"})
    if err != nil {
        t.Fatal(err)
    }
    want := []string{"tmux", "new-session", "-A", "-s", "bridge-main", "-c",
        "/home/me/projects/repos/github/me/public/bridge", "claude"}
    if !reflect.DeepEqual(got, want) {
        t.Errorf("got %v want %v", got, want)
    }
}

func TestTmuxAttachArgv(t *testing.T) {
    l := &Tmux{}
    got := l.AttachArgv("bridge-main")
    want := []string{"tmux", "attach-session", "-t", "bridge-main"}
    if !reflect.DeepEqual(got, want) {
        t.Errorf("got %v want %v", got, want)
    }
}

func TestTmuxLaunchArgvWithCodeAgent(t *testing.T) {
    // code agent uses args=["."] which should be appended.
    l := &Tmux{}
    got, _ := l.LaunchArgv("slot", "/path", agents.AgentSpec{Name: "code", Bin: "code", Args: []string{"."}})
    want := []string{"tmux", "new-session", "-A", "-s", "slot", "-c", "/path", "code", "."}
    if !reflect.DeepEqual(got, want) {
        t.Errorf("got %v want %v", got, want)
    }
}

func TestTmuxLaunchRejectsEmptySlot(t *testing.T) {
    l := &Tmux{}
    if _, err := l.LaunchArgv("", "/x", agents.AgentSpec{Name: "claude", Bin: "claude"}); err == nil {
        t.Error("expected error on empty slot")
    }
}
```

- [ ] **Step 2: FAIL.**

- [ ] **Step 3: Implement**

Create `internal/launcher/launcher.go`:
```go
// Package launcher constructs the argv that the parent shell should exec
// to land the user inside a session. It does not spawn processes itself —
// it returns argv that the shellbridge directive emits.
package launcher

import "github.com/freaxnx01/bridge/internal/agents"

// Launcher is the cross-platform interface.
type Launcher interface {
    // LaunchArgv returns the argv for creating-and-attaching a session that runs the agent.
    // Idempotent: if a session named slot already exists, must attach to it.
    LaunchArgv(slot, dir string, agent agents.AgentSpec) ([]string, error)
    // AttachArgv returns the argv for attaching to an existing session.
    AttachArgv(slot string) []string
}
```

Create `internal/launcher/tmux.go`:
```go
//go:build !windows

package launcher

import (
    "errors"

    "github.com/freaxnx01/bridge/internal/agents"
)

type Tmux struct{}

func New() Launcher { return &Tmux{} }

func (Tmux) LaunchArgv(slot, dir string, agent agents.AgentSpec) ([]string, error) {
    if slot == "" {
        return nil, errors.New("launcher: empty slot")
    }
    if dir == "" {
        return nil, errors.New("launcher: empty dir")
    }
    if agent.Bin == "" {
        return nil, errors.New("launcher: agent has no Bin")
    }
    argv := []string{"tmux", "new-session", "-A", "-s", slot, "-c", dir, agent.Bin}
    argv = append(argv, agent.Args...)
    return argv, nil
}

func (Tmux) AttachArgv(slot string) []string {
    return []string{"tmux", "attach-session", "-t", slot}
}
```

- [ ] **Step 4: PASS. `go test ./...`.**

- [ ] **Step 5: Commit**

```bash
git add internal/launcher/launcher.go internal/launcher/tmux.go internal/launcher/tmux_test.go
git commit -m "feat(go): internal/launcher + tmux implementation"
```

---

## Task 9: Windows `internal/launcher/wt.go`

Build-tagged Windows implementation. No CI; we verify it cross-compiles.

**Files:**
- Create: `internal/launcher/wt.go`
- Create: `internal/launcher/wt_smoke_test.go`

- [ ] **Step 1: Implement `wt.go`**

```go
//go:build windows

package launcher

import (
    "errors"

    "github.com/freaxnx01/bridge/internal/agents"
)

// WT uses Windows Terminal's command-line interface to open a new tab in the
// repo directory running the agent. Session reuse semantics differ from tmux:
// WT does not support named persistent sessions, so "attach" opens a fresh tab.
type WT struct{}

func New() Launcher { return &WT{} }

func (WT) LaunchArgv(slot, dir string, agent agents.AgentSpec) ([]string, error) {
    if slot == "" || dir == "" || agent.Bin == "" {
        return nil, errors.New("launcher(wt): missing slot/dir/agent")
    }
    argv := []string{"wt.exe", "new-tab", "--title", slot, "-d", dir, agent.Bin}
    argv = append(argv, agent.Args...)
    return argv, nil
}

func (WT) AttachArgv(slot string) []string {
    // WT has no notion of "attach to named session" — opening a new tab is
    // the closest approximation. The user-visible workflow is "wt opens a
    // new tab", which is the same UX they get on launch.
    return []string{"wt.exe", "new-tab", "--title", slot}
}
```

- [ ] **Step 2: Build-only smoke test**

Create `internal/launcher/wt_smoke_test.go`:
```go
//go:build windows

package launcher

import "testing"

func TestWTNew(t *testing.T) {
    l := New()
    if l == nil {
        t.Fatal("nil")
    }
}
```

- [ ] **Step 3: Cross-compile**

```bash
GOOS=windows GOARCH=amd64 go build -o /tmp/bridge.exe ./cmd/bridge
file /tmp/bridge.exe
rm -f /tmp/bridge.exe
```

Expected: PE32+ executable.

- [ ] **Step 4: Commit**

```bash
git add internal/launcher/wt.go internal/launcher/wt_smoke_test.go
git commit -m "feat(go): internal/launcher Windows Terminal implementation"
```

---

## Task 10: Logging plumbing — `cmd/bridge/logging.go`

`log/slog` for the binary. Defaults to silent. `-v` enables INFO to stderr (human text), `-vv` enables DEBUG. Daemons (`sync --auto`, `watch --daemonize`) additionally write JSON-lines to `~/.cache/bridge/bridge.log`, rotated at 10 MB, keep 3.

**Files:**
- Modify: `cmd/bridge/root.go` (add persistent -v/-vv flags, install handler in `PersistentPreRunE`)
- Create: `cmd/bridge/logging.go`
- Create: `cmd/bridge/logging_test.go`
- Add dependency: `gopkg.in/natefinch/lumberjack.v2`

- [ ] **Step 1: Add dependency**

```bash
go get gopkg.in/natefinch/lumberjack.v2@latest
go mod tidy
```

- [ ] **Step 2: Tests**

```go
package main

import (
    "bytes"
    "log/slog"
    "strings"
    "testing"
)

func TestInstallLoggerSilentByDefault(t *testing.T) {
    var buf bytes.Buffer
    h := installLogger(&buf, 0, "") // verbose=0, no file
    slog.SetDefault(slog.New(h))
    slog.Info("hi")
    if buf.Len() != 0 {
        t.Errorf("expected silent, got %q", buf.String())
    }
}

func TestInstallLoggerVerboseEmitsInfo(t *testing.T) {
    var buf bytes.Buffer
    h := installLogger(&buf, 1, "")
    slog.SetDefault(slog.New(h))
    slog.Info("hi")
    if !strings.Contains(buf.String(), "hi") {
        t.Errorf("expected hi in %q", buf.String())
    }
}

func TestInstallLoggerVeryVerboseEmitsDebug(t *testing.T) {
    var buf bytes.Buffer
    h := installLogger(&buf, 2, "")
    slog.SetDefault(slog.New(h))
    slog.Debug("dbg")
    if !strings.Contains(buf.String(), "dbg") {
        t.Errorf("expected dbg in %q", buf.String())
    }
}
```

- [ ] **Step 3: Implement**

Create `cmd/bridge/logging.go`:
```go
package main

import (
    "io"
    "log/slog"

    "gopkg.in/natefinch/lumberjack.v2"
)

// installLogger returns an slog.Handler configured for the given verbosity:
//   0 → silent (discard).
//   1 → INFO to stderrWriter (human text).
//   2 → DEBUG to stderrWriter (human text).
// If logFile != "", a JSON handler is teed to a rotating logfile (10MB / 3 keep)
// at INFO level regardless of stderr verbosity (so daemons always have a record).
func installLogger(stderrWriter io.Writer, verbose int, logFile string) slog.Handler {
    stderrLevel := slog.LevelError + 100 // effectively off
    switch verbose {
    case 1:
        stderrLevel = slog.LevelInfo
    case 2:
        stderrLevel = slog.LevelDebug
    }
    stderrHandler := slog.NewTextHandler(stderrWriter, &slog.HandlerOptions{Level: stderrLevel})

    if logFile == "" {
        return stderrHandler
    }
    lj := &lumberjack.Logger{
        Filename:   logFile,
        MaxSize:    10, // megabytes
        MaxBackups: 3,
    }
    fileHandler := slog.NewJSONHandler(lj, &slog.HandlerOptions{Level: slog.LevelInfo})
    return multiHandler{stderrHandler, fileHandler}
}

// multiHandler dispatches Handle to each child whose Enabled returns true for
// the record's level. Used to tee stderr (verbose-gated) and file (always-on).
type multiHandler []slog.Handler

func (m multiHandler) Enabled(ctx context.Context, l slog.Level) bool {
    for _, h := range m {
        if h.Enabled(ctx, l) {
            return true
        }
    }
    return false
}

func (m multiHandler) Handle(ctx context.Context, r slog.Record) error {
    var firstErr error
    for _, h := range m {
        if h.Enabled(ctx, r.Level) {
            if err := h.Handle(ctx, r.Clone()); err != nil && firstErr == nil {
                firstErr = err
            }
        }
    }
    return firstErr
}

func (m multiHandler) WithAttrs(as []slog.Attr) slog.Handler {
    out := make(multiHandler, len(m))
    for i, h := range m {
        out[i] = h.WithAttrs(as)
    }
    return out
}

func (m multiHandler) WithGroup(name string) slog.Handler {
    out := make(multiHandler, len(m))
    for i, h := range m {
        out[i] = h.WithGroup(name)
    }
    return out
}
```

(Add `import "context"` at the top.)

- [ ] **Step 4: Wire into root.go**

Edit `cmd/bridge/root.go` to add persistent `-v`/`-vv` flags counted into `verboseCount`, and a `PersistentPreRunE` that calls `slog.SetDefault(slog.New(installLogger(os.Stderr, verboseCount, "")))`. Daemons override the file path themselves before their loop starts.

```go
var verboseCount int

func init() {
    rootCmd.PersistentFlags().CountVarP(&verboseCount, "verbose", "v", "increase log verbosity (-v info, -vv debug)")
    rootCmd.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
        slog.SetDefault(slog.New(installLogger(os.Stderr, verboseCount, "")))
        return nil
    }
}
```

(Adds imports `log/slog`, `os`.)

- [ ] **Step 5: PASS, `go test ./...`. Verify `./bridge-go -v list` emits INFO if any code path logs.**

- [ ] **Step 6: Commit**

```bash
git add cmd/bridge/logging.go cmd/bridge/logging_test.go cmd/bridge/root.go go.mod go.sum
git commit -m "feat(go): structured logging with slog + file rotation"
```

---

## Task 11: `bridge open <name>` — explicit form

Looks up a repo by name, validates, writes MRU, returns. In normal cobra flow it prints status; in `__preflight` flow (Task 13) it emits a `cd:` or `exec:` directive.

For Task 11, implement only the `open` subcommand body. Picker (`bridge` no-arg) and positional dispatch land later.

**Files:**
- Create: `cmd/bridge/open.go`
- Create: `cmd/bridge/open_test.go`

- [ ] **Step 1: Tests**

```go
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
    // MRU was touched.
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
    err := cmd.Run()
    if err == nil {
        t.Fatal("expected non-zero exit")
    }
    if ee, ok := err.(*exec.ExitError); ok {
        if ee.ExitCode() != 2 {
            t.Errorf("expected exit 2, got %d", ee.ExitCode())
        }
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
```

- [ ] **Step 2: FAIL → Implement**

Create `cmd/bridge/open.go`:
```go
package main

import (
    "fmt"
    "os"
    "path/filepath"
    "strings"

    "github.com/spf13/cobra"

    "github.com/freaxnx01/bridge/internal/core"
    "github.com/freaxnx01/bridge/internal/store"
)

var (
    openJSON     bool
    openAgent    string
    openWorktree string
    openRC       bool
)

var openCmd = &cobra.Command{
    Use:   "open <name>",
    Short: "Open a repo (creates/attaches an agent session in Phase 2)",
    Args:  cobra.ExactArgs(1),
    RunE:  runOpen,
}

func init() {
    openCmd.Flags().BoolVar(&openJSON, "json", false, "machine-readable output")
    openCmd.Flags().StringVar(&openAgent, "agent", "", "agent to launch (claude|copilot|opencode|code); empty = no auto-launch")
    openCmd.Flags().StringVarP(&openWorktree, "worktree", "w", "", "pass-through worktree name")
    openCmd.Flags().BoolVar(&openRC, "rc", false, "pass-through --remote-control")
    rootCmd.AddCommand(openCmd)
}

func runOpen(cmd *cobra.Command, args []string) error {
    name := args[0]
    repos, err := core.DiscoverRepos(reposRoot())
    if err != nil {
        return fmt.Errorf("discover: %w", err)
    }
    repo, ok := findRepoByName(repos, name)
    if !ok {
        cmd.SilenceUsage = true
        cmd.SilenceErrors = true
        fmt.Fprintf(cmd.ErrOrStderr(), "bridge: unknown repo %q\n", name)
        os.Exit(2)
    }
    // Touch MRU.
    mruPath := filepath.Join(cacheRoot(), "mru")
    if err := store.MRUTouch(mruPath, repo.Path); err != nil {
        // non-fatal
        fmt.Fprintf(cmd.ErrOrStderr(), "warning: MRU touch failed: %v\n", err)
    }
    if openJSON {
        return emitJSON(cmd.OutOrStdout(), repo)
    }
    fmt.Fprintf(cmd.OutOrStdout(), "%s\n", repo.Path)
    return nil
}

// findRepoByName returns the first repo whose Name equals name (case-insensitive).
func findRepoByName(repos []core.Repo, name string) (core.Repo, bool) {
    needle := strings.ToLower(name)
    for _, r := range repos {
        if strings.ToLower(r.Name) == needle {
            return r, true
        }
    }
    return core.Repo{}, false
}
```

- [ ] **Step 3: PASS, `go test ./...`.**

- [ ] **Step 4: Commit**

```bash
git add cmd/bridge/open.go cmd/bridge/open_test.go
git commit -m "feat(go): bridge open <name> (validate + MRU touch + --json)"
```

---

## Task 12: Keyword fallback for `open`

Spec calls for keyword fallback when the exact-name lookup misses.

**Files:**
- Modify: `cmd/bridge/open.go` (add `findRepoByKeyword`)
- Modify: `cmd/bridge/open_test.go` (add test)

- [ ] **Step 1: Add the failing test**

```go
func TestOpenKeywordFallback(t *testing.T) {
    root := writeFakeRepos(t)
    cache := t.TempDir()
    // "br" should match "bridge" via keyword fallback.
    cmd := exec.Command("go", "run", ".", "open", "br", "--json")
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

func TestOpenAmbiguousKeyword(t *testing.T) {
    // Two repos containing "re" → "bridge" and "secret"? Actually
    // writeFakeRepos has "bridge", "secret", "glrepo". "re" matches both
    // bridge ("br") and... actually no. Use "e" which matches all three.
    root := writeFakeRepos(t)
    cache := t.TempDir()
    cmd := exec.Command("go", "run", ".", "open", "e")
    cmd.Env = append(os.Environ(),
        "BRIDGE_REPOS_ROOT="+root,
        "XDG_CACHE_HOME="+cache,
    )
    err := cmd.Run()
    if err == nil {
        t.Fatal("expected non-zero exit for ambiguous match")
    }
}
```

- [ ] **Step 2: FAIL → extend `open.go`**

Replace the lookup block in `runOpen`:
```go
    repo, ok := findRepoByName(repos, name)
    if !ok {
        matches := findReposByKeyword(repos, name)
        switch len(matches) {
        case 1:
            repo = matches[0]
            ok = true
        case 0:
            cmd.SilenceUsage = true
            cmd.SilenceErrors = true
            fmt.Fprintf(cmd.ErrOrStderr(), "bridge: unknown repo %q\n", name)
            os.Exit(2)
        default:
            cmd.SilenceUsage = true
            cmd.SilenceErrors = true
            fmt.Fprintf(cmd.ErrOrStderr(), "bridge: %q is ambiguous (%d matches):\n", name, len(matches))
            for _, m := range matches {
                fmt.Fprintf(cmd.ErrOrStderr(), "  %s\n", m.Name)
            }
            os.Exit(2)
        }
    }
    _ = ok
```

Add the helper:
```go
// findReposByKeyword returns repos whose Name contains the substring (case-insensitive).
func findReposByKeyword(repos []core.Repo, q string) []core.Repo {
    needle := strings.ToLower(q)
    var out []core.Repo
    for _, r := range repos {
        if strings.Contains(strings.ToLower(r.Name), needle) {
            out = append(out, r)
        }
    }
    return out
}
```

- [ ] **Step 3: PASS, `go test ./...`.**

- [ ] **Step 4: Commit**

```bash
git add cmd/bridge/open.go cmd/bridge/open_test.go
git commit -m "feat(go): bridge open keyword fallback + ambiguity error"
```

---

## Task 13: `__preflight` knows about `open` — emits `exec:` directive

Now `__preflight` actually does something. For `bridge __preflight open <name> [flags]`, it resolves the repo, resolves the agent, asks the launcher for argv, and emits an `exec:<argv>` directive.

If `--agent` is omitted, the directive is just `cd:<path>` — the user wanted to land in the directory without spawning a session.

**Files:**
- Modify: `cmd/bridge/preflight.go`
- Modify: `cmd/bridge/preflight_test.go`

- [ ] **Step 1: Add failing tests**

```go
func TestPreflightOpenEmitsCD(t *testing.T) {
    root := writeFakeRepos(t)
    cache := t.TempDir()
    cmd := exec.Command("go", "run", ".", "__preflight", "open", "bridge")
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
    cmd := exec.Command("go", "run", ".", "__preflight", "open", "bridge", "--agent", "claude")
    cmd.Env = append(os.Environ(),
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

func TestPreflightOpenUnknownRepoExits2(t *testing.T) {
    root := writeFakeRepos(t)
    cache := t.TempDir()
    cmd := exec.Command("go", "run", ".", "__preflight", "open", "nope")
    cmd.Env = append(os.Environ(),
        "BRIDGE_REPOS_ROOT="+root,
        "XDG_CACHE_HOME="+cache,
    )
    err := cmd.Run()
    if err == nil {
        t.Fatal("expected exit 2")
    }
    if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() != 2 {
        t.Errorf("exit %d", ee.ExitCode())
    }
}
```

- [ ] **Step 2: FAIL → Implement dispatcher**

Replace `dispatchPreflight` in `cmd/bridge/preflight.go`:
```go
func dispatchPreflight(out interface{ Write(p []byte) (int, error) }, args []string) error {
    if len(args) == 0 {
        return shellbridge.EmitNoop(out)
    }
    if args[0] == "open" {
        return preflightOpen(out, args[1:])
    }
    return shellbridge.EmitNoop(out)
}

func preflightOpen(out interface{ Write(p []byte) (int, error) }, args []string) error {
    // Parse a minimal subset: <name> [--agent X] [-w/--worktree X] [--rc].
    var name, agentName string
    for i := 0; i < len(args); i++ {
        switch args[i] {
        case "--agent":
            if i+1 < len(args) {
                agentName = args[i+1]
                i++
            }
        case "-w", "--worktree":
            if i+1 < len(args) {
                i++ // skip value; carried forward to the binary's real run via noop fallback if needed
            }
        case "--rc", "--remote-control", "--json":
            // ignore in preflight; the binary's normal run handles them
        default:
            if name == "" && !strings.HasPrefix(args[i], "-") {
                name = args[i]
            }
        }
    }
    if name == "" {
        return shellbridge.EmitNoop(out)
    }
    repos, err := core.DiscoverRepos(reposRoot())
    if err != nil {
        return err
    }
    repo, ok := findRepoByName(repos, name)
    if !ok {
        matches := findReposByKeyword(repos, name)
        if len(matches) == 1 {
            repo = matches[0]
        } else {
            // Let the normal `bridge open` (via noop fallthrough in the shim)
            // surface the error to the user.
            fmt.Fprintf(os.Stderr, "bridge: unknown repo %q\n", name)
            os.Exit(2)
        }
    }
    // Touch MRU here too — preflight is the one that actually fires before the shell cd's.
    _ = store.MRUTouch(filepath.Join(cacheRoot(), "mru"), repo.Path)

    if agentName == "" {
        return shellbridge.EmitCD(out, repo.Path)
    }
    spec, err := agents.Resolve(agentName)
    if err != nil {
        fmt.Fprintf(os.Stderr, "bridge: %v\n", err)
        os.Exit(2)
    }
    slot := slotIDFor(repo, "")
    l := launcher.New()
    argv, err := l.LaunchArgv(slot, repo.Path, spec)
    if err != nil {
        return err
    }
    return shellbridge.EmitExec(out, argv)
}

// slotIDFor produces a deterministic tmux session name. Uses repo.Name; if
// worktree is non-empty, appends "-wt-<worktree>".
func slotIDFor(repo core.Repo, worktree string) string {
    id := repo.Name
    if worktree != "" {
        id += "-wt-" + worktree
    }
    return id
}
```

Add imports: `"fmt"`, `"os"`, `"path/filepath"`, `"strings"`, and the new internal packages `core`, `store`, `agents`, `launcher`.

- [ ] **Step 3: PASS, `go test ./...`.**

- [ ] **Step 4: Commit**

```bash
git add cmd/bridge/preflight.go cmd/bridge/preflight_test.go
git commit -m "feat(go): __preflight open emits cd/exec directives"
```

---

## Task 14: Positional `bridge <name>` — alias for `open`

Cobra doesn't natively dispatch unknown-verb-looks-like-a-repo to `open`. Easiest: a `PersistentPreRunE` on `rootCmd` that, before cobra parses, rewrites `os.Args` so a single bare token becomes `open <token>`.

This is also wired through `__preflight`: if the first arg is not a known verb, treat as `open <name>`.

**Files:**
- Create: `cmd/bridge/positional.go`
- Create: `cmd/bridge/positional_test.go`

- [ ] **Step 1: Tests**

```go
package main

import (
    "os"
    "os/exec"
    "strings"
    "testing"
)

func TestPositionalOpensRepo(t *testing.T) {
    root := writeFakeRepos(t)
    cache := t.TempDir()
    cmd := exec.Command("go", "run", ".", "bridge", "--json")
    cmd.Env = append(os.Environ(),
        "BRIDGE_REPOS_ROOT="+root,
        "XDG_CACHE_HOME="+cache,
    )
    var sout stringBuf
    cmd.Stdout = &sout
    if err := cmd.Run(); err != nil {
        t.Fatalf("run: %v", err)
    }
    if !strings.Contains(sout.String(), `"name": "bridge"`) {
        t.Errorf("expected JSON for bridge, got: %s", sout.String())
    }
}

func TestPreflightPositionalEmitsCD(t *testing.T) {
    root := writeFakeRepos(t)
    cache := t.TempDir()
    cmd := exec.Command("go", "run", ".", "__preflight", "bridge")
    cmd.Env = append(os.Environ(),
        "BRIDGE_REPOS_ROOT="+root,
        "XDG_CACHE_HOME="+cache,
    )
    out, err := cmd.CombinedOutput()
    if err != nil {
        t.Fatalf("run: %v\n%s", err, out)
    }
    s := strings.TrimSpace(string(out))
    if !strings.HasPrefix(s, "cd:") {
        t.Errorf("got %q", s)
    }
}
```

- [ ] **Step 2: FAIL → implement**

Create `cmd/bridge/positional.go`:
```go
package main

import (
    "os"
    "strings"
)

// knownVerbs lists subcommands cobra owns. Anything not in this set,
// not starting with "-", and present as the first arg is rewritten to
// `open <arg>` so muscle-memory `bridge bridge` opens the bridge repo.
var knownVerbs = map[string]bool{
    "list": true, "slots": true, "sessions": true, "presence": true,
    "sync": true, "status": true, "issues": true, "open": true,
    "rm": true, "watch": true, "tui": true, "__preflight": true,
    "version": true, "help": true,
    "completion": true, // cobra default
}

// rewritePositional runs in main() before rootCmd.Execute(). If os.Args has
// the form `bridge <positional-name> [flags]` where positional-name is not a
// known verb, it becomes `bridge open <positional-name> [flags]`.
func rewritePositional() {
    if len(os.Args) < 2 {
        return
    }
    first := os.Args[1]
    if knownVerbs[first] {
        return
    }
    if strings.HasPrefix(first, "-") {
        return
    }
    if first == "__preflight" {
        return
    }
    // Same logic must apply inside __preflight: handled separately in
    // dispatchPreflight via the same knownVerbs map.
    rest := os.Args[2:]
    os.Args = append([]string{os.Args[0], "open", first}, rest...)
}
```

Modify `cmd/bridge/main.go`:
```go
func main() {
    rewritePositional()
    if err := rootCmd.Execute(); err != nil {
        fmt.Fprintln(os.Stderr, err)
        os.Exit(1)
    }
}
```

Modify `dispatchPreflight` (in `preflight.go`) to also handle positional:
```go
func dispatchPreflight(out interface{ Write(p []byte) (int, error) }, args []string) error {
    if len(args) == 0 {
        return shellbridge.EmitNoop(out)
    }
    head := args[0]
    if head == "open" {
        return preflightOpen(out, args[1:])
    }
    if !knownVerbs[head] && !strings.HasPrefix(head, "-") {
        return preflightOpen(out, args)
    }
    return shellbridge.EmitNoop(out)
}
```

- [ ] **Step 3: PASS, `go test ./...`.**

- [ ] **Step 4: Commit**

```bash
git add cmd/bridge/positional.go cmd/bridge/positional_test.go cmd/bridge/main.go cmd/bridge/preflight.go
git commit -m "feat(go): positional bridge <name> rewrites to bridge open <name>"
```

---

## Task 15: `bridge` no-arg picker — fzf subprocess

When invoked with no args, `bridge` should show a picker. Reuse fzf (it's already used by the bash version).

`__preflight` is where the picker fires (because the picker must own the TTY before we know what directive to emit). The flow:

1. `__preflight` with no args runs picker on stderr/tty.
2. fzf prints the chosen line on stdout.
3. We resolve, emit `cd:` (or `exec:` if `BRIDGE_DEFAULT_AGENT` is set).

For tests, we use `BRIDGE_PICKER_FIXTURE=<line>` to bypass the interactive fzf.

**Files:**
- Create: `cmd/bridge/picker.go`
- Create: `cmd/bridge/picker_test.go`
- Modify: `cmd/bridge/preflight.go` (dispatch no-args → picker)

- [ ] **Step 1: Tests**

```go
package main

import (
    "os"
    "os/exec"
    "strings"
    "testing"
)

func TestPickerFixtureCD(t *testing.T) {
    root := writeFakeRepos(t)
    cache := t.TempDir()
    cmd := exec.Command("go", "run", ".", "__preflight")
    cmd.Env = append(os.Environ(),
        "BRIDGE_REPOS_ROOT="+root,
        "XDG_CACHE_HOME="+cache,
        "BRIDGE_PICKER_FIXTURE=bridge",
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

func TestPickerFixtureCancel(t *testing.T) {
    // Empty fixture simulates the user pressing Esc — emit noop, exit 0.
    root := writeFakeRepos(t)
    cache := t.TempDir()
    cmd := exec.Command("go", "run", ".", "__preflight")
    cmd.Env = append(os.Environ(),
        "BRIDGE_REPOS_ROOT="+root,
        "XDG_CACHE_HOME="+cache,
        "BRIDGE_PICKER_FIXTURE_CANCEL=1",
    )
    out, err := cmd.CombinedOutput()
    if err != nil {
        t.Fatalf("run: %v\n%s", err, out)
    }
    if strings.TrimSpace(string(out)) != "noop" {
        t.Errorf("got %q", out)
    }
}
```

- [ ] **Step 2: FAIL → Implement picker**

Create `cmd/bridge/picker.go`:
```go
package main

import (
    "bytes"
    "errors"
    "os"
    "os/exec"
    "sort"
    "strings"

    "github.com/freaxnx01/bridge/internal/core"
)

// pickRepo runs the picker against the supplied repos and returns the chosen
// one. Returns (Repo{}, false, nil) if the user cancelled (Esc/Ctrl-C).
//
// Two test hooks:
//   BRIDGE_PICKER_FIXTURE — return the repo with the named Name.
//   BRIDGE_PICKER_FIXTURE_CANCEL — return (Repo{}, false, nil).
func pickRepo(repos []core.Repo) (core.Repo, bool, error) {
    if os.Getenv("BRIDGE_PICKER_FIXTURE_CANCEL") != "" {
        return core.Repo{}, false, nil
    }
    if name := os.Getenv("BRIDGE_PICKER_FIXTURE"); name != "" {
        r, ok := findRepoByName(repos, name)
        return r, ok, nil
    }
    if _, err := exec.LookPath("fzf"); err != nil {
        return core.Repo{}, false, errors.New("fzf not found in PATH; install fzf or set BRIDGE_DEFAULT_AGENT to skip picker")
    }
    sort.Slice(repos, func(i, j int) bool { return strings.ToLower(repos[i].Name) < strings.ToLower(repos[j].Name) })
    var input bytes.Buffer
    for _, r := range repos {
        input.WriteString(r.Name + "\t" + r.Path + "\n")
    }
    cmd := exec.Command("fzf", "--with-nth=1", "--delimiter=\t", "--prompt=bridge> ")
    cmd.Stdin = &input
    cmd.Stderr = os.Stderr
    var out bytes.Buffer
    cmd.Stdout = &out
    err := cmd.Run()
    if err != nil {
        if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 130 {
            // 130 = user cancelled
            return core.Repo{}, false, nil
        }
        return core.Repo{}, false, err
    }
    chosen := strings.TrimSpace(out.String())
    if chosen == "" {
        return core.Repo{}, false, nil
    }
    parts := strings.SplitN(chosen, "\t", 2)
    if len(parts) != 2 {
        return core.Repo{}, false, errors.New("picker: malformed selection")
    }
    for _, r := range repos {
        if r.Path == parts[1] {
            return r, true, nil
        }
    }
    return core.Repo{}, false, errors.New("picker: chosen repo not in list")
}
```

Modify `dispatchPreflight` to call picker on no args:
```go
func dispatchPreflight(out interface{ Write(p []byte) (int, error) }, args []string) error {
    if len(args) == 0 {
        return preflightPicker(out)
    }
    // (existing logic continues)
}

func preflightPicker(out interface{ Write(p []byte) (int, error) }) error {
    repos, err := core.DiscoverRepos(reposRoot())
    if err != nil {
        return err
    }
    r, ok, err := pickRepo(repos)
    if err != nil {
        return err
    }
    if !ok {
        return shellbridge.EmitNoop(out)
    }
    _ = store.MRUTouch(filepath.Join(cacheRoot(), "mru"), r.Path)
    if agent := os.Getenv("BRIDGE_DEFAULT_AGENT"); agent != "" {
        spec, err := agents.Resolve(agent)
        if err == nil {
            argv, err := launcher.New().LaunchArgv(slotIDFor(r, ""), r.Path, spec)
            if err == nil {
                return shellbridge.EmitExec(out, argv)
            }
        }
    }
    return shellbridge.EmitCD(out, r.Path)
}
```

- [ ] **Step 3: PASS, `go test ./...`.**

- [ ] **Step 4: Commit**

```bash
git add cmd/bridge/picker.go cmd/bridge/picker_test.go cmd/bridge/preflight.go
git commit -m "feat(go): bridge no-arg picker (fzf with test fixtures)"
```

---

## Task 16: `bridge sessions attach <name>` — emit attach directive

Cobra subcommand of `sessions`. Validates the session exists; preflight emits `exec:tmux attach-session -t <slot>`.

**Files:**
- Create: `cmd/bridge/sessions_attach.go`
- Create: `cmd/bridge/sessions_attach_test.go`
- Modify: `cmd/bridge/preflight.go` (dispatch `sessions attach`)

- [ ] **Step 1: Tests**

```go
package main

import (
    "os"
    "os/exec"
    "path/filepath"
    "strings"
    "testing"
)

func TestPreflightSessionsAttachEmitsExec(t *testing.T) {
    dir := t.TempDir()
    fixture := filepath.Join(dir, "tmux.txt")
    _ = os.WriteFile(fixture, []byte("bridge-main|0|1716000000\n"), 0o644)

    cmd := exec.Command("go", "run", ".", "__preflight", "sessions", "attach", "bridge-main")
    cmd.Env = append(os.Environ(), "BRIDGE_TMUX_FIXTURE="+fixture)
    out, err := cmd.CombinedOutput()
    if err != nil {
        t.Fatalf("run: %v\n%s", err, out)
    }
    s := strings.TrimSpace(string(out))
    if !strings.HasPrefix(s, "exec:tmux attach-session -t bridge-main") {
        t.Errorf("got %q", s)
    }
}

func TestSessionsAttachUnknownExits2(t *testing.T) {
    dir := t.TempDir()
    fixture := filepath.Join(dir, "tmux.txt")
    _ = os.WriteFile(fixture, []byte(""), 0o644)
    cmd := exec.Command("go", "run", ".", "__preflight", "sessions", "attach", "bogus")
    cmd.Env = append(os.Environ(), "BRIDGE_TMUX_FIXTURE="+fixture)
    err := cmd.Run()
    if err == nil {
        t.Fatal("expected exit 2")
    }
    if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() != 2 {
        t.Errorf("exit %d", ee.ExitCode())
    }
}
```

- [ ] **Step 2: FAIL → Implement**

Create `cmd/bridge/sessions_attach.go`:
```go
package main

import (
    "fmt"

    "github.com/spf13/cobra"
)

var sessionsAttachCmd = &cobra.Command{
    Use:   "attach <slot>",
    Short: "Attach to a live session (use `bridge __preflight sessions attach <slot>` via the shim to actually attach)",
    Args:  cobra.ExactArgs(1),
    RunE: func(cmd *cobra.Command, args []string) error {
        // Plain (non-preflight) invocation just prints the slot; the shim is
        // responsible for the actual attach via __preflight.
        fmt.Fprintln(cmd.OutOrStdout(), args[0])
        return nil
    },
}

func init() {
    sessionsCmd.AddCommand(sessionsAttachCmd)
}
```

Modify `dispatchPreflight` in `preflight.go`:
```go
    if head == "sessions" && len(args) >= 3 && args[1] == "attach" {
        return preflightSessionsAttach(out, args[2])
    }
```

Add helper:
```go
func preflightSessionsAttach(out interface{ Write(p []byte) (int, error) }, slot string) error {
    sessions, err := loadSessions()
    if err != nil {
        return err
    }
    found := false
    for _, s := range sessions {
        if s.SlotID == slot {
            found = true
            break
        }
    }
    if !found {
        fmt.Fprintf(os.Stderr, "bridge: no session %q\n", slot)
        os.Exit(2)
    }
    return shellbridge.EmitExec(out, launcher.New().AttachArgv(slot))
}
```

- [ ] **Step 3: PASS, `go test ./...`.**

- [ ] **Step 4: Commit**

```bash
git add cmd/bridge/sessions_attach.go cmd/bridge/sessions_attach_test.go cmd/bridge/preflight.go
git commit -m "feat(go): bridge sessions attach <slot> via preflight exec"
```

---

## Task 17: `bridge rm <name>` — delete a local repo

Confirms with the user (or `--yes`), removes the directory, prunes MRU.

**Files:**
- Create: `cmd/bridge/rm.go`
- Create: `cmd/bridge/rm_test.go`

- [ ] **Step 1: Tests**

```go
package main

import (
    "os"
    "os/exec"
    "path/filepath"
    "testing"
)

func TestRmRequiresYes(t *testing.T) {
    root := writeFakeRepos(t)
    cache := t.TempDir()
    // Without --yes and without a TTY, should fail (refuse to delete without confirmation).
    cmd := exec.Command("go", "run", ".", "rm", "bridge")
    cmd.Env = append(os.Environ(),
        "BRIDGE_REPOS_ROOT="+root,
        "XDG_CACHE_HOME="+cache,
    )
    err := cmd.Run()
    if err == nil {
        t.Fatal("expected non-zero exit without --yes")
    }
    if _, err := os.Stat(filepath.Join(root, "github/freaxnx01/public/bridge")); err != nil {
        t.Errorf("repo should still exist: %v", err)
    }
}

func TestRmWithYesDeletes(t *testing.T) {
    root := writeFakeRepos(t)
    cache := t.TempDir()
    target := filepath.Join(root, "github/freaxnx01/public/bridge")
    cmd := exec.Command("go", "run", ".", "rm", "bridge", "--yes")
    cmd.Env = append(os.Environ(),
        "BRIDGE_REPOS_ROOT="+root,
        "XDG_CACHE_HOME="+cache,
    )
    if err := cmd.Run(); err != nil {
        t.Fatalf("run: %v", err)
    }
    if _, err := os.Stat(target); !os.IsNotExist(err) {
        t.Errorf("expected repo deleted, stat err = %v", err)
    }
}

func TestRmUnknownExits2(t *testing.T) {
    root := writeFakeRepos(t)
    cache := t.TempDir()
    cmd := exec.Command("go", "run", ".", "rm", "nope", "--yes")
    cmd.Env = append(os.Environ(),
        "BRIDGE_REPOS_ROOT="+root,
        "XDG_CACHE_HOME="+cache,
    )
    err := cmd.Run()
    if err == nil {
        t.Fatal("expected exit 2")
    }
    if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() != 2 {
        t.Errorf("exit %d", ee.ExitCode())
    }
}
```

- [ ] **Step 2: FAIL → Implement**

Create `cmd/bridge/rm.go`:
```go
package main

import (
    "fmt"
    "os"

    "github.com/spf13/cobra"
    "golang.org/x/term"

    "github.com/freaxnx01/bridge/internal/core"
)

var rmYes bool

var rmCmd = &cobra.Command{
    Use:   "rm <name>",
    Short: "Delete a local repo (refuses without --yes if not a TTY)",
    Args:  cobra.ExactArgs(1),
    RunE:  runRm,
}

func init() {
    rmCmd.Flags().BoolVar(&rmYes, "yes", false, "skip confirmation prompt")
    rootCmd.AddCommand(rmCmd)
}

func runRm(cmd *cobra.Command, args []string) error {
    name := args[0]
    repos, err := core.DiscoverRepos(reposRoot())
    if err != nil {
        return err
    }
    repo, ok := findRepoByName(repos, name)
    if !ok {
        fmt.Fprintf(cmd.ErrOrStderr(), "bridge: unknown repo %q\n", name)
        os.Exit(2)
    }
    if !rmYes {
        if !term.IsTerminal(int(os.Stdin.Fd())) {
            fmt.Fprintf(cmd.ErrOrStderr(), "bridge: refusing to delete without --yes (not a TTY)\n")
            os.Exit(2)
        }
        fmt.Fprintf(cmd.ErrOrStderr(), "delete %s? [y/N] ", repo.Path)
        var s string
        fmt.Fscanln(os.Stdin, &s)
        if s != "y" && s != "Y" && s != "yes" {
            fmt.Fprintln(cmd.ErrOrStderr(), "aborted")
            return nil
        }
    }
    if err := os.RemoveAll(repo.Path); err != nil {
        return fmt.Errorf("rm: %w", err)
    }
    fmt.Fprintf(cmd.OutOrStdout(), "removed %s\n", repo.Path)
    return nil
}
```

Add dep:
```bash
go get golang.org/x/term
go mod tidy
```

- [ ] **Step 3: PASS, `go test ./...`.**

- [ ] **Step 4: Commit**

```bash
git add cmd/bridge/rm.go cmd/bridge/rm_test.go go.mod go.sum
git commit -m "feat(go): bridge rm <name> (with --yes/TTY guard)"
```

---

## Task 18: `bridge presence away|back|auto` — write

Plan A's `presence.go` rejected positional args. Now we wire them through.

**Files:**
- Create: `cmd/bridge/presence_write.go`
- Modify: `cmd/bridge/presence.go` (remove rejection, dispatch positional to writer)
- Create: `cmd/bridge/presence_write_test.go`

- [ ] **Step 1: Tests**

```go
package main

import (
    "encoding/json"
    "os"
    "os/exec"
    "path/filepath"
    "testing"
)

func TestPresenceSetAway(t *testing.T) {
    cache := t.TempDir()
    cmd := exec.Command("go", "run", ".", "presence", "away")
    cmd.Env = append(os.Environ(), "XDG_CACHE_HOME="+cache)
    if err := cmd.Run(); err != nil {
        t.Fatalf("run: %v", err)
    }
    b, _ := os.ReadFile(filepath.Join(cache, "bridge", "presence.json"))
    var p map[string]any
    _ = json.Unmarshal(b, &p)
    if p["mode"] != "away" {
        t.Errorf("got %+v", p)
    }
}

func TestPresenceSetUnknownExits2(t *testing.T) {
    cache := t.TempDir()
    cmd := exec.Command("go", "run", ".", "presence", "bogus")
    cmd.Env = append(os.Environ(), "XDG_CACHE_HOME="+cache)
    err := cmd.Run()
    if err == nil {
        t.Fatal("expected exit 2")
    }
}
```

- [ ] **Step 2: FAIL → Implement**

Replace the `if len(args) > 0` branch in `cmd/bridge/presence.go`'s `runPresence` to call into the writer:
```go
    if len(args) > 0 {
        return setPresence(cmd, args[0])
    }
```

Create `cmd/bridge/presence_write.go`:
```go
package main

import (
    "encoding/json"
    "fmt"
    "os"
    "path/filepath"
    "time"

    "github.com/spf13/cobra"

    "github.com/freaxnx01/bridge/internal/core"
    "github.com/freaxnx01/bridge/internal/store"
)

func setPresence(cmd *cobra.Command, mode string) error {
    switch mode {
    case "away", "back", "auto":
    default:
        fmt.Fprintf(cmd.ErrOrStderr(), "bridge: unknown presence mode %q (want away|back|auto)\n", mode)
        os.Exit(2)
    }
    p, _ := core.LoadPresence(filepath.Join(cacheRoot(), "presence.json"))
    p.Mode = mode
    p.UpdatedAt = time.Now().UTC()
    b, err := json.MarshalIndent(p, "", "  ")
    if err != nil {
        return err
    }
    return store.AtomicWrite(filepath.Join(cacheRoot(), "presence.json"), b)
}
```

- [ ] **Step 3: PASS, `go test ./...`.**

- [ ] **Step 4: Commit**

```bash
git add cmd/bridge/presence.go cmd/bridge/presence_write.go cmd/bridge/presence_write_test.go
git commit -m "feat(go): bridge presence away|back|auto (write)"
```

---

## Task 19: `internal/syncer` — per-repo git fetch+pull driver

Shared logic for `sync now` and `sync --auto`. Receives a list of `core.Repo`, runs `git fetch && git pull --ff-only` against each, collects results.

For testability: the `git` binary is invoked via a `Runner` interface; tests inject a fake.

**Files:**
- Create: `internal/syncer/syncer.go`
- Create: `internal/syncer/syncer_test.go`

- [ ] **Step 1: Tests**

```go
package syncer

import (
    "context"
    "errors"
    "testing"

    "github.com/freaxnx01/bridge/internal/core"
)

type fakeRunner struct {
    calls []string
    fail  map[string]error
}

func (f *fakeRunner) Run(ctx context.Context, dir, name string, args ...string) error {
    key := dir + ":" + name + " " + joinArgs(args)
    f.calls = append(f.calls, key)
    if f.fail != nil {
        if err, ok := f.fail[key]; ok {
            return err
        }
    }
    return nil
}

func joinArgs(a []string) string {
    s := ""
    for i, x := range a {
        if i > 0 {
            s += " "
        }
        s += x
    }
    return s
}

func TestSyncOneRepoSuccess(t *testing.T) {
    r := &fakeRunner{}
    s := &Syncer{Runner: r}
    repos := []core.Repo{{Name: "bridge", Path: "/r/bridge"}}
    res := s.Run(context.Background(), repos)
    if len(res.Failed) != 0 {
        t.Errorf("expected no failures, got %+v", res)
    }
    if len(r.calls) != 2 {
        t.Errorf("expected 2 calls (fetch+pull), got %v", r.calls)
    }
}

func TestSyncFetchFailureStopsRepo(t *testing.T) {
    r := &fakeRunner{fail: map[string]error{
        "/r/bridge:git fetch --all --prune": errors.New("network"),
    }}
    s := &Syncer{Runner: r}
    repos := []core.Repo{{Name: "bridge", Path: "/r/bridge"}}
    res := s.Run(context.Background(), repos)
    if len(res.Failed) != 1 || res.Failed[0].Repo.Name != "bridge" {
        t.Errorf("expected fetch failure recorded, got %+v", res)
    }
    if len(r.calls) != 1 {
        t.Errorf("expected only fetch call, got %v", r.calls)
    }
}
```

- [ ] **Step 2: FAIL → Implement**

Create `internal/syncer/syncer.go`:
```go
// Package syncer drives `git fetch && git pull --ff-only` across a set of repos.
package syncer

import (
    "context"
    "os/exec"

    "github.com/freaxnx01/bridge/internal/core"
)

// Runner runs a command in a directory. Production implementation is exec.Command.
type Runner interface {
    Run(ctx context.Context, dir, name string, args ...string) error
}

type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, dir, name string, args ...string) error {
    cmd := exec.CommandContext(ctx, name, args...)
    cmd.Dir = dir
    return cmd.Run()
}

// Syncer drives syncs.
type Syncer struct {
    Runner Runner
}

// Failure records a per-repo sync error.
type Failure struct {
    Repo  core.Repo
    Step  string // "fetch" | "pull"
    Error error
}

// Result aggregates a sync run.
type Result struct {
    OK     []core.Repo
    Failed []Failure
}

// Run synchronises each repo. Aborts a repo's pull if its fetch failed; other
// repos are unaffected. Returns aggregate result; never returns an error.
func (s *Syncer) Run(ctx context.Context, repos []core.Repo) Result {
    if s.Runner == nil {
        s.Runner = ExecRunner{}
    }
    var res Result
    for _, r := range repos {
        if err := s.Runner.Run(ctx, r.Path, "git", "fetch", "--all", "--prune"); err != nil {
            res.Failed = append(res.Failed, Failure{Repo: r, Step: "fetch", Error: err})
            continue
        }
        if err := s.Runner.Run(ctx, r.Path, "git", "pull", "--ff-only"); err != nil {
            res.Failed = append(res.Failed, Failure{Repo: r, Step: "pull", Error: err})
            continue
        }
        res.OK = append(res.OK, r)
    }
    return res
}
```

- [ ] **Step 3: PASS, `go test ./...`.**

- [ ] **Step 4: Commit**

```bash
git add internal/syncer/syncer.go internal/syncer/syncer_test.go
git commit -m "feat(go): internal/syncer (per-repo git fetch+pull driver)"
```

---

## Task 20: `bridge sync now` — one-shot sync + write sync.json

Replaces Plan A's "sync now is not implemented yet" stub.

**Files:**
- Modify: `cmd/bridge/sync.go` (dispatch "now" arg to runSyncNow)
- Create: `cmd/bridge/sync_now.go`
- Create: `cmd/bridge/sync_now_test.go`

- [ ] **Step 1: Test**

```go
package main

import (
    "encoding/json"
    "os"
    "os/exec"
    "path/filepath"
    "testing"
)

func TestSyncNowWritesState(t *testing.T) {
    root := writeFakeRepos(t)
    cache := t.TempDir()
    // Provide a fake `git` that always succeeds.
    bindir := t.TempDir()
    _ = os.WriteFile(filepath.Join(bindir, "git"), []byte("#!/bin/sh\nexit 0\n"), 0o755)

    cmd := exec.Command("go", "run", ".", "sync", "now")
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
```

- [ ] **Step 2: FAIL → Implement**

Replace the positional rejection in `cmd/bridge/sync.go`'s `runSync`:
```go
    if len(args) > 0 {
        switch args[0] {
        case "now":
            return runSyncNow(cmd)
        case "--auto":
            return fmt.Errorf("`bridge sync --auto` should be invoked via flag, not positional; see --help")
        }
        return fmt.Errorf("`bridge sync %s` is not implemented", args[0])
    }
```

Create `cmd/bridge/sync_now.go`:
```go
package main

import (
    "context"
    "encoding/json"
    "fmt"
    "path/filepath"
    "time"

    "github.com/spf13/cobra"

    "github.com/freaxnx01/bridge/internal/core"
    "github.com/freaxnx01/bridge/internal/store"
    "github.com/freaxnx01/bridge/internal/syncer"
)

func runSyncNow(cmd *cobra.Command) error {
    repos, err := core.DiscoverRepos(reposRoot())
    if err != nil {
        return err
    }
    s := &syncer.Syncer{}
    res := s.Run(context.Background(), repos)
    state := SyncState{
        LastRun: time.Now().UTC(),
    }
    for _, f := range res.Failed {
        state.Queue = append(state.Queue, f.Repo.Name)
    }
    // unpushed detection deferred to a follow-up task / Plan C
    b, err := json.MarshalIndent(state, "", "  ")
    if err != nil {
        return err
    }
    if err := store.AtomicWrite(filepath.Join(cacheRoot(), "sync.json"), b); err != nil {
        return err
    }
    fmt.Fprintf(cmd.OutOrStdout(), "synced %d repos (%d failed)\n", len(res.OK), len(res.Failed))
    return nil
}
```

- [ ] **Step 3: PASS, `go test ./...`.**

- [ ] **Step 4: Commit**

```bash
git add cmd/bridge/sync.go cmd/bridge/sync_now.go cmd/bridge/sync_now_test.go
git commit -m "feat(go): bridge sync now (one-shot, writes sync.json)"
```

---

## Task 21: `bridge sync --auto` — long-running daemon

Loop: every `--interval` (default 5m), call `runSyncNow`. PID file at `~/.cache/bridge/sync.pid`. `--daemonize` forks (using `os/exec` self-restart) and detaches.

For tests: `BRIDGE_DAEMON_MAX_ITERATIONS=1` runs one cycle then exits.

**Files:**
- Create: `cmd/bridge/sync_auto.go`
- Create: `cmd/bridge/sync_auto_test.go`

- [ ] **Step 1: Test**

```go
package main

import (
    "os"
    "os/exec"
    "path/filepath"
    "strconv"
    "testing"
)

func TestSyncAutoSingleIteration(t *testing.T) {
    root := writeFakeRepos(t)
    cache := t.TempDir()
    bindir := t.TempDir()
    _ = os.WriteFile(filepath.Join(bindir, "git"), []byte("#!/bin/sh\nexit 0\n"), 0o755)

    cmd := exec.Command("go", "run", ".", "sync", "--auto")
    cmd.Env = append(os.Environ(),
        "BRIDGE_REPOS_ROOT="+root,
        "XDG_CACHE_HOME="+cache,
        "PATH="+bindir+":"+os.Getenv("PATH"),
        "BRIDGE_DAEMON_MAX_ITERATIONS=1",
    )
    if err := cmd.Run(); err != nil {
        t.Fatalf("run: %v", err)
    }
    // sync.json was written.
    if _, err := os.Stat(filepath.Join(cache, "bridge", "sync.json")); err != nil {
        t.Errorf("expected sync.json: %v", err)
    }
    // PID file is cleaned up.
    if _, err := os.Stat(filepath.Join(cache, "bridge", "sync.pid")); !os.IsNotExist(err) {
        t.Errorf("expected pidfile removed, stat err = %v", err)
    }
    _ = strconv.Itoa
}
```

- [ ] **Step 2: FAIL → Implement**

Create `cmd/bridge/sync_auto.go`:
```go
package main

import (
    "context"
    "fmt"
    "log/slog"
    "os"
    "os/signal"
    "path/filepath"
    "strconv"
    "syscall"
    "time"

    "github.com/spf13/cobra"

    "github.com/freaxnx01/bridge/internal/store"
)

var (
    syncAuto     bool
    syncInterval time.Duration
)

func init() {
    syncCmd.Flags().BoolVar(&syncAuto, "auto", false, "run in a loop until stopped")
    syncCmd.Flags().DurationVar(&syncInterval, "interval", 5*time.Minute, "interval between syncs in --auto mode")
    // Override sync's RunE so flag-mode works without a positional arg.
    syncCmd.RunE = func(cmd *cobra.Command, args []string) error {
        if syncAuto {
            return runSyncAuto(cmd)
        }
        return runSync(cmd, args)
    }
}

func runSyncAuto(cmd *cobra.Command) error {
    pidPath := filepath.Join(cacheRoot(), "sync.pid")
    if existing, _ := store.ReadPIDFile(pidPath); existing > 0 && store.IsPIDRunning(existing) {
        return fmt.Errorf("sync --auto already running (PID %d)", existing)
    }
    if err := store.WritePIDFile(pidPath, os.Getpid()); err != nil {
        return err
    }
    defer store.RemovePIDFile(pidPath)

    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()
    sigCh := make(chan os.Signal, 1)
    signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
    go func() {
        <-sigCh
        slog.Info("sync auto: signal received, stopping")
        cancel()
    }()

    maxIter := 0
    if v := os.Getenv("BRIDGE_DAEMON_MAX_ITERATIONS"); v != "" {
        n, _ := strconv.Atoi(v)
        maxIter = n
    }

    iter := 0
    ticker := time.NewTicker(syncInterval)
    defer ticker.Stop()
    for {
        if err := runSyncNow(cmd); err != nil {
            slog.Warn("sync iter failed", "err", err)
        }
        iter++
        if maxIter > 0 && iter >= maxIter {
            return nil
        }
        select {
        case <-ctx.Done():
            return nil
        case <-ticker.C:
        }
    }
}
```

- [ ] **Step 3: PASS, `go test ./...`.**

- [ ] **Step 4: Commit**

```bash
git add cmd/bridge/sync_auto.go cmd/bridge/sync_auto_test.go
git commit -m "feat(go): bridge sync --auto daemon (PID file + signal handling)"
```

---

## Task 22: `bridge watch` — fsnotify on `~/projects/repos/`

Long-running. Fires on dir create/delete and bumps a state file (`watch.last`). Mostly a hook point for future automation; for Phase 2 we ship the loop and bookkeeping.

**Files:**
- Create: `cmd/bridge/watch.go`
- Create: `cmd/bridge/watch_test.go`
- Add dep: `github.com/fsnotify/fsnotify`

- [ ] **Step 1: Add dep**

```bash
go get github.com/fsnotify/fsnotify
go mod tidy
```

- [ ] **Step 2: Test**

```go
package main

import (
    "os"
    "os/exec"
    "path/filepath"
    "testing"
    "time"
)

func TestWatchSingleIteration(t *testing.T) {
    root := writeFakeRepos(t)
    cache := t.TempDir()
    cmd := exec.Command("go", "run", ".", "watch")
    cmd.Env = append(os.Environ(),
        "BRIDGE_REPOS_ROOT="+root,
        "XDG_CACHE_HOME="+cache,
        "BRIDGE_DAEMON_MAX_ITERATIONS=1",
    )
    if err := cmd.Run(); err != nil {
        t.Fatalf("run: %v", err)
    }
    if _, err := os.Stat(filepath.Join(cache, "bridge", "watch.last")); err != nil {
        t.Errorf("expected watch.last: %v", err)
    }
    _ = time.Now
}

func TestWatchStatusReportsNotRunning(t *testing.T) {
    cache := t.TempDir()
    cmd := exec.Command("go", "run", ".", "watch", "--status")
    cmd.Env = append(os.Environ(), "XDG_CACHE_HOME="+cache)
    out, _ := cmd.CombinedOutput()
    if string(out) == "" {
        t.Error("expected output")
    }
}
```

- [ ] **Step 3: FAIL → Implement**

Create `cmd/bridge/watch.go`:
```go
package main

import (
    "context"
    "fmt"
    "log/slog"
    "os"
    "os/signal"
    "path/filepath"
    "strconv"
    "syscall"
    "time"

    "github.com/fsnotify/fsnotify"
    "github.com/spf13/cobra"

    "github.com/freaxnx01/bridge/internal/store"
)

var (
    watchStatus    bool
    watchStop      bool
    watchDaemonize bool
)

var watchCmd = &cobra.Command{
    Use:   "watch",
    Short: "Long-running watcher of ~/projects/repos/",
    RunE:  runWatch,
}

func init() {
    watchCmd.Flags().BoolVar(&watchStatus, "status", false, "report whether a watcher is running")
    watchCmd.Flags().BoolVar(&watchStop, "stop", false, "signal a running watcher to exit")
    watchCmd.Flags().BoolVar(&watchDaemonize, "daemonize", false, "re-exec self detached")
    rootCmd.AddCommand(watchCmd)
}

func runWatch(cmd *cobra.Command, args []string) error {
    pidPath := filepath.Join(cacheRoot(), "watch.pid")

    if watchStatus {
        pid, _ := store.ReadPIDFile(pidPath)
        if pid > 0 && store.IsPIDRunning(pid) {
            fmt.Fprintf(cmd.OutOrStdout(), "watch: running (PID %d)\n", pid)
            return nil
        }
        fmt.Fprintln(cmd.OutOrStdout(), "watch: not running")
        return nil
    }
    if watchStop {
        pid, _ := store.ReadPIDFile(pidPath)
        if pid <= 0 || !store.IsPIDRunning(pid) {
            fmt.Fprintln(cmd.ErrOrStderr(), "watch: nothing to stop")
            return nil
        }
        proc, _ := os.FindProcess(pid)
        _ = proc.Signal(syscall.SIGTERM)
        return nil
    }
    if watchDaemonize {
        // Re-exec self without --daemonize, setsid-detached.
        argv := append([]string{}, os.Args[1:]...)
        for i, a := range argv {
            if a == "--daemonize" {
                argv = append(argv[:i], argv[i+1:]...)
                break
            }
        }
        c := osExecSelf(argv)
        if err := c.Start(); err != nil {
            return err
        }
        fmt.Fprintf(cmd.OutOrStdout(), "watch: detached (PID %d)\n", c.Process.Pid)
        return nil
    }

    if existing, _ := store.ReadPIDFile(pidPath); existing > 0 && store.IsPIDRunning(existing) {
        return fmt.Errorf("watch already running (PID %d)", existing)
    }
    if err := store.WritePIDFile(pidPath, os.Getpid()); err != nil {
        return err
    }
    defer store.RemovePIDFile(pidPath)

    w, err := fsnotify.NewWatcher()
    if err != nil {
        return err
    }
    defer w.Close()
    if err := w.Add(reposRoot()); err != nil {
        return err
    }

    ctx, cancel := context.WithCancel(context.Background())
    defer cancel()
    sigCh := make(chan os.Signal, 1)
    signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
    go func() { <-sigCh; cancel() }()

    maxIter := 0
    if v := os.Getenv("BRIDGE_DAEMON_MAX_ITERATIONS"); v != "" {
        n, _ := strconv.Atoi(v)
        maxIter = n
    }

    iter := 0
    tick := time.NewTicker(30 * time.Second)
    defer tick.Stop()
    for {
        select {
        case ev := <-w.Events:
            slog.Info("watch event", "op", ev.Op.String(), "name", ev.Name)
            _ = store.AtomicWrite(filepath.Join(cacheRoot(), "watch.last"), []byte(time.Now().UTC().Format(time.RFC3339)+"\n"))
        case err := <-w.Errors:
            slog.Warn("watch error", "err", err)
        case <-tick.C:
            _ = store.AtomicWrite(filepath.Join(cacheRoot(), "watch.last"), []byte(time.Now().UTC().Format(time.RFC3339)+"\n"))
        case <-ctx.Done():
            return nil
        }
        iter++
        if maxIter > 0 && iter >= maxIter {
            return nil
        }
    }
}

// osExecSelf returns an *exec.Cmd that re-launches the bridge-go binary with
// argv, detached from the current process group. Defined in a small helper
// for cross-platform variation if we need it later.
func osExecSelf(argv []string) *exec.Cmd {
    self, _ := os.Executable()
    c := exec.Command(self, argv...)
    c.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
    c.Stdin = nil
    c.Stdout = nil
    c.Stderr = nil
    return c
}
```

Add imports (`os/exec`). On Windows, `SysProcAttr{Setsid: true}` won't compile; if Windows CI is added we'll build-tag. For Phase 2 we accept this is Linux-only.

- [ ] **Step 4: PASS, `go test ./...`.**

- [ ] **Step 5: Commit**

```bash
git add cmd/bridge/watch.go cmd/bridge/watch_test.go go.mod go.sum
git commit -m "feat(go): bridge watch (foreground + --status/--stop/--daemonize)"
```

---

## Task 23: `bridge sessions attach` no-arg picker

User-facing `bridge sessions attach` with no `<slot>` should fzf-pick from live sessions.

**Files:**
- Modify: `cmd/bridge/sessions_attach.go` (relax `Args` to `MaximumNArgs(1)`)
- Modify: `cmd/bridge/preflight.go`

- [ ] **Step 1: Test**

```go
func TestPreflightSessionsAttachPickerFixture(t *testing.T) {
    dir := t.TempDir()
    fixture := filepath.Join(dir, "tmux.txt")
    _ = os.WriteFile(fixture, []byte("alpha|0|1716000000\nbeta|1|1716000100\n"), 0o644)

    cmd := exec.Command("go", "run", ".", "__preflight", "sessions", "attach")
    cmd.Env = append(os.Environ(),
        "BRIDGE_TMUX_FIXTURE="+fixture,
        "BRIDGE_PICKER_FIXTURE=beta",
    )
    out, err := cmd.CombinedOutput()
    if err != nil {
        t.Fatalf("run: %v\n%s", err, out)
    }
    if !strings.Contains(string(out), "exec:tmux attach-session -t beta") {
        t.Errorf("got %q", out)
    }
}
```

- [ ] **Step 2: FAIL → Implement**

Modify `sessions_attach.go`:
```go
var sessionsAttachCmd = &cobra.Command{
    Use:   "attach [slot]",
    Args:  cobra.MaximumNArgs(1),
    ...
}
```

Modify dispatcher in `preflight.go`:
```go
    if head == "sessions" && len(args) >= 2 && args[1] == "attach" {
        slot := ""
        if len(args) >= 3 {
            slot = args[2]
        } else {
            // pick from live sessions
            sessions, _ := loadSessions()
            if len(sessions) == 0 {
                fmt.Fprintln(os.Stderr, "bridge: no live sessions to attach to")
                os.Exit(2)
            }
            slot = pickSession(sessions)
            if slot == "" {
                return shellbridge.EmitNoop(out)
            }
        }
        return preflightSessionsAttach(out, slot)
    }
```

Add helper alongside `pickRepo` in `picker.go`:
```go
// pickSession mirrors pickRepo for sessions. Returns "" on cancel.
func pickSession(sessions []core.Session) string {
    if os.Getenv("BRIDGE_PICKER_FIXTURE_CANCEL") != "" {
        return ""
    }
    if name := os.Getenv("BRIDGE_PICKER_FIXTURE"); name != "" {
        for _, s := range sessions {
            if s.SlotID == name {
                return s.SlotID
            }
        }
        return ""
    }
    if _, err := exec.LookPath("fzf"); err != nil {
        return ""
    }
    var input bytes.Buffer
    for _, s := range sessions {
        input.WriteString(s.SlotID + "\n")
    }
    cmd := exec.Command("fzf", "--prompt=session> ")
    cmd.Stdin = &input
    cmd.Stderr = os.Stderr
    var out bytes.Buffer
    cmd.Stdout = &out
    _ = cmd.Run()
    return strings.TrimSpace(out.String())
}
```

- [ ] **Step 3: PASS, `go test ./...`.**

- [ ] **Step 4: Commit**

```bash
git add cmd/bridge/sessions_attach.go cmd/bridge/preflight.go cmd/bridge/picker.go
git commit -m "feat(go): sessions attach no-arg picker"
```

---

## Task 24: `bridge tui` reserved subcommand

Spec reserves the verb. Ship a stub that exits 1 with a "not implemented yet (see dashboard spec)" message so the verb shows up in `--help` and can't be claimed by accident.

**Files:**
- Create: `cmd/bridge/tui.go`
- Create: `cmd/bridge/tui_test.go`

- [ ] **Step 1: Test**

```go
func TestTUINotImplemented(t *testing.T) {
    cmd := exec.Command("go", "run", ".", "tui")
    err := cmd.Run()
    if err == nil {
        t.Fatal("expected non-zero exit")
    }
}
```

- [ ] **Step 2: Implement**

Create `cmd/bridge/tui.go`:
```go
package main

import (
    "errors"

    "github.com/spf13/cobra"
)

var tuiCmd = &cobra.Command{
    Use:   "tui",
    Short: "Dashboard TUI (reserved; not implemented yet)",
    RunE: func(cmd *cobra.Command, args []string) error {
        return errors.New("bridge tui: not implemented yet — see the dashboard spec")
    },
}

func init() {
    rootCmd.AddCommand(tuiCmd)
}
```

- [ ] **Step 3: PASS, `go test ./...`.**

- [ ] **Step 4: Commit**

```bash
git add cmd/bridge/tui.go cmd/bridge/tui_test.go
git commit -m "feat(go): bridge tui (reserved verb stub)"
```

---

## Task 25: Makefile `install-shim` target

Installs the shim file to `~/.local/share/bridge/` without modifying `~/.bashrc`. User must source it manually during Phase 3.

**Files:**
- Modify: `Makefile`

- [ ] **Step 1: Append to Makefile**

```make
.PHONY: install-shim
install-shim:
	install -d $(HOME)/.local/share/bridge
	install -m 0644 shims/bridge-shim.sh $(HOME)/.local/share/bridge/bridge-shim.sh
	@echo
	@echo "Shim installed to $(HOME)/.local/share/bridge/bridge-shim.sh"
	@echo "DO NOT add to ~/.bashrc yet — Phase 3 (Plan C) handles cutover."
```

- [ ] **Step 2: Verify**

```bash
make install-shim
ls -l ~/.local/share/bridge/bridge-shim.sh
```

- [ ] **Step 3: Commit**

```bash
git add Makefile
git commit -m "chore(go): install-shim Makefile target (does not touch ~/.bashrc)"
```

---

## Task 26: Extend `docs/cli-json-schema.md` for new commands

New `--json` shapes from Plan B: `bridge open --json` returns a `Repo`; `bridge presence` write returns nothing on stdout; no new schemas otherwise.

Plan A's doc already covers status/list/slots/sessions/presence/sync/issues. Add a section for `bridge open --json` and an explicit note that interactive commands (`rm`, `presence away|back`, `sync now`, `sync --auto`, `watch`) emit no structured stdout.

**Files:**
- Modify: `docs/cli-json-schema.md`

- [ ] **Step 1: Append**

Add after the existing `bridge issues --json` section:

```markdown
## `bridge open --json`

Same shape as a single `Repo` object from `bridge list --json`:

```json
{
  "name": "bridge",
  "path": "/home/me/projects/repos/github/me/public/bridge",
  "forge": "github",
  "owner": "me",
  "visibility": "public",
  "topics": [],
  "desc": "",
  "default_branch": "",
  "remote_url": "",
  "last_used": "0001-01-01T00:00:00Z"
}
```

When invoked via the shell shim (`__preflight`), the binary instead emits a
directive on stdout (`cd:<path>` or `exec:<argv>`); the shim consumes it and
nothing reaches the user's terminal as JSON. Use `bridge open --json` as a
scriptable form when you want the structured Repo data without changing the
shell.

## Interactive commands (no structured stdout)

These write state or detach from stdout; they intentionally do not emit JSON:

- `bridge rm <name>` — prints removal confirmation
- `bridge presence away|back|auto` — silent on success
- `bridge sync now` — prints a count summary
- `bridge sync --auto` — long-running; log to file
- `bridge watch` — long-running; log to file
```

- [ ] **Step 2: Commit**

```bash
git add docs/cli-json-schema.md
git commit -m "docs(go): extend --json schema for Plan B"
```

---

## Task 27: Cross-compile + tag `v2.0.0-go.1`

**Files:** none (verification + tag).

- [ ] **Step 1: Full build + test**

```bash
go build ./...
go test ./...
GOOS=windows GOARCH=amd64 go build -o /tmp/bridge.exe ./cmd/bridge
file /tmp/bridge.exe
rm -f /tmp/bridge.exe
```

All green.

- [ ] **Step 2: Verify CI workflow still passes (locally simulated)**

```bash
go vet ./...
```

- [ ] **Step 3: Tag**

```bash
git tag -a v2.0.0-go.1 -m "Plan B complete: interactive + launcher + shim shipped alongside bash"
```

Do NOT push the tag. Phase 3 (Plan C) will cut over the user's shell and push everything.

- [ ] **Step 4: Final smoke**

```bash
make build-go
./bridge-go --help
./bridge-go __preflight   # should print "noop"
echo $?                   # 0
./bridge-go status --json
rm bridge-go
```

---

## Spec coverage check

| Spec requirement | Task(s) |
|---|---|
| `internal/shellbridge` directive protocol | 1, 2 |
| Shell shim (bash) | 3 |
| Shell shim (PowerShell) | 4 |
| MRU writes | 5 |
| PID file helpers | 6 |
| `internal/agents` | 7 |
| `internal/launcher` (Linux tmux) | 8 |
| `internal/launcher` (Windows WT) | 9 |
| Structured logging (slog + rotation) | 10 |
| `bridge open <name>` | 11, 12 |
| `__preflight` emits directives | 13, 15, 16, 23 |
| Positional `bridge <name>` | 14 |
| `bridge` no-arg picker | 15 |
| `bridge sessions attach` | 16, 23 |
| `bridge rm` | 17 |
| `bridge presence away/back/auto` write | 18 |
| `internal/syncer` | 19 |
| `bridge sync now` | 20 |
| `bridge sync --auto` daemon | 21 |
| `bridge watch` daemon | 22 |
| `bridge tui` reserved | 24 |
| install-shim Makefile target | 25 |
| `--json` schema doc growth | 26 |
| Windows cross-compile + tag | 27 |

**Explicitly deferred to Plan C (Phase 3 cutover):**
- Renaming `bridge-go` → `bridge` (binary install path collision with bash script)
- Editing the user's `~/.bashrc` to source `bridge-shim.sh`
- Removing the bash `bridge` function from `~/.bashrc`
- Legacy flag silent-forwarding (`-r` → `list -r`, `-D` → `rm`, etc.) — once cutover happens, these flags become the only ways old muscle memory still works; we wire the forwarder then
- `_BRIDGE_VERSION` sunset and final CHANGELOG entry
- Pushing `v2.0.0-go.1` tag (waits for Plan C completion so the tag points at "everything Phase 2+3" rather than "Phase 2 only")
- Removing `bridge-watcher.sh`, `bridge-autosync.sh`, `bridge-unpushed-warn.sh` (Phase 4)
- Unpushed-branch detection inside `bridge sync` (folded from `bridge-unpushed-warn.sh`)

**Open items (intentional gaps, fold into Plan C or follow-ups):**

1. The launcher does not yet write to `slots.json`. Currently Plan A's read-only `bridge slots` shows whatever happens to be in the file — likely empty. Plan C should add slot bookkeeping at session-create time (call site is `preflightOpen` after `LaunchArgv` succeeds, before emitting the directive).
2. Worktree handling: `-w/--worktree` is parsed but ignored in `LaunchArgv`. The launcher needs a `Worktree` field and tmux needs `-c <worktree-path>` instead of `<repo-path>`. Defer until you actually start using worktrees again.
3. `bridge sessions attach` always uses tmux; on Windows it would need a WT-specific story. Out of scope (no Windows CI; manual user task).
4. `bridge watch` emits per-event slog records but doesn't yet do anything with them (no auto-refresh of `remote.list`, no MRU pruning of deleted repos). Wire those in Plan C.
5. Concurrency between `bridge sync --auto` and a one-shot `bridge sync now` is not guarded by flock today — both will fight over `sync.json`. Add a `sync.lock` flock in Plan C.
