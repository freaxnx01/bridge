# Claude Launch Naming Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Restore the bash bridge's `claude -n "<repo>"` / `"<repo> [<worktree>]"` launch label in the Go port (issue #84), so launched Claude Code sessions are identifiable in Claude's picker and terminal title.

**Architecture:** Two pure helpers (`displayName`, `withClaudeName`) live in `cmd/bridge/preflight.go`. Every preflight launch path calls `withClaudeName(spec, repo, worktree)` immediately after the spec is resolved and before argv is built. The helper prepends `-n <name>` only when `spec.Name == "claude"`; other agents (copilot/opencode/code) pass through unchanged. The shared `agents.registry` spec is never mutated — `withClaudeName` allocates a fresh `Args` slice.

**Tech Stack:** Go 1.x, Cobra, existing `internal/agents` + `internal/launcher` + `internal/shellbridge` packages. Tests follow the established `bridgeCmd(...)`/`writeFakeRepos(...)` integration pattern that spawns the prebuilt binary and asserts on the emitted directive string.

**Spec:** `docs/superpowers/specs/2026-05-28-claude-launch-naming-design.md` (Approved).

**Out of scope:** the `/clear → /rename` restore hook (#20). Not implemented here.

---

## File Structure

| File | Action | Responsibility |
|---|---|---|
| `cmd/bridge/preflight.go` | Modify | Add `displayName` + `withClaudeName` helpers; call `withClaudeName` at the three modification sites described below. |
| `cmd/bridge/preflight_test.go` | Modify | Add unit tests for both helpers + integration tests asserting the emitted `exec:` directive contains `claude -n <name>` (with and without `-w`). |

No new files; no changes outside `cmd/bridge`.

**Note on "three modification sites" vs the spec's "four launch sites":** in `preflightOpen` the `LaunchArgv` / `LaunchArgvNested` branch operates on a single resolved `spec` variable, so one `withClaudeName` call before the branch covers both launch sites. The spec table is correct about *launch sites*; the *code edits* are: `preflightPicker` (line ~146), `preflightPickerWithRemote` (line ~124), and `preflightOpen` (one line just before the `if os.Getenv("TMUX") != ""` branch at ~256).

---

## Branch & PR setup

A `feat/claude-launch-naming` branch already exists on `origin` containing only the design-doc commit. That commit has been cherry-picked to `main` (commits `d5a88a0` + `dc159ee`). The implementation will land on a fresh branch off `main` named `feat/claude-launch-naming-impl`.

- [ ] **Step 0.1: Verify current state**

Run:

```bash
git status
git log --oneline -3
```

Expected: branch `main`, working tree clean, top commit is `dc159ee docs(specs): mark claude-launch-naming spec approved; list 4 launch sites`.

- [ ] **Step 0.2: Create implementation branch**

Run:

```bash
git switch -c feat/claude-launch-naming-impl
```

Expected: `Switched to a new branch 'feat/claude-launch-naming-impl'`.

---

### Task 1: Add helpers with unit tests (TDD)

**Files:**

- Modify: `cmd/bridge/preflight.go` (append two unexported funcs near the bottom, after `slotIDFor`)
- Modify: `cmd/bridge/preflight_test.go` (append unit tests at the bottom)

- [ ] **Step 1.1: Write the failing unit tests**

Append to `cmd/bridge/preflight_test.go`:

```go
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
```

Add `"reflect"` to the existing test-file imports if not already present, and the `"github.com/freaxnx01/bridge/internal/agents"` + `"github.com/freaxnx01/bridge/internal/core"` package imports.

- [ ] **Step 1.2: Run tests to verify they fail**

Run:

```bash
go test ./cmd/bridge -run 'TestDisplayName|TestWithClaudeName' -v
```

Expected: compile error — `undefined: displayName` and `undefined: withClaudeName`.

- [ ] **Step 1.3: Implement the helpers**

Append to `cmd/bridge/preflight.go` (after the existing `slotIDFor` function at the end of the file):

```go
// displayName returns the claude session display name for a repo launch:
// "<repo>" normally, "<repo> [<worktree>]" when a worktree is given. Matches
// the bash bridge's label.
func displayName(repo core.Repo, worktree string) string {
	if worktree != "" {
		return repo.Name + " [" + worktree + "]"
	}
	return repo.Name
}

// withClaudeName prepends `-n <displayName>` to a claude spec's args so the
// launched session is named in the picker/terminal title. No-op for non-claude
// agents (only claude has --name). Builds a fresh Args slice so the shared
// registry spec is never mutated.
func withClaudeName(spec agents.AgentSpec, repo core.Repo, worktree string) agents.AgentSpec {
	if spec.Name != "claude" {
		return spec
	}
	spec.Args = append([]string{"-n", displayName(repo, worktree)}, spec.Args...)
	return spec
}
```

- [ ] **Step 1.4: Run unit tests to verify they pass**

Run:

```bash
go test ./cmd/bridge -run 'TestDisplayName|TestWithClaudeName' -v
```

Expected: all 5 tests PASS.

- [ ] **Step 1.5: Commit**

```bash
git add cmd/bridge/preflight.go cmd/bridge/preflight_test.go
git commit -m "feat(preflight): add displayName + withClaudeName helpers

Pure helpers that build the claude -n display name and prepend it to a
spec's Args. No-op for non-claude agents. Registry spec is never mutated
(fresh slice on every call)."
```

---

### Task 2: Wire all three call sites

**Files:**

- Modify: `cmd/bridge/preflight.go` — three one-line edits.

- [ ] **Step 2.1: Wire `preflightPickerWithRemote`**

In `cmd/bridge/preflight.go`, locate the block in `preflightPickerWithRemote` that currently reads:

```go
	if spec, ok := resolveDefaultAgent(); ok {
		argv, err := launcher.New().LaunchArgv(slotIDFor(repo, ""), repo.Path, spec)
```

Replace with:

```go
	if spec, ok := resolveDefaultAgent(); ok {
		spec = withClaudeName(spec, repo, "")
		argv, err := launcher.New().LaunchArgv(slotIDFor(repo, ""), repo.Path, spec)
```

- [ ] **Step 2.2: Wire `preflightPicker`**

In the same file, locate the block in `preflightPicker` that currently reads:

```go
	if spec, ok := resolveDefaultAgent(); ok {
		argv, err := launcher.New().LaunchArgv(slotIDFor(r, ""), r.Path, spec)
```

Replace with:

```go
	if spec, ok := resolveDefaultAgent(); ok {
		spec = withClaudeName(spec, r, "")
		argv, err := launcher.New().LaunchArgv(slotIDFor(r, ""), r.Path, spec)
```

- [ ] **Step 2.3: Wire `preflightOpen` (covers both `LaunchArgv` and `LaunchArgvNested`)**

In `preflightOpen`, locate the existing block:

```go
	slot := slotIDFor(repo, worktree)
	// Record the slot in the registry. Non-fatal on failure — emitting the
```

Just above that line, insert one line so the surrounding code becomes:

```go
	spec = withClaudeName(spec, repo, worktree)
	slot := slotIDFor(repo, worktree)
	// Record the slot in the registry. Non-fatal on failure — emitting the
```

`spec` is already in scope at that point and is the variable consumed by both `LaunchArgvNested` (TMUX branch) and `LaunchArgv` (top-level branch) further down.

- [ ] **Step 2.4: Verify compilation**

Run:

```bash
go build ./...
```

Expected: no errors.

- [ ] **Step 2.5: Run the existing test suite to catch regressions**

Run:

```bash
go test ./cmd/bridge -count=1
```

Expected: PASS. (Existing tests that match `claude` in argv with `strings.Contains(s, " claude")` continue to hold — `-n <name>` comes between `claude` and the rest, but the substring is still present.)

- [ ] **Step 2.6: Commit**

```bash
git add cmd/bridge/preflight.go
git commit -m "feat(preflight): name claude sessions at launch (-n <repo>)

Wires withClaudeName at all three call sites: preflightPicker,
preflightPickerWithRemote, and preflightOpen (covering both the TMUX-
nested and top-level launch branches). Restores the bash bridge's
launch label. Closes #84."
```

---

### Task 3: Integration tests for the emitted directive

**Files:**

- Modify: `cmd/bridge/preflight_test.go` — append integration tests using the existing `bridgeCmd` / `writeFakeRepos` / `envWithout` helpers.

- [ ] **Step 3.1: Write the failing integration tests**

Append to `cmd/bridge/preflight_test.go`:

```go
// --- launch-naming integration (#84) ---

func TestPreflightOpenClaudeNamesSession(t *testing.T) {
	// `bridge open bridge --agent claude` must emit `claude -n bridge` in argv.
	root := writeFakeRepos(t)
	cache := t.TempDir()
	cmd := bridgeCmd("__preflight", "open", "bridge", "--agent", "claude")
	cmd.Env = append(envWithout("TMUX"),
		"BRIDGE_REPOS_ROOT="+root,
		"XDG_CACHE_HOME="+cache,
	)
	out, _ := cmd.CombinedOutput()
	s := strings.TrimSpace(string(out))
	if !strings.Contains(s, " claude -n bridge ") {
		t.Errorf("expected ' claude -n bridge ' in directive, got: %s", s)
	}
}

func TestPreflightOpenClaudeWorktreeNamesSession(t *testing.T) {
	// With -w feature-x the display name must be the sh-quoted 'bridge [feature-x]'.
	root := writeFakeRepos(t)
	cache := t.TempDir()
	cmd := bridgeCmd("__preflight", "open", "bridge", "-w", "feature-x", "--agent", "claude")
	cmd.Env = append(envWithout("TMUX"),
		"BRIDGE_REPOS_ROOT="+root,
		"XDG_CACHE_HOME="+cache,
	)
	out, _ := cmd.CombinedOutput()
	s := strings.TrimSpace(string(out))
	if !strings.Contains(s, " claude -n 'bridge [feature-x]' ") {
		t.Errorf("expected sh-quoted worktree name in directive, got: %s", s)
	}
}

func TestPreflightOpenDefaultAgentClaudeNamesSession(t *testing.T) {
	// With BRIDGE_DEFAULT_AGENT=claude (no explicit --agent) the name must
	// still be injected.
	root := writeFakeRepos(t)
	cache := t.TempDir()
	cmd := bridgeCmd("__preflight", "open", "bridge")
	cmd.Env = append(envWithout("TMUX"),
		"BRIDGE_REPOS_ROOT="+root,
		"XDG_CACHE_HOME="+cache,
		"BRIDGE_DEFAULT_AGENT=claude",
	)
	out, _ := cmd.CombinedOutput()
	s := strings.TrimSpace(string(out))
	if !strings.Contains(s, " claude -n bridge ") {
		t.Errorf("expected ' claude -n bridge ' in directive, got: %s", s)
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
```

- [ ] **Step 3.2: Run the new integration tests**

Run:

```bash
go test ./cmd/bridge -run 'TestPreflightOpenClaudeNamesSession|TestPreflightOpenClaudeWorktreeNamesSession|TestPreflightOpenDefaultAgentClaudeNamesSession|TestPreflightOpenNonClaudeAgentUnchanged' -v
```

Expected: all 4 PASS. (If they fail, the wiring in Task 2 was missed at one of the sites.)

- [ ] **Step 3.3: Run the whole `cmd/bridge` test suite once more**

Run:

```bash
go test ./cmd/bridge -count=1
```

Expected: PASS.

- [ ] **Step 3.4: Run the full repo test suite**

Run:

```bash
go test ./...
```

Expected: PASS across all packages.

- [ ] **Step 3.5: Commit**

```bash
git add cmd/bridge/preflight_test.go
git commit -m "test(preflight): integration coverage for claude -n at launch

Asserts the emitted exec: directive contains 'claude -n <repo>' for
explicit --agent claude, the BRIDGE_DEFAULT_AGENT=claude path, and the
sh-quoted 'bridge [feature-x]' form with -w. Verifies non-claude agents
(code) still launch without -n."
```

---

### Task 4: Cross-shell parity check and PR

**Files:**

- No code changes; manual verification + PR open.

- [ ] **Step 4.1: Build + reinstall locally**

Run:

```bash
make install-go
bridge --version
```

Expected: install succeeds, `bridge --version` prints a version string.

- [ ] **Step 4.2: Hand-exercise the bash shim path**

Run:

```bash
BRIDGE_DEFAULT_AGENT=claude bridge __preflight open bridge
```

Expected: stdout starts with `exec:tmux new-session -A -s bridge -c` and the substring ` claude -n bridge ` appears in the output. (Do not pipe to `eval`; we just want to inspect the directive.)

Then with a worktree (use any worktree name; the path need not exist for preflight):

```bash
BRIDGE_DEFAULT_AGENT=claude bridge __preflight open bridge -w feature-x
```

Expected: output contains ` claude -n 'bridge [feature-x]' ` (sh-quoted with single quotes).

- [ ] **Step 4.3: PowerShell shim — note manual gap**

There is no Windows CI. Note in the PR body that the PowerShell shim path was NOT exercised this round, and that worktree names containing spaces are the known minor risk on Windows.

- [ ] **Step 4.4: Push branch and open draft PR**

Run:

```bash
git push -u origin feat/claude-launch-naming-impl
gh pr create --title "feat(open): name the claude session at launch (-n, worktree-aware)" --body "$(cat <<'EOF'
## Summary

Closes #84. Restores the old bash bridge's `claude -n "<repo>"` /
`"<repo> [<worktree>]"` launch label in the Go port, so launched Claude
Code sessions are identifiable in Claude's picker and the terminal
title.

- Two pure helpers in `cmd/bridge/preflight.go`: `displayName` and
  `withClaudeName`. Claude-only — non-claude agents pass through.
- Wired at the three modification sites in `preflight.go`:
  `preflightPicker`, `preflightPickerWithRemote`, and `preflightOpen`
  (covering both the inside-tmux `LaunchArgvNested` and top-level
  `LaunchArgv` branches).
- The shared `agents.registry` spec is never mutated — `withClaudeName`
  always allocates a fresh `Args` slice.

Out of scope: the `/clear → /rename` restore hook (#20). Separate
follow-up.

Spec: `docs/superpowers/specs/2026-05-28-claude-launch-naming-design.md`.

## Test plan

- [x] `go test ./...` — passes
- [x] Unit: `displayName`, `withClaudeName` (claude vs non-claude, with/without worktree, registry-immutability)
- [x] Integration: emitted `exec:` directive contains `claude -n bridge` for both `--agent claude` and `BRIDGE_DEFAULT_AGENT=claude`; sh-quoted `'bridge [feature-x]'` with `-w`; non-claude agent (`code`) unchanged
- [x] Hand-exercised bash shim path via `bridge __preflight open …`
- [ ] PowerShell shim path NOT exercised this round (no Windows CI). Worktree names containing spaces are the known minor risk on Windows.

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)" --draft
```

Expected: PR is created in draft state and the URL is printed.

- [ ] **Step 4.5: Clean up the old design-only branch**

The `feat/claude-launch-naming` remote branch contained only the design commit which is now on `main`. Confirm with the user before deleting (destructive op on remote):

> "The remote branch `feat/claude-launch-naming` is now obsolete (its only commit, the design doc, is on `main` and on `feat/claude-launch-naming-impl`). OK to delete it? (`git push origin --delete feat/claude-launch-naming`)"

Wait for explicit user OK before running the deletion.

---

## Self-review

**Spec coverage:**

| Spec requirement | Task |
|---|---|
| `displayName(repo, worktree)` helper | Task 1 (1.1, 1.3) |
| `withClaudeName(spec, repo, worktree)` helper | Task 1 (1.1, 1.3) |
| Claude-only; non-claude pass-through | Task 1 (1.1 `TestWithClaudeNameNonClaudePassthrough`), Task 3 (3.1 `TestPreflightOpenNonClaudeAgentUnchanged`) |
| Worktree-aware naming | Task 1 (1.1 `TestDisplayName`/`TestWithClaudeNameClaudeWithWorktree`), Task 3 (3.1 worktree test) |
| Don't mutate registry spec | Task 1 (1.1 `TestWithClaudeNameDoesNotMutateRegistry`) |
| Wired at preflightPicker | Task 2.2 |
| Wired at preflightPickerWithRemote | Task 2.1 |
| Wired at preflightOpen (both TMUX branches) | Task 2.3 |
| Integration test: `exec:` directive contains `claude -n <name>` | Task 3.1 |
| Integration test: sh-quoted `'<repo> [<wt>]'` | Task 3.1 |
| Cross-shell parity note | Task 4.3, PR body |
| `/clear → /rename` deferred | Acknowledged in plan header and PR body |

**Placeholder scan:** every step has either runnable commands or complete code blocks. No "TBD" or "handle edge cases" generic instructions.

**Type consistency:** `displayName(core.Repo, string) string` and `withClaudeName(agents.AgentSpec, core.Repo, string) agents.AgentSpec` are referenced identically in unit tests (Task 1) and call sites (Task 2). `spec.Name == "claude"` matches `agents.AgentSpec.Name`.
