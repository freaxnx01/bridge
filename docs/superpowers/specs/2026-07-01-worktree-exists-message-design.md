# Friendlier Message When a Worktree Already Exists — Design

- **Issue:** #173
- **Date:** 2026-07-01
- **Status:** Approved for implementation

## Goal

When creating a worktree from the bridge nav "New worktree" modal, and the typed
name maps to a `.worktrees/<name>` directory that already exists on disk but is
**not** a registered worktree of the repo, bridge currently leaks git's raw
error into the red modal line:

```
git worktree add: fatal: '/…/.worktrees/misc' already exists
```

Replace that with a clear, actionable message. Attaching to a name that already
resolves to a *registered* worktree already works and must keep working.

## Background — what already happens

`worktree.Resolve(r, repoPath, wt)` (`internal/worktree/worktree.go`) runs
`git worktree list --porcelain` first and, via `matches()`, returns an existing
worktree's path with `created=false` when the name matches by path basename,
exact branch, or `worktree-<name>` branch. So typing the name of a **registered**
worktree already attaches to it — no error.

The failure in the issue is the **orphaned-directory** case: `.worktrees/misc`
exists on disk but is absent from `git worktree list` (not registered). `matches`
does not find it, so `Resolve` proceeds to `git worktree add -b worktree-misc
.worktrees/misc`, which fails with `fatal: '…/.worktrees/misc' already exists`.
That failure is not a branch-exists error, so `Resolve` returns the wrapped raw
error, and nav renders it verbatim (`internal/nav/update.go` → `internal/nav/view.go`).

## Approach

**Typed error from `Resolve`, formatted in the nav layer.** `Resolve` classifies
the target-path-already-exists failure and returns a typed
`*WorktreeExistsError{Name, Path}`. The nav layer maps it — via `errors.As` — to a
friendly modal message. This keeps git-output string-matching in the worktree
package (next to the existing `branchExistsErr`) and keeps user-facing wording in
the presentation layer, matching the repo convention that lower layers return
errors and the command/UI maps them to messages.

Rejected alternatives:

- **String-match in nav** — puts git-phrasing knowledge in the UI layer, far from
  where the command runs.
- **Auto-repair / adopt the orphaned dir** (`git worktree repair` then attach) —
  out of scope (YAGNI + risk): an arbitrary leftover `.worktrees/<name>` is not
  necessarily a repairable worktree, and silently adopting it could attach
  unrelated content. A clear message that lets the user decide is safer.

---

## 1. Detection — `internal/worktree/worktree.go`

Add a typed error and reuse the existing add-failure branch in `Resolve`.

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
```

In `Resolve`, the current add-failure block:

```go
if _, aerr := r.Run(repoPath, "worktree", "add", "-b", branch, target); aerr != nil {
	if !branchExistsErr(aerr) {
		return "", false, fmt.Errorf("git worktree add: %w", aerr)
	}
	if _, aerr2 := r.Run(repoPath, "worktree", "add", target, branch); aerr2 != nil {
		return "", false, fmt.Errorf("git worktree add: %w", aerr2)
	}
}
```

becomes (branch-exists retry unchanged; new path-exists classification before the
raw fall-through):

```go
if _, aerr := r.Run(repoPath, "worktree", "add", "-b", branch, target); aerr != nil {
	switch {
	case branchExistsErr(aerr):
		if _, aerr2 := r.Run(repoPath, "worktree", "add", target, branch); aerr2 != nil {
			return "", false, fmt.Errorf("git worktree add: %w", aerr2)
		}
	case targetExistsErr(aerr):
		return "", false, &WorktreeExistsError{Name: wt, Path: target}
	default:
		return "", false, fmt.Errorf("git worktree add: %w", aerr)
	}
}
```

New classifier beside `branchExistsErr`:

```go
// targetExistsErr reports whether a `git worktree add` failure was caused by the
// target directory already existing — git says "'<path>' already exists" without
// the "branch" qualifier that branchExistsErr looks for.
func targetExistsErr(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "already exists") && !strings.Contains(msg, "branch")
}
```

`branchExistsErr` is checked first, so an `already exists` message that also
mentions `branch` still routes to the retry, never to `targetExistsErr`.

## 2. Presentation — `internal/nav/update.go`

In the `wtCreatedMsg` handler, classify the error before assigning the raw string:

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

with a small helper (nav package) that keeps the default behavior for every other
error:

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

The modal stays open (no state-machine change) so the user can type a different
name. `view.go` renders `m.modal.err` in the red `stBad` style, unchanged.

## 3. Data flow

`createWorktreeCmd` → `worktree.Resolve` returns `*WorktreeExistsError` →
`wtCreatedMsg{err}` → nav `worktreeErrMessage` (`errors.As`) → friendly
`m.modal.err` → red render. The registered-worktree case is unchanged: `Resolve`
returns the existing path with `created=false`, the modal closes, and `launchRow`
attaches.

## 4. Error handling / edge cases

- Branch-exists retry (dangling `worktree-<name>` branch, dir absent) is untouched.
- Unrelated `git worktree add` failures (invalid name, empty repo, no commits)
  still surface their real cause verbatim — only the path-exists phrasing is
  special-cased, and only when `branch` is absent from the message.
- No behavior change outside the New-worktree modal.

## 5. Testing

- **`internal/worktree/worktree_test.go`**
  - New case: `add -b` fails with `fatal: '<path>' already exists` → `Resolve`
    returns a `*WorktreeExistsError` (asserted via `errors.As`) with `Name` = the
    requested name and `Path` = `<repo>/.worktrees/<name>`, and does **not** issue
    a second `add` (no-`-b` retry must not fire).
  - Guard case: an unrelated failure (e.g. `fatal: not a valid object name`) is
    still returned wrapped/raw and is **not** classified as `WorktreeExistsError`.
  - Attach lock-in: a fake `worktree list` containing a matching entry makes
    `Resolve` return that path with `created=false` and never call `add`
    (extends/complements the existing find tests).
- **`internal/nav/update_test.go`**
  - New test: feeding `wtCreatedMsg{err: &worktree.WorktreeExistsError{Name: "misc",
    Path: "…/.worktrees/misc"}}` with an open modal sets `m.modal.err` to the
    friendly message (`worktree "misc" already exists — pick a different name, or
    remove .worktrees/misc`). This `wtCreatedMsg{err}` → `modal.err` path is
    currently untested.
  - New test: a plain non-typed error still sets `m.modal.err` to `err.Error()`.

## Acceptance criteria

- [ ] Creating a worktree whose name matches an existing **registered** worktree
      attaches to it (no error) — covered by a `Resolve` test.
- [ ] Creating a worktree whose target dir exists but is **not** a registered
      worktree shows the friendly message `worktree "<name>" already exists — pick
      a different name, or remove .worktrees/<name>`, not the raw
      `git worktree add: fatal: …`.
- [ ] The modal stays open so the user can enter a different name.
- [ ] Branch-exists retry and all other `git worktree add` failures behave exactly
      as before (real cause not masked).
- [ ] `gofmt -l .` empty, `go vet ./...`, `golangci-lint run`, and
      `go test -race ./...` all clean.
