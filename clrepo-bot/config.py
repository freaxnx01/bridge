"""Load/save the clrepo-bot config file."""

import json
import os
from pathlib import Path

CONFIG_PATH = str(Path.home() / ".cache" / "clrepo" / "clrepo-bot.json")


class ConfigError(RuntimeError):
    pass


def load() -> dict:
    if not os.path.exists(CONFIG_PATH):
        raise ConfigError(
            f"{CONFIG_PATH} not found — run setup-claude-channels.sh"
        )
    with open(CONFIG_PATH) as fh:
        return json.load(fh)


def set_last_update_id(value: int) -> None:
    d = load()
    d["last_update_id"] = int(value)
    tmp = CONFIG_PATH + ".tmp"
    with open(tmp, "w") as fh:
        json.dump(d, fh, indent=2, sort_keys=True)
    os.replace(tmp, CONFIG_PATH)
