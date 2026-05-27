# Restore repo-name tab-completion (bash + PowerShell)

**Issue:** [#65](https://github.com/freaxnx01/bridge/issues/65)
**Date:** 2026-05-27
**Status:** Draft — pending user approval

## Problem

The Phase-4 Go cutover (commit `ef53cf4`, #35) deleted the bash `bridge()`
function, taking with it the two-stage tab-completion that resolved a typed
prefix or keyword to a local repo basename. Today:

```
$ bridge nextgen<tab>          # bash
(nothing)
$ bridge open nextgen<tab>     # bash
(nothing)
```

The previous bash implementation matched first against a basename cache,
then fell back to searching `repo-meta.json` (description + topics). That
fallback is what made `nextgen<tab>` resolve to `ArchiveRestApiNextGen`
even though the substring `nextgen` is not in the basename.

Bridge is a repo-launcher whose value proposition is fast local-repo
navigation; losing tab-completion is a real positioning hit
(`docs/positioning.md`).

## Scope

In scope:

- Restore tab-completion for repo basenames, with meta-keyword fallback,
  via Cobra `ValidArgsFunction`.
- Wire completion on `bridge open`, `bridge rm`, and at the root level
  (mirroring the existing `positional.go` rewrite that turns
  `bridge nextgen` into `bridge open nextgen` at runtime).
- Bash and PowerShell parity, via Cobra's built-in shell-script emission.

Out of scope (separate follow-up issues):

- **Restoring the `repo-meta.json` writer.** Nothing in the current Go
  binary populates this cache; it was previously written by the deleted
  bash code. The completion code reads it through `core.LoadRepoMeta`,
  which gracefully returns an empty map when the file is missing —
  meta-fallback degrades to a no-op until the writer is restored. File a
  new issue: *feat(meta-cache): restore forge-metadata writer for
  `repo-meta.json`*.
- Removing the stale `~/.cache/bridge/local-repos.list` leftover. No Go
  code reads or writes it. Untouched by this work; optional housekeeping
  later.
- Changing the runtime resolver in `cmd/bridge/open.go` to consult meta
  (this issue only changes completion). Today's resolver does
  exact-basename → basename-substring matching; that stays as is.

## Design

### Package layout

```
internal/completion/         # new
  completion.go              # pure Resolve(prefix, repos, meta) []string
  completion_test.go         # table-driven unit tests

cmd/bridge/
  completion.go              # new — Cobra glue: loads data, calls Resolve
  completion_test.go         # new — invokes Cobra's __complete entry point
  open.go                    # +1 line: openCmd.ValidArgsFunction = ...
  rm.go                      # +1 line: rmCmd.ValidArgsFunction = ...
  root.go                    # +1 line: rootCmd.ValidArgsFunction = ...
```

The pure resolver lives in `internal/completion` so its behaviour can be
unit-tested without touching disk, Cobra, or the filesystem walker. The
Cobra glue in `cmd/bridge/completion.go` is the only piece that performs
I/O.

### Resolver contract

```go
// Resolve returns the candidate repo names for a tab-completion prefix.
// It returns the first non-empty stage:
//
//  1. Case-insensitive basename prefix match.
//  2. Case-insensitive basename substring match.
//  3. Meta-keyword fallback: case-insensitive substring against
//     description and topics from repo-meta.json, mapped back to names.
//
// An empty `prefix` returns all repo names (stage 1 matches everything).
// Returned names are sorted (case-insensitive) and de-duplicated. The
// function never returns an error.
func Resolve(prefix string, repos []core.Repo, meta map[string]core.RepoMeta) []string
```

Why three stages instead of merging into one ranked list: the original
bash behaviour was "prefer basename match over meta fallback"
(commit `bfb9d96`). A typed prefix that matches both a basename and an
unrelated description should not surface the description-only repo
alongside the obvious basename hit. Stages preserve that preference.

### Cobra glue

```go
// cmd/bridge/completion.go
func repoNameCompletion(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
    repos, err := core.DiscoverRepos(reposRoot())
    if err != nil {
        return nil, cobra.ShellCompDirectiveError
    }
    meta, _ := core.LoadRepoMeta(filepath.Join(cacheRoot(), "repo-meta.json"))
    return completion.Resolve(toComplete, repos, meta), cobra.ShellCompDirectiveNoFileComp
}
```

Registrations:

| Command | Guard | Notes |
|---|---|---|
| `openCmd.ValidArgsFunction` | `len(args) == 0` | Only first positional. |
| `rmCmd.ValidArgsFunction` | `len(args) == 0` | Only first positional. |
| `rootCmd.ValidArgsFunction` | `len(args) == 0` | **Filters out** candidates that exactly match a known verb in `positional.knownVerbs` so `bridge li<tab>` continues to surface `list` rather than getting overridden by repo basenames. Cobra still emits subcommand suggestions independently. |

`knownVerbs` is already a package-level `var` in `cmd/bridge/positional.go`,
so the new `completion.go` (same package) references it directly — no
refactor needed.

### Cache strategy & self-healing

- **Basename data:** fresh `core.DiscoverRepos(reposRoot())` walk on every
  tab press. Two `os.ReadDir` calls — sub-10 ms on warm FS cache. No cache
  file is read or written. New clones appear immediately without an
  explicit `bridge list -r`.
- **Meta data:** `core.LoadRepoMeta(filepath.Join(cacheRoot(), "repo-meta.json"))`
  on every tab press. Missing file → empty map → meta stage no-ops. No
  refresh attempt during completion (would add latency, network).

No background refresh. No debouncing. The walk is cheap enough that
adding complexity would be premature.

### README updates

Add a *Completion* section (or extend the existing install section) with
both install snippets:

- **bash** — `eval "$(bridge completion bash)"` in `.bashrc` (or
  `bridge completion bash > /etc/bash_completion.d/bridge`).
- **PowerShell** — `bridge completion powershell | Out-String | Invoke-Expression` in `$PROFILE`.

If a `brg`-alias completion recipe already lives in `docs/`
(commit `d0ce58a`), cross-link it.

## Acceptance criteria

Mapped from the issue:

- [x] `bridge open <tab>` completes basenames (case-insensitive) in bash.
- [x] Same in PowerShell.
- [x] `bridge open nextgen<tab>` resolves to `ArchiveRestApiNextGen` —
      **provided `repo-meta.json` exists and contains `next-gen` in
      topics/description**. The completion code is wired; the data
      availability depends on the follow-up writer issue.
- [x] Cache self-heals: fresh `DiscoverRepos` walk every tab press;
      clones show up within one tab press.
- [x] Root form: `bridge nextgen<tab>` mirrors `bridge open nextgen<tab>`.

## Testing strategy

| Layer | What |
|---|---|
| `internal/completion/completion_test.go` | Table-driven unit tests for `Resolve`: prefix-beats-substring, basename-beats-meta, description match, topic match, case-insensitivity, dedup, empty prefix returns all, no match returns empty. Pure function — fast, no fixtures. |
| `cmd/bridge/completion_test.go` | Integration: invokes Cobra's hidden `__complete` command via `rootCmd.SetArgs(...)` with `t.TempDir()`-backed `BRIDGE_REPOS_ROOT` and `XDG_CACHE_HOME`. Asserts candidate lists for `open`, `rm`, and root (with verb-filter behaviour). |
| `shims/bridge-shim.bats` | One end-to-end smoke test: `bridge __complete open <prefix>` through the shim. Confirms the shim passes the completion handshake through without mangling. |
| PowerShell | Manual checklist appended to the PR description (the PR template already requires Windows exercise). No Pester harness is added in this PR. |

## Risk & open questions

- **`knownVerbs` drift:** the root-level filter depends on `knownVerbs`
  staying current. New subcommands must be added there — same constraint
  the existing positional rewrite already imposes, so no new tax.
- **Subcommand vs repo-name collision at root:** if a repo's basename
  exactly matches a verb (e.g. someone clones a repo called `list`), the
  filter hides it from root-level completion. They can still reach it via
  `bridge open list<tab>`. Acceptable trade-off; the alternative would
  break `bridge li<tab>` for subcommand discovery.
- **Cobra `__complete` semantics:** `cobra.ShellCompDirectiveNoFileComp`
  is correct (we don't want filename fallback). Verify both
  `bridge completion bash` and `bridge completion powershell` produce
  working scripts during implementation — Cobra emits both from the same
  `ValidArgsFunction` registration.
- **Performance on large repo roots:** `DiscoverRepos` is O(forges × owners
  × repos). Mitigation if it ever bites: cache the walk for a few seconds
  in a process-local memo. Not adding it now (YAGNI).

## References

- Issue [#65](https://github.com/freaxnx01/bridge/issues/65)
- Predecessor bash design: commits `28fd18c` (basename cache),
  `bfb9d96` (basename-over-meta preference), `4a46df0` (focus-flag
  completion).
- Existing positional rewrite: `cmd/bridge/positional.go`.
- Existing meta reader: `internal/core/repo_meta.go`.
- `docs/positioning.md` — completion listed as a differentiator.
