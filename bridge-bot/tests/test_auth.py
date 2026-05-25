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
