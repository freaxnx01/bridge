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
