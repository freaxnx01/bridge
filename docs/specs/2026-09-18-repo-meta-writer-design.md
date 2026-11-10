# repo-meta.json: restore the writer

**Issue:** [#297](https://github.com/freaxnx01/bridge/issues/297)
**Date:** 2026-09-18
**Status:** approved

## Problem

`README.md:75` documents `repo-meta.json` as being written by `list -r [--refresh]`:

| `repo-meta.json` | `list -r [--refresh]` | per-repo topics/description/default-branch/remote URL |

**No Go code writes it.** `internal/core/repo_meta.go` only reads it (`LoadRepoMeta`)
and merges it onto discovered repos (`MergeRepoMeta`); `grep -rn "repo-meta"
--include=*.go` finds no writer. `remote.Refresh` writes `remote.list` and nothing
else. The file on disk is left over from the bash implementation — every entry in
`~/.cache/bridge/repo-meta.json` carries `"fetched_at": 1788293514` (~2026-05) and
an empty description.

The cache is not decorative. Two features read it through `MergeRepoMeta`:

- **`bridge open <keyword>`** resolves a repo by description or topic when the
  basename doesn't match (`cmd/bridge/open.go:122-126`).
- **`bridge __complete-meta <prefix>`** does the same for tab completion
  (`cmd/bridge/completion.go:86-88`) — the mechanism `README.md:202` describes for
  `bridge nextgen<TAB>` → `ArchiveRestApiNextGen`.

Both silently degrade as the cache ages, and no command can refresh it. A repo
created or re-described after the bash era is invisible to keyword search forever.

## Goals

- Every path that refreshes the remote repo list also refreshes `repo-meta.json`.
- The file keeps its existing on-disk shape, so the stale cache upgrades in place
  rather than being invalidated.
- `README.md:75` becomes true.

## Non-goals

- Changing how `MergeRepoMeta`, `bridge open` or `__complete-meta` *consume* the
  metadata. Consumers are untouched.
- Removing `loadOrFetchRemote`'s unused `local` parameter (see Notes).
- Changing `bridge doctor`'s cache check from a warning to an age report (see Notes).
- Adding a standalone `bridge meta refresh` command. Refresh is a side effect of the
  existing remote refresh, not a new user-facing verb.

## Design

### 1. The writer lives in `remote.Refresh`

There are **three** entry points that refresh the remote list, not one:

- `bridge list -r --refresh` → `loadOrFetchRemote` (`cmd/bridge/list.go:90`)
- `bridge --refresh` (the picker) → `loadOrFetchRemote` (`cmd/bridge/preflight.go:103`)
- `bridge nav`, `r` / `ctrl+r` → `remote.Refresh` directly (`cmd/bridge/nav.go:66`)

All three funnel into `remote.Refresh(ctx, roots, cachePath)`, which already holds
the repos roots and writes `remote.list`. Writing `repo-meta.json` there covers
every path with one change. Putting it in `list.go` instead would leave nav and the
picker refreshing `remote.list` while `repo-meta.json` kept ageing — the same class
of bug as today's, just narrower.

`Refresh` gains the meta cache path as a parameter rather than deriving it, keeping
path policy in `cmd/bridge` where `cacheRoot()` lives:

```go
func Refresh(ctx context.Context, roots []string, cachePath, metaPath string) ([]forge.RepoRef, error)
```

### 2. Mapping refs to local clones

`Refresh` discovers the local repos from the roots it already walks
(`core.DiscoverRepos`), then matches each against the fetched refs on
**case-insensitive forge + owner + name** — the same identity the rest of the
codebase uses to pair a clone with its remote. The entry is keyed by the repo's
path relative to its root, which is exactly what `MergeRepoMeta` looks up.

`core` keeps ownership of that key format, so the read and write sides cannot drift:

```go
// RepoMetaKey returns the repo-meta.json key for a repo path: its shortest
// non-escaping path relative to any of roots.
func RepoMetaKey(roots []string, repoPath string) string

// SaveRepoMeta atomically writes the metadata cache.
func SaveRepoMeta(path string, meta map[string]RepoMeta) error
```

`RepoMetaKey` is a thin exported wrapper over the existing unexported
`bestRelUnder` (`repo_meta.go:79`) — the function `MergeRepoMeta` already uses to
resolve the same key on read. `SaveRepoMeta` follows the `WriteRepoCache` idiom
(`internal/forge/cache.go:60`): `json.MarshalIndent` + `store.AtomicWrite`. No
flock: unlike `slots.json` this file has a single writer per refresh and is
rewritten wholesale, so the atomic rename is sufficient.

`core` gains no import of `internal/forge`. The `remote` package builds the map,
since it already depends on both.

`RepoMeta` gains the field the on-disk file already carries and the struct silently
drops today:

```go
FetchedAt int64 `json:"fetched_at,omitempty"` // Unix seconds of the last refresh
```

### 3. Merge, don't replace

`Refresh` reads the existing cache, updates the entries it has fresh refs for, and
writes the union back. A local repo whose forge failed this round — a 401, a
timeout, a token not in `direnv` — keeps its previous entry instead of losing it.
`Refresh` already returns partial results alongside the first error for exactly
this reason (`remote.go:44-52`); the cache write follows the same principle.

Entries for repos that no longer exist locally are dropped, so the file cannot grow
forever as clones come and go.

### 4. Best-effort write

The write sits beside the existing `remote.list` write and is ignored the same way:

```go
_ = forge.WriteRepoCache(cachePath, forge.RepoCache{UpdatedAt: time.Now(), Repos: all})
_ = writeRepoMeta(metaPath, roots, all) // best-effort, same as above
```

The caller already holds the fresh repos; a cache-write failure must not turn a
successful refresh into an error. This mirrors the comment already on the
`remote.list` write.

## Testing

Per the Go stack overlay: table-driven, `t.Run` subtests, hand-rolled fakes, green
under `-race`.

**`internal/core`** —

- `RepoMetaKey` returns the same key `MergeRepoMeta` resolves for the same path,
  across single and multiple roots, including a path under no root.
- `SaveRepoMeta` → `LoadRepoMeta` round-trips through `t.TempDir()`, preserving
  `FetchedAt`.
- A written file merged by `MergeRepoMeta` populates `Desc`, `Topics`,
  `DefaultBranch` and `RemoteURL` on a matching repo.

**`internal/remote`** —

- The map builder pairs a local repo with its ref case-insensitively, skips a repo
  with no matching ref, and keys entries by the root-relative path.
- A repo present in the existing cache but absent from this round's refs keeps its
  entry; a repo no longer on disk loses its entry.
- `Refresh` against an `httptest` forge writes **both** `remote.list` and
  `repo-meta.json` into `t.TempDir()`.
- An unwritable meta path does not make `Refresh` return an error.

## Acceptance criteria

- [ ] `remote.Refresh` writes `repo-meta.json` in addition to `remote.list`, on all
      three refresh paths (`list -r --refresh`, `bridge --refresh`, nav `r`/`ctrl+r`).
- [ ] Entries are keyed by the repo's root-relative path, identical to the key
      `MergeRepoMeta` reads.
- [ ] Refs are matched to local clones case-insensitively on forge+owner+name.
- [ ] `RepoMeta` carries `fetched_at`, and it round-trips through load/save.
- [ ] A repo whose forge failed this round keeps its previous entry; a repo no
      longer on disk is dropped.
- [ ] A failing meta-cache write does not fail the refresh.
- [ ] `core` does not import `internal/forge`.
- [ ] `gofmt -l .` empty, `go vet ./...` clean, `golangci-lint run` clean,
      `go test -race ./...` green.

## Notes (out of scope)

- `loadOrFetchRemote(ctx, local, refresh)` ignores its `local` parameter today
  (`cmd/bridge/list.go:90`) — plausibly the vestige of this very writer. Once the
  writer lives in `Refresh` it is provably dead and should be removed, in its own
  change.
- `bridge doctor` warns that the cache merely exists (`cmd/bridge/doctor.go:173`).
  With `fetched_at` written it could report the cache's age instead, which is the
  more useful signal. Separate issue.
