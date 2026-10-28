#!/usr/bin/env python3
"""Fail when a relative Markdown link points at a file that does not exist."""
from __future__ import annotations

import pathlib
import re
import sys

LINK = re.compile(r"\[[^\]]*\]\(([^)]+)\)")
SKIP = ("http://", "https://", "mailto:")


def main() -> int:
    root = pathlib.Path(__file__).resolve().parents[1]
    broken: list[str] = []
    for page in sorted(root.rglob("*.md")):
        if "node_modules" in page.parts or ".git" in page.parts:
            continue
        text = page.read_text(encoding="utf-8")
        for match in LINK.finditer(text):
            target = match.group(1).split("#", 1)[0]
            if not target or target.startswith(SKIP):
                continue
            if not (page.parent / target).resolve().exists():
                broken.append(f"{page.relative_to(root)}: {match.group(1)}")
    if broken:
        print("\n".join(broken))
        return 1
    print("all relative links resolve")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
