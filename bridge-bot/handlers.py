"""Command + callback handlers. Pure orchestration around an injectable Context."""

import logging
import os
import re
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
    spawner: Callable[[str, list[str] | None], dict | None]  # (name, extra_args) -> {slot, session} or None
    kill_session: Callable[[str], bool]
    status_provider: Callable[[], str]
    repo_creator: Callable[[str, str, bool], dict | None]


HELP_TEXT = (
    "bridge-bot — DM commands:\n"
    "  /new            Open repo picker (local, MRU)\n"
    "  /new <query>    Filter the picker by query\n"
    "  /new <name>     Launch directly if exactly one match\n"
    "  /newrepo <name> Create a new repo (Forgejo/GitHub)\n"
    "  /status         Show bridge slot status\n"
    "  /kill <slot>    Kill a slot's tmux session (confirms)\n"
    "  /cancel         Drop the current picker\n"
    "  /help           This message"
)


_REPO_NAME_RE = re.compile(r"^[A-Za-z0-9][A-Za-z0-9._-]*$")


def cmd_newrepo(ctx: Context, chat_id: int, args: str) -> None:
    name = args.strip()
    if not name:
        ctx.bot.send_message(chat_id, "Usage: /newrepo <name>")
        return
    if not _REPO_NAME_RE.match(name):
        ctx.bot.send_message(chat_id, "Invalid name (allowed: A-Za-z0-9._-)")
        return
    ctx.bot.send_message(
        chat_id, f'Create "{name}" where?',
        reply_markup={"inline_keyboard": [
            [{"text": "Forgejo · Private", "callback_data": f"newrepo:forgejo:private:{name}"},
             {"text": "Forgejo · Public", "callback_data": f"newrepo:forgejo:public:{name}"}],
            [{"text": "GitHub · Private", "callback_data": f"newrepo:github:private:{name}"},
             {"text": "GitHub · Public", "callback_data": f"newrepo:github:public:{name}"}],
        ]},
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
        extra_args_list = parts[1:]
        # Exact-basename single match → direct spawn.
        all_items, _ = _items(ctx, include_remote=False, query="")
        exact = [i for i in all_items if _basename(i).lower() == name_or_query.lower()]
        if len(exact) == 1:
            result = ctx.spawner(_basename(exact[0]), extra_args_list or None)
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
    ctx.bot.send_message(
        chat_id,
        f"Kill slot {slot}? This will terminate the Claude session.",
        reply_markup={"inline_keyboard": [[
            {"text": "✅ Confirm", "callback_data": f"kill_confirm:{slot}"},
            {"text": "✖ Cancel", "callback_data": f"kill_cancel:{slot}"},
        ]]},
    )


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
    if data.startswith("newrepo:"):
        _, forge, vis, name = data.split(":", 3)
        ctx.bot.answer_callback_query(callback_id, f"Creating {name}…")
        result = ctx.repo_creator(name, forge, vis == "private")
        if result:
            ctx.bot.edit_message_text(
                chat_id, message_id,
                f"✅ Created + cloned: {result['full_name']} ({forge})",
                reply_markup={"inline_keyboard": [[
                    {"text": "🚀 Launch session", "callback_data": f"newrepo_launch:{name}"}]]},
            )
        else:
            ctx.bot.edit_message_text(
                chat_id, message_id, f"❌ Create failed for {name}",
                reply_markup={"inline_keyboard": []})
        return
    if data.startswith("newrepo_launch:"):
        name = data.split(":", 1)[1]
        ctx.bot.answer_callback_query(callback_id, f"Launching {name}…")
        result = ctx.spawner(name, [])
        if result:
            text = f"✅ Launched: {name} → slot {result['slot']} (tmux: {result['session']})"
        else:
            text = "⏳ Spawn dispatched. Check /status in a few seconds."
        ctx.bot.edit_message_text(chat_id, message_id, text, reply_markup={"inline_keyboard": []})
        return
    if data.startswith("kill_confirm:"):
        slot = data.split(":", 1)[1]
        ok = ctx.kill_session(slot)
        ctx.bot.answer_callback_query(callback_id)
        ctx.bot.edit_message_text(
            chat_id, message_id,
            f"{'✅' if ok else '❌'} kill slot {slot}: {'done' if ok else 'failed'}",
            reply_markup={"inline_keyboard": []},
        )
        return
    elif data.startswith("kill_cancel:"):
        ctx.bot.answer_callback_query(callback_id, "Cancelled")
        ctx.bot.edit_message_text(chat_id, message_id, "Cancelled.", reply_markup={"inline_keyboard": []})
        return

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
                                  reply_markup={"inline_keyboard": []})
        ctx.bot.answer_callback_query(callback_id)
    elif data == "cancel":
        del ctx.pickers[chat_id]
        ctx.bot.edit_message_text(chat_id, state.message_id, "Cancelled.", reply_markup={"inline_keyboard": []})
        ctx.bot.answer_callback_query(callback_id, "Cancelled")
    elif data.startswith("pick:"):
        try:
            idx = int(data.split(":", 1)[1])
        except (ValueError, IndexError):
            ctx.bot.answer_callback_query(callback_id, "Bad payload")
            return
        if idx >= len(state.items):
            ctx.bot.answer_callback_query(callback_id, "Out of range")
            return
        target = state.items[idx]
        name = _basename(target)
        ctx.bot.answer_callback_query(callback_id, f"Launching {name}…")
        result = ctx.spawner(name, [])
        if result:
            text = (f"✅ Launched: {target} → slot {result['slot']} "
                    f"(tmux: {result['session']})")
        else:
            text = "⏳ Spawn dispatched. Check /status in a few seconds."
        ctx.bot.edit_message_text(chat_id, state.message_id, text, reply_markup={"inline_keyboard": []})
        del ctx.pickers[chat_id]
    else:
        ctx.bot.answer_callback_query(callback_id, "Unknown action")
