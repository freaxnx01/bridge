"""Repo list assembly — reads clrepo caches, never writes."""

import os
from pathlib import Path

DEFAULT_ROOT = str(Path.home() / "projects" / "repos")
MRU_PATH = str(Path.home() / ".cache" / "clrepo" / "mru")
REMOTE_LIST_PATH = str(Path.home() / ".cache" / "clrepo" / "remote.list")


def list_local(root: str = DEFAULT_ROOT) -> list[str]:
    """Walk `root` for `.git` dirs, return repo paths relative to `root`."""
    out: list[str] = []
    for dirpath, dirnames, _ in os.walk(root):
        if "_archive" in dirnames:
            dirnames.remove("_archive")
        if ".git" in dirnames:
            dirnames[:] = []  # don't recurse into the repo
            out.append(os.path.relpath(dirpath, root))
    return sorted(out)


def order_by_mru(items: list[str], mru: list[str]) -> list[str]:
    """Sort items: MRU order first (for items present in mru), then the rest in original order."""
    mru_set = [m for m in mru if m in items]
    leftover = [i for i in items if i not in mru_set]
    return mru_set + leftover


def filter_query(items: list[str], query: str) -> list[str]:
    """Case-insensitive substring match against basename (final path component)."""
    q = query.strip().lower()
    if not q:
        return list(items)
    return [i for i in items if q in os.path.basename(i).lower()]


def list_remote(path: str = REMOTE_LIST_PATH) -> list[str]:
    if not os.path.exists(path):
        return []
    with open(path) as fh:
        return [line.strip() for line in fh if line.strip()]


def read_mru(path: str = MRU_PATH) -> list[str]:
    if not os.path.exists(path):
        return []
    with open(path) as fh:
        return [line.strip() for line in fh if line.strip()]
