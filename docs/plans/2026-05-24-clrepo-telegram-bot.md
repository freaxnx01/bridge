# clrepo Telegram wrapper bot — implementation plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship a standalone Telegram bot (`clrepo-bot`) that wraps `clrepo` on claude-dev so the user can spawn new Claude sessions from Telegram via a paginated picker.

**Architecture:** Python 3 stdlib daemon under `systemd --user`. Long-polls Telegram, dispatches to handlers, spawns sessions via `tmux new-session -d 'bash -lc "clrepo <name>"'`. No Claude in the command loop. Independent of bot0/admin.

**Tech Stack:** Python 3 (stdlib only — `urllib`, `json`, `subprocess`, `unittest`), `tmux`, `passbolt` CLI, `systemd --user`.

**Spec:** [`docs/specs/2026-05-24-clrepo-telegram-bot-design.md`](../specs/2026-05-24-clrepo-telegram-bot-design.md)

---

## File structure

```
clrepo-bot/
├── README.md                  # quick install/run notes
├── clrepo_bot.py              # entrypoint: poll loop + dispatcher (executable)
├── handlers.py                # one function per command
├── picker.py                  # pagination state + inline-keyboard rendering
├── tg.py                      # Bot API wrapper (HTTP via urllib)
├── spawn.py                   # tmux detached launch + slot-poll confirmation
├── auth.py                    # allowlist + per-user token-bucket rate limit
├── repos.py                   # local + remote repo list assembly (reads clrepo caches)
├── config.py                  # load/save ~/.cache/clrepo/clrepo-bot.json
└── tests/
    ├── __init__.py
    ├── test_auth.py
    ├── test_picker.py
    ├── test_repos.py
    ├── test_handlers.py
    └── test_config.py
```

Also touched:

- `setup-claude-channels.sh` — gains a "clrepo-bot" section (optional prompt block)
- `clrepo-bot/systemd/clrepo-bot.service` — shipped in repo, installed via setup script
- `README.md` (top-level) — one paragraph + link to `clrepo-bot/README.md`

**Caches read (not owned by this bot):**

- `~/.cache/clrepo/slots.json` — read for `/status`, `/kill`, spawn confirmation poll
- `~/.cache/clrepo/mru` — read for picker MRU ordering
- `~/.cache/clrepo/remote.list` — read when `[🌐 Include remote]` toggled
- `~/projects/repos/` — walked for local repo list

**Caches owned by this bot:**

- `~/.cache/clrepo/clrepo-bot.json` — config + `last_update_id`
- `~/.cache/clrepo/clrepo-bot.log` — append-only JSON-line log (written by systemd unit)
- `~/.cache/clrepo/clrepo-bot.pid` — heartbeat (written every 60s)

---

## Conventions

- **Tests:** Python `unittest` (stdlib). Run with `python3 -m unittest discover clrepo-bot/tests -v`.
- **Commits:** Conventional Commits format per `CLAUDE.md`. No `_CLREPO_VERSION` bump unless we touch `clrepo.sh` (we do not in this plan).
- **Style:** stdlib only; no `requirements.txt`. Type hints encouraged but not enforced. Modules ≤ ~150 LOC each — split if growing.

---

## Task 1: Scaffold the package directory

**Files:**
- Create: `clrepo-bot/README.md`
- Create: `clrepo-bot/__init__.py` (empty)
- Create: `clrepo-bot/tests/__init__.py` (empty)
- Create: `clrepo-bot/.gitignore`

- [ ] **Step 1: Create directory layout**

```bash
mkdir -p clrepo-bot/tests clrepo-bot/systemd
touch clrepo-bot/__init__.py clrepo-bot/tests/__init__.py
```

- [ ] **Step 2: Write `clrepo-bot/README.md`**

```markdown
# clrepo-bot

Standalone Telegram bot that wraps `clrepo` on the host. Spawns new Claude
sessions on tap. Independent of bot0/admin and the per-slot Telegram bots.

Spec: `../docs/specs/2026-05-24-clrepo-telegram-bot-design.md`

## Install

1. Create a Telegram bot via @BotFather; store the token in Passbolt.
2. Run `../setup-claude-channels.sh` and answer the "clrepo-bot" section.
3. The setup script offers to install + enable the systemd user unit.

## Run manually (debug)

    ./clrepo_bot.py

## Tests

    python3 -m unittest discover tests -v
```

- [ ] **Step 3: Write `clrepo-bot/.gitignore`**

```
__pycache__/
*.pyc
.pytest_cache/
```

- [ ] **Step 4: Commit**

```bash
git add clrepo-bot
git commit -m "feat(bot): scaffold clrepo-bot package layout"
```

---

## Task 2: `config.py` — load/save bot config

**Files:**
- Create: `clrepo-bot/config.py`
- Test: `clrepo-bot/tests/test_config.py`

The config wraps `~/.cache/clrepo/clrepo-bot.json` with two operations the daemon needs: full load, and atomic update of `last_update_id`.

- [ ] **Step 1: Write failing test**

`clrepo-bot/tests/test_config.py`:

```python
import json
import os
import tempfile
import unittest
from unittest import mock

import sys, pathlib
sys.path.insert(0, str(pathlib.Path(__file__).resolve().parents[1]))

import config


class ConfigTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.NamedTemporaryFile(mode="w", suffix=".json", delete=False)
        json.dump({
            "passbolt_resource_id": "abc-123",
            "telegram_owner_id": 42,
            "allowlist": [42, 99],
            "last_update_id": 100,
        }, self.tmp)
        self.tmp.close()
        self.patcher = mock.patch.object(config, "CONFIG_PATH", self.tmp.name)
        self.patcher.start()

    def tearDown(self):
        self.patcher.stop()
        os.unlink(self.tmp.name)

    def test_load_returns_dict_with_expected_keys(self):
        c = config.load()
        self.assertEqual(c["passbolt_resource_id"], "abc-123")
        self.assertEqual(c["telegram_owner_id"], 42)
        self.assertEqual(c["allowlist"], [42, 99])
        self.assertEqual(c["last_update_id"], 100)

    def test_set_last_update_id_persists_atomically(self):
        config.set_last_update_id(250)
        with open(self.tmp.name) as fh:
            d = json.load(fh)
        self.assertEqual(d["last_update_id"], 250)
        # Other fields preserved.
        self.assertEqual(d["allowlist"], [42, 99])

    def test_load_raises_friendly_error_when_missing(self):
        os.unlink(self.tmp.name)
        with self.assertRaises(config.ConfigError):
            config.load()


if __name__ == "__main__":
    unittest.main()
```

- [ ] **Step 2: Run, expect failure**

```bash
python3 -m unittest clrepo-bot/tests/test_config.py -v
```

Expected: `ModuleNotFoundError: No module named 'config'`.

- [ ] **Step 3: Implement `clrepo-bot/config.py`**

```python
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
```

- [ ] **Step 4: Run, expect pass**

```bash
python3 -m unittest clrepo-bot/tests/test_config.py -v
```

Expected: 3 tests pass.

- [ ] **Step 5: Commit**

```bash
git add clrepo-bot/config.py clrepo-bot/tests/test_config.py
git commit -m "feat(bot): config loader for clrepo-bot.json"
```

---

## Task 3: `auth.py` — allowlist + rate limit

**Files:**
- Create: `clrepo-bot/auth.py`
- Test: `clrepo-bot/tests/test_auth.py`

Pure functions: `is_allowed(user_id, allowlist)` and a `RateLimiter` class with a per-user token bucket (20 tokens, refill at 20/min).

- [ ] **Step 1: Write failing test**

`clrepo-bot/tests/test_auth.py`:

```python
import unittest
import sys, pathlib
sys.path.insert(0, str(pathlib.Path(__file__).resolve().parents[1]))

import auth


class AllowlistTests(unittest.TestCase):
    def test_user_in_allowlist_is_allowed(self):
        self.assertTrue(auth.is_allowed(42, [42, 99]))

    def test_user_not_in_allowlist_is_denied(self):
        self.assertFalse(auth.is_allowed(7, [42, 99]))

    def test_empty_allowlist_denies_all(self):
        self.assertFalse(auth.is_allowed(42, []))


class RateLimiterTests(unittest.TestCase):
    def test_allows_up_to_capacity(self):
        clock = [1000.0]
        rl = auth.RateLimiter(capacity=3, refill_per_sec=1.0, clock=lambda: clock[0])
        self.assertTrue(rl.take(1))
        self.assertTrue(rl.take(1))
        self.assertTrue(rl.take(1))
        self.assertFalse(rl.take(1))

    def test_refills_over_time(self):
        clock = [1000.0]
        rl = auth.RateLimiter(capacity=2, refill_per_sec=1.0, clock=lambda: clock[0])
        rl.take(1)
        rl.take(1)
        self.assertFalse(rl.take(1))
        clock[0] += 2.0  # 2 tokens regenerated
        self.assertTrue(rl.take(1))
        self.assertTrue(rl.take(1))
        self.assertFalse(rl.take(1))

    def test_per_user_independent(self):
        clock = [1000.0]
        rl = auth.RateLimiter(capacity=1, refill_per_sec=1.0, clock=lambda: clock[0])
        self.assertTrue(rl.take(42))
        self.assertFalse(rl.take(42))
        self.assertTrue(rl.take(99))


if __name__ == "__main__":
    unittest.main()
```

- [ ] **Step 2: Run, expect failure**

```bash
python3 -m unittest clrepo-bot/tests/test_auth.py -v
```

Expected: `ModuleNotFoundError: No module named 'auth'`.

- [ ] **Step 3: Implement `clrepo-bot/auth.py`**

```python
"""Allowlist + token-bucket rate limiter."""

import time
from typing import Callable, Iterable


def is_allowed(user_id: int, allowlist: Iterable[int]) -> bool:
    return int(user_id) in {int(x) for x in allowlist}


class RateLimiter:
    """Per-user token bucket. Capacity tokens, refills at refill_per_sec."""

    def __init__(self, capacity: int, refill_per_sec: float, clock: Callable[[], float] = time.monotonic):
        self.capacity = capacity
        self.refill = refill_per_sec
        self.clock = clock
        self._state: dict[int, tuple[float, float]] = {}  # user -> (tokens, last_ts)

    def take(self, user_id: int, cost: float = 1.0) -> bool:
        now = self.clock()
        tokens, last = self._state.get(user_id, (float(self.capacity), now))
        tokens = min(self.capacity, tokens + (now - last) * self.refill)
        if tokens < cost:
            self._state[user_id] = (tokens, now)
            return False
        self._state[user_id] = (tokens - cost, now)
        return True
```

- [ ] **Step 4: Run, expect pass**

```bash
python3 -m unittest clrepo-bot/tests/test_auth.py -v
```

Expected: 6 tests pass.

- [ ] **Step 5: Commit**

```bash
git add clrepo-bot/auth.py clrepo-bot/tests/test_auth.py
git commit -m "feat(bot): allowlist + per-user token-bucket rate limit"
```

---

## Task 4: `repos.py` — local + remote repo list

**Files:**
- Create: `clrepo-bot/repos.py`
- Test: `clrepo-bot/tests/test_repos.py`

Two pure functions plus one cache reader. Local list is `walk(REPOS_ROOT)` for `.git` dirs minus prefix; remote list reads `~/.cache/clrepo/remote.list`. Ordering uses `~/.cache/clrepo/mru` if present.

- [ ] **Step 1: Write failing test**

`clrepo-bot/tests/test_repos.py`:

```python
import os
import tempfile
import unittest
from pathlib import Path

import sys, pathlib
sys.path.insert(0, str(pathlib.Path(__file__).resolve().parents[1]))

import repos


class LocalReposTests(unittest.TestCase):
    def setUp(self):
        self.root = tempfile.mkdtemp()
        for rel in ["github/me/public/foo", "github/me/public/bar", "gitlab/me/baz"]:
            os.makedirs(os.path.join(self.root, rel, ".git"))

    def test_walks_git_dirs_and_strips_prefix(self):
        found = repos.list_local(self.root)
        self.assertEqual(
            sorted(found),
            ["github/me/public/bar", "github/me/public/foo", "gitlab/me/baz"],
        )

    def test_mru_ordering_puts_recent_first(self):
        mru_lines = ["gitlab/me/baz", "github/me/public/foo"]
        out = repos.order_by_mru(
            ["github/me/public/foo", "github/me/public/bar", "gitlab/me/baz"],
            mru_lines,
        )
        self.assertEqual(
            out,
            ["gitlab/me/baz", "github/me/public/foo", "github/me/public/bar"],
        )

    def test_filter_query_matches_basename_case_insensitive(self):
        items = ["github/me/public/clrepo", "github/me/public/dotfiles", "gitlab/me/CLRepo-x"]
        self.assertEqual(
            sorted(repos.filter_query(items, "clrepo")),
            ["github/me/public/clrepo", "gitlab/me/CLRepo-x"],
        )

    def test_filter_query_empty_returns_all(self):
        items = ["a", "b"]
        self.assertEqual(repos.filter_query(items, ""), ["a", "b"])


class RemoteReposTests(unittest.TestCase):
    def test_reads_remote_list_lines(self):
        with tempfile.NamedTemporaryFile("w", delete=False) as tmp:
            tmp.write("github/me/public/foo\ngithub/me/public/bar\n")
            path = tmp.name
        try:
            out = repos.list_remote(path)
            self.assertEqual(out, ["github/me/public/foo", "github/me/public/bar"])
        finally:
            os.unlink(path)

    def test_missing_remote_file_returns_empty(self):
        self.assertEqual(repos.list_remote("/nonexistent/path"), [])


if __name__ == "__main__":
    unittest.main()
```

- [ ] **Step 2: Run, expect failure**

```bash
python3 -m unittest clrepo-bot/tests/test_repos.py -v
```

Expected: `ModuleNotFoundError: No module named 'repos'`.

- [ ] **Step 3: Implement `clrepo-bot/repos.py`**

```python
"""Repo list assembly — reads clrepo caches, never writes."""

import os
from pathlib import Path

DEFAULT_ROOT = str(Path.home() / "projects" / "repos")
MRU_PATH = str(Path.home() / ".cache" / "clrepo" / "mru")
REMOTE_LIST_PATH = str(Path.home() / ".cache" / "clrepo" / "remote.list")


def list_local(root: str = DEFAULT_ROOT) -> list[str]:
    """Walk `root` for `.git` dirs, return repo paths relative to `root`."""
    out: list[str] = []
    for dirpath, dirnames, _ in os.walk(root):
        if "_archive" in dirnames:
            dirnames.remove("_archive")
        if ".git" in dirnames:
            dirnames[:] = []  # don't recurse into the repo
            out.append(os.path.relpath(dirpath, root))
    return sorted(out)


def order_by_mru(items: list[str], mru: list[str]) -> list[str]:
    """Sort items: MRU order first (for items present in mru), then the rest in original order."""
    mru_set = [m for m in mru if m in items]
    leftover = [i for i in items if i not in mru_set]
    return mru_set + leftover


def filter_query(items: list[str], query: str) -> list[str]:
    """Case-insensitive substring match against basename (final path component)."""
    q = query.strip().lower()
    if not q:
        return list(items)
    return [i for i in items if q in os.path.basename(i).lower()]


def list_remote(path: str = REMOTE_LIST_PATH) -> list[str]:
    if not os.path.exists(path):
        return []
    with open(path) as fh:
        return [line.strip() for line in fh if line.strip()]


def read_mru(path: str = MRU_PATH) -> list[str]:
    if not os.path.exists(path):
        return []
    with open(path) as fh:
        return [line.strip() for line in fh if line.strip()]
```

- [ ] **Step 4: Run, expect pass**

```bash
python3 -m unittest clrepo-bot/tests/test_repos.py -v
```

Expected: 6 tests pass.

- [ ] **Step 5: Commit**

```bash
git add clrepo-bot/repos.py clrepo-bot/tests/test_repos.py
git commit -m "feat(bot): local+remote repo list with MRU and filter"
```

---

## Task 5: `picker.py` — pagination state + keyboard rendering

**Files:**
- Create: `clrepo-bot/picker.py`
- Test: `clrepo-bot/tests/test_picker.py`

Pure data: a `PickerState` dataclass and a `render(state)` function that returns `(text, inline_keyboard)` ready for the Telegram API. No I/O.

- [ ] **Step 1: Write failing test**

`clrepo-bot/tests/test_picker.py`:

```python
import unittest
import sys, pathlib
sys.path.insert(0, str(pathlib.Path(__file__).resolve().parents[1]))

import picker


class PickerStateTests(unittest.TestCase):
    def test_default_state(self):
        s = picker.PickerState(items=["a", "b", "c"])
        self.assertEqual(s.page, 0)
        self.assertEqual(s.include_remote, False)
        self.assertEqual(s.query, "")

    def test_pagination_pages_calculation(self):
        s = picker.PickerState(items=[str(i) for i in range(25)])
        self.assertEqual(picker.total_pages(s), 3)  # 10 per page

    def test_page_slice(self):
        s = picker.PickerState(items=[str(i) for i in range(25)], page=2)
        self.assertEqual(picker.current_page_items(s), ["20", "21", "22", "23", "24"])

    def test_clamp_page_when_query_shrinks_results(self):
        s = picker.PickerState(items=[str(i) for i in range(5)], page=4)
        # page is clamped lazily by current_page_items / total_pages
        self.assertEqual(picker.total_pages(s), 1)
        self.assertEqual(picker.current_page_items(s), ["0", "1", "2", "3", "4"])


class RenderTests(unittest.TestCase):
    def test_header_lists_page_and_total(self):
        s = picker.PickerState(items=["foo", "bar"], page=0)
        text, _ = picker.render(s)
        self.assertIn("page 1/1", text)
        self.assertIn("local", text)

    def test_header_says_remote_when_toggled(self):
        s = picker.PickerState(items=["foo"], include_remote=True)
        text, _ = picker.render(s)
        self.assertIn("local + remote", text)

    def test_header_shows_filter_line_when_query(self):
        s = picker.PickerState(items=["foo"], query="bar")
        text, _ = picker.render(s)
        self.assertIn("Filter: «bar»", text)

    def test_repo_rows_use_pick_callback(self):
        s = picker.PickerState(items=["foo", "bar"])
        _, kb = picker.render(s)
        # First row: "foo" with callback "pick:0"
        self.assertEqual(kb[0][0]["text"], "foo")
        self.assertEqual(kb[0][0]["callback_data"], "pick:0")
        self.assertEqual(kb[1][0]["callback_data"], "pick:1")

    def test_control_row_has_prev_next_and_toggles(self):
        s = picker.PickerState(items=[str(i) for i in range(25)], page=1)
        _, kb = picker.render(s)
        # Find prev/next row
        prev_next = next(row for row in kb if any("Prev" in b["text"] for b in row))
        labels = [b["text"] for b in prev_next]
        self.assertIn("◀ Prev", labels)
        self.assertIn("Next ▶", labels)

    def test_remote_only_rows_get_globe_prefix(self):
        # remote_set marks items that are remote-only
        s = picker.PickerState(
            items=["foo", "bar"], remote_only={"bar"}
        )
        _, kb = picker.render(s)
        self.assertEqual(kb[0][0]["text"], "foo")
        self.assertEqual(kb[1][0]["text"], "🌐 bar")


if __name__ == "__main__":
    unittest.main()
```

- [ ] **Step 2: Run, expect failure**

```bash
python3 -m unittest clrepo-bot/tests/test_picker.py -v
```

Expected: `ModuleNotFoundError: No module named 'picker'`.

- [ ] **Step 3: Implement `clrepo-bot/picker.py`**

```python
"""Pagination state + inline-keyboard rendering."""

from dataclasses import dataclass, field

PAGE_SIZE = 10


@dataclass
class PickerState:
    items: list[str]
    page: int = 0
    include_remote: bool = False
    query: str = ""
    remote_only: set[str] = field(default_factory=set)
    message_id: int | None = None  # Telegram message id, set after first send
    awaiting_query: bool = False


def total_pages(state: PickerState) -> int:
    if not state.items:
        return 1
    return max(1, (len(state.items) + PAGE_SIZE - 1) // PAGE_SIZE)


def clamp_page(state: PickerState) -> int:
    return max(0, min(state.page, total_pages(state) - 1))


def current_page_items(state: PickerState) -> list[str]:
    p = clamp_page(state)
    start = p * PAGE_SIZE
    return state.items[start : start + PAGE_SIZE]


def render(state: PickerState) -> tuple[str, list[list[dict]]]:
    """Return (text, inline_keyboard) for sendMessage / editMessageText."""
    scope = "local + remote" if state.include_remote else "local"
    page_num = clamp_page(state) + 1
    header = f"Pick a repo ({scope}, MRU — page {page_num}/{total_pages(state)})"
    lines = [header]
    if state.query:
        lines.append(f"Filter: «{state.query}»")
    text = "\n".join(lines)

    page_items = current_page_items(state)
    start_idx = clamp_page(state) * PAGE_SIZE
    keyboard: list[list[dict]] = []
    for i, item in enumerate(page_items):
        label = f"🌐 {item}" if item in state.remote_only else item
        keyboard.append([{"text": label, "callback_data": f"pick:{start_idx + i}"}])

    # Pagination row
    nav: list[dict] = []
    if clamp_page(state) > 0:
        nav.append({"text": "◀ Prev", "callback_data": "nav:prev"})
    if clamp_page(state) < total_pages(state) - 1:
        nav.append({"text": "Next ▶", "callback_data": "nav:next"})
    if nav:
        keyboard.append(nav)

    # Toggles row
    remote_label = "🌐 Local only" if state.include_remote else "🌐 Include remote"
    keyboard.append([
        {"text": remote_label, "callback_data": "toggle:remote"},
        {"text": "🔍 Search", "callback_data": "search"},
    ])
    keyboard.append([{"text": "✖ Cancel", "callback_data": "cancel"}])

    return text, keyboard
```

- [ ] **Step 4: Run, expect pass**

```bash
python3 -m unittest clrepo-bot/tests/test_picker.py -v
```

Expected: 9 tests pass.

- [ ] **Step 5: Commit**

```bash
git add clrepo-bot/picker.py clrepo-bot/tests/test_picker.py
git commit -m "feat(bot): picker pagination state + keyboard renderer"
```

---

## Task 6: `tg.py` — Bot API wrapper

**Files:**
- Create: `clrepo-bot/tg.py`

Thin wrappers around `urllib.request` for `getUpdates`, `sendMessage`, `editMessageText`, `answerCallbackQuery`. No tests here — pure I/O with no logic worth mocking. Integration is verified manually in Task 11.

- [ ] **Step 1: Implement `clrepo-bot/tg.py`**

```python
"""Minimal Telegram Bot API client (urllib)."""

import json
import logging
import urllib.error
import urllib.parse
import urllib.request

LOG = logging.getLogger(__name__)


class TelegramAPIError(RuntimeError):
    pass


class Bot:
    def __init__(self, token: str, timeout: int = 35):
        self.token = token
        self.base = f"https://api.telegram.org/bot{token}"
        self.timeout = timeout

    def _call(self, method: str, params: dict | None = None) -> dict:
        url = f"{self.base}/{method}"
        data = None
        if params is not None:
            data = json.dumps(params).encode()
        req = urllib.request.Request(
            url, data=data, method="POST" if data else "GET",
            headers={"Content-Type": "application/json"} if data else {},
        )
        try:
            with urllib.request.urlopen(req, timeout=self.timeout) as resp:
                payload = json.loads(resp.read())
        except urllib.error.URLError as e:
            raise TelegramAPIError(f"{method}: {e}") from e
        if not payload.get("ok"):
            raise TelegramAPIError(f"{method}: {payload}")
        return payload["result"]

    def get_updates(self, offset: int, timeout: int = 30) -> list[dict]:
        return self._call("getUpdates", {
            "offset": offset, "timeout": timeout,
            "allowed_updates": ["message", "callback_query"],
        })

    def send_message(self, chat_id: int, text: str,
                     reply_markup: dict | None = None,
                     parse_mode: str | None = None) -> dict:
        params: dict = {"chat_id": chat_id, "text": text}
        if reply_markup is not None:
            params["reply_markup"] = reply_markup
        if parse_mode is not None:
            params["parse_mode"] = parse_mode
        return self._call("sendMessage", params)

    def edit_message_text(self, chat_id: int, message_id: int, text: str,
                          reply_markup: dict | None = None,
                          parse_mode: str | None = None) -> dict:
        params: dict = {"chat_id": chat_id, "message_id": message_id, "text": text}
        if reply_markup is not None:
            params["reply_markup"] = reply_markup
        if parse_mode is not None:
            params["parse_mode"] = parse_mode
        return self._call("editMessageText", params)

    def answer_callback_query(self, callback_id: str, text: str | None = None) -> None:
        params: dict = {"callback_query_id": callback_id}
        if text is not None:
            params["text"] = text
        self._call("answerCallbackQuery", params)


def fetch_token_from_passbolt(resource_id: str) -> str:
    """Shell out to `passbolt get resource --id <id>`, parse Password line."""
    import subprocess
    out = subprocess.check_output(
        ["passbolt", "get", "resource", "--id", resource_id],
        text=True,
    )
    for line in out.splitlines():
        if line.startswith("Password:"):
            return line.split(":", 1)[1].strip()
    raise TelegramAPIError(f"no Password field in passbolt resource {resource_id}")
```

- [ ] **Step 2: Sanity-check it imports**

```bash
python3 -c "import sys; sys.path.insert(0, 'clrepo-bot'); import tg; print(tg.Bot)"
```

Expected: `<class 'tg.Bot'>` (no traceback).

- [ ] **Step 3: Commit**

```bash
git add clrepo-bot/tg.py
git commit -m "feat(bot): Telegram Bot API wrapper + Passbolt token loader"
```

---

## Task 7: `spawn.py` — detached tmux spawn + slot poll

**Files:**
- Create: `clrepo-bot/spawn.py`
- Test: `clrepo-bot/tests/test_spawn.py`

Tests focus on the pure parts: env scrubbing and the slot-poll predicate. The actual `subprocess.run` call is verified manually in Task 11.

- [ ] **Step 1: Write failing test**

`clrepo-bot/tests/test_spawn.py`:

```python
import json
import os
import tempfile
import time
import unittest
from unittest import mock

import sys, pathlib
sys.path.insert(0, str(pathlib.Path(__file__).resolve().parents[1]))

import spawn


class CleanEnvTests(unittest.TestCase):
    def test_strips_tmux_and_pane_vars(self):
        src = {"HOME": "/h", "PATH": "/p", "TMUX": "x", "TMUX_PANE": "y", "STY": "z"}
        out = spawn.clean_env(src)
        self.assertNotIn("TMUX", out)
        self.assertNotIn("TMUX_PANE", out)
        self.assertNotIn("STY", out)

    def test_strips_claude_sse_port(self):
        src = {"HOME": "/h", "CLAUDE_CODE_SSE_PORT": "1234"}
        out = spawn.clean_env(src)
        self.assertNotIn("CLAUDE_CODE_SSE_PORT", out)

    def test_keeps_home_and_path(self):
        src = {"HOME": "/h", "PATH": "/p", "TMUX": "x"}
        out = spawn.clean_env(src)
        self.assertEqual(out["HOME"], "/h")
        self.assertEqual(out["PATH"], "/p")


class SlotPollTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.NamedTemporaryFile(mode="w", suffix=".json", delete=False)
        json.dump({"slots": {"1": {"repo": "old", "pid": 99}}}, self.tmp)
        self.tmp.close()

    def tearDown(self):
        os.unlink(self.tmp.name)

    def test_returns_slot_when_repo_appears(self):
        # Pre-existing slot 1 has "old"; we want to find the new "clrepo" slot.
        before = json.load(open(self.tmp.name))["slots"]
        # Simulate clrepo writing a new slot:
        with open(self.tmp.name, "w") as fh:
            json.dump({"slots": {
                "1": {"repo": "old", "pid": 99},
                "2": {"repo": "clrepo", "pid": 1234, "session": "clrepo"},
            }}, fh)
        result = spawn.find_new_slot(self.tmp.name, before_keys=set(before), repo="clrepo")
        self.assertIsNotNone(result)
        self.assertEqual(result["slot"], "2")
        self.assertEqual(result["session"], "clrepo")

    def test_returns_none_when_no_new_slot(self):
        before = json.load(open(self.tmp.name))["slots"]
        result = spawn.find_new_slot(self.tmp.name, before_keys=set(before), repo="clrepo")
        self.assertIsNone(result)

    def test_ignores_other_repo_in_new_slot(self):
        before = json.load(open(self.tmp.name))["slots"]
        with open(self.tmp.name, "w") as fh:
            json.dump({"slots": {
                "1": {"repo": "old", "pid": 99},
                "3": {"repo": "other", "pid": 1, "session": "other"},
            }}, fh)
        result = spawn.find_new_slot(self.tmp.name, before_keys=set(before), repo="clrepo")
        self.assertIsNone(result)


if __name__ == "__main__":
    unittest.main()
```

- [ ] **Step 2: Run, expect failure**

```bash
python3 -m unittest clrepo-bot/tests/test_spawn.py -v
```

Expected: `ModuleNotFoundError: No module named 'spawn'`.

- [ ] **Step 3: Implement `clrepo-bot/spawn.py`**

```python
"""Spawn clrepo in detached tmux + poll slots.json for confirmation."""

import json
import logging
import os
import secrets
import shlex
import subprocess
import time
from pathlib import Path

LOG = logging.getLogger(__name__)

SLOTS_PATH = str(Path.home() / ".cache" / "clrepo" / "slots.json")

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


def spawn_clrepo(name: str, extra_args: str = "") -> str:
    """Launch `clrepo <name> <extra_args>` in detached tmux. Return wrapper session name."""
    wrapper = f"clrepo-spawn-{secrets.token_hex(3)}"
    cmdline = f"clrepo {shlex.quote(name)}"
    if extra_args:
        cmdline += " " + extra_args
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
```

- [ ] **Step 4: Run, expect pass**

```bash
python3 -m unittest clrepo-bot/tests/test_spawn.py -v
```

Expected: 6 tests pass.

- [ ] **Step 5: Commit**

```bash
git add clrepo-bot/spawn.py clrepo-bot/tests/test_spawn.py
git commit -m "feat(bot): detached tmux spawn + slots.json confirmation poll"
```

---

## Task 8: `handlers.py` — command dispatch

**Files:**
- Create: `clrepo-bot/handlers.py`
- Test: `clrepo-bot/tests/test_handlers.py`

Handlers take a `Context` (carrying a fake-able `tg.Bot`, state dicts, config). Tests use a fake Bot that records calls — no network. The handlers covered here: `/start`/`/help`, `/new` (no args), `/new <query>`, `/status`, `/cancel`, and the callback-query dispatcher (nav, toggle, search, pick).

The basename-resolution for `/new <name>` (single-match → direct spawn) is covered by `resolve_name`.

- [ ] **Step 1: Write failing test**

`clrepo-bot/tests/test_handlers.py`:

```python
import json
import os
import tempfile
import unittest

import sys, pathlib
sys.path.insert(0, str(pathlib.Path(__file__).resolve().parents[1]))

import handlers
import picker


class FakeBot:
    def __init__(self):
        self.sent: list[dict] = []
        self.edited: list[dict] = []
        self.callbacks_answered: list[dict] = []
        self._next_msg_id = 1000

    def send_message(self, chat_id, text, reply_markup=None, parse_mode=None):
        self._next_msg_id += 1
        self.sent.append({"chat_id": chat_id, "text": text, "reply_markup": reply_markup, "message_id": self._next_msg_id})
        return {"message_id": self._next_msg_id}

    def edit_message_text(self, chat_id, message_id, text, reply_markup=None, parse_mode=None):
        self.edited.append({"chat_id": chat_id, "message_id": message_id, "text": text, "reply_markup": reply_markup})

    def answer_callback_query(self, callback_id, text=None):
        self.callbacks_answered.append({"id": callback_id, "text": text})


def make_ctx(items=("foo", "bar", "baz")):
    return handlers.Context(
        bot=FakeBot(),
        pickers={},
        local_provider=lambda: list(items),
        remote_provider=lambda: [],
        mru_provider=lambda: [],
        spawner=lambda name, extra: {"slot": "2", "session": name},
        kill_session=lambda s: True,
        status_provider=lambda: "status output",
    )


class HelpTests(unittest.TestCase):
    def test_help_sends_one_message_with_command_list(self):
        ctx = make_ctx()
        handlers.cmd_help(ctx, chat_id=1)
        self.assertEqual(len(ctx.bot.sent), 1)
        text = ctx.bot.sent[0]["text"]
        for cmd in ("/new", "/status", "/kill", "/help"):
            self.assertIn(cmd, text)


class NewCommandTests(unittest.TestCase):
    def test_new_no_args_sends_picker_with_all_local(self):
        ctx = make_ctx(items=("aaa", "bbb"))
        handlers.cmd_new(ctx, chat_id=1, args="")
        self.assertEqual(len(ctx.bot.sent), 1)
        kb = ctx.bot.sent[0]["reply_markup"]["inline_keyboard"]
        labels = [row[0]["text"] for row in kb if row[0]["callback_data"].startswith("pick:")]
        self.assertEqual(labels, ["aaa", "bbb"])
        self.assertIn(1, ctx.pickers)

    def test_new_query_filters_results(self):
        ctx = make_ctx(items=("alpha", "alphabet", "beta"))
        handlers.cmd_new(ctx, chat_id=1, args="alph")
        kb = ctx.bot.sent[0]["reply_markup"]["inline_keyboard"]
        labels = [row[0]["text"] for row in kb if row[0]["callback_data"].startswith("pick:")]
        self.assertEqual(sorted(labels), ["alpha", "alphabet"])

    def test_new_exact_single_match_spawns_directly(self):
        spawn_calls = []
        ctx = make_ctx(items=("clrepo", "dotfiles"))
        ctx.spawner = lambda name, extra: (spawn_calls.append((name, extra)) or {"slot": "1", "session": name})
        handlers.cmd_new(ctx, chat_id=1, args="clrepo")
        # When the basename matches exactly one item, spawn directly:
        self.assertEqual(spawn_calls, [("clrepo", "")])
        self.assertTrue(any("Launched" in s["text"] for s in ctx.bot.sent))


class StatusTests(unittest.TestCase):
    def test_status_sends_provider_output(self):
        ctx = make_ctx()
        handlers.cmd_status(ctx, chat_id=1)
        self.assertIn("status output", ctx.bot.sent[0]["text"])


class CallbackTests(unittest.TestCase):
    def test_nav_next_advances_page(self):
        ctx = make_ctx(items=[str(i) for i in range(25)])
        handlers.cmd_new(ctx, chat_id=1, args="")
        st = ctx.pickers[1]
        self.assertEqual(st.page, 0)
        handlers.on_callback(ctx, chat_id=1, callback_id="cb1", data="nav:next", message_id=st.message_id)
        self.assertEqual(ctx.pickers[1].page, 1)
        self.assertEqual(len(ctx.bot.edited), 1)

    def test_toggle_remote_flips_state_and_refreshes_items(self):
        ctx = make_ctx(items=("foo",))
        ctx.remote_provider = lambda: ["forge/x/bar", "forge/x/baz"]
        handlers.cmd_new(ctx, chat_id=1, args="")
        handlers.on_callback(ctx, chat_id=1, callback_id="cb2", data="toggle:remote",
                             message_id=ctx.pickers[1].message_id)
        st = ctx.pickers[1]
        self.assertTrue(st.include_remote)
        # Items now include remote entries
        self.assertIn("forge/x/bar", st.items)

    def test_pick_spawns_and_edits_message(self):
        spawn_calls = []
        ctx = make_ctx(items=("foo", "bar"))
        ctx.spawner = lambda name, extra: (spawn_calls.append(name) or {"slot": "3", "session": name})
        handlers.cmd_new(ctx, chat_id=1, args="")
        st = ctx.pickers[1]
        handlers.on_callback(ctx, chat_id=1, callback_id="cb3", data="pick:1",
                             message_id=st.message_id)
        self.assertEqual(spawn_calls, ["bar"])
        # Picker session is dropped after spawn
        self.assertNotIn(1, ctx.pickers)
        self.assertTrue(any("Launched" in e["text"] for e in ctx.bot.edited))

    def test_cancel_drops_picker(self):
        ctx = make_ctx(items=("foo",))
        handlers.cmd_new(ctx, chat_id=1, args="")
        st = ctx.pickers[1]
        handlers.on_callback(ctx, chat_id=1, callback_id="cb4", data="cancel",
                             message_id=st.message_id)
        self.assertNotIn(1, ctx.pickers)

    def test_search_button_sets_awaiting_query(self):
        ctx = make_ctx(items=("foo",))
        handlers.cmd_new(ctx, chat_id=1, args="")
        st = ctx.pickers[1]
        handlers.on_callback(ctx, chat_id=1, callback_id="cb5", data="search",
                             message_id=st.message_id)
        self.assertTrue(ctx.pickers[1].awaiting_query)


if __name__ == "__main__":
    unittest.main()
```

- [ ] **Step 2: Run, expect failure**

```bash
python3 -m unittest clrepo-bot/tests/test_handlers.py -v
```

Expected: `ModuleNotFoundError: No module named 'handlers'`.

- [ ] **Step 3: Implement `clrepo-bot/handlers.py`**

```python
"""Command + callback handlers. Pure orchestration around an injectable Context."""

import logging
import os
import shlex
from dataclasses import dataclass
from typing import Callable

import picker
import repos

LOG = logging.getLogger(__name__)


@dataclass
class Context:
    bot: object  # tg.Bot or test fake
    pickers: dict  # chat_id -> PickerState
    local_provider: Callable[[], list[str]]
    remote_provider: Callable[[], list[str]]
    mru_provider: Callable[[], list[str]]
    spawner: Callable[[str, str], dict | None]  # (name, extra_args) -> {slot, session} or None
    kill_session: Callable[[str], bool]
    status_provider: Callable[[], str]


HELP_TEXT = (
    "clrepo-bot — DM commands:\n"
    "  /new            Open repo picker (local, MRU)\n"
    "  /new <query>    Filter the picker by query\n"
    "  /new <name>     Launch directly if exactly one match\n"
    "  /status         Show clrepo slot status\n"
    "  /kill <slot>    Kill a slot's tmux session (confirms)\n"
    "  /cancel         Drop the current picker\n"
    "  /help           This message"
)


def _basename(path: str) -> str:
    return os.path.basename(path)


def _items(ctx: Context, include_remote: bool, query: str) -> tuple[list[str], set[str]]:
    local = ctx.local_provider()
    local = repos.order_by_mru(local, ctx.mru_provider())
    remote_only: set[str] = set()
    items = list(local)
    if include_remote:
        for r in ctx.remote_provider():
            if r not in items:
                items.append(r)
                remote_only.add(r)
    return repos.filter_query(items, query), remote_only


def _send_picker(ctx: Context, chat_id: int, state: picker.PickerState) -> None:
    text, kb = picker.render(state)
    result = ctx.bot.send_message(chat_id, text, reply_markup={"inline_keyboard": kb})
    state.message_id = result["message_id"]


def _edit_picker(ctx: Context, chat_id: int, state: picker.PickerState) -> None:
    text, kb = picker.render(state)
    ctx.bot.edit_message_text(chat_id, state.message_id, text,
                              reply_markup={"inline_keyboard": kb})


def cmd_help(ctx: Context, chat_id: int) -> None:
    ctx.bot.send_message(chat_id, HELP_TEXT)


def cmd_new(ctx: Context, chat_id: int, args: str) -> None:
    parts = shlex.split(args) if args else []
    if parts:
        name_or_query = parts[0]
        extra_args = " ".join(shlex.quote(p) for p in parts[1:])
        # Exact-basename single match → direct spawn.
        all_items, _ = _items(ctx, include_remote=False, query="")
        exact = [i for i in all_items if _basename(i).lower() == name_or_query.lower()]
        if len(exact) == 1:
            result = ctx.spawner(_basename(exact[0]), extra_args)
            if result:
                ctx.bot.send_message(
                    chat_id,
                    f"✅ Launched: {exact[0]} → slot {result['slot']} "
                    f"(tmux: {result['session']})",
                )
            else:
                ctx.bot.send_message(
                    chat_id,
                    "⏳ Spawn dispatched. Check /status in a few seconds.",
                )
            return
        # Else open picker filtered by query.
        items, remote_only = _items(ctx, include_remote=False, query=name_or_query)
        state = picker.PickerState(items=items, remote_only=remote_only, query=name_or_query)
    else:
        items, remote_only = _items(ctx, include_remote=False, query="")
        state = picker.PickerState(items=items, remote_only=remote_only)
    ctx.pickers[chat_id] = state
    _send_picker(ctx, chat_id, state)


def cmd_status(ctx: Context, chat_id: int) -> None:
    out = ctx.status_provider()
    ctx.bot.send_message(chat_id, f"<pre>{out}</pre>", parse_mode="HTML")


def cmd_kill(ctx: Context, chat_id: int, args: str) -> None:
    parts = shlex.split(args) if args else []
    if not parts:
        ctx.bot.send_message(chat_id, "Usage: /kill <slot>")
        return
    slot = parts[0]
    ok = ctx.kill_session(slot)
    ctx.bot.send_message(chat_id, f"{'✅' if ok else '❌'} kill slot {slot}: {'done' if ok else 'failed'}")


def cmd_cancel(ctx: Context, chat_id: int) -> None:
    if chat_id in ctx.pickers:
        del ctx.pickers[chat_id]
    ctx.bot.send_message(chat_id, "Cancelled.")


def on_text_message(ctx: Context, chat_id: int, text: str) -> None:
    """Plain text — consumed as filter query if picker is awaiting one."""
    state = ctx.pickers.get(chat_id)
    if not state or not state.awaiting_query:
        ctx.bot.send_message(chat_id, "Unknown input. /help for commands.")
        return
    state.awaiting_query = False
    state.query = text.strip()
    state.page = 0
    items, remote_only = _items(ctx, include_remote=state.include_remote, query=state.query)
    state.items = items
    state.remote_only = remote_only
    _edit_picker(ctx, chat_id, state)


def on_callback(ctx: Context, chat_id: int, callback_id: str, data: str, message_id: int) -> None:
    state = ctx.pickers.get(chat_id)
    if not state or state.message_id != message_id:
        ctx.bot.answer_callback_query(callback_id, "Picker expired — /new to restart")
        return

    if data == "nav:prev":
        state.page = max(0, state.page - 1)
        _edit_picker(ctx, chat_id, state)
        ctx.bot.answer_callback_query(callback_id)
    elif data == "nav:next":
        state.page = min(picker.total_pages(state) - 1, state.page + 1)
        _edit_picker(ctx, chat_id, state)
        ctx.bot.answer_callback_query(callback_id)
    elif data == "toggle:remote":
        state.include_remote = not state.include_remote
        state.page = 0
        items, remote_only = _items(ctx, include_remote=state.include_remote, query=state.query)
        state.items = items
        state.remote_only = remote_only
        _edit_picker(ctx, chat_id, state)
        ctx.bot.answer_callback_query(callback_id)
    elif data == "search":
        state.awaiting_query = True
        ctx.bot.edit_message_text(chat_id, state.message_id,
                                  "Reply with a search query to filter:",
                                  reply_markup=None)
        ctx.bot.answer_callback_query(callback_id)
    elif data == "cancel":
        del ctx.pickers[chat_id]
        ctx.bot.edit_message_text(chat_id, state.message_id, "Cancelled.", reply_markup=None)
        ctx.bot.answer_callback_query(callback_id, "Cancelled")
    elif data.startswith("pick:"):
        idx = int(data.split(":", 1)[1])
        if idx >= len(state.items):
            ctx.bot.answer_callback_query(callback_id, "Out of range")
            return
        target = state.items[idx]
        name = _basename(target)
        ctx.bot.answer_callback_query(callback_id, f"Launching {name}…")
        result = ctx.spawner(name, "")
        if result:
            text = (f"✅ Launched: {target} → slot {result['slot']} "
                    f"(tmux: {result['session']})")
        else:
            text = "⏳ Spawn dispatched. Check /status in a few seconds."
        ctx.bot.edit_message_text(chat_id, state.message_id, text, reply_markup=None)
        del ctx.pickers[chat_id]
    else:
        ctx.bot.answer_callback_query(callback_id, "Unknown action")
```

- [ ] **Step 4: Run, expect pass**

```bash
python3 -m unittest clrepo-bot/tests/test_handlers.py -v
```

Expected: 10 tests pass.

- [ ] **Step 5: Commit**

```bash
git add clrepo-bot/handlers.py clrepo-bot/tests/test_handlers.py
git commit -m "feat(bot): command + callback handlers with injectable context"
```

---

## Task 9: `clrepo_bot.py` — entrypoint, poll loop, glue

**Files:**
- Create: `clrepo-bot/clrepo_bot.py` (executable, shebang `#!/usr/bin/env python3`)

Wires everything: load config, load token from Passbolt, build `Context` with real providers, long-poll loop, auth + rate-limit gate, dispatch, log every event as JSON line, persist `last_update_id`, heartbeat every 60s, handle SIGHUP for reload.

No unit tests — this is glue. Verified by manual smoke test in Task 12.

- [ ] **Step 1: Implement `clrepo-bot/clrepo_bot.py`**

```python
#!/usr/bin/env python3
"""clrepo-bot entrypoint: long-poll Telegram and dispatch to handlers."""

import json
import logging
import os
import signal
import subprocess
import sys
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

LOG = logging.getLogger("clrepo-bot")
PID_PATH = Path.home() / ".cache" / "clrepo" / "clrepo-bot.pid"

# Reload flag set by SIGHUP handler.
_RELOAD = {"want": False}


def _log_event(**kwargs) -> None:
    rec = {"ts": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()), **kwargs}
    print(json.dumps(rec), flush=True)


def _kill_slot(slot: str) -> bool:
    """Kill the tmux session belonging to a clrepo slot."""
    slots = spawn.read_slots()
    entry = slots.get(str(slot))
    if not entry or not entry.get("session"):
        return False
    try:
        subprocess.run(["tmux", "kill-session", "-t", entry["session"]],
                       check=True, env=spawn.clean_env())
        return True
    except subprocess.CalledProcessError:
        return False


def _status() -> str:
    try:
        out = subprocess.run(
            ["bash", "-lc", "clrepo --status"],
            capture_output=True, text=True, timeout=10,
            env=spawn.clean_env(),
        )
        return (out.stdout + out.stderr).strip() or "(empty)"
    except subprocess.SubprocessError as e:
        return f"(error: {e})"


def _spawn_and_confirm(name: str, extra: str) -> dict | None:
    before = set(spawn.read_slots())
    try:
        spawn.spawn_clrepo(name, extra)
    except (FileNotFoundError, subprocess.CalledProcessError) as e:
        _log_event(evt="spawn", repo=name, ok=False, error=str(e))
        return None
    hit = spawn.wait_for_slot(name, before)
    _log_event(evt="spawn", repo=name, ok=bool(hit),
               slot=(hit or {}).get("slot"), session=(hit or {}).get("session"))
    return hit


def build_context(bot: tg.Bot) -> handlers.Context:
    return handlers.Context(
        bot=bot,
        pickers={},
        local_provider=lambda: repos.list_local(),
        remote_provider=lambda: repos.list_remote(),
        mru_provider=lambda: repos.read_mru(),
        spawner=_spawn_and_confirm,
        kill_session=_kill_slot,
        status_provider=_status,
    )


def _refresh_remote_if_stale() -> None:
    """If remote.list is missing or > 10min old, warm it."""
    p = repos.REMOTE_LIST_PATH
    needs = (not os.path.exists(p)) or (time.time() - os.path.getmtime(p) > 600)
    if not needs:
        return
    try:
        subprocess.run(["bash", "-lc", "clrepo --refresh >/dev/null 2>&1"],
                       timeout=30, env=spawn.clean_env())
    except subprocess.SubprocessError:
        pass


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
    PID_PATH.write_text(str(os.getpid()))

    cfg = config.load()
    token = tg.fetch_token_from_passbolt(cfg["passbolt_resource_id"])
    bot = tg.Bot(token)
    ctx = build_context(bot)
    rl = auth.RateLimiter(capacity=20, refill_per_sec=20 / 60.0)

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
```

- [ ] **Step 2: Make executable**

```bash
chmod +x clrepo-bot/clrepo_bot.py
```

- [ ] **Step 3: Sanity-check it imports (no Telegram call)**

```bash
python3 -c "import sys; sys.path.insert(0, 'clrepo-bot'); import clrepo_bot; print(clrepo_bot.main)"
```

Expected: `<function main at 0x…>`.

- [ ] **Step 4: Commit**

```bash
git add clrepo-bot/clrepo_bot.py
git commit -m "feat(bot): main entrypoint with long-poll, auth, rate-limit, heartbeat"
```

---

## Task 10: systemd unit file

**Files:**
- Create: `clrepo-bot/systemd/clrepo-bot.service`

- [ ] **Step 1: Write unit file**

```ini
[Unit]
Description=clrepo Telegram wrapper bot
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=%h/projects/repos/github/freaxnx01/public/clrepo/clrepo-bot/clrepo_bot.py
ExecReload=/bin/kill -HUP $MAINPID
Restart=on-failure
RestartSec=5s
StandardOutput=append:%h/.cache/clrepo/clrepo-bot.log
StandardError=inherit

[Install]
WantedBy=default.target
```

- [ ] **Step 2: Commit**

```bash
git add clrepo-bot/systemd/clrepo-bot.service
git commit -m "feat(bot): systemd --user unit file"
```

---

## Task 11: Extend `setup-claude-channels.sh` with clrepo-bot section

**Files:**
- Modify: `setup-claude-channels.sh` (append a new section after the existing slot-tokens loop, before the final summary line)

- [ ] **Step 1: Read the current end of the file**

```bash
tail -20 setup-claude-channels.sh
```

Confirm the script ends with the `echo "✓ slot-tokens.json: …"` block and `echo "Done. Run 'clrepo --status' to confirm."`.

- [ ] **Step 2: Append the new section**

Edit `setup-claude-channels.sh`. Before the final `echo "Done. Run 'clrepo --status' to confirm."` line, insert:

```bash

# --- 3. clrepo-bot (standalone Telegram wrapper for spawning sessions) ---
echo
echo "clrepo-bot — standalone Telegram bot for spawning new sessions."
echo "  Needs its own BotFather bot + Passbolt resource for the token."
echo "  Press Enter to skip if you don't want to set this up now."

CLREPO_BOT_CFG="$CACHE/clrepo-bot.json"
bot_json=$(json_read "$CLREPO_BOT_CFG")
cur_bot_pb=$(printf '%s' "$bot_json" | json_get passbolt_resource_id)
cur_bot_owner=$(printf '%s' "$bot_json" | json_get telegram_owner_id)
[ -z "$cur_bot_owner" ] && cur_bot_owner="${new_owner:-$cur_owner}"

new_bot_pb=$(prompt_default "  Passbolt resource id for bot token" "$cur_bot_pb")
if [ -z "$new_bot_pb" ]; then
  echo "  (skipped — clrepo-bot not configured)"
else
  if [ "$new_bot_pb" = "$cur_bot_pb" ] || validate_passbolt "$new_bot_pb"; then
    new_bot_owner=$(prompt_default "  Telegram owner user_id (allowlist)" "$cur_bot_owner")
    python3 - "$CLREPO_BOT_CFG" "$new_bot_pb" "$new_bot_owner" <<'PY'
import json, os, sys
path, pb, owner = sys.argv[1], sys.argv[2], int(sys.argv[3])
d = json.load(open(path)) if os.path.exists(path) else {}
d["passbolt_resource_id"] = pb
d["telegram_owner_id"] = owner
d.setdefault("allowlist", [])
if owner not in d["allowlist"]:
    d["allowlist"].append(owner)
d.setdefault("last_update_id", 0)
json.dump(d, open(path + ".tmp", "w"), indent=2, sort_keys=True)
os.replace(path + ".tmp", path)
PY
    echo "  ✓ clrepo-bot.json written"

    UNIT_SRC="$(dirname "$0")/clrepo-bot/systemd/clrepo-bot.service"
    UNIT_DST="$HOME/.config/systemd/user/clrepo-bot.service"
    if [ -f "$UNIT_SRC" ]; then
      read -r -p "  Install + enable systemd --user unit now? [Y/n]: " ans
      case "${ans:-y}" in
        [Yy]*)
          mkdir -p "$(dirname "$UNIT_DST")"
          cp "$UNIT_SRC" "$UNIT_DST"
          systemctl --user daemon-reload
          systemctl --user enable --now clrepo-bot.service
          echo "  ✓ clrepo-bot.service enabled"
          ;;
        *) echo "  (skipped systemd install — run later: systemctl --user enable --now clrepo-bot.service)" ;;
      esac
    fi
  else
    echo "  ✗ Passbolt id did not resolve — clrepo-bot config unchanged" >&2
  fi
fi
```

- [ ] **Step 3: Lint with shellcheck (if available)**

```bash
shellcheck setup-claude-channels.sh || echo "(shellcheck not installed — skipping)"
```

Expected: no new errors introduced by the section (warnings about pre-existing patterns are acceptable).

- [ ] **Step 4: Dry-run by skipping the prompt**

```bash
echo "" | bash -c "set -e; export CLREPO_CACHE=$(mktemp -d); ./setup-claude-channels.sh < <(yes '' | head -20)"
```

This feeds empty answers to all prompts. Expected: script completes without error and the clrepo-bot section prints "(skipped — clrepo-bot not configured)".

- [ ] **Step 5: Commit**

```bash
git add setup-claude-channels.sh
git commit -m "feat(setup): clrepo-bot section in setup-claude-channels.sh"
```

---

## Task 12: Manual smoke test

This is a **manual verification** task — no code or commit. Tick when done.

- [ ] **Step 1: Run all unit tests**

```bash
python3 -m unittest discover clrepo-bot/tests -v
```

Expected: all tests pass (30+).

- [ ] **Step 2: Create the bot via BotFather**

In Telegram, DM @BotFather: `/newbot`, give it a name and username (suggested: `claude_freax_clrepo_bot`). Store the token in Passbolt; note the resource id.

- [ ] **Step 3: Run setup**

```bash
./setup-claude-channels.sh
```

Skip through to the clrepo-bot section. Enter the Passbolt resource id and your Telegram user_id. Accept the systemd install.

- [ ] **Step 4: Verify daemon is up**

```bash
systemctl --user status clrepo-bot.service
tail -n 20 ~/.cache/clrepo/clrepo-bot.log
```

Expected: `active (running)`; log shows `{"evt":"start",...}`.

- [ ] **Step 5: Smoke-test commands in Telegram**

DM the bot:

1. `/help` → command list reply.
2. `/status` → output of `clrepo --status`.
3. `/new` → picker message with inline keyboard.
4. Tap `[Next ▶]` if pagination present → message edits in place.
5. Tap `[🌐 Include remote]` → header changes to "local + remote", more items.
6. Tap `[🔍 Search]` → prompt to reply; reply `clrepo` → picker re-renders filtered.
7. Tap a repo row → `✅ Launched: <repo> → slot N (tmux: <session>)`.
8. SSH to claude-dev, run `tmux a -t <session>` → attached to a fresh Claude session.

- [ ] **Step 6: Verify auth**

From a Telegram account **not** in `allowlist`, DM the bot. Expected: no reply. `~/.cache/clrepo/clrepo-bot.log` shows `{"evt":"unauthorized","user":…}`.

- [ ] **Step 7: Verify reload**

```bash
systemctl --user reload clrepo-bot.service
tail -n 5 ~/.cache/clrepo/clrepo-bot.log
```

Expected: `{"evt":"reload","ok":true}` line.

---

## Task 13: Wire-up docs

**Files:**
- Modify: `README.md` (top-level) — one paragraph under the existing slot/telegram section pointing at `clrepo-bot/`
- Modify: `ideas.md` — remove anything this superseded (none currently apply, but check)

- [ ] **Step 1: Find the slot/telegram section in `README.md`**

```bash
grep -n "Integration point for slot/telegram\|## Bootstrap and channel wiring" README.md
```

- [ ] **Step 2: Append paragraph after that section**

After the "Bootstrap and channel wiring" block, insert:

```markdown
## clrepo-bot — Telegram wrapper for spawning

`clrepo-bot/` ships a standalone Telegram bot that wraps `clrepo` on the host.
DM it `/new` to get a paginated picker of local (and optionally remote) repos;
tap a row to launch a fresh Claude session in detached tmux. Independent of
bot0/admin and the per-slot Telegram bots; no Claude in the command loop.

Setup is part of `setup-claude-channels.sh`. See
[`clrepo-bot/README.md`](clrepo-bot/README.md) and the design at
[`docs/specs/2026-05-24-clrepo-telegram-bot-design.md`](docs/specs/2026-05-24-clrepo-telegram-bot-design.md).
```

- [ ] **Step 3: Commit**

```bash
git add README.md
git commit -m "docs: point top-level README at clrepo-bot"
```

---

## Done

After Task 13, the feature is shipped: a standalone Telegram bot that wraps `clrepo` end-to-end, with unit-tested pure pieces, an integration-tested live daemon, an automated setup path, and docs.

**Total commits:** 12 (one per task except Task 12 which is manual verification).

**Follow-up parking lot** (from spec, not in this plan):

- Worktree picker UI
- Repo creation (Ctrl-N equivalent)
- Repo deletion
- `/refresh` command
- `clrepo --doctor` consumer for the heartbeat
- Live bot title updates
