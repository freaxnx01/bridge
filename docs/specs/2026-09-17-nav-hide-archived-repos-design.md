# nav: hide archived repos in the repo picker

**Issue:** [#292](https://github.com/freaxnx01/bridge/issues/292)
**Date:** 2026-09-17
**Status:** approved

## Problem

Archived repos show up in the `bridge nav` repo picker. They cannot be worked on,
so they are noise in a list that is already long (88 remote refs + every local
clone).

The obvious reading — "the picker lists archived repos from the forge" — is wrong,
and the fix follows from the correction. Every forge client **already** drops
archived repos inside `ListRepos`:

- `internal/forge/github.go:339` — `if r.Archived { continue }`
- `internal/forge/forgejo.go:320` — same
- `internal/forge/gitlab.go:66` — same

and the on-disk cache confirms it: `~/.cache/bridge/remote.list` holds 88 refs and
**zero** archived ones. The remote (`↓`) rows are clean today.

The noise is **local clones**. `core.DiscoverRepos` walks the repos root and knows
nothing about archived state, so a clone of a repo that was archived upstream keeps
appearing forever. Concretely: `freaxnx01/FlowHub-CAS-AISE` is archived on GitHub,
is still checked out at `~/repos/github/freaxnx01/public/FlowHub-CAS-AISE`, and is
still a picker row.

So the problem to solve is **teaching nav which local repos are archived**, and the
information it needs is exactly what `ListRepos` currently throws away.

## Goals

- A local clone of an archived repo does not appear in the picker by default.
- Archived state comes from the forge, with no manual list to maintain.
- A deliberate key reveals archived rows when one is genuinely wanted.
- No behaviour change for any other consumer of `ListRepos`, in particular the
  `list_repos` MCP tool.

## Non-goals

- Hiding archived repos anywhere outside the nav picker (`bridge list`, `bridge
  open`, completion). They are unaffected.
- Detecting *stale* or abandoned clones that were never archived upstream.
- Deleting or offering to delete an archived clone.
- ADO. Azure DevOps repos have no archived concept in this codebase; `ado.go`'s
  `ListRepos` is untouched.

## Design

### 1. Stop discarding archived state in the forge clients

`ListRepos` on the GitHub, Forgejo and GitLab clients stops filtering and instead
records the flag on the ref:

```go
out = append(out, RepoRef{
    ...
    Archived: r.Archived,
})
```

`forge.RepoRef` already carries `Archived bool` (`internal/forge/client.go:83`), so
this is populating an existing field rather than widening the type.

This makes `ListRepos` report what the forge says instead of deciding for its
callers what they may see — but it is a **semantic change to a shared interface**,
so every existing caller that relied on the filtering gets the filter back
explicitly:

- `internal/mcp/tools_read.go` `handleListRepos` — drops archived refs before
  building `listReposOutput`. The `list_repos` tool's output is byte-identical to
  today's.
- `internal/nav/data.go` `remoteRows` — drops archived refs, so an archived repo is
  never offered as a clone-on-select `↓` row. Cloning something archived is exactly
  the wrong affordance.

`internal/remote/remote.go` `Refresh` keeps whatever the clients return, so
`remote.list` grows by the archived count (3 today) and becomes the set nav reads.
That cache is the transport; no new file, no new fetch, no new TTL.

### 2. Derive the archived set where the rows are already built

Both remote-loading paths funnel through `remoteRows(refs)` — the cache reader
(`loadRemoteCmd`) and the live refresh (`refreshRemoteCmd`). That is the one seam
where refs are in hand, so the archived set is computed there alongside the rows:

```go
// archivedKeys returns the repoRowKey identity of every archived ref.
func archivedKeys(refs []forge.RepoRef) map[string]bool
```

The key is the **existing** `repoRowKey` identity from `internal/nav/format.go:56`
— case-insensitive `forge + owner + name` — which is already how
`dedupRemoteRows` decides that a `↓` row and a local clone are the same repo.
Reusing it means a local clone matches its archived remote ref under exactly the
same rules that already pair them, including the `FreaxNx01` vs `freaxnx01` casing
the dedup logic was built to survive.

`remoteMsg` and `remoteErrMsg` each carry the set alongside their rows, and the
`Update` handlers store it on the model. Both messages, because a partial refresh
still yields usable refs.

### 3. Filter in `visibleRepos`, toggle with `ctrl+a`

`Model` gains two fields (`internal/nav/model.go`):

```go
archived     map[string]bool // repoRowKey set of archived repos, from the remote cache
showArchived bool            // ctrl+a reveals archived rows; session-local, default false
```

`visibleRepos()` (`internal/nav/update.go:266`) drops rows whose `repoRowKey` is in
`archived`, unless `showArchived`. It sits **before** the forge subfilter and the
text filter, and applies to local and remote rows with one rule — no second code
path for the two row kinds.

`ctrl+a` toggles `showArchived` in `updatePicker`'s picker-global switch, next to
`ctrl+f`. Picker-global rather than list-focus-only, because the picker opens with
the filter focused, so a bare letter key would be typed into the filter instead.
`ctrl+f` (forge subfilter) already establishes this pattern — and already shadows
the textinput's readline binding for the same reason — so `ctrl+a` follows an
existing precedent rather than setting a new one.

### 4. What the user sees

The hint line gains `· ctrl+a archived` when there is at least one archived repo to
reveal, matching how `· ctrl+f forge` appears only when the forge subfilter is
meaningful (`view.go:301`). An environment with no archived repos sees no change at
all.

With `showArchived` on, the revealed rows are rendered muted (`stMuted`) with an
` archived` suffix, so they are visibly not ordinary rows. They sort in their normal
alphabetical position — a revealed row is being looked for, so it should be where
the user expects it.

### 5. Failure behaviour: fail open

Every path that yields no archived information leaves rows **visible**:

- no cache file, or an unreadable one → empty set → nothing hidden
- a stale cache → the archived set as of the last refresh; `r`/`ctrl+r` refreshes it
- a repo with no remote ref at all (never pushed, or a forge whose token is
  missing) → not in the set → shown

A repo disappearing because a token expired or a fetch timed out would be a far
worse failure than an archived repo lingering one more session.

## Testing

Per the Go stack overlay: table-driven, `t.Run` subtests, hand-rolled fakes, green
under `-race`.

**`internal/forge`** — each client's `ListRepos` against an `httptest` server whose
payload contains one archived and one active repo: both come back, and the archived
one carries `Archived: true`. `forgejo_test.go:23`'s fixture already has an
`archived-repo` entry, so its existing assertion inverts rather than needing new
scaffolding.

**`internal/mcp`** — `handleListRepos` with a fake client returning an archived ref
omits it from `listReposOutput`. This is the regression test for the interface
change; without it the widened `ListRepos` silently leaks archived repos into the
MCP tool.

**`internal/nav`** —

- `archivedKeys` builds the expected case-insensitive key set.
- `remoteRows` drops archived refs (no clone-on-select row).
- `visibleRepos` hides a **local** row matching an archived key, shows it when
  `showArchived`, and is unaffected by an empty set. Includes the casing case
  (`FreaxNx01/FlowHub-CAS-AISE` archived upstream vs the lowercase local path).
- `ctrl+a` through `Update` flips `showArchived` and the row count changes with it;
  the selection index stays in range (`clampInt`, as `ctrl+f` does).
- The view test asserts the `archived` marker and the conditional hint fragment.

No tmux tier-2 test: this changes list contents and one key, both fully drivable
through `Update`.

## Acceptance criteria

- [ ] `ListRepos` on the GitHub, Forgejo and GitLab clients returns archived repos
      with `Archived: true` instead of dropping them; ADO is untouched.
- [ ] The `list_repos` MCP tool still omits archived repos.
- [ ] Archived repos are never offered as clone-on-select (`↓`) rows.
- [ ] A local clone of a repo archived upstream does not appear in the nav picker by
      default, matched case-insensitively on forge+owner+name.
- [ ] `ctrl+a` in the picker reveals archived rows, rendered muted with an
      `archived` marker, and hides them again.
- [ ] The `ctrl+a` hint appears only when at least one archived repo is present.
- [ ] With no/unreadable/stale cache, or for a repo with no remote ref, nothing is
      hidden.
- [ ] `gofmt -l .` empty, `go vet ./...` clean, `golangci-lint run` clean,
      `go test -race ./...` green.

## Notes and discoveries (out of scope)

- `README.md:75` states `repo-meta.json` is written by `list -r [--refresh]`, but no
  Go code writes that file — only `LoadRepoMeta`/`MergeRepoMeta` read it. The cache
  on disk is stale from the bash era (`fetched_at` ~2026-05). Worth an issue; not
  touched here.
