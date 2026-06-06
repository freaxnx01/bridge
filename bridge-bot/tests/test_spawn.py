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


class ListFormatSlotTests(unittest.TestCase):
    """Current bridge writes slots as a list of {id,repo,worktree,...} (session==id)."""

    def setUp(self):
        self.tmp = tempfile.NamedTemporaryFile(mode="w", suffix=".json", delete=False)
        json.dump({"slots": [{"id": "old-wt-x", "repo": "old", "worktree": "x"}]}, self.tmp)
        self.tmp.close()

    def tearDown(self):
        os.unlink(self.tmp.name)

    def test_read_slots_normalizes_list_to_dict_keyed_by_id(self):
        slots = spawn.read_slots(self.tmp.name)
        self.assertEqual(set(slots), {"old-wt-x"})
        self.assertEqual(slots["old-wt-x"]["repo"], "old")

    def test_find_new_slot_detects_list_entry_session_defaults_to_id(self):
        before = set(spawn.read_slots(self.tmp.name))
        with open(self.tmp.name, "w") as fh:
            json.dump({"slots": [
                {"id": "old-wt-x", "repo": "old", "worktree": "x"},
                {"id": "bridge-wt-main", "repo": "bridge", "worktree": "main"},
            ]}, fh)
        result = spawn.find_new_slot(self.tmp.name, before_keys=before, repo="bridge")
        self.assertIsNotNone(result)
        self.assertEqual(result["slot"], "bridge-wt-main")
        self.assertEqual(result["session"], "bridge-wt-main")  # session defaults to id

    def test_find_new_slot_list_no_match_returns_none(self):
        before = set(spawn.read_slots(self.tmp.name))
        with open(self.tmp.name, "w") as fh:
            json.dump({"slots": [
                {"id": "old-wt-x", "repo": "old", "worktree": "x"},
                {"id": "other-wt-y", "repo": "other", "worktree": "y"},
            ]}, fh)
        self.assertIsNone(
            spawn.find_new_slot(self.tmp.name, before_keys=before, repo="bridge"))


class SpawnBridgeTests(unittest.TestCase):
    def _captured_cmdline(self, mock_run):
        # subprocess.run was called with positional args=[tmux ... bash -lc <cmdline>]
        args = mock_run.call_args_list[0].args[0]
        # cmdline is the last positional arg
        return args[-1]

    def test_cmdline_contains_agent_claude(self):
        # Post-cutover regression guard: without --agent claude, bare `bridge
        # <name>` only emits a cd: directive and the wrapper exits without
        # spawning anything. See issue #60.
        with mock.patch("spawn.subprocess.run") as run:
            spawn.spawn_bridge("bridge")
            cmdline = self._captured_cmdline(run)
        self.assertIn("--agent", cmdline)
        self.assertIn("claude", cmdline)
        self.assertIn("open", cmdline)
        self.assertIn("bridge", cmdline)

    def test_cmdline_passes_extra_args_after_agent(self):
        with mock.patch("spawn.subprocess.run") as run:
            spawn.spawn_bridge("bridge", ["-w", "feature-x"])
            cmdline = self._captured_cmdline(run)
        # Extra args come after the agent flag.
        self.assertIn("--agent claude", cmdline)
        self.assertIn("-w feature-x", cmdline)

    def test_custom_agent(self):
        with mock.patch("spawn.subprocess.run") as run:
            spawn.spawn_bridge("bridge", agent="opencode")
            cmdline = self._captured_cmdline(run)
        self.assertIn("--agent opencode", cmdline)

    def test_quotes_name_safely(self):
        with mock.patch("spawn.subprocess.run") as run:
            # Shouldn't actually be a realistic name but spawn must not let it
            # word-split or inject.
            spawn.spawn_bridge("repo with spaces")
            cmdline = self._captured_cmdline(run)
        # shlex.quote produces single-quoted form for names with spaces.
        self.assertIn("'repo with spaces'", cmdline)


if __name__ == "__main__":
    unittest.main()
