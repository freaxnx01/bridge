"""Minimal Telegram Bot API client (urllib)."""

import json
import logging
import subprocess
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
    out = subprocess.check_output(
        ["passbolt", "get", "resource", "--id", resource_id],
        text=True,
    )
    for line in out.splitlines():
        if line.startswith("Password:"):
            return line.split(":", 1)[1].strip()
    raise TelegramAPIError(f"no Password field in passbolt resource {resource_id}")
