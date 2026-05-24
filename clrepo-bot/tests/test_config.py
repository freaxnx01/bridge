import json
import os
import tempfile
import unittest
from unittest import mock

import sys, pathlib
sys.path.insert(0, str(pathlib.Path(__file__).resolve().parents[1]))

import config


class ConfigTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.NamedTemporaryFile(mode="w", suffix=".json", delete=False)
        json.dump({
            "passbolt_resource_id": "abc-123",
            "telegram_owner_id": 42,
            "allowlist": [42, 99],
            "last_update_id": 100,
        }, self.tmp)
        self.tmp.close()
        self.patcher = mock.patch.object(config, "CONFIG_PATH", self.tmp.name)
        self.patcher.start()

    def tearDown(self):
        self.patcher.stop()
        if os.path.exists(self.tmp.name):
            os.unlink(self.tmp.name)

    def test_load_returns_dict_with_expected_keys(self):
        c = config.load()
        self.assertEqual(c["passbolt_resource_id"], "abc-123")
        self.assertEqual(c["telegram_owner_id"], 42)
        self.assertEqual(c["allowlist"], [42, 99])
        self.assertEqual(c["last_update_id"], 100)

    def test_set_last_update_id_persists_atomically(self):
        config.set_last_update_id(250)
        with open(self.tmp.name) as fh:
            d = json.load(fh)
        self.assertEqual(d["last_update_id"], 250)
        # Other fields preserved.
        self.assertEqual(d["allowlist"], [42, 99])

    def test_load_raises_friendly_error_when_missing(self):
        os.unlink(self.tmp.name)
        with self.assertRaises(config.ConfigError):
            config.load()


if __name__ == "__main__":
    unittest.main()
