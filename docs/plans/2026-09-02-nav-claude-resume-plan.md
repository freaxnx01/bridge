# `bridge nav` — launch a session with `claude --resume` — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a dashboard keybinding (`r`) that launches a **new** session with `claude --resume` for the selected worktree row, instead of a plain `claude`, when that row has no live session. A row that already has a live session is unaffected — attach still wins.

**Architecture:** Thread a `resume bool` parameter through the existing pure argv-building path `launchPlan` → `launchArgvFor` / `launchRow` (`internal/nav/update.go`). When `resume` is true, the row has no live session, and the resolved agent is `claude`, append `"--resume"` to `spec.Args` at the same point `NameArgs`/`AgentArgs` already extend it. Add one new key case in `updateDashWorktrees`. Update the dashboard hint line. No new types, no I/O changes, no launcher-interface changes.

**Tech Stack:** Go, `charmbracelet/bubbletea` (Model-Update-View). Stdlib `testing`, table-driven, hand-rolled fakes — no testify.

**Spec:** [`docs/specs/2026-09-02-nav-claude-resume-design.md`](../specs/2026-09-02-nav-claude-resume-design.md)

**Conventions for every task:** run `gofmt -w` on touched files; final gate is `go test -race ./...`, `go vet ./...`, `golangci-lint run`. Commit messages are Conventional Commits; do not push until the user asks. All paths below are relative to the repo root.

---

## File structure

All files already exist; every change is a modification.

- `internal/nav/update.go` (+ `update_test.go`) — `launchPlan`, `launchArgvFor`, `launchRow` gain a `resume bool` parameter; new `"r"` case in `updateDashWorktrees`.
- `internal/nav/view.go` (+ `view_test.go` if a hint-line test exists) — dashboard hint line gains `· r resume`.
- `CHANGELOG.md` — `[Unreleased]` entry.

Task order: argv-building change (with tests) → keybinding wiring → hint line → docs. After Task 1 the package still compiles and all existing tests pass unchanged (new parameter defaults to `false` at the one pre-existing call site).

---

## Task 1: Thread `resume` through `launchPlan` / `launchArgvFor` / `launchRow`

**Files:**
- Modify: `internal/nav/update.go`
- Modify: `internal/nav/update_test.go`

- [ ] **Step 1: Write the failing test first**

Add table-driven cases to whichever existing test covers `launchPlan` (or add a new `TestLaunchPlan_Resume` if none exists — check `update_test.go` for the current test name first). Cover:

  1. `resume=true`, agent `claude`, row has no live session → returned argv contains `--resume` as the last element after the agent's other args.
  2. `resume=true`, row **has** a live session (`hasSession: true, slotID: "x"`) → returned argv is identical to `resume=false` (attach argv, unaffected by resume).
  3. `resume=true`, `m.cfg.DefaultAgent` set to a non-`claude` agent (e.g. `"opencode"`), no live session → returned argv does **not** contain `--resume` (non-goal: only `claude` gets the flag).
  4. `resume=false` (existing behavior) → argv unchanged from before this change, for both the has-session and no-session cases.

  Use the existing fakes/helpers in `update_test.go` for building a `Model` and `dashRow` — do not add new mocking machinery.

- [ ] **Step 2: Run it — confirm it fails to compile** (signature doesn't have the parameter yet)

```bash
go test ./internal/nav/... -run TestLaunchPlan
```

- [ ] **Step 3: Implement the minimum change**

In `internal/nav/update.go`:

```go
func (m Model) launchPlan(row dashRow, resume bool) (argv []string, slot, agent string, err error) {
	l := launcher.New()
	if row.hasSession && row.slotID != "" {
		return l.AttachArgv(row.slotID), "", "", nil
	}
	agent = m.cfg.DefaultAgent
	if agent == "" {
		agent = "claude"
	}
	spec, err := agents.Resolve(agent)
	if err != nil {
		return nil, "", "", err
	}
	if m.cfg.NameArgs != nil {
		if na := m.cfg.NameArgs(agent, m.repo, row.worktree, row.displayLabel); len(na) > 0 {
			spec.Args = append(append([]string{}, na...), spec.Args...)
		}
	}
	if len(m.cfg.AgentArgs) > 0 {
		spec.Args = append(append([]string{}, spec.Args...), m.cfg.AgentArgs...)
	}
	if resume && agent == "claude" {
		spec.Args = append(spec.Args, "--resume")
	}
	slot = core.SlotID(m.repo.Name, row.worktree)
	argv, err = l.LaunchArgv(slot, row.path, spec)
	if err != nil {
		return nil, "", "", err
	}
	return argv, slot, agent, nil
}
```

Update the two callers:

```go
func (m Model) launchArgvFor(row dashRow, resume bool) ([]string, error) {
	argv, _, _, err := m.launchPlan(row, resume)
	return argv, err
}

func (m Model) launchRow(row dashRow, resume bool) (tea.Model, tea.Cmd) {
	argv, slot, agent, err := m.launchPlan(row, resume)
	...
}
```

Update every existing call site to pass `false` (the "+ create" row Enter path and any other current caller of `launchRow`/`launchArgvFor`) — grep for both names to find them all:

```bash
grep -rn "launchRow(\|launchArgvFor(" internal/nav/*.go
```

- [ ] **Step 4: Run it — confirm it passes**

```bash
go test ./internal/nav/... -run TestLaunchPlan
```

- [ ] **Step 5: Run the full package suite**

```bash
go test -race ./internal/nav/...
```

## Task 2: Add the `r` keybinding

**Files:**
- Modify: `internal/nav/update.go`
- Modify: `internal/nav/update_test.go`

- [ ] **Step 1: Write the failing test first**

Add a test (alongside existing `updateDashWorktrees` key-handling tests, matching their style) that:

  1. Sends `"r"` when the selected row has no live session → asserts a launch `tea.Cmd` is returned equivalent to what `launchRow(row, true)` would produce, and `m.status` is unchanged.
  2. Sends `"r"` when the selected row **has** a live session → asserts no launch happens (or an attach-equivalent, per the spec's "resume is ignored, attach wins" rule — match whatever `launchPlan` already returns for that case) and `m.status` is set to the "session already attached" message.
  3. Sends `"r"` when `m.dashSel == len(m.dashRows)` (the "+ create" row) → no-op, model unchanged (same guard shape as the existing `enter` case).

- [ ] **Step 2: Run it — confirm it fails**

```bash
go test ./internal/nav/... -run TestUpdateDashWorktrees
```

- [ ] **Step 3: Implement the minimum change**

In `updateDashWorktrees` (`internal/nav/update.go`), add alongside the existing `"enter"` case:

```go
case "r":
	if m.dashSel < len(m.dashRows) {
		row := m.dashRows[m.dashSel]
		if row.hasSession {
			m.status = "session already attached — resume only applies to a new session"
			return m, nil
		}
		return m.launchRow(row, true)
	}
```

And change the existing `"enter"` case's launch call from `m.launchRow(m.dashRows[m.dashSel])` to `m.launchRow(m.dashRows[m.dashSel], false)`.

- [ ] **Step 4: Run it — confirm it passes**

```bash
go test ./internal/nav/... -run TestUpdateDashWorktrees
```

- [ ] **Step 5: Run the full package suite**

```bash
go test -race ./internal/nav/...
```

## Task 3: Hint line + CHANGELOG

**Files:**
- Modify: `internal/nav/view.go`
- Modify: `internal/nav/view_test.go` (only if an existing test asserts the exact hint-line string — grep first)
- Modify: `CHANGELOG.md`

- [ ] **Step 1: Check for an existing hint-line assertion**

```bash
grep -rn "attach/launch" internal/nav/*_test.go
```

If found, update it as part of this task's test-first step (change the expected string, confirm it fails, then fix the source). If not found, skip straight to Step 2 — there's no failing test to write for a display-string change with no existing coverage.

- [ ] **Step 2: Update the hint line**

In `viewDash` (`internal/nav/view.go`, currently around line 351):

```go
hint := m.hintLine("↑↓ move · tab panes · ⏎ attach/launch · r resume · n new worktree · ? legend · esc back · q quit")
```

- [ ] **Step 3: Run the full package suite**

```bash
go test -race ./internal/nav/...
```

- [ ] **Step 4: Add a `CHANGELOG.md` `[Unreleased]` entry**

Under `### Added`:

```markdown
- `bridge nav`: press `r` on a worktree with no live session to launch it with `claude --resume` (#257)
```

## Task 4: Final gate

- [ ] **Step 1:** `gofmt -l .` — must be empty
- [ ] **Step 2:** `go vet ./...`
- [ ] **Step 3:** `golangci-lint run`
- [ ] **Step 4:** `go test -race ./...` — full suite, not just `internal/nav`
- [ ] **Step 5:** Commit with a Conventional Commits message (`feat(nav): ...`), referencing `Closes #257`. Do not push until asked.
