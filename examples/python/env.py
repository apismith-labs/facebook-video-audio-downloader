"""Nearest ``.env`` discovery shared by the two command line tools.

The Node client resolves ``.env`` from the repository root, but Python, Go, and Java have no such
anchor. All three walk up from the working directory instead, so running a script from
``examples/python`` still finds the repository ``.env`` and the four clients agree on what they
read.
"""
from __future__ import annotations

import os
from pathlib import Path

#: Bounded so a stray ``.env`` several directories up is never picked up by accident.
MAX_DEPTH = 4


def find_dotenv(start: str | Path | None = None) -> Path | None:
    """Return the nearest ``.env`` at or above ``start`` (defaults to the working directory)."""
    directory = Path(start).expanduser().resolve() if start is not None else Path.cwd()
    if directory.is_file():
        directory = directory.parent
    for _ in range(MAX_DEPTH + 1):
        candidate = directory / ".env"
        if candidate.is_file():
            return candidate
        parent = directory.parent
        if parent == directory:
            return None
        directory = parent
    return None


def load_env_file(path: str | Path | None = None) -> Path | None:
    """Load ``APIFY_*`` keys from ``path`` or the nearest ``.env``.

    Values already present in the environment always win, and ``APIFY_SKIP_DOTENV=1`` disables the
    file entirely so tests can never pick up a developer's real token. Returns the file that was
    read, or ``None``.
    """
    if os.getenv("APIFY_SKIP_DOTENV", "").strip().lower() in {"1", "true", "yes"}:
        return None
    target = Path(path).expanduser() if path is not None else find_dotenv()
    if target is None or not target.is_file():
        return None
    for line in target.read_text(encoding="utf-8").splitlines():
        line = line.strip()
        if not line or line.startswith("#") or "=" not in line:
            continue
        key, _, value = line.partition("=")
        key, value = key.strip(), value.strip().strip("'\"")
        if key.startswith("APIFY_") and key not in os.environ:
            os.environ[key] = value
    return target
