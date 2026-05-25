# bridge `--status` — unified session overview — design

**Date:** 2026-05-18
**Component:** `bridge.sh` — rewrite of `_bridge_slot_status`, extension of `_bridge_tmux_session_defaults`, deprecation of `_bridge_slot_status_rc`
**Issue:** [#1](https://github.com/freaxnx01/bridge/issues/1)
**Followup blocked by this:** [#2](https://github.com/freaxnx01/bridge/issues/2)

## Problem

`bridge --status` today only lists bridge-managed *slots* (`s0..sN`), which correspond to Telegram channel/bot sessions. Claude sessions started in other bridge modes are invisible:

- `--no-channel` sessions (no slot, no Telegram, shared `~/.claude`), which may still run in tmux when started over SSH.
- Remote Control sessions exposing a `bridgeSessionId`, surfaced only via the separate `--status-rc` command — and only for slot-bound rows.

This makes `--status` misleading as a "what's running" overview: it answers *which Telegram slots are occupied*, not *which Claude sessions are active on this host*. It also forces users to remember which command surfaces which fact (`--status` vs `--status-rc`).

## Goal

`bridge --status` becomes the single overview of all bridge-managed Claude sessions on this host — slot or not, RC-active or not, tmux or foreground. Issue #2 (interactive connect mode) is then a thin layer on top of the unified row set.

## Non-goals

- **No broader discovery.** Hand-rolled tmux sessions running `claude`/`opencode` outside bridge are explicitly *not* surfaced. Discovery is scoped to sessions bridge itself created. This is the conservative choice locked in during brainstorming; broader scans (pane-cmd heuristics) are out of scope.
- **No new persistent state.** No sidecar registry file alongside `slots.json`. Non-slot sessions are discovered via tmux user-options set at session-creation time.
- **No `--attach`-style picker.** That is issue #2 and depends on this change landing first.
- **No backfill of pre-existing tmux sessions.** Sessions launched by an older bridge version don't carry the tags; they remain invisible to the new code path. Acceptable — they were invisible to `--status` before this change too.
- **No removal of `--status-rc` in this change.** It becomes a deprecated alias for one release; outright removal is a follow-up.
- **No bot-token column.** The current `--status` shows a `✓/—` token-availability mark. It's configuration state, not session state — surfaced today by `--doctor`. The new table drops it.

## Design

### Discovery: tmux user-option tagging

At every site where `bridge` creates a tmux session, we set tmux user-options on that session immediately after `tmux new-session`. The set is centralised in `_bridge_tmux_session_defaults` (`bridge.sh:1745`), which is already called from every create site, so this is a single function edit plus an argument-passing pass at the call sites.

New `_bridge_tmux_session_defaults` signature:

```sh
_bridge_tmux_session_defaults <session> <repo> <worktree> <kind> <slot> <pid>
```

Where:

- `session` — tmux session name (existing arg).
- `repo` — repo basename.
- `worktree` — worktree name or empty string.
- `kind` — one of `slot`, `no-channel`, `code`, `copilot`, `opencode`.
- `slot` — slot number for `slot` kind; empty otherwise.
- `pid` — pid of the in-pane process, read once via `tmux display-message -t <session> -p '#{pane_pid}'` after the session has spawned. This is the same mechanism the slot path already uses (`bridge.sh:1962`), so synthetic tmux rows and slot rows carry the same kind of pid — RC lookup behaves identically across both.

The function sets the corresponding user-options:

```
@bridge-repo       <repo>
@bridge-worktree   <worktree>
@bridge-kind       <kind>
@bridge-slot       <slot>
@bridge-pid        <pid>
```

Plus the existing `mouse on` / `history-limit 50000` options. All option writes are scoped with `-t <session>` so they never touch unrelated tmux sessions.

Call sites updated (current line numbers in parentheses, approximate):

| Site | Kind |
|---|---|
| VS Code path (`bridge.sh:1851`) | `code` |
| opencode path (`bridge.sh:1882`) | `opencode` |
| `--no-channel` path (`bridge.sh:1904`) | `no-channel` |
| Slot path (`bridge.sh:1937`) | `slot` |

All four already invoke `_bridge_tmux_session_defaults "$session"` — we extend each invocation with the additional args. Call sites that don't spawn tmux (foreground modes) are unaffected.

### Enumeration

`_bridge_slot_status` produces its row list from the **union** of two sources:

1. **`slots.json` rows** — current behavior, post `_bridge_reconcile_slots`. Each row carries `slot`, `repo`, `worktree`, `pid`, `started_at`, `session` (tmux name or empty).
2. **Tmux-tagged sessions** — every `tmux list-sessions` entry where `@bridge-repo` is set.

Enumeration command:

```sh
tmux list-sessions -F \
  '#{session_name}	#{session_created}	#{@bridge-repo}	#{@bridge-worktree}	#{@bridge-kind}	#{@bridge-slot}	#{@bridge-pid}'
```

Tab-separated for parsing safety. Rows with empty `@bridge-repo` are dropped (untagged or non-bridge sessions).

### Dedup

If a tmux-tagged session's name matches a slot row's `.session` field, we already have it from `slots.json` and the slot row wins (it carries richer metadata: slot number, bot name). The tmux duplicate is dropped.

Surviving tmux-tagged rows become **synthetic rows** with:

- `slot = —`
- `kind` from `@bridge-kind`
- `repo / worktree` from the tags
- `pid` from `@bridge-pid`
- `started_at` from `session_created` (epoch seconds, native tmux field)
- `bot = —`
- `tmux = <session_name>`

### Reconcile

No new logic. Existing `_bridge_reconcile_slots` continues to prune dead `slots.json` rows. Tmux-tagged synthetic rows don't need pruning: if the tmux session is gone, `tmux list-sessions` doesn't return it.

### Output format

A single table followed by an optional URLs footer. Columns:

```
SLOT  KIND        REPO                 STARTED        PID      TMUX                 BOT                          RC
```

- `SLOT`: `s0..sN` for slot rows; `—` for synthetic tmux rows.
- `KIND`: `slot`, `no-channel`, `code`, `copilot`, `opencode`.
- `REPO`: repo basename, with ` [worktree]` suffix when a worktree is set (consistent with current `--status-rc`).
- `STARTED`: `{h}h{mm}m ago` (existing format), `—` for empty slots.
- `PID`: integer, `—` for empty slots.
- `TMUX`: tmux session name, `—` for foreground sessions.
- `BOT`: `@claude_freax_s<N>_bot` for slot rows, `(admin bot)` for slot 0, `—` for non-slot rows.
- `RC`: `✓` if a `bridgeSessionId` is present for this row's pid (resolved as below), `—` otherwise.

RC lookup uses the same logic as today's `_bridge_slot_status_rc`: for slot rows, read `~/.claude-s<N>/sessions/<pid>.json` and pick up `bridgeSessionId`. For synthetic `no-channel` rows, read `~/.claude/sessions/<pid>.json` (their shared config dir). For `code`/`opencode` rows, no Claude session file exists, so RC stays `—`.

Sort order: numbered slots `0..MAX` first (occupied or not), then synthetic tmux rows by `started_at` descending (newest first).

Footer:

```
Remote Control URLs:
  s0          https://claude.ai/code/<bridge-id>
  bar [feat]  https://claude.ai/code/<bridge-id>
```

- Omitted entirely when no row has `RC = ✓`.
- Label column shows the slot id for slot rows; the `REPO` value (including any worktree suffix) for synthetic rows.

Example full output:

```
SLOT  KIND        REPO                 STARTED        PID      TMUX                 BOT                          RC
s0    slot        bridge               1h05m ago      12345    bridge               (admin bot)                  ✓
s1    slot        foo                  2h00m ago      12346    foo                  @claude_freax_s1_bot         —
s2    slot        —                    —              —        —                    @claude_freax_s2_bot         —
—     no-channel  bar [feat]           15m ago        12347    bar-feat             —                            ✓

Remote Control URLs:
  s0          https://claude.ai/code/abcd1234...
  bar [feat]  https://claude.ai/code/efgh5678...
```

### `--status-rc` fate

Becomes a thin alias:

```sh
_bridge_slot_status_rc() {
  echo "bridge: --status-rc is deprecated; use --status (RC URLs now shown in the footer)" >&2
  _bridge_slot_status
}
```

- Help text drops the dedicated `--status-rc` line.
- Tab-completion keeps `--status-rc` so users typing it land on the alias without surprise.
- Removal is a follow-up commit at least one minor release later, with its own CHANGELOG entry.

### Versioning & changelog

Per repo convention (`CLAUDE.md`):

- Bump `_BRIDGE_VERSION` minor (new feature).
- Add a `CHANGELOG.md` entry under today's date with `Added` (unified `--status` overview), `Changed` (`--status` now includes non-slot tmux sessions and RC info), and `Deprecated` (`--status-rc`).

## Implementation locations

All edits land in `bridge.sh`:

- `_bridge_tmux_session_defaults` — signature change + new option writes.
- Four call sites that invoke the helper — pass the new args.
- `_bridge_slot_status` — full rewrite for the unified table + footer.
- `_bridge_slot_status_rc` — collapse to alias.
- Help text and `--flags` completion list.

Plus `CHANGELOG.md` + version bump.

## Verification

No automated test suite exists. Manual verification matrix (the implementation plan will turn these into a checklist):

1. **Coverage** — start one slot session, one `--no-channel` SSH session (so it runs in tmux), one hand-rolled tmux session running `claude` outside bridge. `bridge --status` shows exactly the first two; the hand-rolled one stays invisible.
2. **RC merge** — one RC-active session, one without. RC column reads `✓` for the active row and `—` for the other; footer lists only the active session's URL.
3. **Worktree rendering** — start a session with `-w feat`. The row's REPO shows `repo [feat]`; footer label matches.
4. **Out-of-band cleanup** — `tmux kill-session -t <name>` on a synthetic tmux row, then re-run `--status`. Row disappears, no errors, no orphan in slot table.
5. **Empty state** — no sessions running. All slots show `—`; no footer; exit 0.
6. **`--status-rc` alias** — runs, prints the deprecation line on stderr, body identical to `--status`.
7. **Foreground-mode slot** — start a slot session not over SSH (foreground, no tmux). Row appears with `TMUX=—`; RC lookup still works.
8. **Old-version compatibility** — manually launch a `--no-channel` SSH session via the *pre-change* `bridge.sh`, switch to the new binary, run `--status`. Row is invisible (no tags). Acceptable per non-goals; documented in CHANGELOG.

## Open questions

None at design time. Issue #2 (interactive connect mode) is a separate spec and will reuse the row schema defined here.
