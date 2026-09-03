# `bridge nav` — Herdr mode — Design Spec (v1)

Status: **approved design, pending plan** · Date: 2026-09-03

Adds a second session backend to `bridge nav` so that, when nav runs inside a
Herdr session, it launches coding agents as **Herdr tabs** instead of wrapping
them in tmux — making them first-class, Herdr-recognized agents rather than
opaque panes running tmux.

Source issue: none yet (design-first; file before implementation).

## Problem

`nav.launchPlan` (`internal/nav/update.go:871`) always builds a tmux argv via
`launcher.New()` and runs it through `tea.ExecProcess`
(`execArgvCmd`, `internal/nav/update.go:994`), which hands nav's terminal over
to `tmux new-session -A`.

Run under Herdr, that is not merely a redundant layer. Herdr recognizes coding
agents *per pane*; a pane running tmux presents **one process** to Herdr, not
the N agents inside it. So every session bridge starts is invisible to:

- `herdr agent list` — the agents never appear

- the `idle` / `working` / `blocked` / `done` lifecycle — no state is tracked

- Herdr notifications — a blocked agent never raises one

- `herdr agent prompt` / `read` / `send-keys` — no addressable target

Session **discovery** has the mirror-image problem: `core.LiveSessions()`
(`internal/core/session.go:63`) shells out to `tmux list-sessions`, so nav's
dashboard can only ever show tmux sessions. Under Herdr it shows nothing.

## Goal / success criterion

With `HERDR_ENV=1` set, pressing Enter on a dashboard row in `bridge nav`:

1. opens a **new Herdr tab** in the row's directory running the configured
   agent, registered with Herdr as a recognized agent of that kind;
2. **switches focus to that tab**, leaving nav alive and running in its own
   tab (nav is never replaced by `tea.ExecProcess`);
3. is **idempotent** — a row whose session is already live focuses the existing
   tab instead of creating a second one.

And: nav's dashboard and picker session panels show live Herdr agents, with
`blocked` visually distinct from `working`.

With `HERDR_ENV` unset, behaviour is byte-for-byte what it is today.

## Non-goals (this cycle)

- **The shell `bridge open` path.** `cmd/bridge/preflight.go:288-298` keeps
  emitting a tmux `exec:` directive under Herdr. It is a separate entry point
  with a different contract (it emits a directive for the shim to exec, rather
  than acting directly), and folding it in would double the surface of this
  change. Parked as a follow-up.

- **`bridge sessions` / `bridge status` / the WebUI.** `cmd/bridge/sessions.go`,
  `cmd/bridge/status.go` and `internal/api/repos.go:79` all call
  `core.LiveSessions()` directly and stay tmux-only this cycle. Parked.

- **A Herdr-mode e2e tier.** The tier-2 tmux PTY tests
  (`e2e/sessions_test.go`) exercise tmux mode and are unaffected. A Herdr
  equivalent needs an isolated `herdr --session <name>` server; deferred, and
  called out in the PR.

- **Herdr worktree commands.** Herdr ships `herdr worktree create|open|list`,
  but bridge owns worktree creation (`internal/worktree`, `.worktrees/` per
  CLAUDE.md). This cycle does not delegate worktrees to Herdr.

- **Windows.** The Herdr backend compiles everywhere but is only reachable when
  `HERDR_ENV=1`; the `wt.go` Windows Terminal launcher is untouched.

- **Prompting agents from nav.** `herdr agent prompt` / `send-keys` open the
  door to driving agents from the dashboard. Out of scope; nav launches and
  focuses only.

## Design

### Overview

```text
cmd/bridge  ── detection: HERDR_ENV=1, override BRIDGE_LAUNCHER=tmux|herdr
   ├── internal/launcher   (tmux / WT — argv, unchanged)
   └── internal/herdr      (herdr CLI + JSON, new)
              │  injected as nav.Config.Backend
   internal/nav            (backend-agnostic)
```

The herdr backend is **not** behind a build tag. `tmux.go` is `//go:build
!windows` and `wt.go` is `//go:build windows` because those are *platform*
choices; Herdr selection is a *runtime* choice, so the package compiles on
every GOOS and is selected by env.

### The seam: `nav.Backend`

nav defines the interface it consumes, per the Go overlay's *accept interfaces,
define them at the consumer*. In `internal/nav/types.go`:

```go
// Backend is the session backend nav launches into. A nil Config.Backend
// selects the tmux/Windows-Terminal default.
type Backend interface {
    // Launch prepares a launch of spec in dir under the given slot. It must be
    // idempotent: a slot that is already live resolves to the same result as
    // Attach.
    Launch(slot, dir string, spec agents.AgentSpec) (LaunchPlan, error)
    // Attach prepares focusing/attaching the existing session for slot.
    Attach(slot string) (LaunchPlan, error)
    // Live returns the sessions this backend currently has, each carrying the
    // slot id everything downstream matches on.
    Live() ([]core.Session, error)
}
```

`Launch` and `Attach` take exactly the arguments `launcher.Launcher` takes
today, so **nav's existing agent resolution, `NameArgs` labelling and slot-id
derivation in `launchPlan` are untouched** — only `launcher.New()` becomes
`m.cfg.Backend`.

### `LaunchPlan`: exec-or-run without a flag argument

A tmux launch replaces nav's terminal; a Herdr launch does not. That is a sum
type, and Go's idiomatic encoding is a struct with unexported alternatives plus
constructors, so the invariant cannot be broken by a caller:

```go
// LaunchPlan is one prepared launch. Exactly one alternative is set; the two
// constructors are the only way to build a valid value.
type LaunchPlan struct {
    exec []string                    // tmux/WT: hand nav's terminal over
    run  func(context.Context) error // herdr: run out-of-band, nav survives
}

// ExecPlan is a launch that replaces nav's terminal (tmux, Windows Terminal).
func ExecPlan(argv []string) LaunchPlan { return LaunchPlan{exec: argv} }

// RunPlan is a launch performed out-of-band; nav stays on screen.
func RunPlan(fn func(context.Context) error) LaunchPlan { return LaunchPlan{run: fn} }
```

Deliberately **not** `(argv []string, isExec bool, err error)` — a boolean that
switches behaviour is the flag argument CLAUDE.md forbids.

nav runs a plan in one place, replacing `execArgvCmd`:

- `exec` non-empty → today's `tea.ExecProcess` path with `tmuxUnset`
  (`internal/nav/update.go:929`) applied, unchanged.

- `run` non-nil → an ordinary `tea.Cmd` that calls it in a goroutine and
  returns `execDoneMsg{err}`. nav stays rendered; the existing `execDoneMsg`
  handler (`internal/nav/update.go:137`) already refreshes the current screen,
  so the dashboard picks the new session up on return.

### The Herdr backend

New package `internal/herdr`, wrapping the `herdr` CLI (`$HERDR_BIN_PATH`,
falling back to `herdr` on `PATH`). Every command returns JSON on stdout;
responses are decoded into typed structs, never string-matched.

**`Launch(slot, dir, spec)`**

1. `Attach(slot)` first — if an agent is already live for this slot, return
   that plan. This is the `tmux new-session -A` idempotency that Herdr does not
   provide: `herdr tab create` *always* creates, so without this every Enter on
   a live row spawns a duplicate tab.
2. `herdr tab create --cwd <dir> --label <slot> --no-focus` → read
   `.result.root_pane.pane_id`.
3. `herdr agent start <name> --kind <kind> --pane <pane_id> -- <spec.Args...>`.
4. `herdr tab focus <tab_id>`.

Steps 2–4 run inside the returned `RunPlan` closure, not while building it, so
nav can render a `starting <agent> in <repo>…` status while they execute and
the `context.Context` bounds them.

**Agent kind.** `agents.AgentSpec.Name` maps to a Herdr kind. `claude`,
`copilot` and `opencode` are all in Herdr's kind list verbatim. `code` (VS
Code) is **not** an agent — it is a GUI launch that returns immediately — so it
takes `herdr pane run <pane_id> "code ."` instead of `agent start`, with no
`tab focus` (the GUI takes focus itself).

**Agent name.** Herdr names must match `[a-z][a-z0-9_-]{0,31}` and be unique
among live agents. `core.SlotID` (`internal/core/slot.go:27`) satisfies
neither: the local tree contains `BI_ExportSQLiteToCsv`, `Avaloq` and `ASSL`
(uppercase), and `quilvest-archiverestapi-wt-<name>` exceeds 32 characters. So
`agentName(slot, taken []string) string`:

1. lowercase;
2. replace every rune outside `[a-z0-9_-]` with `-`, collapse runs, trim `-`;
3. prefix `a-` if the first rune is not `[a-z]`;
4. truncate to 32;
5. on collision with a live agent name, truncate further and append `-2`,
   `-3`, … keeping the total ≤ 32.

The name is used **only** for the `agent start` call. It is never the identity
key — see discovery below.

**`Attach(slot)`** — `herdr agent list`, find the agent whose `cwd` maps to
`slot`, return a `RunPlan` calling `herdr tab focus <tab_id>`. Returns a
`ErrNoSession` sentinel when nothing matches, which `Launch` treats as "create".

**`Live()`** — `herdr agent list`, then map each agent to a `core.Session`.

### Discovery: cwd → slot id

`herdr agent list` reports `cwd` and `foreground_cwd` per agent (verified
against the live CLI). Everything downstream of `Live()` keys on
`core.Session.SlotID` — `buildDashRows` (`internal/nav/format.go:174`) and
`buildSessionRows` (`internal/nav/data.go:63`) both index by it — so the
backend's whole job is producing a slot id, and **neither builder changes**.

That mapping is a pure function inverting bridge's own directory layout:

```go
// SlotIDForPath maps an agent's working directory to the bridge slot id that
// would have launched it, or "" when the path is not a bridge repo checkout.
//
//   /…/<repo>                      -> "<repo>"
//   /…/<repo>/.worktrees/<wt>      -> "<repo>-wt-<wt>"
func SlotIDForPath(cwd string) string
```

It composes `core.SlotID`, so the two halves cannot drift. Being pure, it is
directly table-testable with no Herdr running.

A worthwhile side effect: an agent started **by hand** in a repo (not through
bridge) now maps to a slot id and appears in nav's picker session panel —
something tmux mode never did. It will not light up a *dashboard* row, because
`buildDashRows` requires a matching record in `slots.json`
(`internal/nav/format.go:190`), exactly as in tmux mode. No regression either
way.

### State vocabulary

Herdr reports **no timestamps** — there is no `LastActivity` equivalent in
`agent list`, `agent get` or `pane list`. It reports something more useful for
this purpose: `agent_status` of `working | idle | blocked | done | unknown`.

`core.Session.State` carries the Herdr status through verbatim. Two dot
switches gain cases:

| `State` | dot | style | meaning |
|---|---|---|---|
| `working` | `●` | ok | agent is running a turn |
| `blocked` | `●` | **warn** | agent is waiting on you |
| `idle`, `done` | `○` | muted | agent is ready for input |
| `unknown` | `·` | muted | agent present, unclassified |
| `attached`, `detached` | unchanged | — | tmux mode |

- `internal/nav/view.go:227` — picker "Active sessions" panel

- `internal/nav/view.go:372` — dashboard worktree list

`Session.LastActivity` stays the zero value in Herdr mode. Consequences,
both accepted:

- The `lastAccessed` column has nothing to show, so in Herdr mode it renders
  the **status word** (`working`, `blocked`, `idle`) instead. No new state
  file, no invented timestamps.

- `sortDashRows` (`internal/nav/format.go:234`) compares `LastActivity`; with
  all values equal it degrades to `sort.SliceStable`'s input order, i.e.
  worktree name. Acceptable. A status-rank sort (`blocked` → `working` →
  `idle`) is a natural follow-up but is not required for this cycle.

Surfacing `blocked` on the dashboard is the real payoff: nav becomes "which of
my agents needs me", which tmux's `attached`/`detached` could never express.

### Wiring and detection

In `cmd/bridge` (nav command construction), alongside the other `nav.Config`
injections:

```go
switch os.Getenv("BRIDGE_LAUNCHER") {
case "tmux":
    cfg.Backend = nil                 // explicit opt-out
case "herdr":
    cfg.Backend = herdr.New()         // explicit opt-in
default:
    if os.Getenv("HERDR_ENV") == "1" {
        cfg.Backend = herdr.New()
    }
}
```

`nav.Config` gains exactly one field:

```go
// Backend is the session backend nav launches into and reads live sessions
// from. Nil selects the tmux/Windows-Terminal default. Injected by cmd/bridge
// so internal/nav stays free of backend selection.
Backend Backend
```

`internal/nav` gains a small default implementation wrapping today's behaviour
(`launcher.New()` + `core.LiveSessions()`), so `Model` never branches on nil:
`initialModel` substitutes it once when `cfg.Backend == nil`.

Call sites changed in nav, all mechanical:

- `internal/nav/update.go:871` `launchPlan` — `launcher.New()` → `m.cfg.Backend`

- `internal/nav/update.go:439` picker attach — same

- `internal/nav/data.go:118` `loadDashRowsCmd` and `internal/nav/data.go:51`
  `loadSessionsCmd` — `core.LiveSessions()` → the backend's `Live()`. Both take
  the backend as a parameter rather than reading a global, keeping them pure
  enough to test.

### Error handling

- **No silent fallback to tmux.** When `HERDR_ENV=1` and a Herdr command fails,
  the error surfaces in nav's status line. Falling back would spawn tmux inside
  Herdr — precisely the bug being fixed.

- **`agent_not_ready` is not a failure.** `herdr agent start` returns it when
  the agent is blocked during startup (e.g. Claude showing a trust prompt for a
  new directory). The agent exists and needs input, so the backend **still
  focuses the tab** and reports success. Any other error leaves focus in nav
  with the message shown.

- **Exit-code discipline.** The Herdr CLI returns JSON on stderr with exit 1
  for server errors and exit 2 for CLI syntax errors. The backend distinguishes
  them: exit 2 is a bridge bug (wrong flags) and is wrapped as such, rather
  than being reported to the user as a Herdr outage.

- Errors wrap with `%w` and flow up to nav's status line; the backend never
  prints or exits, per the Go overlay.

## Testing

Per the Go overlay: table-driven, stdlib only, hand-rolled fakes, green under
`-race`.

**Pure functions (no Herdr required)**

- `SlotIDForPath` — repo root, worktree, nested non-repo path, path outside any
  root, trailing slash, path containing `.worktrees` as a repo name component.

- `agentName` — the real awkward names (`BI_ExportSQLiteToCsv`, `Avaloq`,
  `ASSL`, `quilvest-archiverestapi-wt-featurebranch`), leading-digit input,
  collision suffixing, the 32-char boundary.

- JSON decoding — **golden files in `internal/herdr/testdata/`** captured from
  the live CLI (`agent list`, `tab list`, `pane list`, `tab create`,
  `agent start` success and `agent_not_ready`, both error exit shapes).

**Backend behaviour** — `internal/herdr` takes an injected command runner
(`func(ctx, args...) ([]byte, error)`) so a hand-rolled fake replays the golden
files and records the argv issued. Asserts the create → start → focus sequence,
the attach-before-create idempotency, and the `agent_not_ready` focus-anyway
path.

**nav integration** — a hand-rolled fake `nav.Backend` in `internal/nav`,
driving `Update` directly as the existing tests do. Asserts that a `RunPlan`
does **not** produce a `tea.ExecProcess` (nav survives), that an `ExecPlan`
still does, and that dashboard rows render the new state vocabulary.

**Unchanged** — the tmux tier-2 PTY tests in `e2e/` cover tmux mode and must
stay green; a Herdr e2e tier is out of scope (see Non-goals) and is called out
in the PR.

Gates, as ever: `gofmt -l .` empty, `go vet ./...`, `golangci-lint run`,
`go test -race ./...`.

## Risks

- **Herdr CLI drift.** The backend depends on `herdr`'s JSON field names. The
  skill file states the installed binary is the authority for syntax. Mitigated
  by decoding into typed structs (an added field is ignored, a renamed one
  fails loudly in tests against refreshed golden files) and by keeping every
  CLI call in one package.

- **`agent start` latency.** It blocks until Herdr detects the agent, default
  30 s timeout. Running inside the `RunPlan` closure keeps nav responsive, and
  nav shows a status line for the duration.

- **Slot-id collision across roots.** Two repos with the same basename under
  different roots map to the same slot id. This is pre-existing (`core.SlotID`
  is basename-only, and tmux session names collide identically today), not
  introduced here.

## Follow-ups to file

1. `bridge open` (shell path) under Herdr — `cmd/bridge/preflight.go`
2. `bridge sessions` / `bridge status` under Herdr
3. WebUI / `internal/api/repos.go` session data under Herdr
4. Herdr-mode e2e tier using an isolated `herdr --session` server
5. Status-rank sort for dashboard rows in Herdr mode
