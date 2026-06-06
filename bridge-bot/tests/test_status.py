import json
import sys
import pathlib
import unittest
from unittest import mock

sys.path.insert(0, str(pathlib.Path(__file__).resolve().parents[1]))

import bridge_bot
import tg


class SetMyCommandsTests(unittest.TestCase):
    def test_posts_to_set_my_commands(self):
        bot = tg.Bot("123:abc")
        captured = {}
        bot._call = lambda method, params=None: captured.update(
            method=method, params=params) or {}
        bot.set_my_commands([{"command": "new", "description": "d"}])
        self.assertEqual(captured["method"], "setMyCommands")
        self.assertEqual(captured["params"]["commands"][0]["command"], "new")

    def test_bot_commands_match_dispatcher(self):
        # Guard: every advertised command (except help/start aliases) must be
        # dispatched in _handle_message.
        advertised = {c["command"] for c in bridge_bot.BOT_COMMANDS}
        self.assertEqual(advertised, {"new", "status", "kill", "cancel", "help"})


class StatusTests(unittest.TestCase):
    def _fake_run(self, stdout):
        return mock.Mock(stdout=stdout, stderr="")

    def test_sessions_table_formats_rows(self):
        rows = json.dumps([
            {"slot_id": "alpha-wt-bots", "state": "attached",
             "last_activity": "2026-06-06T23:47:34+02:00"},
            {"slot_id": "beta", "state": "detached",
             "last_activity": "2026-06-05T16:54:56+02:00"},
        ])
        with mock.patch.object(bridge_bot.subprocess, "run",
                               return_value=self._fake_run(rows)):
            out = bridge_bot._sessions_table()
        self.assertIn("alpha-wt-bots", out)
        self.assertIn("attached", out)
        self.assertIn("2026-06-06 23:47", out)  # T -> space, trimmed to minute
        # aligned: both rows share the same column start for state
        lines = out.splitlines()
        self.assertEqual(lines[0].index("attached"), lines[1].index("detached"))

    def test_sessions_table_empty(self):
        with mock.patch.object(bridge_bot.subprocess, "run",
                               return_value=self._fake_run("[]")):
            self.assertEqual(bridge_bot._sessions_table(), "(no live sessions)")

    def test_status_combines_summary_and_table(self):
        with mock.patch.object(bridge_bot, "_bridge_summary",
                               return_value="sessions:  1"), \
             mock.patch.object(bridge_bot, "_sessions_table",
                               return_value="alpha  attached  x"):
            out = bridge_bot._status()
        self.assertIn("sessions:  1", out)
        self.assertIn("Sessions:", out)
        self.assertIn("alpha  attached", out)


if __name__ == "__main__":
    unittest.main()
