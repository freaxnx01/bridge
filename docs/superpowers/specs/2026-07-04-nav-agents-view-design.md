# Agents view — live Claude sessions across repos (#170)

## Problem

Claude Code can run many concurrent sessions across different repos/worktrees, but
there is no single place to see them. Claude's own "Agent View" (`claude agents`)
lists them, yet it requires an interactive terminal and is not surfaced anywhere in
`bridge`. Issue #170 asks bridge to wrap `claude agents --json` and present all live
Claude sessions — status, repo assignment — in the nav TUI **and** the WebUI, as a
read-only status pane.

This aligns with `docs/ai-notes/agent-view-analysis.md` (tracking #131), which names
`claude agents --json` as the "most promising near-term integration" and frames bridge
as a **pull-style reporter** of Claude's session state — explicitly *without*
re-implementing session transcripts or steering.

## Goal (success criterion)

From bridge, the user can see every live Claude Code session across all repos in one
list — its name, status, kind, repo (from `cwd`), and age — on both the nav TUI (a new
screen) and the WebUI (a new section). The listing is read-only and refreshes on
demand (TUI) / on the existing broadcast tick (WebUI). When `claude` is unavailable,
each surface shows a clear "unavailable" state rather than an error.

## Grounded facts (verified at design time)

`claude agents --json` (Claude Code **2.1.201**) is real and prints a JSON array to
stdout in non-interactive mode. Verified entry shape (union of keys across a live run):

```json
{
  "pid": 1294806,
  "cwd": "/home/freax/repos/github/freaxnx01/public/bridge/.worktrees/work",
  "kind": "interactive",
  "startedAt": 1783094237071,
  "sessionId": "d10d3501-aea7-4019-b95d-845a91aeeeb2",
  "name": "bridge [work]",
  "status": "busy"
}
```

- Fields: `pid` (int), `cwd` (abs path), `kind` (`interactive` observed; `background`
  for `claude --bg` sessions), `startedAt` (**epoch milliseconds**), `sessionId`
  (uuid), `name` (string), `status` (`busy`/`idle` observed).
- **No "last output line" field exists.** The issue mentions one; it is only
  recoverable by reading Claude's private transcript files
  (`~/.claude/projects/<slug>/<sessionId>.jsonl`). Per the analysis doc's steer, this
  is **out of scope** (see Non-goals).
- **Repo assignment** is derived from `cwd`.
- Non-TTY plain `claude agents` (no `--json`) errors and points at `--json`; only the
  `--json` form is machine-readable. Invocation is exactly `claude agents --json`
  (the subcommand takes **zero positional args** — `claude agents list --json` errors).

## Decisions (settled during brainstorming)

- **Surface: both nav TUI and WebUI.** A shared core does the fetch+parse once; each
  surface renders it.
- **Drop "last output line".** Show only fields the JSON provides. Keeps bridge a thin
  read-only wrapper and honors the analysis doc's "don't re-implement transcripts".
- **Show all sessions, labelled by kind.** Interactive is the common case; a
  background-only filter would usually be near-empty. The `kind` is displayed per row.
- **Read-only.** No attach / steer / kill / launch from this view.
- **New TUI screen**, reached by a key from the picker's focusList — the same
  convention as `o`→overview (`internal/nav/update.go:364`). Screen-switch keys live in
  focusList so they never collide with the focused filter input.
- **WebUI: a plain tabbed section, no client router.** Matches the current
  `App.svelte` stub's level; a router is deferred (Non-goals).

## Architecture

### Shared core — new package `internal/agentview`

New package (name nods to Claude's "Agent View"; deliberately distinct from the
existing `internal/agents`, which is only a launch-command registry).

```go
// Session is one live Claude Code session as reported by `claude agents --json`.
type Session struct {
    PID       int
    CWD       string
    Kind      string    // "interactive" | "background"
    SessionID string
    Name      string
    Status    string    // "busy" | "idle" | ...
    StartedAt time.Time // converted from startedAt epoch-ms
}

// Runner runs an external command and returns its stdout. The consumer defines it
// so tests inject a fake without a real `claude` binary (mirrors worktree.Primary).
type Runner interface {
    Run(ctx context.Context, name string, args ...string) ([]byte, error)
}

// ErrUnavailable is returned when the claude CLI is absent or the agents command
// fails — surfaces render an "unavailable" state rather than a hard error.
var ErrUnavailable = errors.New("claude agent view unavailable")

// List returns the live Claude sessions, sorted busy-first then by name. An empty
// array is a valid zero-session result, not an error.
func List(ctx context.Context, run Runner) ([]Session, error)
```

Behavior:
- Execs `claude agents --json`; unmarshals the array into an internal DTO
  (json tags: `pid`, `cwd`, `kind`, `startedAt`, `sessionId`, `name`, `status`) then
  maps to `Session`, converting `startedAt` ms → `time.Time`
  (`time.UnixMilli(startedAt)`).
- Sort: `busy` before others, then by `Name` (stable, deterministic for goldens).
- Errors: a run error (binary missing / non-zero exit) → wrap as `ErrUnavailable`
  (`errors.Is`-checkable). Malformed JSON → a distinct parse error (not
  `ErrUnavailable`), so a genuine format break is visible and not silently hidden.
- Empty JSON array `[]` → `nil, nil` (zero sessions).

A production `Runner` is a trivial `exec.CommandContext(ctx, name, args...).Output()`
wrapper, defined at the call sites (nav / api) or a shared `execRunner` — no
package-level global.

### Display helper — shortened repo label from `cwd`

A small pure helper (in `agentview` or the nav formatter) turns an absolute `cwd`
into a compact repo-ish label for display: replace `$HOME` prefix with `~`, and show
a short tail. Cheap string ops only — **no** per-row git/toplevel exec. Exact form
finalized in the plan; goal is a readable one-line label (e.g.
`~/…/bridge/.worktrees/work`).

### nav TUI — new `screenAgents`

- `internal/nav/types.go` — add `screenAgents` to the `screen` const block.
- `internal/nav/model.go` — add fields mirroring the overview block: `agents
  []agentview.Session`, `agentsSel int`, `agentsState loadState`.
- `internal/nav/update.go`:
  - Entry: in `updatePicker`'s **focusList** switch (near the `"o"` case,
    `update.go:364`), add a key — proposed **`a`** (verify unbound in that switch) —
    that sets `m.screen = screenAgents`, `m.agentsState = loadPending`,
    `m.agentsSel = 0`, and returns `m.loadAgentsCmd()`.
  - Routing: in the top-level key block (`update.go:185-198`), route `screenAgents`
    to a new `updateAgentsKeys(msg)`.
  - `updateAgentsKeys`: `up/k`, `down/j`, `g/G` selection; `r` refresh
    (`loadAgentsCmd`); `esc` → `m.screen = screenPicker`; `q`/`ctrl+c` quit.
  - Result msg: add `agentsMsg` (carrying `[]agentview.Session` + an error/unavailable
    flag), handled in `Update` mirroring `overviewMsg` (`update.go:170-183`), setting
    `agentsState` to loaded/error.
- `internal/nav/data.go` — `loadAgentsCmd() tea.Cmd` runs `agentview.List(ctx,
  execRunner)` off the Update loop and returns `agentsMsg`.
- `internal/nav/view.go`:
  - `View()` (`view.go:57-68`) — add `if m.screen == screenAgents { return
    m.viewAgents() }`.
  - `viewAgents()` renders a titled table using existing `panel()`/`st*` styles:
    columns **status dot · kind · name · status · repo (shortened cwd) · age**
    (humanized from `StartedAt`, reuse the existing age humanizer used by sessions).
    Busy rows use `stOk`, idle `stMuted`. States: loading (spinner/"loading…"),
    empty ("No live Claude sessions."), unavailable ("Claude Agent View unavailable —
    is the `claude` CLI installed?"). Footer hint (`r refresh · esc back`).

### WebUI — `/api/agents`

- `internal/api/agents.go` — `AgentsHandler` for `GET /api/agents`: calls
  `agentview.List(ctx, execRunner)`; on success `writeJSON` the sessions (marshalled
  with lowercase JSON field tags + `startedAt` as epoch-ms or RFC3339 — finalize in
  plan, keep consistent with the store); on `ErrUnavailable` return an empty array
  with 200 (surface renders empty/unavailable), on a real parse error `writeError`.
  Register in the `apiMux` in `cmd/bridge/serve.go:123-127`.
- `cmd/bridge/serve.go:129-141` — the existing ticker also broadcasts
  `web.Event{Type: "agents-updated"}` alongside `overview-updated`.
- `web/src/lib/stores/agents.js` — mirror `web/src/lib/stores/overview.js`:
  `loadAgents()` GETs `/api/agents`; re-fetch when `sseEvent` type is
  `agents-updated`.
- `web/src/App.svelte` — add a minimal section/tab (simple local tab state, no
  router) listing each session: name, status, kind, repo, age. Polish level matches
  the current stub.

## Data flow / interactions

- TUI: picker(`a`) → `loadAgentsCmd` → `agentview.List` (exec `claude agents --json`)
  → `agentsMsg` → `viewAgents`. Independent of the tmux/session concept
  (`core.LiveSessions`) — a separate data source; no changes to existing session rows.
- WebUI: ticker → `agents-updated` SSE → store re-fetch → `GET /api/agents` →
  `agentview.List` → JSON → render.
- The shared `agentview` package is the single fetch+parse point both surfaces call —
  no duplicated exec/parse logic.

## Edge cases

- **`claude` not installed / not on PATH** → `ErrUnavailable`; TUI shows the
  unavailable line, WebUI returns `[]` (empty section). Non-fatal, mirrors how
  `core.LiveSessions` treats a missing `tmux`.
- **Zero sessions** (`[]`) → "No live Claude sessions." (TUI) / empty list (WebUI).
- **Malformed JSON** → distinct parse error (surfaced, not swallowed as
  "unavailable") so a real upstream format change is caught.
- **Unknown `kind`/`status` values** → displayed verbatim (no enum gate); styling
  falls back to a neutral style for unrecognized status.
- **`bridge`'s own session appears in the list** (the session running the TUI) — that
  is correct and expected; not filtered.

## Testing

**Core (`internal/agentview`)** — table-driven with a fake `Runner`:
- valid multi-entry array → parsed, sorted busy-first, epoch-ms → `time.Time` correct.
- empty array `[]` → `nil, nil`.
- runner error (simulate missing binary) → `errors.Is(err, ErrUnavailable)`.
- malformed JSON → non-nil error that is **not** `ErrUnavailable`.
- field mapping: `sessionId`→`SessionID`, `startedAt`→`StartedAt`, etc.

**nav TUI** — Update is a pure function of `(model, msg)`:
- `a` from picker focusList sets `screen == screenAgents` and issues the load cmd.
- `agentsMsg` populates rows / sets loaded state; error flag → unavailable state.
- `esc` returns to picker; `r` re-issues the load cmd; `up`/`down` move selection.
- Golden of `viewAgents` at a fixed width with a fixed fake session set
  (`testdata/agents.golden`), plus a golden/assertion for the empty and unavailable
  states (no ANSI).

**WebUI API (`internal/api`)** — `AgentsHandler` with a fake `Runner`:
- success → 200 + JSON body with the expected fields.
- unavailable → 200 + `[]`.
- (Frontend store/section: exercised via `npm run build` succeeding + a manual smoke;
  no JS unit harness exists in the repo today.)

**Gates:** `gofmt -l .` empty, `go vet ./...` clean, `golangci-lint run` clean,
`go test -race ./...` green, and `npm run build` (web) succeeds so the embedded `dist`
stays current.

## Acceptance Criteria

- [ ] `internal/agentview.List` wraps `claude agents --json`, returns typed `Session`s
      (epoch-ms → `time.Time`), sorted deterministically; `ErrUnavailable` on a missing
      / failing CLI; empty array → zero sessions; malformed JSON → a distinct error.
- [ ] nav TUI: `a` from the picker opens a new Agents screen listing every live
      session (status dot, kind, name, status, repo-from-cwd, age); `esc` back, `r`
      refresh; empty and unavailable states render clearly.
- [ ] WebUI: `GET /api/agents` returns the sessions as JSON; the ticker broadcasts
      `agents-updated`; `App.svelte` shows an Agents section that refreshes on that SSE
      event; unavailable → empty section, not an error.
- [ ] All sessions shown regardless of `kind`, with `kind` labelled per row; the view
      is read-only (no attach/steer/kill).
- [ ] Tests: `agentview` table tests (fake Runner), nav Update + golden tests, api
      handler test — all green under `-race`; `npm run build` succeeds.
- [ ] `gofmt -l .` empty, `go vet ./...` clean, `golangci-lint run` clean.

## Non-goals

- **Last-output-line / transcript reading** (`~/.claude/projects/.../*.jsonl`) — the
  JSON doesn't expose it; deferred per the analysis-doc steer. A possible later
  follow-up.
- **The Radar agent-ping animation** (`docs/design/bridge-poc2.html`) — that is
  issue **#171**, which depends on this panel existing.
- **Attach / steer / kill / launch** from the view — read-only only.
- **A WebUI client-side router** — a plain tabbed section for now.
- **Merging with the tmux `core.LiveSessions` session model** — this is a separate,
  additive data source; existing session dots/rows are untouched.
- **Cross-platform guarantees beyond what `claude` itself provides** — bridge just
  execs the CLI.

## Sequencing note

The #157 status-glyph legend (PR #187) is **not on `main`** yet. This view adds a
status-dot/kind styling that is legend-worthy. When both land, add the Agents view's
glyphs to `legendEntries` and its expected set (same pattern #157 used for the `★`
base-checkout glyph from #182). If #157 has already merged when this is implemented,
include the entries from the start.

## Open questions / follow-ups

- Exact `startedAt` wire format for `/api/agents` (epoch-ms vs RFC3339) — pin in the
  plan; keep the store and handler consistent.
- Whether to also reach the Agents screen from the dashboard (overview is picker-only
  today; matching that is the default). Decide in the plan; picker-only is acceptable.
