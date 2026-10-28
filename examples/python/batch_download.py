#!/usr/bin/env python3
"""Download a CSV of Facebook video URLs: one Run and one media folder per non-empty row.

Without ``--execute`` this script only validates the file and prints a plan; it does not read the
token and it creates no Run. The result file is written with owner-only permissions and never
overwrites an earlier batch.
"""
from __future__ import annotations

import argparse
import csv
import json
import os
import sys
import threading
from concurrent.futures import ThreadPoolExecutor
from dataclasses import dataclass
from datetime import datetime, timezone
from pathlib import Path
from typing import Any

if __package__:
    from .client import APPROVED_BUILD_NUMBER, ConfigurationError, validate_video_url
    from .cost import DATASET_ITEM_USD, TIER_PRICES_USD_PER_MINUTE, billed_minutes
    from .env import load_env_file
    from .pipeline import download_one
    from .reliable_actor import RunJournal, sanitize, write_private
else:
    sys.path.insert(0, str(Path(__file__).resolve().parent))
    from client import APPROVED_BUILD_NUMBER, ConfigurationError, validate_video_url
    from cost import DATASET_ITEM_USD, TIER_PRICES_USD_PER_MINUTE, billed_minutes
    from env import load_env_file
    from pipeline import download_one
    from reliable_actor import RunJournal, sanitize, write_private

COUNTER_KEYS = (
    "success",
    "no_transcript",
    "actor_error",
    "empty",
    "unknown",
    "transport_error",
    "run_failed",
    "creation_unknown",
    "download_failed",
    "config_error",
)


@dataclass(frozen=True)
class CsvRow:
    row_id: str
    url: str
    canonical_url: str
    url_form: str
    duration_seconds: float | None


def parse_rows(path: Path) -> list[CsvRow]:
    """Validate every non-empty data row before any Run is created.

    The header must contain ``videoUrl`` or ``url`` (case-insensitive). ``durationSeconds`` is
    optional and only used for cost estimates. The CSV line number becomes ``rowId`` so duplicate
    URLs stay traceable to their input line.
    """
    if not path.is_file():
        raise ConfigurationError(f"CSV file not found: {path}")
    with path.open(encoding="utf-8-sig", newline="") as handle:
        reader = csv.DictReader(handle)
        fields = {name.strip().lower(): name for name in (reader.fieldnames or [])}
        url_key = fields.get("videourl") or fields.get("url")
        if url_key is None:
            raise ConfigurationError("CSV must have a 'videoUrl' or 'url' column")
        duration_key = fields.get("durationseconds")
        rows: list[CsvRow] = []
        for raw in reader:
            row_id = str(reader.line_num)
            url = (raw.get(url_key) or "").strip()
            if not url:
                continue
            try:
                canonical, form = validate_video_url(url)
            except ConfigurationError as exc:
                raise ConfigurationError(f"CSV line {row_id}: {exc}") from None
            duration: float | None = None
            if duration_key:
                value = (raw.get(duration_key) or "").strip()
                if value:
                    try:
                        duration = float(value)
                    except ValueError:
                        raise ConfigurationError(
                            f"CSV line {row_id}: durationSeconds must be a number"
                        ) from None
                    if duration < 0:
                        raise ConfigurationError(
                            f"CSV line {row_id}: durationSeconds must not be negative"
                        )
            rows.append(CsvRow(row_id, url, canonical, form, duration))
    if not rows:
        raise ConfigurationError("CSV contains no non-empty URL rows")
    return rows


def estimate_rows(rows: list[CsvRow], tier: str) -> dict[str, Any]:
    """Sum per-row Run costs; media transfer is free and is not counted here."""
    selected = (tier or "FREE").strip().upper()
    if selected not in TIER_PRICES_USD_PER_MINUTE:
        raise ConfigurationError("tier must be one of " + ", ".join(TIER_PRICES_USD_PER_MINUTE))
    minutes = sum(billed_minutes(row.duration_seconds or 0) for row in rows)
    item_cost = DATASET_ITEM_USD * len(rows)
    speech_cost = TIER_PRICES_USD_PER_MINUTE[selected] * minutes
    return {
        "tier": selected,
        "videos": len(rows),
        "billedMinutes": minutes,
        "datasetItemUsd": float(item_cost),
        "speechToTextUsd": float(speech_cost),
        "totalUsd": float(item_cost + speech_cost),
        "lowerBound": any(row.duration_seconds is None for row in rows),
    }


def run_batch(
    rows: list[CsvRow],
    *,
    token: str,
    out_dir: str | Path,
    actor_id: str | None = None,
    build: str | None = APPROVED_BUILD_NUMBER,
    max_concurrency: int = 3,
    timeout: float = 900,
    max_total_charge_usd: str | float | None = None,
    require_charge_cap: bool = False,
    dataset_limit: int = 100,
    want_video: bool = True,
    want_audio: bool = False,
    audio_format: str = "raw",
    force: bool = False,
    journal=None,
    transport_factory=None,
) -> list[dict]:
    """Download every row concurrently and return per-row result records in input order."""
    if isinstance(max_concurrency, bool) or not isinstance(max_concurrency, int) or max_concurrency < 1:
        raise ConfigurationError("max_concurrency must be a positive integer")

    def one(row: CsvRow) -> dict:
        report, code = download_one(
            row.url,
            out_dir=out_dir,
            want_video=want_video,
            want_audio=want_audio,
            audio_format=audio_format,
            force=force,
            build=build,
            actor_id=actor_id,
            timeout=timeout,
            max_total_charge_usd=max_total_charge_usd,
            require_charge_cap=require_charge_cap,
            dataset_limit=dataset_limit,
            row_id=row.row_id,
            token=token,
            transport=transport_factory() if transport_factory else None,
            journal=journal,
        )
        return {**report, "rowId": row.row_id, "exitCode": code}

    with ThreadPoolExecutor(max_workers=min(max_concurrency, len(rows))) as pool:
        return list(pool.map(one, rows))


def count_results(results: list[dict]) -> dict[str, int]:
    counts = {key: 0 for key in COUNTER_KEYS}
    for row in results:
        status = row.get("status", "unknown")
        counts[status if status in counts else "unknown"] += 1
    counts["processed"] = sum(counts.values())
    counts["failed"] = counts["processed"] - counts["success"] - counts["no_transcript"]
    return counts


def _unique_path(path: Path) -> Path:
    if not path.exists():
        return path
    stem, suffix = path.stem, path.suffix
    for index in range(1, 1000):
        candidate = path.with_name(f"{stem}-{index}{suffix}")
        if not candidate.exists():
            return candidate
    raise ConfigurationError("could not allocate a unique result filename")


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        description="Download one Run per CSV row. Plan-only unless --execute.",
    )
    parser.add_argument("csv", help="CSV file with a videoUrl (or url) column")
    parser.add_argument("--execute", action="store_true",
                        help="Actually create Runs and download; without it nothing is billed")
    parser.add_argument("--out", default="downloads", metavar="DIR",
                        help="Output base directory (default: downloads)")
    parser.add_argument("--limit", type=int, help="Process only the first N rows")
    parser.add_argument("--max-concurrency", type=int, default=3,
                        help="Concurrent rows (default 3)")
    parser.add_argument("--output", metavar="PATH", help="Result JSON path (default: beside the CSV)")
    parser.add_argument("--tier", default="FREE",
                        help="Apify plan tier for the cost estimate (default FREE)")
    parser.add_argument("--no-video", action="store_true", help="Skip video downloads")
    parser.add_argument("--audio", action="store_true", help="Also download audio")
    parser.add_argument("--audio-format", choices=("raw", "m4a", "mp3"), default="raw")
    parser.add_argument("--force", action="store_true", help="Re-download existing files")
    parser.add_argument("--timeout", type=float, default=900, help="Per-Run deadline in seconds")
    parser.add_argument("--build", default=APPROVED_BUILD_NUMBER,
                        help=f"Actor Build (default {APPROVED_BUILD_NUMBER})")
    parser.add_argument("--actor-id", help="Override the Actor ID")
    parser.add_argument("--dataset-limit", type=int, default=100,
                        help="Maximum Dataset items per Run")
    parser.add_argument("--max-total-charge-usd", help="Per-Run charge cap in USD")
    parser.add_argument("--require-charge-cap", action="store_true",
                        help="Fail before any Run when no cap is set")
    return parser


def main(argv: list[str] | None = None) -> int:
    args = build_parser().parse_args(argv)
    csv_path = Path(args.csv).expanduser().resolve()
    try:
        rows = parse_rows(csv_path)
        if args.limit is not None:
            if args.limit < 1:
                raise ConfigurationError("--limit must be a positive integer")
            rows = rows[: args.limit]
        estimate = estimate_rows(rows, args.tier)
    except ConfigurationError as exc:
        print(f"Configuration error: {exc}", file=sys.stderr)
        return 2

    print(f"CSV: {csv_path}")
    print(f"Rows: {len(rows)}")
    print(
        f"Estimate: {estimate['videos']} video(s), {estimate['billedMinutes']} billed minute(s), "
        f"{estimate['tier']} tier -> ${estimate['totalUsd']:.4f} estimated "
        f"(dataset ${estimate['datasetItemUsd']:.4f} + speech ${estimate['speechToTextUsd']:.4f})"
        + (" [lower bound: some durations unknown]" if estimate["lowerBound"] else "")
    )
    print(f"Output: {Path(args.out).expanduser().resolve()}")

    if not args.execute:
        print("Plan only. No Run was created and no token was read.")
        print("Re-run with --execute to download these rows.")
        return 0

    # Plan mode deliberately reads no token; only an --execute run touches .env.
    load_env_file()
    token = os.getenv("APIFY_TOKEN")
    if not token or not token.strip():
        print("Configuration error: APIFY_TOKEN is required when --execute is used", file=sys.stderr)
        return 2

    stamp = datetime.now(timezone.utc).strftime("%Y%m%dT%H%M%SZ")
    result_path = _unique_path(
        Path(args.output).expanduser().resolve()
        if args.output
        else csv_path.parent / f"facebook-batch-download-{stamp}.json"
    )
    journal = RunJournal(result_path.parent / "python-download-journal.jsonl", lock=threading.Lock())

    try:
        results = run_batch(
            rows,
            token=token,
            out_dir=args.out,
            actor_id=args.actor_id,
            build=args.build,
            max_concurrency=args.max_concurrency,
            timeout=args.timeout,
            max_total_charge_usd=args.max_total_charge_usd,
            require_charge_cap=args.require_charge_cap,
            dataset_limit=args.dataset_limit,
            want_video=not args.no_video,
            want_audio=args.audio,
            audio_format=args.audio_format,
            force=args.force,
            journal=journal,
        )
    except ConfigurationError as exc:
        print(f"Configuration error: {exc}", file=sys.stderr)
        return 2

    counts = count_results(results)
    payload = sanitize(
        {
            "generatedAt": datetime.now(timezone.utc).isoformat(),
            "actorId": args.actor_id or os.getenv("APIFY_ACTOR_ID", "apple_yang/facebook-transcript-scraper-api"),
            "build": args.build,
            "source": str(csv_path),
            "outputDir": str(Path(args.out).expanduser().resolve()),
            "estimate": estimate,
            "counts": counts,
            "results": results,
        }
    )
    write_private(result_path, json.dumps(payload, ensure_ascii=False, indent=2) + "\n")
    print(f"Result: {result_path}")
    print("Counts: " + ", ".join(f"{name}={value}" for name, value in counts.items()))
    failed = [row["rowId"] for row in results if row.get("exitCode") != 0]
    if failed:
        print(f"Rows that did not finish cleanly: {', '.join(failed)}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
