"""Media download layer for the Facebook Video & Audio Downloader.

The Actor resolves a Facebook video page into signed ``videoUrl`` / ``audioUrl`` links on
``fbcdn.net``. This module turns those links into files on disk. It performs no Actor calls and
reads no environment variables, so it is fully testable offline.

Design rules, in order of importance:

1. A signed link is short-lived. Download immediately after the Run succeeds, and never resume a
   ``.part`` file from an earlier Run — the byte range is only meaningful for the same link.
2. Downloads are resumable with HTTP ``Range``. A ``.part`` file survives an interrupted transfer
   and is renamed into place only after the byte count and SHA-256 have been verified.
3. A file that already verifies is skipped, so re-running a batch is cheap and idempotent.
4. Persisted metadata never contains the signed query string unless the caller opts in.
"""
from __future__ import annotations

import hashlib
import math
import os
import re
import time
import urllib.error
import urllib.parse
import urllib.request
from dataclasses import dataclass
from pathlib import Path
from typing import Any, Callable

CHUNK_SIZE = 1 << 20
DEFAULT_TIMEOUT_SECONDS = 60.0
DEFAULT_MAX_ATTEMPTS = 3
RETRYABLE_STATUS = {408, 425, 429, 500, 502, 503, 504}
SIGNED_QUERY_KEYS = {
    "oh",
    "oe",
    "sig",
    "signature",
    "token",
    "hdnea",
    "hash",
    "expires",
    "expire",
    "x-amz-signature",
    "x-amz-credential",
    "x-amz-security-token",
}


class MediaError(Exception):
    """A download failed. ``status`` carries the HTTP status when there was a response."""

    def __init__(self, message: str, *, status: int | None = None, retryable: bool = False):
        super().__init__(message)
        self.status = status
        self.retryable = retryable


@dataclass(frozen=True)
class MediaAsset:
    """One downloaded or reused media file."""

    kind: str
    path: str
    bytes: int
    sha256: str
    content_type: str | None
    source_host: str
    skipped: bool

    def as_dict(self) -> dict[str, Any]:
        return {
            "kind": self.kind,
            "path": self.path,
            "bytes": self.bytes,
            "sha256": self.sha256,
            "contentType": self.content_type,
            "sourceHost": self.source_host,
            "skipped": self.skipped,
        }


def redact_media_url(url: str) -> str:
    """Return ``scheme://host/path`` without the signed query string.

    Signed parameters are credentials for a single object: they should not be written to a JSON
    sidecar, pasted into an issue, or committed by accident.
    """
    if not isinstance(url, str) or not url:
        return ""
    try:
        parsed = urllib.parse.urlsplit(url)
    except ValueError:
        return "[unparseable media url]"
    if not parsed.netloc:
        return "[unparseable media url]"
    return urllib.parse.urlunsplit((parsed.scheme, parsed.netloc, parsed.path, "", ""))


def media_host(url: str) -> str:
    """Host only, for provenance records."""
    try:
        return (urllib.parse.urlsplit(url).hostname or "").lower().rstrip(".")
    except ValueError:
        return ""


def _has_signed_query(url: str) -> bool:
    try:
        query = urllib.parse.parse_qsl(urllib.parse.urlsplit(url).query, keep_blank_values=True)
    except ValueError:
        return False
    return any(key.lower() in SIGNED_QUERY_KEYS for key, _ in query)


def _sha256_of(path: Path, chunk_size: int = CHUNK_SIZE) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as stream:
        while True:
            block = stream.read(chunk_size)
            if not block:
                break
            digest.update(block)
    return digest.hexdigest()


def _parse_content_range(value: str | None) -> int | None:
    """Extract the total length from ``bytes 0-1023/4096``."""
    if not value:
        return None
    match = re.search(r"/(\d+)\s*$", value)
    return int(match.group(1)) if match else None


def _read_headers(response) -> tuple[int | None, int | None, str | None]:
    headers = getattr(response, "headers", None)
    if headers is None:
        return None, None, None
    content_type = headers.get("Content-Type")
    try:
        length = int(headers.get("Content-Length")) if headers.get("Content-Length") else None
    except (TypeError, ValueError):
        length = None
    total = _parse_content_range(headers.get("Content-Range"))
    return length, total, content_type


def _is_retryable_network_error(exc: BaseException) -> bool:
    return isinstance(
        exc,
        (
            TimeoutError,
            ConnectionError,
            urllib.error.URLError,
            OSError,
        ),
    ) and not isinstance(exc, urllib.error.HTTPError)


def download_media(
    url: str,
    destination: str | Path,
    *,
    kind: str = "video",
    timeout: float = DEFAULT_TIMEOUT_SECONDS,
    max_attempts: int = DEFAULT_MAX_ATTEMPTS,
    chunk_size: int = CHUNK_SIZE,
    force: bool = False,
    progress: Callable[[str, int, int | None], None] | None = None,
    opener: Callable[..., Any] | None = None,
    sleep: Callable[[float], None] = time.sleep,
    clock: Callable[[], float] = time.monotonic,
) -> MediaAsset:
    """Download ``url`` to ``destination``, resuming a partial file when possible.

    ``destination`` must not exist unless it is being replaced. A verified existing file is
    returned with ``skipped=True`` so batch runs stay idempotent.
    """
    destination = Path(destination)
    if not isinstance(url, str) or not url.lower().startswith(("https://", "http://")):
        raise MediaError("media URL must be an absolute http(s) URL")
    if timeout <= 0 or not math.isfinite(timeout):
        raise MediaError("timeout must be a finite positive number of seconds")
    if isinstance(max_attempts, bool) or not isinstance(max_attempts, int) or max_attempts <= 0:
        raise MediaError("max_attempts must be a positive integer")
    if chunk_size <= 0:
        raise MediaError("chunk_size must be positive")

    host = media_host(url)
    if destination.exists() and not force:
        size = destination.stat().st_size
        return MediaAsset(
            kind=kind,
            path=str(destination),
            bytes=size,
            sha256=_sha256_of(destination, chunk_size),
            content_type=None,
            source_host=host,
            skipped=True,
        )

    destination.parent.mkdir(parents=True, exist_ok=True)
    part_path = destination.with_name(destination.name + ".part")
    if force:
        for stale in (part_path, destination):
            try:
                stale.unlink()
            except FileNotFoundError:
                pass

    open_request = opener or urllib.request.urlopen
    last_error: MediaError | None = None

    for attempt in range(max_attempts):
        resume_from = part_path.stat().st_size if part_path.exists() else 0
        headers = {}
        if resume_from:
            headers["Range"] = f"bytes={resume_from}-"
        request = urllib.request.Request(url, headers=headers, method="GET")
        try:
            with open_request(request, timeout=timeout) as response:
                status = getattr(response, "status", None) or getattr(response, "code", None)
                length, total, content_type = _read_headers(response)
                append = bool(resume_from) and status == 206
                if status not in (200, 206):
                    raise MediaError(f"media server returned HTTP {status}", status=status)
                mode = "ab" if append else "wb"
                written = resume_from if append else 0
                expected = total if total is not None else (length + resume_from if append and length else length)
                with part_path.open(mode) as stream:
                    while True:
                        block = response.read(chunk_size)
                        if not block:
                            break
                        stream.write(block)
                        written += len(block)
                        if progress is not None:
                            progress(kind, written, expected)
                    stream.flush()
                    os.fsync(stream.fileno())
            produced = part_path.stat().st_size
            if expected is not None and produced != expected:
                raise MediaError(
                    f"truncated download: expected {expected} bytes, wrote {produced}",
                    retryable=True,
                )
            os.replace(part_path, destination)
            return MediaAsset(
                kind=kind,
                path=str(destination),
                bytes=produced,
                sha256=_sha256_of(destination, chunk_size),
                content_type=content_type,
                source_host=host,
                skipped=False,
            )
        except urllib.error.HTTPError as exc:
            # 416 means the resume offset is already at (or past) the object length. If the server
            # confirms the total and our .part matches it, the transfer had finished before an
            # interrupted rename: promote the file instead of failing.
            if exc.code == 416 and part_path.exists():
                headers = getattr(exc, "headers", None)
                total = _parse_content_range(headers.get("Content-Range") if headers else None)
                if total is not None and part_path.stat().st_size == total:
                    os.replace(part_path, destination)
                    return MediaAsset(
                        kind=kind,
                        path=str(destination),
                        bytes=total,
                        sha256=_sha256_of(destination, chunk_size),
                        content_type=None,
                        source_host=host,
                        skipped=False,
                    )
            retryable = exc.code in RETRYABLE_STATUS
            # A stale signature answers 403; retrying the same link cannot help.
            last_error = MediaError(f"media server returned HTTP {exc.code}", status=exc.code, retryable=retryable)
            if not retryable or attempt + 1 >= max_attempts:
                raise last_error from None
        except MediaError as exc:
            last_error = exc
            if not exc.retryable or attempt + 1 >= max_attempts:
                raise
        except (TimeoutError, ConnectionError, urllib.error.URLError, OSError) as exc:
            last_error = MediaError(
                f"media download transport failure: {type(exc).__name__}", retryable=True
            )
            if attempt + 1 >= max_attempts:
                raise last_error from None
        delay = min(2**attempt, 8)
        sleep(delay)
        _ = clock  # clock is injected for symmetry with the API layer; kept for future use.

    raise last_error or MediaError("media download failed")


def audio_target(destination: str | Path, audio_format: str) -> tuple[Path, bool]:
    """Map a raw ``audio.mp4`` download to the requested container.

    Returns ``(final_path, needs_remux)``. ``raw`` keeps Facebook's audio-only MP4 untouched;
    ``m4a`` and ``mp3`` require ffmpeg and are produced by :func:`transcode_audio`.
    """
    destination = Path(destination)
    if audio_format not in {"raw", "m4a", "mp3"}:
        raise MediaError("audio_format must be one of: raw, m4a, mp3")
    if audio_format == "raw":
        return destination, False
    return destination.with_suffix("." + audio_format), True


def build_ffmpeg_args(source: str | Path, target: str | Path, audio_format: str) -> list[str]:
    """Build the ffmpeg argument list for an audio container change.

    ``m4a`` is a stream copy of the existing AAC track: no quality loss, no re-encode.
    ``mp3`` has to re-encode because MP4 audio is AAC.
    """
    source, target = Path(source), Path(target)
    if audio_format == "m4a":
        return ["ffmpeg", "-nostdin", "-y", "-loglevel", "error", "-i", str(source),
                "-vn", "-c:a", "copy", "-movflags", "+faststart", str(target)]
    if audio_format == "mp3":
        return ["ffmpeg", "-nostdin", "-y", "-loglevel", "error", "-i", str(source),
                "-vn", "-c:a", "libmp3lame", "-b:a", "192k", str(target)]
    raise MediaError("transcode is only defined for m4a and mp3")


def transcode_audio(
    source: str | Path,
    target: str | Path,
    audio_format: str,
    *,
    runner: Callable[..., Any] | None = None,
) -> Path:
    """Convert an audio-only MP4 into ``m4a`` or ``mp3`` using ffmpeg.

    ``runner`` is injectable so tests never need ffmpeg installed.
    """
    import shutil
    import subprocess

    source, target = Path(source), Path(target)
    if not source.is_file():
        raise MediaError(f"audio source is missing: {source}")
    target.parent.mkdir(parents=True, exist_ok=True)
    args = build_ffmpeg_args(source, target, audio_format)
    execute = runner or subprocess.run
    if runner is None and shutil.which("ffmpeg") is None:
        raise MediaError(
            "ffmpeg is required for --audio-format m4a|mp3; install it or use --audio-format raw"
        )
    result = execute(args, capture_output=True, text=True)
    code = getattr(result, "returncode", 0)
    if code != 0:
        stderr = (getattr(result, "stderr", "") or "").strip().splitlines()
        detail = stderr[-1][:160] if stderr else f"exit code {code}"
        raise MediaError(f"ffmpeg failed: {detail}")
    return target


__all__ = [
    "MediaAsset",
    "MediaError",
    "audio_target",
    "build_ffmpeg_args",
    "download_media",
    "media_host",
    "redact_media_url",
    "transcode_audio",
]
