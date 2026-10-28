#!/usr/bin/env python3
"""Download the video, audio, transcript, and metadata for one public Facebook video URL.

One invocation may create one billable Actor Run ($0.0055 for a video under one minute). The Run
resolves the page into signed ``videoUrl`` / ``audioUrl`` links on fbcdn.net; those links are
downloaded immediately because the signature expires.

Exit codes: 0 downloaded (or already present), 2 bad configuration, 4 Actor reported an item error,
5 empty Dataset, 7 unknown, 8 transport failure, 9 Run failed, 10 creation outcome unknown,
11 Actor succeeded but a media file could not be downloaded.
"""
from __future__ import annotations

import argparse
import json
import os
import sys
from pathlib import Path

if __package__:
    from .client import APPROVED_BUILD_NUMBER
    from .env import load_env_file
    from .pipeline import download_one
    from .reliable_actor import write_private
else:
    sys.path.insert(0, str(Path(__file__).resolve().parent))
    from client import APPROVED_BUILD_NUMBER
    from env import load_env_file
    from pipeline import download_one
    from reliable_actor import write_private

REPO_ROOT = Path(__file__).resolve().parents[2]


def human_bytes(value: int | None) -> str:
    if not value:
        return "0 B"
    units = ("B", "KB", "MB", "GB", "TB")
    size = float(value)
    for unit in units:
        if size < 1024 or unit == units[-1]:
            return f"{int(size)} B" if unit == "B" else f"{size:.1f} {unit}"
        size /= 1024
    return f"{size:.1f} TB"


def make_progress(quiet: bool):
    state: dict[str, int] = {}
    step = 5 * 1024 * 1024

    def report(kind: str, written: int, total: int | None) -> None:
        if quiet:
            return
        last = state.get(kind, 0)
        if written - last < step and not (total is not None and written >= total):
            return
        state[kind] = written
        share = f" ({written * 100 // total}%)" if total else ""
        print(f"  {kind}: {human_bytes(written)}{share}", file=sys.stderr, end="\r", flush=True)

    return report


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        description="Download video, audio, transcript, and metadata for one Facebook video URL.",
        epilog=(
            "Example: python examples/python/download.py "
            "'https://www.facebook.com/watch/?v=1131784255863745' --audio --out downloads"
        ),
    )
    parser.add_argument("url", help="Public Facebook video URL")
    parser.add_argument("--out", default="downloads", metavar="DIR",
                        help="Output base directory (default: downloads)")
    parser.add_argument("--name", metavar="NAME", help="Override the output folder name")
    parser.add_argument("--no-video", action="store_true", help="Do not download video.mp4")
    parser.add_argument("--audio", action="store_true",
                        help="Also download the audio track (audio-only MP4)")
    parser.add_argument("--audio-format", choices=("raw", "m4a", "mp3"), default="raw",
                        help="raw keeps the audio-only MP4; m4a/mp3 require ffmpeg (default: raw)")
    parser.add_argument("--force", action="store_true",
                        help="Re-download even when files already exist")
    parser.add_argument("--keep-media-urls", action="store_true",
                        help="Store full signed URLs in metadata.json (off by default)")
    parser.add_argument("--json", action="store_true", help="Print one JSON report to stdout")
    parser.add_argument("--report", metavar="PATH", help="Also write the JSON report to PATH")
    parser.add_argument("--resume", metavar="RUN_ID",
                        help="Reuse an existing Run (GET only, no new charge)")
    parser.add_argument("--dataset-limit", type=int, default=100,
                        help="Maximum Dataset items to read (default 100)")
    parser.add_argument("--timeout", type=float, default=900,
                        help="Overall Run deadline in seconds (default 900)")
    parser.add_argument("--build", default=APPROVED_BUILD_NUMBER,
                        help=f"Actor Build (default {APPROVED_BUILD_NUMBER})")
    parser.add_argument("--actor-id", help="Override the Actor ID")
    parser.add_argument("--max-total-charge-usd", help="Per-Run charge cap in USD")
    parser.add_argument("--require-charge-cap", action="store_true",
                        help="Fail before creating a Run without a cap")
    parser.add_argument("--quiet", action="store_true", help="Suppress progress output")
    return parser


def main(argv=None) -> int:
    args = build_parser().parse_args(argv)
    load_env_file()
    if args.audio_format != "raw" and not args.audio:
        print("--audio-format requires --audio", file=sys.stderr)
        return 2

    report, code = download_one(
        args.url,
        out_dir=args.out,
        want_video=not args.no_video,
        want_audio=args.audio,
        audio_format=args.audio_format,
        force=args.force,
        keep_media_urls=args.keep_media_urls,
        name=args.name,
        build=args.build,
        actor_id=args.actor_id,
        timeout=args.timeout,
        max_total_charge_usd=args.max_total_charge_usd,
        require_charge_cap=args.require_charge_cap,
        dataset_limit=args.dataset_limit,
        resume=args.resume,
        progress=make_progress(args.quiet),
    )

    if args.json:
        print(json.dumps(report, ensure_ascii=False, indent=2))
    elif not args.quiet:
        if code == 0:
            files = ", ".join(Path(asset["path"]).name for asset in report["assets"]) or "no media"
            print(f"{report['status']}: {report['outputDir']}  segments={report['segments']}  {files}")
        else:
            print(f"{report['status']}: {report.get('error') or 'no output'}", file=sys.stderr)
    if args.report:
        write_private(
            Path(args.report),
            json.dumps(report, ensure_ascii=False, indent=2) + "\n",
        )
    return code


if __name__ == "__main__":
    raise SystemExit(main())
