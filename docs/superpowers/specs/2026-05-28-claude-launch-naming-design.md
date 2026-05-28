# Name the claude session at launch (`-n`), worktree-aware

**Date:** 2026-05-28
**Status:** Draft — pending user approval
**Related:** old bash `bridge.sh` (deleted in #35, commit `ef53cf4`); auto-launch (#77), default-agent init (#82)

## Problem

In the old bash bridge, opening a repo named the launched Claude Code
session after the repo, so it was identifiable in Claude's session picker
and the terminal title:

```bash
# bridge.sh
display_name="$repo"
[ -n "$worktree" ] && display_name="$repo [$worktree]"
...
claude_args=(-n "$display_name" --dangerously-skip-permissions)
```

`claude -n, --name <name>` is a real flag: *"Set a display name for this
session (picker, and terminal title)."*

The Phase-4 Go cutover dropped this. Today the launcher emits:

```
exec:tmux new-session -A -s FlowHub-CAS-AISE -c <dir> claude --remote-control --dangerously-skip-permissions
```

— no `-n`, so the Claude session launches unnamed. The tmux session is
named (`<repo>` / `<repo>-wt-<wt>` via `slotIDFor`), but the Claude session
display name is not.

## Scope

In scope:

- Pass `-n "<display_name>"` to claude at launch, for every entry point that
  launches claude (picker default-launch, positional single-match,
  `bridge open` / `bridge <repo>`, including explicit `--agent claude`).
- Worktree-aware naming: `"<repo>"` normally, `"<repo> [<worktree>]"` when
  launched with `-w/--worktree`.
- claude-only: copilot / opencode / code have no `--name` flag and are left
  untouched.

Out of scope (explicitly deferred):

- The `/clear` → `/rename` **restore** mechanism (old bash wrote
  `$CLAUDE_CONFIG_DIR/bridge-label` and a SessionStart `relabel.sh` hook
  re-issued `/rename` after `/clear` wiped the title, #20). This needs
  hook-install machinery the Go port does not have yet (tied to the Plan-A
  slot layer). Separate follow-up.
- Changing the tmux session/slot name (stays `<repo>` / `<repo>-wt-<wt>`).
- Feeding the name into `--remote-control [name]` /
  `--remote-control-session-name-prefix`. The `-n` display name is the
  mechanism; remote-control flags remain user-controlled via
  `BRIDGE_DEFAULT_AGENT_ARGS`.

## Design

Approach: a small `cmd/bridge` helper that augments the resolved agent spec.
This keeps `internal/launcher` generic (it knows nothing about agent
semantics today) and keeps claude-specific knowledge in `cmd/bridge`
alongside the other agent logic.

Two helpers (likely in `cmd/bridge/preflight.go`):

```go
// displayName returns the claude session display name for a repo launch:
// "<repo>" normally, "<repo> [<worktree>]" when a worktree is given. Matches
// the bash bridge's label.
func displayName(repo core.Repo, worktree string) string {
    if worktree != "" {
        return repo.Name + " [" + worktree + "]"
    }
    return repo.Name
}

// withClaudeName prepends `-n <displayName>` to a claude spec's args so the
// launched session is named in the picker/terminal title. No-op for non-claude
// agents (only claude has --name). Builds a fresh Args slice so the shared
// registry spec is never mutated.
func withClaudeName(spec agents.AgentSpec, repo core.Repo, worktree string) agents.AgentSpec {
    if spec.Name != "claude" {
        return spec
    }
    spec.Args = append([]string{"-n", displayName(repo, worktree)}, spec.Args...)
    return spec
}
```

Wiring — call `withClaudeName(...)` at the three launch sites in
`preflight.go`, right after the spec is resolved and before building argv:

| Site (current line) | Path | Worktree |
|---|---|---|
| ~124 | picker default-launch | `""` |
| ~146 | positional single-match | `""` |
| ~261 | `preflightOpen` (default **and** explicit `--agent claude`) | the real `worktree` |

### Resulting behavior

```
bridge FlowHub-CAS-AISE
  → exec:tmux new-session -A -s FlowHub-CAS-AISE -c <dir> \
       claude -n FlowHub-CAS-AISE --remote-control --dangerously-skip-permissions

bridge FlowHub-CAS-AISE -w feature-x
  → exec:tmux new-session -A -s FlowHub-CAS-AISE-wt-feature-x -c <dir/.worktrees/feature-x> \
       claude -n 'FlowHub-CAS-AISE [feature-x]' --remote-control --dangerously-skip-permissions
```

The space in `'<repo> [<wt>]'` survives the shim boundary:
`shellbridge.EmitExec` shell-quotes each argv element and the bash shim runs
`eval "exec …"`, so the name stays a single argument.

## Error handling

No new failure modes. `withClaudeName` is pure; a non-claude or empty spec
passes through unchanged. If a user has put their own `-n` in
`BRIDGE_DEFAULT_AGENT_ARGS`, claude receives two `-n` flags and uses the last
— harmless, not worth de-duplicating (YAGNI).

## Testing

- Unit: `displayName` (with/without worktree) and `withClaudeName`
  (claude vs non-claude; with/without worktree; assert the shared registry
  spec's `Args` is not mutated).
- Integration (preflight): with `BRIDGE_DEFAULT_AGENT=claude`,
  `bridge __preflight open <repo>` emits an `exec:` directive containing
  `claude -n <repo>`; with `-w <wt>` it contains the sh-quoted
  `'<repo> [<wt>]'` form.

## Cross-shell parity

The bash shim handles the quoted name via `eval exec`. The PowerShell shim
parses the same sh-quoted directive; a worktree name **containing a space** is
the only risk on Windows — plain `<repo>` names (the common case) are
unaffected. Degrade gracefully and note the limitation in the PR rather than
building a separate Windows quoting path now (no Windows CI). The `-n` flag
itself is platform-agnostic (same `claude` binary).
