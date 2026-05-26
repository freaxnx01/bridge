# bridge core redesign — Plan B.1 (pre-cutover gap fillers)

**Goal:** Close two cutover-blocking gaps from Plan B before Plan C touches the user's `~/.bashrc`:

1. **Slot bookkeeping** — Go writes to `slots.json`. Currently it only reads. After cutover, the bash bridge stops writing too, so `slots.json` would freeze at whatever was there pre-cutover. Wire a write at session-create time and a status-derived display on read.
2. **Unpushed-branch detection** — `bridge sync` records repos with commits ahead of upstream. Spec folds this from `bridge-unpushed-warn.sh`. Without it, `bridge status` shows `unpushed: 0` perpetually once `_BRIDGE_VERSION` retires.

Out of scope (deferred to Plan C or later):
- Worktree wiring (parsed but ignored — low impact, fixed when worktrees come back into use)
- Repo metadata enrichment in `bridge open --json` (cosmetic)
- File-naming alignment (`watcher.pid` vs `watch.pid`) — handled at cutover

## Tasks

### Task 1: `core.WriteSlots` + `core.UpsertSlot`

**Files:**
- Modify: `internal/core/slot.go` (add writer + upsert; reuse `internal/store.AcquireLock`)
- Create/modify: `internal/core/slot_test.go`

Implementation sketch:
```go
// WriteSlots writes the slot registry in Go format under flock.
func WriteSlots(path string, slots []Slot) error { ... }

// UpsertSlot reads, replaces-by-ID-or-appends, writes — all under flock.
func UpsertSlot(path string, s Slot) error { ... }
```

Tests cover: write-then-read round-trip; upsert adds new; upsert replaces existing by ID; concurrent UpsertSlot calls don't lose data (flock holds).

### Task 2: `preflightOpen` records the slot when launching an agent

**Files:**
- Modify: `cmd/bridge/preflight.go`

After resolving repo+slot+agent and before emitting the exec directive, call `core.UpsertSlot(filepath.Join(cacheRoot(), "slots.json"), core.Slot{ID: slot, Repo: repo.Name, Agent: agentName, Created: time.Now().UTC()})`. Tests: `__preflight open --agent claude` writes the slot; second invocation doesn't duplicate.

### Task 3: Unpushed detection in syncer

**Files:**
- Modify: `internal/syncer/syncer.go` (add `Unpushed(ctx, repos) []string`)
- Modify: `internal/syncer/syncer_test.go`

`Unpushed` runs `git rev-list --count @{u}..HEAD` per repo; returns names of repos with N > 0. Repos without upstream → silently skipped (not flagged). Uses the same `Runner` injection.

### Task 4: Wire unpushed into `runSyncNow`

**Files:**
- Modify: `cmd/bridge/sync_now.go`

After `s.Run(ctx, repos)`, also call `syncer.Unpushed(ctx, repos)` and populate `state.Unpushed`.

Test: with a stub `git` that exits 0 for fetch/pull and prints "2" for rev-list, sync.json's `unpushed` array is non-empty.

### Task 5: Final smoke + commit summary

Cross-compile Windows, `go test ./...`, bats. No new tag (this rides on `v2.0.0-go.1`).
