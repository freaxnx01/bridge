# `bridge nav` — launch a session with `claude --resume` — Design Spec (v1)

Status: **approved design, pending plan** · Date: 2026-09-02

Adds a way to launch a **new** session in the nav dashboard that starts
`claude --resume` instead of a plain `claude`, so a tester/user can continue a
previous conversation for a worktree instead of always starting fresh.

Source issue: [#257](https://github.com/freaxnx01/bridge/issues/257).

## Problem

Today `launchPlan` (`internal/nav/update.go:868-899`) only ever builds a plain
`claude` invocation for a worktree with no live session — `agents.Resolve`
(`internal/agents/agents.go`) has no notion of "resume", and there is no key in
the dashboard to request it. A user who closed a Claude Code session (killed
the tmux session, or the process exited) has no way from nav to pick the
conversation back up — they have to `cd` there manually and run
`claude --resume` themselves.

## Goal / success criterion

From the dashboard's worktree list, pressing a dedicated key on a worktree row
that has **no live session** launches `claude --resume` in that worktree
instead of a plain `claude`, using the existing tmux-launch plumbing
(`launcher.LaunchArgv`) unchanged.

## Non-goals (this cycle)

- **Resumable-session detection.** bridge does not know whether a given
  worktree actually has prior Claude Code session history — that's Claude
  Code's own on-disk state (`~/.claude/projects/...`), which bridge does not
  read today (`hasSession`/`slotID` in `dashRow` only ever reflect *live* tmux
  slots — `internal/nav/format.go:192-219`). This cycle wires the flag
  through; it does not try to pre-detect resumability or dim/hide the key when
  there's nothing to resume. If `claude --resume` finds nothing to resume, its
  own behavior (fresh session or its own message) is what the user sees —
  unchanged from running it by hand.
- **Attach-time resume.** A row with a live session (`row.hasSession`) always
  attaches to the running tmux/claude process (unchanged) — resume only
  applies to starting a *new* process, so the key is a no-op there (see below).
- **Other agents.** Only the `claude` agent gets a resume flag. `copilot`,
  `opencode`, `code` are untouched — no evidence any of them share the same
  flag semantics, and the issue only asks for `claude --resume`.
- **`--continue`.** The issue mentions `--resume` *or* `--continue` as
  alternatives; this spec implements `--resume` only (matches the issue title
  and is the flag that lets the user pick which past session to continue,
  rather than always the most recent). Adding `--continue` later is a small,
  separate extension of the same mechanism if wanted.
- **Picker screen, MRU, slot schema** — untouched.

## Design

### Threading a resume flag through the existing launch path

`launchPlan` currently has this shape (`internal/nav/update.go:868-899`):

```go
func (m Model) launchPlan(row dashRow) (argv []string, slot, agent string, err error) {
    l := launcher.New()
    if row.hasSession && row.slotID != "" {
        return l.AttachArgv(row.slotID), "", "", nil
    }
    agent = m.cfg.DefaultAgent
    ...
    spec, err := agents.Resolve(agent)
    ...
    argv, err = l.LaunchArgv(slot, row.path, spec)
    ...
}
```

Add a `resume bool` parameter: `launchPlan(row dashRow, resume bool)`. When
`resume` is true, `agent == "claude"`, and the row has no live session, append
`"--resume"` to `spec.Args` before the existing `NameArgs`/`AgentArgs`
composition (same place those two already extend `spec.Args`, so ordering
stays consistent — name-labeling args first, then user's `AgentArgs`, then
`--resume` last so it's never swallowed by a positional arg some agent config
might add). If the row *has* a live session, `resume` is ignored — attach
takes precedence exactly as it does today, matching the non-goal above.

`launchArgvFor` and `launchRow` both call `launchPlan` — thread the new
parameter through both (`internal/nav/update.go:902-919`), defaulting to
`false` at their one existing call site so today's Enter-to-launch/attach
behavior is byte-for-byte unchanged.

### New keybinding

In `updateDashWorktrees` (`internal/nav/update.go:630-663`), add a case
alongside the existing `"enter"` case:

```go
case "r":
    if m.dashSel < len(m.dashRows) {
        row := m.dashRows[m.dashSel]
        if row.hasSession {
            m.status = "session already attached — resume only applies to a new session"
            return m, nil
        }
        return m.launchRow(row, true)
    }
```

(`launchRow` gains the same `resume bool` parameter, passed straight to
`launchPlan`.) `"r"` is unused in the dashboard's worktree pane today (the
existing `r`/`ctrl+r` refresh binding is picker-screen only — see the picker
hint line in `internal/nav/view.go:300`), so there's no collision.

The "+ create" row (`m.dashSel == len(m.dashRows)`) has no matching `dashRow`;
`r` is a no-op there, same guard shape as `enter`'s existing check.

### Discoverability

Update the dashboard hint line (`internal/nav/view.go:351`) from:

```
↑↓ move · tab panes · ⏎ attach/launch · n new worktree · ? legend · esc back · q quit
```

to:

```
↑↓ move · tab panes · ⏎ attach/launch · r resume · n new worktree · ? legend · esc back · q quit
```

No change to `legendEntries` (`internal/nav/view.go:44` onward) — that table
documents status *glyphs*, not keybindings.

## Alternatives considered

- **Auto-detect resumability and make Enter smart about it.** Rejected as
  more invasive and speculative for this cycle (see Non-goals) — it would
  require bridge to understand Claude Code's on-disk session store, which is
  an internal implementation detail of another tool. A dedicated key that
  always tries `--resume` is simpler, and `claude --resume` already has its
  own sensible behavior when there's nothing to resume.
- **A modal/confirmation before resuming**, mirroring `newWorktreeModal`.
  Rejected — resume has no destructive side effect and no fields to fill in;
  a modal would only add friction to a one-key action.
- **A CLI/env-level flag** (like `BRIDGE_DEFAULT_AGENT_ARGS`) that always adds
  `--resume`. Rejected — that would apply to *every* launch including rows
  that should attach, and doesn't match the request ("continue *a* session" —
  an explicit, per-row choice), whereas a dedicated key matches the existing
  per-row action model (`enter`, `n`).

## Self-review

- **Scope check:** touches `internal/nav/update.go` (two functions gain a
  parameter, one new key case) and one hint-line string in
  `internal/nav/view.go`. No schema, launcher-interface, or picker-screen
  changes.
- **Consistency with existing extension points:** reuses the exact
  `spec.Args` append pattern `NameArgs`/`AgentArgs` already use
  (`internal/nav/update.go:882-888`) rather than inventing a new mechanism.
- **Backward compatibility:** the new parameter defaults to `false` at the
  existing Enter call site, so current launch/attach behavior is unchanged.
- **Testability:** `launchPlan`/`launchRow` are already exercised by
  `internal/nav/*_test.go` as pure argv-building functions (no I/O) — the new
  `resume bool` param is trivially table-driven testable (resume+claude+no
  session → argv has `--resume`; resume+has-session → attach argv unchanged;
  resume+non-claude agent → argv unchanged).
