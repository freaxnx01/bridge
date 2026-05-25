# bridge vs. Claude Agents view

_Written 2026-05-21. Closes #21._

## Verdict

bridge and the Claude Agents view are complementary, not overlapping. **bridge is the local-repo hub** — it knows your filesystem, your forges, your MRU, your worktrees. **Agents view is the session manager** — it knows what is running, on what device, and lets you steer it from anywhere. Neither replaces the other.

## What bridge owns (won't move to Agents view)

- **Repo discovery:** `$_BRIDGE_BASE`, multi-base, multi-forge (GitHub, Forgejo, GitLab, ADO), `fzf` picker, MRU ordering, metadata/topic search.
- **Repo lifecycle:** clone, create, delete across all configured forges.
- **Worktrees:** `--ws` status, `-w` launch-into-worktree.
- **Local dashboards:** `--dashboard`, `--issues`, `-f` focus list with issue counts.
- **Non-Claude launchers:** copilot, opencode, VS Code — Agents view is Claude-specific.
- **Launch decision:** bridge is where you pick the repo _and_ choose whether to enable Remote Control (`--rc`). That opt-in decision always lives here.

## What Agents view handles better

- **Session listing across devices:** Agents view sees all RC-enabled sessions regardless of which machine or whether tmux is involved. `bridge --status` only sees what's running on the local host.
- **Remote steering and mobile visibility:** steering, interrupting, and reading transcripts from a phone or browser. Remote Control + Telegram were approximating this; Agents view does it natively.

## Overlap areas — resolved

**`--status` / `--attach` / `--pick`:** Keep them, but scope them mentally to _non-RC sessions_ — `--no-channel` tmux sessions, copilot, opencode, VS Code. For RC-enabled Claude sessions, Agents view is the better surface. No code change needed; users self-select.

**Telegram integration:** Still useful for _push_ notifications and presence-aware paging (Agents view is pull — you check it). Not a priority to expand; not a priority to remove. Treat as stable background infrastructure.

**Remote Control URL (`--rc`):** bridge enables it at launch time; Agents view consumes it. The bridge is already built. No new bridge points needed.

## Future-feature filter

| Proposed feature | Verdict |
|---|---|
| Deeper local-first features (worktrees, focus, dashboards) | **Yes** — bridge's differentiator |
| Replace `--status` with Agents view API polling | **No** — wrong layer, adds cloud dependency |
| `--open-in-agents` deep-link shortcut | **Maybe** — small, deferred, not urgent |
| Replicating session transcript/interrupt in bridge | **No** — Agents view owns this |
| More forges, more clone targets, multi-base improvements | **Yes** — pure local-first value |
