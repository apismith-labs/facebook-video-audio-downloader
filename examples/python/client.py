"""Synchronous integration API for the Facebook Transcript Actor.

Importing this module does not read credentials, load dotenv files, create files, or access the
network. Configuration is read when a function is called.
"""
from __future__ import annotations

import math
import os
import re
from dataclasses import dataclass
from typing import Any
from urllib.parse import parse_qsl, urlsplit

if __package__:  # Package import, for example ``examples.python.client``.
    from .reliable_actor import (
        APPROVED_BUILD_ID,
        APPROVED_BUILD_NUMBER,
        DEFAULT_ACTOR_ID,
        STATUSES,
        ApiError,
        RestTransport,
        build_run_input,
        execute_input,
        normalize_segments,
        safe_error,
        validate_build,
        validate_run_charge_cap,
    )
else:  # Copy the two modules beside one another and ``import client``.
    from reliable_actor import (
        APPROVED_BUILD_ID,
        APPROVED_BUILD_NUMBER,
        DEFAULT_ACTOR_ID,
        STATUSES,
        ApiError,
        RestTransport,
        build_run_input,
        execute_input,
        normalize_segments,
        safe_error,
        validate_build,
        validate_run_charge_cap,
    )

DEFAULT_TIMEOUT_SECONDS = 900
DEFAULT_DATASET_LIMIT = 100

# Hosts that can front a public Facebook video.
_ALLOWED_HOSTS = {
    "facebook.com",
    "www.facebook.com",
    "m.facebook.com",
    "web.facebook.com",
    "fb.watch",
    "www.fb.watch",
}

# First path segments that are site sections, never page slugs.
_RESERVED_SEGMENTS = {
    "about",
    "business",
    "events",
    "fundraisers",
    "gaming",
    "groups",
    "help",
    "login",
    "login.php",
    "marketplace",
    "messages",
    "permalink.php",
    "photo",
    "plugins",
    "policies",
    "settings",
    "sharer",
    "stories",
    "story.php",
    "watchparty",
}

_WATCH = re.compile(r"^/watch/?$", re.IGNORECASE)
_VIDEO_PHP = re.compile(r"^/video\.php$", re.IGNORECASE)
_PAGE_VIDEO = re.compile(r"^/(?P<slug>[A-Za-z0-9._-]+)/videos/(?P<id>\d+)/?$", re.IGNORECASE)
_VIDEOS = re.compile(r"^/videos/(?P<id>\d+)/?$", re.IGNORECASE)
_REEL = re.compile(r"^/reel/(?P<id>\d+)/?$", re.IGNORECASE)
_SHARE = re.compile(r"^/share/(?P<kind>[vr])/(?P<code>[A-Za-z0-9_-]+)/?$", re.IGNORECASE)
_SHORT = re.compile(r"^/(?P<code>[A-Za-z0-9_-]+)/?$")
_DIGITS = re.compile(r"^\d+$")
_ACTOR_ID = re.compile(r"^[A-Za-z0-9][A-Za-z0-9._~/-]*$")
_BUSINESS_STATUSES = {"success", "actor_error", "empty", "no_transcript", "unknown", "run_failed"}


class TranscriptClientError(Exception):
    """Base class for errors callers should handle explicitly."""


class ConfigurationError(TranscriptClientError, ValueError):
    """Invalid URL, credentials, timeout, Actor ID, Build, or charge-cap configuration."""


class ProtocolError(TranscriptClientError):
    """The runner returned a malformed or contradictory result."""

    def __init__(self, message: str, *, run_id: str | None = None, dataset_id: str | None = None):
        super().__init__(message)
        self.run_id = run_id
        self.dataset_id = dataset_id


class CreationUnknownError(TranscriptClientError):
    """The create POST may have produced a Run; do not call again blindly."""


class TranscriptTransportError(TranscriptClientError):
    """A definite transport or post-creation retrieval failure."""

    def __init__(self, message: str, *, run_id: str | None = None, dataset_id: str | None = None):
        super().__init__(message)
        self.run_id = run_id
        self.dataset_id = dataset_id


@dataclass(frozen=True)
class TranscriptResult:
    """Outcome of one Actor Run. ``items`` retains the Actor's original item fields."""

    status: str
    text: str | None
    segments: list[dict[str, Any]]
    items: list[dict[str, Any]]
    run_id: str | None
    dataset_id: str | None
    source_url: str
    canonical_url: str
    url_form: str
    error: str | None
    dataset_complete: bool | None
    title: str | None = None
    duration: float | None = None
    nickname: str | None = None

    @property
    def succeeded(self) -> bool:
        return self.status == "success"

    @property
    def item(self) -> dict[str, Any] | None:
        return self.items[0] if self.items else None


class _MemoryJournal:
    """Journal-shaped in-memory sink; the reusable API creates no local files by default."""

    def __init__(self):
        self.records: list[dict[str, Any]] = []

    def append(self, record: dict) -> None:
        self.records.append(record.copy())


def _single_v(query: list[tuple[str, str]]) -> str | None:
    values = [value for key, value in query if key == "v"]
    if len(values) != 1 or not _DIGITS.fullmatch(values[0]):
        return None
    return values[0]


def validate_video_url(url: str) -> tuple[str, str]:
    """Validate a public Facebook video URL and return ``(canonical_url, form)``.

    Tracking parameters are dropped. Query strings are only meaningful for ``watch`` and
    ``video.php``, where a single numeric ``v`` is required. Unrecognized forms are rejected
    rather than guessed at.
    """
    if not isinstance(url, str) or not url or url != url.strip():
        raise ConfigurationError("url must be a bare Facebook video URL without surrounding whitespace")
    if len(url) > 2048:
        raise ConfigurationError("url is longer than 2048 characters")
    if url.lower().startswith("http://"):
        raise ConfigurationError("url must use https://; http links are rejected")
    parsed = urlsplit(url)
    if parsed.scheme != "https":
        raise ConfigurationError("url must be an absolute https:// Facebook video URL")
    if parsed.username or parsed.password:
        raise ConfigurationError("url must not contain user information")
    if parsed.port not in (None, 443):
        raise ConfigurationError("url must not use a non-default port")
    host = (parsed.hostname or "").lower().rstrip(".")
    if host not in _ALLOWED_HOSTS:
        raise ConfigurationError(
            "url host must be facebook.com, www.facebook.com, m.facebook.com, web.facebook.com, or fb.watch"
        )
    if parsed.fragment:
        raise ConfigurationError("url must not contain a fragment")

    path = parsed.path or "/"
    query = parse_qsl(parsed.query, keep_blank_values=True)

    if host in {"fb.watch", "www.fb.watch"}:
        match = _SHORT.fullmatch(path)
        if not match:
            raise ConfigurationError("fb.watch URLs must look like https://fb.watch/<code>/")
        return f"https://fb.watch/{match.group('code')}/", "short_link"

    if _WATCH.fullmatch(path):
        video_id = _single_v(query)
        if video_id is None:
            raise ConfigurationError("watch URLs must include exactly one numeric ?v= parameter")
        return f"https://www.facebook.com/watch/?v={video_id}", "watch"

    if _VIDEO_PHP.fullmatch(path):
        video_id = _single_v(query)
        if video_id is None:
            raise ConfigurationError("video.php URLs must include exactly one numeric ?v= parameter")
        return f"https://www.facebook.com/video.php?v={video_id}", "video_php"

    match = _PAGE_VIDEO.fullmatch(path)
    if match:
        slug = match.group("slug")
        if slug.lower() in _RESERVED_SEGMENTS:
            raise ConfigurationError("url points at a site section, not a video")
        return f"https://www.facebook.com/{slug}/videos/{match.group('id')}/", "page_video"

    match = _VIDEOS.fullmatch(path)
    if match:
        return f"https://www.facebook.com/videos/{match.group('id')}/", "video"

    match = _REEL.fullmatch(path)
    if match:
        return f"https://www.facebook.com/reel/{match.group('id')}/", "reel"

    match = _SHARE.fullmatch(path)
    if match:
        return f"https://www.facebook.com/share/{match.group('kind').lower()}/{match.group('code')}/", "share"

    raise ConfigurationError(
        "unsupported Facebook URL form; see docs/input-output-fields.md#accepted-url-forms"
    )


def _positive_timeout(value: float | None) -> float:
    if value is None:
        return float(DEFAULT_TIMEOUT_SECONDS)
    if isinstance(value, bool):
        raise ConfigurationError("timeout must be a finite positive number of seconds")
    try:
        result = float(value)
    except (TypeError, ValueError):
        raise ConfigurationError("timeout must be a finite positive number of seconds") from None
    if not math.isfinite(result) or result <= 0:
        raise ConfigurationError("timeout must be a finite positive number of seconds")
    return result


def _select_token(token: str | None) -> str:
    selected = token if token is not None else os.getenv("APIFY_TOKEN")
    if not isinstance(selected, str) or not selected.strip():
        raise ConfigurationError(
            "APIFY_TOKEN is required; pass token=... or set it in the process environment"
        )
    return selected.strip()


def _select_actor(actor_id: str | None) -> str:
    selected = actor_id if actor_id is not None else os.getenv("APIFY_ACTOR_ID", DEFAULT_ACTOR_ID)
    if (
        not isinstance(selected, str)
        or not selected.strip()
        or selected.strip().startswith("/")
        or not _ACTOR_ID.fullmatch(selected.strip())
    ):
        raise ConfigurationError("actor_id must be a non-empty Apify Actor ID or owner/name")
    return selected.strip()


def get_transcript(
    url: str,
    *,
    token: str | None = None,
    actor_id: str | None = None,
    build: str | None = None,
    timeout: float | None = None,
    max_total_charge_usd: str | float | None = None,
    require_charge_cap: bool | None = None,
    dataset_limit: int = DEFAULT_DATASET_LIMIT,
    run_id: str | None = None,
    row_id: str | None = None,
    transport: Any = None,
    journal: Any = None,
) -> TranscriptResult:
    """Run one public Facebook video URL through the pinned Actor Build.

    ``token`` overrides ``APIFY_TOKEN``. ``transport`` and ``journal`` are injection seams for
    offline tests and advanced callers. Passing ``run_id`` performs a GET-only resume of an
    existing Run: no create POST is issued.
    """
    canonical, form = validate_video_url(url)
    selected_token = _select_token(token)
    selected_actor = _select_actor(actor_id)

    selected_build = APPROVED_BUILD_NUMBER if build is None else build
    try:
        selected_build = validate_build(selected_build, required=True)
        cap = (
            max_total_charge_usd
            if max_total_charge_usd is not None
            else os.getenv("APIFY_MAX_TOTAL_CHARGE_USD")
        )
        require_cap = (
            require_charge_cap
            if require_charge_cap is not None
            else os.getenv("APIFY_REQUIRE_MAX_TOTAL_CHARGE_USD") == "1"
        )
        validate_run_charge_cap(cap, required=require_cap)
    except ValueError as exc:
        raise ConfigurationError(str(exc)) from None

    run_timeout = _positive_timeout(timeout)
    if isinstance(dataset_limit, bool) or not isinstance(dataset_limit, int) or dataset_limit <= 0:
        raise ConfigurationError("dataset_limit must be a positive integer")
    if run_id is not None and (not isinstance(run_id, str) or not run_id.strip() or run_id != run_id.strip()):
        raise ConfigurationError("run_id must be a non-empty Run ID without whitespace")

    active_transport = transport if transport is not None else RestTransport(selected_token)
    active_journal = journal if journal is not None else _MemoryJournal()
    try:
        outcome = execute_input(
            transport=active_transport,
            actor_id=selected_actor,
            run_input=build_run_input(canonical),
            source_url=canonical,
            journal=active_journal,
            deadline_seconds=run_timeout,
            max_total_charge_usd=cap,
            require_charge_cap=bool(require_cap),
            build=selected_build,
            require_build=True,
            dataset_limit=dataset_limit,
            run_id=run_id,
            row_id=row_id,
        )
    except ApiError as exc:
        raise TranscriptTransportError(str(exc)) from None

    if not isinstance(outcome, dict) or not isinstance(outcome.get("status"), str):
        raise ProtocolError("Runner returned a malformed result")
    status = outcome["status"]
    run_id_out = outcome.get("runId")
    dataset_id = outcome.get("datasetId")
    if status == "creation_unknown":
        raise CreationUnknownError(
            "Run creation outcome is unknown (Run ID unavailable): "
            f"{outcome.get('error') or 'reconcile before retrying'}; do not call get_transcript again blindly"
        )
    if status == "transport_error":
        if outcome.get("stage") == "build_mismatch":
            raise ProtocolError(
                f"Run was created but its Build could not be verified: "
                f"{outcome.get('error') or 'Build mismatch'}; do not create a replacement Run automatically",
                run_id=run_id_out,
                dataset_id=dataset_id,
            )
        detail = outcome.get("error") or "Apify transport failed"
        if run_id_out:
            detail = (
                f"{detail}; Run {run_id_out} already exists. Reconcile or resume that Run; "
                "do not call get_transcript again to replace it"
            )
        raise TranscriptTransportError(detail, run_id=run_id_out, dataset_id=dataset_id)
    if status not in STATUSES or status not in _BUSINESS_STATUSES:
        raise ProtocolError(f"Runner returned unsupported status: {status!r}", run_id=run_id_out, dataset_id=dataset_id)

    raw_items = outcome.get("rawItems", [])
    if not isinstance(raw_items, list) or any(not isinstance(item, dict) for item in raw_items):
        raise ProtocolError("Runner returned malformed Dataset items", run_id=run_id_out, dataset_id=dataset_id)
    items: list[dict[str, Any]] = raw_items

    if status == "run_failed":
        return TranscriptResult(
            status="run_failed",
            text=None,
            segments=[],
            items=items,
            run_id=run_id_out,
            dataset_id=dataset_id,
            source_url=url,
            canonical_url=canonical,
            url_form=form,
            error=outcome.get("error") or "Actor Run failed",
            dataset_complete=False,
        )

    transcript_item = next(
        (item for item in items if isinstance(item.get("text"), str) and item["text"].strip()),
        None,
    )
    text = transcript_item.get("text") if transcript_item else None
    segments = normalize_segments(transcript_item.get("segments")) if transcript_item else []
    if status == "success" and text is None:
        raise ProtocolError("Runner reported success without a non-empty transcript", run_id=run_id_out, dataset_id=dataset_id)

    metadata_item = transcript_item or (items[0] if items else {})
    duration = metadata_item.get("duration")
    if isinstance(duration, bool) or not isinstance(duration, (int, float)):
        duration = None
    title = metadata_item.get("title") if isinstance(metadata_item.get("title"), str) else None
    nickname = metadata_item.get("nickname") if isinstance(metadata_item.get("nickname"), str) else None

    return TranscriptResult(
        status=status,
        text=text,
        segments=segments,
        items=items,
        run_id=run_id_out,
        dataset_id=dataset_id,
        source_url=url,
        canonical_url=canonical,
        url_form=form,
        error=outcome.get("error") or None,
        dataset_complete=outcome.get("datasetComplete"),
        title=title,
        duration=float(duration) if duration is not None else None,
        nickname=nickname,
    )


__all__ = [
    "APPROVED_BUILD_ID",
    "APPROVED_BUILD_NUMBER",
    "ConfigurationError",
    "CreationUnknownError",
    "DEFAULT_ACTOR_ID",
    "ProtocolError",
    "TranscriptClientError",
    "TranscriptResult",
    "TranscriptTransportError",
    "get_transcript",
    "normalize_segments",
    "safe_error",
    "validate_video_url",
]
