import sys
import pathlib
import unittest
from unittest import mock

sys.path.insert(0, str(pathlib.Path(__file__).resolve().parents[1]))

import bridge_bot


class KillSlotTests(unittest.TestCase):
    def test_kills_list_format_slot_by_id(self):
        # Current bridge list entries have no `session`; tmux name == id == key.
        slots = {"agent-dev-lxc-wt-bots": {
            "id": "agent-dev-lxc-wt-bots", "repo": "agent-dev-lxc", "worktree": "bots"}}
        with mock.patch.object(bridge_bot.spawn, "read_slots", return_value=slots), \
             mock.patch.object(bridge_bot.subprocess, "run") as run:
            run.return_value = mock.Mock()
            ok = bridge_bot._kill_slot("agent-dev-lxc-wt-bots")
        self.assertTrue(ok)
        self.assertEqual(run.call_args.args[0],
                         ["tmux", "kill-session", "-t", "agent-dev-lxc-wt-bots"])

    def test_kills_dict_format_slot_by_session(self):
        slots = {"2": {"repo": "bridge", "session": "bridge"}}
        with mock.patch.object(bridge_bot.spawn, "read_slots", return_value=slots), \
             mock.patch.object(bridge_bot.subprocess, "run") as run:
            run.return_value = mock.Mock()
            ok = bridge_bot._kill_slot("2")
        self.assertTrue(ok)
        self.assertEqual(run.call_args.args[0][-1], "bridge")

    def test_unknown_slot_returns_false(self):
        with mock.patch.object(bridge_bot.spawn, "read_slots", return_value={}):
            self.assertFalse(bridge_bot._kill_slot("nope"))


if __name__ == "__main__":
    unittest.main()
