# bridge direnv symlink clone fix + auto-allow

Date: 2026-06-15
Status: Approved (design)
Scope: `cmd/bridge/picker_remote.go` (`cloneRemoteRepo`) + tests

## Problem

Cloning a remote-only repo through bridge fails with:

```
direnv: error /home/freax/repos/github/freaxnx01/public/.envrc is blocked.
Run `direnv allow` to approve its content
```

`cloneRemoteRepo` shells out to `direnv exec <parentDir> git clone …` so direnv
injects the forge token (`GH_TOKEN`) from the nearest `.envrc`. The reposRoot is
reached via the symlink `/home/freax/repos → /home/freax/projects/repos`.

Root cause (verified empirically with direnv 2.32.1):

- `direnv exec <path>` hashes the **literal path argument** — it does **not**
  resolve symlinks.
- `direnv allow <path>` **canonicalizes** to the real path before recording the
  allow hash.

So the symlink-path `.envrc` can never appear in the allow database: every
`direnv allow` rewrites the entry to the real path, while bridge keeps asking
`direnv exec` about the symlink path. Result: a permanent "blocked" for bridge,
even though the file is genuinely allowed at its real path (which is why `cd`
into the dir works fine interactively).

This is a standing bug, not a one-time fresh-clone gate.

## Design

Two changes, both confined to `cloneRemoteRepo` and new pure helpers.

### Part 1 — Canonicalize the direnv exec path (the fix)

After `os.MkdirAll(parentDir, …)` (so the path exists), resolve the directory
passed to direnv with `filepath.EvalSymlinks`:

- Compute `execDir` = real path of `parentDir`.
- Use `execDir` (not `parentDir`) as the argument to every `direnv exec …`
  invocation.
- On `EvalSymlinks` error, return a wrapped `clone: resolve parent dir: %w`.

`parentDir`, `targetDir`, and the returned clone path are left unchanged — only
the argument handed to direnv is canonicalized. This keeps the repo's displayed
path / MRU entry on the user-facing symlink path and stays surgical.

Because the real path is already allowed, this alone unblocks current clones.

### Part 2 — Auto-allow a blocked token `.envrc`

Before the clone, if direnv reports the token `.envrc` as blocked for `execDir`,
bridge approves it and notes it once:

1. Probe: run `direnv exec <execDir> true`, capture stderr.
2. If stderr indicates a block, run `direnv allow <execDir>`.
   - On success, print to stderr:
     `bridge: approved direnv .envrc at <execDir>`
   - On failure, return `clone: direnv allow <execDir>: <err>: <output>`.
3. Proceed with the real `direnv exec <execDir> git clone …`.

With Part 1 in place the allow targets the same real path direnv exec resolves,
so it actually takes effect. On a fresh machine the clone self-heals:
canonicalize → allow if blocked → exec with token injected.

Security: `execDir` is always a bridge-managed reposRoot subtree (the user's own
token `.envrc`), never the cloned repo's content — cloned repos in this tree
carry no `.envrc`. So auto-trust introduces no remote-code-execution risk.

## Components

- `isDirenvBlocked(stderr string) bool` — pure predicate; reports whether
  direnv's stderr names a blocked rc (matches `"is blocked"`). Table-tested.
- `direnvBlocked(execDir string) bool` — runs the probe and delegates to
  `isDirenvBlocked`. Exit code is unreliable for a blocked-but-ran command, so
  detection is by stderr content. Thin wrapper, not unit-tested (no exec seam),
  consistent with existing untested shell-outs in this file.
- `cloneRemoteRepo` orchestration: EvalSymlinks → optional probe+allow → exec.

## Error handling

- `EvalSymlinks` failure → wrapped error, clone aborts (existing
  half-clone cleanup unaffected; nothing created yet at that point).
- `direnv allow` failure → wrapped error including combined output, clone
  aborts.
- No `.envrc` found up-tree → probe reports not-blocked → no allow, no note →
  clone proceeds tokenless (preserves current behavior for public repos).

## Testing

Follows the file's existing pattern (pure helpers table-tested; shell-out
orchestration not unit-tested — no exec seam introduced):

- `TestIsDirenvBlocked_*` — table-driven: blocked stderr → true; empty / normal
  loading message → false.
- A focused test that a symlinked directory resolves to its real path via the
  canonicalization step (t.TempDir + os.Symlink), guarding the Part 1 intent.

Gates after change: `gofmt -l .` empty, `go vet ./...`, `golangci-lint run`,
`go test -race ./...` all green.

## Out of scope

- No change to `reposRoot()` or any other path used for display/MRU.
- No direnv config (`whitelist.prefix`) changes.
- No new exec abstraction/seam for the direnv/git calls.
