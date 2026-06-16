# bridge direnv symlink clone fix + auto-allow Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `bridge` clone remote repos through a symlinked reposRoot without direnv "blocked" errors, and auto-approve the token `.envrc` when still blocked.

**Architecture:** In `cloneRemoteRepo`, resolve the directory handed to `direnv exec` with `filepath.EvalSymlinks` so it matches the canonical path `direnv allow` records. Then, if that token `.envrc` is still blocked, run `direnv allow` once and note it before cloning. Pure logic (stderr-block detection, path resolution) is extracted into small tested helpers; the direnv/git shell-outs stay thin and untested, matching the file's existing style.

**Tech Stack:** Go (stdlib `os/exec`, `path/filepath`, `strings`, `bytes`), standard `testing` table-driven tests.

---

### Task 1: `isDirenvBlocked` stderr predicate

**Files:**
- Modify: `cmd/bridge/picker_remote.go` (add helper near `cloneRemoteRepo`)
- Test: `cmd/bridge/picker_remote_test.go` (add table test)

- [ ] **Step 1: Write the failing test**

Add to `cmd/bridge/picker_remote_test.go`:

```go
func TestIsDirenvBlocked(t *testing.T) {
	tests := []struct {
		name   string
		stderr string
		want   bool
	}{
		{"blocked message", "direnv: error /x/.envrc is blocked. Run `direnv allow` to approve its content", true},
		{"normal loading", "direnv: loading ~/x/.envrc", false},
		{"empty", "", false},
		{"unrelated error", "direnv: error: command not found", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isDirenvBlocked(tt.stderr); got != tt.want {
				t.Errorf("isDirenvBlocked(%q) = %v, want %v", tt.stderr, got, tt.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/bridge -run TestIsDirenvBlocked`
Expected: FAIL — `undefined: isDirenvBlocked`

- [ ] **Step 3: Write minimal implementation**

Add to `cmd/bridge/picker_remote.go` (above `cloneRemoteRepo`):

```go
// isDirenvBlocked reports whether direnv's stderr indicates the rc file is
// blocked (not yet approved). direnv exits 0 for a blocked-but-ran command,
// so the stderr text is the reliable signal.
func isDirenvBlocked(stderr string) bool {
	return strings.Contains(stderr, "is blocked")
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./cmd/bridge -run TestIsDirenvBlocked`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add cmd/bridge/picker_remote.go cmd/bridge/picker_remote_test.go
git commit -m "feat(clone): add isDirenvBlocked stderr predicate"
```

---

### Task 2: `resolveDirenvDir` symlink canonicalization (Part 1 fix)

**Files:**
- Modify: `cmd/bridge/picker_remote.go` (add helper; use it in `cloneRemoteRepo`)
- Test: `cmd/bridge/picker_remote_test.go`

- [ ] **Step 1: Write the failing test**

Add to `cmd/bridge/picker_remote_test.go`:

```go
func TestResolveDirenvDir_Symlink_ReturnsRealPath(t *testing.T) {
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	got, err := resolveDirenvDir(link)
	if err != nil {
		t.Fatalf("resolveDirenvDir(%q): %v", link, err)
	}
	want, err := filepath.EvalSymlinks(real)
	if err != nil {
		t.Fatalf("EvalSymlinks(%q): %v", real, err)
	}
	if got != want {
		t.Errorf("resolveDirenvDir(%q) = %q, want %q", link, got, want)
	}
}
```

Ensure `os` and `path/filepath` are imported in the test file (add if missing).

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/bridge -run TestResolveDirenvDir`
Expected: FAIL — `undefined: resolveDirenvDir`

- [ ] **Step 3: Write minimal implementation**

Add to `cmd/bridge/picker_remote.go`:

```go
// resolveDirenvDir returns the real (symlink-resolved) path of dir. direnv exec
// hashes the literal path argument while direnv allow records the canonical
// path, so passing the resolved path keeps the two in agreement. dir must
// already exist.
func resolveDirenvDir(dir string) (string, error) {
	return filepath.EvalSymlinks(dir)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./cmd/bridge -run TestResolveDirenvDir`
Expected: PASS

- [ ] **Step 5: Wire it into `cloneRemoteRepo`**

In `cmd/bridge/picker_remote.go`, the block currently reads (around lines 291–308):

```go
	if err := os.MkdirAll(parentDir, 0o755); err != nil {
		return "", fmt.Errorf("clone: mkdir parent: %w", err)
	}
	url := cloneURLFor(ref)
```

Replace with:

```go
	if err := os.MkdirAll(parentDir, 0o755); err != nil {
		return "", fmt.Errorf("clone: mkdir parent: %w", err)
	}
	// direnv exec does not resolve symlinks but direnv allow does; pass the
	// real path so the rc's allow state is found (e.g. reposRoot reached via a
	// symlink). parentDir exists now (MkdirAll above).
	execDir, err := resolveDirenvDir(parentDir)
	if err != nil {
		return "", fmt.Errorf("clone: resolve parent dir: %w", err)
	}
	url := cloneURLFor(ref)
```

Then change the `direnv exec` line (currently line 308) from:

```go
	cmd := exec.Command("direnv", append([]string{"exec", parentDir, "git"}, gitArgs...)...)
```

to:

```go
	cmd := exec.Command("direnv", append([]string{"exec", execDir, "git"}, gitArgs...)...)
```

Leave `parentDir`, `targetDir`, and the returned path unchanged.

- [ ] **Step 6: Run package build + tests**

Run: `go build ./cmd/bridge && go test ./cmd/bridge`
Expected: builds; tests PASS

- [ ] **Step 7: Commit**

```bash
git add cmd/bridge/picker_remote.go cmd/bridge/picker_remote_test.go
git commit -m "fix(clone): canonicalize direnv exec path for symlinked reposRoot"
```

---

### Task 3: Auto-allow a blocked token `.envrc` (Part 2)

**Files:**
- Modify: `cmd/bridge/picker_remote.go` (add `direnvBlocked` wrapper; wire probe+allow into `cloneRemoteRepo`)

No new unit test: `direnvBlocked` and the allow call shell out to `direnv`, and this file does not unit-test its shell-outs (no exec seam). The pure logic is already covered by `TestIsDirenvBlocked`.

- [ ] **Step 1: Add the `direnvBlocked` wrapper**

Add to `cmd/bridge/picker_remote.go` (below `isDirenvBlocked`):

```go
// direnvBlocked probes whether direnv considers execDir's rc file blocked by
// running a no-op through `direnv exec` and inspecting its stderr.
func direnvBlocked(execDir string) bool {
	cmd := exec.Command("direnv", "exec", execDir, "true")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	_ = cmd.Run() // exit code is 0 even when blocked; stderr is the signal
	return isDirenvBlocked(stderr.String())
}
```

(`bytes` is already imported in this file.)

- [ ] **Step 2: Wire probe + allow into `cloneRemoteRepo`**

In `cloneRemoteRepo`, immediately after the `execDir, err := resolveDirenvDir(...)` block added in Task 2 and before `url := cloneURLFor(ref)`, insert:

```go
	// If the token .envrc is still blocked (e.g. first clone on this machine),
	// approve it once so direnv exec can inject the forge token. execDir is
	// always a bridge-managed reposRoot subtree (the user's own .envrc), never
	// cloned repo content.
	if direnvBlocked(execDir) {
		if out, aerr := exec.Command("direnv", "allow", execDir).CombinedOutput(); aerr != nil {
			return "", fmt.Errorf("clone: direnv allow %s: %v: %s", execDir, aerr, strings.TrimSpace(string(out)))
		}
		fmt.Fprintf(os.Stderr, "bridge: approved direnv .envrc at %s\n", execDir)
	}
```

- [ ] **Step 3: Build + run the package tests**

Run: `go build ./cmd/bridge && go test ./cmd/bridge`
Expected: builds; tests PASS

- [ ] **Step 4: Commit**

```bash
git add cmd/bridge/picker_remote.go
git commit -m "feat(clone): auto-approve blocked token .envrc before cloning"
```

---

### Task 4: Full verification

**Files:** none (gates only)

- [ ] **Step 1: Formatting**

Run: `gofmt -l .`
Expected: no output

- [ ] **Step 2: Vet**

Run: `go vet ./...`
Expected: no output / clean

- [ ] **Step 3: Lint**

Run: `golangci-lint run`
Expected: clean

- [ ] **Step 4: Full race suite**

Run: `go test -race ./...`
Expected: all PASS

- [ ] **Step 5: Build the binary**

Run: `just build`
Expected: builds + installs with version stamping

- [ ] **Step 6: Manual smoke (optional, needs a remote-only repo)**

Trigger a bridge remote clone through the symlinked reposRoot and confirm no
"is blocked" error appears and the repo clones with credentials.

---

## Notes for the executor

- Branch: `worktree-fix` (current). All work stays in `cmd/bridge/picker_remote.go` + its test.
- The repo commits only when the user asks; confirm before running the `git commit` steps if unsure.
- Do not introduce an exec seam/abstraction for direnv/git — match the file's existing untested-shell-out style.
- Do not touch `reposRoot()` or any display/MRU path.
