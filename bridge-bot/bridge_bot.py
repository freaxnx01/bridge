#!/usr/bin/env python3
"""bridge-bot entrypoint: long-poll Telegram and dispatch to handlers."""

import json
import logging
import os
import shlex
import signal
import subprocess
import sys
import threading
import time
from pathlib import Path

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE))

import auth
import config
import handlers
import repos
import spawn
import tg

LOG = logging.getLogger("bridge-bot")
PID_PATH = Path.home() / ".cache" / "bridge" / "bridge-bot.pid"

# Reload flag set by SIGHUP handler.
_RELOAD = {"want": False}


def _log_event(**kwargs) -> None:
    rec = {"ts": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()), **kwargs}
    print(json.dumps(rec), flush=True)


def _kill_slot(slot: str) -> bool:
    """Kill the tmux session belonging to a bridge slot."""
    slots = spawn.read_slots()
    entry = slots.get(str(slot))
    if not entry:
        return False
    # List-format entries have no `session`; the tmux name equals the slot id/key.
    session = entry.get("session") or str(slot)
    try:
        subprocess.run(["tmux", "kill-session", "-t", session],
                       check=True, env=spawn.clean_env())
        return True
    except subprocess.CalledProcessError:
        return False


BOT_COMMANDS = [
    {"command": "new", "description": "Start a session (repo picker)"},
    {"command": "newrepo", "description": "Create a new repo (Forgejo/GitHub)"},
    {"command": "status", "description": "Bridge summary + live sessions"},
    {"command": "kill", "description": "Stop a session"},
    {"command": "cancel", "description": "Drop the current picker"},
    {"command": "help", "description": "Show help"},
]


def _bridge_summary() -> str:
    try:
        out = subprocess.run(
            ["bridge", "status"], capture_output=True, text=True, timeout=10,
            env=spawn.clean_env(),
        )
        return (out.stdout + out.stderr).strip()
    except subprocess.SubprocessError as e:
        LOG.warning("bridge status failed: %s", e)
        return f"(error: {e})"


def _sessions_table() -> str:
    try:
        out = subprocess.run(
            ["bridge", "sessions", "--json"], capture_output=True, text=True,
            timeout=10, env=spawn.clean_env(),
        )
        rows = json.loads(out.stdout) or []
    except (subprocess.SubprocessError, json.JSONDecodeError, ValueError) as e:
        return f"(sessions error: {e})"
    if not rows:
        return "(no live sessions)"
    width = max(len(r.get("slot_id", "?")) for r in rows)
    lines = []
    for r in rows:
        sid = r.get("slot_id", "?")
        state = r.get("state", "?")
        when = (r.get("last_activity") or "")[:16].replace("T", " ")
        lines.append(f"{sid:<{width}}  {state:<8}  {when}")
    return "\n".join(lines)


def _status() -> str:
    """Structured status: bridge summary, then a table of live sessions."""
    summary = _bridge_summary()
    table = _sessions_table()
    sections = []
    if summary:
        sections.append(summary)
    sections.append("Sessions:\n" + table)
    return "\n\n".join(sections) or "(empty)"


def _spawn_and_confirm(name: str, extra: list[str] | None) -> dict | None:
    before = set(spawn.read_slots())
    try:
        spawn.spawn_bridge(name, extra or None)
    except (FileNotFoundError, subprocess.CalledProcessError) as e:
        _log_event(evt="spawn", repo=name, ok=False, error=str(e))
        return None
    hit = spawn.wait_for_slot(name, before)
    _log_event(evt="spawn", repo=name, ok=bool(hit),
               slot=(hit or {}).get("slot"), session=(hit or {}).get("session"))
    return hit


def _create_repo(name: str, forge: str, private: bool) -> dict | None:
    cmd = ["bridge", "create", "--forge", forge, "--json"]
    if not private:
        cmd.append("--public")
    cmd += ["--", name]
    try:
        out = subprocess.run(cmd, capture_output=True, text=True, timeout=60,
                             env=spawn.clean_env())  # create+clone can be slow
        if out.returncode != 0:
            _log_event(evt="create_repo", name=name, ok=False, error=out.stderr.strip())
            return None
        result = json.loads(out.stdout)
        _log_event(evt="create_repo", name=name, ok=True,
                   full_name=result.get("full_name"))
        return result
    except (subprocess.SubprocessError, json.JSONDecodeError, ValueError) as e:
        _log_event(evt="create_repo", name=name, ok=False, error=str(e))
        return None


def build_context(bot: tg.Bot) -> handlers.Context:
    return handlers.Context(
        bot=bot,
        pickers={},  # in-memory only; abandoned pickers are never pruned (single-user bot)
        local_provider=lambda: repos.list_local(),
        remote_provider=lambda: repos.list_remote(),
        mru_provider=lambda: repos.read_mru(),
        spawner=_spawn_and_confirm,
        kill_session=_kill_slot,
        status_provider=_status,
        repo_creator=_create_repo,
    )


def _refresh_remote_if_stale() -> None:
    """Refresh remote.list cache in a background thread if stale (missing or > 10min old)."""
    p = repos.REMOTE_LIST_PATH
    needs = (not os.path.exists(p)) or (time.time() - os.path.getmtime(p) > 600)
    if not needs:
        return

    def _refresh():
        try:
            subprocess.run(["bash", "-lc", "bridge --refresh >/dev/null 2>&1"],
                           timeout=30, env=spawn.clean_env())
        except subprocess.SubprocessError:
            pass

    t = threading.Thread(target=_refresh, daemon=True)
    t.start()


def _handle_message(ctx: handlers.Context, msg: dict) -> None:
    chat_id = msg["chat"]["id"]
    text = msg.get("text", "")
    if not text.startswith("/"):
        handlers.on_text_message(ctx, chat_id, text)
        return
    head, _, rest = text.partition(" ")
    cmd = head.split("@", 1)[0].lstrip("/")
    if cmd in ("start", "help"):
        handlers.cmd_help(ctx, chat_id)
    elif cmd == "new":
        if ctx.pickers.get(chat_id) is None:
            _refresh_remote_if_stale()  # cheap, only triggers if stale
        handlers.cmd_new(ctx, chat_id, rest.strip())
    elif cmd == "newrepo":
        handlers.cmd_newrepo(ctx, chat_id, rest.strip())
    elif cmd == "status":
        handlers.cmd_status(ctx, chat_id)
    elif cmd == "kill":
        handlers.cmd_kill(ctx, chat_id, rest.strip())
    elif cmd == "cancel":
        handlers.cmd_cancel(ctx, chat_id)
    else:
        ctx.bot.send_message(chat_id, "Unknown command. /help")


def _handle_update(ctx: handlers.Context, cfg: dict, rl: auth.RateLimiter, upd: dict) -> None:
    if "message" in upd:
        msg = upd["message"]
        user = msg["from"]["id"]
        chat_type = msg["chat"]["type"]
        if chat_type != "private":
            return
        if not auth.is_allowed(user, cfg["allowlist"]):
            _log_event(evt="unauthorized", user=user)
            return
        if not rl.take(user):
            _log_event(evt="rate_limited", user=user)
            return
        _log_event(evt="cmd", user=user, chat=msg["chat"]["id"], text=msg.get("text", ""))
        _handle_message(ctx, msg)
    elif "callback_query" in upd:
        cq = upd["callback_query"]
        user = cq["from"]["id"]
        if not auth.is_allowed(user, cfg["allowlist"]):
            _log_event(evt="unauthorized", user=user)
            return
        if not rl.take(user):
            return
        msg = cq.get("message") or {}
        chat_id = msg.get("chat", {}).get("id")
        if msg.get("chat", {}).get("type") != "private":
            return
        _log_event(evt="callback", user=user, data=cq.get("data"))
        handlers.on_callback(
            ctx, chat_id=chat_id, callback_id=cq["id"],
            data=cq.get("data", ""), message_id=msg.get("message_id"),
        )


def _on_sighup(_signum, _frame):
    _RELOAD["want"] = True


def main() -> int:
    logging.basicConfig(level=logging.INFO)
    signal.signal(signal.SIGHUP, _on_sighup)
    PID_PATH.parent.mkdir(parents=True, exist_ok=True)
    PID_PATH.write_text(str(os.getpid()))

    cfg = config.load()
    token = tg.fetch_token_from_passbolt(cfg["passbolt_resource_id"])
    bot = tg.Bot(token)
    ctx = build_context(bot)
    rl = auth.RateLimiter(capacity=20, refill_per_sec=20 / 60.0)

    try:
        bot.set_my_commands(BOT_COMMANDS)
        _log_event(evt="commands_registered", n=len(BOT_COMMANDS))
    except tg.TelegramAPIError as e:
        _log_event(evt="commands_error", error=str(e))

    offset = int(cfg.get("last_update_id", 0)) + 1
    last_beat = 0.0
    _log_event(evt="start", pid=os.getpid())

    while True:
        if _RELOAD["want"]:
            _RELOAD["want"] = False
            try:
                cfg = config.load()
                token = tg.fetch_token_from_passbolt(cfg["passbolt_resource_id"])
                bot = tg.Bot(token)
                ctx.bot = bot
                _log_event(evt="reload", ok=True)
            except Exception as e:
                _log_event(evt="reload", ok=False, error=str(e))

        now = time.monotonic()
        if now - last_beat > 60:
            _log_event(evt="heartbeat")
            PID_PATH.write_text(str(os.getpid()))
            last_beat = now

        try:
            updates = bot.get_updates(offset=offset, timeout=30)
        except tg.TelegramAPIError as e:
            _log_event(evt="poll_error", error=str(e))
            time.sleep(5)
            continue

        for upd in updates:
            try:
                _handle_update(ctx, cfg, rl, upd)
            except Exception as e:  # noqa: BLE001
                _log_event(evt="dispatch_error", error=str(e))
            offset = upd["update_id"] + 1

        if updates:
            try:
                config.set_last_update_id(offset - 1)
            except Exception as e:  # noqa: BLE001
                _log_event(evt="persist_error", error=str(e))


if __name__ == "__main__":
    sys.exit(main() or 0)
