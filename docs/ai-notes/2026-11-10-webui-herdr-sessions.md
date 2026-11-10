# WebUI: start and see Herdr sessions — brainstorming state

Phase: **brainstorming** (superpowers:brainstorming). No spec, wireframe or code yet.
This note carries what was decided and found so the brainstorm can resume cold.

## The ask

- "schaue nach session/repo wo wir via telegram neue session machen können. könnten
  wir das auch in bridge webui haben?" → a **＋ Session** action in the cockpit
  (pick repo, optional worktree, optional prompt, start a Claude session).
- Follow-up decision: "Ich nutze nur noch 'bridge nav' und das unterstützt bereits
  herdr. Darum gleich herdr und ich will im webui herdr sessions sehen".

## Decided by the user

- Sessions started from the WebUI launch **into Herdr**, not tmux.
- The WebUI **shows Herdr sessions**, including their status.

## Proposed (my reading, played back, not yet confirmed)

1. `bridge launch` gains Herdr support by reusing `internal/herdr` (`Launch(slot, dir, spec)`),
   so ＋ Session, Enrich, Dispatch and the Telegram bot (it also calls `bridge launch`)
   all land in Herdr. No tmux fallback, matching `selectBackend` in `cmd/bridge/backend.go`.
2. Running column sources sessions from `herdr agent list` instead of `claude agents --json`
   (`agentview.List`): `working` and `blocked` shown, `blocked` also as a header signal,
   `idle`/`done` folded (like today's "Idle sessions" row from `web/src/lib/running.js`).
3. ＋ Session dialog as before, target = a Herdr tab.

## Open questions (ask one at a time)

1. **First:** which Herdr workspace do tabs started by the WebUI / Telegram land in?
   `bridge serve` is a service and has no "current" workspace. Options: per-repo
   workspace (like nav), a fixed one (e.g. `bridge`), or chosen in the dialog.
2. Should Enrich/Dispatch switch to Herdr in the same change, or later?
3. Does the Remote Control link (`--rc`) still matter when sessions live in Herdr?
4. Later: a "Session stoppen" action.

## Facts found (agent-dev)

- herdr 0.8.0 at `~/.local/bin/herdr`; socket `~/.config/herdr/herdr.sock` works from
  outside a pane (all `HERDR_*` unset): `herdr workspace list`, `herdr agent list` return
  JSON `{"id":…,"result":{…}}`.
- Agent fields: agent, agent_session, agent_status (idle/done/working/blocked), cwd,
  focused, foreground_cwd, interactive_ready, name (e.g. `freaxnx01-github-io-wt-unami`),
  pane_id, revision, state_change_seq, tab_id, terminal_id, terminal_title(_stripped),
  workspace_id. `cwd` may end with " (deleted)".
- Workspaces seen: bridge, gamez, gamez-rhyflitzer, hlab, flowhub, sw-factory, org, ~.
- `internal/herdr`: client `New()` uses `$HERDR_BIN_PATH` or `herdr` on PATH, pinned to
  `$HERDR_WORKSPACE_ID` (unpinned = all); Backend has `Live()`, `Attach`, `Launch`,
  `tabCreate(ctx, dir, label)`, `agentList`.
- Today's launch path for cockpit actions: `cmd/bridge/pipeline.go` `launchArgs` /
  `launchSession` → `bridge launch <name> -w <wt> --agent claude --rc --rc-wait 15s --json --prompt …` (tmux).
- Telegram bot: `bridge-admin.service` → `admin/run-admin-service.sh`, skill
  `bridge-operator`, launches with `env -u CLAUDE_CONFIG_DIR bridge launch <repo> [-w <wt>] --rc --json`.
- Live testing, Herdr queries and deploy (`bridge-serve` user unit, port 7777) only work on agent-dev.

## Next

Answer open question 1, finish playback, write the spec to
`docs/superpowers/specs/YYYY-MM-DD-webui-herdr-sessions-design.md`, builder check,
approval, then `/ui:brainstorm` (wireframe) → `/ui:flow` → writing-plans → build.

Queued after this: **Test Management from game-rhyflitzer for all repos** (brainstorm not started).
