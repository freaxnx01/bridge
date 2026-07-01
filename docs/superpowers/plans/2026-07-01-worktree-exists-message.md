# Friendlier "Worktree Already Exists" Message — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** When the bridge nav "New worktree" modal is given a name whose `.worktrees/<name>` directory already exists but is not a registered worktree, show a friendly, actionable message instead of git's raw `fatal: '…' already exists`.

**Architecture:** `internal/worktree.Resolve` classifies the target-path-already-exists `git worktree add` failure and returns a typed `*WorktreeExistsError{Name, Path}`. The nav layer maps that typed error — via `errors.As` — to a friendly modal string; every other error keeps its current raw rendering. The existing "attach to a registered worktree" path (`Resolve` find-first) is unchanged and gets a lock-in test.

**Tech Stack:** Go (stdlib `testing`, table-driven, hand-rolled fakes), Cobra/Bubble Tea app. No new dependencies.

## Global Constraints

- Go stdlib only — no testify/mockery/gomock; hand-rolled fakes (`fakeRunner` already exists in `internal/worktree/worktree_test.go`).
- Typed errors implement `error` and carry fields; inspect with `errors.As` (no `err.Error()` string-matching in the nav layer).
- Lower layers return errors; user-facing wording lives in the nav (presentation) layer.
- No package-level mutable global state; no `panic`; no ignored errors.
- After changes: `gofmt -l .` empty, `go vet ./...`, `golangci-lint run`, and `go test -race ./...` all clean.
- Friendly message text, verbatim: `worktree "<name>" already exists — pick a different name, or remove .worktrees/<name>` (em dash `—`, straight double quotes around the name).

---

### Task 1: Classify the path-exists failure in `worktree.Resolve`

**Files:**
- Modify: `internal/worktree/worktree.go` (add `WorktreeExistsError` type + `targetExistsErr` helper; restructure the add-failure branch in `Resolve`, ~lines 75-86)
- Test: `internal/worktree/worktree_test.go`

**Interfaces:**
- Consumes: existing `Resolve(r Runner, repoPath, wt string) (dir string, created bool, err error)`, `branchExistsErr(err error) bool`, `fakeRunner`.
- Produces:
  - `type WorktreeExistsError struct { Name string; Path string }` with `func (e *WorktreeExistsError) Error() string`.
  - `Resolve` returns `&WorktreeExistsError{Name: wt, Path: <repo>/.worktrees/<wt>}` when `git worktree add` fails because the target directory already exists (message contains `already exists` and not `branch`), issuing exactly one `add` (no no-`-b` retry).

- [ ] **Step 1: Write the failing test for the typed error**

Add to `internal/worktree/worktree_test.go`:

```go
func TestResolveCreateReturnsWorktreeExistsError(t *testing.T) {
	// The target dir already exists but is not a registered worktree: git's
	// `add -b` fails with "'<path>' already exists" (no "branch"). Resolve must
	// return a typed *WorktreeExistsError and NOT retry without -b.
	r := &fakeRunner{listOut: porcelain, addErr: errors.New("fatal: '/repo/FlowHub-CAS-AISE/.worktrees/x' already exists")}
	_, _, err := Resolve(r, "/repo/FlowHub-CAS-AISE", "x")
	if err == nil {
		t.Fatalf("want error, got nil")
	}
	var wex *WorktreeExistsError
	if !errors.As(err, &wex) {
		t.Fatalf("want *WorktreeExistsError, got %T: %v", err, err)
	}
	if wex.Name != "x" {
		t.Errorf("Name = %q, want %q", wex.Name, "x")
	}
	if want := filepath.Join("/repo/FlowHub-CAS-AISE", ".worktrees", "x"); wex.Path != want {
		t.Errorf("Path = %q, want %q", wex.Path, want)
	}
	var addCalls int
	for _, c := range r.calls {
		if strings.Contains(strings.Join(c, " "), "worktree add") {
			addCalls++
		}
	}
	if addCalls != 1 {
		t.Errorf("want exactly 1 add attempt (no blind retry), got %d", addCalls)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/worktree/ -run TestResolveCreateReturnsWorktreeExistsError -v`
Expected: FAIL — `undefined: WorktreeExistsError` (compile error).

- [ ] **Step 3: Add the typed error and classifier**

In `internal/worktree/worktree.go`, add after the `branchExistsErr` function (~line 97):

```go
// WorktreeExistsError is returned by Resolve when the target directory already
// exists on disk but is not a registered worktree of the repo, so it can be
// neither created nor safely attached.
type WorktreeExistsError struct {
	Name string
	Path string
}

func (e *WorktreeExistsError) Error() string {
	return fmt.Sprintf("worktree %q already exists at %s", e.Name, e.Path)
}

// targetExistsErr reports whether a `git worktree add` failure was caused by the
// target directory already existing — git says "'<path>' already exists" without
// the "branch" qualifier that branchExistsErr looks for.
func targetExistsErr(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "already exists") && !strings.Contains(msg, "branch")
}
```

- [ ] **Step 4: Route the add failure through the classifier**

In `internal/worktree/worktree.go`, replace the add-failure block in `Resolve` (currently ~lines 75-86):

```go
	if _, aerr := r.Run(repoPath, "worktree", "add", "-b", branch, target); aerr != nil {
		// Only retry when the branch already exists (a dangling branch from a
		// removed worktree): check it out into the new worktree without -b.
		// Any other failure (target dir exists, no commits yet, bad name) is
		// returned as-is so its real cause isn't masked.
		if !branchExistsErr(aerr) {
			return "", false, fmt.Errorf("git worktree add: %w", aerr)
		}
		if _, aerr2 := r.Run(repoPath, "worktree", "add", target, branch); aerr2 != nil {
			return "", false, fmt.Errorf("git worktree add: %w", aerr2)
		}
	}
```

with:

```go
	if _, aerr := r.Run(repoPath, "worktree", "add", "-b", branch, target); aerr != nil {
		switch {
		case branchExistsErr(aerr):
			// A dangling branch from a removed worktree: check it out into the
			// new worktree without -b.
			if _, aerr2 := r.Run(repoPath, "worktree", "add", target, branch); aerr2 != nil {
				return "", false, fmt.Errorf("git worktree add: %w", aerr2)
			}
		case targetExistsErr(aerr):
			// The directory exists but is not a registered worktree — surface a
			// typed error so the UI can render a friendly message.
			return "", false, &WorktreeExistsError{Name: wt, Path: target}
		default:
			// Any other failure (no commits yet, bad name) is returned as-is so
			// its real cause isn't masked.
			return "", false, fmt.Errorf("git worktree add: %w", aerr)
		}
	}
```

- [ ] **Step 5: Run the new test to verify it passes**

Run: `go test ./internal/worktree/ -run TestResolveCreateReturnsWorktreeExistsError -v`
Expected: PASS.

- [ ] **Step 6: Fix the now-misnamed existing test**

`TestResolveCreateDoesNotRetryOnUnrelatedError` currently uses a path-exists message as its "unrelated" example; that message is now *classified*. Change its `addErr` to a genuinely unrelated failure so it still guards the raw-fall-through path. In `internal/worktree/worktree_test.go`, edit that test's runner and assertion:

```go
	r := &fakeRunner{listOut: porcelain, addErr: errors.New("fatal: this operation must be run in a work tree")}
	_, _, err := Resolve(r, "/repo/FlowHub-CAS-AISE", "x")
	if err == nil {
		t.Fatalf("want error, got nil")
	}
	if !strings.Contains(err.Error(), "must be run in a work tree") {
		t.Errorf("error should surface git's original message, got: %v", err)
	}
	var wex *WorktreeExistsError
	if errors.As(err, &wex) {
		t.Errorf("unrelated failure must not be classified as WorktreeExistsError")
	}
```

(Keep the trailing `addCalls != 1` assertion block unchanged.)

- [ ] **Step 7: Run the full worktree package suite**

Run: `go test ./internal/worktree/ -v`
Expected: PASS (including `TestResolveCreateFallsBackToExistingBranch`, `TestResolveFindsExistingByPathBasename`, both edited/new tests).

- [ ] **Step 8: Commit**

```bash
git add internal/worktree/worktree.go internal/worktree/worktree_test.go
git commit -m "feat(worktree): classify target-already-exists as typed error (#173)"
```

---

### Task 2: Render the friendly message in the nav modal

**Files:**
- Modify: `internal/nav/update.go` (import block; `wtCreatedMsg` handler at ~lines 100-110; add `worktreeErrMessage` helper)
- Test: `internal/nav/update_test.go`

**Interfaces:**
- Consumes: `worktree.WorktreeExistsError` (Task 1); existing `wtCreatedMsg{err error; row dashRow}`, `Model.modal *newWorktreeModal` with field `err string`.
- Produces: `func worktreeErrMessage(err error) string` in package `nav`, used by the `wtCreatedMsg` handler.

- [ ] **Step 1: Write the failing tests**

Add to `internal/nav/update_test.go`:

```go
func TestUpdate_WtCreatedMsg_ExistsError_FriendlyMessage(t *testing.T) {
	m := initialModel(Config{})
	m.modal = &newWorktreeModal{name: "misc"}
	out, _ := m.Update(wtCreatedMsg{err: &worktree.WorktreeExistsError{
		Name: "misc",
		Path: "/repo/.worktrees/misc",
	}})
	got := out.(Model)
	want := `worktree "misc" already exists — pick a different name, or remove .worktrees/misc`
	if got.modal == nil {
		t.Fatalf("modal should stay open on error")
	}
	if got.modal.err != want {
		t.Errorf("modal.err = %q, want %q", got.modal.err, want)
	}
}

func TestUpdate_WtCreatedMsg_OtherError_RawMessage(t *testing.T) {
	m := initialModel(Config{})
	m.modal = &newWorktreeModal{name: "misc"}
	out, _ := m.Update(wtCreatedMsg{err: errors.New("git worktree add: boom")})
	got := out.(Model)
	if got.modal == nil || got.modal.err != "git worktree add: boom" {
		t.Errorf("modal.err = %v, want raw error string", got.modal)
	}
}
```

Add the imports this test needs to `internal/nav/update_test.go` — `"errors"` and `"github.com/freaxnx01/bridge/internal/worktree"` — to the existing import block (Task 1's type lives in that package).

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/nav/ -run 'TestUpdate_WtCreatedMsg' -v`
Expected: FAIL — `undefined: worktreeErrMessage`-driven mismatch (the exists case renders the typed error's raw `Error()` string, not the friendly message).

- [ ] **Step 3: Add the mapping helper**

In `internal/nav/update.go`, add the `worktree` import to the import block:

```go
	"github.com/freaxnx01/bridge/internal/worktree"
```

and add `"errors"` to the same block. Then add the helper (near the `wtCreatedMsg` handling, package-level):

```go
// worktreeErrMessage maps a worktree.Resolve error to the modal's error line: a
// friendly, actionable message for the "target dir already exists" case, and the
// raw error string for everything else.
func worktreeErrMessage(err error) string {
	var wex *worktree.WorktreeExistsError
	if errors.As(err, &wex) {
		return fmt.Sprintf("worktree %q already exists — pick a different name, or remove .worktrees/%s", wex.Name, wex.Name)
	}
	return err.Error()
}
```

- [ ] **Step 4: Use the helper in the `wtCreatedMsg` handler**

In `internal/nav/update.go`, change the modal-error assignment (currently `m.modal.err = msg.err.Error()` at ~line 103):

```go
	case wtCreatedMsg:
		if msg.err != nil {
			if m.modal != nil {
				m.modal.err = worktreeErrMessage(msg.err)
			} else {
				m.status = "worktree create failed: " + msg.err.Error()
			}
			return m, nil
		}
		m.modal = nil
		return m.launchRow(msg.row)
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/nav/ -run 'TestUpdate_WtCreatedMsg' -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/nav/update.go internal/nav/update_test.go
git commit -m "feat(nav): friendly modal message when worktree dir already exists (#173)"
```

---

### Task 3: Full verification

**Files:** none (verification only).

- [ ] **Step 1: Format check**

Run: `gofmt -l .`
Expected: no output.

- [ ] **Step 2: Vet**

Run: `go vet ./...`
Expected: no output / exit 0.

- [ ] **Step 3: Lint**

Run: `golangci-lint run`
Expected: no findings.

- [ ] **Step 4: Full race suite**

Run: `go test -race ./...`
Expected: all packages PASS.

- [ ] **Step 5: Commit (only if lint/format produced fixups)**

```bash
git add -A
git commit -m "chore(worktree): lint/format fixups (#173)"
```

Skip this step if the working tree is clean after Tasks 1-2.

---

## Self-Review

**Spec coverage:**
- Detection (typed error + classifier) → Task 1. ✔
- Presentation (nav `errors.As` → friendly message, modal stays open) → Task 2. ✔
- Data flow (Resolve → wtCreatedMsg → nav) → Tasks 1-2. ✔
- Edge cases (branch-exists retry untouched; unrelated errors raw) → Task 1 Steps 4 & 6. ✔
- Testing (worktree classification + no-retry, unrelated-not-classified, nav friendly + raw) → Tasks 1-2. ✔
- Attach lock-in: covered by the **existing** `TestResolveFindsExistingByPathBasename` (registered worktree → `created=false`, no `add`); no new test needed — noted here so it isn't mistaken for a gap.
- Verification gates → Task 3. ✔

**Placeholder scan:** none — every code/test step shows full content.

**Type consistency:** `WorktreeExistsError{Name, Path}` and `worktreeErrMessage` names/fields are identical across Tasks 1-2; friendly string matches the spec's verbatim wording and the AC.
