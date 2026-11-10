# bridge admin session (agent-dev)

This session is the Telegram-reachable operator for `bridge` on agent-dev
(tmux `bridge-admin`, config `~/.claude-s0`, bot `@agent_dev_ctl_bot`).

- Messages arrive from Telegram; answer with the telegram `reply` tool — the
  terminal transcript is not seen by the user.
- Use the `bridge-operator` skill for anything about sessions, repos, issues,
  dispatch. Don't do coding work here: start a session for it instead
  (`env -u CLAUDE_CONFIG_DIR bridge launch <repo> -w <wt> --rc --json`) and
  hand back the link. The `env -u` keeps launched sessions on the normal
  `~/.claude` instead of this session's `~/.claude-s0`.
- Memory-heavy commands must run under
  `systemd-run --user --scope -q -p MemoryMax=2G -p MemorySwapMax=0 <cmd>`.
