# `bridge nav` Remote Sync Status — Design Spec

**Issue:** [#126](https://github.com/freaxnx01/bridge/issues/126) — feat(nav): show remote sync status (unpushed/ahead + behind) on the dashboard

**Status:** Approved (brainstorm 2026-06-02)

---

## Problem

The `bridge nav` Screen-2 dashboard shows each worktree's modified-file count and an **ahead** count, both parsed from a single `git status --porcelain=v1 --branch` call (`parseDirtyStatus` in `internal/nav/format.go`, `dirtyInfo` in `internal/nav/types.go`). There is:

- **no `behind` count** — you can't see a worktree needs a pull;
- **no distinct "no upstream" state** — a branch with no remote tracking looks the same as one that is in sync;
- **no freshness** — `git status -sb` reflects only the *locally-known* upstream, so "out of sync with remote" can't be surfaced without a fetch.

## Goal

An at-a-glance, per-worktree "in sync with remote?" signal on the dashboard: **ahead**, **behind**, and a distinct **no-upstream** state, kept accurate by a non-blocking background fetch.

Out of scope: pushing/pulling from nav (read-only signal only); per-worktree fetch configuration; the repo picker (Screen-1).

---

## Approach

Additive to `internal/nav`. The existing dirty pipeline already carries the data path (`gitDirtyCmd` → `dirtyMsg` → `dirtyInfo` → `dirtyView`); this extends it rather than adding a parallel one.

1. The **same** `git status -sb` call already run per worktree also reports behind and upstream presence — so parsing gains `behind`/`noUpstream` with **no extra git call**.
2. Accurate `behind` requires fresh remote-tracking refs. On dash entry the dashboard runs **one** background `git fetch` for the repo (all worktrees share the object store), then re-reads each worktree's status. Last-known state renders immediately; the fetch updates it silently when it lands. Never blocks the UI; offline is a no-op.

### Why fetch once per repo

The nav dashboard is scoped to a single repo. Git worktrees of one repo share the same object store and remote-tracking refs, so a single `git fetch` from any worktree path freshens the remote state for all of them. Fetching per-worktree would be redundant network work.

---

## Components

### 1. Parser — `parseDirtyStatus` (`internal/nav/format.go`)

Extend to parse the full `## ` branch-header grammar of `git status --porcelain=v1 --branch`:

| Header line | Parsed result |
|---|---|
| `## main` | `noUpstream = true` (no `...upstream` token) |
| `## main...origin/main` | upstream present, in sync |
| `## main...origin/main [ahead 2]` | `ahead = 2` |
| `## main...origin/main [behind 3]` | `behind = 3` |
| `## main...origin/main [ahead 2, behind 3]` | `ahead = 2, behind = 3` |
| `## HEAD (no branch)` | detached; `noUpstream = true` |

Rules:
- `noUpstream` is true when the header has no `...` upstream separator (covers unborn branches and detached HEAD).
- `ahead`/`behind` are read from the `[…]` token; absent ⇒ 0.
- The non-header change-line counting (`modified`) is unchanged.

### 2. Type — `dirtyInfo` (`internal/nav/types.go`)

```go
type dirtyInfo struct {
	modified   int
	ahead      int
	behind     int  // NEW: unpulled commits (accurate after fetch)
	noUpstream bool // NEW: branch has no remote tracking
	clean      bool
}
```

`clean` keeps its current meaning (`modified == 0`); it does not consider ahead/behind.

### 3. Fetch Cmd — `gitFetchCmd` (`internal/nav/data.go`)

```go
// gitFetchCmd freshens remote-tracking refs for the repo so behind/ahead are
// accurate. Runs once per dashboard (worktrees share the object store).
func gitFetchCmd(path string) tea.Cmd
```

- Runs `git -C <path> fetch --quiet`.
- Returns `fetchDoneMsg{err error}` (new message type). Errors are reported, not fatal.

### 4. Reducer (`internal/nav/update.go`)

- **New message:** `type fetchDoneMsg struct{ err error }`.
- **On `dashRowsMsg`** (the single choke point — dash entry, detach-return, create-worktree): in addition to the existing `m.dirtyCmds()`, also fire `gitFetchCmd(m.repo.Path)`. Last-known status loads immediately via `dirtyCmds()`.
- **On `fetchDoneMsg`:** re-run `m.dirtyCmds()` so every row re-reads `git status -sb` against the now-fresh remote refs (behind becomes accurate). On `msg.err != nil`, do nothing further (keep last-known) — the UI never errors on an offline/failed fetch.

`Update` stays a pure function of `(model, msg)`; the fetch and status reads are `tea.Cmd`s.

### 5. View — `dirtyView` (`internal/nav/view.go`)

Render the per-worktree indicator from `dirtyInfo`. Precedence, evaluated top-down:

1. `loadPending` → spinner; `loadErr` → `?` (muted). *(unchanged)*
2. `noUpstream` → `⤳ no upstream` (`stMuted`). Takes precedence over counts — ahead/behind are meaningless without an upstream.
3. Otherwise build space-joined tokens, **omitting any with a zero count**:
   - `●N` modified — `stBad` (red)
   - `↑N` ahead — `stWarn` (amber)  *(matches today's ahead glyph/colour)*
   - `↓N` behind — `stAccent` (blue)
4. If step 3 produced **no** tokens (`modified == 0 && ahead == 0 && behind == 0`) → `✓ clean` (`stOk`).

So a clean-but-diverged worktree shows arrows only (e.g. `↑1 ↓3`), a clean+in-sync one shows `✓ clean`, and a modified+diverged one shows all three (e.g. `●2 ↑1 ↓3`). Note `dirtyInfo.clean` (== `modified == 0`) alone does **not** decide `✓ clean`; the view also requires `ahead == 0 && behind == 0`.

No "fetching…" spinner on the indicator — the background fetch updates the row silently when it completes (decision: keep the dashboard quiet).

---

## Data flow

```
dash entry / detach-return
  └─ dashRowsMsg
       ├─ dirtyCmds()        → per worktree: gitDirtyCmd → dirtyMsg (last-known)   → row shows immediately
       └─ gitFetchCmd(repo)  → fetchDoneMsg
                                  └─ dirtyCmds() again → dirtyMsg (fresh behind)   → rows update silently
```

---

## Error handling & edge cases

- **Offline / fetch fails:** `fetchDoneMsg.err != nil` → no re-read; rows keep last-known ahead/behind. No error surfaced in the UI.
- **No upstream / detached HEAD / unborn branch:** `noUpstream` → distinct muted marker; fetch still runs (harmless).
- **Fetch latency:** non-blocking; the dashboard is fully usable showing last-known state while the fetch is in flight.
- **Concurrent status read during fetch:** the initial `dirtyCmds()` may overlap the fetch; git ref updates are atomic and the authoritative re-read happens on `fetchDoneMsg`, so the final state is correct.

---

## Testing (TDD)

- **Parser** (`format_test.go`, table-driven): in-sync, ahead-only, behind-only, ahead+behind, no-upstream, detached HEAD, dirty + ahead + behind. Assert `modified/ahead/behind/noUpstream/clean`.
- **View** (`view_test.go`): `dirtyView` for each state — no-upstream marker, `✓ clean`, `●N ↑N ↓N`, arrows-only-when-clean, zero-count omission.
- **Reducer** (`update_test.go`): `dashRowsMsg` returns a Cmd batch that includes the fetch; `fetchDoneMsg{}` triggers a dirty reload Cmd; `fetchDoneMsg{err}` is a harmless no-op (no panic, no error state).

Per-task gate: `go test ./internal/nav/...`. Final gate: `gofmt -l internal/nav` (empty), `go vet ./...`, `go test -race ./...`, `golangci-lint run` (the last two run in CI).

---

## Files touched

All modifications, additive:

- `internal/nav/types.go` — `dirtyInfo` fields; `fetchDoneMsg`.
- `internal/nav/format.go` (+ `format_test.go`) — `parseDirtyStatus` upstream grammar.
- `internal/nav/data.go` — `gitFetchCmd`.
- `internal/nav/update.go` (+ `update_test.go`) — wire fetch on `dashRowsMsg`; handle `fetchDoneMsg`.
- `internal/nav/view.go` (+ `view_test.go`) — `dirtyView` rendering.
- `README.md`, `CHANGELOG.md` — document the indicator.
