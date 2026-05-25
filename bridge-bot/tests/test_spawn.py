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
        before = json.load(open(self.tmp.name))["slots"]
        with open(self.tmp.name, "w") as fh:
            json.dump({"slots": {
                "1": {"repo": "old", "pid": 99},
                "2": {"repo": "bridge", "pid": 1234, "session": "bridge"},
            }}, fh)
        result = spawn.find_new_slot(self.tmp.name, before_keys=set(before), repo="bridge")
        self.assertIsNotNone(result)
        self.assertEqual(result["slot"], "2")
        self.assertEqual(result["session"], "bridge")

    def test_returns_none_when_no_new_slot(self):
        before = json.load(open(self.tmp.name))["slots"]
        result = spawn.find_new_slot(self.tmp.name, before_keys=set(before), repo="bridge")
        self.assertIsNone(result)

    def test_ignores_other_repo_in_new_slot(self):
        before = json.load(open(self.tmp.name))["slots"]
        with open(self.tmp.name, "w") as fh:
            json.dump({"slots": {
                "1": {"repo": "old", "pid": 99},
                "3": {"repo": "other", "pid": 1, "session": "other"},
            }}, fh)
        result = spawn.find_new_slot(self.tmp.name, before_keys=set(before), repo="bridge")
        self.assertIsNone(result)


if __name__ == "__main__":
    unittest.main()
