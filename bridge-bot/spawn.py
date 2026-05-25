"""Spawn bridge in detached tmux + poll slots.json for confirmation."""

import json
import logging
import os
import secrets
import shlex
import subprocess
import time
from pathlib import Path

LOG = logging.getLogger(__name__)

SLOTS_PATH = str(Path.home() / ".cache" / "bridge" / "slots.json")

STRIPPED_ENV_VARS = {
    "TMUX", "TMUX_PANE", "STY", "CLAUDE_CODE_SSE_PORT",
    "INVOCATION_ID", "JOURNAL_STREAM", "LISTEN_FDS", "LISTEN_PID",
    "NOTIFY_SOCKET", "MAINPID",
}


def clean_env(src: dict[str, str] | None = None) -> dict[str, str]:
    src = dict(os.environ if src is None else src)
    return {k: v for k, v in src.items() if k not in STRIPPED_ENV_VARS}


def read_slots(path: str = SLOTS_PATH) -> dict:
    if not os.path.exists(path):
        return {}
    with open(path) as fh:
        try:
            return json.load(fh).get("slots", {})
        except (json.JSONDecodeError, ValueError):
            return {}


def find_new_slot(path: str, before_keys: set[str], repo: str) -> dict | None:
    """Return {slot, session, ...} if a new slot for `repo` has appeared, else None."""
    now = read_slots(path)
    new_keys = set(now) - set(before_keys)
    for k in new_keys:
        entry = now[k] or {}
        if entry.get("repo") == repo:
            return {"slot": k, "session": entry.get("session"), **entry}
    return None


def spawn_bridge(name: str, extra_args: list[str] | None = None) -> str:
    """Launch `bridge <name> [extra_args...]` in detached tmux. Return wrapper session name."""
    wrapper = f"bridge-spawn-{secrets.token_hex(3)}"
    parts = [shlex.quote(name)] + [shlex.quote(a) for a in (extra_args or [])]
    cmdline = "bridge " + " ".join(parts)
    LOG.info("spawn: tmux session=%s cmd=%s", wrapper, cmdline)
    subprocess.run(
        ["tmux", "new-session", "-d", "-s", wrapper, "bash", "-lc", cmdline],
        check=True, env=clean_env(),
    )
    return wrapper


def wait_for_slot(repo: str, before_keys: set[str], deadline_sec: float = 3.0,
                  path: str = SLOTS_PATH) -> dict | None:
    end = time.monotonic() + deadline_sec
    while time.monotonic() < end:
        hit = find_new_slot(path, before_keys, repo)
        if hit:
            return hit
        time.sleep(0.15)
    return None
