# nav Ctrl+N: create a new repo from the picker (#129)

**Date:** 2026-06-21
**Area:** `internal/nav` (new-repo modal) + `cmd/bridge` (`createAndClone` extraction + `CreateRepo` wiring)
**Status:** Approved (design)

## Problem

The bash tool could create a new repo from the picker. The Go `bridge create`
CLI (Forgejo/GitHub, private/public, then clone) exists, and the Telegram bot
has `/newrepo` — but `bridge nav` has no way to create a repo. Issue **#129**
(consolidating #88) asks for **`Ctrl+N` on the repo picker → choose forge → (for
GitHub) choose Private|Public → create → clone → land in nav**.

## Goal (success criterion)

On the repo picker, `Ctrl+N` → type a name → pick forge × visibility → the repo
is created on the forge and cloned locally → you land on the new repo's nav
**dashboard** (as if you'd picked it).

## Decisions (from brainstorming)

1. **Two-step inline modal** mirroring the existing `n` new-worktree modal:
   step 1 = name text input; step 2 = a 4-option forge×visibility pick.
2. **Forge set = Forgejo + GitHub** (exactly what `bridge create` supports).
   **ADO is deferred** — `bridge create` has no ADO path; the issue's ADO option
   waits until ADO create exists. Visibility: **Private (default) | Public** for
   both forges.
3. **After create → open the new repo's dashboard** (your choice; "launch" =
   opened in nav, not auto-spawning an agent).
4. **nav stays forge-token-free** — it creates via an injected
   `Config.CreateRepo` callback (like `Clone`/`FetchIssues`/`FetchRemote`).
5. **DRY:** extract a `createAndClone` core in `cmd/bridge` that **both** the
   `bridge create` CLI and the nav callback call — no duplicated forge/clone
   logic.

## Architecture

```
  picker ──Ctrl+N──► newRepoModal
     step=name:  text input  ──⏎ (validate non-empty)──► step=forge
     step=forge: ▸ Forgejo·Private / Forgejo·Public / GitHub·Private / GitHub·Public
                 ↑↓ move sel · ⏎ create · esc back/cancel
                        │ ⏎
                        ▼  createRepoCmd → m.cfg.CreateRepo(name, forge, private)   (tea.Cmd, off-loop)
                        ▼
                 repoCreatedMsg{repo core.Repo, err error}
                   err  → modal.err, creating=false
                   ok   → clear modal → enter dashboard for repo (the openRepoRow path)
```

### 1. `internal/nav` — the modal

- New struct in `types.go`:
  ```go
  type repoModalStep int
  const ( repoModalName repoModalStep = iota; repoModalForge )

  // newRepoModal is the inline Ctrl+N create-repo state (picker screen).
  type newRepoModal struct {
      name     string
      step     repoModalStep
      sel      int  // 0..3 index into repoForgeChoices
      creating bool
      err      string
  }

  // repoForgeChoices are the forge×visibility options, in display order.
  var repoForgeChoices = []struct{ label, forge string; private bool }{
      {"Forgejo · Private", "forgejo", true},
      {"Forgejo · Public", "forgejo", false},
      {"GitHub · Private", "github", true},
      {"GitHub · Public", "github", false},
  }
  ```
- A model field `repoModal *newRepoModal` (picker-scoped; distinct from the
  dash-scoped `modal *newWorktreeModal` — never active together).
- `Config.CreateRepo func(name, forgeName string, private bool) (core.Repo, error)`
  (nil disables `Ctrl+N`).
- Message: `repoCreatedMsg struct{ repo core.Repo; err error }`.

### 2. `internal/nav` — Update

- Picker `focusList` switch (next to `case "r"`/`case "o"`):
  ```go
  case "ctrl+n":
      if m.cfg.CreateRepo == nil { return m, nil }
      m.repoModal = &newRepoModal{}
  ```
- When `m.repoModal != nil`, route keys to a `updateRepoModal(msg)` (placed
  before the picker list handling, like the worktree modal at `update.go:352`):
  - **name step:** runes append, backspace trims, `enter` → if name empty set
    `err="name required"` else `step=repoModalForge`, `esc` → cancel
    (`repoModal=nil`).
  - **forge step:** `up`/`k` / `down`/`j` move `sel` clamped to 0..3, `enter` →
    `creating=true`, return `m.createRepoCmd()`, `esc` → back to name step.
  - While `creating`, ignore further keys except the resolving `repoCreatedMsg`.
- `repoCreatedMsg` handler: on `err` → `m.repoModal.err = err.Error()`,
  `creating=false`; on success → `m.repoModal = nil`, then **enter the dashboard
  for `msg.repo`** by reusing the same path `openRepoRow` uses for a local repo
  (`m.repo = repo; m.screen = screenDash;` + `loadDashRowsCmd`/`loadNotesCmd`).
- `createRepoCmd`:
  ```go
  func (m Model) createRepoCmd() tea.Cmd {
      create := m.cfg.CreateRepo
      ch := repoForgeChoices[m.repoModal.sel]
      name := strings.TrimSpace(m.repoModal.name)
      return func() tea.Msg {
          repo, err := create(name, ch.forge, ch.private)
          return repoCreatedMsg{repo: repo, err: err}
      }
  }
  ```

### 3. `internal/nav` — View

Render the modal overlay (extend the existing modal rendering):
- **name step:** `New repo` panel with `name: <name>▌` and hint `⏎ next · esc cancel`.
- **forge step:** `New repo · "<name>"` panel listing the 4 choices, the `sel`
  row highlighted; `⏳ creating…` while in flight; `err` shown in red when set.
  Hint `↑↓ pick · ⏎ create · esc back`.

### 4. `cmd/bridge` — extract `createAndClone` + wire `CreateRepo`

- Lift the create+clone core out of `runCreate` into:
  ```go
  // createAndClone validates the name, creates the repo on the forge, clones it,
  // and returns the local repo. Shared by the CLI and nav's CreateRepo callback.
  func createAndClone(ctx context.Context, name, forgeName string, private bool) (core.Repo, error)
  ```
  It does what `runCreate` does today (validate via `validRepoName`; resolve the
  forge target dir + token via `forgejoTargetDir`/`githubTargetDir`;
  `forge.CreateRepo`; `cloneFn`), and returns a `core.Repo{Name, Path: targetDir,
  Forge, Owner, Visibility, RemoteURL: ref.SSHURL, DefaultBranch}`. `runCreate`
  becomes a thin wrapper: call `createAndClone`, then emit `--json`/text output.
- In `cmd/bridge/nav.go`, inject:
  ```go
  CreateRepo: func(name, forgeName string, private bool) (core.Repo, error) {
      return createAndClone(context.Background(), name, forgeName, private)
  },
  ```

## Error handling

- **Invalid/empty name:** nav blocks the empty case at the name step
  (`err="name required"`); `createAndClone` enforces `validRepoName` and returns
  a clear error → surfaced in `modal.err`.
- **Repo already exists / forge error / clone failure:** `createAndClone`
  returns the error (`ErrRepoExists` → "repo … already exists"; clone failure
  keeps the created-repo URL in the message) → `modal.err`, `creating=false`,
  modal stays open so the user can rename/cancel.
- **No token for the chosen forge:** `createAndClone` errors clearly → `modal.err`.
- **`Config.CreateRepo` nil:** `Ctrl+N` is a no-op (feature disabled).

## Testing

- **`internal/nav`** (pure `Update`): `Ctrl+N` opens the modal (when `CreateRepo`
  set; no-op when nil); name input + `enter` advances to the forge step; empty
  name → `err`; forge `↑↓` moves `sel`; `enter` on the forge step fires
  `createRepoCmd` whose resolved msg, with a **fake `CreateRepo`** returning a
  `core.Repo`, lands on `screenDash` with `m.repo` set; a fake returning an error
  → `modal.err`, modal still open. Plus a **golden flow test** of the modal
  (name step and forge step frames) using the TUI harness.
- **`cmd/bridge`**: a test that `createAndClone` is exercised by both paths — the
  existing `create_test.go` (CLI, with `cloneFn` stubbed) keeps passing against
  the refactor; add/confirm a direct `createAndClone` test (stub `cloneFn`,
  `BRIDGE_*_API` to an httptest forge) returning the expected `core.Repo`.
- Gates: `gofmt`/`go vet`/`golangci-lint`/`go test -race ./...`; golden
  stability; the new screen renders under `--once`-style harness tests.

## Non-goals

- **ADO create** (no `bridge create` ADO path) — deferred; consolidates #88.
- **The `bridge create` CLI / `/newrepo` bot flow** — already exist; this only
  adds the nav entry point + the shared `createAndClone`.
- **Auto-launching an agent** after create (you chose dashboard).
- **Editing/deleting/renaming** repos.

## Open questions / follow-ups

- **Default forge order / remembering the last choice:** start with `sel=0`
  (Forgejo·Private); a "remember last" could come later — out of scope.
- **ADO create** becomes a clean follow-up once a `forge.ADOClient.CreateRepo`
  exists (then add the two ADO rows to `repoForgeChoices`).
