# Dotted Repo Names — tmux-safe Slot IDs Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `bridge launch <repo-with-dots>` creates, detects, re-detects (`already_running`) and RC-scrapes the same tmux session, because the slot id already equals the name tmux stores.

**Architecture:** tmux 3.x silently rewrites `.` and `:` in a session name to `_` (verified on tmux 3.4: `new-session -s a.b:c` → session `a_b_c`; `has-session -t a.b:c` → "can't find session"). Every bridge path — launch argv, `sessionLive`, `already_running`, `waitRCURL`, slot registry, nav, herdr — derives the name from `core.SlotID`. So the fix is one place: `core.SlotID` applies the same rewrite tmux does. Only the *id* is sanitized; repo name, display label (`claude -n`), and working directory keep their dots.

**Tech Stack:** Go, stdlib `testing` (table-driven, hand-rolled fakes), tmux 3.4.

**Spec:** GitHub issue #337 (no separate spec doc — the issue's "Expected" section is the spec).

## Global Constraints

- Use Test-Driven Development for every task: write a failing test first, watch it fail, implement minimally to pass, verify green.
- No new Go modules; no `go.mod` changes.
- Sanitization mirrors tmux exactly: `.` → `_`, `:` → `_`. Nothing else is rewritten (no lower-casing, no other characters) — a broader rewrite would rename existing, working sessions.
- `core.SlotID` stays the single source of truth; do not add a second sanitizer in `cmd/bridge` or `internal/nav`.
- Commits reference `#337` (Conventional Commits, `fix(core): …`).
- After the change: `gofmt -l .` empty, `go vet ./...`, `golangci-lint run`, `go test -race ./...` all green.

## Review Focus

1. **Dot in the worktree name** (`bridge launch r -w fix.1`) → slot `r-wt-fix_1`; the worktree *directory* is still `.worktrees/fix.1`. Pinned in Task 1 (SlotID table + note that `WorkDir` is computed from the raw name in `cmd/bridge/launch_target.go`, untouched).
2. **herdr path → slot mapping for a dotted repo dir** (`/x/freaxnx01.github.io`) must produce the same id as launch did, or herdr-backed sessions won't match their slot. Pinned in Task 1 (herdr test case).
3. **Display label keeps the dots** — `displayName` uses `repo.Name`, not the slot id; the claude picker shows `freaxnx01.github.io`. No code change; reviewer confirms `displayName` is untouched.
4. **Stale `slots.json` entries** written before the fix carry dotted ids that no live session matches — they just show as non-live cache rows. Acceptable (registry is ephemeral cache); no migration.
5. **Names without `.`/`:`** must be byte-identical to today (no rename of existing live sessions). Pinned by the existing SlotID cases staying green.

---

### Task 1: tmux-safe `core.SlotID`

**Files:**
- Modify: `internal/core/slot.go:24-33` (`SlotID`)
- Test: `internal/core/slot_test.go` (`TestSlotID_RepoAndWorktree`)
- Test: `internal/herdr/path_test.go` (add a dotted-repo case to the existing `SlotIDForPath` test; create the table case in the existing style — read the file first)

**Interfaces:**
- Consumes: nothing new.
- Produces: `func SlotID(repoName, worktree string) string` — unchanged signature; output now has `.` and `:` replaced by `_`.

- [ ] **Step 1: Write the failing tests**

Extend the table in `internal/core/slot_test.go`:

```go
	tests := []struct{ name, repo, wt, want string }{
		{"no worktree", "bridge", "", "bridge"},
		{"with worktree", "bridge", "fix-x", "bridge-wt-fix-x"},
		{"dotted repo", "freaxnx01.github.io", "", "freaxnx01_github_io"},
		{"dotted repo and worktree", "freaxnx01.github.io", "fix.1", "freaxnx01_github_io-wt-fix_1"},
		{"colon", "a:b", "", "a_b"},
	}
```

Add to the `SlotIDForPath` table in `internal/herdr/path_test.go` a case for a plain dotted repo dir, e.g. cwd `/home/u/repos/freaxnx01.github.io` → `freaxnx01_github_io`, and a dotted repo worktree dir `/home/u/repos/freaxnx01.github.io/.worktrees/fix.1` → `freaxnx01_github_io-wt-fix_1` (match the test file's existing field names and path shape).

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/core -run TestSlotID ./internal/herdr -run SlotIDForPath -v`
Expected: FAIL — e.g. `SlotID("freaxnx01.github.io","")="freaxnx01.github.io" want "freaxnx01_github_io"`.

- [ ] **Step 3: Minimal implementation**

In `internal/core/slot.go` (add `"strings"` to imports):

```go
// tmuxNameReplacer mirrors tmux's own session-name rewrite: tmux silently
// turns '.' and ':' into '_' (they are target separators), so a slot id must
// already be in that form or has-session / capture-pane miss the session.
var tmuxNameReplacer = strings.NewReplacer(".", "_", ":", "_")

// SlotID is the deterministic tmux session name / slot id for a repo and
// optional worktree: "<repo>" or "<repo>-wt-<worktree>", with '.' and ':'
// replaced by '_' exactly as tmux does. It is the single source of truth
// shared by the launch (cmd/bridge) and navigator (internal/nav) paths.
func SlotID(repoName, worktree string) string {
	id := repoName
	if worktree != "" {
		id += "-wt-" + worktree
	}
	return tmuxNameReplacer.Replace(id)
}
```

(`strings.Replacer` is immutable and safe for concurrent use, so the package-level var is not mutable global state.)

- [ ] **Step 4: Run tests to verify they pass, then the full gate**

Run: `go test ./internal/core ./internal/herdr -v -run 'SlotID'` → PASS
Then: `gofmt -l .` (empty), `go vet ./...`, `golangci-lint run`, `go test -race ./...` → all green.

- [ ] **Step 5: Manual smoke (isolated tmux socket, no real agent)**

```bash
S=bridge337; trap 'tmux -L $S kill-server 2>/dev/null' EXIT
tmux -L $S new-session -d -s freaxnx01_github_io 'sleep 30'
tmux -L $S has-session -t freaxnx01_github_io && echo "slot id resolves"
```

Expected: `slot id resolves` (the id SlotID now returns is the name tmux keeps).

- [ ] **Step 6: Commit**

```bash
git add internal/core/slot.go internal/core/slot_test.go internal/herdr/path_test.go docs/superpowers/plans/2026-10-07-issue-337-dotted-slot-names.md
git commit -m "fix(core): make slot ids tmux-safe for dotted repo names

tmux rewrites '.' and ':' in session names to '_', so launch, the
liveness check, already_running and RC-link scraping all looked up a
name tmux never kept.

Closes #337"
```
