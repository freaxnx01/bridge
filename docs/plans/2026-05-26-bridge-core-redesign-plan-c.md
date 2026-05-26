# bridge core redesign — Plan C (Phase 3 cutover)

> **For agentic workers:** This plan touches the user's `~/.bashrc` and replaces the working `bridge` command. Do NOT execute autonomously — every task has a "USER ACTION REQUIRED" or "VERIFY WITH USER" gate. The Go-binary changes in tasks 1, 2, and 5 can be implemented and committed without user action; tasks 3, 4, 6, 7 require explicit go-ahead.

**Goal:** Cut the user's shell over from the bash `bridge` function to the Go binary + shim. After this plan, typing `bridge` invokes the Go binary via the shim; `bridge.sh` remains in the repo but is no longer sourced. Legacy flags (`-r`, `-D`, `--refresh`, bare `away|back|auto`) are silently forwarded to the new verbs inside the Go binary.

**Architecture:** Two-stage cutover. Stage A (tasks 1–2) lands legacy-flag forwarding in the Go binary while the bash bridge is still primary — fully reversible by `git revert`. Stage B (tasks 3–4) flips `~/.bashrc` and renames the install path; risky but trivially rollback-able by editing one line of `.bashrc` back to the bash sourcing line. Stage C (tasks 5–7) does cleanup, CHANGELOG, tag.

**Tech Stack:** Same as B. No new dependencies.

**Spec:** `docs/specs/2026-05-25-bridge-core-redesign-design.md` (Phase 3 section).

**Pre-cutover state observed on 2026-05-26:**
- `~/.bashrc` line 29 sources `bridge.sh`: `_f=~/projects/repos/github/freaxnx01/public/bridge/bridge.sh; [ -f "$_f" ] && . "$_f"; unset _f`
- `~/.bashrc` line 17: `alias brg='bridge'` — preserved, will still resolve to the new function.
- The Go binary is installed at `~/.local/bin/bridge-go` via `make install-go`.
- The shim is installed at `~/.local/share/bridge/bridge-shim.sh` via `make install-shim`, currently invokes `command bridge-go` (intentional during the overlap window).

**Rollback:** at any point, revert `~/.bashrc` line 29 to its original value (sourcing `bridge.sh`) and the old shell behavior is restored. Keep a dated backup before editing.

---

## Task 1: Legacy flag forwarding in Go binary

**Files:**
- Create: `cmd/bridge/legacy.go`
- Create: `cmd/bridge/legacy_test.go`
- Modify: `cmd/bridge/main.go` (call `rewriteLegacy()` before `rewritePositional()`)
- Modify: `cmd/bridge/preflight.go` (mirror legacy rewrites in `dispatchPreflight`)

The Go binary must accept user input that targets the old bash flag surface so muscle memory keeps working. Per spec: silent forwarding, no deprecation hints.

Mapping table:

| Old input | Rewrites to |
|---|---|
| `bridge -r` | `bridge list -r` |
| `bridge -r --refresh` | `bridge list -r --refresh` |
| `bridge --refresh` | `bridge list --refresh` |
| `bridge -D <name>` | `bridge rm <name> --yes` |
| `bridge -D` (no name) | error: "missing repo name for -D" (exit 2) |
| `bridge away` | `bridge presence away` |
| `bridge back` | `bridge presence back` |
| `bridge auto` | `bridge presence auto` |

Subtleties:
- The bash `bridge -D` invocation today skips confirmation; new mapping appends `--yes` to preserve that behavior.
- `--refresh` is ALSO a flag on `bridge list` and `bridge issues`; rewrite only fires when `--refresh` appears as the *first* non-program arg with no verb preceding it.
- `away|back|auto` are also valid args to `bridge presence`; rewrite only fires when these appear as the *only* positional, no verb.

- [ ] **Step 1: Write failing tests in `cmd/bridge/legacy_test.go`**

```go
package main

import (
    "reflect"
    "testing"
)

func TestRewriteLegacyDashR(t *testing.T) {
    got := rewriteLegacyArgs([]string{"bridge", "-r"})
    want := []string{"bridge", "list", "-r"}
    if !reflect.DeepEqual(got, want) {
        t.Errorf("got %v, want %v", got, want)
    }
}

func TestRewriteLegacyDashRWithRefresh(t *testing.T) {
    got := rewriteLegacyArgs([]string{"bridge", "-r", "--refresh"})
    want := []string{"bridge", "list", "-r", "--refresh"}
    if !reflect.DeepEqual(got, want) {
        t.Errorf("got %v, want %v", got, want)
    }
}

func TestRewriteLegacyStandaloneRefresh(t *testing.T) {
    got := rewriteLegacyArgs([]string{"bridge", "--refresh"})
    want := []string{"bridge", "list", "--refresh"}
    if !reflect.DeepEqual(got, want) {
        t.Errorf("got %v, want %v", got, want)
    }
}

func TestRewriteLegacyDashD(t *testing.T) {
    got := rewriteLegacyArgs([]string{"bridge", "-D", "old-repo"})
    want := []string{"bridge", "rm", "old-repo", "--yes"}
    if !reflect.DeepEqual(got, want) {
        t.Errorf("got %v, want %v", got, want)
    }
}

func TestRewriteLegacyAway(t *testing.T) {
    for _, mode := range []string{"away", "back", "auto"} {
        got := rewriteLegacyArgs([]string{"bridge", mode})
        want := []string{"bridge", "presence", mode}
        if !reflect.DeepEqual(got, want) {
            t.Errorf("%s: got %v, want %v", mode, got, want)
        }
    }
}

func TestRewriteLegacyLeavesKnownVerbsAlone(t *testing.T) {
    in := []string{"bridge", "list", "--refresh"}
    got := rewriteLegacyArgs(in)
    if !reflect.DeepEqual(got, in) {
        t.Errorf("known verb mutated: got %v", got)
    }
}

func TestRewriteLegacyLeavesPreflightAlone(t *testing.T) {
    in := []string{"bridge", "__preflight", "-r"}
    got := rewriteLegacyArgs(in)
    if !reflect.DeepEqual(got, in) {
        t.Errorf("preflight rewritten outer layer: got %v", got)
    }
}

func TestRewriteLegacyLeavesAwayAsArgToPresence(t *testing.T) {
    // If the user already typed `bridge presence away`, do nothing.
    in := []string{"bridge", "presence", "away"}
    got := rewriteLegacyArgs(in)
    if !reflect.DeepEqual(got, in) {
        t.Errorf("got %v want unchanged", got)
    }
}
```

- [ ] **Step 2: Run; FAIL.**

`go test ./cmd/bridge -run TestRewriteLegacy -v`

- [ ] **Step 3: Implement `cmd/bridge/legacy.go`**

```go
package main

import "os"

// legacyForwards maps a single-token first arg (after os.Args[0]) to its
// modern equivalent. Used for `bridge away`, `bridge back`, `bridge auto`.
var legacyVerbForwards = map[string][]string{
    "away": {"presence", "away"},
    "back": {"presence", "back"},
    "auto": {"presence", "auto"},
}

// rewriteLegacyArgs takes the equivalent of os.Args (program name + user args)
// and applies the legacy-flag forwarding rules described in Plan C task 1.
// Pure function — no side effects, easy to test.
func rewriteLegacyArgs(args []string) []string {
    if len(args) < 2 {
        return args
    }
    first := args[1]

    // Leave __preflight alone — it has its own dispatcher that mirrors these
    // rewrites at the directive layer.
    if first == "__preflight" {
        return args
    }
    // Leave known modern verbs alone.
    if knownVerbs[first] {
        return args
    }

    switch first {
    case "-r":
        // bridge -r [trailing flags...] → bridge list -r [trailing flags...]
        out := []string{args[0], "list", "-r"}
        out = append(out, args[2:]...)
        return out
    case "--refresh":
        out := []string{args[0], "list", "--refresh"}
        out = append(out, args[2:]...)
        return out
    case "-D":
        // bridge -D <name> → bridge rm <name> --yes
        if len(args) < 3 {
            // Let cobra surface the error.
            return args
        }
        out := []string{args[0], "rm", args[2], "--yes"}
        out = append(out, args[3:]...)
        return out
    }

    if forward, ok := legacyVerbForwards[first]; ok {
        out := []string{args[0]}
        out = append(out, forward...)
        out = append(out, args[2:]...)
        return out
    }
    return args
}

// rewriteLegacy applies rewriteLegacyArgs to os.Args in-place.
func rewriteLegacy() {
    os.Args = rewriteLegacyArgs(os.Args)
}
```

- [ ] **Step 4: Wire into `main.go`**

Read `cmd/bridge/main.go` first. Replace the function with:
```go
func main() {
    rewriteLegacy()
    rewritePositional()
    if err := rootCmd.Execute(); err != nil {
        fmt.Fprintln(os.Stderr, err)
        os.Exit(1)
    }
}
```

- [ ] **Step 5: Mirror the rewrites in `__preflight`**

In `cmd/bridge/preflight.go::dispatchPreflight`, before the existing branches, apply the same translation when invoked through the shim:

```go
func dispatchPreflight(out io.Writer, args []string) error {
    args = rewriteLegacyPreflight(args)
    // existing logic continues...
}

// rewriteLegacyPreflight is the preflight-side analogue of rewriteLegacyArgs.
// Operates on the args *after* "__preflight" (no program name, no verb).
func rewriteLegacyPreflight(args []string) []string {
    if len(args) == 0 {
        return args
    }
    first := args[0]
    if knownVerbs[first] {
        return args
    }
    switch first {
    case "-r":
        return append([]string{"list", "-r"}, args[1:]...)
    case "--refresh":
        return append([]string{"list", "--refresh"}, args[1:]...)
    case "-D":
        if len(args) < 2 {
            return args
        }
        return append([]string{"rm", args[1], "--yes"}, args[2:]...)
    }
    if forward, ok := legacyVerbForwards[first]; ok {
        out := append([]string{}, forward...)
        return append(out, args[1:]...)
    }
    return args
}
```

Add a test in `legacy_test.go`:
```go
func TestRewriteLegacyPreflightDashR(t *testing.T) {
    got := rewriteLegacyPreflight([]string{"-r"})
    want := []string{"list", "-r"}
    if !reflect.DeepEqual(got, want) {
        t.Errorf("got %v want %v", got, want)
    }
}

func TestRewriteLegacyPreflightAway(t *testing.T) {
    got := rewriteLegacyPreflight([]string{"away"})
    want := []string{"presence", "away"}
    if !reflect.DeepEqual(got, want) {
        t.Errorf("got %v want %v", got, want)
    }
}
```

- [ ] **Step 6: Integration test via the binary**

In `cmd/bridge/legacy_test.go`, also add subprocess tests using `bridgeCmd`:
```go
func TestLegacyDashRListsLocal(t *testing.T) {
    root := writeFakeRepos(t)
    cache := t.TempDir()
    cmd := bridgeCmd("-r", "--json")
    cmd.Env = append(envWithout("TMUX"),
        "BRIDGE_REPOS_ROOT="+root,
        "XDG_CACHE_HOME="+cache,
        // Disable real network — empty tokens
        "GH_TOKEN=", "GITLAB_TOKEN=", "FORGEJO_TOKEN=",
        "BRIDGE_GITHUB_API=", "BRIDGE_GITLAB_API=", "BRIDGE_FORGEJO_API=",
    )
    out, err := cmd.CombinedOutput()
    if err != nil {
        t.Fatalf("run: %v\n%s", err, out)
    }
    // Output should be {local:[...], remote:[...]}, NOT [...]
    if !strings.Contains(string(out), `"local"`) {
        t.Errorf("expected -r to take list -r shape (with `local` key), got: %s", out)
    }
}
```

(Reuse `envWithout` from `preflight_test.go`.)

- [ ] **Step 7: `go test ./...` PASS. Commit.**

```bash
git add cmd/bridge/legacy.go cmd/bridge/legacy_test.go cmd/bridge/main.go cmd/bridge/preflight.go
git commit -m "feat(go): legacy flag forwarding (-r, --refresh, -D, away/back/auto)"
```

---

## Task 2: Install path — rename `bridge-go` → `bridge`

**Files:**
- Modify: `Makefile` (`install-go` target now installs as `bridge`)
- Modify: `shims/bridge-shim.sh` (`command bridge-go` → `command bridge`)
- Modify: `shims/bridge-shim.bats` (build target renamed too)
- Modify: `shims/bridge-shim.ps1` (`bridge-go.exe` → `bridge.exe`)

Why this is safe BEFORE editing .bashrc: as long as the bash `bridge` script is still sourced as a function, the function shadows any binary named `bridge` on PATH in the interactive shell. So installing the Go binary as `~/.local/bin/bridge` while bash bridge is still active just means the binary is reachable via `command bridge` from inside the bash function (which it never actually uses) — harmless.

- [ ] **Step 1: Modify Makefile**

Replace:
```make
install-go: build-go
	install -m 0755 bridge-go $(HOME)/.local/bin/bridge-go
```
with:
```make
# install-go installs the Go binary as `bridge` on PATH. The bash bridge()
# function (still sourced from ~/.bashrc) shadows this in interactive shells
# until `make activate-shim` flips ~/.bashrc to use the shim instead.
install-go: build-go
	install -m 0755 bridge-go $(HOME)/.local/bin/bridge
```

- [ ] **Step 2: Modify `shims/bridge-shim.sh`**

```sh
bridge() {
    local directive rc
    directive=$(command bridge __preflight "$@")
    rc=$?
    ...
    case "$directive" in
        cd:*)   cd "${directive#cd:}" ;;
        exec:*) eval "exec ${directive#exec:}" ;;
        noop)   command bridge "$@" ;;
        ...
    esac
}
```

**However** there's a recursion hazard: if the function is named `bridge` and the binary is named `bridge`, then `command bridge` inside the function correctly calls the binary (because `command` bypasses functions/aliases). Confirmed safe.

- [ ] **Step 3: Modify `shims/bridge-shim.bats`**

Replace all `"$BRIDGE_TEST_DIR/bridge-go"` with `"$BRIDGE_TEST_DIR/bridge"` and the `go build -o "$BRIDGE_TEST_DIR/bridge-go"` line similarly. The bats `@test` cases stay the same.

- [ ] **Step 4: Modify `shims/bridge-shim.ps1`**

Replace `bridge-go.exe` with `bridge.exe`.

- [ ] **Step 5: Build, install, verify**

```bash
make build-go install-go
ls -l ~/.local/bin/bridge ~/.local/bin/bridge-go 2>&1
# After this point, ~/.local/bin/bridge-go may still exist as a stale leftover
# from the previous install. Remove it to avoid confusion:
rm -f ~/.local/bin/bridge-go
```

Verify the bash bridge function is STILL the thing typing `bridge` invokes (because it's a sourced function in the interactive shell):
```bash
type bridge | head -1   # → "bridge is a function"
command -v bridge       # → /home/freax/.local/bin/bridge (the binary path)
```

- [ ] **Step 6: bats**

```bash
bats shims/bridge-shim.bats
```
2/3 → 3/3 expected. (No tests change shape; only the build artifact name.)

- [ ] **Step 7: Commit**

```bash
git add Makefile shims/bridge-shim.sh shims/bridge-shim.bats shims/bridge-shim.ps1
git commit -m "chore(cutover): install Go binary as bridge; update shims to match"
```

---

## Task 3: USER ACTION — install shim, flip ~/.bashrc

**This task is interactive. Stop and confirm with the user before executing any step.** No code changes. Documents the user's manual cutover steps; the agent can perform them only with explicit per-step authorization.

- [ ] **Step 1: Back up `~/.bashrc`**

```bash
cp ~/.bashrc ~/.bashrc.bak-bridge-cutover-$(date -u +%Y%m%d-%H%M%S)
ls -la ~/.bashrc.bak-bridge-cutover-*
```

- [ ] **Step 2: Install the latest shim**

```bash
make install-shim
ls -la ~/.local/share/bridge/bridge-shim.sh
```

- [ ] **Step 3: Edit `~/.bashrc` line 29**

Current (verified on 2026-05-26):
```
_f=~/projects/repos/github/freaxnx01/public/bridge/bridge.sh; [ -f "$_f" ] && . "$_f"; unset _f
```

Replace with:
```
_f=~/.local/share/bridge/bridge-shim.sh; [ -f "$_f" ] && . "$_f"; unset _f
```

Preserves the conditional-source pattern (if the shim file is missing, the line is a no-op — fail-safe to a `bridge` that resolves to the binary directly).

- [ ] **Step 4: Open a NEW shell (do not touch the current one) and verify**

```bash
# In a new terminal (or `bash -l` subshell), check:
type bridge | head -3    # → "bridge is a function" (the shim function, not bash bridge.sh)
declare -f bridge | head # → should show the shim body (case directive in...)
bridge --version         # → the Go binary's version string
bridge list | head -3    # → Go-binary list output
bridge bridge            # → should `cd` into the bridge repo (parent shell cwd changes)
pwd                      # → confirms cd worked
```

If anything fails: in that shell, `source ~/.bashrc.bak-bridge-cutover-*` to revert.

- [ ] **Step 5: Exercise legacy forwards in the new shell**

```bash
bridge -r --json | head -5         # → JSON with `local` and `remote` keys
bridge away                        # → presence write; silent
bridge presence                    # → "mode: away"
bridge back
bridge -D non-existent-repo 2>&1   # → exit 2, "unknown repo"
```

- [ ] **Step 6: VERIFY WITH USER before continuing.** Cutover is now active. If the user reports any breakage, revert via Step 4's fallback before proceeding to Task 4.

---

## Task 4: Sunset `_BRIDGE_VERSION` rule

**Files:**
- Modify: `CLAUDE.md` (project-local)

The CLAUDE.md rule says "When committing any change to `bridge.sh`, bump `_BRIDGE_VERSION`." With Phase 3 complete, `bridge.sh` is no longer sourced; future bug fixes land in Go. Keep the rule until the user confirms Task 3 succeeded.

- [ ] **Step 1: Replace the rule in `CLAUDE.md`**

Read the file first. Replace:
```
- When committing any change to `bridge.sh`, bump `_BRIDGE_VERSION` (defined near the top of the file) according to semver: patch for fixes, minor for new features, major for breaking changes.
- Whenever `_BRIDGE_VERSION` is bumped, add a matching entry to `CHANGELOG.md` (Keep a Changelog format) in the same commit, with the new version, today's date, and a section (`Added` / `Changed` / `Fixed`) describing the change.
```
with:
```
- `bridge.sh` and the other bash scripts (`bridge-watcher.sh`, `bridge-autosync.sh`, `bridge-unpushed-warn.sh`) are frozen as of Phase 3 cutover (v2.0.0). Do not edit them — fixes land in the Go binary. The `_BRIDGE_VERSION` rule is retired.
- For Go changes, bump the `v2.0.0-go.N` series via `git tag` when shipping; CHANGELOG.md entries describe the user-visible changes per release.
```

- [ ] **Step 2: Commit**

```bash
git add CLAUDE.md
git commit -m "docs(cutover): sunset _BRIDGE_VERSION rule; bash scripts frozen"
```

---

## Task 5: CHANGELOG entry for v2.0.0

**Files:**
- Modify: `CHANGELOG.md`

- [ ] **Step 1: Add v2.0.0 entry at the top**

Read the existing file. Prepend:
```markdown
## [v2.0.0] — 2026-05-26

### Changed
- Complete Go-binary rewrite (`cmd/bridge`) replaces the ~3,600-line `bridge.sh`.
  All read paths (`list`, `slots`, `sessions`, `presence`, `sync`, `status`,
  `issues`) ship from Plan A; interactive paths (`open`, `rm`, presence writes,
  `sync now`, `sync --auto`, `watch`, `sessions attach`) plus tmux/WT launcher
  and shell shim ship from Plan B/B.1.
- `~/.bashrc` now sources `~/.local/share/bridge/bridge-shim.sh` instead of
  `bridge.sh`. The shim invokes the Go binary via the `__preflight` directive
  protocol and acts on `cd:` / `exec:` / `noop` responses.
- `bridge --status` decomposed into slim `bridge status` plus focused
  `bridge sessions` / `bridge slots` / `bridge presence` / `bridge sync` verbs.
  Each supports `--json`.
- Legacy flags `-r`, `--refresh`, `-D`, and bare `away|back|auto` are silently
  forwarded to the new verbs inside the binary. Muscle memory preserved.

### Added
- `bridge issues` — open issues across forges, with TTL cache.
- `bridge tui` — reserved verb stub for a future dashboard spec.
- Cross-platform support: Linux + Windows from one codebase. tmux launcher on
  Linux, Windows Terminal launcher on Windows.
- `--json` shape documented in `docs/cli-json-schema.md`.
- Structured logging (`log/slog` + JSON-lines `bridge.log` with rotation) for
  long-running `sync --auto` and `watch` daemons.

### Frozen / removed
- `bridge.sh`, `bridge-watcher.sh`, `bridge-autosync.sh`, `bridge-unpushed-warn.sh`
  remain in the repo for one release cycle but are no longer sourced or run.
  They will be deleted in a follow-up PR (Phase 4).
- `_BRIDGE_VERSION` retired. Go releases tagged `v2.0.0-go.N`.

### Migration notes
- Cache directory `~/.cache/bridge/` is shared between the old bash bridge and
  the Go binary. `slots.json` written by bash continues to be readable by Go;
  `slots.json` written by Go uses an array-shaped `slots` field.
- Bash-only files (`hooks.log`, `hooks.lock`, `meta-warm.lock`, `.channels-hinted`,
  `sessions/`, `watcher.pid`) remain on disk; Go does not read or write them.
  These will be cleaned up at Phase 4.
```

- [ ] **Step 2: Commit**

```bash
git add CHANGELOG.md
git commit -m "docs(cutover): CHANGELOG entry for v2.0.0"
```

---

## Task 6: Tag `v2.0.0`

**Files:** none.

- [ ] **Step 1: Verify state**

```bash
go test ./...
bats shims/bridge-shim.bats
git log --oneline -10
```

All green; recent commits match the cutover plan.

- [ ] **Step 2: Tag**

```bash
git tag -a v2.0.0 -m "v2.0.0 — Phase 3 cutover: Go binary is primary; bash bridge frozen"
git tag --list 'v2.0.0*'
```

- [ ] **Step 3: VERIFY WITH USER before pushing.**

```bash
git push origin main
git push origin v2.0.0
```

---

## Task 7: VERIFY WITH USER — final smoke against real workflow

No file changes. Spend a day or two using the new `bridge` for normal work and confirm:
- Picker (`bridge` no-arg) shows the repo list and lands you in the chosen dir
- `bridge <name>` for muscle-memory repos
- `bridge -r` for remote listings
- Slot management as you spawn agents
- Presence transitions when stepping away
- `bridge status` reads sensibly throughout

If anything misbehaves, capture the symptom and roll back via Task 3's backup. Open an issue / spec for the gap.

---

## Phase 4 (deferred, separate PR)

NOT part of Plan C. After v2.0.0 has been live for a release cycle without reported issues:

- Delete `bridge.sh`, `bridge-watcher.sh`, `bridge-autosync.sh`, `bridge-unpushed-warn.sh`, `setup-claude-channels.sh` from the repo.
- Drop the read-compat shim for bash-format `slots.json` (only relevant during overlap).
- Remove unused bash-only files from `~/.cache/bridge/` via a one-shot `bridge __cleanup-bash-cache` subcommand.
- Tag `v2.1.0`.

---

## Spec coverage check

| Spec requirement | Task |
|---|---|
| Legacy flag silent forwarding (`-r`, `-D`, `--refresh`, `away` positional) | 1 |
| Binary renamed/installed as `bridge` | 2 |
| Shim invokes `command bridge` | 2 |
| `~/.bashrc` flipped from `bridge.sh` to `bridge-shim.sh` | 3 |
| `_BRIDGE_VERSION` retired | 4 |
| CHANGELOG entry | 5 |
| Tag `v2.0.0` | 6 |
| Phase 4 cleanup | deferred (out of scope) |

## Risk register

| Risk | Mitigation |
|---|---|
| Shim breaks user's interactive shell | Backup `.bashrc` before edit; new-shell smoke test before declaring done |
| Go binary missing or broken at cutover | `_f=path; [ -f "$_f" ] && . "$_f"` pattern means missing file is a no-op; user still has bash bridge in old shells |
| Legacy forwarding mistranslates a corner case | Task 1 has unit + integration tests; if a real edge case surfaces, ship a fix on a follow-up commit before pushing v2.0.0 |
| `slots.json` written by Go is then read by an old bash bridge in a long-lived shell | Bash reader tolerates extra fields; the Go-format array shape under `slots` would break bash's reader. Mitigation: user closes old shells after cutover. Documented in CHANGELOG migration notes. |
| `_BRIDGE_VERSION` rule retirement is premature | Rule retirement is in Task 4, AFTER user confirms Task 3 success |
