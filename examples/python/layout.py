"""Output layout: deterministic folder names and the sidecar files written next to the media.

Every language client produces the same layout, so a download started with Python can be resumed
or inspected by the Node, Go, or Java examples::

    <out>/<folder>/
        video.mp4          requested with --video (default on)
        audio.mp4          requested with --audio; audio-only MP4, or .m4a/.mp3 after a remux
        transcript.txt     plain text, empty when the video has no speech
        segments.json      [{start, end, text}, ...] in seconds
        metadata.json      Actor item with signed URLs reduced to scheme://host/path
        manifest.json      what was written, when, from which Run, with sizes and SHA-256
"""
from __future__ import annotations

import json
import os
import re
import unicodedata
from datetime import datetime, timezone
from pathlib import Path
from typing import Any
from urllib.parse import urlsplit

MANIFEST_SCHEMA_VERSION = 1
_TITLE_LIMIT = 48
_MEDIA_FIELDS = ("videoUrl", "audioUrl", "avatarUri")


def utc_now() -> str:
    return datetime.now(timezone.utc).isoformat(timespec="seconds").replace("+00:00", "Z")


def slugify(value: str | None, limit: int = _TITLE_LIMIT) -> str:
    """ASCII-only, lowercase, dash-separated slug. Non-ASCII titles degrade to an empty string."""
    if not value:
        return ""
    normalized = unicodedata.normalize("NFKD", value)
    ascii_only = normalized.encode("ascii", "ignore").decode("ascii")
    slug = re.sub(r"[^A-Za-z0-9]+", "-", ascii_only).strip("-").lower()
    if len(slug) > limit:
        slug = slug[:limit].rstrip("-")
    return slug


def video_identity(canonical_url: str, url_form: str) -> str:
    """Stable identity for the folder name, derived from the canonical URL.

    The identity never depends on the title, so renaming a video upstream does not fork the folder.
    """
    parsed = urlsplit(canonical_url)
    path = parsed.path or "/"
    if url_form == "watch":
        match = re.search(r"(?:^|&)v=(\d+)(?:&|$)", parsed.query)
        return f"watch-{match.group(1)}" if match else "watch-unknown"
    if url_form == "video_php":
        match = re.search(r"(?:^|&)v=(\d+)(?:&|$)", parsed.query)
        return f"video-{match.group(1)}" if match else "video-unknown"
    match = re.fullmatch(r"/([A-Za-z0-9._-]+)/videos/(\d+)/?", path)
    if match and url_form == "page_video":
        return f"page-{slugify(match.group(1), 24)}-{match.group(2)}"
    match = re.fullmatch(r"/videos/(\d+)/?", path)
    if match:
        return f"video-{match.group(1)}"
    match = re.fullmatch(r"/reel/(\d+)/?", path)
    if match:
        return f"reel-{match.group(1)}"
    match = re.fullmatch(r"/share/([A-Za-z]+)/([A-Za-z0-9_-]+)/?", path, re.IGNORECASE)
    if match:
        return f"share-{match.group(1).lower()}-{match.group(2)}"
    if url_form == "short_link":
        match = re.fullmatch(r"/([A-Za-z0-9_-]+)/?", path)
        if match:
            return f"short-{match.group(1)}"
    return f"{url_form or 'video'}-unknown"


def folder_name(canonical_url: str, url_form: str, title: str | None = None) -> str:
    """Deterministic folder name: identity plus an optional readable title suffix."""
    identity = video_identity(canonical_url, url_form)
    suffix = slugify(title)
    return f"{identity}-{suffix}" if suffix else identity


def choose_output_dir(base: str | Path, folder: str, canonical_url: str) -> Path:
    """Return an existing compatible folder, or a fresh one that does not collide.

    Re-running the same URL reuses its folder. A different URL that happens to slug to the same
    name gets a numeric suffix instead of overwriting unrelated media.
    """
    base = Path(base)
    candidate = base / folder
    index = 2
    while True:
        manifest = candidate / "manifest.json"
        if not candidate.exists() or not manifest.is_file():
            return candidate
        try:
            recorded = json.loads(manifest.read_text(encoding="utf-8"))
        except (OSError, json.JSONDecodeError):
            return candidate
        if recorded.get("canonicalUrl") in (None, canonical_url):
            return candidate
        candidate = base / f"{folder}-{index}"
        index += 1


def sanitize_item(item: dict[str, Any], *, keep_media_urls: bool = False) -> dict[str, Any]:
    """Copy an Actor item, reducing signed media URLs to ``scheme://host/path``."""
    if not isinstance(item, dict):
        return {}
    cleaned = dict(item)
    for field in _MEDIA_FIELDS:
        value = cleaned.get(field)
        if isinstance(value, str) and value and not keep_media_urls:
            parsed = urlsplit(value)
            if parsed.netloc:
                cleaned[field] = f"{parsed.scheme}://{parsed.netloc}{parsed.path}"
    return cleaned


def transcript_segments(segments: list[dict[str, Any]]) -> list[dict[str, Any]]:
    """Keep only well-formed segments; timestamps are seconds relative to the video."""
    result = []
    for entry in segments or []:
        if not isinstance(entry, dict):
            continue
        start, end, text = entry.get("start"), entry.get("end"), entry.get("text")
        if isinstance(start, bool) or isinstance(end, bool):
            continue
        if not isinstance(start, (int, float)) or not isinstance(end, (int, float)):
            continue
        if not isinstance(text, str):
            continue
        if start < 0 or end < start:
            continue
        result.append({"start": float(start), "end": float(end), "text": text})
    return result


def write_text(path: str | Path, text: str) -> int:
    """Write UTF-8 text atomically (temp file + replace) and return the byte count."""
    path = Path(path)
    path.parent.mkdir(parents=True, exist_ok=True)
    temporary = path.with_name(path.name + ".tmp")
    data = text.encode("utf-8")
    with temporary.open("wb") as stream:
        stream.write(data)
        stream.flush()
        os.fsync(stream.fileno())
    os.replace(temporary, path)
    return len(data)


def write_json(path: str | Path, payload: Any) -> int:
    return write_text(path, json.dumps(payload, ensure_ascii=False, indent=2) + "\n")


def build_manifest(
    *,
    source_url: str,
    canonical_url: str,
    url_form: str,
    result: Any,
    assets: list[dict[str, Any]],
    transcript_bytes: int | None,
    segment_count: int,
) -> dict[str, Any]:
    """Assemble ``manifest.json``. No signed URL is ever included here."""
    return {
        "schemaVersion": MANIFEST_SCHEMA_VERSION,
        "sourceUrl": source_url,
        "canonicalUrl": canonical_url,
        "urlForm": url_form,
        "status": getattr(result, "status", None),
        "runId": getattr(result, "run_id", None),
        "datasetId": getattr(result, "dataset_id", None),
        "title": getattr(result, "title", None),
        "nickname": getattr(result, "nickname", None),
        "duration": getattr(result, "duration", None),
        "downloadedAt": utc_now(),
        "assets": assets,
        "transcript": {
            "bytes": transcript_bytes,
            "segments": segment_count,
        },
        "notes": [
            "Media links returned by the Actor are signed and short-lived; they are not stored here.",
            "Re-running the same URL reuses this folder and skips files whose size still matches.",
        ],
    }


__all__ = [
    "MANIFEST_SCHEMA_VERSION",
    "build_manifest",
    "choose_output_dir",
    "folder_name",
    "sanitize_item",
    "slugify",
    "transcript_segments",
    "utc_now",
    "video_identity",
    "write_json",
    "write_text",
]
