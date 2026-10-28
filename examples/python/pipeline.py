"""One-video download pipeline: Actor Run, media download, sidecar files, manifest.

The CLI (``download.py``) and the batch runner share this module so a single URL and a CSV row
produce byte-identical folder layouts and the same status vocabulary.
"""
from __future__ import annotations

import sys
from pathlib import Path
from typing import Any, Callable

if __package__:
    from .client import (
        ConfigurationError,
        CreationUnknownError,
        ProtocolError,
        TranscriptTransportError,
        get_transcript,
    )
    from .layout import (
        build_manifest,
        choose_output_dir,
        folder_name,
        sanitize_item,
        transcript_segments,
        write_json,
        write_text,
    )
    from .media import MediaError, audio_target, download_media, redact_media_url, transcode_audio
else:
    sys.path.insert(0, str(Path(__file__).resolve().parent))
    from client import (
        ConfigurationError,
        CreationUnknownError,
        ProtocolError,
        TranscriptTransportError,
        get_transcript,
    )
    from layout import (
        build_manifest,
        choose_output_dir,
        folder_name,
        sanitize_item,
        transcript_segments,
        write_json,
        write_text,
    )
    from media import MediaError, audio_target, download_media, redact_media_url, transcode_audio

DOWNLOAD_FAILED_EXIT = 11
# These Runs produced usable media; a missing transcript does not block the download.
DOWNLOADABLE_STATUSES = {"success", "no_transcript"}
TRANSCRIPT_EXIT_CODES = {"actor_error": 4, "empty": 5, "no_transcript": 6, "unknown": 7, "run_failed": 9}


def empty_report(source_url: str) -> dict[str, Any]:
    return {
        "status": "unknown",
        "sourceUrl": source_url,
        "canonicalUrl": None,
        "urlForm": None,
        "runId": None,
        "datasetId": None,
        "outputDir": None,
        "duration": None,
        "title": None,
        "nickname": None,
        "segments": 0,
        "assets": [],
        "transcript": None,
        "error": None,
    }


def download_one(
    url: str,
    *,
    out_dir: str | Path = "downloads",
    want_video: bool = True,
    want_audio: bool = False,
    audio_format: str = "raw",
    force: bool = False,
    keep_media_urls: bool = False,
    name: str | None = None,
    build: str | None = None,
    actor_id: str | None = None,
    timeout: float | None = None,
    max_total_charge_usd: str | float | None = None,
    require_charge_cap: bool | None = None,
    dataset_limit: int = 100,
    resume: str | None = None,
    row_id: str | None = None,
    token: str | None = None,
    transport: Any = None,
    journal: Any = None,
    progress: Callable[[str, int, int | None], None] | None = None,
) -> tuple[dict[str, Any], int]:
    """Resolve one URL, download the requested media, and write the sidecar files.

    Returns ``(report, exit_code)``. Exit codes: 0 downloaded, 2 configuration, 4 Actor item error,
    5 empty Dataset, 7 unknown, 8 transport, 9 Run failed, 10 creation unknown, 11 download failed.
    """
    report = empty_report(url)
    try:
        result = get_transcript(
            url,
            token=token,
            build=build,
            actor_id=actor_id,
            timeout=timeout,
            max_total_charge_usd=max_total_charge_usd,
            require_charge_cap=require_charge_cap,
            dataset_limit=dataset_limit,
            run_id=resume,
            row_id=row_id,
            transport=transport,
            journal=journal,
        )
    except ConfigurationError as exc:
        report.update(status="config_error", error=str(exc))
        return report, 2
    except CreationUnknownError as exc:
        report.update(status="creation_unknown", error=str(exc))
        return report, 10
    except ProtocolError as exc:
        report.update(status="unknown", runId=exc.run_id, datasetId=exc.dataset_id, error=str(exc))
        return report, 7
    except TranscriptTransportError as exc:
        report.update(status="transport_error", runId=exc.run_id, datasetId=exc.dataset_id, error=str(exc))
        return report, 8

    report.update(
        status=result.status,
        canonicalUrl=result.canonical_url,
        urlForm=result.url_form,
        runId=result.run_id,
        datasetId=result.dataset_id,
        duration=result.duration,
        title=result.title,
        nickname=result.nickname,
        error=result.error,
    )
    item = result.item or {}
    folder = name or folder_name(result.canonical_url, result.url_form, result.title)
    target = choose_output_dir(out_dir, folder, result.canonical_url)
    report["outputDir"] = str(target)

    if result.status not in DOWNLOADABLE_STATUSES:
        # No usable media: keep the metadata as evidence so the failure can be inspected later.
        write_json(target / "metadata.json", sanitize_item(item, keep_media_urls=keep_media_urls))
        return report, TRANSCRIPT_EXIT_CODES.get(result.status, 7)

    target.mkdir(parents=True, exist_ok=True)
    segments = transcript_segments(result.segments)
    transcript_text = (result.text or "").strip()
    transcript_bytes = write_text(
        target / "transcript.txt", transcript_text + ("\n" if transcript_text else "")
    )
    write_json(target / "segments.json", segments)
    write_json(target / "metadata.json", sanitize_item(item, keep_media_urls=keep_media_urls))
    report["segments"] = len(segments)
    report["transcript"] = {"bytes": transcript_bytes, "segments": len(segments)}

    # (kind, url, raw destination, converted destination or None)
    jobs: list[tuple[str, str, Path, Path | None]] = []
    if want_video and isinstance(item.get("videoUrl"), str) and item["videoUrl"]:
        jobs.append(("video", item["videoUrl"], target / "video.mp4", None))
    if want_audio and isinstance(item.get("audioUrl"), str) and item["audioUrl"]:
        raw_path = target / "audio.mp4"
        final_path, needs_remux = audio_target(raw_path, audio_format)
        jobs.append(("audio", item["audioUrl"], raw_path, final_path if needs_remux else None))

    assets: list[dict[str, Any]] = []
    failure: MediaError | None = None
    if not jobs:
        failure = MediaError(f"the Dataset item carries no media URL for the requested output ({report['status']})")
        report["error"] = str(failure)

    for kind, media_url, destination, converted in jobs:
        try:
            asset = download_media(
                media_url, destination, kind=kind, force=force, progress=progress
            )
            assets.append(asset.as_dict())
            if converted is not None:
                # ``--force`` re-derives the converted container too; otherwise a re-downloaded
                # audio.mp4 would sit next to a stale audio.m4a.
                rebuilt = force or not converted.exists()
                if rebuilt:
                    transcode_audio(destination, converted, audio_format)
                assets.append({
                    "kind": "audio-converted",
                    "path": str(converted),
                    "bytes": converted.stat().st_size,
                    "sha256": "",
                    "contentType": None,
                    "sourceHost": "",
                    "skipped": not rebuilt,
                })
        except MediaError as exc:
            failure = exc
            report["error"] = f"{kind} download failed: {exc}"
            break

    manifest = build_manifest(
        source_url=result.source_url,
        canonical_url=result.canonical_url,
        url_form=result.url_form,
        result=result,
        assets=assets,
        transcript_bytes=transcript_bytes,
        segment_count=len(segments),
    )
    manifest["mediaSources"] = [
        {"kind": kind, "url": redact_media_url(media_url)} for kind, media_url, *_ in jobs
    ]
    write_json(target / "manifest.json", manifest)
    report["assets"] = assets
    return report, DOWNLOAD_FAILED_EXIT if failure is not None else 0


__all__ = ["DOWNLOAD_FAILED_EXIT", "DOWNLOADABLE_STATUSES", "download_one", "empty_report"]
