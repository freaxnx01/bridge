# Design: TERM-fallback for tmux launches under kitty (#104)

## Problem

Launching a session via `bridge` inside the **kitty** terminal on a host
without kitty's terminfo (e.g. Chromebook/Crostini) fails with:

```
missing or unsuitable terminal: xterm-kitty
```

This is *tmux's* error, not bridge's. bridge emits an `exec: tmux
new-session …` directive that the shim runs; when tmux starts it looks up
the terminfo entry named by `$TERM` (`xterm-kitty`), finds nothing on the
host, and aborts. bridge can't install terminfo, but it can fail far more
gracefully.

## Behavior

When bridge is about to emit a tmux launch directive and `$TERM` has **no
terminfo entry** on the host, bridge transparently launches tmux with
`TERM=xterm-256color` and prints a one-line stderr notice. tmux then works;
the only cost is losing kitty-specific terminfo features inside tmux (which
tmux usually strips anyway).

`BRIDGE_NO_TERM_FALLBACK=1` disables the fallback — bridge emits the
unmodified command, reproducing the raw tmux error. Mirrors the existing
`BRIDGE_NO_SYNC` convention.

### Notice copy

```
bridge: TERM="xterm-kitty" has no terminfo entry on this host; launching tmux with TERM=xterm-256color (set BRIDGE_NO_TERM_FALLBACK=1 to disable)
```

## Injection mechanism

Prepend `["env", "TERM=xterm-256color"]` to the launch argv **before**
`shellbridge.EmitExec`. This needs no shim-protocol change and works for all
three argv shapes the tmux launcher produces:

- `new-session`: `env TERM=… tmux new-session …`
- nested `sh -c "…"`: `env TERM=… sh -c "…"` — `env` exports the var into
  the subshell, so the inner `tmux` calls inherit it.
- `attach-session`: `env TERM=… tmux attach-session …`

It also survives both shim exec paths: `eval "exec ${argv}"` →
`exec env TERM=… tmux …`, and the SSH child path `eval "${argv}"`. The
tokens `env` and `TERM=xterm-256color` are all "safe" characters, so
`EmitExec`'s shell-quoting passes them through untouched.

## Detection

`infocmp <term>` (ncurses; ships wherever terminfo does). Exit 0 → the term
resolves → no fallback. Non-zero → unresolved → fall back. This matches
exactly what tmux itself checks.

- Skip the check entirely when `$TERM` is unset or already
  `xterm-256color` (nothing to fix).
- **If `infocmp` is not on `PATH`, treat the term as resolved → no
  fallback.** We can't prove the term is broken, so preserve current
  behavior rather than risk a wrong fallback on a working setup.

## Components

- `cmd/bridge/termfallback_unix.go` (`//go:build !windows`):
  - `maybeTermFallback(stderr io.Writer, argv []string) []string` — returns
    `argv` unchanged when: argv empty, `$TERM` unset or `xterm-256color`,
    `BRIDGE_NO_TERM_FALLBACK` set, or the term resolves. Otherwise prints
    the notice to `stderr` and returns
    `append([]string{"env", "TERM=xterm-256color"}, argv...)`.
  - `termResolver func(string) bool` — package var, default runs `infocmp`
    (with the not-on-PATH → true rule above). Stubbed in tests.
- `cmd/bridge/termfallback_windows.go` (`//go:build windows`): no-op
  returning `argv` unchanged. Windows uses the `wt.exe` launcher, which has
  no terminfo concept.
- `emitLaunch(out io.Writer, argv []string) error` in `preflight.go` =
  `shellbridge.EmitExec(out, maybeTermFallback(os.Stderr, argv))`. Swapped
  in at the 4 launch sites: `preflightPicker`, `preflightPickerWithRemote`,
  `preflightOpen`, `preflightSessionsAttach`.

## Cross-shell parity

- The directive protocol is unchanged, so neither shim (`bridge-shim.sh` /
  `bridge-shim.ps1`) nor `bridge-shim.bats` needs edits.
- The fallback is Unix-only by build tag; the Windows `wt.exe` path is a
  no-op pass-through. This is a legitimate non-parity feature (it relies on
  tmux/terminfo), degrading gracefully on Windows rather than crashing.

## Testing

Unit-test `maybeTermFallback` with a stubbed `termResolver` (no subprocess):

- `$TERM` unset → argv unchanged.
- `$TERM=xterm-256color` → argv unchanged.
- `BRIDGE_NO_TERM_FALLBACK=1` (with unresolved term) → argv unchanged.
- resolver returns true → argv unchanged (passthrough).
- resolver returns false → argv prefixed with `env TERM=xterm-256color`,
  and the notice is asserted on the captured stderr.

## Docs

- `docs/local-cc-session.md`: update the #104 entry in "Current state".
- Setup guide / README: add a kitty + missing-terminfo troubleshooting note
  (now auto-handled; mention the disable var).
- `CHANGELOG.md`: user-visible entry.

## Out of scope

- Installing terminfo on the host.
- Making the fallback target configurable (YAGNI; `xterm-256color` is the
  universal safe choice — add a knob later only if needed).
- The TUI-clipping bug (#103) and the cross-base dashboard limitation are
  tracked separately.
