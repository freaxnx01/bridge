import os
import tempfile
import unittest
from pathlib import Path

import sys, pathlib
sys.path.insert(0, str(pathlib.Path(__file__).resolve().parents[1]))

import repos


class LocalReposTests(unittest.TestCase):
    def setUp(self):
        self.root = tempfile.mkdtemp()
        for rel in ["github/me/public/foo", "github/me/public/bar", "gitlab/me/baz"]:
            os.makedirs(os.path.join(self.root, rel, ".git"))

    def test_walks_git_dirs_and_strips_prefix(self):
        found = repos.list_local(self.root)
        self.assertEqual(
            sorted(found),
            ["github/me/public/bar", "github/me/public/foo", "gitlab/me/baz"],
        )

    def test_mru_ordering_puts_recent_first(self):
        mru_lines = ["gitlab/me/baz", "github/me/public/foo"]
        out = repos.order_by_mru(
            ["github/me/public/foo", "github/me/public/bar", "gitlab/me/baz"],
            mru_lines,
        )
        self.assertEqual(
            out,
            ["gitlab/me/baz", "github/me/public/foo", "github/me/public/bar"],
        )

    def test_filter_query_matches_basename_case_insensitive(self):
        items = ["github/me/public/clrepo", "github/me/public/dotfiles", "gitlab/me/CLRepo-x"]
        self.assertEqual(
            sorted(repos.filter_query(items, "clrepo")),
            ["github/me/public/clrepo", "gitlab/me/CLRepo-x"],
        )

    def test_filter_query_empty_returns_all(self):
        items = ["a", "b"]
        self.assertEqual(repos.filter_query(items, ""), ["a", "b"])


class RemoteReposTests(unittest.TestCase):
    def test_reads_remote_list_lines(self):
        with tempfile.NamedTemporaryFile("w", delete=False) as tmp:
            tmp.write("github/me/public/foo\ngithub/me/public/bar\n")
            path = tmp.name
        try:
            out = repos.list_remote(path)
            self.assertEqual(out, ["github/me/public/foo", "github/me/public/bar"])
        finally:
            os.unlink(path)

    def test_missing_remote_file_returns_empty(self):
        self.assertEqual(repos.list_remote("/nonexistent/path"), [])


if __name__ == "__main__":
    unittest.main()
