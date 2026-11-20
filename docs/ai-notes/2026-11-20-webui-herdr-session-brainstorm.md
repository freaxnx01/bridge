# Brainstorm state: "＋ Session" in the cockpit, Herdr-based

Phase: **brainstorming** (`superpowers:brainstorming`), before any spec. Branch
`worktree-webui-herdr`, rebased on `origin/main` @ `8eb4b7e` on 2026-11-20.

Path after this: spec in `docs/superpowers/specs/YYYY-MM-DD-webui-herdr-session-design.md`
→ builder check → `/ui:brainstorm` → `/ui:flow` → `/ui:build` → `/ui:review`.

## Goal (from the user)

Start a Claude session from the cockpit WebUI (Svelte 5 in `web/`, embedded in
`bridge serve`). It lands as a Herdr tab, not a tmux window.

## Already decided by the user

- Sessions started from the WebUI, Enrich, Dispatch **and Telegram** start in Herdr.
  `bridge launch` uses the existing backend in `internal/herdr`
  (`Launch`/`Live`; `selectBackend` in `cmd/bridge/backend.go`). **No tmux fallback.**
- The Running column reads `herdr agent list` instead of `claude agents --json`
  (today: `internal/agentview`). `working` and `blocked` go on top, and `blocked`
  also shows as a signal in the header. `idle` and `done` are folded away.
- The Herdr socket (`~/.config/herdr/herdr.sock`) is reachable for `bridge-serve`.
  On srvdmsk8s01 both run as `admin`.

## Open question (asked, not answered yet)

**Which Herdr workspace do tabs started by the WebUI or Telegram land in?** The
service has no "current" workspace. Last question put to the user: "If you start a
session from the cockpit or Telegram while you're away, where on your Herdr screen
would you look for that tab once you're back at the notebook?"

## Facts found during recon

- `selectBackend` picks Herdr only via `BRIDGE_LAUNCHER=herdr` or `HERDR_ENV=1`.
  `bridge-serve.service` sets neither, so today it falls back to **tmux**. The
  "no tmux fallback" decision means changing this for service-launched sessions.
- `internal/herdr` already has: `backend.go`, `herdr.go`, `name.go`, `path.go`
  (plus tests).

## Deployment (changed this session)

- The cockpit now runs on **srvdmsk8s01** (the company VPN can't reach the homelab
  LXC 192.168.1.108). User unit `~/.config/systemd/user/bridge-serve.service`:
  `bridge serve --host 0.0.0.0 --port 7777` (the user chose direct access over an
  SSH tunnel), linger on.
- `~/.config/bridge-serve/env` (0600): `BRIDGE_API_TOKEN` (random) and
  `BRIDGE_COCKPIT_ORIGINS=10.240.10.83:7777,srvdmsk8s01:7777`.
- URL: http://10.240.10.83:7777, confirmed by the user's screenshot.
- Rebuilding means `just build` from an up-to-date tree, then
  `systemctl --user restart bridge-serve`.

## Loose ends, outside this scope

- `docs/cockpit.md` still describes the homelab deployment only.
- `bridge-mcp.service` was not restarted after the rebuild.
- #366 (ADO error noise) has been fixed on main in the meantime (#388).

## After this

Bring "Test Management" from `game-rhyflitzer` into the WebUI for all repos. It
gets its own brainstorm.
