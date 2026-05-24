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
