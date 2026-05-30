# TERM-fallback for tmux launches under kitty (#104) — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** When `bridge` is about to launch tmux but `$TERM` has no terminfo entry on the host, transparently launch with `TERM=xterm-256color` and a one-line notice, so kitty users on hosts lacking `xterm-kitty` terminfo stop hitting `missing or unsuitable terminal`.

**Architecture:** A build-tagged helper `maybeTermFallback` in `cmd/bridge` inspects `$TERM`, runs `infocmp` to test resolution (via a stubbable package var), and prepends `["env", "TERM=xterm-256color"]` to the launch argv when the term is unresolved. A thin `emitLaunch` wrapper applies it at the 4 tmux launch sites before `shellbridge.EmitExec`. Windows gets a no-op build of the helper.

**Tech Stack:** Go (stdlib `os`, `os/exec`, `io`), Cobra CLI, existing `internal/shellbridge` directive protocol. No shim changes.

---

## File Structure

- `cmd/bridge/termfallback_unix.go` (new, `//go:build !windows`) — `maybeTermFallback`, `termResolver` var, default `infocmp`-based resolver.
- `cmd/bridge/termfallback_windows.go` (new, `//go:build windows`) — no-op `maybeTermFallback`.
- `cmd/bridge/termfallback_test.go` (new, `//go:build !windows`) — unit tests with stubbed `termResolver`.
- `cmd/bridge/preflight.go` (modify) — add `emitLaunch`, swap it in at 4 sites.
- `CHANGELOG.md` (modify) — Unreleased → Fixed entry.
- `docs/local-cc-session.md` (modify) — update #104 state.
- `README.md` (modify) — kitty/terminfo troubleshooting note.

---

## Task 1: TERM-fallback helper (unix) with stubbable resolver

**Files:**
- Create: `cmd/bridge/termfallback_unix.go`
- Test: `cmd/bridge/termfallback_test.go`

- [ ] **Step 1: Write the failing test**

Create `cmd/bridge/termfallback_test.go`:

```go
//go:build !windows

package main

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
)

// withTermResolver swaps the package-level termResolver for the duration of a
// test and restores it afterward.
func withTermResolver(t *testing.T, fn func(string) bool) {
	t.Helper()
	prev := termResolver
	termResolver = fn
	t.Cleanup(func() { termResolver = prev })
}

func TestMaybeTermFallback(t *testing.T) {
	argv := []string{"tmux", "new-session", "-A", "-s", "repo", "-c", "/p", "claude"}

	t.Run("term unset: passthrough", func(t *testing.T) {
		t.Setenv("TERM", "")
		withTermResolver(t, func(string) bool { return false })
		var errBuf bytes.Buffer
		got := maybeTermFallback(&errBuf, argv)
		if !reflect.DeepEqual(got, argv) {
			t.Errorf("got %v, want unchanged %v", got, argv)
		}
		if errBuf.Len() != 0 {
			t.Errorf("unexpected notice: %q", errBuf.String())
		}
	})

	t.Run("term already xterm-256color: passthrough", func(t *testing.T) {
		t.Setenv("TERM", "xterm-256color")
		withTermResolver(t, func(string) bool { return false })
		var errBuf bytes.Buffer
		if got := maybeTermFallback(&errBuf, argv); !reflect.DeepEqual(got, argv) {
			t.Errorf("got %v, want unchanged", got)
		}
	})

	t.Run("disable var set: passthrough", func(t *testing.T) {
		t.Setenv("TERM", "xterm-kitty")
		t.Setenv("BRIDGE_NO_TERM_FALLBACK", "1")
		withTermResolver(t, func(string) bool { return false })
		var errBuf bytes.Buffer
		if got := maybeTermFallback(&errBuf, argv); !reflect.DeepEqual(got, argv) {
			t.Errorf("got %v, want unchanged", got)
		}
	})

	t.Run("term resolves: passthrough", func(t *testing.T) {
		t.Setenv("TERM", "xterm-kitty")
		t.Setenv("BRIDGE_NO_TERM_FALLBACK", "")
		withTermResolver(t, func(string) bool { return true })
		var errBuf bytes.Buffer
		if got := maybeTermFallback(&errBuf, argv); !reflect.DeepEqual(got, argv) {
			t.Errorf("got %v, want unchanged", got)
		}
	})

	t.Run("term unresolved: prefix env and notice", func(t *testing.T) {
		t.Setenv("TERM", "xterm-kitty")
		t.Setenv("BRIDGE_NO_TERM_FALLBACK", "")
		withTermResolver(t, func(string) bool { return false })
		var errBuf bytes.Buffer
		got := maybeTermFallback(&errBuf, argv)
		want := append([]string{"env", "TERM=xterm-256color"}, argv...)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("got %v, want %v", got, want)
		}
		notice := errBuf.String()
		if !strings.Contains(notice, "xterm-kitty") ||
			!strings.Contains(notice, "TERM=xterm-256color") ||
			!strings.Contains(notice, "BRIDGE_NO_TERM_FALLBACK") {
			t.Errorf("notice missing expected content: %q", notice)
		}
	})

	t.Run("empty argv: passthrough", func(t *testing.T) {
		t.Setenv("TERM", "xterm-kitty")
		withTermResolver(t, func(string) bool { return false })
		var errBuf bytes.Buffer
		if got := maybeTermFallback(&errBuf, nil); len(got) != 0 {
			t.Errorf("got %v, want empty", got)
		}
	})
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/bridge -run TestMaybeTermFallback -v`
Expected: FAIL — `undefined: maybeTermFallback` / `undefined: termResolver`.

- [ ] **Step 3: Write minimal implementation**

Create `cmd/bridge/termfallback_unix.go`:

```go
//go:build !windows

package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
)

// fallbackTerm is the portable terminfo bridge falls back to when the
// advertised $TERM has no entry on the host.
const fallbackTerm = "xterm-256color"

// termResolver reports whether name has a terminfo entry on this host. It is a
// package var so tests can stub it without spawning infocmp.
var termResolver = infocmpResolves

// infocmpResolves runs `infocmp <name>` and reports success. If infocmp is not
// on PATH we cannot tell, so we report true (resolved) to preserve current
// behavior rather than risk a wrong fallback on a working setup.
func infocmpResolves(name string) bool {
	if _, err := exec.LookPath("infocmp"); err != nil {
		return true
	}
	return exec.Command("infocmp", name).Run() == nil
}

// maybeTermFallback prepends `env TERM=xterm-256color` to a tmux launch argv
// when the current $TERM has no terminfo entry on the host (which would make
// tmux abort with "missing or unsuitable terminal"). Returns argv unchanged
// when there's nothing to fix or the fallback is disabled. A one-line notice
// is written to stderr when the fallback is applied. See #104.
func maybeTermFallback(stderr io.Writer, argv []string) []string {
	if len(argv) == 0 {
		return argv
	}
	term := os.Getenv("TERM")
	if term == "" || term == fallbackTerm {
		return argv
	}
	if os.Getenv("BRIDGE_NO_TERM_FALLBACK") != "" {
		return argv
	}
	if termResolver(term) {
		return argv
	}
	fmt.Fprintf(stderr,
		"bridge: TERM=%q has no terminfo entry on this host; launching tmux with TERM=%s (set BRIDGE_NO_TERM_FALLBACK=1 to disable)\n",
		term, fallbackTerm)
	return append([]string{"env", "TERM=" + fallbackTerm}, argv...)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./cmd/bridge -run TestMaybeTermFallback -v`
Expected: PASS (all subtests).

- [ ] **Step 5: Commit**

```bash
git add cmd/bridge/termfallback_unix.go cmd/bridge/termfallback_test.go
git commit -m "feat(launcher): TERM-fallback helper for unresolvable terminfo (#104)"
```

---

## Task 2: Windows no-op build of the helper

**Files:**
- Create: `cmd/bridge/termfallback_windows.go`

- [ ] **Step 1: Write minimal implementation**

There is no Windows CI and no `t.Setenv`-friendly cross-compile test here; correctness is "it compiles and is a pass-through." Create `cmd/bridge/termfallback_windows.go`:

```go
//go:build windows

package main

import "io"

// maybeTermFallback is a no-op on Windows: the launcher uses Windows Terminal
// (wt.exe), which has no terminfo concept, so there is nothing to fall back
// from. Mirrors the unix signature so preflight.go compiles on both. See #104.
func maybeTermFallback(_ io.Writer, argv []string) []string {
	return argv
}
```

- [ ] **Step 2: Verify it compiles for windows**

Run: `GOOS=windows GOARCH=amd64 go build ./cmd/bridge`
Expected: builds with no error. (Confirms the windows file's signature matches the call sites added in Task 3 — run this again after Task 3.)

- [ ] **Step 3: Commit**

```bash
git add cmd/bridge/termfallback_windows.go
git commit -m "feat(launcher): no-op TERM-fallback on Windows (#104)"
```

---

## Task 3: Wire emitLaunch into the 4 tmux launch sites

**Files:**
- Modify: `cmd/bridge/preflight.go`

- [ ] **Step 1: Add the emitLaunch wrapper**

In `cmd/bridge/preflight.go`, add this function (place it just below `runPreflight`, near the top of the file's function block):

```go
// emitLaunch applies the TERM-fallback to a tmux launch argv (a no-op on
// Windows / when not needed), then emits the exec directive. All tmux launch
// paths go through here so the kitty/terminfo fallback (#104) is uniform.
func emitLaunch(out io.Writer, argv []string) error {
	return shellbridge.EmitExec(out, maybeTermFallback(os.Stderr, argv))
}
```

- [ ] **Step 2: Swap the 4 call sites**

Replace each `shellbridge.EmitExec(out, argv)` launch call with `emitLaunch(out, argv)`:

1. In `preflightPickerWithRemote` — change:

```go
		argv, err := launcher.New().LaunchArgv(slotIDFor(repo, ""), repo.Path, spec)
			if err == nil {
				return shellbridge.EmitExec(out, argv)
			}
```
to:
```go
		argv, err := launcher.New().LaunchArgv(slotIDFor(repo, ""), repo.Path, spec)
			if err == nil {
				return emitLaunch(out, argv)
			}
```

2. In `preflightPicker` — change:

```go
		argv, err := launcher.New().LaunchArgv(slotIDFor(r, ""), r.Path, spec)
			if err == nil {
				return shellbridge.EmitExec(out, argv)
			}
```
to:
```go
		argv, err := launcher.New().LaunchArgv(slotIDFor(r, ""), r.Path, spec)
			if err == nil {
				return emitLaunch(out, argv)
			}
```

3. In `preflightOpen` (final return) — change:

```go
	return shellbridge.EmitExec(out, argv)
```
to:
```go
	return emitLaunch(out, argv)
```

4. In `preflightSessionsAttach` — change:

```go
	return shellbridge.EmitExec(out, launcher.New().AttachArgv(slot))
```
to:
```go
	return emitLaunch(out, launcher.New().AttachArgv(slot))
```

Note: `io` and `os` are already imported in `preflight.go`; no import changes needed. `shellbridge` stays imported (used by `emitLaunch` and other emits).

- [ ] **Step 3: Write an integration test for the open path**

Add to `cmd/bridge/preflight_test.go`:

```go
func TestPreflightOpenTermFallback(t *testing.T) {
	root := writeFakeRepos(t)
	cache := t.TempDir()
	// "bridge" is one of the fake repos created by writeFakeRepos
	// (github/freaxnx01/public/bridge).
	cmd := bridgeCmd("__preflight", "open", "bridge", "--agent", "claude")
	cmd.Env = append(os.Environ(),
		"BRIDGE_REPOS_ROOT="+root,
		"XDG_CACHE_HOME="+cache,
		"BRIDGE_NO_SYNC=1",
		"TERM=definitely-not-a-real-terminfo-xyz",
		"PATH="+os.Getenv("PATH"),
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
```

(`writeFakeRepos` in `list_test.go` creates `bridge`, `secret`, `glrepo`; `bridge` is used here.)

- [ ] **Step 4: Run the test to verify it fails, then passes**

Run: `go test ./cmd/bridge -run 'TestPreflightOpenTermFallback|TestMaybeTermFallback' -v`
Expected after Steps 1-2: PASS. (If `infocmp` is absent on the test host, `infocmpResolves` returns true and the env prefix is skipped — in that case the integration assertion would fail; this is why the unit test in Task 1, which stubs the resolver, is the authoritative coverage. If `go test` runs where `infocmp` is missing, skip this integration test with `t.Skip` guarded by `exec.LookPath("infocmp")`.)

Guarded form — make Step 3's test robust by adding at its top:

```go
	if _, err := exec.LookPath("infocmp"); err != nil {
		t.Skip("infocmp not installed; fallback can't trigger")
	}
```
(`os/exec` is already imported in `preflight_test.go`.)

- [ ] **Step 5: Run the full package + windows build**

Run: `go test ./cmd/bridge` then `GOOS=windows GOARCH=amd64 go build ./cmd/bridge`
Expected: tests PASS; windows build succeeds.

- [ ] **Step 6: Commit**

```bash
git add cmd/bridge/preflight.go cmd/bridge/preflight_test.go
git commit -m "fix(launcher): route tmux launches through TERM-fallback (#104)"
```

---

## Task 4: Docs + CHANGELOG

**Files:**
- Modify: `CHANGELOG.md`
- Modify: `docs/local-cc-session.md`
- Modify: `README.md`

- [ ] **Step 1: Add CHANGELOG entry**

In `CHANGELOG.md`, under `## [Unreleased]` → `### Fixed`, add a new bullet after the existing SSH bullet:

```markdown
- Launching under a terminal whose `$TERM` has no terminfo entry on the host (e.g. **kitty** → `xterm-kitty` on a Chromebook/Crostini box without kitty's terminfo) no longer fails with tmux's `missing or unsuitable terminal`. bridge now detects the unresolvable `$TERM` (via `infocmp`) and transparently launches tmux with `TERM=xterm-256color`, printing a one-line notice. Set `BRIDGE_NO_TERM_FALLBACK=1` to disable and surface the raw error. Unix-only; the Windows `wt.exe` path is unaffected (#104).
```

- [ ] **Step 2: Update the #104 state in docs/local-cc-session.md**

In `docs/local-cc-session.md`, in the "Current state" section, replace the `#104` bullet (the one starting `**#104** — tmux launch under **kitty** fails`) with:

```markdown
- **#104** — *fixed* (branch `fix/issue-104-term-fallback`). tmux launch under
  **kitty** on a host lacking the `xterm-kitty` terminfo aborted with
  `missing or unsuitable terminal`. bridge now detects an unresolvable `$TERM`
  via `infocmp` before launching tmux and transparently falls back to
  `TERM=xterm-256color` with a one-line notice (`internal` helper
  `maybeTermFallback` in `cmd/bridge`, applied via `emitLaunch`). Disable with
  `BRIDGE_NO_TERM_FALLBACK=1`.
```

Also, in section "1. Start a session", the kitty caveat can stay but note the fallback now handles it; update the paragraph that begins `On the **Chromebook + kitty** box (see #104)` to:

```markdown
On the **Chromebook + kitty** box (see #104), bridge now auto-falls-back to
`TERM=xterm-256color` when launching tmux, so `bridge` works directly. To opt
out and see the raw tmux error, set `BRIDGE_NO_TERM_FALLBACK=1`.
```

- [ ] **Step 3: Add a README troubleshooting note**

Find the troubleshooting / FAQ area of `README.md` (grep for `Troubleshoot` or `## ` headings; if none exists, add the note under the section describing tmux/launch behavior). Add:

```markdown
### kitty / "missing or unsuitable terminal: xterm-kitty"

If the host lacks kitty's terminfo entry (common on Chromebook/Crostini or
fresh SSH targets), tmux would abort with `missing or unsuitable terminal:
xterm-kitty`. bridge auto-detects an unresolvable `$TERM` (via `infocmp`) and
launches tmux with `TERM=xterm-256color`, printing a one-line notice. To keep
full kitty terminfo, install it on the host
(`infocmp -x xterm-kitty | tic -x -`) or set `term xterm-256color` in
`kitty.conf`. To disable the fallback and see the raw error, export
`BRIDGE_NO_TERM_FALLBACK=1`.
```

- [ ] **Step 4: Verify build + tests still green**

Run: `go build ./... && go test ./cmd/bridge`
Expected: build clean, tests PASS. (Docs-only changes, but confirm nothing references a renamed symbol.)

- [ ] **Step 5: Commit**

```bash
git add CHANGELOG.md docs/local-cc-session.md README.md
git commit -m "docs: document kitty/terminfo TERM-fallback (#104)"
```

---

## Task 5: Full verification + PR

**Files:** none (verification only)

- [ ] **Step 1: Full test + build pass**

Run: `make all` (or `go test ./... && make build-go`)
Expected: all Go tests PASS, shim bats pass, binary builds.

- [ ] **Step 2: Cross-compile sanity**

Run: `GOOS=windows GOARCH=amd64 go build ./cmd/bridge`
Expected: succeeds.

- [ ] **Step 3: Manual smoke (optional, on a kitty host without xterm-kitty terminfo)**

```bash
make install-go
TERM=xterm-kitty bridge open <some-repo> --agent claude
# Expect the one-line notice on stderr and tmux to actually start.
TERM=xterm-kitty BRIDGE_NO_TERM_FALLBACK=1 bridge open <some-repo> --agent claude
# Expect the raw "missing or unsuitable terminal" path (no env prefix).
```

- [ ] **Step 4: Push and open PR**

```bash
git push
gh pr create --fill
```
Then in the PR body, note: directive protocol unchanged → no shim/bats edits; Windows path is a build-tagged no-op (manual PowerShell check N/A — behavior unchanged there); closes #104.

---

## Self-Review

**Spec coverage:**
- Auto-fallback + notice → Task 1 (`maybeTermFallback` writes notice + prefixes argv).
- `infocmp` detection, skip when unset/xterm-256color, infocmp-missing→resolved → Task 1.
- `BRIDGE_NO_TERM_FALLBACK` disable → Task 1.
- `env TERM=…` injection working on all 3 argv shapes + both shim paths → Task 3 (wired at all 4 sites incl. attach + the nested/open paths).
- Windows no-op / parity → Task 2 + windows build checks in Tasks 2,3,5.
- Tests with stubbed resolver → Task 1; integration → Task 3.
- Docs (local-cc-session, README, CHANGELOG) → Task 4.

**Placeholder scan:** No TBD/TODO; all code shown in full. Two conditional instructions (confirm `repo-a` exists; add `t.Skip` guard) are explicit and actionable, not placeholders.

**Type consistency:** `maybeTermFallback(io.Writer, []string) []string` and `termResolver func(string) bool` are used identically in Tasks 1, 2, 3. `fallbackTerm = "xterm-256color"` used consistently. `emitLaunch(io.Writer, []string) error` matches its 4 call sites.
