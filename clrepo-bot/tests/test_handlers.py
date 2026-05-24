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
