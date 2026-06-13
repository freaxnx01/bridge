# Nav picker `r`: re-fetch remotes from the forge

**Date:** 2026-06-11
**Area:** `internal/nav` (picker `r` key) · new `internal/remote` package · `cmd/bridge` (`list`, `nav` wiring)
**Status:** Approved (design)

## Problem

In `bridge nav`, the picker screen merges local repos with remote repos (forge
repos the user owns), the latter marked with a `↓` prefix. Remote rows come from
a disk cache (`<cacheRoot>/remote.list`). The picker's `r` key — intended as
"refresh" — only **re-reads that cache file** (`loadRemoteCmd`); it never
re-queries the forge.

The actual forge query lives in `bridge list --refresh`
(`cmd/bridge/list.go` → `loadOrFetchRemote`), which discovers per-owner token
scopes via `.envrc`/direnv and calls each forge's `ListRepos`. So a
newly-created GitHub repo does **not** appear in nav until the user drops to a
shell and runs `bridge list -r --refresh` (or the 1h cache TTL lapses and some
other code path refreshes it). A user who presses `r` expecting their new repo
to show up sees nothing change — the trap this design removes.

## Goal (success criterion)

Pressing `r` on the picker re-queries the forge, updates the live list **and**
the on-disk cache, and surfaces newly-created remote repos — without leaving
nav. Concretely: create a repo on GitHub → open `bridge nav` → press `r` → the
repo appears in the picker with the `↓` prefix.

## Decisions (from brainstorming)

1. **`r` re-fetches from the forge** (not just re-reads cache). This is the real
   fix; documenting the existing cache-only `r` would re-set the same trap.
2. **Extract the fetch orchestration into `internal/remote`.** It currently
   lives in `cmd/bridge/list.go`; the Go overlay wants logic in `internal/`, and
   two callers (the `list` command and nav) now need it. One package, one
   responsibility: forge-target discovery → token resolution → fetch → cache.
3. **nav reaches it through a `Config.FetchRemote` callback**, mirroring the
   existing `Config.Clone` and `Config.FetchIssues` DI callbacks. `internal/nav`
   stays decoupled from the direnv/forge machinery and stays testable with a
   fake callback — no filesystem or network in nav tests.
4. **Picker only — no dashboard refresh key.** The dashboard already auto-runs
   `git fetch` + dirty-status on entry and worktree navigation; a manual key
   there is redundant.
5. **`bridge list` behavior and its 1h TTL are unchanged.** The CLI keeps its
   cache-staleness gate and merely delegates the fetch to `internal/remote`.

## Components

### 1. New package `internal/remote`

Move verbatim from `cmd/bridge/list.go` (no behavior change):

- `remoteTarget` struct
- `discoverRemoteTargets(root string) []remoteTarget` — walks the well-known
  `github/<owner>/[<vis>/]`, `gitlab/<owner>/`, `git-forgejo/`, `ado/` layout
  for `.envrc` markers.
- `envFromDirenv(dir string, vars []string) map[string]string` — resolves tokens
  under a target's direnv scope, falling back to process env.
- `fetchTargetRepos(ctx, t remoteTarget) ([]forge.RepoRef, error)` — per-forge
  token + `ListRepos`.
- `ownerEnvrcDir(ownerDir string) string` — used only by the moved code, moves
  entirely.
- `dirExists` / `fileExists` — **stay in `cmd/bridge`** (`bases.go:65` still uses
  `dirExists`); add small private copies in `internal/remote`. These are trivial
  3-line `os.Stat` wrappers, so duplicating beats exporting a shared util
  package.

New exported entry point:

```go
// Refresh discovers every forge target reachable from roots, fetches each
// owner's repos, writes the merged result to cachePath, and returns it. The
// first per-target error is returned alongside whatever repos did succeed, so a
// single failing forge does not lose the others.
func Refresh(ctx context.Context, roots []string, cachePath string) ([]forge.RepoRef, error)
```

`Refresh` is today's `loadOrFetchRemote` body **without** the
read-cache/TTL/`refresh` gate — it always fetches and always writes the cache.

### 2. `cmd/bridge/list.go` delegates

`loadOrFetchRemote` keeps the TTL gate and calls `remote.Refresh` on
miss/stale/`--refresh`:

```go
func loadOrFetchRemote(ctx context.Context, local []core.Repo, refresh bool) ([]forge.RepoRef, error) {
    cachePath := filepath.Join(cacheRoot(), "remote.list")
    if !refresh {
        if c, err := forge.ReadRepoCache(cachePath); err == nil && !c.IsStale(remoteTTL) && len(c.Repos) > 0 {
            return c.Repos, nil
        }
    }
    return remote.Refresh(ctx, reposRoots(), cachePath)
}
```

The moved functions are deleted from `list.go`. `remoteTTL` stays here (it's a
CLI-cache policy, not a fetch concern).

### 3. `Config.FetchRemote` callback

In `internal/nav/types.go`, add alongside `Clone` / `FetchIssues`:

```go
// FetchRemote re-queries every configured forge and returns the owned repos,
// also refreshing the on-disk cache. Nil disables live refresh: the r key
// falls back to re-reading the cache.
FetchRemote func(ctx context.Context) ([]forge.RepoRef, error)
```

Wired in `cmd/bridge/nav.go`:

```go
FetchRemote: func(ctx context.Context) ([]forge.RepoRef, error) {
    return remote.Refresh(ctx, reposRoots(), filepath.Join(cacheRoot(), "remote.list"))
},
```

### 4. nav `r` handler → `refreshRemoteCmd`

Replace the `case "r"` body (`update.go:307`):

```go
case "r":
    m.remoteState = loadPending
    return m, m.refreshRemoteCmd()
```

New command in `internal/nav/data.go`:

```go
func (m Model) refreshRemoteCmd() tea.Cmd {
    if m.cfg.FetchRemote == nil {
        return loadRemoteCmd(m.cfg.RemoteCache) // no live fetch: re-read cache
    }
    fetch := m.cfg.FetchRemote
    return func() tea.Msg {
        refs, err := fetch(context.Background())
        if err != nil {
            return remoteErrMsg{err: err}
        }
        rows := make([]repoRow, 0, len(refs))
        for i := range refs {
            ref := refs[i]
            rows = append(rows, repoRow{label: "↓ " + remoteLabel(ref), remote: &ref})
        }
        sortRepoRows(rows)
        return remoteMsg{rows: rows}
    }
}
```

Row construction mirrors `loadRemoteCmd` exactly (`↓ ` prefix, `remoteLabel`,
`sortRepoRows`). Cache writing happens inside `remote.Refresh`, so the next nav
launch is also fresh.

### 5. Help text

`view.go:138` picker hint line gains `· r refresh`:

```
↑↓ move · g/G first/last · ⏎ open/attach · / filter · r refresh · tab panes · q quit
```

## Data flow

```
press r (picker)
  └─ remoteState = loadPending          (title shows "◐ loading remote…", existing)
  └─ refreshRemoteCmd
       ├─ FetchRemote == nil → loadRemoteCmd (re-read cache)        [fallback]
       └─ FetchRemote(ctx) → remote.Refresh
            ├─ discoverRemoteTargets(root) for each root
            ├─ envFromDirenv → token per target
            ├─ forge ListRepos per target
            └─ WriteRepoCache(remote.list)
       ├─ success → remoteMsg{rows}  → remoteRepos set, remoteState = loadOK
       └─ error   → remoteErrMsg     → remoteState = loadErr
```

`remoteMsg` / `remoteErrMsg` are already handled in `update.go:29-36` (set rows
+ `loadOK`, or `loadErr` + "remote unavailable (cached rows shown)" status), and
the picker title already renders the spinner for `loadPending` and the warning
for `loadErr`. No view changes beyond the hint line.

## Error handling

- **Fetch fails:** `remoteErrMsg` → `loadErr` + "remote unavailable (cached rows
  shown)"; the last-good `remoteRepos` stay on screen.
- **Partial forge failure:** `remote.Refresh` returns the repos that succeeded
  plus the first error. nav treats any non-nil error as `remoteErrMsg` — so a
  partial failure shows the warning rather than silently dropping a forge. (The
  cache still gets the successful subset, matching `bridge list` today.)
- **`FetchRemote` nil:** silent fall back to cache re-read (current behavior).

## Testing (TDD)

`internal/remote`:
- `discoverRemoteTargets` — table tests over a `t.TempDir()` layout: github
  owner with `.envrc` at owner level; github owner with `.envrc` only under a
  `<visibility>/` subdir; gitlab/forgejo/ado markers; missing markers yield no
  target; dedup across roots.
- `Refresh` — a target whose token does not resolve returns no repos and writes
  an (empty-repos) cache: asserts the cache file is written and no network is
  touched. (Forge HTTP paths are already covered by `internal/forge` client
  tests; `Refresh` does not re-test them.)

`internal/nav` (pure `Update`, no TTY):
- `r` with a fake `FetchRemote` returning 2 refs → next msg is `remoteMsg` with
  `↓`-prefixed, sorted rows; after applying, `remoteState == loadOK`.
- `r` with a fake `FetchRemote` returning an error → `remoteErrMsg` →
  `remoteState == loadErr`.
- `r` with `FetchRemote == nil` → falls back to cache read (no panic; rows come
  from a seeded `RemoteCache` temp file).

`cmd/bridge`:
- Existing `list_test.go` stays green (delegation only, behavior unchanged).

### Verify (Go overlay gates)

- `gofmt -l .` empty
- `go vet ./...` clean
- `golangci-lint run` clean
- `go test -race ./...` full suite green
- Manual: create a repo on GitHub → `bridge nav` → `r` → repo appears with `↓`.

## Scope guard / non-goals

- No dashboard refresh key.
- No change to `bridge list` CLI behavior or its 1h TTL.
- No new dependencies; no change to the cache file format/path.
- No change to how tokens are discovered (the moved direnv logic is verbatim).
