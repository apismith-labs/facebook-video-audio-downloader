"""Cost model for the Facebook Video & Audio Downloader Actor.

One pay-per-event charge applies: ``apify-default-dataset-item`` at a flat price per dataset item.
A Run that writes an item is billed, including a Run whose item carries ``errMsg``.

Prices were captured on 2026-11-20 from the public Actor metadata. They are estimates, not a
quote: an Actor Run can be charged differently if pricing changes or if no item is written.
"""
from __future__ import annotations

from decimal import Decimal

RESULT_USD = Decimal("0.005")

PRICING_CAPTURED_AT = "2026-11-20"


def estimate_cost_usd(*, videos: int = 1) -> dict:
    """Estimate the USD cost of one or more single-video Runs."""
    if isinstance(videos, bool) or not isinstance(videos, int) or videos < 1:
        raise ValueError("videos must be a positive integer")

    total = RESULT_USD * videos
    return {
        "videos": videos,
        "resultUsd": float(RESULT_USD),
        "totalUsd": float(total),
        "pricingCapturedAt": PRICING_CAPTURED_AT,
        "note": "Estimate only. A Run is billed per dataset item it writes.",
    }


def format_estimate(estimate: dict) -> str:
    """Render a compact one-line summary for CLI output."""
    return (
        f"{estimate['videos']} video(s) -> ${estimate['totalUsd']:.4f} estimated "
        f"(${estimate['resultUsd']:.4f} per result)"
    )
